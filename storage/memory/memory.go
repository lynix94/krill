package memory

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/lynix/krill/storage"
	"github.com/lynix/krill/storage/gorilla"
)

// MemoryStorage implements in-memory storage using Gorilla compression with configurable buckets
type MemoryStorage struct {
	mu      sync.RWMutex
	buckets map[bucketKey]*MetricSeries // (seriesID, bucket) -> series
	labels  map[uint64]storage.Labels   // seriesID -> labels
	
	bucketSize int64 // Bucket size in seconds (default: 3600 = 1 hour)
	
	// Cache for GetMetrics to avoid repeated formatting
	metricsCache     []string
	metricsCacheTime time.Time
	metricsCacheMu   sync.RWMutex
}

// bucketKey identifies a series bucket
type bucketKey struct {
	seriesID uint64
	bucket   int64 // Unix timestamp rounded to bucketSize
}

// MetricSeries stores compressed time-series data for a single metric bucket
type MetricSeries struct {
	mu              sync.Mutex
	id              uint64
	bucket          int64 // Bucket start timestamp
	firstTimestamp  int64
	lastTimestamp   int64
	firstValue      float64
	lastValue       float64
	timestampStream *gorilla.TimestampEncoder
	valueStream     *gorilla.ValueEncoder
	count           int
}

// NewMemoryStorage creates a new in-memory storage with default 1-hour buckets
func NewMemoryStorage() *MemoryStorage {
	return NewMemoryStorageWithBucketSize(3600)
}

// NewMemoryStorageWithBucketSize creates a new in-memory storage with custom bucket size
func NewMemoryStorageWithBucketSize(bucketSize int64) *MemoryStorage {
	if bucketSize <= 0 {
		bucketSize = 3600 // Default to 1 hour
	}
	fmt.Printf("[MemoryStorage] Initialized with bucketSize=%d seconds (%.1f hours)\n", bucketSize, float64(bucketSize)/3600.0)
	return &MemoryStorage{
		buckets:    make(map[bucketKey]*MetricSeries),
		labels:     make(map[uint64]storage.Labels),
		bucketSize: bucketSize,
	}
}

// Put stores a time-series data point with Gorilla compression
// This is a legacy wrapper that converts string metric to Labels
func (ms *MemoryStorage) Put(ts int64, metric string, value float64) error {
	// Parse metric string to extract name and tags
	name, tags := parseMetricString(metric)
	labels := storage.LabelsFromMap(name, tags)
	return ms.PutLabels(ts, labels, value)
}

// PutBatch stores multiple time-series data points efficiently with 1-hour buckets
func (ms *MemoryStorage) PutBatch(points []storage.DataPoint) error {
	// Group points by (seriesID, bucket)
	type pointData struct {
		timestamp int64
		value     float64
		labels    storage.Labels
	}
	bucketPoints := make(map[bucketKey][]pointData)
	
	for i := range points {
		seriesID := points[i].Labels.Hash()
		bucket := points[i].Timestamp / ms.bucketSize * ms.bucketSize
		bkey := bucketKey{seriesID: seriesID, bucket: bucket}
		bucketPoints[bkey] = append(bucketPoints[bkey], pointData{
			timestamp: points[i].Timestamp,
			value:     points[i].Value,
			labels:    points[i].Labels.Copy(), // Deep copy labels
		})
	}
	
	// Process each bucket
	for bkey, pointsData := range bucketPoints {
		ms.mu.Lock()
		series, exists := ms.buckets[bkey]
		if !exists {
			series = &MetricSeries{
				id:              bkey.seriesID,
				bucket:          bkey.bucket,
				timestampStream: gorilla.NewTimestampEncoder(),
				valueStream:     gorilla.NewValueEncoder(),
			}
			ms.buckets[bkey] = series
			ms.labels[bkey.seriesID] = pointsData[0].labels
		}
		ms.mu.Unlock()
		
		// Write all points for this bucket
		series.mu.Lock()
		for _, pd := range pointsData {
			if series.count == 0 {
				series.firstTimestamp = pd.timestamp
				series.lastTimestamp = pd.timestamp
				series.firstValue = pd.value
				series.lastValue = pd.value
				series.count++
				continue
			}
			
			if pd.timestamp < series.lastTimestamp {
				continue // Skip old data
			}
			if pd.timestamp == series.lastTimestamp {
				continue // Skip duplicate
			}
			
			delta := pd.timestamp - series.lastTimestamp
			series.timestampStream.Encode(delta)
			series.valueStream.Encode(pd.value, series.lastValue)
			
			series.lastTimestamp = pd.timestamp
			series.lastValue = pd.value
			series.count++
		}
		series.mu.Unlock()
	}
	
	// Invalidate metrics cache
	ms.metricsCacheMu.Lock()
	ms.metricsCache = nil
	ms.metricsCacheMu.Unlock()
	
	return nil
}

