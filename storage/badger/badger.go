package badger

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/lynix/krill/storage/gorilla"
)

// BadgerTSDB is a persistent time-series database using BadgerDB
type BadgerTSDB struct {
	db  *badger.DB
	ttl time.Duration
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

	return &BadgerTSDB{
		db:  db,
		ttl: opts.TTL,
	}, nil
}

// TsdbPut stores a time-series data point with Gorilla compression
func (bdb *BadgerTSDB) TsdbPut(ts int64, metric string, value float64) error {
	// Create time-partitioned key: metric:timestamp_bucket
	// Use hourly buckets for better read performance
	bucket := ts / 3600 * 3600
	key := makeKey(metric, bucket)

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
			Metric:           metric,
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

// Get retrieves all data points for a metric within a time range
func (bdb *BadgerTSDB) Get(metric string, startTs, endTs int64) ([]int64, []float64, error) {
	if endTs == 0 {
		endTs = math.MaxInt64
	}

	type dataPoint struct {
		timestamp int64
		value     float64
	}
	var allPoints []dataPoint

	err := bdb.db.View(func(txn *badger.Txn) error {
		// Iterate through all buckets that might contain data
		prefix := []byte(metric + ":")
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
		return nil, nil, fmt.Errorf("metric not found: %s", metric)
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

// GetMetrics returns all metric names
func (bdb *BadgerTSDB) GetMetrics() ([]string, error) {
	metrics := make(map[string]bool)

	err := bdb.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = false

		it := txn.NewIterator(opts)
		defer it.Close()

		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			key := item.Key()

			// Extract metric name from key (format: metric:bucket)
			metric := extractMetricFromKey(key)
			metrics[metric] = true
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	result := make([]string, 0, len(metrics))
	for metric := range metrics {
		result = append(result, metric)
	}

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

// SeriesBlock represents a compressed time series block
type SeriesBlock struct {
	Metric           string
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
