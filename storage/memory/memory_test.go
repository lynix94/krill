package memory

import (
	"testing"
	"time"
)

func TestSameTimestamp(t *testing.T) {
	storage := NewMemoryStorage()

	now := time.Now().Unix()

	// Put multiple metrics with same timestamp
	err := storage.Put(now, "metric1", 100.0)
	if err != nil {
		t.Fatalf("Failed to put first metric: %v", err)
	}

	err = storage.Put(now, "metric2", 200.0)
	if err != nil {
		t.Fatalf("Failed to put second metric with same timestamp: %v", err)
	}

	err = storage.Put(now, "metric3", 300.0)
	if err != nil {
		t.Fatalf("Failed to put third metric with same timestamp: %v", err)
	}

	// Verify all metrics were stored
	metrics, err := storage.GetMetrics()
	if err != nil {
		t.Fatalf("Failed to get metrics: %v", err)
	}

	if len(metrics) != 3 {
		t.Errorf("Expected 3 metrics, got %d", len(metrics))
	}

	// Test updating same metric with same timestamp - should be ignored
	err = storage.Put(now, "metric1", 150.0)
	if err != nil {
		t.Fatalf("Failed to put metric with same timestamp (should be ignored): %v", err)
	}

	// Value should remain the same (first write wins)
	_, values, err := storage.Get("metric1", 0, 0)
	if err != nil {
		t.Fatalf("Failed to get metric1: %v", err)
	}

	if len(values) != 1 {
		t.Errorf("Expected 1 value for metric1, got %d", len(values))
	}

	if values[0] != 100.0 {
		t.Errorf("Expected original value 100.0, got %f (same timestamp should be ignored)", values[0])
	}
}

func TestOlderTimestamp(t *testing.T) {
	storage := NewMemoryStorage()

	now := time.Now().Unix()

	// Put a metric
	err := storage.Put(now, "metric1", 100.0)
	if err != nil {
		t.Fatalf("Failed to put metric: %v", err)
	}

	// Try to put with older timestamp - should fail
	err = storage.Put(now-10, "metric1", 200.0)
	if err == nil {
		t.Error("Expected error when putting older timestamp, got nil")
	}
}
