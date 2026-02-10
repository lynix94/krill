package badger

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/lynix/krill/storage"
	"github.com/lynix/krill/storage/gorilla"
)

// BadgerTSDB is a persistent time-series database using BadgerDB
type BadgerTSDB struct {
	db                *badger.DB
	ttl               time.Duration
	labels            map[uint64]storage.Labels // seriesID -> labels mapping
	labelsMu          sync.RWMutex              // Protects labels map
	formattedMetrics  map[uint64]string         // seriesID -> formatted metric string cache
	metricsCache      []string                  // cached GetMetrics() result
	metricsCacheTime  time.Time                 // last cache update time
	metricsCacheMu    sync.RWMutex              // Protects metrics cache
	metricsCacheTTL   time.Duration             // cache TTL (default 5 minutes)
}

// BadgerOptions contains configuration for BadgerTSDB
type BadgerOptions struct {
	Path string        // Directory path for database
	TTL  time.Duration // Time-to-live for data points (0 = no expiration)
}

// NewBadgerTSDB creates a new persistent TSDB with BadgerDB
func NewBadgerTSDB(opts BadgerOptions) (*BadgerTSDB, error) {
	if opts.Path == "" {
		opts.Path = "./krill_data"
	}

	// Configure BadgerDB
	badgerOpts := badger.DefaultOptions(opts.Path)
	badgerOpts.Logger = nil // Disable logging for cleaner output

	db, err := badger.Open(badgerOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to open BadgerDB: %w", err)
	}

	bdb := &BadgerTSDB{
		db:               db,
		ttl:              opts.TTL,
		labels:           make(map[uint64]storage.Labels),
		formattedMetrics: make(map[uint64]string),
		metricsCacheTTL:  5 * time.Minute, // 5 minutes cache
	}

	// Load all series labels from database
	if err := bdb.loadAllSeries(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to load series: %w", err)
	}

	return bdb, nil
}

// PutLabels stores a time-series data point with Gorilla compression using Labels
func (bdb *BadgerTSDB) PutLabels(ts int64, labels storage.Labels, value float64) error {
	seriesID := labels.Hash()
	
	// Store labels mapping in memory with proper locking
	bdb.labelsMu.Lock()
	bdb.labels[seriesID] = labels.Copy()
	// Cache the formatted metric string
	bdb.formattedMetrics[seriesID] = formatLabelsAsMetricString(labels)
	bdb.labelsMu.Unlock()
	
	// Invalidate GetMetrics cache when new series is added
	bdb.metricsCacheMu.Lock()
	bdb.metricsCache = nil
	bdb.metricsCacheMu.Unlock()
	
	// Store labels metadata in BadgerDB for persistence
	if err := bdb.storeLabelsMetadata(seriesID, labels); err != nil {
		return fmt.Errorf("failed to store labels metadata: %w", err)
	}
	
	// Create time-partitioned key: seriesID:timestamp_bucket
	// Use hourly buckets for better read performance
	bucket := ts / 3600 * 3600
	key := makeKeyFromID(seriesID, bucket)

	return bdb.db.Update(func(txn *badger.Txn) error {
		// Get existing series for this bucket
		item, err := txn.Get(key)
		
		var existingTimestamps []int64
		var existingValues []float64
		
		if err == nil {
			// Deserialize and decode existing series
			err = item.Value(func(val []byte) error {
				series, err := DeserializeSeriesBlock(val)
				if err != nil {
					return err
				}
				existingTimestamps, existingValues, err = series.Decode()
				return err
			})
			if err != nil {
				return err
			}
		} else if err != badger.ErrKeyNotFound {
			return err
		}

		// Validate timestamp ordering
		if len(existingTimestamps) > 0 {
			lastTs := existingTimestamps[len(existingTimestamps)-1]
			if ts < lastTs {
				return fmt.Errorf("timestamp must not be older than last timestamp: %d < %d", ts, lastTs)
			}
			// Silently ignore if timestamp is exactly the same (idempotent write)
			if ts == lastTs {
				return nil
			}
			// Append new data point
			existingTimestamps = append(existingTimestamps, ts)
			existingValues = append(existingValues, value)
		} else {
			// First data point
			existingTimestamps = append(existingTimestamps, ts)
			existingValues = append(existingValues, value)
		}

		// Create new series block with all data
		series := &SeriesBlock{
			SeriesID:         seriesID,
			StartTimestamp:   bucket,
			timestampEncoder: gorilla.NewTimestampEncoder(),
			valueEncoder:     gorilla.NewValueEncoder(),
		}

		// Encode all data points
		for i, timestamp := range existingTimestamps {
			if err := series.AddPoint(timestamp, existingValues[i]); err != nil {
				return err
			}
		}

		// Serialize and store
		data, err := series.Serialize()
		if err != nil {
			return err
		}

		// Set TTL if configured
		entry := badger.NewEntry(key, data)
		if bdb.ttl > 0 {
			entry = entry.WithTTL(bdb.ttl)
		}

		return txn.SetEntry(entry)
	})
}