// PutLabels stores a time-series data point using Labels with configurable buckets
func (ms *MemoryStorage) PutLabels(ts int64, labels storage.Labels, value float64) error {
	seriesID := labels.Hash()
	bucket := ts / ms.bucketSize * ms.bucketSize
	bkey := bucketKey{seriesID: seriesID, bucket: bucket}
	
	ms.mu.Lock()
	series, exists := ms.buckets[bkey]
	if !exists {
		series = &MetricSeries{
			id:              seriesID,
			bucket:          bucket,
			timestampStream: gorilla.NewTimestampEncoder(),
			valueStream:     gorilla.NewValueEncoder(),
		}
		ms.buckets[bkey] = series
		ms.labels[seriesID] = labels.Copy()
		
		// Invalidate metrics cache when new series added
		ms.metricsCacheMu.Lock()
		ms.metricsCache = nil
		ms.metricsCacheMu.Unlock()
	}
	ms.mu.Unlock()

	series.mu.Lock()
	defer series.mu.Unlock()

	// First data point
	if series.count == 0 {
		series.firstTimestamp = ts
		series.lastTimestamp = ts
		series.firstValue = value
		series.lastValue = value
		series.count++
		return nil
	}

	// Skip if timestamp is older than last timestamp
	if ts < series.lastTimestamp {
		return fmt.Errorf("timestamp must not be older than last timestamp: %d < %d", ts, series.lastTimestamp)
	}

	// If timestamp is the same as last, silently ignore (idempotent write)
	// This allows multiple metrics with same timestamp during bulk scraping
	if ts == series.lastTimestamp {
		return nil
	}

	// Compress timestamp delta
	delta := ts - series.lastTimestamp
	series.timestampStream.Encode(delta)

	// Compress value using XOR encoding
	series.valueStream.Encode(value, series.lastValue)

	series.lastTimestamp = ts
	series.lastValue = value
	series.count++

	return nil
}

// Get retrieves all data points for a metric within a time range
// This is a legacy wrapper
func (ms *MemoryStorage) Get(metric string, startTs, endTs int64) ([]int64, []float64, error) {
	name, tags := parseMetricString(metric)
	labels := storage.LabelsFromMap(name, tags)
	return ms.GetLabels(labels, startTs, endTs)
}

// GetLabels retrieves all data points for labels within a time range across all buckets
func (ms *MemoryStorage) GetLabels(labels storage.Labels, startTs, endTs int64) ([]int64, []float64, error) {
	seriesID := labels.Hash()
	
	// Calculate bucket range
	startBucket := startTs / ms.bucketSize * ms.bucketSize
	endBucket := endTs / ms.bucketSize * ms.bucketSize
	
	allTimestamps := []int64{}
	allValues := []float64{}
	
	// Iterate through all relevant buckets
	for bucket := startBucket; bucket <= endBucket; bucket += ms.bucketSize {
		bkey := bucketKey{seriesID: seriesID, bucket: bucket}
		
		ms.mu.RLock()
		series, exists := ms.buckets[bkey]
		ms.mu.RUnlock()

		if !exists || series.count == 0 {
			continue
		}

		series.mu.Lock()
		
		timestamps := make([]int64, 0, series.count)
		values := make([]float64, 0, series.count)

		// First value
		timestamps = append(timestamps, series.firstTimestamp)
		values = append(values, series.firstValue)

		if series.count > 1 {
			// Decode remaining values
			timestampDecoder := gorilla.NewTimestampDecoder(series.timestampStream.Bytes())
			valueDecoder := gorilla.NewValueDecoder(series.valueStream.Bytes())

			currentTimestamp := series.firstTimestamp
			currentValue := series.firstValue

			for i := 1; i < series.count; i++ {
				delta, err := timestampDecoder.Decode()
				if err != nil {
					series.mu.Unlock()
					return nil, nil, fmt.Errorf("failed to decode timestamp: %v", err)
				}
				currentTimestamp += delta
				timestamps = append(timestamps, currentTimestamp)

				value, err := valueDecoder.Decode(currentValue)
				if err != nil {
					series.mu.Unlock()
					return nil, nil, fmt.Errorf("failed to decode value: %v", err)
				}
				currentValue = value
				values = append(values, value)
			}
		}
		
		series.mu.Unlock()
		
		// Filter by time range
		for i, ts := range timestamps {
			if ts >= startTs && ts <= endTs {
				allTimestamps = append(allTimestamps, ts)
				allValues = append(allValues, values[i])
			}
		}
	}

	if len(allTimestamps) == 0 {
		return []int64{}, []float64{}, nil
	}

	return allTimestamps, allValues, nil
}

