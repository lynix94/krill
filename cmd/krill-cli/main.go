package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/chzyer/readline"
)

const usage = `krill-cli - Command line tool for Krill TSDB

Usage:
  krill-cli -server <url>                                    # Interactive mode
  krill-cli -server <url> query_range <metric> <start> <end> # Single command
  krill-cli -server <url> put <timestamp> <metric> <value> [...]

Modes:
  Interactive   Start without command to enter interactive shell
                - Command history (saved to ~/.krill_history)
                - Line editing with arrow keys
                - Type 'exit' or Ctrl+D to quit

  Single        Execute one command and exit

Commands:
  query_range  Query metric data within a time range
               Alias: query
  put          Insert one or more metric data points
               Alias: write
  metrics      List all metrics (optionally filtered by regex)
  help         Show this help message
  exit         Exit interactive mode (interactive only)

Options:
  -server string
        Krill server URL (default "http://localhost:8080")

Timestamp Format:
  - "now" for current timestamp
  - Unix timestamp (e.g., 1706000000)
  - Relative time: "now-1h", "now-30m", "now-1d"

Examples:
  # Interactive mode
  krill-cli -server http://localhost:9090
  krill> put now cpu.usage 45.5
  krill> query cpu.usage 0 9999999999
  krill> metrics
  krill> metrics network
  krill> exit

  # Single commands
  krill-cli -server http://localhost:9090 query_range cpu.usage 0 9999999999
  krill-cli -server http://localhost:9090 put now cpu.usage 45.5
  krill-cli -server http://localhost:9090 metrics
  krill-cli -server http://localhost:9090 metrics "^http"

  # Multiple metrics
  krill-cli put now \
    'http_requests{method="GET",status="200"}' 150 \
    'http_requests{method="POST",status="201"}' 42

  # Relative time
  krill-cli put now-1h cpu.load 1.5
`

var serverURL string

type QueryRangeRequest struct {
	Metric  string `json:"metric"`
	StartTs int64  `json:"start_ts"`
	EndTs   int64  `json:"end_ts"`
}

type QueryRangeResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Values [][]interface{}   `json:"values,omitempty"` // For range queries: [[timestamp, value], ...]
			Value  []interface{}     `json:"value,omitempty"`  // For instant queries: [timestamp, value]
		} `json:"result"`
	} `json:"data"`
}

type WriteRequest struct {
	Timestamp int64   `json:"timestamp"`
	Metric    string  `json:"metric"`
	Value     float64 `json:"value"`
}

