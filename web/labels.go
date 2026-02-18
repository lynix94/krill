package web

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lynix/krill/storage"
)

type labelMatcher struct {
	metric string
	labels map[string]string
}

// HandleLabels returns label names (Prometheus-compatible)
// GET /api/v1/labels
// Supports optional: start, end, match[], filter, limit, offset
func (ph *PrometheusHandler) HandleLabels(w http.ResponseWriter, r *http.Request) {
	start, end, timeFilter, err := parseLabelTimeRange(r)
	if err != nil {
		ph.sendError(w, http.StatusBadRequest, err.Error())
		return
	}
	
	matchers, err := parseLabelMatchers(r)
	if err != nil {
		ph.sendError(w, http.StatusBadRequest, err.Error())
		return
	}

	filterRegex, limit, offset, err := parseLabelListParams(r)
	if err != nil {
		ph.sendError(w, http.StatusBadRequest, err.Error())
		return
	}

	allSeries, err := ph.tsdb.GetAllSeries()
	if err != nil {
		ph.sendError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get series: %v", err))
		return
	}

	filtered, err := ph.filterSeries(allSeries, matchers, start, end, timeFilter)
	if err != nil {
		ph.sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	labelNames := make(map[string]bool)
	for _, series := range filtered {
		for _, label := range series {
			labelNames[label.Name] = true
		}
	}

	result := make([]string, 0, len(labelNames))
	for name := range labelNames {
		result = append(result, name)
	}
	sort.Strings(result)

	result = applyFilter(result, filterRegex)
	result = applyPagination(result, limit, offset)

	ph.sendSuccess(w, result)
}

// HandleLabelValues returns all values for a specific label (Prometheus/Grafana API)
// GET /api/v1/label/<label_name>/values
// Supports optional: start, end, match[]
func (ph *PrometheusHandler) HandleLabelValues(w http.ResponseWriter, r *http.Request) {
	labelName, ok := extractLabelNameFromPath(r.URL.Path)
	if !ok {
		ph.sendError(w, http.StatusBadRequest, "invalid URL format, expected /api/v1/label/<name>/values or /api/v1/labels/<name>/values")
		return
	}

	start, end, timeFilter, err := parseLabelTimeRange(r)
	if err != nil {
		ph.sendError(w, http.StatusBadRequest, err.Error())
		return
	}
	
	matchers, err := parseLabelMatchers(r)
	if err != nil {
		ph.sendError(w, http.StatusBadRequest, err.Error())
		return
	}

	filterRegex, limit, offset, err := parseLabelListParams(r)
	if err != nil {
		ph.sendError(w, http.StatusBadRequest, err.Error())
		return
	}

	allSeries, err := ph.tsdb.GetAllSeries()
	if err != nil {
		ph.sendError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get series: %v", err))
		return
	}

	filtered, err := ph.filterSeries(allSeries, matchers, start, end, timeFilter)
	if err != nil {
		ph.sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	labelValues := make(map[string]bool)
	for _, series := range filtered {
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

	result = applyFilter(result, filterRegex)
	result = applyPagination(result, limit, offset)

	ph.sendSuccess(w, result)
}

// HandleSeries returns series metadata matching label matchers (Prometheus/Grafana API)
// GET /api/v1/series?match[]=<matcher>
// Supports optional: start, end
func (ph *PrometheusHandler) HandleSeries(w http.ResponseWriter, r *http.Request) {
	matchers, err := parseLabelMatchers(r)
	if err != nil {
		ph.sendError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(matchers) == 0 {
		ph.sendError(w, http.StatusBadRequest, "at least one match[] parameter required")
		return
	}

	start, end, timeFilter, err := parseLabelTimeRange(r)
	if err != nil {
		ph.sendError(w, http.StatusBadRequest, err.Error())
		return
	}

	allSeries, err := ph.tsdb.GetAllSeries()
	if err != nil {
		ph.sendError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get series: %v", err))
		return
	}

	filtered, err := ph.filterSeries(allSeries, matchers, start, end, timeFilter)
	if err != nil {
		ph.sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type SeriesMeta struct {
		Metric map[string]string `json:"metric"`
	}

	var result []SeriesMeta
	for _, series := range filtered {
		labelsMap := make(map[string]string)
		for _, label := range series {
			labelsMap[label.Name] = label.Value
		}
		result = append(result, SeriesMeta{Metric: labelsMap})
	}

	ph.sendSuccess(w, result)
}

func parseLabelTimeRange(r *http.Request) (int64, int64, bool, error) {
	startParam := r.URL.Query().Get("start")
	endParam := r.URL.Query().Get("end")

	start, err := parseTimeParam(startParam)
	if err != nil {
		return 0, 0, false, fmt.Errorf("invalid start: %v", err)
	}
	end, err := parseTimeParam(endParam)
	if err != nil {
		return 0, 0, false, fmt.Errorf("invalid end: %v", err)
	}

	timeFilter := start != 0 || end != 0
	if start != 0 && end == 0 {
		end = time.Now().Unix()
	}
	if start != 0 && end != 0 && start > end {
		return 0, 0, false, fmt.Errorf("start must be <= end")
	}

	return start, end, timeFilter, nil
}

func parseTimeParam(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}

	if f, err := strconv.ParseFloat(value, 64); err == nil {
		return int64(f), nil
	}

	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t.Unix(), nil
	}

	return 0, fmt.Errorf("invalid time %q", value)
}

func parseLabelMatchers(r *http.Request) ([]labelMatcher, error) {
	matchParams := r.URL.Query()["match[]"]
	if len(matchParams) == 0 {
		return nil, nil
	}

	matchers := make([]labelMatcher, 0, len(matchParams))
	for _, sel := range matchParams {
		m, err := parseSelector(sel)
		if err != nil {
			return nil, err
		}
		matchers = append(matchers, m)
	}
	return matchers, nil
}

func parseSelector(selector string) (labelMatcher, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return labelMatcher{}, fmt.Errorf("empty match[] selector")
	}

	if !strings.Contains(selector, "{") {
		return labelMatcher{metric: selector, labels: map[string]string{}}, nil
	}

	openIdx := strings.Index(selector, "{")
	closeIdx := strings.LastIndex(selector, "}")
	if openIdx == -1 || closeIdx == -1 || closeIdx < openIdx {
		return labelMatcher{}, fmt.Errorf("invalid selector: %s", selector)
	}

	metric := strings.TrimSpace(selector[:openIdx])
	labelsPart := strings.TrimSpace(selector[openIdx+1 : closeIdx])
	labels := make(map[string]string)

	if labelsPart != "" {
		parts := strings.Split(labelsPart, ",")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			kv := strings.SplitN(part, "=", 2)
			if len(kv) != 2 {
				return labelMatcher{}, fmt.Errorf("invalid label matcher: %s", part)
			}
			key := strings.TrimSpace(kv[0])
			val := strings.TrimSpace(kv[1])
			val = strings.Trim(val, "\"")
			if key == "" {
				return labelMatcher{}, fmt.Errorf("invalid label key in matcher")
			}
			labels[key] = val
		}
	}

	return labelMatcher{metric: metric, labels: labels}, nil
}

func (ph *PrometheusHandler) filterSeries(series []storage.Labels, matchers []labelMatcher, start, end int64, timeFilter bool) ([]storage.Labels, error) {
	filtered := make([]storage.Labels, 0, len(series))
	for _, s := range series {
		labelsMap := make(map[string]string)
		for _, l := range s {
			labelsMap[l.Name] = l.Value
		}

		if len(matchers) > 0 && !matchesAny(labelsMap, matchers) {
			continue
		}

		if timeFilter {
			hasData, err := ph.seriesHasData(s, start, end)
			if err != nil {
				return nil, err
			}
			if !hasData {
				continue
			}
		}

		filtered = append(filtered, s)
	}
	return filtered, nil
}

func matchesAny(labels map[string]string, matchers []labelMatcher) bool {
	for _, m := range matchers {
		if matchesSelector(labels, m) {
			return true
		}
	}
	return false
}

func matchesSelector(labels map[string]string, matcher labelMatcher) bool {
	if matcher.metric != "" {
		metricName, ok := labels["__name__"]
		if !ok || metricName != matcher.metric {
			return false
		}
	}

	for k, v := range matcher.labels {
		if labels[k] != v {
			return false
		}
	}

	return true
}

func (ph *PrometheusHandler) seriesHasData(series storage.Labels, start, end int64) (bool, error) {
	metric, ok := buildMetricFromLabels(series)
	if !ok {
		return false, nil
	}

	timestamps, _, err := ph.tsdb.Get(metric, start, end)
	if err != nil {
		return false, nil
	}
	return len(timestamps) > 0, nil
}

func buildMetricFromLabels(labels storage.Labels) (string, bool) {
	tags := make(map[string]string)
	name := ""
	for _, label := range labels {
		if label.Name == "__name__" {
			name = label.Value
			continue
		}
		tags[label.Name] = label.Value
	}
	if name == "" {
		return "", false
	}
	return buildMetricKey(name, tags), true
}

func extractLabelNameFromPath(path string) (string, bool) {
	if strings.HasPrefix(path, "/api/v1/label/") {
		parts := strings.Split(strings.TrimPrefix(path, "/api/v1/label/"), "/")
		if len(parts) >= 2 && parts[1] == "values" {
			return parts[0], true
		}
		return "", false
	}

	if strings.HasPrefix(path, "/api/v1/labels/") {
		parts := strings.Split(strings.TrimPrefix(path, "/api/v1/labels/"), "/")
		if len(parts) >= 2 && parts[1] == "values" {
			return parts[0], true
		}
		return "", false
	}

	return "", false
}

func parseLabelListParams(r *http.Request) (*regexp.Regexp, int, int, error) {
	filterParam := r.URL.Query().Get("filter")
	limitParam := r.URL.Query().Get("limit")
	offsetParam := r.URL.Query().Get("offset")

	var filterRegex *regexp.Regexp
	if filterParam != "" {
		re, err := regexp.Compile(filterParam)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("invalid filter regex: %v", err)
		}
		filterRegex = re
	}

	limit := 0
	if limitParam != "" {
		v, err := strconv.Atoi(limitParam)
		if err != nil || v < 0 {
			return nil, 0, 0, fmt.Errorf("invalid limit parameter")
		}
		limit = v
	}

	offset := 0
	if offsetParam != "" {
		v, err := strconv.Atoi(offsetParam)
		if err != nil || v < 0 {
			return nil, 0, 0, fmt.Errorf("invalid offset parameter")
		}
		offset = v
	}

	return filterRegex, limit, offset, nil
}

func applyFilter(items []string, re *regexp.Regexp) []string {
	if re == nil {
		return items
	}
	filtered := make([]string, 0, len(items))
	for _, item := range items {
		if re.MatchString(item) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func applyPagination(items []string, limit, offset int) []string {
	if offset < 0 {
		offset = 0
	}
	if offset > len(items) {
		return []string{}
	}
	end := len(items)
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	return items[offset:end]
}
