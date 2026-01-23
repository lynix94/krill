package web

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// AggregationFunc represents a PromQL aggregation function
type AggregationFunc string

const (
	AggSum         AggregationFunc = "sum"
	AggMin         AggregationFunc = "min"
	AggMax         AggregationFunc = "max"
	AggAvg         AggregationFunc = "avg"
	AggStddev      AggregationFunc = "stddev"
	AggStdvar      AggregationFunc = "stdvar"
	AggCount       AggregationFunc = "count"
	AggCountValues AggregationFunc = "count_values"
	AggBottomk     AggregationFunc = "bottomk"
	AggTopk        AggregationFunc = "topk"
	AggQuantile    AggregationFunc = "quantile"
	// Range vector functions
	AggSumOverTime      AggregationFunc = "sum_over_time"
	AggAvgOverTime      AggregationFunc = "avg_over_time"
	AggMinOverTime      AggregationFunc = "min_over_time"
	AggMaxOverTime      AggregationFunc = "max_over_time"
	AggCountOverTime    AggregationFunc = "count_over_time"
	AggStddevOverTime   AggregationFunc = "stddev_over_time"
	AggStdvarOverTime   AggregationFunc = "stdvar_over_time"
	AggQuantileOverTime AggregationFunc = "quantile_over_time"
)

// ParsedQuery represents a parsed PromQL query with aggregation
type ParsedQuery struct {
	AggFunc       AggregationFunc
	AggParam      float64 // For topk, bottomk, quantile
	AggParamLabel string  // For count_values
	MetricName    string
	LabelMatchers map[string]string
	GroupBy       []string // Labels to group by
	RangeVector   int64    // Range in seconds (for _over_time functions)
}

// parsePromQL parses a PromQL query with optional aggregation function
// Examples:
//   - cpu_usage
//   - cpu_usage{host="server1"}
//   - sum(cpu_usage)
//   - avg(cpu_usage) by (host)
//   - topk(5, http_requests)
//   - quantile(0.95, response_time) by (endpoint)
func parsePromQL(query string) ParsedQuery {
	query = strings.TrimSpace(query)
	parsed := ParsedQuery{
		LabelMatchers: make(map[string]string),
	}

	// Check for aggregation function
	for _, aggFunc := range []AggregationFunc{
		AggSum, AggMin, AggMax, AggAvg, AggStddev, AggStdvar,
		AggCount, AggCountValues, AggBottomk, AggTopk, AggQuantile,
		// Range vector functions
		AggSumOverTime, AggAvgOverTime, AggMinOverTime, AggMaxOverTime,
		AggCountOverTime, AggStddevOverTime, AggStdvarOverTime, AggQuantileOverTime,
	} {
		prefix := string(aggFunc) + "("
		if strings.HasPrefix(query, prefix) {
			parsed.AggFunc = aggFunc
			
			// Find matching closing parenthesis
			depth := 0
			endIdx := -1
			for i, ch := range query {
				if ch == '(' {
					depth++
				} else if ch == ')' {
					depth--
					if depth == 0 {
						endIdx = i
						break
					}
				}
			}

			if endIdx > 0 {
				innerQuery := query[len(prefix):endIdx]
				
				// Handle functions with parameters (topk, bottomk, quantile, count_values)
				if aggFunc == AggTopk || aggFunc == AggBottomk || aggFunc == AggQuantile {
					// Format: topk(5, metric) or quantile(0.95, metric)
					parts := strings.SplitN(innerQuery, ",", 2)
					if len(parts) == 2 {
						param, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
						if err == nil {
							parsed.AggParam = param
							innerQuery = strings.TrimSpace(parts[1])
						}
					}
				} else if aggFunc == AggCountValues {
					// Format: count_values("value", metric)
					parts := strings.SplitN(innerQuery, ",", 2)
					if len(parts) == 2 {
						parsed.AggParamLabel = strings.Trim(strings.TrimSpace(parts[0]), "\"")
						innerQuery = strings.TrimSpace(parts[1])
					}
				} else if aggFunc == AggQuantileOverTime {
					// Format: quantile_over_time(0.95, metric[5m])
					parts := strings.SplitN(innerQuery, ",", 2)
					if len(parts) == 2 {
						param, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
						if err == nil {
							parsed.AggParam = param
							innerQuery = strings.TrimSpace(parts[1])
						}
					}
				}

				// Parse range vector selector [5m], [1h], etc.
				if strings.Contains(innerQuery, "[") {
					rangeStart := strings.Index(innerQuery, "[")
					rangeEnd := strings.Index(innerQuery, "]")
					if rangeStart > 0 && rangeEnd > rangeStart {
						rangeStr := innerQuery[rangeStart+1 : rangeEnd]
						parsed.RangeVector = parseDuration(rangeStr)
						innerQuery = innerQuery[:rangeStart] // Remove range selector from query
					}
				}

				// Parse the inner metric query
				parsed.MetricName, parsed.LabelMatchers = parseMetricKey(innerQuery)

				// Check for "by" clause after the closing parenthesis
				remaining := strings.TrimSpace(query[endIdx+1:])
				if strings.HasPrefix(remaining, "by") {
					remaining = strings.TrimPrefix(remaining, "by")
					remaining = strings.TrimSpace(remaining)
					if strings.HasPrefix(remaining, "(") && strings.HasSuffix(remaining, ")") {
						groupByStr := remaining[1 : len(remaining)-1]
						labels := strings.Split(groupByStr, ",")
						for _, label := range labels {
							parsed.GroupBy = append(parsed.GroupBy, strings.TrimSpace(label))
						}
					}
				}
			}
			return parsed
		}
	}

	// No aggregation function, just parse metric name and labels
	parsed.MetricName, parsed.LabelMatchers = parseMetricKey(query)
	return parsed
}

