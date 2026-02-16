package web

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"sync"

	"github.com/lynix/krill"
	"github.com/lynix/krill/storage"
)

// PrometheusHandler handles Prometheus-compatible API requests
type PrometheusHandler struct {
	tsdb       krill.QueryableDB
	debugIndex bool
	stats      *Stats
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
	Metric map[string]string `json:"metric" msgpack:"metric"`
	Values [][]interface{}   `json:"values,omitempty" msgpack:"values,omitempty"`
	Value  []interface{}     `json:"value,omitempty" msgpack:"value,omitempty"`
}

// QueryData represents the data portion of a query response
type QueryData struct {
	ResultType string        `json:"resultType" msgpack:"resultType"`
	Result     []QueryResult `json:"result" msgpack:"result"`
}

// FunctionStage represents a function to apply in a pipeline
type FunctionStage struct {
	Name   string `json:"name"`
	Type   string `json:"type,omitempty"`
	Code   string `json:"code,omitempty"`
	Module string `json:"module,omitempty"`
}

// KrillQLQuery represents a single stage in the KrillQL pipeline
// Can be either a query stage or a function stage
type KrillQLQuery struct {
	// Query stage fields
	Query     string          `json:"query,omitempty"`
	Start     int64           `json:"start,omitempty"`
	End       int64           `json:"end,omitempty"`
	Step      int64           `json:"step,omitempty"`
	Functions []FunctionStage `json:"functions,omitempty"`

	// Function stage fields (for backwards compatibility)
	Function string `json:"function,omitempty"`
	Type     string `json:"type,omitempty"`
	Code     string `json:"code,omitempty"`
	Module   string `json:"module,omitempty"`
}

// KrillQLRequest represents a JSON request for /api/v1/krillql
type KrillQLRequest struct {
	Queries []KrillQLQuery `json:"queries"`
}

