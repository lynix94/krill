package krill

import (
	"fmt"
	"log"
	"strings"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/lynix/krill/storage/badger"
)

// DownsamplingLevel represents a single downsampling configuration
type DownsamplingLevel struct {
	Name         string
	Interval     time.Duration
	Retention    time.Duration
	Storage      TimeSeriesDB
	LastRun      time.Time
	mu           sync.Mutex
}

// DownsamplingManager manages multiple downsampling levels
type DownsamplingManager struct {
	rawStorage   QueryableDB
	memoryCache  QueryableDB // Direct access to memory cache for fast reads
	levels       []*DownsamplingLevel
	stopCh       chan struct{}
	wg           sync.WaitGroup
}

// AggregatedData holds downsampled metrics
type AggregatedData struct {
	Avg   float64
	Min   float64
	Max   float64
	Count int64
}

// NewDownsamplingManager creates a new downsampling manager
// If memoryCache is provided, it will be used for fast data reads instead of disk
func NewDownsamplingManager(rawStorage QueryableDB, memoryCache QueryableDB) *DownsamplingManager {
	return &DownsamplingManager{
		rawStorage:  rawStorage,
		memoryCache: memoryCache,
		levels:      make([]*DownsamplingLevel, 0),
		stopCh:      make(chan struct{}),
	}
}

// AddLevel adds a downsampling level
func (dm *DownsamplingManager) AddLevel(name string, interval, retention time.Duration, storage TimeSeriesDB) error {
	if interval == 0 {
		return fmt.Errorf("interval must be greater than 0 for downsampling level '%s'", name)
	}
	
	level := &DownsamplingLevel{
		Name:      name,
		Interval:  interval,
		Retention: retention,
		Storage:   storage,
		LastRun:   time.Now().Add(-interval), // Run immediately on first tick
	}
	
	dm.levels = append(dm.levels, level)
	log.Printf("Added downsampling level: %s (interval=%v, retention=%v)", name, interval, retention)
	return nil
}

// Start begins the downsampling process
func (dm *DownsamplingManager) Start() {
	if len(dm.levels) == 0 {
		log.Println("[DownsamplingManager] No downsampling levels configured, skipping downsampling")
		return
	}
	
	log.Printf("[DownsamplingManager] Starting downsampling manager with %d levels", len(dm.levels))
	
	for i, level := range dm.levels {
		log.Printf("[DownsamplingManager] Starting goroutine for level %d: %s", i+1, level.Name)
		dm.wg.Add(1)
		go dm.runLevel(level)
	}
	
	log.Printf("[DownsamplingManager] All %d downsampling goroutines started", len(dm.levels))
}

// Stop stops the downsampling process
func (dm *DownsamplingManager) Stop() {
	close(dm.stopCh)
	dm.wg.Wait()
	
	// Close all level storages
	for _, level := range dm.levels {
		if level.Storage != nil {
			level.Storage.Close()
		}
	}
	
	log.Println("Downsampling manager stopped")
}

// runLevel runs downsampling for a specific level
func (dm *DownsamplingManager) runLevel(level *DownsamplingLevel) {
	defer dm.wg.Done()
	
	ticker := time.NewTicker(level.Interval)
	defer ticker.Stop()
	
	log.Printf("Downsampling level '%s' started (interval=%v)", level.Name, level.Interval)
	
	for {
		select {
		case <-dm.stopCh:
			return
		case <-ticker.C:
			level.mu.Lock()
			if err := dm.downsample(level); err != nil {
				log.Printf("Error downsampling level '%s': %v", level.Name, err)
			}
			level.LastRun = time.Now()
			level.mu.Unlock()
		}
	}
}