// parseDuration converts PromQL duration string to seconds
// Examples: "5m" -> 300, "1h" -> 3600, "30s" -> 30, "1d" -> 86400
func parseDuration(s string) int64 {
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return 0
	}

	// Extract number and unit
	numStr := s[:len(s)-1]
	unit := s[len(s)-1:]

	num, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		return 0
	}

	switch unit {
	case "s":
		return num
	case "m":
		return num * 60
	case "h":
		return num * 3600
	case "d":
		return num * 86400
	case "w":
		return num * 604800
	case "y":
		return num * 31536000
	default:
		return 0
	}
}

// applyAggregation applies the aggregation function to query results
func applyAggregation(parsed ParsedQuery, results []QueryResult) []QueryResult {
	if parsed.AggFunc == "" {
		return results // No aggregation
	}

	// Group results if needed
	groups := groupResults(results, parsed.GroupBy)

	var aggregated []QueryResult

	for groupKey, groupResults := range groups {
		var aggResult QueryResult

		// Build metric labels from group key
		aggResult.Metric = parseGroupKey(groupKey, parsed.GroupBy)

		switch parsed.AggFunc {
		case AggSum:
			aggResult = aggregateSum(groupResults)
		case AggMin:
			aggResult = aggregateMin(groupResults)
		case AggMax:
			aggResult = aggregateMax(groupResults)
		case AggAvg:
			aggResult = aggregateAvg(groupResults)
		case AggStddev:
			aggResult = aggregateStddev(groupResults)
		case AggStdvar:
			aggResult = aggregateStdvar(groupResults)
		case AggCount:
			aggResult = aggregateCount(groupResults)
		case AggCountValues:
			return aggregateCountValues(groupResults, parsed.AggParamLabel)
		case AggBottomk:
			aggResult = aggregateBottomk(groupResults, int(parsed.AggParam))
		case AggTopk:
			aggResult = aggregateTopk(groupResults, int(parsed.AggParam))
		case AggQuantile:
			aggResult = aggregateQuantile(groupResults, parsed.AggParam)
		// Range vector functions
		case AggSumOverTime:
			aggResult = aggregateSumOverTime(groupResults)
		case AggAvgOverTime:
			aggResult = aggregateAvgOverTime(groupResults)
		case AggMinOverTime:
			aggResult = aggregateMinOverTime(groupResults)
		case AggMaxOverTime:
			aggResult = aggregateMaxOverTime(groupResults)
		case AggCountOverTime:
			aggResult = aggregateCountOverTime(groupResults)
		case AggStddevOverTime:
			aggResult = aggregateStddevOverTime(groupResults)
		case AggStdvarOverTime:
			aggResult = aggregateStdvarOverTime(groupResults)
		case AggQuantileOverTime:
			aggResult = aggregateQuantileOverTime(groupResults, parsed.AggParam)
		}

		// Preserve group-by labels
		for k, v := range parseGroupKey(groupKey, parsed.GroupBy) {
			aggResult.Metric[k] = v
		}

		aggregated = append(aggregated, aggResult)
	}

	return aggregated
}

