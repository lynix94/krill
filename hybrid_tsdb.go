package krill

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lynix/krill/storage"
	"github.com/lynix/krill/storage/badger"
	"github.com/lynix/krill/storage/memory"
	"github.com/lynix/krill/storage/persistence"
)

// HybridTSDB combines memory cache and persistent storage
// Recent data (default 2 hours) is cached in memory for fast access
type HybridTSDB struct {
	memoryCache      storage.Storage
	persistStorage   storage.Storage
	cacheDuration    time.Duration
	cleanupInterval  time.Duration
	serverStartTime  int64 // Unix timestamp when server started
	mu               sync.RWMutex
	stopCleanup      chan struct{}
	cleanupDone      chan struct{}
	metricsCache     []string      // cached GetMetrics() result
	metricsCacheTime time.Time     // last cache update time
	metricsCacheTTL  time.Duration // cache TTL (default 5 minutes)

	// Async write queue for performance
	writeQueue    chan []storage.DataPoint
	flushInterval time.Duration
	stopWriter    chan struct{}
	writerDone    chan struct{}
	asyncWrites   bool // Enable/disable async writes
}

// HybridOptions contains configuration for HybridTSDB
type HybridOptions struct {
	PersistencePath string        // Path for persistent storage
	CacheDuration   time.Duration // How long to keep data in memory cache (default: 2 hours)
	CleanupInterval time.Duration // How often to cleanup old cache data (default: 10 minutes)
	TTL             time.Duration // TTL for persistent storage (0 = no expiration)
	AsyncWrites     bool          // Enable async disk writes for better performance (default: true)
	FlushInterval   time.Duration // How often to flush async writes to disk (default: 5 seconds)
	WriteQueueSize  int           // Size of async write queue (default: 1000)
}

