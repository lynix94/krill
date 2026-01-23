package krill

import (
	"testing"
	"time"

	"github.com/lynix/krill/storage/memory"
)

func TestTSDBPut(t *testing.T) {
	db := NewTSDB()

	// Test basic insertion
	err := db.TsdbPut(1000, "cpu.usage", 45.5)
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

	if len(values) != 3 {
		t.Errorf("Expected 3 values, got %d", len(values))
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

func TestTSDBMultipleMetrics(t *testing.T) {
	db := NewTSDB()

	// Insert data for multiple metrics
	db.TsdbPut(1000, "cpu.usage", 45.5)
	db.TsdbPut(1000, "memory.usage", 70.2)
	db.TsdbPut(2000, "cpu.usage", 48.2)
	db.TsdbPut(2000, "memory.usage", 72.5)

	metrics, _ := db.GetMetrics()
	if len(metrics) != 2 {
		t.Errorf("Expected 2 metrics, got %d", len(metrics))
	}

	// Check cpu.usage
	timestamps, values, _ := db.Get("cpu.usage", 0, 0)
	if len(timestamps) != 2 || values[0] != 45.5 || values[1] != 48.2 {
		t.Error("CPU usage data incorrect")
	}

	// Check memory.usage
	timestamps, values, _ = db.Get("memory.usage", 0, 0)
	if len(timestamps) != 2 || values[0] != 70.2 || values[1] != 72.5 {
		t.Error("Memory usage data incorrect")
	}
}

func TestTSDBTimestampValidation(t *testing.T) {
	db := NewTSDB()

	db.TsdbPut(1000, "test.metric", 10.0)

	// Same timestamp is now allowed (idempotent write)
	err := db.TsdbPut(1000, "test.metric", 20.0)
	if err != nil {
		t.Errorf("Same timestamp should be allowed (idempotent), got error: %v", err)
	}

	// Should still have only one value (first one)
	_, values, _ := db.Get("test.metric", 0, 0)
	if len(values) != 1 {
		t.Errorf("Expected 1 value, got %d", len(values))
	}
	if values[0] != 10.0 {
		t.Errorf("Expected first value 10.0, got %f", values[0])
	}

	// Try to insert with earlier timestamp - should fail
	err = db.TsdbPut(500, "test.metric", 30.0)
	if err == nil {
		t.Error("Expected error for earlier timestamp, got nil")
	}
}

func TestTSDBCompression(t *testing.T) {
	db := NewTSDB()

	// Insert many data points with regular intervals and similar values
	baseTime := time.Now().Unix()
	for i := 0; i < 1000; i++ {
		ts := baseTime + int64(i*60) // 1 minute intervals
		value := 50.0 + float64(i%10) // Values vary slightly
		err := db.TsdbPut(ts, "sensor.temperature", value)
		if err != nil {
			t.Fatalf("Failed to insert value at index %d: %v", i, err)
		}
	}

	// Retrieve and verify
	timestamps, values, err := db.Get("sensor.temperature", 0, 0)
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

	// Check compression effectiveness with MemoryStorage
	memStorage, ok := db.storage.(*memory.MemoryStorage)
	if !ok {
		t.Skip("Skipping compression test - not using MemoryStorage")
		return
	}

	// Get internal metrics map to check compression
	series := memStorage.GetSeries("sensor.temperature")
	if series == nil {
		t.Fatal("Failed to get series for compression check")
	}

	compressedSize := len(series.TimestampStream.Bytes()) + len(series.ValueStream.Bytes())
	uncompressedSize := 1000 * (8 + 8) // 1000 points * (8 bytes timestamp + 8 bytes value)

	compressionRatio := float64(uncompressedSize) / float64(compressedSize)
	t.Logf("Compression ratio: %.2fx (uncompressed: %d bytes, compressed: %d bytes)",
		compressionRatio, uncompressedSize, compressedSize)

	// We expect at least 2x compression for regular time series data
	if compressionRatio < 2.0 {
		t.Errorf("Poor compression ratio: %.2fx (expected at least 2x)", compressionRatio)
	}
}

func TestTSDBEmptyMetric(t *testing.T) {
	db := NewTSDB()

	_, _, err := db.Get("nonexistent.metric", 0, 0)
	if err == nil {
		t.Error("Expected error for nonexistent metric, got nil")
	}
}
