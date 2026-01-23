package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lynix/krill"
)

// PrometheusHandler handles Prometheus-compatible API requests
type PrometheusHandler struct {
	tsdb krill.QueryableDB
}

// NewPrometheusHandler creates a new Prometheus API handler
func NewPrometheusHandler(tsdb krill.QueryableDB) *PrometheusHandler {
	return &PrometheusHandler{
		tsdb: tsdb,
	}
}

// PrometheusResponse represents the standard Prometheus API response
type PrometheusResponse struct {
	Status string      `json:"status"`
	Data   interface{} `json:"data,omitempty"`
	Error  string      `json:"error,omitempty"`
}

// QueryResult represents a single time series result
type QueryResult struct {
	Metric map[string]string `json:"metric"`
	Values [][]interface{}   `json:"values,omitempty"`
	Value  []interface{}     `json:"value,omitempty"`
}

// QueryData represents the data portion of a query response
type QueryData struct {
	ResultType string        `json:"resultType"`
	Result     []QueryResult `json:"result"`
}

// HandleQuery handles instant queries: GET /api/v1/query
func (ph *PrometheusHandler) HandleQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		ph.sendError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	query := r.URL.Query().Get("query")
	if query == "" {
		ph.sendError(w, http.StatusBadRequest, "query parameter is required")
		return
	}

	// Parse time parameter (default to now)
	timeParam := r.URL.Query().Get("time")
	queryTime := time.Now().Unix()
	if timeParam != "" {
		t, err := strconv.ParseInt(timeParam, 10, 64)
		if err == nil {
			queryTime = t
		}
	}

	// Parse query to extract metric name and label matchers
	metricName, labelMatchers := parseQuery(query)

	// Get all metrics and filter by label matchers
	metrics, err := ph.tsdb.GetMetrics()
	if err != nil {
		ph.sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Filter metrics by name and labels
	var result []QueryResult
	for _, metric := range metrics {
		if matchesQuery(metric, metricName, labelMatchers) {
			// Query TSDB for this specific metric
			timestamps, values, err := ph.tsdb.Get(metric, 0, queryTime)
			if err != nil {
				continue
			}

			// Get the latest value
			if len(values) > 0 {
				lastIdx := len(values) - 1
				// Parse metric name and tags from stored key
				parsedName, parsedTags := parseMetricKey(metric)
				parsedTags["__name__"] = parsedName
				result = append(result, QueryResult{
					Metric: parsedTags,
					Value:  []interface{}{timestamps[lastIdx], fmt.Sprintf("%f", values[lastIdx])},
				})
			}
		}
	}

	ph.sendSuccess(w, QueryData{
		ResultType: "vector",
		Result:     result,
	})
}

// HandleQueryRange handles range queries: GET /api/v1/query_range
func (ph *PrometheusHandler) HandleQueryRange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		ph.sendError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	query := r.URL.Query().Get("query")
	if query == "" {
		ph.sendError(w, http.StatusBadRequest, "query parameter is required")
		return
	}

	// Parse start and end times
	startParam := r.URL.Query().Get("start")
	endParam := r.URL.Query().Get("end")

	var start, end int64
	var err error

	if startParam != "" {
		start, err = strconv.ParseInt(startParam, 10, 64)
		if err != nil {
			ph.sendError(w, http.StatusBadRequest, "invalid start parameter")
			return
		}
	}

	if endParam != "" {
		end, err = strconv.ParseInt(endParam, 10, 64)
		if err != nil {
			ph.sendError(w, http.StatusBadRequest, "invalid end parameter")
			return
		}
	} else {
		end = time.Now().Unix()
	}

	// Parse query to extract metric name and label matchers
	metricName, labelMatchers := parseQuery(query)

	// Get all metrics and filter by label matchers
	metrics, err := ph.tsdb.GetMetrics()
	if err != nil {
		ph.sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Filter metrics by name and labels
	var result []QueryResult
	for _, metric := range metrics {
		if matchesQuery(metric, metricName, labelMatchers) {
			// Query TSDB for this specific metric
			timestamps, values, err := ph.tsdb.Get(metric, start, end)
			if err != nil {
				continue
			}

			if len(values) > 0 {
				valuesArray := make([][]interface{}, len(values))
				for i := range values {
					valuesArray[i] = []interface{}{timestamps[i], fmt.Sprintf("%f", values[i])}
				}
				// Parse metric name and tags from stored key
				parsedName, parsedTags := parseMetricKey(metric)
				parsedTags["__name__"] = parsedName
				result = append(result, QueryResult{
					Metric: parsedTags,
					Values: valuesArray,
				})
			}
		}
	}

	ph.sendSuccess(w, QueryData{
		ResultType: "matrix",
		Result:     result,
	})
}

