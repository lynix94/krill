package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
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

	// Parse query with aggregation support
	parsed := parsePromQL(query)

	// Get all metrics and filter by label matchers
	metrics, err := ph.tsdb.GetMetrics()
	if err != nil {
		ph.sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Determine query time range
	var startTs, endTs int64
	if parsed.RangeVector > 0 {
		// Range vector function: get data from [queryTime - range] to queryTime
		endTs = queryTime
		startTs = queryTime - parsed.RangeVector
	} else {
		// Normal instant query: just get latest value
		startTs = 0
		endTs = queryTime
	}

	// Filter metrics by name and labels
	var result []QueryResult
	for _, metric := range metrics {
		if matchesQuery(metric, parsed.MetricName, parsed.LabelMatchers) {
			// Query TSDB for this specific metric
			timestamps, values, err := ph.tsdb.Get(metric, startTs, endTs)
			if err != nil {
				continue
			}

			if len(values) > 0 {
				// Parse metric name and tags from stored key
				parsedName, parsedTags := parseMetricKey(metric)
				parsedTags["__name__"] = parsedName

				if parsed.RangeVector > 0 {
					// Range vector: return all values in range
					valuesArray := make([][]interface{}, len(values))
					for i := range values {
						valuesArray[i] = []interface{}{timestamps[i], fmt.Sprintf("%f", values[i])}
					}
					result = append(result, QueryResult{
						Metric: parsedTags,
						Values: valuesArray,
					})
				} else {
					// Instant query: return only latest value
					lastIdx := len(values) - 1
					result = append(result, QueryResult{
						Metric: parsedTags,
						Value:  []interface{}{timestamps[lastIdx], fmt.Sprintf("%f", values[lastIdx])},
					})
				}
			}
		}
	}

	// Apply aggregation if specified
	result = applyAggregation(parsed, result)

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
	stepParam := r.URL.Query().Get("step")

	var start, end int64
	var step int64 = 0 // 0 means no step (return all data)
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

	if stepParam != "" {
		step, err = strconv.ParseInt(stepParam, 10, 64)
		if err != nil {
			ph.sendError(w, http.StatusBadRequest, "invalid step parameter")
			return
		}
		if step <= 0 {
			ph.sendError(w, http.StatusBadRequest, "step must be positive")
			return
		}
	}

	// Parse query with aggregation support
	parsed := parsePromQL(query)

	// Check if this is a rate/irate query with step parameter
	if step > 0 && (parsed.AggFunc == AggRate || parsed.AggFunc == AggIrate) {
		result := ph.evaluateRateWithStep(parsed, start, end, step)
		ph.sendSuccess(w, QueryData{
			ResultType: "matrix",
			Result:     result,
		})
		return
	}

	// Get all metrics and filter by label matchers
	metrics, err := ph.tsdb.GetMetrics()
	if err != nil {
		ph.sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Filter metrics by name and labels
	var result []QueryResult
	for _, metric := range metrics {
		if matchesQuery(metric, parsed.MetricName, parsed.LabelMatchers) {
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

	// Apply aggregation if specified
	result = applyAggregation(parsed, result)

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

// HandleMetrics returns list of all metrics: GET /api/v1/label/__name__/values or GET /api/v1/metrics
// Supports optional 'filter' query parameter with regex pattern
func (ph *PrometheusHandler) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		ph.sendError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Get filter parameter (optional)
	filterParam := r.URL.Query().Get("filter")

	// Get all metrics from TSDB
	allMetrics, err := ph.tsdb.GetMetrics()
	if err != nil {
		ph.sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var filteredMetrics []string

	// If no filter specified, return all metrics
	if filterParam == "" {
		filteredMetrics = allMetrics
	} else {
		// Compile regex filter
		filterRegex, err := regexp.Compile(filterParam)
		if err != nil {
			ph.sendError(w, http.StatusBadRequest, fmt.Sprintf("invalid filter regex: %v", err))
			return
		}

		// Filter metrics using regex
		for _, metric := range allMetrics {
			if filterRegex.MatchString(metric) {
				filteredMetrics = append(filteredMetrics, metric)
			}
		}
	}

	ph.sendSuccess(w, filteredMetrics)
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

// evaluateRateWithStep evaluates rate/irate at multiple time steps
func (ph *PrometheusHandler) evaluateRateWithStep(parsed ParsedQuery, start, end, step int64) []QueryResult {
	var results []QueryResult

	// Get all matching metrics
	metrics, err := ph.tsdb.GetMetrics()
	if err != nil {
		return results
	}

	// Filter metrics by name and labels
	for _, metric := range metrics {
		if !matchesQuery(metric, parsed.MetricName, parsed.LabelMatchers) {
			continue
		}

		// Parse metric labels
		parsedName, parsedTags := parseMetricKey(metric)
		parsedTags["__name__"] = parsedName

		// Calculate rate/irate at each evaluation time
		var evaluationTimes []int64
		for evalTime := start; evalTime <= end; evalTime += step {
			evaluationTimes = append(evaluationTimes, evalTime)
		}
		// Always include end time if not already included
		if len(evaluationTimes) == 0 || evaluationTimes[len(evaluationTimes)-1] != end {
			evaluationTimes = append(evaluationTimes, end)
		}

		var valuesArray [][]interface{}
		
		for _, evalTime := range evaluationTimes {
			// Get data in the range vector window before this evaluation time
			rangeStart := evalTime - parsed.RangeVector
			rangeEnd := evalTime

			timestamps, values, err := ph.tsdb.Get(metric, rangeStart, rangeEnd)
			if err != nil || len(values) < 2 {
				continue // Need at least 2 points
			}

			var rateValue float64

			if parsed.AggFunc == AggRate {
				// rate: use first and last points
				firstVal := values[0]
				lastVal := values[len(values)-1]
				firstTs := timestamps[0]
				lastTs := timestamps[len(timestamps)-1]

				timeDiff := float64(lastTs - firstTs)
				if timeDiff > 0 {
					valueDiff := lastVal - firstVal
					if valueDiff < 0 {
						// Counter reset
						valueDiff = lastVal
					}
					rateValue = valueDiff / timeDiff
				}
			} else if parsed.AggFunc == AggIrate {
				// irate: use last two points
				prevVal := values[len(values)-2]
				lastVal := values[len(values)-1]
				prevTs := timestamps[len(timestamps)-2]
				lastTs := timestamps[len(timestamps)-1]

				timeDiff := float64(lastTs - prevTs)
				if timeDiff > 0 {
					valueDiff := lastVal - prevVal
					if valueDiff < 0 {
						// Counter reset
						valueDiff = lastVal
					}
					rateValue = valueDiff / timeDiff
				}
			}

			valuesArray = append(valuesArray, []interface{}{evalTime, fmt.Sprintf("%f", rateValue)})
		}

		if len(valuesArray) > 0 {
			results = append(results, QueryResult{
				Metric: parsedTags,
				Values: valuesArray,
			})
		}
	}

	return results
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
