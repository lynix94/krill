package memory

import (
	"fmt"
	"sync"

	"github.com/lynix/krill/storage/gorilla"
)

// MemoryStorage implements in-memory storage using Gorilla compression
type MemoryStorage struct {
	mu      sync.RWMutex
	metrics map[string]*MetricSeries
}

// MetricSeries stores compressed time-series data for a single metric
type MetricSeries struct {
	mu              sync.Mutex
	name            string
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
		metrics: make(map[string]*MetricSeries),
	}
}

// Put stores a time-series data point with Gorilla compression
func (ms *MemoryStorage) Put(ts int64, metric string, value float64) error {
	ms.mu.Lock()
	series, exists := ms.metrics[metric]
	if !exists {
		series = &MetricSeries{
			name:            metric,
			timestampStream: gorilla.NewTimestampEncoder(),
			valueStream:     gorilla.NewValueEncoder(),
		}
		ms.metrics[metric] = series
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
func (ms *MemoryStorage) Get(metric string, startTs, endTs int64) ([]int64, []float64, error) {
	ms.mu.RLock()
	series, exists := ms.metrics[metric]
	ms.mu.RUnlock()

	if !exists {
		return nil, nil, fmt.Errorf("metric not found: %s", metric)
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

// GetMetrics returns all metric names
func (ms *MemoryStorage) GetMetrics() ([]string, error) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	metrics := make([]string, 0, len(ms.metrics))
	for metric := range ms.metrics {
		metrics = append(metrics, metric)
	}
	return metrics, nil
}

// Close closes the storage (no-op for memory storage)
func (ms *MemoryStorage) Close() error {
	return nil
}

// DeleteOlderThan removes data points older than the specified timestamp
func (ms *MemoryStorage) DeleteOlderThan(cutoffTs int64) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	toDelete := make([]string, 0)
	
	for metric, series := range ms.metrics {
		series.mu.Lock()
		if series.lastTimestamp < cutoffTs {
			// Entire series is old, delete it
			toDelete = append(toDelete, metric)
		}
		series.mu.Unlock()
	}

	for _, metric := range toDelete {
		delete(ms.metrics, metric)
	}

	return nil
}

// GetSeries returns the metric series for testing purposes
func (ms *MemoryStorage) GetSeries(metric string) *PublicMetricSeries {
	ms.mu.RLock()
	series, exists := ms.metrics[metric]
	ms.mu.RUnlock()
	
	if !exists {
		return nil
	}
	
	series.mu.Lock()
	defer series.mu.Unlock()
	
	return &PublicMetricSeries{
		Name:            series.name,
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