// TsdbPut stores a time-series data point with Gorilla compression (legacy string-based API)
func (bdb *BadgerTSDB) TsdbPut(ts int64, metric string, value float64) error {
	labels := parseMetricString(metric)
	return bdb.PutLabels(ts, labels, value)
}

// TsdbPutBatch stores multiple time-series data points efficiently
func (bdb *BadgerTSDB) TsdbPutBatch(points []storage.DataPoint) error {
	// Group points by series ID and bucket for efficient batching
	type bucketKey struct {
		seriesID uint64
		bucket   int64
	}
	bucketPoints := make(map[bucketKey][]storage.DataPoint)
	
	for _, point := range points {
		seriesID := point.Labels.Hash()
		bucket := point.Timestamp / 3600 * 3600
		key := bucketKey{seriesID, bucket}
		bucketPoints[key] = append(bucketPoints[key], point)
		
		// Store labels mapping
		bdb.labelsMu.Lock()
		bdb.labels[seriesID] = point.Labels.Copy()
		bdb.formattedMetrics[seriesID] = formatLabelsAsMetricString(point.Labels)
		bdb.labelsMu.Unlock()
	}
	
	// Invalidate cache
	bdb.metricsCacheMu.Lock()
	bdb.metricsCache = nil
	bdb.metricsCacheMu.Unlock()
	
	// Store labels metadata
	labelsSeen := make(map[uint64]bool)
	for key := range bucketPoints {
		if !labelsSeen[key.seriesID] {
			labelsSeen[key.seriesID] = true
			if err := bdb.storeLabelsMetadata(key.seriesID, bdb.labels[key.seriesID]); err != nil {
				return fmt.Errorf("failed to store labels metadata: %w", err)
			}
		}
	}
	
	// Batch write all points
	return bdb.db.Update(func(txn *badger.Txn) error {
		for bKey, points := range bucketPoints {
			key := makeKeyFromID(bKey.seriesID, bKey.bucket)
			
			// Get existing series for this bucket
			var existingTimestamps []int64
			var existingValues []float64
			
			item, err := txn.Get(key)
			if err == nil {
				err = item.Value(func(val []byte) error {
					series, err := DeserializeSeriesBlock(val)
					if err != nil {
						return err
					}
					existingTimestamps, existingValues, err = series.Decode()
					return err
				})
				if err != nil {
					return err
				}
			} else if err != badger.ErrKeyNotFound {
				return err
			}
			
			// Add new points
			for _, point := range points {
				if len(existingTimestamps) > 0 {
					lastTs := existingTimestamps[len(existingTimestamps)-1]
					if point.Timestamp < lastTs {
						continue
					}
					if point.Timestamp == lastTs {
						continue
					}
				}
				existingTimestamps = append(existingTimestamps, point.Timestamp)
				existingValues = append(existingValues, point.Value)
			}
			
			// Create new series block
			series := &SeriesBlock{
				SeriesID:         bKey.seriesID,
				StartTimestamp:   bKey.bucket,
				timestampEncoder: gorilla.NewTimestampEncoder(),
				valueEncoder:     gorilla.NewValueEncoder(),
			}
			
			// Encode all points
			for i, timestamp := range existingTimestamps {
				if err := series.AddPoint(timestamp, existingValues[i]); err != nil {
					return err
				}
			}
			
			// Serialize and store
			data, err := series.Serialize()
			if err != nil {
				return err
			}
			
			entry := badger.NewEntry(key, data)
			if bdb.ttl > 0 {
				entry = entry.WithTTL(bdb.ttl)
			}
			
			if err := txn.SetEntry(entry); err != nil {
				return err
			}
		}
		return nil
	})
}

// PutBatch implements Storage interface batch write
func (bdb *BadgerTSDB) PutBatch(points []storage.DataPoint) error {
	return bdb.TsdbPutBatch(points)
}

