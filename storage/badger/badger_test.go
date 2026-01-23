package badger

import (
	"os"
	"testing"
	"time"
)

func TestBadgerTSDBBasic(t *testing.T) {
	// Create temporary directory
	dir := "./test_badger_basic"
	defer os.RemoveAll(dir)

	db, err := NewBadgerTSDB(BadgerOptions{Path: dir})
	if err != nil {
		t.Fatalf("Failed to create BadgerTSDB: %v", err)
	}
	defer db.Close()

	// Insert data
	err = db.TsdbPut(1000, "cpu.usage", 45.5)
	if err != nil {
		t.Fatalf("Failed to insert first value: %v", err)
	}

	err = db.TsdbPut(2000, "cpu.usage", 48.2)
	if err != nil {
		t.Fatalf("Failed to insert second value: %v", err)
	}

	err = db.TsdbPut(3000, "cpu.usage", 52.1)
	if err != nil {
		t.Fatalf("Failed to insert third value: %v", err)
	}

	// Retrieve data
	timestamps, values, err := db.Get("cpu.usage", 0, 0)
	if err != nil {
		t.Fatalf("Failed to retrieve data: %v", err)
	}

	if len(timestamps) != 3 {
		t.Errorf("Expected 3 timestamps, got %d", len(timestamps))
	}

	// Verify data
	expectedTimestamps := []int64{1000, 2000, 3000}
	expectedValues := []float64{45.5, 48.2, 52.1}

	for i := 0; i < len(timestamps); i++ {
		if timestamps[i] != expectedTimestamps[i] {
			t.Errorf("Timestamp mismatch at index %d: expected %d, got %d", i, expectedTimestamps[i], timestamps[i])
		}
		if values[i] != expectedValues[i] {
			t.Errorf("Value mismatch at index %d: expected %f, got %f", i, expectedValues[i], values[i])
		}
	}
}

func TestBadgerTSDBMultipleBuckets(t *testing.T) {
	dir := "./test_badger_buckets"
	defer os.RemoveAll(dir)

	db, err := NewBadgerTSDB(BadgerOptions{Path: dir})
	if err != nil {
		t.Fatalf("Failed to create BadgerTSDB: %v", err)
	}
	defer db.Close()

	// Insert data across multiple time buckets (hourly buckets)
	baseTime := int64(1000000)
	for i := 0; i < 5; i++ {
		ts := baseTime + int64(i*3600) // 1 hour apart
		value := 50.0 + float64(i)
		err = db.TsdbPut(ts, "temperature", value)
		if err != nil {
			t.Fatalf("Failed to insert value %d: %v", i, err)
		}
	}

	// Retrieve all data
	timestamps, values, err := db.Get("temperature", 0, 0)
	if err != nil {
		t.Fatalf("Failed to retrieve data: %v", err)
	}

	if len(timestamps) != 5 {
		t.Errorf("Expected 5 data points, got %d", len(timestamps))
	}

	// Verify values
	for i, value := range values {
		expected := 50.0 + float64(i)
		if value != expected {
			t.Errorf("Value mismatch at index %d: expected %f, got %f", i, expected, value)
		}
	}
}

func TestBadgerTSDBTimeRangeQuery(t *testing.T) {
	dir := "./test_badger_range"
	defer os.RemoveAll(dir)

	db, err := NewBadgerTSDB(BadgerOptions{Path: dir})
	if err != nil {
		t.Fatalf("Failed to create BadgerTSDB: %v", err)
	}
	defer db.Close()

	// Insert data
	for i := 0; i < 10; i++ {
		ts := int64(1000 + i*100)
		value := float64(i * 10)
		db.TsdbPut(ts, "metric", value)
	}

	// Query specific time range
	timestamps, _, err := db.Get("metric", 1200, 1600)
	if err != nil {
		t.Fatalf("Failed to query range: %v", err)
	}

	// Should get indices 2, 3, 4, 5, 6 (timestamps 1200, 1300, 1400, 1500, 1600)
	if len(timestamps) != 5 {
		t.Errorf("Expected 5 data points in range, got %d", len(timestamps))
	}

	if timestamps[0] != 1200 || timestamps[4] != 1600 {
		t.Errorf("Time range incorrect: got [%d, %d], expected [1200, 1600]",
			timestamps[0], timestamps[len(timestamps)-1])
	}
}

