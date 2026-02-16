package badger

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log"
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
	db               *badger.DB
	ttl              time.Duration
	labels           map[uint64]storage.Labels // seriesID -> labels mapping
	labelsMu         sync.RWMutex              // Protects labels map
	formattedMetrics map[uint64]string         // seriesID -> formatted metric string cache
	metricsCache     []string                  // cached GetMetrics() result
	metricsCacheTime time.Time                 // last cache update time
	metricsCacheMu   sync.RWMutex              // Protects metrics cache
	metricsCacheTTL  time.Duration             // cache TTL (default 5 minutes)

	// Inverted index: labelName -> labelValue -> []seriesID
	// Example: labelIndex["cpu"]["cpu0"] = [seriesID1, seriesID3, ...]
	labelIndex   map[string]map[string][]uint64
	labelIndexMu sync.RWMutex
}

// BadgerOptions contains configuration for BadgerTSDB
type BadgerOptions struct {
	Path string        // Directory path for database
	TTL  time.Duration // Time-to-live for data points (0 = no expiration)
}

// CleanupCorruptedDatabase removes corrupted database files and allows fresh start
func CleanupCorruptedDatabase(path string) error {
	log.Printf("Cleaning up potentially corrupted database at: %s", path)

	// Open with minimal options to check status
	opts := badger.DefaultOptions(path)
	opts.Logger = nil
	opts.ReadOnly = true

	db, err := badger.Open(opts)
	if err == nil {
		// Database opened successfully in read-only mode, close it
		db.Close()
		return nil
	}

	log.Printf("Database appears corrupted, attempting cleanup: %v", err)

	// Try to run value log garbage collection
	opts.ReadOnly = false
	opts.BypassLockGuard = true

	db, err = badger.Open(opts)
	if err != nil {
		// If still failing, the database is severely corrupted
		log.Printf("Cannot open database for cleanup. Manual intervention may be required.")
		log.Printf("Consider removing the database directory: %s", path)
		return fmt.Errorf("database severely corrupted: %w", err)
	}

	// Run GC to clean up corrupted value log files
	log.Printf("Running garbage collection to recover database...")
	for {
		err := db.RunValueLogGC(0.5) // Aggressively collect garbage
		if err != nil {
			break
		}
	}

	db.Close()
	log.Printf("Database cleanup completed")
	return nil
}

