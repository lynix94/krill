package scraper

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestParsePrometheusMetrics(t *testing.T) {
	input := `# HELP http_requests_total Total HTTP requests
# TYPE http_requests_total counter
http_requests_total{method="GET",status="200"} 1234
http_requests_total{method="POST",status="201"} 567

# HELP cpu_usage CPU usage percentage
# TYPE cpu_usage gauge
cpu_usage 45.5

# HELP memory_bytes Memory usage in bytes
memory_bytes{type="heap"} 8388608 1234567890
`

	metrics, err := ParsePrometheusMetrics(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Failed to parse metrics: %v", err)
	}

	if len(metrics) != 4 {
		t.Errorf("Expected 4 metrics, got %d", len(metrics))
	}

	// Test first metric
	if metrics[0].Name != "http_requests_total" {
		t.Errorf("Expected name 'http_requests_total', got '%s'", metrics[0].Name)
	}
	if metrics[0].Value != 1234 {
		t.Errorf("Expected value 1234, got %f", metrics[0].Value)
	}
	if metrics[0].Labels["method"] != "GET" {
		t.Errorf("Expected method=GET, got %s", metrics[0].Labels["method"])
	}

	// Test metric without labels
	if metrics[2].Name != "cpu_usage" {
		t.Errorf("Expected name 'cpu_usage', got '%s'", metrics[2].Name)
	}
	if metrics[2].Value != 45.5 {
		t.Errorf("Expected value 45.5, got %f", metrics[2].Value)
	}

	// Test metric with timestamp
	if metrics[3].Timestamp != 1234567890 {
		t.Errorf("Expected timestamp 1234567890, got %d", metrics[3].Timestamp)
	}
}

func TestFormatMetricName(t *testing.T) {
	tests := []struct {
		name     string
		labels   map[string]string
		prefix   string
		expected string
	}{
		{
			name:     "http_requests_total",
			labels:   map[string]string{"job": "api", "instance": "localhost:8080"},
			prefix:   "app",
			expected: "app.http_requests_total.api.localhost",
		},
		{
			name:     "cpu-usage:rate",
			labels:   map[string]string{},
			prefix:   "",
			expected: "cpu_usage_rate",
		},
	}

	for _, tt := range tests {
		result := FormatMetricName(tt.name, tt.labels, tt.prefix)
		if result != tt.expected {
			t.Errorf("FormatMetricName(%s) = %s, want %s", tt.name, result, tt.expected)
		}
	}
}

func TestLoadConfig(t *testing.T) {
	// Create a temporary config file
	configYAML := `
global:
  scrape_interval: 30s
  scrape_timeout: 10s

scrape_configs:
  - job_name: test-job
    metrics_path: /metrics
    static_configs:
      - targets:
          - localhost:9090
        labels:
          env: test
`

	// Write to temp file
	tmpfile := "/tmp/test-scraper-config.yaml"
	if err := os.WriteFile(tmpfile, []byte(configYAML), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpfile)

	config, err := LoadConfig(tmpfile)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if config.Global.ScrapeInterval != 30*time.Second {
		t.Errorf("Expected scrape_interval 30s, got %v", config.Global.ScrapeInterval)
	}

	if len(config.ScrapeConfigs) != 1 {
		t.Errorf("Expected 1 scrape config, got %d", len(config.ScrapeConfigs))
	}

	if config.ScrapeConfigs[0].JobName != "test-job" {
		t.Errorf("Expected job_name 'test-job', got '%s'", config.ScrapeConfigs[0].JobName)
	}
}