// downsample performs downsampling for a specific level
func (dm *DownsamplingManager) downsample(level *DownsamplingLevel) error {
	now := time.Now()
	startTime := level.LastRun
	endTime := now
	
	log.Printf("Downsampling '%s': processing range %v to %v", level.Name, startTime, endTime)
	
	// Get metrics from memory cache if available (raw metrics only)
	// If memoryCache is not available, fall back to rawStorage
	var metrics []string
	var err error
	
	if dm.memoryCache != nil {
		metrics, err = dm.memoryCache.GetMetrics()
	} else {
		metrics, err = dm.rawStorage.GetMetrics()
	}
	
	if err != nil {
		return fmt.Errorf("failed to get metrics: %w", err)
	}
	if len(metrics) == 0 {
		log.Printf("No metrics found for downsampling '%s'", level.Name)
		return nil
	}
	
	// Filter out already downsampled metrics (those with _avg, _min, _max, _count suffixes)
	// This is critical to prevent exponential growth
	rawMetrics := make([]string, 0, len(metrics))
	for _, metric := range metrics {
		if !strings.HasSuffix(metric, "_avg") && 
		   !strings.HasSuffix(metric, "_min") && 
		   !strings.HasSuffix(metric, "_max") && 
		   !strings.HasSuffix(metric, "_count") {
			rawMetrics = append(rawMetrics, metric)
		}
	}
	
	log.Printf("Downsampling '%s': processing %d raw metrics (filtered from %d total)", level.Name, len(rawMetrics), len(metrics))
	
	// Early exit if no raw metrics (all filtered out)
	if len(rawMetrics) == 0 {
		log.Printf("Downsampling '%s': no raw metrics to process (all filtered)", level.Name)
		return nil
	}
	metrics = nil // Release memory
	
	// Process in batches to limit memory usage
	processedCount := 0
	skippedCount := 0
	batchSize := 1000
	
	for i := 0; i < len(rawMetrics); i += batchSize {
		end := i + batchSize
		if end > len(rawMetrics) {
			end = len(rawMetrics)
		}
		batch := rawMetrics[i:end]
		
		for _, metric := range batch {
			if err := dm.downsampleMetric(level, metric, startTime, endTime); err != nil {
				// Only log non-"not found" errors to reduce noise
				if err.Error() != "metric not found" && !strings.Contains(err.Error(), "metric not found") {
					log.Printf("Error downsampling metric '%s' for level '%s': %v", metric, level.Name, err)
				}
				skippedCount++
				continue
			}
			processedCount++
		}
		
		// Log progress for large datasets
		if len(rawMetrics) > 10000 && (end%10000 == 0 || end == len(rawMetrics)) {
			log.Printf("Downsampling '%s': progress %d/%d metrics", level.Name, end, len(rawMetrics))
		}
	}
	
	if skippedCount > 0 {
		log.Printf("Downsampling '%s': completed %d/%d metrics (skipped %d)", level.Name, processedCount, len(metrics), skippedCount)
	} else {
		log.Printf("Downsampling '%s': completed %d/%d metrics", level.Name, processedCount, len(metrics))
	}
	return nil
}

// downsampleMetric downsamples a single metric
func (dm *DownsamplingManager) downsampleMetric(level *DownsamplingLevel, metric string, startTime, endTime time.Time) error {
	// Get raw data points from memory cache first (much faster than disk)
	var timestamps []int64
	var values []float64
	var err error
	
	if dm.memoryCache != nil {
		timestamps, values, err = dm.memoryCache.Get(metric, startTime.Unix(), endTime.Unix())
	} else {
		timestamps, values, err = dm.rawStorage.Get(metric, startTime.Unix(), endTime.Unix())
	}
	
	if err != nil {
		return fmt.Errorf("failed to get raw data: %w", err)
	}

	if len(timestamps) == 0 {
		return nil // No data to downsample
	}
	
	// Aggregate data by interval
	buckets := dm.aggregateByInterval(timestamps, values, level.Interval)
	
	// Release memory immediately after aggregation
	timestamps = nil
	values = nil
	
	// Store aggregated data in ascending time order
	bucketTimes := make([]int64, 0, len(buckets))
	for bucketTime := range buckets {
		bucketTimes = append(bucketTimes, bucketTime)
	}
	sort.Slice(bucketTimes, func(i, j int) bool { return bucketTimes[i] < bucketTimes[j] })

	for _, bucketTime := range bucketTimes {
		aggData := buckets[bucketTime]
		if err := level.Storage.TsdbPut(bucketTime, metric+"_avg", aggData.Avg); err != nil {
			return fmt.Errorf("failed to store avg: %w", err)
		}
		if err := level.Storage.TsdbPut(bucketTime, metric+"_min", aggData.Min); err != nil {
			return fmt.Errorf("failed to store min: %w", err)
		}
		if err := level.Storage.TsdbPut(bucketTime, metric+"_max", aggData.Max); err != nil {
			return fmt.Errorf("failed to store max: %w", err)
		}
		if err := level.Storage.TsdbPut(bucketTime, metric+"_count", float64(aggData.Count)); err != nil {
			return fmt.Errorf("failed to store count: %w", err)
		}
	}
	
	return nil
}

// aggregateByInterval aggregates data points into buckets
func (dm *DownsamplingManager) aggregateByInterval(timestamps []int64, values []float64, interval time.Duration) map[int64]AggregatedData {
	buckets := make(map[int64]AggregatedData)
	intervalSeconds := int64(interval.Seconds())

	for i, ts := range timestamps {
		bucketTime := (ts / intervalSeconds) * intervalSeconds
		
		agg, exists := buckets[bucketTime]
		if !exists {
			agg = AggregatedData{
				Min: values[i],
				Max: values[i],
			}
		}
		
		// Update aggregates
		agg.Count++
		agg.Avg += (values[i] - agg.Avg) / float64(agg.Count) // Running average
		agg.Min = math.Min(agg.Min, values[i])
		agg.Max = math.Max(agg.Max, values[i])
		
		buckets[bucketTime] = agg
	}
	
	return buckets
}

// CreateDownsamplingStorage creates a BadgerTSDB instance for downsampling
func CreateDownsamplingStorage(path string, partitions, chunkSize int, bucketSize int64, retention time.Duration) (TimeSeriesDB, error) {
	opts := badger.BadgerOptions{
		Path:       path,
		TTL:        retention,
		ChunkSize:  chunkSize,
		Partitions: partitions,
		BucketSize: bucketSize,
	}
	
	if partitions > 0 {
		return badger.NewPartitionedBadgerTSDB(opts, partitions)
	}
	
	return badger.NewBadgerTSDB(opts)
}