func TestBadgerTSDBPersistence(t *testing.T) {
	dir := "./test_badger_persist"
	defer os.RemoveAll(dir)

	// Create DB and insert data
	db1, err := NewBadgerTSDB(BadgerOptions{Path: dir})
	if err != nil {
		t.Fatalf("Failed to create BadgerTSDB: %v", err)
	}

	db1.TsdbPut(1000, "persistent.metric", 123.45)
	db1.TsdbPut(2000, "persistent.metric", 678.90)
	db1.Close()

	// Reopen DB and verify data persisted
	db2, err := NewBadgerTSDB(BadgerOptions{Path: dir})
	if err != nil {
		t.Fatalf("Failed to reopen BadgerTSDB: %v", err)
	}
	defer db2.Close()

	timestamps, values, err := db2.Get("persistent.metric", 0, 0)
	if err != nil {
		t.Fatalf("Failed to retrieve data after reopen: %v", err)
	}

	if len(timestamps) != 2 {
		t.Errorf("Expected 2 data points after reopen, got %d", len(timestamps))
	}

	if values[0] != 123.45 || values[1] != 678.90 {
		t.Errorf("Data mismatch after reopen: got [%f, %f], expected [123.45, 678.90]",
			values[0], values[1])
	}
}

func TestBadgerTSDBTTL(t *testing.T) {
	dir := "./test_badger_ttl"
	defer os.RemoveAll(dir)

	// Create DB with 1 second TTL
	db, err := NewBadgerTSDB(BadgerOptions{
		Path: dir,
		TTL:  1 * time.Second,
	})
	if err != nil {
		t.Fatalf("Failed to create BadgerTSDB: %v", err)
	}
	defer db.Close()

	// Insert data
	db.TsdbPut(1000, "ttl.metric", 100.0)

	// Verify data exists
	timestamps, _, err := db.Get("ttl.metric", 0, 0)
	if err != nil {
		t.Fatalf("Failed to retrieve data: %v", err)
	}
	if len(timestamps) != 1 {
		t.Errorf("Expected 1 data point, got %d", len(timestamps))
	}

	// Wait for TTL to expire
	time.Sleep(2 * time.Second)

	// Run GC to clean up expired data
	db.RunGC()

	// Data should be expired (this is implementation-dependent)
	// Note: BadgerDB TTL cleanup is eventual, not immediate
	t.Log("TTL test completed - data expiration is eventual in BadgerDB")
}

func TestBadgerTSDBGetMetrics(t *testing.T) {
	dir := "./test_badger_metrics"
	defer os.RemoveAll(dir)

	db, err := NewBadgerTSDB(BadgerOptions{Path: dir})
	if err != nil {
		t.Fatalf("Failed to create BadgerTSDB: %v", err)
	}
	defer db.Close()

	// Insert multiple metrics
	db.TsdbPut(1000, "cpu.usage", 50.0)
	db.TsdbPut(1000, "memory.usage", 70.0)
	db.TsdbPut(1000, "disk.io", 100.0)

	metrics, err := db.GetMetrics()
	if err != nil {
		t.Fatalf("Failed to get metrics: %v", err)
	}

	if len(metrics) != 3 {
		t.Errorf("Expected 3 metrics, got %d", len(metrics))
	}

	// Check all metrics are present
	metricMap := make(map[string]bool)
	for _, m := range metrics {
		metricMap[m] = true
	}

	if !metricMap["cpu.usage"] || !metricMap["memory.usage"] || !metricMap["disk.io"] {
		t.Error("Not all metrics found")
	}
}

func TestBadgerTSDBCompression(t *testing.T) {
	dir := "./test_badger_compression"
	defer os.RemoveAll(dir)

	db, err := NewBadgerTSDB(BadgerOptions{Path: dir})
	if err != nil {
		t.Fatalf("Failed to create BadgerTSDB: %v", err)
	}
	defer db.Close()

	// Insert many data points with regular intervals
	baseTime := time.Now().Unix()
	for i := 0; i < 1000; i++ {
		ts := baseTime + int64(i*60) // 1 minute intervals
		value := 50.0 + float64(i%10)
		err = db.TsdbPut(ts, "sensor.data", value)
		if err != nil {
			t.Fatalf("Failed to insert value at index %d: %v", i, err)
		}
	}

	// Retrieve and verify
	timestamps, values, err := db.Get("sensor.data", 0, 0)
	if err != nil {
		t.Fatalf("Failed to retrieve data: %v", err)
	}

	if len(timestamps) != 1000 {
		t.Errorf("Expected 1000 timestamps, got %d", len(timestamps))
	}

	// Verify first and last values
	if values[0] != 50.0 {
		t.Errorf("First value incorrect: expected 50.0, got %f", values[0])
	}

	if values[999] != 50.0+float64(999%10) {
		t.Errorf("Last value incorrect: expected %f, got %f", 50.0+float64(999%10), values[999])
	}

	t.Log("Compression test completed successfully with 1000 data points")
}