// groupResults groups results by specified labels
func groupResults(results []QueryResult, groupBy []string) map[string][]QueryResult {
	groups := make(map[string][]QueryResult)

	for _, result := range results {
		key := buildGroupKey(result.Metric, groupBy)
		groups[key] = append(groups[key], result)
	}

	return groups
}

// buildGroupKey creates a unique key for a group
func buildGroupKey(metric map[string]string, groupBy []string) string {
	if len(groupBy) == 0 {
		return "" // Single group
	}

	var parts []string
	for _, label := range groupBy {
		if val, ok := metric[label]; ok {
			parts = append(parts, fmt.Sprintf("%s=%s", label, val))
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// parseGroupKey converts a group key back to metric labels
func parseGroupKey(key string, groupBy []string) map[string]string {
	labels := make(map[string]string)
	if key == "" {
		return labels
	}

	pairs := strings.Split(key, ",")
	for _, pair := range pairs {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 2 {
			labels[parts[0]] = parts[1]
		}
	}
	return labels
}

// Aggregation functions

func aggregateSum(results []QueryResult) QueryResult {
	result := QueryResult{Metric: make(map[string]string)}

	if len(results) == 0 {
		return result
	}

	// Check if this is instant query (Value) or range query (Values)
	if len(results[0].Value) > 0 {
		// Instant query
		var sum float64
		var timestamp int64

		for _, r := range results {
			if len(r.Value) >= 2 {
				if ts, ok := r.Value[0].(int64); ok {
					timestamp = ts
				}
				if valStr, ok := r.Value[1].(string); ok {
					val, _ := strconv.ParseFloat(valStr, 64)
					sum += val
				}
			}
		}

		result.Value = []interface{}{timestamp, fmt.Sprintf("%f", sum)}
	} else {
		// Range query - sum values at each timestamp
		result.Values = aggregateRangeSum(results)
	}

	return result
}

func aggregateMin(results []QueryResult) QueryResult {
	result := QueryResult{Metric: make(map[string]string)}

	if len(results) == 0 {
		return result
	}

	if len(results[0].Value) > 0 {
		minVal := math.Inf(1)
		var timestamp int64

		for _, r := range results {
			if len(r.Value) >= 2 {
				if ts, ok := r.Value[0].(int64); ok {
					timestamp = ts
				}
				if valStr, ok := r.Value[1].(string); ok {
					val, _ := strconv.ParseFloat(valStr, 64)
					if val < minVal {
						minVal = val
					}
				}
			}
		}

		result.Value = []interface{}{timestamp, fmt.Sprintf("%f", minVal)}
	} else {
		result.Values = aggregateRangeMin(results)
	}

	return result
}

func aggregateMax(results []QueryResult) QueryResult {
	result := QueryResult{Metric: make(map[string]string)}

	if len(results) == 0 {
		return result
	}

	if len(results[0].Value) > 0 {
		maxVal := math.Inf(-1)
		var timestamp int64

		for _, r := range results {
			if len(r.Value) >= 2 {
				if ts, ok := r.Value[0].(int64); ok {
					timestamp = ts
				}
				if valStr, ok := r.Value[1].(string); ok {
					val, _ := strconv.ParseFloat(valStr, 64)
					if val > maxVal {
						maxVal = val
					}
				}
			}
		}

		result.Value = []interface{}{timestamp, fmt.Sprintf("%f", maxVal)}
	} else {
		result.Values = aggregateRangeMax(results)
	}

	return result
}

func aggregateAvg(results []QueryResult) QueryResult {
	result := QueryResult{Metric: make(map[string]string)}

	if len(results) == 0 {
		return result
	}

	if len(results[0].Value) > 0 {
		var sum float64
		var count int
		var timestamp int64

		for _, r := range results {
			if len(r.Value) >= 2 {
				if ts, ok := r.Value[0].(int64); ok {
					timestamp = ts
				}
				if valStr, ok := r.Value[1].(string); ok {
					val, _ := strconv.ParseFloat(valStr, 64)
					sum += val
					count++
				}
			}
		}

		avg := sum / float64(count)
		result.Value = []interface{}{timestamp, fmt.Sprintf("%f", avg)}
	} else {
		result.Values = aggregateRangeAvg(results)
	}

	return result
}

func aggregateStddev(results []QueryResult) QueryResult {
	result := aggregateStdvar(results)
	
	if len(result.Value) >= 2 {
		if valStr, ok := result.Value[1].(string); ok {
			variance, _ := strconv.ParseFloat(valStr, 64)
			stddev := math.Sqrt(variance)
			result.Value[1] = fmt.Sprintf("%f", stddev)
		}
	} else if len(result.Values) > 0 {
		for i := range result.Values {
			if len(result.Values[i]) >= 2 {
				if valStr, ok := result.Values[i][1].(string); ok {
					variance, _ := strconv.ParseFloat(valStr, 64)
					stddev := math.Sqrt(variance)
					result.Values[i][1] = fmt.Sprintf("%f", stddev)
				}
			}
		}
	}

	return result
}

func aggregateStdvar(results []QueryResult) QueryResult {
	result := QueryResult{Metric: make(map[string]string)}

	if len(results) == 0 {
		return result
	}

	if len(results[0].Value) > 0 {
		var values []float64
		var timestamp int64

		for _, r := range results {
			if len(r.Value) >= 2 {
				if ts, ok := r.Value[0].(int64); ok {
					timestamp = ts
				}
				if valStr, ok := r.Value[1].(string); ok {
					val, _ := strconv.ParseFloat(valStr, 64)
					values = append(values, val)
				}
			}
		}

		variance := calculateVariance(values)
		result.Value = []interface{}{timestamp, fmt.Sprintf("%f", variance)}
	} else {
		result.Values = aggregateRangeStdvar(results)
	}

	return result
}

func aggregateCount(results []QueryResult) QueryResult {
	result := QueryResult{Metric: make(map[string]string)}

	if len(results) == 0 {
		return result
	}

	if len(results[0].Value) > 0 {
		count := len(results)
		var timestamp int64
		if len(results[0].Value) >= 1 {
			if ts, ok := results[0].Value[0].(int64); ok {
				timestamp = ts
			}
		}
		result.Value = []interface{}{timestamp, fmt.Sprintf("%f", float64(count))}
	} else {
		result.Values = aggregateRangeCount(results)
	}

	return result
}

func aggregateCountValues(results []QueryResult, labelName string) []QueryResult {
	if labelName == "" {
		labelName = "value"
	}

	valueCounts := make(map[string]int)

	// Count occurrences of each value
	for _, r := range results {
		if len(r.Value) >= 2 {
			if valStr, ok := r.Value[1].(string); ok {
				valueCounts[valStr]++
			}
		} else if len(r.Values) > 0 {
			// For range queries, count last value
			lastIdx := len(r.Values) - 1
			if len(r.Values[lastIdx]) >= 2 {
				if valStr, ok := r.Values[lastIdx][1].(string); ok {
					valueCounts[valStr]++
				}
			}
		}
	}

	var counted []QueryResult
	var timestamp int64
	if len(results) > 0 && len(results[0].Value) >= 1 {
		if ts, ok := results[0].Value[0].(int64); ok {
			timestamp = ts
		}
	}

	for value, count := range valueCounts {
		counted = append(counted, QueryResult{
			Metric: map[string]string{labelName: value},
			Value:  []interface{}{timestamp, fmt.Sprintf("%f", float64(count))},
		})
	}

	return counted
}

func aggregateTopk(results []QueryResult, k int) QueryResult {
	if k <= 0 || len(results) == 0 {
		return QueryResult{Metric: make(map[string]string)}
	}

	// Sort by value (descending)
	sorted := make([]QueryResult, len(results))
	copy(sorted, results)

	sort.Slice(sorted, func(i, j int) bool {
		vi := getLastValue(sorted[i])
		vj := getLastValue(sorted[j])
		return vi > vj
	})

	if k > len(sorted) {
		k = len(sorted)
	}

	// Return only top k
	return mergeResults(sorted[:k])
}

func aggregateBottomk(results []QueryResult, k int) QueryResult {
	if k <= 0 || len(results) == 0 {
		return QueryResult{Metric: make(map[string]string)}
	}

	// Sort by value (ascending)
	sorted := make([]QueryResult, len(results))
	copy(sorted, results)

	sort.Slice(sorted, func(i, j int) bool {
		vi := getLastValue(sorted[i])
		vj := getLastValue(sorted[j])
		return vi < vj
	})

	if k > len(sorted) {
		k = len(sorted)
	}

	// Return only bottom k
	return mergeResults(sorted[:k])
}

func aggregateQuantile(results []QueryResult, quantile float64) QueryResult {
	result := QueryResult{Metric: make(map[string]string)}

	if len(results) == 0 || quantile < 0 || quantile > 1 {
		return result
	}

	var values []float64
	var timestamp int64

	for _, r := range results {
		if len(r.Value) >= 2 {
			if ts, ok := r.Value[0].(int64); ok {
				timestamp = ts
			}
			if valStr, ok := r.Value[1].(string); ok {
				val, _ := strconv.ParseFloat(valStr, 64)
				values = append(values, val)
			}
		}
	}

	if len(values) == 0 {
		return result
	}

	sort.Float64s(values)
	idx := int(float64(len(values)-1) * quantile)
	result.Value = []interface{}{timestamp, fmt.Sprintf("%f", values[idx])}

	return result
}

// Helper functions for range queries

func aggregateRangeSum(results []QueryResult) [][]interface{} {
	timeMap := make(map[int64]float64)

	for _, r := range results {
		for _, valPair := range r.Values {
			if len(valPair) >= 2 {
				ts, _ := valPair[0].(int64)
				valStr, _ := valPair[1].(string)
				val, _ := strconv.ParseFloat(valStr, 64)
				timeMap[ts] += val
			}
		}
	}

	return convertTimeMapToValues(timeMap)
}

func aggregateRangeMin(results []QueryResult) [][]interface{} {
	timeMap := make(map[int64]float64)

	for _, r := range results {
		for _, valPair := range r.Values {
			if len(valPair) >= 2 {
				ts, _ := valPair[0].(int64)
				valStr, _ := valPair[1].(string)
				val, _ := strconv.ParseFloat(valStr, 64)
				
				if existing, ok := timeMap[ts]; !ok || val < existing {
					timeMap[ts] = val
				}
			}
		}
	}

	return convertTimeMapToValues(timeMap)
}

func aggregateRangeMax(results []QueryResult) [][]interface{} {
	timeMap := make(map[int64]float64)

	for _, r := range results {
		for _, valPair := range r.Values {
			if len(valPair) >= 2 {
				ts, _ := valPair[0].(int64)
				valStr, _ := valPair[1].(string)
				val, _ := strconv.ParseFloat(valStr, 64)
				
				if existing, ok := timeMap[ts]; !ok || val > existing {
					timeMap[ts] = val
				}
			}
		}
	}

	return convertTimeMapToValues(timeMap)
}

func aggregateRangeAvg(results []QueryResult) [][]interface{} {
	timeSums := make(map[int64]float64)
	timeCounts := make(map[int64]int)

	for _, r := range results {
		for _, valPair := range r.Values {
			if len(valPair) >= 2 {
				ts, _ := valPair[0].(int64)
				valStr, _ := valPair[1].(string)
				val, _ := strconv.ParseFloat(valStr, 64)
				timeSums[ts] += val
				timeCounts[ts]++
			}
		}
	}

	timeMap := make(map[int64]float64)
	for ts, sum := range timeSums {
		timeMap[ts] = sum / float64(timeCounts[ts])
	}

	return convertTimeMapToValues(timeMap)
}

func aggregateRangeStdvar(results []QueryResult) [][]interface{} {
	timeValues := make(map[int64][]float64)

	for _, r := range results {
		for _, valPair := range r.Values {
			if len(valPair) >= 2 {
				ts, _ := valPair[0].(int64)
				valStr, _ := valPair[1].(string)
				val, _ := strconv.ParseFloat(valStr, 64)
				timeValues[ts] = append(timeValues[ts], val)
			}
		}
	}

	timeMap := make(map[int64]float64)
	for ts, values := range timeValues {
		timeMap[ts] = calculateVariance(values)
	}

	return convertTimeMapToValues(timeMap)
}

func aggregateRangeCount(results []QueryResult) [][]interface{} {
	timeCounts := make(map[int64]int)

	for _, r := range results {
		for _, valPair := range r.Values {
			if len(valPair) >= 2 {
				ts, _ := valPair[0].(int64)
				timeCounts[ts]++
			}
		}
	}

	timeMap := make(map[int64]float64)
	for ts, count := range timeCounts {
		timeMap[ts] = float64(count)
	}

	return convertTimeMapToValues(timeMap)
}

// Utility functions

func calculateVariance(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	// Calculate mean
	var sum float64
	for _, v := range values {
		sum += v
	}
	mean := sum / float64(len(values))

	// Calculate variance
	var variance float64
	for _, v := range values {
		diff := v - mean
		variance += diff * diff
	}
	variance /= float64(len(values))

	return variance
}

func getLastValue(result QueryResult) float64 {
	if len(result.Value) >= 2 {
		if valStr, ok := result.Value[1].(string); ok {
			val, _ := strconv.ParseFloat(valStr, 64)
			return val
		}
	} else if len(result.Values) > 0 {
		lastIdx := len(result.Values) - 1
		if len(result.Values[lastIdx]) >= 2 {
			if valStr, ok := result.Values[lastIdx][1].(string); ok {
				val, _ := strconv.ParseFloat(valStr, 64)
				return val
			}
		}
	}
	return 0
}

func mergeResults(results []QueryResult) QueryResult {
	// For topk/bottomk, we return the first result's format
	// but this is a simplified version
	if len(results) == 0 {
		return QueryResult{Metric: make(map[string]string)}
	}
	return results[0]
}

func convertTimeMapToValues(timeMap map[int64]float64) [][]interface{} {
	// Sort by timestamp
	var timestamps []int64
	for ts := range timeMap {
		timestamps = append(timestamps, ts)
	}
	sort.Slice(timestamps, func(i, j int) bool {
		return timestamps[i] < timestamps[j]
	})

	var values [][]interface{}
	for _, ts := range timestamps {
		values = append(values, []interface{}{ts, fmt.Sprintf("%f", timeMap[ts])})
	}

	return values
}

// Range vector aggregation functions (_over_time)

func aggregateSumOverTime(results []QueryResult) QueryResult {
	result := QueryResult{Metric: make(map[string]string)}

	if len(results) == 0 {
		return result
	}

	// For each series, sum all values in its time range
	for _, r := range results {
		var sum float64
		var timestamp int64

		// If it's a range query result
		if len(r.Values) > 0 {
			for _, valPair := range r.Values {
				if len(valPair) >= 2 {
					ts, _ := valPair[0].(int64)
					valStr, _ := valPair[1].(string)
					val, _ := strconv.ParseFloat(valStr, 64)
					sum += val
					timestamp = ts // Use last timestamp
				}
			}
			// Return single value (sum of all points in range)
			result.Value = []interface{}{timestamp, fmt.Sprintf("%f", sum)}
			result.Metric = r.Metric
			return result
		} else if len(r.Value) >= 2 {
			// Instant query - just return the value
			result.Value = r.Value
			result.Metric = r.Metric
			return result
		}
	}

	return result
}

func aggregateAvgOverTime(results []QueryResult) QueryResult {
	result := QueryResult{Metric: make(map[string]string)}

	if len(results) == 0 {
		return result
	}

	for _, r := range results {
		var sum float64
		var count int
		var timestamp int64

		if len(r.Values) > 0 {
			for _, valPair := range r.Values {
				if len(valPair) >= 2 {
					ts, _ := valPair[0].(int64)
					valStr, _ := valPair[1].(string)
					val, _ := strconv.ParseFloat(valStr, 64)
					sum += val
					count++
					timestamp = ts
				}
			}
			if count > 0 {
				avg := sum / float64(count)
				result.Value = []interface{}{timestamp, fmt.Sprintf("%f", avg)}
				result.Metric = r.Metric
				return result
			}
		} else if len(r.Value) >= 2 {
			result.Value = r.Value
			result.Metric = r.Metric
			return result
		}
	}

	return result
}

func aggregateMinOverTime(results []QueryResult) QueryResult {
	result := QueryResult{Metric: make(map[string]string)}

	if len(results) == 0 {
		return result
	}

	for _, r := range results {
		minVal := math.Inf(1)
		var timestamp int64

		if len(r.Values) > 0 {
			for _, valPair := range r.Values {
				if len(valPair) >= 2 {
					ts, _ := valPair[0].(int64)
					valStr, _ := valPair[1].(string)
					val, _ := strconv.ParseFloat(valStr, 64)
					if val < minVal {
						minVal = val
					}
					timestamp = ts
				}
			}
			if !math.IsInf(minVal, 1) {
				result.Value = []interface{}{timestamp, fmt.Sprintf("%f", minVal)}
				result.Metric = r.Metric
				return result
			}
		} else if len(r.Value) >= 2 {
			result.Value = r.Value
			result.Metric = r.Metric
			return result
		}
	}

	return result
}

func aggregateMaxOverTime(results []QueryResult) QueryResult {
	result := QueryResult{Metric: make(map[string]string)}

	if len(results) == 0 {
		return result
	}

	for _, r := range results {
		maxVal := math.Inf(-1)
		var timestamp int64

		if len(r.Values) > 0 {
			for _, valPair := range r.Values {
				if len(valPair) >= 2 {
					ts, _ := valPair[0].(int64)
					valStr, _ := valPair[1].(string)
					val, _ := strconv.ParseFloat(valStr, 64)
					if val > maxVal {
						maxVal = val
					}
					timestamp = ts
				}
			}
			if !math.IsInf(maxVal, -1) {
				result.Value = []interface{}{timestamp, fmt.Sprintf("%f", maxVal)}
				result.Metric = r.Metric
				return result
			}
		} else if len(r.Value) >= 2 {
			result.Value = r.Value
			result.Metric = r.Metric
			return result
		}
	}

	return result
}

func aggregateCountOverTime(results []QueryResult) QueryResult {
	result := QueryResult{Metric: make(map[string]string)}

	if len(results) == 0 {
		return result
	}

	for _, r := range results {
		var count int
		var timestamp int64

		if len(r.Values) > 0 {
			count = len(r.Values)
			if len(r.Values) > 0 && len(r.Values[len(r.Values)-1]) >= 1 {
				timestamp, _ = r.Values[len(r.Values)-1][0].(int64)
			}
			result.Value = []interface{}{timestamp, fmt.Sprintf("%f", float64(count))}
			result.Metric = r.Metric
			return result
		} else if len(r.Value) >= 2 {
			result.Value = []interface{}{r.Value[0], "1.000000"}
			result.Metric = r.Metric
			return result
		}
	}

	return result
}

func aggregateStddevOverTime(results []QueryResult) QueryResult {
	result := aggregateStdvarOverTime(results)

	if len(result.Value) >= 2 {
		if valStr, ok := result.Value[1].(string); ok {
			variance, _ := strconv.ParseFloat(valStr, 64)
			stddev := math.Sqrt(variance)
			result.Value[1] = fmt.Sprintf("%f", stddev)
		}
	}

	return result
}

func aggregateStdvarOverTime(results []QueryResult) QueryResult {
	result := QueryResult{Metric: make(map[string]string)}

	if len(results) == 0 {
		return result
	}

	for _, r := range results {
		var values []float64
		var timestamp int64

		if len(r.Values) > 0 {
			for _, valPair := range r.Values {
				if len(valPair) >= 2 {
					ts, _ := valPair[0].(int64)
					valStr, _ := valPair[1].(string)
					val, _ := strconv.ParseFloat(valStr, 64)
					values = append(values, val)
					timestamp = ts
				}
			}
			if len(values) > 0 {
				variance := calculateVariance(values)
				result.Value = []interface{}{timestamp, fmt.Sprintf("%f", variance)}
				result.Metric = r.Metric
				return result
			}
		} else if len(r.Value) >= 2 {
			result.Value = []interface{}{r.Value[0], "0.000000"}
			result.Metric = r.Metric
			return result
		}
	}

	return result
}

func aggregateQuantileOverTime(results []QueryResult, quantile float64) QueryResult {
	result := QueryResult{Metric: make(map[string]string)}

	if len(results) == 0 || quantile < 0 || quantile > 1 {
		return result
	}

	for _, r := range results {
		var values []float64
		var timestamp int64

		if len(r.Values) > 0 {
			for _, valPair := range r.Values {
				if len(valPair) >= 2 {
					ts, _ := valPair[0].(int64)
					valStr, _ := valPair[1].(string)
					val, _ := strconv.ParseFloat(valStr, 64)
					values = append(values, val)
					timestamp = ts
				}
			}
			if len(values) > 0 {
				sort.Float64s(values)
				idx := int(float64(len(values)-1) * quantile)
				result.Value = []interface{}{timestamp, fmt.Sprintf("%f", values[idx])}
				result.Metric = r.Metric
				return result
			}
		} else if len(r.Value) >= 2 {
			result.Value = r.Value
			result.Metric = r.Metric
			return result
		}
	}

	return result
}
