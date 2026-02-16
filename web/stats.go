package web

import (
	"fmt"
	"runtime"
	"sync/atomic"
	"time"
)

// Stats holds internal metrics for Krill TSDB
type Stats struct {
	// Query metrics
	queriesInstant    atomic.Uint64
	queriesRange      atomic.Uint64
	queryDurationSum  atomic.Uint64 // nanoseconds
	queryDurationMax  atomic.Uint64 // nanoseconds
	
	// Write metrics
	writesSingle      atomic.Uint64
	writesBatch       atomic.Uint64
	datapointsWritten atomic.Uint64
	
	// GC metrics
	gcRuns            atomic.Uint64
	gcDurationSum     atomic.Uint64 // nanoseconds
	
	startTime         int64 // Unix timestamp
}

// NewStats creates a new Stats instance
func NewStats() *Stats {
	return &Stats{
		startTime: time.Now().Unix(),
	}
}

// RecordInstantQuery records an instant query
func (s *Stats) RecordInstantQuery(duration time.Duration) {
	s.queriesInstant.Add(1)
	s.recordQueryDuration(duration)
}

// RecordRangeQuery records a range query
func (s *Stats) RecordRangeQuery(duration time.Duration) {
	s.queriesRange.Add(1)
	s.recordQueryDuration(duration)
}

func (s *Stats) recordQueryDuration(duration time.Duration) {
	nanos := uint64(duration.Nanoseconds())
	s.queryDurationSum.Add(nanos)
	
	// Update max duration
	for {
		oldMax := s.queryDurationMax.Load()
		if nanos <= oldMax {
			break
		}
		if s.queryDurationMax.CompareAndSwap(oldMax, nanos) {
			break
		}
	}
}

// RecordSingleWrite records a single write request
func (s *Stats) RecordSingleWrite(datapoints int) {
	s.writesSingle.Add(1)
	s.datapointsWritten.Add(uint64(datapoints))
}

// RecordBatchWrite records a batch write request
func (s *Stats) RecordBatchWrite(datapoints int) {
	s.writesBatch.Add(1)
	s.datapointsWritten.Add(uint64(datapoints))
}

// RecordGC records a garbage collection run
func (s *Stats) RecordGC(duration time.Duration) {
	s.gcRuns.Add(1)
	s.gcDurationSum.Add(uint64(duration.Nanoseconds()))
}

// ToPrometheusFormat exports stats in Prometheus text format
func (s *Stats) ToPrometheusFormat(seriesCount, metricCount int) string {
	now := time.Now().Unix()
	uptime := now - s.startTime
	
	queriesInstant := s.queriesInstant.Load()
	queriesRange := s.queriesRange.Load()
	totalQueries := queriesInstant + queriesRange
	queryDurationSum := s.queryDurationSum.Load()
	queryDurationMax := s.queryDurationMax.Load()
	
	writesSingle := s.writesSingle.Load()
	writesBatch := s.writesBatch.Load()
	datapointsWritten := s.datapointsWritten.Load()
	
	gcRuns := s.gcRuns.Load()
	gcDurationSum := s.gcDurationSum.Load()
	
	var avgQueryDuration float64
	if totalQueries > 0 {
		avgQueryDuration = float64(queryDurationSum) / float64(totalQueries) / 1e9 // convert to seconds
	}
	
	var avgGCDuration float64
	if gcRuns > 0 {
		avgGCDuration = float64(gcDurationSum) / float64(gcRuns) / 1e9
	}
	
	// Get memory statistics
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	
	return fmt.Sprintf(`# HELP krill_uptime_seconds Time since server started
# TYPE krill_uptime_seconds gauge
krill_uptime_seconds %d

# HELP krill_queries_total Total number of queries
# TYPE krill_queries_total counter
krill_queries_total{type="instant"} %d
krill_queries_total{type="range"} %d

# HELP krill_query_duration_seconds Query execution time
# TYPE krill_query_duration_seconds summary
krill_query_duration_seconds{quantile="avg"} %.6f
krill_query_duration_seconds{quantile="max"} %.6f
krill_query_duration_seconds_sum %.6f
krill_query_duration_seconds_count %d

# HELP krill_writes_total Total number of write requests
# TYPE krill_writes_total counter
krill_writes_total{type="single"} %d
krill_writes_total{type="batch"} %d

# HELP krill_datapoints_written_total Total number of datapoints written
# TYPE krill_datapoints_written_total counter
krill_datapoints_written_total %d

# HELP krill_series_count Current number of time series
# TYPE krill_series_count gauge
krill_series_count %d

# HELP krill_metric_count Current number of unique metrics
# TYPE krill_metric_count gauge
krill_metric_count %d

# HELP krill_memory_alloc_bytes Currently allocated memory in bytes
# TYPE krill_memory_alloc_bytes gauge
krill_memory_alloc_bytes %d

# HELP krill_memory_sys_bytes Total memory obtained from OS in bytes
# TYPE krill_memory_sys_bytes gauge
krill_memory_sys_bytes %d

# HELP krill_memory_heap_alloc_bytes Heap memory allocated in bytes
# TYPE krill_memory_heap_alloc_bytes gauge
krill_memory_heap_alloc_bytes %d

# HELP krill_memory_heap_inuse_bytes Heap memory in use in bytes
# TYPE krill_memory_heap_inuse_bytes gauge
krill_memory_heap_inuse_bytes %d

# HELP krill_memory_stack_inuse_bytes Stack memory in use in bytes
# TYPE krill_memory_stack_inuse_bytes gauge
krill_memory_stack_inuse_bytes %d

# HELP krill_go_gc_runs_total Total number of Go GC runs
# TYPE krill_go_gc_runs_total counter
krill_go_gc_runs_total %d

# HELP krill_go_goroutines Current number of goroutines
# TYPE krill_go_goroutines gauge
krill_go_goroutines %d

# HELP krill_gc_runs_total Total number of BadgerDB garbage collection runs
# TYPE krill_gc_runs_total counter
krill_gc_runs_total %d

# HELP krill_gc_duration_seconds BadgerDB garbage collection execution time
# TYPE krill_gc_duration_seconds summary
krill_gc_duration_seconds{quantile="avg"} %.6f
krill_gc_duration_seconds_sum %.6f
krill_gc_duration_seconds_count %d
`,
		uptime,
		queriesInstant,
		queriesRange,
		avgQueryDuration,
		float64(queryDurationMax)/1e9,
		float64(queryDurationSum)/1e9,
		totalQueries,
		writesSingle,
		writesBatch,
		datapointsWritten,
		seriesCount,
		metricCount,
		m.Alloc,
		m.Sys,
		m.HeapAlloc,
		m.HeapInuse,
		m.StackInuse,
		m.NumGC,
		runtime.NumGoroutine(),
		gcRuns,
		avgGCDuration,
		float64(gcDurationSum)/1e9,
		gcRuns,
	)
}
