package krill

import (
	"time"

	"github.com/lynix/krill/storage/badger"
)

// TimeSeriesDB is the interface for time-series database implementations
type TimeSeriesDB interface {
	// TsdbPut stores a time-series data point
	TsdbPut(ts int64, metric string, value float64) error
	
	// Close closes the database and releases resources
	Close() error
}

// QueryableDB extends TimeSeriesDB with query capabilities
type QueryableDB interface {
	TimeSeriesDB
	
	// Get retrieves data points for a metric
	// For memory-based implementation, startTs and endTs can be 0 to get all data
	Get(metric string, startTs, endTs int64) ([]int64, []float64, error)
	
	// GetMetrics returns all metric names
	GetMetrics() ([]string, error)
}

// Ensure implementations satisfy the interfaces
var (
	_ TimeSeriesDB = (*TSDB)(nil)
	_ QueryableDB  = (*TSDB)(nil)
	_ TimeSeriesDB = (*badger.BadgerTSDB)(nil)
	_ QueryableDB  = (*badger.BadgerTSDB)(nil)
	_ TimeSeriesDB = (*HybridTSDB)(nil)
	_ QueryableDB  = (*HybridTSDB)(nil)
)

// MemoryTSDB creates a new in-memory TSDB (alias for NewTSDB)
func MemoryTSDB() *TSDB {
	return NewTSDB()
}

// PersistentTSDB creates a new persistent TSDB with default options
func PersistentTSDB(path string) (*badger.BadgerTSDB, error) {
	return badger.NewBadgerTSDB(badger.BadgerOptions{
		Path: path,
		TTL:  0, // No expiration
	})
}

// PersistentTSDBWithTTL creates a new persistent TSDB with TTL
func PersistentTSDBWithTTL(path string, ttl time.Duration) (*badger.BadgerTSDB, error) {
	return badger.NewBadgerTSDB(badger.BadgerOptions{
		Path: path,
		TTL:  ttl,
	})
}

// HybridTSDBWithDefaults creates a new hybrid TSDB with default settings
// Default: 2 hour cache, 10 minute cleanup interval
func HybridTSDBWithDefaults(path string) (*HybridTSDB, error) {
	return NewHybridTSDB(HybridOptions{
		PersistencePath: path,
		CacheDuration:   2 * time.Hour,
		CleanupInterval: 10 * time.Minute,
	})
}