// NewBadgerTSDB creates a new persistent TSDB with BadgerDB
func NewBadgerTSDB(opts BadgerOptions) (*BadgerTSDB, error) {
	if opts.Path == "" {
		opts.Path = "./krill_data"
	}

	// Configure BadgerDB with safer options to prevent corruption
	badgerOpts := badger.DefaultOptions(opts.Path)
	badgerOpts.Logger = nil // Disable logging for cleaner output

	// Data integrity and corruption prevention settings
	badgerOpts.SyncWrites = true            // Sync writes to disk (prevents corruption on crash)
	badgerOpts.DetectConflicts = false      // Better performance for TSDB workload
	badgerOpts.CompactL0OnClose = true      // Cleanup on shutdown
	badgerOpts.NumCompactors = 2            // Parallel compaction
	badgerOpts.NumLevelZeroTables = 5       // Trigger compaction earlier
	badgerOpts.NumLevelZeroTablesStall = 10 // Prevent too many L0 tables
	badgerOpts.ValueLogFileSize = 64 << 20  // 64MB value log files (smaller = less corruption impact)
	badgerOpts.MemTableSize = 32 << 20      // 32MB memtable (more frequent flushes)
	badgerOpts.BlockCacheSize = 128 << 20   // 128MB block cache

	db, err := badger.Open(badgerOpts)
	if err != nil {
		// If normal open fails, try with BypassLockGuard (allows recovery on locked DB)
		log.Printf("Warning: Failed to open BadgerDB normally: %v", err)
		log.Printf("Attempting to open with recovery options...")

		badgerOpts.BypassLockGuard = true
		db, err = badger.Open(badgerOpts)
		if err != nil {
			return nil, fmt.Errorf("failed to open BadgerDB even with recovery options: %w", err)
		}
		log.Printf("Successfully opened BadgerDB with recovery options. Database may need compaction.")
	}

	bdb := &BadgerTSDB{
		db:               db,
		ttl:              opts.TTL,
		labels:           make(map[uint64]storage.Labels),
		formattedMetrics: make(map[uint64]string),
		metricsCacheTTL:  5 * time.Minute, // 5 minutes cache
		labelIndex:       make(map[string]map[string][]uint64),
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

	// Check if this is a new series
	bdb.labelsMu.RLock()
	_, exists := bdb.labels[seriesID]
	bdb.labelsMu.RUnlock()

	// Store labels mapping in memory with proper locking
	bdb.labelsMu.Lock()
	bdb.labels[seriesID] = labels.Copy()
	// Cache the formatted metric string
	bdb.formattedMetrics[seriesID] = formatLabelsAsMetricString(labels)
	bdb.labelsMu.Unlock()

	// Update inverted index for new series
	if !exists {
		bdb.updateLabelIndex(seriesID, labels)
	}

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
	// Split large batches into chunks to avoid "Txn is too big" error
	const maxChunkSize = 1000 // Process 1000 points per transaction

	for i := 0; i < len(points); i += maxChunkSize {
		end := i + maxChunkSize
		if end > len(points) {
			end = len(points)
		}
		chunk := points[i:end]

		if err := bdb.writeBatchChunk(chunk); err != nil {
			return err
		}
	}

	return nil
}

// writeBatchChunk writes a chunk of points in a single transaction
func (bdb *BadgerTSDB) writeBatchChunk(points []storage.DataPoint) error {
	// Group points by series ID and bucket for efficient batching
	type bucketKey struct {
		seriesID uint64
		bucket   int64
	}
	bucketPoints := make(map[bucketKey][]storage.DataPoint)

	// Track new series for index updates
	newSeries := make(map[uint64]storage.Labels)

	// Store labels with proper locking
	bdb.labelsMu.Lock()
	for _, point := range points {
		seriesID := point.Labels.Hash()
		bucket := point.Timestamp / 3600 * 3600
		key := bucketKey{seriesID, bucket}
		bucketPoints[key] = append(bucketPoints[key], point)

		// Check if new series
		if _, exists := bdb.labels[seriesID]; !exists {
			newSeries[seriesID] = point.Labels.Copy()
		}

		// Store labels mapping (already holding lock)
		bdb.labels[seriesID] = point.Labels.Copy()
		bdb.formattedMetrics[seriesID] = formatLabelsAsMetricString(point.Labels)
	}
	bdb.labelsMu.Unlock()

	// Update index for new series
	for seriesID, labels := range newSeries {
		bdb.updateLabelIndex(seriesID, labels)
	}

	// Invalidate cache
	bdb.metricsCacheMu.Lock()
	bdb.metricsCache = nil
	bdb.metricsCacheMu.Unlock()

	// Store labels metadata (need to read labels again with lock)
	labelsSeen := make(map[uint64]bool)
	for key := range bucketPoints {
		if !labelsSeen[key.seriesID] {
			labelsSeen[key.seriesID] = true

			bdb.labelsMu.RLock()
			labels := bdb.labels[key.seriesID]
			bdb.labelsMu.RUnlock()

			if err := bdb.storeLabelsMetadata(key.seriesID, labels); err != nil {
				return fmt.Errorf("failed to store labels metadata: %w", err)
			}
		}
	}

	// Batch write all points with retry logic for transaction conflicts
	maxRetries := 3
	var lastErr error
	for retry := 0; retry < maxRetries; retry++ {
		lastErr = bdb.db.Update(func(txn *badger.Txn) error {
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

		// Check if we should retry
		if lastErr == nil {
			return nil
		}

		// Only retry on transaction conflict
		if lastErr == badger.ErrConflict {
			if retry < maxRetries-1 {
				// Small delay before retry
				time.Sleep(time.Millisecond * time.Duration(10*(retry+1)))
				continue
			}
		}

		// Non-conflict error or max retries reached
		break
	}

	return lastErr
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
				// Add error recovery for corrupted data blocks
				defer func() {
					if r := recover(); r != nil {
						log.Printf("[BADGER] Warning: Recovered from panic while deserializing data block: %v", r)
					}
				}()

				series, err := DeserializeSeriesBlock(val)
				if err != nil {
					// Log and skip corrupted block instead of failing entire query
					log.Printf("[BADGER] Warning: Skipping corrupted data block: %v", err)
					return nil
				}

				timestamps, values, err := series.Decode()
				if err != nil {
					log.Printf("[BADGER] Warning: Skipping block with decode error: %v", err)
					return nil
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

// updateLabelIndex updates the inverted index when a new series is added
// Persists to disk immediately for consistency
func (bdb *BadgerTSDB) updateLabelIndex(seriesID uint64, labels storage.Labels) {
	bdb.labelIndexMu.Lock()
	defer bdb.labelIndexMu.Unlock()

	// Batch all index updates for this series
	wb := bdb.db.NewWriteBatch()
	defer wb.Cancel()

	for _, label := range labels {
		// Create label name map if not exists
		if bdb.labelIndex[label.Name] == nil {
			bdb.labelIndex[label.Name] = make(map[string][]uint64)
		}

		// Add seriesID to posting list
		posting := bdb.labelIndex[label.Name][label.Value]
		// Check if already exists
		alreadyExists := false
		for _, id := range posting {
			if id == seriesID {
				alreadyExists = true
				break
			}
		}

		if !alreadyExists {
			bdb.labelIndex[label.Name][label.Value] = append(posting, seriesID)

			// Persist this posting list to disk
			key := fmt.Sprintf("idx:%s:%s", label.Name, label.Value)
			buf := make([]byte, len(bdb.labelIndex[label.Name][label.Value])*8)
			for i, id := range bdb.labelIndex[label.Name][label.Value] {
				binary.BigEndian.PutUint64(buf[i*8:], id)
			}

			if err := wb.Set([]byte(key), buf); err != nil {
				log.Printf("[BADGER-INDEX] Warning: failed to add index entry to batch: %v", err)
			}
		}
	}

	// Flush all index updates for this series
	if err := wb.Flush(); err != nil {
		log.Printf("[BADGER-INDEX] Warning: failed to persist index updates: %v", err)
	}
}

// updateLabelIndexBulk is used during initial index building (without DB persistence)
func (bdb *BadgerTSDB) updateLabelIndexBulk(seriesID uint64, labels storage.Labels) {
	bdb.labelIndexMu.Lock()
	defer bdb.labelIndexMu.Unlock()

	for _, label := range labels {
		// Create label name map if not exists
		if bdb.labelIndex[label.Name] == nil {
			bdb.labelIndex[label.Name] = make(map[string][]uint64)
		}

		// Add seriesID to posting list (no duplicate check for performance)
		bdb.labelIndex[label.Name][label.Value] = append(
			bdb.labelIndex[label.Name][label.Value],
			seriesID,
		)
	}
}

// saveIndexEntryToDB persists a single posting list to BadgerDB (deprecated)
func (bdb *BadgerTSDB) saveIndexEntryToDB(labelName, labelValue string, seriesIDs []uint64) {
	key := fmt.Sprintf("idx:%s:%s", labelName, labelValue)

	// Serialize seriesIDs
	buf := make([]byte, len(seriesIDs)*8)
	for i, id := range seriesIDs {
		binary.BigEndian.PutUint64(buf[i*8:], id)
	}

	err := bdb.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(key), buf)
	})

	if err != nil {
		log.Printf("[BADGER-INDEX] Warning: failed to persist index entry %s=%s: %v", labelName, labelValue, err)
	}
}

// persistEntireIndex saves the entire inverted index to BadgerDB in batches
func (bdb *BadgerTSDB) persistEntireIndex() error {
	bdb.labelIndexMu.RLock()
	defer bdb.labelIndexMu.RUnlock()

	wb := bdb.db.NewWriteBatch()
	defer wb.Cancel()

	count := 0
	for labelName, valueMap := range bdb.labelIndex {
		for labelValue, seriesIDs := range valueMap {
			key := fmt.Sprintf("idx:%s:%s", labelName, labelValue)
			buf := make([]byte, len(seriesIDs)*8)
			for i, id := range seriesIDs {
				binary.BigEndian.PutUint64(buf[i*8:], id)
			}

			if err := wb.Set([]byte(key), buf); err != nil {
				return err
			}

			count++
			if count%10000 == 0 {
				if err := wb.Flush(); err != nil {
					return err
				}
				wb = bdb.db.NewWriteBatch()
			}
		}
	}

	return wb.Flush()
}

// loadIndexFromDB loads the entire inverted index from BadgerDB
func (bdb *BadgerTSDB) loadIndexFromDB() error {
	log.Printf("[BADGER-INDEX] Loading inverted index from disk...")

	bdb.labelIndexMu.Lock()
	defer bdb.labelIndexMu.Unlock()

	bdb.labelIndex = make(map[string]map[string][]uint64)

	err := bdb.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte("idx:")
		it := txn.NewIterator(opts)
		defer it.Close()

		count := 0
		for it.Rewind(); it.Valid(); it.Next() {
			item := it.Item()
			key := string(item.Key())

			// Parse key: "idx:labelName:labelValue"
			parts := strings.SplitN(key, ":", 3)
			if len(parts) != 3 {
				continue
			}
			labelName := parts[1]
			labelValue := parts[2]

			// Deserialize seriesIDs
			err := item.Value(func(val []byte) error {
				if len(val)%8 != 0 {
					return fmt.Errorf("invalid posting list size")
				}

				seriesIDs := make([]uint64, len(val)/8)
				for i := 0; i < len(seriesIDs); i++ {
					seriesIDs[i] = binary.BigEndian.Uint64(val[i*8:])
				}

				// Add to index
				if bdb.labelIndex[labelName] == nil {
					bdb.labelIndex[labelName] = make(map[string][]uint64)
				}
				bdb.labelIndex[labelName][labelValue] = seriesIDs
				count++

				return nil
			})

			if err != nil {
				log.Printf("[BADGER-INDEX] Warning: failed to load index entry %s=%s: %v", labelName, labelValue, err)
			}
		}

		log.Printf("[BADGER-INDEX] Loaded %d posting lists from disk", count)
		return nil
	})

	return err
}