// GetLabels retrieves all data points for a series within a time range using Labels
func (bdb *BadgerTSDB) GetLabels(labels storage.Labels, startTs, endTs int64) ([]int64, []float64, error) {
	if endTs == 0 {
		endTs = math.MaxInt64
	}

	seriesID := labels.Hash()

	type dataPoint struct {
		timestamp int64
		value     float64
	}
	var allPoints []dataPoint

	err := bdb.db.View(func(txn *badger.Txn) error {
		// Iterate through all buckets that might contain data
		prefix := makeKeyPrefixFromID(seriesID)
		opts := badger.DefaultIteratorOptions
		opts.Prefix = prefix

		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()

			err := item.Value(func(val []byte) error {
				series, err := DeserializeSeriesBlock(val)
				if err != nil {
					return err
				}

				timestamps, values, err := series.Decode()
				if err != nil {
					return err
				}

				// Filter by time range and collect points
				for i, ts := range timestamps {
					if ts >= startTs && ts <= endTs {
						allPoints = append(allPoints, dataPoint{
							timestamp: ts,
							value:     values[i],
						})
					}
				}

				return nil
			})

			if err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		return nil, nil, err
	}

	if len(allPoints) == 0 {
		return nil, nil, fmt.Errorf("metric not found: %s", labels.String())
	}

	// Sort by timestamp (BadgerDB iterator may not return keys in order)
	// Simple insertion sort since data is likely already mostly sorted
	for i := 1; i < len(allPoints); i++ {
		j := i
		for j > 0 && allPoints[j-1].timestamp > allPoints[j].timestamp {
			allPoints[j-1], allPoints[j] = allPoints[j], allPoints[j-1]
			j--
		}
	}

	// Extract sorted timestamps and values
	timestamps := make([]int64, len(allPoints))
	values := make([]float64, len(allPoints))
	for i, point := range allPoints {
		timestamps[i] = point.timestamp
		values[i] = point.value
	}

	return timestamps, values, nil
}

// Get retrieves all data points for a metric within a time range (legacy string-based API)
func (bdb *BadgerTSDB) Get(metric string, startTs, endTs int64) ([]int64, []float64, error) {
	labels := parseMetricString(metric)
	return bdb.GetLabels(labels, startTs, endTs)
}

// GetAllSeries returns all series (labels) in the database
func (bdb *BadgerTSDB) GetAllSeries() ([]storage.Labels, error) {
	bdb.labelsMu.RLock()
	result := make([]storage.Labels, 0, len(bdb.labels))
	for _, labels := range bdb.labels {
		result = append(result, labels.Copy())
	}
	bdb.labelsMu.RUnlock()
	return result, nil
}

// GetMetrics returns all metric names (legacy API, returns formatted strings)
func (bdb *BadgerTSDB) GetMetrics() ([]string, error) {
	// Check cache first
	bdb.metricsCacheMu.RLock()
	if bdb.metricsCache != nil && time.Since(bdb.metricsCacheTime) < bdb.metricsCacheTTL {
		result := make([]string, len(bdb.metricsCache))
		copy(result, bdb.metricsCache)
		bdb.metricsCacheMu.RUnlock()
		return result, nil
	}
	bdb.metricsCacheMu.RUnlock()

	// Cache miss or expired, rebuild
	metrics := make(map[string]bool)

	bdb.labelsMu.RLock()
	// Use cached formatted strings instead of formatting each time
	for seriesID := range bdb.labels {
		if metricStr, ok := bdb.formattedMetrics[seriesID]; ok {
			metrics[metricStr] = true
		} else {
			// Fallback: format if not cached (shouldn't happen)
			metricStr = formatLabelsAsMetricString(bdb.labels[seriesID])
			metrics[metricStr] = true
		}
	}
	bdb.labelsMu.RUnlock()

	result := make([]string, 0, len(metrics))
	for metric := range metrics {
		result = append(result, metric)
	}

	// Update cache
	bdb.metricsCacheMu.Lock()
	bdb.metricsCache = result
	bdb.metricsCacheTime = time.Now()
	bdb.metricsCacheMu.Unlock()

	return result, nil
}

// Close closes the database
func (bdb *BadgerTSDB) Close() error {
	return bdb.db.Close()
}

// RunGC runs garbage collection to reclaim disk space
func (bdb *BadgerTSDB) RunGC() error {
	return bdb.db.RunValueLogGC(0.5)
}

// loadAllSeries scans the database and loads all series labels into memory
func (bdb *BadgerTSDB) loadAllSeries() error {
	var skippedCount int
	var loadedCount int
	
	err := bdb.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = true
		opts.Prefix = []byte("meta:")
		it := txn.NewIterator(opts)
		defer it.Close()

		loadedSeries := make(map[uint64]storage.Labels)

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			key := item.Key()
			
			// Extract seriesID from key "meta:seriesID"
			if len(key) < 13 { // "meta:" (5 bytes) + uint64 (8 bytes)
				continue
			}
			seriesID := binary.BigEndian.Uint64(key[5:])
			
			// Read labels from value
			err := item.Value(func(val []byte) error {
				// Skip empty values
				if len(val) == 0 {
					return nil
				}
				labels, err := deserializeLabels(val)
				if err != nil {
					// Log error but continue loading other series
					fmt.Printf("Warning: skipping corrupted series metadata (ID=%d): %v\n", seriesID, err)
					skippedCount++
					return nil // Don't fail, just skip this series
				}
				loadedSeries[seriesID] = labels
				loadedCount++
				return nil
			})
			if err != nil {
				// This shouldn't happen now since we return nil on error above
				fmt.Printf("Warning: failed to read series metadata (ID=%d): %v\n", seriesID, err)
				skippedCount++
			}
		}

		// Update labels map and build formatted metrics cache with proper locking
		bdb.labelsMu.Lock()
		bdb.labels = loadedSeries
		// Pre-build formatted metrics cache
		bdb.formattedMetrics = make(map[uint64]string, len(loadedSeries))
		for seriesID, labels := range loadedSeries {
			bdb.formattedMetrics[seriesID] = formatLabelsAsMetricString(labels)
		}
		bdb.labelsMu.Unlock()
		return nil
	})
	
	if err != nil {
		return err
	}
	
	if skippedCount > 0 {
		fmt.Printf("Loaded %d series, skipped %d corrupted series\n", loadedCount, skippedCount)
	}
	
	return nil
}