// NewHybridTSDB creates a new hybrid TSDB with memory cache and persistent storage
func NewHybridTSDB(opts HybridOptions) (*HybridTSDB, error) {
	// Set defaults
	if opts.CacheDuration == 0 {
		opts.CacheDuration = 2 * time.Hour
	}
	if opts.CleanupInterval == 0 {
		opts.CleanupInterval = 10 * time.Minute
	}
	if opts.PersistencePath == "" {
		opts.PersistencePath = "./krill_hybrid_data"
	}
	if opts.FlushInterval == 0 {
		opts.FlushInterval = 5 * time.Second
	}
	if opts.WriteQueueSize == 0 {
		opts.WriteQueueSize = 1000
	}
	// Async writes enabled by default for performance
	asyncWrites := true
	if !opts.AsyncWrites && opts.AsyncWrites { // Explicitly disabled
		asyncWrites = false
	}

	// Create memory cache
	memCache := memory.NewMemoryStorage()

	// Create persistent storage
	persistStore, err := persistence.NewPersistenceStorage(badger.BadgerOptions{
		Path: opts.PersistencePath,
		TTL:  opts.TTL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create persistent storage: %w", err)
	}

	h := &HybridTSDB{
		memoryCache:     memCache,
		persistStorage:  persistStore,
		cacheDuration:   opts.CacheDuration,
		cleanupInterval: opts.CleanupInterval,
		serverStartTime: time.Now().Unix(), // Record server start time
		stopCleanup:     make(chan struct{}),
		cleanupDone:     make(chan struct{}),
		metricsCacheTTL: 5 * time.Minute, // 5 minutes cache
		writeQueue:      make(chan []storage.DataPoint, opts.WriteQueueSize),
		flushInterval:   opts.FlushInterval,
		stopWriter:      make(chan struct{}),
		writerDone:      make(chan struct{}),
		asyncWrites:     asyncWrites,
	}

	// Start background cleanup goroutine
	go h.cleanupLoop()

	// Start async writer if enabled
	if h.asyncWrites {
		go h.asyncWriterLoop()
	}

	return h, nil
}

// TsdbPut stores a data point in both memory cache and persistent storage
func (h *HybridTSDB) TsdbPut(ts int64, metric string, value float64) error {
	// Write to memory cache first (fast path)
	if err := h.memoryCache.Put(ts, metric, value); err != nil {
		return fmt.Errorf("failed to write to memory cache: %w", err)
	}

	// Write to persistent storage (can be async in production)
	if err := h.persistStorage.Put(ts, metric, value); err != nil {
		return fmt.Errorf("failed to write to persistent storage: %w", err)
	}

	return nil
}

// TsdbPutBatch stores multiple data points efficiently
func (h *HybridTSDB) TsdbPutBatch(points []storage.DataPoint) error {
	// Write to memory cache first (fast path - always synchronous)
	if err := h.memoryCache.PutBatch(points); err != nil {
		return fmt.Errorf("failed to write batch to memory cache: %w", err)
	}

	// Write to persistent storage
	if h.asyncWrites {
		// Async mode: queue for background writer (non-blocking)
		select {
		case h.writeQueue <- points:
			// Successfully queued
		default:
			// Queue full - log warning but don't block
			// Data is still safe in memory cache
			fmt.Printf("[WARN] Write queue full (%d items), skipping disk write for %d points\n",
				len(h.writeQueue), len(points))
		}
		return nil
	} else {
		// Sync mode: write immediately (blocking)
		if err := h.persistStorage.PutBatch(points); err != nil {
			return fmt.Errorf("failed to write batch to persistent storage: %w", err)
		}
		return nil
	}
}

// PutLabels stores a data point with labels in both memory cache and persistent storage (dual write)
func (h *HybridTSDB) PutLabels(ts int64, labels storage.Labels, value float64) error {
	// Write to memory cache first (fast path)
	if err := h.memoryCache.PutLabels(ts, labels, value); err != nil {
		return fmt.Errorf("failed to write to memory cache: %w", err)
	}

	// Write to persistent storage for durability
	if err := h.persistStorage.PutLabels(ts, labels, value); err != nil {
		return fmt.Errorf("failed to write to persistent storage: %w", err)
	}

	// Invalidate metrics cache when new data is added
	h.mu.Lock()
	h.metricsCache = nil
	h.mu.Unlock()

	return nil
}

// Get retrieves data points, trying memory cache first, then persistent storage
func (h *HybridTSDB) Get(metric string, startTs, endTs int64) ([]int64, []float64, error) {
	if endTs == 0 {
		endTs = 1<<63 - 1 // Max int64
	}

	var allTimestamps []int64
	var allValues []float64

	// Calculate effective cache cutoff: max(now - cacheDuration, serverStartTime)
	// This ensures we query persistent storage for data before server restart
	now := time.Now().Unix()
	cacheCutoff := now - int64(h.cacheDuration.Seconds())

	// IMPORTANT: Use server start time as minimum cache cutoff
	// Before this time, memory cache has no data (server wasn't running)
	if h.serverStartTime > cacheCutoff {
		cacheCutoff = h.serverStartTime
	}

	// If query is entirely before server start, use persistent storage only
	if endTs < h.serverStartTime {
		return h.persistStorage.Get(metric, startTs, endTs)
	}

	// Query overlaps with cache range - check both storages
	// Get old data from persistent storage (before cache cutoff)
	if startTs < cacheCutoff {
		persistTimestamps, persistValues, err := h.persistStorage.Get(metric, startTs, cacheCutoff-1)
		if err != nil && !strings.Contains(err.Error(), "metric not found") {
			return nil, nil, fmt.Errorf("failed to query persistent storage: %w", err)
		}
		if len(persistTimestamps) > 0 {
			allTimestamps = append(allTimestamps, persistTimestamps...)
			allValues = append(allValues, persistValues...)
		}
	}

	// Get recent data from both memory cache AND persistent storage
	// Memory cache: data after server start
	// Persistent storage: all data (including before restart)
	cacheStart := cacheCutoff
	if startTs > cacheStart {
		cacheStart = startTs
	}

	// Get from persistent storage (includes data from before restart)
	persistRecentTs, persistRecentVals, err := h.persistStorage.Get(metric, cacheStart, endTs)
	if err == nil && len(persistRecentTs) > 0 {
		allTimestamps = append(allTimestamps, persistRecentTs...)
		allValues = append(allValues, persistRecentVals...)
	}

	// Also get from memory cache (includes data after restart)
	cacheTimestamps, cacheValues, err := h.memoryCache.Get(metric, cacheStart, endTs)
	if err == nil && len(cacheTimestamps) > 0 {
		allTimestamps = append(allTimestamps, cacheTimestamps...)
		allValues = append(allValues, cacheValues...)
	}

	if len(allTimestamps) == 0 {
		return nil, nil, fmt.Errorf("metric not found: %s", metric)
	}

	// Deduplicate and sort by timestamp
	allTimestamps, allValues = deduplicateTimeseries(allTimestamps, allValues)

	return allTimestamps, allValues, nil
}

// GetLabels retrieves data points by labels, trying memory cache first for recent data
func (h *HybridTSDB) GetLabels(labels storage.Labels, startTs, endTs int64) ([]int64, []float64, error) {
	if endTs == 0 {
		endTs = 1<<63 - 1 // Max int64
	}

	var allTimestamps []int64
	var allValues []float64

	// Calculate effective cache cutoff: max(now - cacheDuration, serverStartTime)
	// This ensures we query persistent storage for data before server restart
	now := time.Now().Unix()
	cacheCutoff := now - int64(h.cacheDuration.Seconds())

	// IMPORTANT: Use server start time as minimum cache cutoff
	// Before this time, memory cache has no data (server wasn't running)
	if h.serverStartTime > cacheCutoff {
		cacheCutoff = h.serverStartTime
	}

	// If query is entirely before server start, use persistent storage only
	if endTs < h.serverStartTime {
		return h.persistStorage.GetLabels(labels, startTs, endTs)
	}

	// Query overlaps with cache range - check both storages
	// Get old data from persistent storage (before cache cutoff)
	if startTs < cacheCutoff {
		persistTimestamps, persistValues, err := h.persistStorage.GetLabels(labels, startTs, cacheCutoff-1)
		if err != nil && !strings.Contains(err.Error(), "metric not found") && !strings.Contains(err.Error(), "series not found") {
			return nil, nil, fmt.Errorf("failed to query persistent storage: %w", err)
		}
		if len(persistTimestamps) > 0 {
			allTimestamps = append(allTimestamps, persistTimestamps...)
			allValues = append(allValues, persistValues...)
		}
	}

	// Get recent data from both memory cache AND persistent storage
	// Memory cache: data after server start
	// Persistent storage: all data (including before restart)
	cacheStart := cacheCutoff
	if startTs > cacheStart {
		cacheStart = startTs
	}

	// Get from persistent storage (includes data from before restart)
	persistRecentTs, persistRecentVals, err := h.persistStorage.GetLabels(labels, cacheStart, endTs)
	if err == nil && len(persistRecentTs) > 0 {
		allTimestamps = append(allTimestamps, persistRecentTs...)
		allValues = append(allValues, persistRecentVals...)
	}

	// Also get from memory cache (includes data after restart)
	cacheTimestamps, cacheValues, err := h.memoryCache.GetLabels(labels, cacheStart, endTs)
	if err == nil && len(cacheTimestamps) > 0 {
		allTimestamps = append(allTimestamps, cacheTimestamps...)
		allValues = append(allValues, cacheValues...)
	}

	if len(allTimestamps) == 0 {
		return nil, nil, fmt.Errorf("series not found: %s", labels.String())
	}

	// Deduplicate and sort by timestamp
	allTimestamps, allValues = deduplicateTimeseries(allTimestamps, allValues)

	return allTimestamps, allValues, nil
}

// GetMetrics returns all metric names from both cache and persistent storage
func (h *HybridTSDB) GetMetrics() ([]string, error) {
	// Check cache first
	h.mu.RLock()
	if h.metricsCache != nil && time.Since(h.metricsCacheTime) < h.metricsCacheTTL {
		result := make([]string, len(h.metricsCache))
		copy(result, h.metricsCache)
		h.mu.RUnlock()
		return result, nil
	}
	h.mu.RUnlock()

	// Cache miss or expired, rebuild
	metricsMap := make(map[string]bool)

	// Get from memory cache
	cacheMetrics, err := h.memoryCache.GetMetrics()
	if err != nil {
		return nil, err
	}
	for _, m := range cacheMetrics {
		metricsMap[m] = true
	}

	// Get from persistent storage
	persistMetrics, err := h.persistStorage.GetMetrics()
	if err != nil {
		return nil, err
	}
	for _, m := range persistMetrics {
		metricsMap[m] = true
	}

	// Convert to slice
	metrics := make([]string, 0, len(metricsMap))
	for m := range metricsMap {
		metrics = append(metrics, m)
	}

	// Update cache
	h.mu.Lock()
	h.metricsCache = metrics
	h.metricsCacheTime = time.Now()
	h.mu.Unlock()

	return metrics, nil
}

// GetAllSeries returns all series (labels) from both cache and persistent storage
func (h *HybridTSDB) GetAllSeries() ([]storage.Labels, error) {
	seriesMap := make(map[uint64]storage.Labels)

	// Get from memory cache
	cacheSeries, err := h.memoryCache.GetAllSeries()
	if err != nil {
		return nil, fmt.Errorf("failed to get cache series: %w", err)
	}
	for _, labels := range cacheSeries {
		seriesMap[labels.Hash()] = labels
	}

	// Get from persistent storage
	persistSeries, err := h.persistStorage.GetAllSeries()
	if err != nil {
		return nil, fmt.Errorf("failed to get persistent series: %w", err)
	}
	for _, labels := range persistSeries {
		seriesMap[labels.Hash()] = labels
	}

	// Convert to slice
	series := make([]storage.Labels, 0, len(seriesMap))
	for _, labels := range seriesMap {
		series = append(series, labels)
	}

	return series, nil
}

// FindSeriesByLabels finds series IDs matching the given label matchers using inverted index
// Delegates to persistent storage which has the inverted index
func (h *HybridTSDB) FindSeriesByLabels(labelMatchers map[string]string) []uint64 {
	// Type assert to get access to index methods
	type IndexFinder interface {
		FindSeriesByLabels(map[string]string) []uint64
	}

	if finder, ok := h.persistStorage.(IndexFinder); ok {
		return finder.FindSeriesByLabels(labelMatchers)
	}

	// Fallback: return empty (will trigger full scan)
	return []uint64{}
}

// GetLabelsForSeriesID retrieves labels for a given series ID
// Delegates to persistent storage which has the series ID mapping
func (h *HybridTSDB) GetLabelsForSeriesID(seriesID uint64) (storage.Labels, bool) {
	// Type assert to get access to index methods
	type LabelsGetter interface {
		GetLabelsForSeriesID(uint64) (storage.Labels, bool)
	}

	if getter, ok := h.persistStorage.(LabelsGetter); ok {
		return getter.GetLabelsForSeriesID(seriesID)
	}

	// Fallback: not found
	return nil, false
}

// Close closes both storage backends and stops cleanup
func (h *HybridTSDB) Close() error {
	// Stop cleanup goroutine
	close(h.stopCleanup)
	<-h.cleanupDone

	// Stop async writer if running
	if h.asyncWrites {
		close(h.stopWriter)
		<-h.writerDone
	}

	var errs []error

	if err := h.memoryCache.Close(); err != nil {
		errs = append(errs, fmt.Errorf("memory cache close error: %w", err))
	}

	if err := h.persistStorage.Close(); err != nil {
		errs = append(errs, fmt.Errorf("persistent storage close error: %w", err))
	}

	if len(errs) > 0 {
		return fmt.Errorf("close errors: %v", errs)
	}

	return nil
}

// asyncWriterLoop runs in background to flush write queue to disk periodically
func (h *HybridTSDB) asyncWriterLoop() {
	defer close(h.writerDone)

	ticker := time.NewTicker(h.flushInterval)
	defer ticker.Stop()

	// Accumulate points for batch writing
	batch := make([]storage.DataPoint, 0, 10000)
	var batchCount int
	var totalFlushed int64

	flushBatch := func() {
		if len(batch) == 0 {
			return
		}

		startTime := time.Now()
		if err := h.persistStorage.PutBatch(batch); err != nil {
			fmt.Printf("[ERROR] Failed to flush batch (%d points): %v\n", len(batch), err)
		} else {
			batchCount++
			totalFlushed += int64(len(batch))
			elapsed := time.Since(startTime)
			if batchCount%10 == 0 { // Log every 10 flushes
				fmt.Printf("[INFO] Async writer: flushed batch #%d (%d points, %v, total: %d)\n",
					batchCount, len(batch), elapsed, totalFlushed)
			}
		}

		batch = batch[:0] // Reuse slice
	}

	for {
		select {
		case points := <-h.writeQueue:
			batch = append(batch, points...)

			// Flush immediately if batch is large enough
			if len(batch) >= 10000 {
				flushBatch()
			}

		case <-ticker.C:
			// Periodic flush
			flushBatch()

		case <-h.stopWriter:
			// Final flush before shutdown
			fmt.Println("[INFO] Async writer shutting down, flushing remaining data...")

			// Drain queue
			for {
				select {
				case points := <-h.writeQueue:
					batch = append(batch, points...)
				default:
					goto done
				}
			}
		done:
			flushBatch()
			fmt.Printf("[INFO] Async writer stopped (total flushed: %d points)\n", totalFlushed)
			return
		}
	}
}

// cleanupLoop periodically removes old data from memory cache
func (h *HybridTSDB) cleanupLoop() {
	defer close(h.cleanupDone)

	ticker := time.NewTicker(h.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			h.cleanupOldCache()
		case <-h.stopCleanup:
			return
		}
	}
}

