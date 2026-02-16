package memory

import (
	"fmt"
	"sync"
	"time"

	"github.com/lynix/krill/storage"
	"github.com/lynix/krill/storage/gorilla"
)

// MemoryStorage implements in-memory storage using Gorilla compression
type MemoryStorage struct {
	mu      sync.RWMutex
	series  map[uint64]*MetricSeries  // seriesID -> series
	labels  map[uint64]storage.Labels // seriesID -> labels
	
	// Cache for GetMetrics to avoid repeated formatting
	metricsCache     []string
	metricsCacheTime time.Time
	metricsCacheMu   sync.RWMutex
}

// MetricSeries stores compressed time-series data for a single metric
type MetricSeries struct {
	mu              sync.Mutex
	id              uint64
	firstTimestamp  int64
	lastTimestamp   int64
	firstValue      float64
	lastValue       float64
	timestampStream *gorilla.TimestampEncoder
	valueStream     *gorilla.ValueEncoder
	count           int
}

// NewMemoryStorage creates a new in-memory storage
func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		series: make(map[uint64]*MetricSeries),
		labels: make(map[uint64]storage.Labels),
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

// PutBatch stores multiple time-series data points efficiently
func (ms *MemoryStorage) PutBatch(points []storage.DataPoint) error {
	// Group points by series ID for efficient processing
	seriesPoints := make(map[uint64][]storage.DataPoint)
	for _, point := range points {
		seriesID := point.Labels.Hash()
		seriesPoints[seriesID] = append(seriesPoints[seriesID], point)
	}
	
	// Process each series
	for seriesID, points := range seriesPoints {
		ms.mu.Lock()
		series, exists := ms.series[seriesID]
		if !exists {
			series = &MetricSeries{
				id:              seriesID,
				timestampStream: gorilla.NewTimestampEncoder(),
				valueStream:     gorilla.NewValueEncoder(),
			}
			ms.series[seriesID] = series
			ms.labels[seriesID] = points[0].Labels.Copy()
		}
		ms.mu.Unlock()
		
		// Write all points for this series
		series.mu.Lock()
		for _, point := range points {
			if series.count == 0 {
				series.firstTimestamp = point.Timestamp
				series.lastTimestamp = point.Timestamp
				series.firstValue = point.Value
				series.lastValue = point.Value
				series.count++
				continue
			}
			
			if point.Timestamp < series.lastTimestamp {
				continue // Skip old data
			}
			if point.Timestamp == series.lastTimestamp {
				continue // Skip duplicate
			}
			
			delta := point.Timestamp - series.lastTimestamp
			series.timestampStream.Encode(delta)
			series.valueStream.Encode(point.Value, series.lastValue)
			
			series.lastTimestamp = point.Timestamp
			series.lastValue = point.Value
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

// PutLabels stores a time-series data point using Labels
func (ms *MemoryStorage) PutLabels(ts int64, labels storage.Labels, value float64) error {
	seriesID := labels.Hash()
	
	ms.mu.Lock()
	series, exists := ms.series[seriesID]
	if !exists {
		series = &MetricSeries{
			id:              seriesID,
			timestampStream: gorilla.NewTimestampEncoder(),
			valueStream:     gorilla.NewValueEncoder(),
		}
		ms.series[seriesID] = series
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

// GetLabels retrieves all data points for labels within a time range
func (ms *MemoryStorage) GetLabels(labels storage.Labels, startTs, endTs int64) ([]int64, []float64, error) {
	seriesID := labels.Hash()
	
	ms.mu.RLock()
	series, exists := ms.series[seriesID]
	ms.mu.RUnlock()

	if !exists {
		return nil, nil, fmt.Errorf("series not found")
	}

	series.mu.Lock()
	defer series.mu.Unlock()

	if series.count == 0 {
		return []int64{}, []float64{}, nil
	}

	timestamps := make([]int64, 0, series.count)
	values := make([]float64, 0, series.count)

	// First value
	timestamps = append(timestamps, series.firstTimestamp)
	values = append(values, series.firstValue)

	if series.count == 1 {
		if startTs > 0 && (series.firstTimestamp < startTs || series.firstTimestamp > endTs) {
			return []int64{}, []float64{}, nil
		}
		return timestamps, values, nil
	}

	// Decode remaining values
	timestampDecoder := gorilla.NewTimestampDecoder(series.timestampStream.Bytes())
	valueDecoder := gorilla.NewValueDecoder(series.valueStream.Bytes())

	currentTimestamp := series.firstTimestamp
	currentValue := series.firstValue

	for i := 1; i < series.count; i++ {
		delta, err := timestampDecoder.Decode()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to decode timestamp: %v", err)
		}
		currentTimestamp += delta
		timestamps = append(timestamps, currentTimestamp)

		value, err := valueDecoder.Decode(currentValue)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to decode value: %v", err)
		}
		currentValue = value
		values = append(values, value)
	}

	// Filter by time range if specified
	if startTs > 0 || endTs > 0 {
		if endTs == 0 {
			endTs = 1<<63 - 1 // Max int64
		}
		filteredTimestamps := make([]int64, 0)
		filteredValues := make([]float64, 0)
		for i, ts := range timestamps {
			if ts >= startTs && ts <= endTs {
				filteredTimestamps = append(filteredTimestamps, ts)
				filteredValues = append(filteredValues, values[i])
			}
		}
		return filteredTimestamps, filteredValues, nil
	}

	return timestamps, values, nil
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
	metrics := make([]string, 0, len(ms.series))
	for seriesID := range ms.series {
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

// DeleteOlderThan removes data points older than the specified timestamp
func (ms *MemoryStorage) DeleteOlderThan(cutoffTs int64) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	toDelete := make([]uint64, 0)
	
	for seriesID, series := range ms.series {
		series.mu.Lock()
		if series.lastTimestamp < cutoffTs {
			// Entire series is old, delete it
			toDelete = append(toDelete, seriesID)
		}
		series.mu.Unlock()
	}

	for _, seriesID := range toDelete {
		delete(ms.series, seriesID)
		delete(ms.labels, seriesID)
	}

	if len(toDelete) > 0 {
		fmt.Printf("Cleaned up %d series from memory cache (older than %d)\n", len(toDelete), cutoffTs)
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

// GetSeries returns the metric series for testing purposes (legacy)
func (ms *MemoryStorage) GetSeries(metric string) *PublicMetricSeries {
	name, tags := parseMetricString(metric)
	labels := storage.LabelsFromMap(name, tags)
	seriesID := labels.Hash()
	
	ms.mu.RLock()
	series, exists := ms.series[seriesID]
	ms.mu.RUnlock()
	
	if !exists {
		return nil
	}
	
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
