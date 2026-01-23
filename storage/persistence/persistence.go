package persistence

import (
	"github.com/lynix/krill/storage/badger"
)

// PersistenceStorage wraps BadgerDB for the Storage interface
type PersistenceStorage struct {
	db *badger.BadgerTSDB
}

// NewPersistenceStorage creates a new persistence storage
func NewPersistenceStorage(opts badger.BadgerOptions) (*PersistenceStorage, error) {
	db, err := badger.NewBadgerTSDB(opts)
	if err != nil {
		return nil, err
	}
	return &PersistenceStorage{db: db}, nil
}

// Put stores a time-series data point
func (ps *PersistenceStorage) Put(ts int64, metric string, value float64) error {
	return ps.db.TsdbPut(ts, metric, value)
}

// Get retrieves data points for a metric within a time range
func (ps *PersistenceStorage) Get(metric string, startTs, endTs int64) ([]int64, []float64, error) {
	return ps.db.Get(metric, startTs, endTs)
}

// GetMetrics returns all metric names
func (ps *PersistenceStorage) GetMetrics() ([]string, error) {
	return ps.db.GetMetrics()
}

// Close closes the storage
func (ps *PersistenceStorage) Close() error {
	return ps.db.Close()
}

// RunGC runs garbage collection
func (ps *PersistenceStorage) RunGC() error {
	return ps.db.RunGC()
}