// FindSeriesByLabels finds series IDs matching the given label matchers using inverted index
// Returns slice of seriesIDs that match ALL label conditions (AND operation)
func (bdb *BadgerTSDB) FindSeriesByLabels(labelMatchers map[string]string) []uint64 {
	if len(labelMatchers) == 0 {
		return nil
	}

	bdb.labelIndexMu.RLock()
	defer bdb.labelIndexMu.RUnlock()

	log.Printf("[BADGER-INDEX] Searching with matchers: %v", labelMatchers)
	log.Printf("[BADGER-INDEX] Index has %d label names", len(bdb.labelIndex))

	// Find the smallest posting list to start with (optimization)
	var smallestPosting []uint64
	smallestSize := -1

	for labelName, labelValue := range labelMatchers {
		if valueMap, ok := bdb.labelIndex[labelName]; ok {
			log.Printf("[BADGER-INDEX] Label %q has %d values", labelName, len(valueMap))
			if posting, ok := valueMap[labelValue]; ok {
				log.Printf("[BADGER-INDEX] Label %q=%q has %d series", labelName, labelValue, len(posting))
				if smallestSize == -1 || len(posting) < smallestSize {
					smallestPosting = posting
					smallestSize = len(posting)
				}
			} else {
				// Label value not found - no matches
				log.Printf("[BADGER-INDEX] Label value %q not found for label %q", labelValue, labelName)
				return nil
			}
		} else {
			// Label name not found - no matches
			log.Printf("[BADGER-INDEX] Label name %q not found in index", labelName)
			return nil
		}
	}

	if smallestPosting == nil {
		return nil
	}

	// If only one matcher, return the posting list
	if len(labelMatchers) == 1 {
		result := make([]uint64, len(smallestPosting))
		copy(result, smallestPosting)
		return result
	}

	// Intersect with other posting lists
	// Convert smallest to map for O(1) lookup
	candidates := make(map[uint64]bool, len(smallestPosting))
	for _, id := range smallestPosting {
		candidates[id] = true
	}

	// Check each candidate against all other matchers
	bdb.labelsMu.RLock()
	defer bdb.labelsMu.RUnlock()

	var result []uint64
	for seriesID := range candidates {
		labels, ok := bdb.labels[seriesID]
		if !ok {
			continue
		}

		// Check if all label matchers match
		match := true
		for name, value := range labelMatchers {
			if labels.Get(name) != value {
				match = false
				break
			}
		}

		if match {
			result = append(result, seriesID)
		}
	}

	return result
}

