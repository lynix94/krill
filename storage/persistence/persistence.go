package persistence

import (
	"github.com/lynix/krill/storage"
	"github.com/lynix/krill/storage/badger"
)

// BadgerDB interface for both single and partitioned implementations
type BadgerDB interface {
	PutLabels(ts int64, labels storage.Labels, value float64) error
	TsdbPut(ts int64, metric string, value float64) error
	PutBatch(points []storage.DataPoint) error
	TsdbPutBatch(points []storage.DataPoint) error
	Get(metric string, startTs, endTs int64) ([]int64, []float64, error)
	GetLabels(labels storage.Labels, startTs, endTs int64) ([]int64, []float64, error)
	GetMetrics() ([]string, error)
	GetAllSeries() ([]storage.Labels, error)
	FindSeriesByLabels(labelMatchers map[string]string) []uint64
	GetLabelsForSeriesID(seriesID uint64) (storage.Labels, bool)
	Close() error
	RunGC() error
	SetMemoryCache(cache badger.MemoryCacheProvider)
}

// PersistenceStorage wraps BadgerDB for the Storage interface
type PersistenceStorage struct {
	db BadgerDB
}

// NewPersistenceStorage creates a new persistence storage
func NewPersistenceStorage(opts badger.BadgerOptions) (*PersistenceStorage, error) {
	var db BadgerDB
	var err error
	
	if opts.Partitions > 0 {
		// Use partitioned BadgerDB for parallel writes
		db, err = badger.NewPartitionedBadgerTSDB(opts, opts.Partitions)
	} else {
		// Use single BadgerDB instance
		db, err = badger.NewBadgerTSDB(opts)
	}
	
	if err != nil {
		return nil, err
	}
	return &PersistenceStorage{db: db}, nil
}

// SetMemoryCache sets the memory cache provider for zero-copy writes
func (ps *PersistenceStorage) SetMemoryCache(cache badger.MemoryCacheProvider) {
	if ps.db != nil {
		ps.db.SetMemoryCache(cache)
	}
}

// Put stores a time-series data point
func (ps *PersistenceStorage) Put(ts int64, metric string, value float64) error {
	return ps.db.TsdbPut(ts, metric, value)
}

// PutLabels stores a data point with labels
func (ps *PersistenceStorage) PutLabels(ts int64, labels storage.Labels, value float64) error {
	return ps.db.PutLabels(ts, labels, value)
}

// PutBatch stores multiple data points efficiently
func (ps *PersistenceStorage) PutBatch(points []storage.DataPoint) error {
	return ps.db.PutBatch(points)
}

// Get retrieves data points for a metric within a time range
func (ps *PersistenceStorage) Get(metric string, startTs, endTs int64) ([]int64, []float64, error) {
	return ps.db.Get(metric, startTs, endTs)
}

// GetLabels retrieves data points by labels within a time range
func (ps *PersistenceStorage) GetLabels(labels storage.Labels, startTs, endTs int64) ([]int64, []float64, error) {
	return ps.db.GetLabels(labels, startTs, endTs)
}

// GetMetrics returns all metric names
func (ps *PersistenceStorage) GetMetrics() ([]string, error) {
	return ps.db.GetMetrics()
}

// GetAllSeries returns all series
func (ps *PersistenceStorage) GetAllSeries() ([]storage.Labels, error) {
	return ps.db.GetAllSeries()
}

// FindSeriesByLabels finds series IDs matching the given label matchers using inverted index
func (ps *PersistenceStorage) FindSeriesByLabels(labelMatchers map[string]string) []uint64 {
	return ps.db.FindSeriesByLabels(labelMatchers)
}

// GetLabelsForSeriesID retrieves labels for a given series ID
func (ps *PersistenceStorage) GetLabelsForSeriesID(seriesID uint64) (storage.Labels, bool) {
	return ps.db.GetLabelsForSeriesID(seriesID)
}

// Close closes the storage
func (ps *PersistenceStorage) Close() error {
	return ps.db.Close()
}

// RunGC runs garbage collection
func (ps *PersistenceStorage) RunGC() error {
	return ps.db.RunGC()
}