// WriteRequest represents a simplified remote write request
type WriteRequest struct {
	Metric string            `json:"metric"`
	Value  float64           `json:"value"`
	Time   int64             `json:"time,omitempty"`
	Tags   map[string]string `json:"tags,omitempty"`
}

// HandleWrite handles write requests: POST /api/v1/write
func (ph *PrometheusHandler) HandleWrite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		ph.sendError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req WriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ph.sendError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if req.Metric == "" {
		ph.sendError(w, http.StatusBadRequest, "metric name is required")
		return
	}

	// Use current time if not specified
	if req.Time == 0 {
		req.Time = time.Now().Unix()
	}

	// Convert to Labels format internally
	// Build metric key with tags for backward compatibility
	metricKey := buildMetricKey(req.Metric, req.Tags)

	// Write to TSDB (TSDB will convert to Labels internally)
	if err := ph.tsdb.TsdbPut(req.Time, metricKey, req.Value); err != nil {
		ph.sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	ph.sendSuccess(w, nil)
}

// HandleMetrics returns list of all metrics: GET /api/v1/label/__name__/values
func (ph *PrometheusHandler) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		ph.sendError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	metrics, err := ph.tsdb.GetMetrics()
	if err != nil {
		ph.sendError(w, http.StatusInternalServerError, err.Error())
		return
	}
	ph.sendSuccess(w, metrics)
}

// buildMetricKey creates a metric key with tags
// Format: metric_name{tag1="value1",tag2="value2"}
func buildMetricKey(name string, tags map[string]string) string {
	if len(tags) == 0 {
		return name
	}

	// Sort tags for consistent ordering
	var sortedTags []string
	for k, v := range tags {
		sortedTags = append(sortedTags, fmt.Sprintf("%s=\"%s\"", k, v))
	}
	
	// Simple sort (for consistency)
	for i := 0; i < len(sortedTags); i++ {
		for j := i + 1; j < len(sortedTags); j++ {
			if sortedTags[i] > sortedTags[j] {
				sortedTags[i], sortedTags[j] = sortedTags[j], sortedTags[i]
			}
		}
	}

	return fmt.Sprintf("%s{%s}", name, strings.Join(sortedTags, ","))
}

// parseMetricKey parses a metric key back into name and tags
// Format: metric_name{tag1="value1",tag2="value2"}
func parseMetricKey(key string) (string, map[string]string) {
	tags := make(map[string]string)
	
	// Find { position
	bracePos := strings.Index(key, "{")
	if bracePos < 0 {
		// No tags
		return key, tags
	}
	
	name := key[:bracePos]
	
	// Find } position
	endPos := strings.Index(key[bracePos:], "}")
	if endPos < 0 {
		return name, tags
	}
	endPos += bracePos
	
	// Parse tags
	tagStr := key[bracePos+1 : endPos]
	if tagStr == "" {
		return name, tags
	}
	
	// Split by comma (simple parser, doesn't handle escaped quotes)
	pairs := strings.Split(tagStr, ",")
	for _, pair := range pairs {
		// Split by =
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			continue
		}
		k := strings.TrimSpace(parts[0])
		v := strings.Trim(strings.TrimSpace(parts[1]), "\"")
		tags[k] = v
	}
	
	return name, tags
}

// parseQuery parses a Prometheus query to extract metric name and label matchers
// Format: metric_name{label1="value1",label2="value2"}
func parseQuery(query string) (string, map[string]string) {
	return parseMetricKey(query)
}

// matchesQuery checks if a metric key matches the query (name and label matchers)
func matchesQuery(metricKey string, queryName string, labelMatchers map[string]string) bool {
	parsedName, parsedTags := parseMetricKey(metricKey)
	
	// Check name match (empty query name matches all)
	if queryName != "" && parsedName != queryName {
		return false
	}
	
	// Check all label matchers
	for k, v := range labelMatchers {
		if parsedTags[k] != v {
			return false
		}
	}
	
	return true
}

func (ph *PrometheusHandler) sendSuccess(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(PrometheusResponse{
		Status: "success",
		Data:   data,
	})
}

func (ph *PrometheusHandler) sendError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(PrometheusResponse{
		Status: "error",
		Error:  message,
	})
}