// storeLabelsMetadata stores series labels in a separate metadata key
func (bdb *BadgerTSDB) storeLabelsMetadata(seriesID uint64, labels storage.Labels) error {
	return bdb.db.Update(func(txn *badger.Txn) error {
		key := makeMetadataKey(seriesID)
		val := serializeLabels(labels)
		return txn.Set(key, val)
	})
}

// makeMetadataKey creates a metadata key for series labels
func makeMetadataKey(seriesID uint64) []byte {
	key := make([]byte, 13) // "meta:" (5 bytes) + uint64 (8 bytes)
	copy(key, []byte("meta:"))
	binary.BigEndian.PutUint64(key[5:], seriesID)
	return key
}

// serializeLabels serializes labels to bytes
func serializeLabels(labels storage.Labels) []byte {
	buf := new(bytes.Buffer)
	
	// Write number of labels
	binary.Write(buf, binary.BigEndian, uint32(len(labels)))
	
	// Write each label
	for _, label := range labels {
		// Write name length and name
		binary.Write(buf, binary.BigEndian, uint32(len(label.Name)))
		buf.Write([]byte(label.Name))
		
		// Write value length and value
		binary.Write(buf, binary.BigEndian, uint32(len(label.Value)))
		buf.Write([]byte(label.Value))
	}
	
	return buf.Bytes()
}

// deserializeLabels deserializes labels from bytes
func deserializeLabels(data []byte) (storage.Labels, error) {
	if len(data) == 0 {
		return storage.Labels{}, nil
	}
	
	buf := bytes.NewReader(data)
	
	// Read number of labels
	var count uint32
	if err := binary.Read(buf, binary.BigEndian, &count); err != nil {
		return nil, fmt.Errorf("failed to read label count: %w", err)
	}
	
	labels := make(storage.Labels, count)
	
	// Read each label
	for i := uint32(0); i < count; i++ {
		// Read name
		var nameLen uint32
		if err := binary.Read(buf, binary.BigEndian, &nameLen); err != nil {
			return nil, fmt.Errorf("failed to read label[%d] name length: %w", i, err)
		}
		nameBuf := make([]byte, nameLen)
		if _, err := buf.Read(nameBuf); err != nil {
			return nil, fmt.Errorf("failed to read label[%d] name: %w", i, err)
		}
		
		// Read value
		var valueLen uint32
		if err := binary.Read(buf, binary.BigEndian, &valueLen); err != nil {
			return nil, fmt.Errorf("failed to read label[%d] value length: %w", i, err)
		}
		valueBuf := make([]byte, valueLen)
		if _, err := buf.Read(valueBuf); err != nil {
			return nil, fmt.Errorf("failed to read label[%d] value: %w", i, err)
		}
		
		labels[i] = storage.Label{
			Name:  string(nameBuf),
			Value: string(valueBuf),
		}
	}
	
	return labels, nil
}