// HandleQuery handles instant queries: GET or POST /api/v1/query
func (ph *PrometheusHandler) HandleQuery(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	defer func() {
		if ph.stats != nil {
			ph.stats.RecordInstantQuery(time.Since(startTime))
		}
	}()

	// Support both GET and POST methods (Grafana uses both)
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		ph.sendError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Parse query from URL params (GET) or form data (POST)
	var query string
	if r.Method == http.MethodPost {
		r.ParseForm()
		query = r.FormValue("query")
	} else {
		query = r.URL.Query().Get("query")
	}
	
	if query == "" {
		ph.sendError(w, http.StatusBadRequest, "query parameter is required")
		return
	}

	// Parse time parameter (default to now)
	timeParam := r.URL.Query().Get("time")
	if r.Method == http.MethodPost && timeParam == "" {
		timeParam = r.FormValue("time")
	}
	queryTime := time.Now().Unix()
	if timeParam != "" {
		t, err := strconv.ParseInt(timeParam, 10, 64)
		if err == nil {
			queryTime = t
		}
	}

	// Parse query with aggregation support
	parsed := parsePromQL(query)
	log.Printf("[PARSE] Query: %s => MetricName: %q, LabelMatchers: %v, AggFunc: %v",
		query, parsed.MetricName, parsed.LabelMatchers, parsed.AggFunc)

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

	// Use index-based search if available
	type IndexFinder interface {
		FindSeriesByLabels(map[string]string) []uint64
	}
	type LabelsGetter interface {
		GetLabelsForSeriesID(uint64) (storage.Labels, bool)
	}

	var result []QueryResult
	var matchingSeries []storage.Labels

	if finder, ok := ph.tsdb.(IndexFinder); ok && parsed.MetricName != "" {
		// Use inverted index to find matching series
		// Even with just metric name, index helps narrow down from 500k to thousands
		allMatchers := make(map[string]string)
		allMatchers["__name__"] = parsed.MetricName
		for k, v := range parsed.LabelMatchers {
			allMatchers[k] = v
		}

		log.Printf("[INDEX] Using index search with matchers: %v", allMatchers)
		seriesIDs := finder.FindSeriesByLabels(allMatchers)
		log.Printf("[INDEX] Found %d matching series IDs", len(seriesIDs))

		// Convert seriesIDs to Labels
		if labelsGetter, ok := ph.tsdb.(LabelsGetter); ok {
			for _, seriesID := range seriesIDs {
				if labels, ok := labelsGetter.GetLabelsForSeriesID(seriesID); ok {
					matchingSeries = append(matchingSeries, labels)
				}
			}
		} else {
			log.Printf("[INDEX] LabelsGetter not available, fallback to full scan")
			allSeries, err := ph.tsdb.GetAllSeries()
			if err != nil {
				ph.sendError(w, http.StatusInternalServerError, err.Error())
				return
			}
			log.Printf("[INDEX] Full scan of %d series", len(allSeries))
			matchingSeries = allSeries
		}
	} else {
		log.Printf("[FULLSCAN] Index not available or no metric name, using full scan")
		allSeries, err := ph.tsdb.GetAllSeries()
		if err != nil {
			ph.sendError(w, http.StatusInternalServerError, err.Error())
			return
		}
		log.Printf("[FULLSCAN] Scanning %d total series", len(allSeries))
		matchingSeries = allSeries
	}

	// Filter series by name and labels
	for _, labels := range matchingSeries {
		if matchesLabels(labels, parsed.MetricName, parsed.LabelMatchers) {
			// Format labels to metric key for DB lookup
			metric := formatLabelsAsMetricString(labels)

			// Query TSDB for this specific metric
			timestamps, values, err := ph.tsdb.Get(metric, startTs, endTs)
			if err != nil {
				continue
			}

			if len(values) > 0 {
				// Build tags map from labels (already parsed!)
				parsedTags := make(map[string]string)
				for _, label := range labels {
					parsedTags[label.Name] = label.Value
				}

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

// HandleQueryRange handles range queries: GET or POST /api/v1/query_range
func (ph *PrometheusHandler) HandleQueryRange(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	defer func() {
		if ph.stats != nil {
			ph.stats.RecordRangeQuery(time.Since(startTime))
		}
	}()

	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		ph.sendError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var query, startParam, endParam, stepParam string

	// Parse parameters based on request method
	if r.Method == http.MethodPost {
		// Parse form data from POST request
		if err := r.ParseForm(); err != nil {
			ph.sendError(w, http.StatusBadRequest, "failed to parse form data")
			return
		}
		query = r.FormValue("query")
		startParam = r.FormValue("start")
		endParam = r.FormValue("end")
		stepParam = r.FormValue("step")
	} else {
		// Parse query parameters from GET request
		query = r.URL.Query().Get("query")
		startParam = r.URL.Query().Get("start")
		endParam = r.URL.Query().Get("end")
		stepParam = r.URL.Query().Get("step")
	}

	if query == "" {
		ph.sendError(w, http.StatusBadRequest, "query parameter is required")
		return
	}

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
	log.Printf("[PARSE] Query: %s => MetricName: %q, LabelMatchers: %v, AggFunc: %v",
		query, parsed.MetricName, parsed.LabelMatchers, parsed.AggFunc)

	// Check if this is a range vector function with step parameter
	isRangeVectorFunc := parsed.AggFunc == AggRate || parsed.AggFunc == AggIrate ||
		parsed.AggFunc == AggSumOverTime || parsed.AggFunc == AggAvgOverTime ||
		parsed.AggFunc == AggMinOverTime || parsed.AggFunc == AggMaxOverTime ||
		parsed.AggFunc == AggCountOverTime || parsed.AggFunc == AggStddevOverTime ||
		parsed.AggFunc == AggStdvarOverTime || parsed.AggFunc == AggQuantileOverTime

	if step > 0 && isRangeVectorFunc {
		// Validate range vector is specified
		if parsed.RangeVector <= 0 {
			ph.sendError(w, http.StatusBadRequest,
				fmt.Sprintf("range vector duration required for %s function (e.g., %s(metric[5m]))",
					parsed.AggFunc, parsed.AggFunc))
			return
		}
		result := ph.evaluateRangeVectorWithStep(parsed, start, end, step)
		ph.sendSuccess(w, QueryData{
			ResultType: "matrix",
			Result:     result,
		})
		return
	}

	// Use index-based search if available (BadgerTSDB), otherwise fallback to full scan
	type IndexFinder interface {
		FindSeriesByLabels(map[string]string) []uint64
	}
	type LabelsGetter interface {
		GetLabelsForSeriesID(uint64) (storage.Labels, bool)
	}

	var result []QueryResult
	var matchingSeries []storage.Labels

	if finder, ok := ph.tsdb.(IndexFinder); ok && parsed.MetricName != "" {
		// Use inverted index to find matching series
		// Even with just metric name, index helps narrow down from 500k to thousands
		allMatchers := make(map[string]string)
		allMatchers["__name__"] = parsed.MetricName
		for k, v := range parsed.LabelMatchers {
			allMatchers[k] = v
		}

		if ph.debugIndex {
			log.Printf("[INDEX-RANGE] Using index search with matchers: %v", allMatchers)
		}
		seriesIDs := finder.FindSeriesByLabels(allMatchers)
		if ph.debugIndex {
			log.Printf("[INDEX-RANGE] Found %d matching series IDs", len(seriesIDs))
		}

		// Convert seriesIDs to Labels
		if labelsGetter, ok := ph.tsdb.(LabelsGetter); ok {
			for _, seriesID := range seriesIDs {
				if labels, ok := labelsGetter.GetLabelsForSeriesID(seriesID); ok {
					matchingSeries = append(matchingSeries, labels)
				}
			}
		} else {
			// Fallback: get all series
			if ph.debugIndex {
				log.Printf("[INDEX-RANGE] LabelsGetter not available, fallback to full scan")
			}
			allSeries, err := ph.tsdb.GetAllSeries()
			if err != nil {
				ph.sendError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if ph.debugIndex {
				log.Printf("[INDEX-RANGE] Full scan of %d series", len(allSeries))
			}
			matchingSeries = allSeries
		}
	} else {
		// Fallback: get all series and filter
		if _, ok := ph.tsdb.(IndexFinder); !ok {
			log.Printf("[FULLSCAN-RANGE] IndexFinder interface not implemented by %T", ph.tsdb)
		} else if parsed.MetricName == "" {
			log.Printf("[FULLSCAN-RANGE] MetricName is empty")
		} else {
			log.Printf("[FULLSCAN-RANGE] Unknown reason for fallback")
		}
		log.Printf("[FULLSCAN-RANGE] Index not available or no metric name, using full scan")
		allSeries, err := ph.tsdb.GetAllSeries()
		if err != nil {
			ph.sendError(w, http.StatusInternalServerError, err.Error())
			return
		}
		log.Printf("[FULLSCAN-RANGE] Scanning %d total series", len(allSeries))
		matchingSeries = allSeries
	}

	// Filter series by name and labels - PARALLEL PROCESSING
	type seriesResult struct {
		labels storage.Labels
		timestamps []int64
		values []float64
	}
	
	resultChan := make(chan seriesResult, len(matchingSeries))
	var wg sync.WaitGroup
	
	// Limit concurrent goroutines to avoid overwhelming the system
	semaphore := make(chan struct{}, 100) // Max 100 concurrent queries
	
	for _, labels := range matchingSeries {
		if matchesLabels(labels, parsed.MetricName, parsed.LabelMatchers) {
			wg.Add(1)
			go func(lbls storage.Labels) {
				defer wg.Done()
				semaphore <- struct{}{} // Acquire
				defer func() { <-semaphore }() // Release
				
				// Format labels to metric key for DB lookup
				metric := formatLabelsAsMetricString(lbls)

				// Query TSDB for this specific metric
				timestamps, values, err := ph.tsdb.Get(metric, start, end)
				if err != nil || len(values) == 0 {
					return
				}

				resultChan <- seriesResult{
					labels: lbls,
					timestamps: timestamps,
					values: values,
				}
			}(labels)
		}
	}
	
	// Close channel when all goroutines complete
	go func() {
		wg.Wait()
		close(resultChan)
	}()
	
	// Collect results
	for sr := range resultChan {
		timestamps, values := sr.timestamps, sr.values
		
		// Apply step-based downsampling if step is specified
		if step > 0 {
			timestamps, values = downsampleByStep(timestamps, values, start, end, step)
		}

		valuesArray := make([][]interface{}, len(values))
		for i := range values {
			valuesArray[i] = []interface{}{timestamps[i], fmt.Sprintf("%f", values[i])}
		}
		// Build tags map from labels
		parsedTags := make(map[string]string)
		for _, label := range sr.labels {
			parsedTags[label.Name] = label.Value
		}
		result = append(result, QueryResult{
			Metric: parsedTags,
			Values: valuesArray,
		})
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
	Metric    string            `json:"metric"`
	Value     float64           `json:"value"`
	Time      int64             `json:"time,omitempty"`
	Timestamp int64             `json:"timestamp,omitempty"` // Alternative to Time
	Tags      map[string]string `json:"tags,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"` // Alternative to Tags
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

	if ph.stats != nil {
		ph.stats.RecordSingleWrite(1)
	}

	ph.sendSuccess(w, nil)
}

// HandleBatchWrite handles batch write requests: POST /api/v1/write/batch
func (ph *PrometheusHandler) HandleBatchWrite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		ph.sendError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var requests []WriteRequest
	if err := json.NewDecoder(r.Body).Decode(&requests); err != nil {
		ph.sendError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if len(requests) == 0 {
		ph.sendError(w, http.StatusBadRequest, "empty batch request")
		return
	}

	now := time.Now().Unix()

	// Convert to DataPoint slice for efficient batch processing
	points := make([]storage.DataPoint, 0, len(requests))
	for _, req := range requests {
		if req.Metric == "" {
			continue
		}

		// Use current time if not specified
		timestamp := req.Time
		if timestamp == 0 {
			timestamp = req.Timestamp
		}
		if timestamp == 0 {
			timestamp = now
		}

		// Merge Tags and Labels (Labels takes precedence)
		allTags := make(map[string]string)
		for k, v := range req.Tags {
			allTags[k] = v
		}
		for k, v := range req.Labels {
			allTags[k] = v
		}

		// Parse metric and tags into Labels
		labels := buildLabels(req.Metric, allTags)

		points = append(points, storage.DataPoint{
			Timestamp: timestamp,
			Labels:    labels,
			Value:     req.Value,
		})
	}

	// Batch write to TSDB (much more efficient!)
	if err := ph.tsdb.TsdbPutBatch(points); err != nil {
		ph.sendError(w, http.StatusInternalServerError, fmt.Sprintf("failed to write batch: %v", err))
		return
	}

	if ph.stats != nil {
		ph.stats.RecordBatchWrite(len(points))
	}

	ph.sendSuccess(w, map[string]int{"written": len(points)})
}

// HandleMetrics returns list of all metrics: GET /api/v1/label/__name__/values or GET /api/v1/metrics
// When called via /api/v1/label/__name__/values (Grafana), returns just metric names
// When called via /api/v1/metrics, returns full metric strings with labels
// Supports optional query parameters:
//   - filter: regex pattern to filter metrics
//   - limit: maximum number of metrics to return (default: no limit)
//   - offset: number of metrics to skip for pagination (default: 0)
func (ph *PrometheusHandler) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		ph.sendError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Check if this is a Grafana label values request
	isLabelValuesRequest := strings.Contains(r.URL.Path, "/label/__name__/values")

	// Get query parameters
	filterParam := r.URL.Query().Get("filter")
	limitParam := r.URL.Query().Get("limit")
	offsetParam := r.URL.Query().Get("offset")

	// Parse limit and offset
	var limit, offset int
	var err error

	if limitParam != "" {
		limit, err = strconv.Atoi(limitParam)
		if err != nil || limit < 0 {
			ph.sendError(w, http.StatusBadRequest, "invalid limit parameter")
			return
		}
	}

	if offsetParam != "" {
		offset, err = strconv.Atoi(offsetParam)
		if err != nil || offset < 0 {
			ph.sendError(w, http.StatusBadRequest, "invalid offset parameter")
			return
		}
	}

	var filteredMetrics []string

	if isLabelValuesRequest {
		// Grafana label values request - return just metric names
		allSeries, err := ph.tsdb.GetAllSeries()
		if err != nil {
			ph.sendError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get series: %v", err))
			return
		}

		// Extract unique metric names
		metricNames := make(map[string]bool)
		for _, series := range allSeries {
			for _, label := range series {
				if label.Name == "__name__" {
					metricNames[label.Value] = true
					break
				}
			}
		}

		// Convert to sorted slice
		for name := range metricNames {
			filteredMetrics = append(filteredMetrics, name)
		}
		sort.Strings(filteredMetrics)
	} else {
		// Regular metrics request - return full metric strings
		allMetrics, err := ph.tsdb.GetMetrics()
		if err != nil {
			ph.sendError(w, http.StatusInternalServerError, err.Error())
			return
		}
		filteredMetrics = allMetrics
	}

	// Apply filter if specified
	if filterParam != "" {
		filterRegex, err := regexp.Compile(filterParam)
		if err != nil {
			ph.sendError(w, http.StatusBadRequest, fmt.Sprintf("invalid filter regex: %v", err))
			return
		}

		var filtered []string
		for _, metric := range filteredMetrics {
			if filterRegex.MatchString(metric) {
				filtered = append(filtered, metric)
			}
		}
		filteredMetrics = filtered
	}

	totalCount := len(filteredMetrics)

	// Apply pagination
	start := offset
	if start > totalCount {
		start = totalCount
	}

	end := totalCount
	if limit > 0 && start+limit < totalCount {
		end = start + limit
	}

	paginatedMetrics := filteredMetrics[start:end]

	// Send response
	if isLabelValuesRequest {
		// Grafana expects simple Prometheus response format
		ph.sendSuccess(w, paginatedMetrics)
	} else {
		// Full response with pagination metadata for dashboard
		response := map[string]interface{}{
			"status": "success",
			"data":   paginatedMetrics,
			"metadata": map[string]interface{}{
				"total":  totalCount,
				"offset": offset,
				"limit":  limit,
				"count":  len(paginatedMetrics),
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}
}

// HandleLabels returns all label names (Grafana API)
// GET /api/v1/labels
func (ph *PrometheusHandler) HandleLabels(w http.ResponseWriter, r *http.Request) {
	allSeries, err := ph.tsdb.GetAllSeries()
	if err != nil {
		ph.sendError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get series: %v", err))
		return
	}

	labelNames := make(map[string]bool)
	for _, series := range allSeries {
		for _, label := range series {
			labelNames[label.Name] = true
		}
	}

	result := make([]string, 0, len(labelNames))
	for name := range labelNames {
		result = append(result, name)
	}
	sort.Strings(result)

	ph.sendSuccess(w, result)
}

// HandleLabelValues returns all values for a specific label (Grafana API)
// GET /api/v1/label/<label_name>/values
func (ph *PrometheusHandler) HandleLabelValues(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/label/"), "/")
	if len(parts) < 2 || parts[1] != "values" {
		ph.sendError(w, http.StatusBadRequest, "invalid URL format, expected /api/v1/label/<name>/values")
		return
	}
	labelName := parts[0]

	allSeries, err := ph.tsdb.GetAllSeries()
	if err != nil {
		ph.sendError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get series: %v", err))
		return
	}

	labelValues := make(map[string]bool)
	for _, series := range allSeries {
		for _, label := range series {
			if label.Name == labelName {
				labelValues[label.Value] = true
			}
		}
	}

	result := make([]string, 0, len(labelValues))
	for value := range labelValues {
		result = append(result, value)
	}
	sort.Strings(result)

	ph.sendSuccess(w, result)
}

// HandleSeries returns series metadata matching label matchers (Grafana API)
// GET /api/v1/series?match[]=<matcher>
func (ph *PrometheusHandler) HandleSeries(w http.ResponseWriter, r *http.Request) {
	matches := r.URL.Query()["match[]"]
	if len(matches) == 0 {
		ph.sendError(w, http.StatusBadRequest, "at least one match[] parameter required")
		return
	}

	allSeries, err := ph.tsdb.GetAllSeries()
	if err != nil {
		ph.sendError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get series: %v", err))
		return
	}

	type SeriesMeta struct {
		Metric map[string]string `json:"metric"`
	}

	var result []SeriesMeta
	for _, series := range allSeries {
		labelsMap := make(map[string]string)
		for _, label := range series {
			labelsMap[label.Name] = label.Value
		}

		for _, match := range matches {
			if matchesSimpleSelector(labelsMap, match) {
				result = append(result, SeriesMeta{Metric: labelsMap})
				break
			}
		}
	}

	ph.sendSuccess(w, result)
}

// matchesSimpleSelector checks if labels match a Prometheus selector
func matchesSimpleSelector(labels map[string]string, selector string) bool {
	selector = strings.TrimSpace(selector)

	if !strings.Contains(selector, "{") {
		metricName, ok := labels["__name__"]
		return ok && metricName == selector
	}

	var metricName string
	if idx := strings.Index(selector, "{"); idx > 0 {
		metricName = selector[:idx]
		if labelValue, ok := labels["__name__"]; !ok || labelValue != metricName {
			return false
		}
	}

	return true // Simplified: just check metric name
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

// buildLabels creates a Labels object from metric name and tags with string interning
func buildLabels(name string, tags map[string]string) storage.Labels {
	labels := make(storage.Labels, 0, len(tags)+1)
	labels = append(labels, storage.InternLabel("__name__", name))

	for k, v := range tags {
		labels = append(labels, storage.InternLabel(k, v))
	}

	// Sort for consistency using sort.Sort
	sort.Sort(labels)
	return labels
}

// parseMetricKey parses a metric key back into name and tags
// Format: metric_name{tag1="value1",tag2="value2"}
// Also supports: {"metric_name", tag1="value1"} (Grafana format)
func parseMetricKey(key string) (string, map[string]string) {
	tags := make(map[string]string)

	// Find { position
	bracePos := strings.Index(key, "{")
	if bracePos < 0 {
		// No tags
		return key, tags
	}

	var name string
	
	// Check if query starts with { (Grafana label matcher format)
	if bracePos == 0 {
		// Format: {"metric_name", cloud="apigw"}
		// Find } position
		endPos := strings.Index(key[bracePos:], "}")
		if endPos < 0 {
			return "", tags
		}
		endPos += bracePos

		// Parse tags
		tagStr := key[bracePos+1 : endPos]
		if tagStr == "" {
			return "", tags
		}

		// Split by comma
		pairs := strings.Split(tagStr, ",")
		for i, pair := range pairs {
			pair = strings.TrimSpace(pair)
			
			// First element without = is the metric name
			if i == 0 && !strings.Contains(pair, "=") {
				// Remove quotes if present
				name = strings.Trim(pair, "\"")
				continue
			}
			
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

	// Standard format: metric_name{tag1="value1"}
	name = key[:bracePos]

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

// formatLabelsAsMetricString converts Labels to metric string format for DB lookup
func formatLabelsAsMetricString(labels storage.Labels) string {
	name := labels.Get("__name__")
	if name == "" {
		name = "unknown"
	}

	// Collect non-name labels
	var tagParts []string
	for _, label := range labels {
		if label.Name != "__name__" {
			tagParts = append(tagParts, fmt.Sprintf("%s=\"%s\"", label.Name, label.Value))
		}
	}

	if len(tagParts) == 0 {
		return name
	}

	return fmt.Sprintf("%s{%s}", name, strings.Join(tagParts, ","))
}

// matchesLabels checks if Labels match the query (name and label matchers) - no string parsing!
func matchesLabels(labels storage.Labels, queryName string, labelMatchers map[string]string) bool {
	// Check name match (empty query name matches all)
	if queryName != "" {
		name := labels.Get("__name__")
		if name != queryName {
			return false
		}
	}

	// Check all label matchers
	for k, v := range labelMatchers {
		if labels.Get(k) != v {
			return false
		}
	}

	return true
}

// matchesQuery checks if a metric key matches the query (name and label matchers)
// Deprecated: Use matchesLabels for better performance
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

// downsampleByStep downsamples time series data by selecting values at step intervals
func downsampleByStep(timestamps []int64, values []float64, start, end, step int64) ([]int64, []float64) {
	if step <= 0 || len(timestamps) == 0 {
		return timestamps, values
	}

	var resultTimestamps []int64
	var resultValues []float64

	// Lookback delta: maximum age of a sample to be considered valid
	// Use 1.1 * step to allow for slight timing variations
	lookbackDelta := int64(float64(step) * 1.1)

	// For each evaluation time (aligned to step intervals)
	for evalTime := start; evalTime <= end; evalTime += step {
		// Find the timestamp that is <= evalTime and closest to it
		// This matches Prometheus behavior for consistent results
		bestIdx := -1
		bestTs := int64(0)

		for i, ts := range timestamps {
			// Only consider timestamps at or before evalTime
			if ts <= evalTime {
				// Pick the closest one (largest timestamp <= evalTime)
				if bestIdx == -1 || ts > bestTs {
					bestIdx = i
					bestTs = ts
				}
			}
		}

		// Only include the value if it's within lookback delta
		// This prevents stale values from being repeated when data is missing
		if bestIdx >= 0 && (evalTime-bestTs) <= lookbackDelta {
			resultTimestamps = append(resultTimestamps, evalTime)
			resultValues = append(resultValues, values[bestIdx])
		}
	}

	return resultTimestamps, resultValues
}

// evaluateRangeVectorWithStep evaluates range vector functions (rate/irate/*_over_time) at multiple time steps
func (ph *PrometheusHandler) evaluateRangeVectorWithStep(parsed ParsedQuery, start, end, step int64) []QueryResult {
	var results []QueryResult

	// Validate that range vector is specified for range vector functions
	if parsed.RangeVector <= 0 {
		// Return empty results - this will show "No data found"
		// A better error message could be added at the API level
		return results
	}

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

		// Calculate function at each evaluation time
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
			if err != nil || len(values) == 0 {
				continue
			}

			var resultValue float64
			hasValue := false

			switch parsed.AggFunc {
			case AggRate:
				if len(values) >= 2 {
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
						resultValue = valueDiff / timeDiff
						hasValue = true
					}
				}
			case AggIrate:
				if len(values) >= 2 {
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
						resultValue = valueDiff / timeDiff
						hasValue = true
					}
				}
			case AggMaxOverTime:
				// Find maximum value in the range
				resultValue = values[0]
				for _, v := range values {
					if v > resultValue {
						resultValue = v
					}
				}
				hasValue = true
			case AggMinOverTime:
				// Find minimum value in the range
				resultValue = values[0]
				for _, v := range values {
					if v < resultValue {
						resultValue = v
					}
				}
				hasValue = true
			case AggAvgOverTime:
				// Calculate average
				sum := 0.0
				for _, v := range values {
					sum += v
				}
				resultValue = sum / float64(len(values))
				hasValue = true
			case AggSumOverTime:
				// Calculate sum
				sum := 0.0
				for _, v := range values {
					sum += v
				}
				resultValue = sum
				hasValue = true
			case AggCountOverTime:
				// Count data points
				resultValue = float64(len(values))
				hasValue = true
			case AggStddevOverTime, AggStdvarOverTime:
				// Calculate standard deviation / variance
				if len(values) > 0 {
					sum := 0.0
					for _, v := range values {
						sum += v
					}
					mean := sum / float64(len(values))

					variance := 0.0
					for _, v := range values {
						diff := v - mean
						variance += diff * diff
					}
					variance /= float64(len(values))

					if parsed.AggFunc == AggStddevOverTime {
						resultValue = math.Sqrt(variance)
					} else {
						resultValue = variance
					}
					hasValue = true
				}
			}

			if hasValue {
				valuesArray = append(valuesArray, []interface{}{evalTime, fmt.Sprintf("%f", resultValue)})
			}
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

// HandleKrillQL handles JSON-based queries: POST /api/v1/krillql
// Supports pipeline processing where results flow from one stage to the next
func (ph *PrometheusHandler) HandleKrillQL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		ph.sendError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Parse JSON request body
	var req KrillQLRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ph.sendError(w, http.StatusBadRequest, "invalid JSON request: "+err.Error())
		return
	}
	defer r.Body.Close()

	// Validate queries array
	if len(req.Queries) == 0 {
		ph.sendError(w, http.StatusBadRequest, "queries array is required and must not be empty")
		return
	}

	// First stage must be a query
	firstStage := req.Queries[0]
	if firstStage.Query == "" {
		ph.sendError(w, http.StatusBadRequest, "first stage must be a query (query field required)")
		return
	}
	if firstStage.Start <= 0 || firstStage.End <= 0 {
		ph.sendError(w, http.StatusBadRequest, "start and end fields are required in first query")
		return
	}
	if firstStage.Start >= firstStage.End {
		ph.sendError(w, http.StatusBadRequest, "start must be less than end")
		return
	}

	// Execute first query stage
	parsed := parsePromQL(firstStage.Query)

	// Determine if this is a range vector function
	isRangeVectorFunc := parsed.AggFunc == "rate" || parsed.AggFunc == "irate" ||
		parsed.AggFunc == "max_over_time" || parsed.AggFunc == "min_over_time" ||
		parsed.AggFunc == "avg_over_time" || parsed.AggFunc == "sum_over_time" ||
		parsed.AggFunc == "count_over_time" || parsed.AggFunc == "stddev_over_time" ||
		parsed.AggFunc == "stdvar_over_time"

	var currentResult QueryData

	// Handle range vector functions with step
	if firstStage.Step > 0 && isRangeVectorFunc {
		if parsed.RangeVector <= 0 {
			ph.sendError(w, http.StatusBadRequest,
				fmt.Sprintf("range vector duration required for %s function (e.g., %s(metric[5m]))",
					parsed.AggFunc, parsed.AggFunc))
			return
		}
		result := ph.evaluateRangeVectorWithStep(parsed, firstStage.Start, firstStage.End, firstStage.Step)
		currentResult = QueryData{
			ResultType: "matrix",
			Result:     result,
		}
	} else {
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
				timestamps, values, err := ph.tsdb.Get(metric, firstStage.Start, firstStage.End)
				if err != nil {
					continue
				}

				if len(values) > 0 {
					// Apply step-based downsampling if step is specified
					if firstStage.Step > 0 {
						timestamps, values = downsampleByStep(timestamps, values, firstStage.Start, firstStage.End, firstStage.Step)
					}

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

		currentResult = QueryData{
			ResultType: "matrix",
			Result:     result,
		}
	}

	// Process functions from first query stage if present
	if len(firstStage.Functions) > 0 {
		for i, fn := range firstStage.Functions {
			// Create a KrillQLQuery from FunctionStage
			fnStage := KrillQLQuery{
				Function: fn.Name,
				Type:     fn.Type,
				Code:     fn.Code,
				Module:   fn.Module,
			}

			result, err := ProcessPipelineFunction(currentResult, fn.Name, fnStage)
			if err != nil {
				ph.sendError(w, http.StatusBadRequest, fmt.Sprintf("error in function[%d]: %s", i, err.Error()))
				return
			}
			currentResult = result
		}
	}

	// Process remaining stages as pipeline functions
	for i := 1; i < len(req.Queries); i++ {
		stage := req.Queries[i]

		// Must be a function stage
		if stage.Function == "" {
			ph.sendError(w, http.StatusBadRequest, fmt.Sprintf("stage %d must specify a function", i))
			return
		}

		// Process function using external function handler
		result, err := ProcessPipelineFunction(currentResult, stage.Function, stage)
		if err != nil {
			ph.sendError(w, http.StatusBadRequest, fmt.Sprintf("error in stage %d: %s", i, err.Error()))
			return
		}
		currentResult = result
	}

	// Send final result
	ph.sendSuccess(w, currentResult)
}

func (ph *PrometheusHandler) sendError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(PrometheusResponse{
		Status: "error",
		Error:  message,
	})
}
