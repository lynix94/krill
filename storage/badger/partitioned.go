package badger

import (
	"fmt"
	"path/filepath"
	"sync"

	"github.com/lynix/krill/storage"
)

// PartitionedBadgerTSDB implements partitioned BadgerDB for parallel writes
type PartitionedBadgerTSDB struct {
	partitions   []*BadgerTSDB
	numPartitions int
}

// NewPartitionedBadgerTSDB creates a new partitioned BadgerDB with multiple instances
func NewPartitionedBadgerTSDB(opts BadgerOptions, numPartitions int) (*PartitionedBadgerTSDB, error) {
	if numPartitions <= 0 {
		numPartitions = 4 // Default
	}

	partitions := make([]*BadgerTSDB, numPartitions)
	
	// Create each partition with separate directory
	for i := 0; i < numPartitions; i++ {
		partitionOpts := opts
		partitionOpts.Path = filepath.Join(opts.Path, fmt.Sprintf("partition_%d", i))
		
		partition, err := NewBadgerTSDB(partitionOpts)
		if err != nil {
			// Cleanup already created partitions on error
			for j := 0; j < i; j++ {
				partitions[j].Close()
			}
			return nil, fmt.Errorf("failed to create partition %d: %w", i, err)
		}
		partitions[i] = partition
	}

	return &PartitionedBadgerTSDB{
		partitions:   partitions,
		numPartitions: numPartitions,
	}, nil
}

// SetMemoryCache sets memory cache for all partitions
func (p *PartitionedBadgerTSDB) SetMemoryCache(cache MemoryCacheProvider) {
	for _, partition := range p.partitions {
		partition.SetMemoryCache(cache)
	}
}

// getPartition returns the partition index for a seriesID
func (p *PartitionedBadgerTSDB) getPartition(seriesID uint64) int {
	return int(seriesID % uint64(p.numPartitions))
}

// PutLabels stores a data point (routes to appropriate partition)
func (p *PartitionedBadgerTSDB) PutLabels(ts int64, labels storage.Labels, value float64) error {
	seriesID := labels.Hash()
	partitionIdx := p.getPartition(seriesID)
	return p.partitions[partitionIdx].PutLabels(ts, labels, value)
}

// TsdbPut stores a data point (legacy API)
func (p *PartitionedBadgerTSDB) TsdbPut(ts int64, metric string, value float64) error {
	labels := parseMetricString(metric)
	return p.PutLabels(ts, labels, value)
}

// PutBatch stores multiple data points in parallel across partitions
func (p *PartitionedBadgerTSDB) PutBatch(points []storage.DataPoint) error {
	// Distribute points to partitions
	partitionPoints := make([][]storage.DataPoint, p.numPartitions)
	for i := range partitionPoints {
		partitionPoints[i] = make([]storage.DataPoint, 0)
	}

	for _, point := range points {
		seriesID := point.Labels.Hash()
		partitionIdx := p.getPartition(seriesID)
		partitionPoints[partitionIdx] = append(partitionPoints[partitionIdx], point)
	}

	// Parallel write to all partitions
	var wg sync.WaitGroup
	errors := make([]error, p.numPartitions)

	for i := 0; i < p.numPartitions; i++ {
		if len(partitionPoints[i]) == 0 {
			continue // Skip empty partitions
		}

		wg.Add(1)
		go func(partitionIdx int, points []storage.DataPoint) {
			defer wg.Done()
			errors[partitionIdx] = p.partitions[partitionIdx].PutBatch(points)
		}(i, partitionPoints[i])
	}

	wg.Wait()

	// Check for errors
	for i, err := range errors {
		if err != nil {
			return fmt.Errorf("partition %d write failed: %w", i, err)
		}
	}

	return nil
}

// TsdbPutBatch is an alias for PutBatch
func (p *PartitionedBadgerTSDB) TsdbPutBatch(points []storage.DataPoint) error {
	return p.PutBatch(points)
}

// GetLabels retrieves data points from all partitions and merges
func (p *PartitionedBadgerTSDB) GetLabels(labels storage.Labels, startTs, endTs int64) ([]int64, []float64, error) {
	seriesID := labels.Hash()
	partitionIdx := p.getPartition(seriesID)
	return p.partitions[partitionIdx].GetLabels(labels, startTs, endTs)
}

// Get retrieves data points (legacy API)
func (p *PartitionedBadgerTSDB) Get(metric string, startTs, endTs int64) ([]int64, []float64, error) {
	labels := parseMetricString(metric)
	return p.GetLabels(labels, startTs, endTs)
}

// GetMetrics returns all metrics from all partitions
func (p *PartitionedBadgerTSDB) GetMetrics() ([]string, error) {
	allMetrics := make(map[string]bool)
	
	for _, partition := range p.partitions {
		metrics, err := partition.GetMetrics()
		if err != nil {
			return nil, err
		}
		for _, metric := range metrics {
			allMetrics[metric] = true
		}
	}

	result := make([]string, 0, len(allMetrics))
	for metric := range allMetrics {
		result = append(result, metric)
	}
	return result, nil
}

// GetAllSeries returns all series from all partitions
func (p *PartitionedBadgerTSDB) GetAllSeries() ([]storage.Labels, error) {
	var allSeries []storage.Labels
	
	for _, partition := range p.partitions {
		series, err := partition.GetAllSeries()
		if err != nil {
			return nil, err
		}
		allSeries = append(allSeries, series...)
	}
	
	return allSeries, nil
}

// FindSeriesByLabels finds series across all partitions
func (p *PartitionedBadgerTSDB) FindSeriesByLabels(labelMatchers map[string]string) []uint64 {
	allSeriesIDs := make(map[uint64]bool)
	
	for _, partition := range p.partitions {
		seriesIDs := partition.FindSeriesByLabels(labelMatchers)
		for _, id := range seriesIDs {
			allSeriesIDs[id] = true
		}
	}

	result := make([]uint64, 0, len(allSeriesIDs))
	for id := range allSeriesIDs {
		result = append(result, id)
	}
	return result
}

// GetLabelsForSeriesID gets labels from the appropriate partition
func (p *PartitionedBadgerTSDB) GetLabelsForSeriesID(seriesID uint64) (storage.Labels, bool) {
	partitionIdx := p.getPartition(seriesID)
	return p.partitions[partitionIdx].GetLabelsForSeriesID(seriesID)
}

// Close closes all partitions
func (p *PartitionedBadgerTSDB) Close() error {
	var firstError error
	for i, partition := range p.partitions {
		if err := partition.Close(); err != nil && firstError == nil {
			firstError = fmt.Errorf("partition %d close failed: %w", i, err)
		}
	}
	return firstError
}

// RunGC runs GC on all partitions in parallel
func (p *PartitionedBadgerTSDB) RunGC() error {
	var wg sync.WaitGroup
	errors := make([]error, p.numPartitions)

	for i := 0; i < p.numPartitions; i++ {
		wg.Add(1)
		go func(partitionIdx int) {
			defer wg.Done()
			errors[partitionIdx] = p.partitions[partitionIdx].RunGC()
		}(i)
	}

	wg.Wait()

	for i, err := range errors {
		if err != nil {
			return fmt.Errorf("partition %d GC failed: %w", i, err)
		}
	}

	return nil
}