// SeriesBlock represents a compressed time series block
type SeriesBlock struct {
	SeriesID         uint64 // Changed from Metric string
	StartTimestamp   int64
	Count            int
	FirstTimestamp   int64
	LastTimestamp    int64
	FirstValue       float64
	LastValue        float64
	timestampBytes   []byte
	valueBytes       []byte
	timestampEncoder *gorilla.TimestampEncoder
	valueEncoder     *gorilla.ValueEncoder
}

// AddPoint adds a data point to the series block
func (sb *SeriesBlock) AddPoint(ts int64, value float64) error {
	// Ensure encoders are initialized
	if sb.timestampEncoder == nil {
		sb.timestampEncoder = gorilla.NewTimestampEncoder()
	}
	if sb.valueEncoder == nil {
		sb.valueEncoder = gorilla.NewValueEncoder()
	}

	if sb.Count == 0 {
		sb.FirstTimestamp = ts
		sb.LastTimestamp = ts
		sb.FirstValue = value
		sb.LastValue = value
		sb.Count++
		return nil
	}

	if ts <= sb.LastTimestamp {
		return fmt.Errorf("timestamp must be greater than last timestamp: %d <= %d", ts, sb.LastTimestamp)
	}

	// Compress timestamp delta
	delta := ts - sb.LastTimestamp
	sb.timestampEncoder.Encode(delta)

	// Compress value
	sb.valueEncoder.Encode(value, sb.LastValue)

	sb.LastTimestamp = ts
	sb.LastValue = value
	sb.Count++

	return nil
}

// Serialize converts the series block to bytes
func (sb *SeriesBlock) Serialize() ([]byte, error) {
	buf := new(bytes.Buffer)

	// Write metadata
	binary.Write(buf, binary.LittleEndian, sb.StartTimestamp)
	binary.Write(buf, binary.LittleEndian, int32(sb.Count))
	binary.Write(buf, binary.LittleEndian, sb.FirstTimestamp)
	binary.Write(buf, binary.LittleEndian, sb.LastTimestamp)
	binary.Write(buf, binary.LittleEndian, sb.FirstValue)
	binary.Write(buf, binary.LittleEndian, sb.LastValue)

	// Get compressed streams - only if we have more than one data point
	var tsData, valData []byte
	if sb.Count > 1 {
		tsData = sb.timestampEncoder.Bytes()
		valData = sb.valueEncoder.Bytes()
	}

	// Write compressed timestamp stream
	binary.Write(buf, binary.LittleEndian, int32(len(tsData)))
	if len(tsData) > 0 {
		buf.Write(tsData)
	}

	// Write compressed value stream
	binary.Write(buf, binary.LittleEndian, int32(len(valData)))
	if len(valData) > 0 {
		buf.Write(valData)
	}

	return buf.Bytes(), nil
}

// DeserializeSeriesBlock converts bytes back to a series block
func DeserializeSeriesBlock(data []byte) (*SeriesBlock, error) {
	buf := bytes.NewReader(data)
	sb := &SeriesBlock{}

	// Read metadata
	binary.Read(buf, binary.LittleEndian, &sb.StartTimestamp)
	var count int32
	binary.Read(buf, binary.LittleEndian, &count)
	sb.Count = int(count)
	binary.Read(buf, binary.LittleEndian, &sb.FirstTimestamp)
	binary.Read(buf, binary.LittleEndian, &sb.LastTimestamp)
	binary.Read(buf, binary.LittleEndian, &sb.FirstValue)
	binary.Read(buf, binary.LittleEndian, &sb.LastValue)

	// Read timestamp stream bytes
	var tsLen int32
	binary.Read(buf, binary.LittleEndian, &tsLen)
	if tsLen > 0 {
		sb.timestampBytes = make([]byte, tsLen)
		buf.Read(sb.timestampBytes)
	}

	// Read value stream bytes
	var valLen int32
	binary.Read(buf, binary.LittleEndian, &valLen)
	if valLen > 0 {
		sb.valueBytes = make([]byte, valLen)
		buf.Read(sb.valueBytes)
	}

	return sb, nil
}

