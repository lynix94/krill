package krill

import (
	"fmt"
	"sync"
	"time"

	"github.com/lynix/krill/storage"
	"github.com/lynix/krill/storage/memory"
	"github.com/lynix/krill/storage/persistence"
	"github.com/lynix/krill/storage/badger"
)

// HybridTSDB combines memory cache and persistent storage
// Recent data (default 2 hours) is cached in memory for fast access
type HybridTSDB struct {
	memoryCache      storage.Storage
	persistStorage   storage.Storage
	cacheDuration    time.Duration
	cleanupInterval  time.Duration
	mu               sync.RWMutex
	stopCleanup      chan struct{}
	cleanupDone      chan struct{}
}

// HybridOptions contains configuration for HybridTSDB
type HybridOptions struct {
	PersistencePath string        // Path for persistent storage
	CacheDuration   time.Duration // How long to keep data in memory cache (default: 2 hours)
	CleanupInterval time.Duration // How often to cleanup old cache data (default: 10 minutes)
	TTL             time.Duration // TTL for persistent storage (0 = no expiration)
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
		stopCleanup:     make(chan struct{}),
		cleanupDone:     make(chan struct{}),
	}

	// Start background cleanup goroutine
	go h.cleanupLoop()

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

// Get retrieves data points, trying memory cache first, then persistent storage
func (h *HybridTSDB) Get(metric string, startTs, endTs int64) ([]int64, []float64, error) {
	if endTs == 0 {
		endTs = 1<<63 - 1 // Max int64
	}

	var allTimestamps []int64
	var allValues []float64

	// Calculate cache cutoff (e.g., 2 hours ago)
	now := time.Now().Unix()
	cacheCutoff := now - int64(h.cacheDuration.Seconds())

	// If query is entirely within cache range, use cache only
	if startTs >= cacheCutoff {
		return h.memoryCache.Get(metric, startTs, endTs)
	}

	// Query needs data from persistent storage
	persistTimestamps, persistValues, err := h.persistStorage.Get(metric, startTs, cacheCutoff-1)
	if err != nil && err.Error() != fmt.Sprintf("metric not found: %s", metric) {
		return nil, nil, fmt.Errorf("failed to query persistent storage: %w", err)
	}

	if len(persistTimestamps) > 0 {
		allTimestamps = append(allTimestamps, persistTimestamps...)
		allValues = append(allValues, persistValues...)
	}

	// Get recent data from memory cache
	cacheTimestamps, cacheValues, err := h.memoryCache.Get(metric, cacheCutoff, endTs)
	if err != nil && err.Error() != fmt.Sprintf("metric not found: %s", metric) {
		// If not in cache either, return error
		if len(allTimestamps) == 0 {
			return nil, nil, fmt.Errorf("metric not found: %s", metric)
		}
	}

	if len(cacheTimestamps) > 0 {
		allTimestamps = append(allTimestamps, cacheTimestamps...)
		allValues = append(allValues, cacheValues...)
	}

	if len(allTimestamps) == 0 {
		return nil, nil, fmt.Errorf("metric not found: %s", metric)
	}

	return allTimestamps, allValues, nil
}

// GetMetrics returns all metric names from both cache and persistent storage
func (h *HybridTSDB) GetMetrics() ([]string, error) {
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

	return metrics, nil
}

// Close closes both storage backends and stops cleanup
func (h *HybridTSDB) Close() error {
	// Stop cleanup goroutine
	close(h.stopCleanup)
	<-h.cleanupDone

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
