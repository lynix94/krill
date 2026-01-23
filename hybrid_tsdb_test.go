package krill

import (
	"os"
	"testing"
	"time"
)

func TestHybridTSDBBasic(t *testing.T) {
	dir := "./test_hybrid_basic"
	defer os.RemoveAll(dir)

	db, err := NewHybridTSDB(HybridOptions{
		PersistencePath: dir,
		CacheDuration:   1 * time.Hour,
		CleanupInterval: 1 * time.Second,
	})
	if err != nil {
		t.Fatalf("Failed to create HybridTSDB: %v", err)
	}
	defer db.Close()

	// Insert data
	now := time.Now().Unix()
	err = db.TsdbPut(now-3600, "cpu.usage", 45.5) // 1 hour ago
	if err != nil {
		t.Fatalf("Failed to insert data: %v", err)
	}

	err = db.TsdbPut(now-1800, "cpu.usage", 48.2) // 30 min ago
	if err != nil {
		t.Fatalf("Failed to insert data: %v", err)
	}

	err = db.TsdbPut(now, "cpu.usage", 52.1) // now
	if err != nil {
		t.Fatalf("Failed to insert data: %v", err)
	}

	// Retrieve all data
	timestamps, values, err := db.Get("cpu.usage", 0, 0)
	if err != nil {
		t.Fatalf("Failed to retrieve data: %v", err)
	}

	if len(timestamps) != 3 {
		t.Errorf("Expected 3 data points, got %d", len(timestamps))
	}

	if values[0] != 45.5 || values[1] != 48.2 || values[2] != 52.1 {
		t.Errorf("Values mismatch: got %v", values)
	}
}

func TestHybridTSDBCacheQuery(t *testing.T) {
	dir := "./test_hybrid_cache"
	defer os.RemoveAll(dir)

	db, err := NewHybridTSDB(HybridOptions{
		PersistencePath: dir,
		CacheDuration:   2 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Failed to create HybridTSDB: %v", err)
	}
	defer db.Close()

	now := time.Now().Unix()

	// Insert old data (beyond cache)
	db.TsdbPut(now-10800, "temp", 20.0) // 3 hours ago
	db.TsdbPut(now-7200, "temp", 21.0)  // 2 hours ago (boundary)
	
	// Insert recent data (in cache)
	db.TsdbPut(now-3600, "temp", 22.0)  // 1 hour ago
	db.TsdbPut(now-1800, "temp", 23.0)  // 30 min ago
	db.TsdbPut(now, "temp", 24.0)       // now

	// Query only recent data (should use cache)
	timestamps, values, err := db.Get("temp", now-3600, 0)
	if err != nil {
		t.Fatalf("Failed to query recent data: %v", err)
	}

	if len(timestamps) != 3 {
		t.Errorf("Expected 3 recent data points, got %d", len(timestamps))
	}

	// Query all data (should use both cache and persistence)
	timestamps, values, err = db.Get("temp", 0, 0)
	if err != nil {
		t.Fatalf("Failed to query all data: %v", err)
	}

	if len(timestamps) != 5 {
		t.Errorf("Expected 5 total data points, got %d", len(timestamps))
	}

	// Verify values are in order
	expectedValues := []float64{20.0, 21.0, 22.0, 23.0, 24.0}
	for i, v := range values {
		if v != expectedValues[i] {
			t.Errorf("Value mismatch at index %d: expected %f, got %f", i, expectedValues[i], v)
		}
	}
}

func TestHybridTSDBPersistence(t *testing.T) {
	dir := "./test_hybrid_persist"
	defer os.RemoveAll(dir)

	// Create and populate DB
	db1, err := NewHybridTSDB(HybridOptions{
		PersistencePath: dir,
		CacheDuration:   1 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Failed to create HybridTSDB: %v", err)
	}

	now := time.Now().Unix()
	
	// Put old data that should go to persistence (outside cache window)
	db1.TsdbPut(now-7200, "persist.metric", 100.0)
	db1.TsdbPut(now-7100, "persist.metric", 150.0)
	
	// Wait a bit to ensure data is written to persistence
	time.Sleep(100 * time.Millisecond)
	
	db1.Close()

	// Reopen and verify data persisted
	db2, err := NewHybridTSDB(HybridOptions{
		PersistencePath: dir,
		CacheDuration:   1 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Failed to reopen HybridTSDB: %v", err)
	}
	defer db2.Close()

	timestamps, values, err := db2.Get("persist.metric", 0, 0)
	if err != nil {
		t.Fatalf("Failed to retrieve data after reopen: %v", err)
	}

	if len(timestamps) != 2 {
		t.Errorf("Expected 2 data points after reopen, got %d", len(timestamps))
		return
	}

	if values[0] != 100.0 || values[1] != 150.0 {
		t.Errorf("Values mismatch after reopen: expected [100.0, 150.0], got %v", values)
	}
}

func TestHybridTSDBGetMetrics(t *testing.T) {
	dir := "./test_hybrid_metrics"
	defer os.RemoveAll(dir)

	db, err := NewHybridTSDB(HybridOptions{
		PersistencePath: dir,
		CacheDuration:   1 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Failed to create HybridTSDB: %v", err)
	}
	defer db.Close()

	now := time.Now().Unix()

	// Insert metrics in different time ranges
	db.TsdbPut(now-7200, "old.metric", 1.0)  // Only in persistence
	db.TsdbPut(now, "new.metric", 2.0)       // In both
	db.TsdbPut(now, "cache.metric", 3.0)     // In both

	metrics, err := db.GetMetrics()
	if err != nil {
		t.Fatalf("Failed to get metrics: %v", err)
	}

	if len(metrics) != 3 {
		t.Errorf("Expected 3 metrics, got %d: %v", len(metrics), metrics)
	}

	// Verify all metrics are present
	metricsMap := make(map[string]bool)
	for _, m := range metrics {
		metricsMap[m] = true
	}

	if !metricsMap["old.metric"] || !metricsMap["new.metric"] || !metricsMap["cache.metric"] {
		t.Error("Not all metrics found")
	}
}

func TestHybridTSDBCleanup(t *testing.T) {
	dir := "./test_hybrid_cleanup"
	defer os.RemoveAll(dir)

	db, err := NewHybridTSDB(HybridOptions{
		PersistencePath: dir,
		CacheDuration:   1 * time.Second, // Very short cache
		CleanupInterval: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Failed to create HybridTSDB: %v", err)
	}
	defer db.Close()

	now := time.Now().Unix()
	db.TsdbPut(now, "test", 1.0)

	// Wait for cleanup to run
	time.Sleep(2 * time.Second)

	// Force cleanup
	db.cleanupOldCache()

	// Data should still be in persistent storage
	timestamps, values, err := db.Get("test", 0, 0)
	if err != nil {
		t.Fatalf("Failed to get data after cleanup: %v", err)
	}

	if len(timestamps) != 1 || values[0] != 1.0 {
		t.Errorf("Data lost after cleanup")
	}
}