func main() {
	flag.StringVar(&serverURL, "server", "http://localhost:8080", "Krill server URL")
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, usage)
	}
	flag.Parse()

	args := flag.Args()
	
	// If no command specified, enter interactive mode
	if len(args) < 1 {
		if err := runInteractive(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Execute single command
	if err := executeCommand(args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func executeCommand(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("no command specified")
	}

	command := args[0]

	switch command {
	case "query_range", "query":
		return handleQueryRange(args[1:])
	case "put", "write":
		return handlePut(args[1:])
	case "metrics":
		return handleMetrics(args[1:])
	case "exit", "quit":
		return nil
	case "help":
		flag.Usage()
		return nil
	default:
		return fmt.Errorf("unknown command '%s' (use 'help' for usage)", command)
	}
}

func runInteractive() error {
	// Get history file path
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "."
	}
	historyFile := filepath.Join(homeDir, ".krill_history")

	// Configure readline with explicit terminal settings
	rl, err := readline.NewEx(&readline.Config{
		Prompt:                 "krill> ",
		HistoryFile:            historyFile,
		InterruptPrompt:        "^C",
		EOFPrompt:              "exit",
		VimMode:                false,
		Stdin:                  readline.NewCancelableStdin(os.Stdin),
		Stdout:                 os.Stdout,
		Stderr:                 os.Stderr,
		DisableAutoSaveHistory: false,
		EnableMask:             false,
		UniqueEditLine:         true,
		FuncFilterInputRune: func(r rune) (rune, bool) {
			return r, true
		},
	})
	if err != nil {
		return fmt.Errorf("failed to initialize readline: %w", err)
	}
	defer rl.Close()

	fmt.Println("Krill CLI - Interactive Mode")
	fmt.Printf("Connected to: %s\n", serverURL)
	fmt.Println("Type 'help' for usage, 'exit' to quit")
	fmt.Println()
	fmt.Println("Keyboard shortcuts:")
	fmt.Println("  Ctrl+A         - Move to beginning of line (alternative to Home)")
	fmt.Println("  Ctrl+E         - Move to end of line (alternative to End)")
	fmt.Println("  Ctrl+B / ←     - Move backward one character")
	fmt.Println("  Ctrl+F / →     - Move forward one character")
	fmt.Println("  Ctrl+W         - Delete word before cursor")
	fmt.Println("  Ctrl+U         - Clear line before cursor")
	fmt.Println("  Ctrl+K         - Clear line after cursor")
	fmt.Println("  Up/Down or ↑/↓ - Navigate command history")
	fmt.Println("  Ctrl+C         - Cancel current input")
	fmt.Println("  Ctrl+D         - Exit (on empty line)")
	fmt.Println()

	for {
		line, err := rl.Readline()
		if err != nil {
			// EOF or interrupt
			if err == readline.ErrInterrupt {
				if len(line) == 0 {
					break
				} else {
					continue
				}
			} else {
				break
			}
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Parse line into arguments
		args := parseCommandLine(line)
		if len(args) == 0 {
			continue
		}

		// Handle exit
		if args[0] == "exit" || args[0] == "quit" {
			break
		}

		// Echo the command being executed
		fmt.Printf("krill> %s\n", line)

		// Execute command
		if err := executeCommand(args); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		fmt.Println()
	}

	fmt.Println("Goodbye!")
	return nil
}

// parseCommandLine splits a command line into arguments, respecting quotes
func parseCommandLine(line string) []string {
	var args []string
	var current strings.Builder
	inQuote := false
	quoteChar := rune(0)

	for _, r := range line {
		switch {
		case r == '"' || r == '\'':
			if !inQuote {
				inQuote = true
				quoteChar = r
			} else if r == quoteChar {
				inQuote = false
				quoteChar = 0
			} else {
				current.WriteRune(r)
			}
		case r == ' ' && !inQuote:
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}

	if current.Len() > 0 {
		args = append(args, current.String())
	}

	return args
}

func handleInstantQuery(metric string) error {
	// Build GET request URL for instant query
	queryURL := fmt.Sprintf("%s/api/v1/query?query=%s",
		serverURL, url.QueryEscape(metric))

	resp, err := http.Get(queryURL)
	if err != nil {
		return fmt.Errorf("failed to query server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned error (status %d): %s", resp.StatusCode, string(body))
	}

	var result QueryRangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if result.Status != "success" {
		return fmt.Errorf("server returned error status")
	}

	if len(result.Data.Result) == 0 {
		fmt.Println("No data found")
		return nil
	}

	// Print results
	fmt.Printf("Found %d series\n", len(result.Data.Result))
	fmt.Println(strings.Repeat("=", 80))
	
	for idx, series := range result.Data.Result {
		fmt.Printf("\n[Series %d/%d]\n", idx+1, len(result.Data.Result))
		fmt.Printf("Metric: ")
		first := true
		for k, v := range series.Metric {
			if !first {
				fmt.Printf(", ")
			}
			fmt.Printf("%s=%s", k, v)
			first = false
		}
		fmt.Println()
		
		if len(series.Value) >= 2 {
			ts := int64(series.Value[0].(float64))
			val := series.Value[1].(string)
			fmt.Printf("Value: %s (at %s)\n", val, time.Unix(ts, 0).Format("2006-01-02 15:04:05"))
		}
	}
	fmt.Println()
	
	return nil
}

func handleInstantQueryWithTime(metric string, queryTime int64) error {
	// Build GET request URL for instant query with specific time
	queryURL := fmt.Sprintf("%s/api/v1/query?query=%s&time=%d",
		serverURL, url.QueryEscape(metric), queryTime)

	resp, err := http.Get(queryURL)
	if err != nil {
		return fmt.Errorf("failed to query server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned error (status %d): %s", resp.StatusCode, string(body))
	}

	var result QueryRangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if result.Status != "success" {
		return fmt.Errorf("server returned error status")
	}

	if len(result.Data.Result) == 0 {
		fmt.Println("No data found")
		return nil
	}

	// Print results (same as instant query)
	fmt.Printf("Found %d series\n", len(result.Data.Result))
	fmt.Println(strings.Repeat("=", 80))
	
	for idx, series := range result.Data.Result {
		fmt.Printf("\n[Series %d/%d]\n", idx+1, len(result.Data.Result))
		fmt.Printf("Metric: ")
		first := true
		for k, v := range series.Metric {
			if !first {
				fmt.Printf(", ")
			}
			fmt.Printf("%s=%s", k, v)
			first = false
		}
		fmt.Println()
		
		if len(series.Value) >= 2 {
			ts := int64(series.Value[0].(float64))
			val := series.Value[1].(string)
			fmt.Printf("Value: %s (at %s)\n", val, time.Unix(ts, 0).Format("2006-01-02 15:04:05"))
		}
	}
	fmt.Println()
	
	return nil
}

func handleQueryRange(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("query_range requires: <metric> [<start_ts> <end_ts>]")
	}

	// Reconstruct the full query string in case it contains spaces (e.g., aggregation functions)
	// Find where the metric query ends and timestamps begin
	var metric string
	var startTs, endTs int64
	var err error

	// If only one argument, it's just the metric (instant query)
	if len(args) == 1 {
		metric = args[0]
		// Use instant query API instead
		return handleInstantQuery(metric)
	}

	// If we have 3+ args, need to figure out where metric ends
	// Common patterns:
	// - "cpu_usage" "0" "now"
	// - "sum(cpu_usage)" "0" "now"
	// - "sum(cpu_usage) by (host)" "0" "now"
	
	// Try to parse last two args as timestamps first
	if len(args) >= 3 {
		// Check if last two arguments could be timestamps
		testStart, err1 := parseTimestamp(args[len(args)-2])
		testEnd, err2 := parseTimestamp(args[len(args)-1])
		
		if err1 == nil && err2 == nil {
			// Last two are timestamps, everything before is the metric
			metric = strings.Join(args[:len(args)-2], " ")
			startTs = testStart
			endTs = testEnd
		} else {
			// Couldn't parse as timestamps, try old behavior
			metric = args[0]
			startTs, err = parseTimestamp(args[1])
			if err != nil {
				return fmt.Errorf("invalid start timestamp: %w", err)
			}
			endTs, err = parseTimestamp(args[2])
			if err != nil {
				return fmt.Errorf("invalid end timestamp: %w", err)
			}
		}
	} else if len(args) == 2 {
		// Two args: metric and one timestamp? Treat as instant query with time
		metric = args[0]
		queryTime, err := parseTimestamp(args[1])
		if err != nil {
			return fmt.Errorf("invalid timestamp: %w", err)
		}
		return handleInstantQueryWithTime(metric, queryTime)
	}

	// Build GET request URL with query parameters
	// Add default step for rate/irate queries
	queryURL := fmt.Sprintf("%s/api/v1/query_range?query=%s&start=%d&end=%d",
		serverURL, url.QueryEscape(metric), startTs, endTs)
	
	// Auto-add step for rate/irate queries (15 second intervals)
	if strings.Contains(metric, "rate(") || strings.Contains(metric, "irate(") {
		step := int64(15) // 15 second default step
		// Adjust step based on time range
		timeRange := endTs - startTs
		if timeRange > 3600 {
			// For ranges > 1 hour, use 1 minute step
			step = 60
		}
		if timeRange > 86400 {
			// For ranges > 1 day, use 5 minute step
			step = 300
		}
		queryURL += fmt.Sprintf("&step=%d", step)
	}

	resp, err := http.Get(queryURL)
	if err != nil {
		return fmt.Errorf("failed to query server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned error (status %d): %s", resp.StatusCode, string(body))
	}

	var result QueryRangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if result.Status != "success" {
		return fmt.Errorf("server returned error status")
	}

	if len(result.Data.Result) == 0 {
		fmt.Println("No data found")
		return nil
	}

	// Print summary
	fmt.Printf("Found %d series\n", len(result.Data.Result))
	fmt.Println(strings.Repeat("=", 80))
	fmt.Println()

	// Print results for each series
	for idx, series := range result.Data.Result {
		fmt.Printf("[Series %d/%d]\n", idx+1, len(result.Data.Result))
		// Print metric labels
		fmt.Printf("Metric: ")
		first := true
		for k, v := range series.Metric {
			if !first {
				fmt.Printf(", ")
			}
			fmt.Printf("%s=%s", k, v)
			first = false
		}
		
		// Check if this is a range query (multiple values) or instant query (single value)
		if len(series.Values) > 0 {
			// Multiple values (range query)
			fmt.Printf("\nData Points: %d\n\n", len(series.Values))

			fmt.Println("Timestamp                | Value")
			fmt.Println("-------------------------|----------------")
			for _, point := range series.Values {
				if len(point) >= 2 {
					// Parse timestamp (can be int64 or float64)
					var ts int64
					switch v := point[0].(type) {
					case float64:
						ts = int64(v)
					case int64:
						ts = v
					}

					// Parse value (string)
					valueStr, ok := point[1].(string)
					if !ok {
						continue
					}
					value, err := strconv.ParseFloat(valueStr, 64)
					if err != nil {
						continue
					}

					timestamp := time.Unix(ts, 0)
					fmt.Printf("%s | %.6f\n", timestamp.Format("2006-01-02 15:04:05"), value)
				}
			}
		} else if len(series.Value) >= 2 {
			// Single value (instant query result)
			fmt.Printf("\nData Points: 1\n\n")
			
			fmt.Println("Timestamp                | Value")
			fmt.Println("-------------------------|----------------")
			
			var ts int64
			switch v := series.Value[0].(type) {
			case float64:
				ts = int64(v)
			case int64:
				ts = v
			}
			
			valueStr, ok := series.Value[1].(string)
			if ok {
				value, err := strconv.ParseFloat(valueStr, 64)
				if err == nil {
					timestamp := time.Unix(ts, 0)
					fmt.Printf("%s | %.6f\n", timestamp.Format("2006-01-02 15:04:05"), value)
				}
			}
		} else {
			fmt.Println("\nNo data points found")
		}
		fmt.Println()
	}

	return nil
}

func handlePut(args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("put requires: <timestamp> <metric> <value> [<metric> <value> ...]")
	}

	// Parse timestamp
	ts, err := parseTimestamp(args[0])
	if err != nil {
		return fmt.Errorf("invalid timestamp: %w", err)
	}

	// Parse metric-value pairs
	metricArgs := args[1:]
	if len(metricArgs)%2 != 0 {
		return fmt.Errorf("metric-value pairs must come in pairs")
	}

	// Send each metric-value pair
	successCount := 0
	for i := 0; i < len(metricArgs); i += 2 {
		metric := metricArgs[i]
		value, err := strconv.ParseFloat(metricArgs[i+1], 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Invalid value '%s' for metric '%s': %v\n", metricArgs[i+1], metric, err)
			continue
		}

		req := WriteRequest{
			Timestamp: ts,
			Metric:    metric,
			Value:     value,
		}

		jsonData, err := json.Marshal(req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to marshal request for '%s': %v\n", metric, err)
			continue
		}

		resp, err := http.Post(serverURL+"/api/v1/write", "application/json", bytes.NewBuffer(jsonData))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to write '%s': %v\n", metric, err)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			fmt.Fprintf(os.Stderr, "Warning: Server error for '%s' (status %d): %s\n", metric, resp.StatusCode, string(body))
			continue
		}

		fmt.Printf("✓ Written: %s = %.6f (ts=%d)\n", metric, value, ts)
		successCount++
	}

	if successCount == 0 {
		return fmt.Errorf("failed to write any metrics")
	}

	fmt.Printf("\nSuccessfully written %d/%d metrics\n", successCount, len(metricArgs)/2)
	return nil
}

