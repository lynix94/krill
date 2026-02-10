package krill

import (
	"github.com/lynix/krill/storage"
	"github.com/lynix/krill/storage/memory"
)

// TSDB represents a time-series database (memory-only)
type TSDB struct {
	storage storage.Storage
}

// NewTSDB creates a new in-memory time-series database
func NewTSDB() *TSDB {
	return &TSDB{
		storage: memory.NewMemoryStorage(),
	}
}

// TsdbPut stores a time-series data point with Gorilla compression
// Alias: tsdb_put
func (db *TSDB) TsdbPut(ts int64, metric string, value float64) error {
	return db.storage.Put(ts, metric, value)
}

// Get retrieves all data points for a metric within a time range
// Pass 0 for startTs and endTs to get all data
func (db *TSDB) Get(metric string, startTs, endTs int64) ([]int64, []float64, error) {
	return db.storage.Get(metric, startTs, endTs)
}

// GetMetrics returns all metric names
func (db *TSDB) GetMetrics() ([]string, error) {
	return db.storage.GetMetrics()
}

// GetAllSeries returns all series (labels) in the database
func (db *TSDB) GetAllSeries() ([]storage.Labels, error) {
	return db.storage.GetAllSeries()
}

// Close closes the database (no-op for in-memory implementation)
func (db *TSDB) Close() error {
	return db.storage.Close()
}
