package scraper

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Metric represents a parsed Prometheus metric
type Metric struct {
	Name      string
	Labels    map[string]string
	Value     float64
	Timestamp int64
}

// ParsePrometheusMetrics parses Prometheus text format metrics
func ParsePrometheusMetrics(reader io.Reader) ([]Metric, error) {
	var metrics []Metric
	scanner := bufio.NewScanner(reader)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		metric, err := parseMetricLine(line)
		if err != nil {
			// Log error but continue parsing
			continue
		}

		metrics = append(metrics, metric)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading metrics: %w", err)
	}

	return metrics, nil
}

// parseMetricLine parses a single metric line
// Format: metric_name{label1="value1",label2="value2"} value timestamp
func parseMetricLine(line string) (Metric, error) {
	var metric Metric
	metric.Labels = make(map[string]string)

	// Find the position of '{'
	labelStart := strings.Index(line, "{")
	spacePos := strings.Index(line, " ")

	var nameEnd int
	if labelStart > 0 && (spacePos < 0 || labelStart < spacePos) {
		// Metric has labels
		nameEnd = labelStart
		labelEnd := strings.Index(line[labelStart:], "}")
		if labelEnd < 0 {
			return metric, fmt.Errorf("invalid metric format: missing }")
		}
		labelEnd += labelStart

		// Extract metric name
		metric.Name = line[:nameEnd]

		// Parse labels
		labelsStr := line[labelStart+1 : labelEnd]
		if err := parseLabels(labelsStr, metric.Labels); err != nil {
			return metric, err
		}

		// Parse value and timestamp
		rest := strings.TrimSpace(line[labelEnd+1:])
		if err := parseValueTimestamp(rest, &metric); err != nil {
			return metric, err
		}
	} else {
		// No labels
		parts := strings.Fields(line)
		if len(parts) < 2 {
			return metric, fmt.Errorf("invalid metric format")
		}

		metric.Name = parts[0]
		
		value, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			return metric, fmt.Errorf("invalid value: %w", err)
		}
		metric.Value = value

		// Optional timestamp
		if len(parts) >= 3 {
			timestamp, err := strconv.ParseInt(parts[2], 10, 64)
			if err == nil {
				metric.Timestamp = timestamp
			}
		}
	}

	return metric, nil
}

// parseLabels parses label pairs
func parseLabels(labelsStr string, labels map[string]string) error {
	if labelsStr == "" {
		return nil
	}

	// Simple parser for labels: key1="value1",key2="value2"
	pairs := strings.Split(labelsStr, ",")
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}

		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), "\"")
		labels[key] = value
	}

	return nil
}

// parseValueTimestamp parses value and optional timestamp
func parseValueTimestamp(str string, metric *Metric) error {
	parts := strings.Fields(str)
	if len(parts) < 1 {
		return fmt.Errorf("missing value")
	}

	value, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return fmt.Errorf("invalid value: %w", err)
	}
	metric.Value = value

	// Optional timestamp
	if len(parts) >= 2 {
		timestamp, err := strconv.ParseInt(parts[1], 10, 64)
		if err == nil {
			metric.Timestamp = timestamp
		}
	}

	return nil
}

// FormatMetricName formats metric name with labels as a flat string
func FormatMetricName(name string, labels map[string]string, prefix string) string {
	if prefix != "" {
		name = prefix + "." + name
	}

	// Replace common Prometheus metric name characters
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, ":", "_")

	// Optionally append important labels to metric name
	if jobLabel, ok := labels["job"]; ok {
		name = name + "." + jobLabel
	}
	if instance, ok := labels["instance"]; ok {
		// Remove port from instance
		if idx := strings.Index(instance, ":"); idx > 0 {
			instance = instance[:idx]
		}
		instance = strings.ReplaceAll(instance, ".", "_")
		name = name + "." + instance
	}

	return name
}