// parseTimestamp parses various timestamp formats:
// - "now" -> current timestamp
// - "now-1h", "now-30m", "now-1d" -> relative to now
// - "1706000000" -> Unix timestamp
func parseTimestamp(s string) (int64, error) {
	s = strings.TrimSpace(s)

	// Handle "now"
	if s == "now" {
		return time.Now().Unix(), nil
	}

	// Handle relative time: "now-1h", "now-30m", etc.
	if strings.HasPrefix(s, "now-") || strings.HasPrefix(s, "now+") {
		return parseRelativeTime(s)
	}

	// Parse as Unix timestamp
	ts, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid timestamp format (use 'now', 'now-1h', or Unix timestamp): %w", err)
	}

	return ts, nil
}

// parseRelativeTime parses relative time expressions like "now-1h", "now-30m", "now+2d"
func parseRelativeTime(s string) (int64, error) {
	now := time.Now()

	// Remove "now" prefix
	s = strings.TrimPrefix(s, "now")
	if s == "" {
		return now.Unix(), nil
	}

	// Parse operator and duration
	if len(s) < 2 {
		return 0, fmt.Errorf("invalid relative time format")
	}

	op := s[0] // '+' or '-'
	durationStr := s[1:]

	// Parse duration
	var duration time.Duration
	var err error

	// Custom parsing for simplified format (e.g., "1h", "30m", "2d")
	if strings.HasSuffix(durationStr, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(durationStr, "d"))
		if err != nil {
			return 0, fmt.Errorf("invalid duration: %w", err)
		}
		duration = time.Duration(days) * 24 * time.Hour
	} else {
		duration, err = time.ParseDuration(durationStr)
		if err != nil {
			return 0, fmt.Errorf("invalid duration: %w", err)
		}
	}

	// Apply operator
	var result time.Time
	if op == '-' {
		result = now.Add(-duration)
	} else if op == '+' {
		result = now.Add(duration)
	} else {
		return 0, fmt.Errorf("invalid operator '%c' (use + or -)", op)
	}

	return result.Unix(), nil
}