// cleanupOldCache removes data older than cache duration from memory
func (h *HybridTSDB) cleanupOldCache() {
	now := time.Now().Unix()
	cutoff := now - int64(h.cacheDuration.Seconds())

	// Type assertion to access memory-specific methods
	if memStorage, ok := h.memoryCache.(*memory.MemoryStorage); ok {
		memStorage.DeleteOlderThan(cutoff)
	}
}

// RunGC runs garbage collection on persistent storage
func (h *HybridTSDB) RunGC() error {
	if ps, ok := h.persistStorage.(*persistence.PersistenceStorage); ok {
		return ps.RunGC()
	}
	return nil
}

// deduplicateTimeseries removes duplicate timestamps and sorts the result
func deduplicateTimeseries(timestamps []int64, values []float64) ([]int64, []float64) {
	if len(timestamps) == 0 {
		return timestamps, values
	}

	// Create a map of timestamp -> value (later values overwrite earlier ones)
	tsMap := make(map[int64]float64, len(timestamps))
	for i, ts := range timestamps {
		tsMap[ts] = values[i]
	}

	// Sort timestamps
	sortedTs := make([]int64, 0, len(tsMap))
	for ts := range tsMap {
		sortedTs = append(sortedTs, ts)
	}

	// Sort using a simple bubble sort for small datasets or use sort package
	for i := 0; i < len(sortedTs)-1; i++ {
		for j := i + 1; j < len(sortedTs); j++ {
			if sortedTs[i] > sortedTs[j] {
				sortedTs[i], sortedTs[j] = sortedTs[j], sortedTs[i]
			}
		}
	}

	// Extract sorted values
	sortedVals := make([]float64, len(sortedTs))
	for i, ts := range sortedTs {
		sortedVals[i] = tsMap[ts]
	}

	return sortedTs, sortedVals
}