// Decode decodes all data points from the series block
func (sb *SeriesBlock) Decode() ([]int64, []float64, error) {
	if sb.Count == 0 {
		return []int64{}, []float64{}, nil
	}

	timestamps := make([]int64, 0, sb.Count)
	values := make([]float64, 0, sb.Count)

	// First value
	timestamps = append(timestamps, sb.FirstTimestamp)
	values = append(values, sb.FirstValue)

	if sb.Count == 1 {
		return timestamps, values, nil
	}

	// Check if we have compressed data
	if len(sb.timestampBytes) == 0 || len(sb.valueBytes) == 0 {
		// If deserialized from old data or encoder still exists, try using encoder bytes
		if sb.timestampEncoder != nil && sb.valueEncoder != nil {
			sb.timestampBytes = sb.timestampEncoder.Bytes()
			sb.valueBytes = sb.valueEncoder.Bytes()
		}
		
		if len(sb.timestampBytes) == 0 || len(sb.valueBytes) == 0 {
			// No compressed data available, return just the first value
			return timestamps, values, nil
		}
	}

	// Create fresh decoders from the stored byte streams
	timestampDecoder := gorilla.NewTimestampDecoder(sb.timestampBytes)
	valueDecoder := gorilla.NewValueDecoder(sb.valueBytes)

	currentTimestamp := sb.FirstTimestamp
	currentValue := sb.FirstValue

	for i := 1; i < sb.Count; i++ {
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

	return timestamps, values, nil
}

// Helper functions
func makeKeyFromID(seriesID uint64, bucket int64) []byte {
	key := fmt.Sprintf("%d:%d", seriesID, bucket)
	return []byte(key)
}

func makeKeyPrefixFromID(seriesID uint64) []byte {
	prefix := fmt.Sprintf("%d:", seriesID)
	return []byte(prefix)
}

// Legacy helper (no longer used internally)
func makeKey(metric string, bucket int64) []byte {
	key := fmt.Sprintf("%s:%d", metric, bucket)
	return []byte(key)
}

func extractMetricFromKey(key []byte) string {
	keyStr := string(key)
	for i := len(keyStr) - 1; i >= 0; i-- {
		if keyStr[i] == ':' {
			return keyStr[:i]
		}
	}
	return keyStr
}

// parseMetricString parses a metric string (e.g., "http_requests{method=\"GET\",status=\"200\"}")
// into Labels
func parseMetricString(metric string) storage.Labels {
	// Find the opening brace
	braceIdx := strings.Index(metric, "{")
	
	var name string
	var labels storage.Labels
	
	if braceIdx == -1 {
		// No tags, just metric name
		name = metric
	} else {
		// Extract metric name
		name = metric[:braceIdx]
		
		// Extract tags
		tagsStr := metric[braceIdx+1:]
		if len(tagsStr) > 0 && tagsStr[len(tagsStr)-1] == '}' {
			tagsStr = tagsStr[:len(tagsStr)-1]
		}
		
		// Parse tags
		tags := splitTags(tagsStr)
		labels = make(storage.Labels, 0, len(tags)+1)
		for key, value := range tags {
			labels = append(labels, storage.Label{Name: key, Value: value})
		}
	}
	
	// Always add __name__ label
	labels = append(labels, storage.Label{Name: "__name__", Value: name})
	sort.Sort(labels)
	
	return labels
}

// splitTags splits a tag string (e.g., "method=\"GET\",status=\"200\"") into a map
func splitTags(tagsStr string) map[string]string {
	tags := make(map[string]string)
	if tagsStr == "" {
		return tags
	}
	
	// Simple parser for key="value" pairs
	parts := strings.Split(tagsStr, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		eqIdx := strings.Index(part, "=")
		if eqIdx == -1 {
			continue
		}
		
		key := strings.TrimSpace(part[:eqIdx])
		value := strings.TrimSpace(part[eqIdx+1:])
		
		// Remove quotes from value
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			value = value[1 : len(value)-1]
		}
		
		tags[key] = value
	}
	
	return tags
}

// formatLabelsAsMetricString converts Labels back to metric string format
func formatLabelsAsMetricString(labels storage.Labels) string {
	name := labels.Get("__name__")
	if name == "" {
		name = "unknown"
	}
	
	// Collect non-name labels
	var tagParts []string
	for _, label := range labels {
		if label.Name != "__name__" {
			tagParts = append(tagParts, fmt.Sprintf("%s=\"%s\"", label.Name, label.Value))
		}
	}
	
	if len(tagParts) == 0 {
		return name
	}
	
	return fmt.Sprintf("%s{%s}", name, strings.Join(tagParts, ","))
}