func handleMetrics(args []string) error {
	// First argument is metric name filter, rest are label filters
	metricFilter := ""
	labelFilters := make(map[string]string)
	
	if len(args) > 0 {
		metricFilter = args[0]
		
		// Parse label filters from remaining arguments (format: key="value" or key=value)
		for _, arg := range args[1:] {
			parts := strings.SplitN(arg, "=", 2)
			if len(parts) != 2 {
				return fmt.Errorf("invalid label filter format '%s' (use key=value or key=\"value\")", arg)
			}
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			// Remove quotes if present
			value = strings.Trim(value, "\"'")
			labelFilters[key] = value
		}
	}

	// Build URL with optional metric name filter
	queryURL := serverURL + "/api/v1/metrics"
	if metricFilter != "" {
		queryURL += "?filter=" + url.QueryEscape(metricFilter)
	}

	resp, err := http.Get(queryURL)
	if err != nil {
		return fmt.Errorf("failed to query server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned error (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
		Error  string   `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if result.Status != "success" {
		return fmt.Errorf("server error: %s", result.Error)
	}

	// Apply label filters if specified
	filteredMetrics := result.Data
	if len(labelFilters) > 0 {
		filteredMetrics = []string{}
		for _, metric := range result.Data {
			if matchesLabelFilters(metric, labelFilters) {
				filteredMetrics = append(filteredMetrics, metric)
			}
		}
	}

	// Print metrics
	if len(filteredMetrics) == 0 {
		fmt.Println("No metrics found")
		fmt.Println("\nTotal: 0 metrics")
		return nil
	}

	fmt.Printf("Found %d metric(s):\n\n", len(filteredMetrics))
	for _, metric := range filteredMetrics {
		fmt.Println(metric)
	}
	
	fmt.Printf("\nTotal: %d metrics\n", len(filteredMetrics))

	return nil
}

// matchesLabelFilters checks if a metric string matches the given label filters
func matchesLabelFilters(metric string, filters map[string]string) bool {
	// Parse labels from metric string (format: metric_name{label1="value1",label2="value2"})
	labelStart := strings.Index(metric, "{")
	if labelStart == -1 {
		// No labels in metric
		return len(filters) == 0
	}
	
	labelEnd := strings.LastIndex(metric, "}")
	if labelEnd == -1 || labelEnd <= labelStart {
		return false
	}
	
	labelStr := metric[labelStart+1 : labelEnd]
	
	// Check if all filters match by searching in the label string
	// This is faster than parsing all labels
	for key, value := range filters {
		// Look for key="value" or key='value' pattern
		pattern1 := key + `="` + value + `"`
		pattern2 := key + `='` + value + `'`
		
		if !strings.Contains(labelStr, pattern1) && !strings.Contains(labelStr, pattern2) {
			return false
		}
	}
	
	return true
}