// GetSerializedBlock returns a serialized block for a series bucket (for direct BadgerDB write)
// With bucket-based storage, this simply serializes the exact bucket data
func (ms *MemoryStorage) GetSerializedBlock(seriesID uint64, bucketStart int64) []byte {
	bkey := bucketKey{seriesID: seriesID, bucket: bucketStart}
	
	ms.mu.RLock()
	series, exists := ms.buckets[bkey]
	ms.mu.RUnlock()

	if !exists || series.count == 0 {
		return nil
	}

	series.mu.Lock()
	defer series.mu.Unlock()

	// Serialize the series block in BadgerDB format (MUST use LittleEndian!)
	// Since memory cache uses same bucket size, data is already aligned
	buf := new(bytes.Buffer)
	
	// Write header - Note: BadgerDB uses LittleEndian, not BigEndian!
	binary.Write(buf, binary.LittleEndian, bucketStart)      // StartTimestamp
	binary.Write(buf, binary.LittleEndian, int32(series.count))
	binary.Write(buf, binary.LittleEndian, series.firstTimestamp)
	binary.Write(buf, binary.LittleEndian, series.lastTimestamp)
	binary.Write(buf, binary.LittleEndian, series.firstValue)
	binary.Write(buf, binary.LittleEndian, series.lastValue)
	
	// Write compressed data
	timestampBytes := series.timestampStream.Bytes()
	valueBytes := series.valueStream.Bytes()
	
	binary.Write(buf, binary.LittleEndian, int32(len(timestampBytes)))
	buf.Write(timestampBytes)
	binary.Write(buf, binary.LittleEndian, int32(len(valueBytes)))
	buf.Write(valueBytes)
	
	return buf.Bytes()
}

// GetMetrics returns all metric names (as formatted strings for backward compatibility)
func (ms *MemoryStorage) GetMetrics() ([]string, error) {
	// Check cache first (5 minute TTL)
	ms.metricsCacheMu.RLock()
	if ms.metricsCache != nil && time.Since(ms.metricsCacheTime) < 5*time.Minute {
		result := make([]string, len(ms.metricsCache))
		copy(result, ms.metricsCache)
		ms.metricsCacheMu.RUnlock()
		return result, nil
	}
	ms.metricsCacheMu.RUnlock()

	// Cache miss or expired, rebuild
	ms.mu.RLock()
	metrics := make([]string, 0, len(ms.labels))
	for seriesID := range ms.labels {
		labels := ms.labels[seriesID]
		metrics = append(metrics, formatLabelsAsMetricString(labels))
	}
	ms.mu.RUnlock()
	
	// Update cache
	ms.metricsCacheMu.Lock()
	ms.metricsCache = metrics
	ms.metricsCacheTime = time.Now()
	ms.metricsCacheMu.Unlock()
	
	return metrics, nil
}

// GetAllSeries returns all series with their labels
func (ms *MemoryStorage) GetAllSeries() ([]storage.Labels, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	result := make([]storage.Labels, 0, len(ms.labels))
	for _, labels := range ms.labels {
		result = append(result, labels.Copy())
	}
	return result, nil
}

// Close closes the storage (no-op for memory storage)
func (ms *MemoryStorage) Close() error {
	return nil
}

