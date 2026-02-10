package storage

// DataPoint represents a single time-series data point with labels
type DataPoint struct {
	Timestamp int64
	Labels    Labels
	Value     float64
}

// Storage is the interface for time-series storage backends
type Storage interface {
	// Put stores a single data point
	Put(ts int64, metric string, value float64) error
	
	// PutLabels stores a data point with labels
	PutLabels(ts int64, labels Labels, value float64) error
	
	// PutBatch stores multiple data points efficiently
	PutBatch(points []DataPoint) error
	
	// Get retrieves data points for a metric within a time range
	// Use startTs=0, endTs=0 to get all data
	Get(metric string, startTs, endTs int64) ([]int64, []float64, error)
	
	// GetLabels retrieves data points by labels within a time range
	GetLabels(labels Labels, startTs, endTs int64) ([]int64, []float64, error)
	
	// GetMetrics returns all metric names
	GetMetrics() ([]string, error)
	
	// GetAllSeries returns all series (labels)
	GetAllSeries() ([]Labels, error)
	
	// Close closes the storage and releases resources
	Close() error
}