// GetLabelsForSeriesID returns the labels for a specific series ID
func (bdb *BadgerTSDB) GetLabelsForSeriesID(seriesID uint64) (storage.Labels, bool) {
	bdb.labelsMu.RLock()
	defer bdb.labelsMu.RUnlock()

	labels, ok := bdb.labels[seriesID]
	if !ok {
		return nil, false
	}
	return labels.Copy(), true
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

	// Try to load persisted index first
	if err := bdb.loadIndexFromDB(); err != nil {
		log.Printf("[BADGER-INDEX] No persisted index found, building from scratch...")

		// Build inverted index from loaded series (without persisting each update)
		log.Printf("[BADGER-INDEX] Building inverted index from %d series...", len(bdb.labels))
		count := 0
		progressInterval := 100000 // Log every 100k series
		for seriesID, labels := range bdb.labels {
			bdb.updateLabelIndexBulk(seriesID, labels)
			count++
			if count%progressInterval == 0 {
				log.Printf("[BADGER-INDEX] Progress: %d/%d series indexed (%.1f%%)",
					count, len(bdb.labels), float64(count)*100.0/float64(len(bdb.labels)))
			}
		}
		log.Printf("[BADGER-INDEX] Index build complete: %d series, %d label names",
			len(bdb.labels), len(bdb.labelIndex))

		// Now persist the entire index in one go
		log.Printf("[BADGER-INDEX] Persisting index to disk...")
		if err := bdb.persistEntireIndex(); err != nil {
			log.Printf("[BADGER-INDEX] Warning: failed to persist index: %v", err)
		} else {
			log.Printf("[BADGER-INDEX] Index persisted successfully")
		}
	} else {
		log.Printf("[BADGER-INDEX] Successfully loaded persisted index with %d label names", len(bdb.labelIndex))
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

// deserializeLabels deserializes labels from bytes with string interning
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

	// Read each label with string interning for memory efficiency
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

		// Use string interning to reduce memory usage
		labels[i] = storage.InternLabel(string(nameBuf), string(valueBuf))
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
	// Validate minimum data size to prevent slice out of bounds
	const minSize = 8 + 4 + 8 + 8 + 8 + 8 + 4 + 4 // metadata fields
	if len(data) < minSize {
		return nil, fmt.Errorf("corrupted data block: insufficient size %d (minimum %d)", len(data), minSize)
	}

	buf := bytes.NewReader(data)
	sb := &SeriesBlock{}

	// Read metadata with error checking
	if err := binary.Read(buf, binary.LittleEndian, &sb.StartTimestamp); err != nil {
		return nil, fmt.Errorf("failed to read StartTimestamp: %w", err)
	}

	var count int32
	if err := binary.Read(buf, binary.LittleEndian, &count); err != nil {
		return nil, fmt.Errorf("failed to read count: %w", err)
	}

	// Validate count to prevent huge allocations
	if count < 0 || count > 1000000 {
		return nil, fmt.Errorf("invalid count value: %d", count)
	}
	sb.Count = int(count)

	if err := binary.Read(buf, binary.LittleEndian, &sb.FirstTimestamp); err != nil {
		return nil, fmt.Errorf("failed to read FirstTimestamp: %w", err)
	}
	if err := binary.Read(buf, binary.LittleEndian, &sb.LastTimestamp); err != nil {
		return nil, fmt.Errorf("failed to read LastTimestamp: %w", err)
	}
	if err := binary.Read(buf, binary.LittleEndian, &sb.FirstValue); err != nil {
		return nil, fmt.Errorf("failed to read FirstValue: %w", err)
	}
	if err := binary.Read(buf, binary.LittleEndian, &sb.LastValue); err != nil {
		return nil, fmt.Errorf("failed to read LastValue: %w", err)
	}

	// Read timestamp stream bytes
	var tsLen int32
	if err := binary.Read(buf, binary.LittleEndian, &tsLen); err != nil {
		return nil, fmt.Errorf("failed to read timestamp length: %w", err)
	}

	// Validate length to prevent huge allocations
	if tsLen < 0 || tsLen > 10000000 {
		return nil, fmt.Errorf("invalid timestamp length: %d", tsLen)
	}

	if tsLen > 0 {
		// Check if enough data remains
		if int64(tsLen) > int64(buf.Len()) {
			return nil, fmt.Errorf("insufficient data for timestamp bytes: need %d, have %d", tsLen, buf.Len())
		}
		sb.timestampBytes = make([]byte, tsLen)
		if _, err := buf.Read(sb.timestampBytes); err != nil {
			return nil, fmt.Errorf("failed to read timestamp bytes: %w", err)
		}
	}

	// Read value stream bytes
	var valLen int32
	if err := binary.Read(buf, binary.LittleEndian, &valLen); err != nil {
		return nil, fmt.Errorf("failed to read value length: %w", err)
	}

	// Validate length to prevent huge allocations
	if valLen < 0 || valLen > 10000000 {
		return nil, fmt.Errorf("invalid value length: %d", valLen)
	}

	if valLen > 0 {
		// Check if enough data remains
		if int64(valLen) > int64(buf.Len()) {
			return nil, fmt.Errorf("insufficient data for value bytes: need %d, have %d", valLen, buf.Len())
		}
		sb.valueBytes = make([]byte, valLen)
		if _, err := buf.Read(sb.valueBytes); err != nil {
			return nil, fmt.Errorf("failed to read value bytes: %w", err)
		}
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