// DeleteOlderThan removes buckets older than the specified timestamp
func (ms *MemoryStorage) DeleteOlderThan(cutoffTs int64) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	cutoffBucket := cutoffTs / ms.bucketSize * ms.bucketSize
	toDelete := make([]bucketKey, 0)
	
	for bkey, series := range ms.buckets {
		series.mu.Lock()
		// Delete entire bucket if it's older than cutoff
		if bkey.bucket < cutoffBucket {
			toDelete = append(toDelete, bkey)
		}
		series.mu.Unlock()
	}

	for _, bkey := range toDelete {
		delete(ms.buckets, bkey)
	}

	if len(toDelete) > 0 {
		fmt.Printf("Cleaned up %d buckets from memory cache (older than %d)\n", len(toDelete), cutoffTs)
	}

	return nil
}

// parseMetricString parses a metric string like "name{tag1=\"v1\",tag2=\"v2\"}"
// Returns metric name and tags map
func parseMetricString(metric string) (string, map[string]string) {
	tags := make(map[string]string)
	
	// Find { position
	bracePos := -1
	for i, c := range metric {
		if c == '{' {
			bracePos = i
			break
		}
	}
	
	if bracePos < 0 {
		return metric, tags
	}
	
	name := metric[:bracePos]
	
	// Find } position
	endPos := len(metric)
	for i := bracePos; i < len(metric); i++ {
		if metric[i] == '}' {
			endPos = i
			break
		}
	}
	
	// Parse tags
	tagStr := metric[bracePos+1 : endPos]
	if tagStr == "" {
		return name, tags
	}
	
	// Simple parser
	pairs := splitTags(tagStr)
	for _, pair := range pairs {
		kv := splitKeyValue(pair)
		if len(kv) == 2 {
			key := trim(kv[0])
			value := trimQuotes(trim(kv[1]))
			tags[key] = value
		}
	}
	
	return name, tags
}

// formatLabelsAsMetricString formats labels as "name{tag1=\"v1\",tag2=\"v2\"}"
func formatLabelsAsMetricString(labels storage.Labels) string {
	name := labels.Get("__name__")
	if name == "" {
		name = "unknown"
	}
	
	tags := labels.WithoutName()
	if len(tags) == 0 {
		return name
	}
	
	return name + tags.String()
}

func splitTags(s string) []string {
	result := make([]string, 0)
	current := ""
	inQuote := false
	
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' {
			inQuote = !inQuote
			current += string(c)
		} else if c == ',' && !inQuote {
			if current != "" {
				result = append(result, current)
				current = ""
			}
		} else {
			current += string(c)
		}
	}
	
	if current != "" {
		result = append(result, current)
	}
	
	return result
}

func splitKeyValue(s string) []string {
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			return []string{s[:i], s[i+1:]}
		}
	}
	return []string{s}
}

func trim(s string) string {
	start := 0
	end := len(s)
	
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	
	return s[start:end]
}

func trimQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// GetSeries returns the metric series for testing purposes (returns first found bucket)
func (ms *MemoryStorage) GetSeries(metric string) *PublicMetricSeries {
	name, tags := parseMetricString(metric)
	labels := storage.LabelsFromMap(name, tags)
	seriesID := labels.Hash()
	
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	
	// Find first bucket for this series
	for bkey, series := range ms.buckets {
		if bkey.seriesID == seriesID {
			series.mu.Lock()
			defer series.mu.Unlock()
			
			return &PublicMetricSeries{
				Name:            formatLabelsAsMetricString(labels),
				FirstTimestamp:  series.firstTimestamp,
				LastTimestamp:   series.lastTimestamp,
				FirstValue:      series.firstValue,
				LastValue:       series.lastValue,
				TimestampStream: series.timestampStream,
				ValueStream:     series.valueStream,
				Count:           series.count,
			}
		}
	}
	
	return nil
}

// PublicMetricSeries is a public view of MetricSeries for testing
type PublicMetricSeries struct {
	Name            string
	FirstTimestamp  int64
	LastTimestamp   int64
	FirstValue      float64
	LastValue       float64
	TimestampStream *gorilla.TimestampEncoder
	ValueStream     *gorilla.ValueEncoder
	Count           int
}
