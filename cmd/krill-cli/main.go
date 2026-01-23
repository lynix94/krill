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
			Values [][]interface{}   `json:"values"` // [[timestamp, value], ...]
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

	// Configure readline
	rl, err := readline.NewEx(&readline.Config{
		Prompt:          "krill> ",
		HistoryFile:     historyFile,
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	})
	if err != nil {
		return fmt.Errorf("failed to initialize readline: %w", err)
	}
	defer rl.Close()

	fmt.Println("Krill CLI - Interactive Mode")
	fmt.Printf("Connected to: %s\n", serverURL)
	fmt.Println("Type 'help' for usage, 'exit' to quit")
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

func handleQueryRange(args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("query_range requires: <metric> <start_ts> <end_ts>")
	}

	metric := args[0]
	startTs, err := parseTimestamp(args[1])
	if err != nil {
		return fmt.Errorf("invalid start timestamp: %w", err)
	}
	endTs, err := parseTimestamp(args[2])
	if err != nil {
		return fmt.Errorf("invalid end timestamp: %w", err)
	}

	// Build GET request URL with query parameters
	queryURL := fmt.Sprintf("%s/api/v1/query_range?query=%s&start=%d&end=%d",
		serverURL, url.QueryEscape(metric), startTs, endTs)

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

	// Print results for each series
	for _, series := range result.Data.Result {
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
		fmt.Printf("\nData Points: %d\n\n", len(series.Values))

		if len(series.Values) == 0 {
			fmt.Println("No data points found")
			continue
		}

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
	// Optional filter argument
	filter := ""
	if len(args) > 0 {
		filter = args[0]
	}

	// Build URL with optional filter
	queryURL := serverURL + "/api/v1/metrics"
	if filter != "" {
		queryURL += "?filter=" + url.QueryEscape(filter)
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

	// Print metrics
	if len(result.Data) == 0 {
		fmt.Println("No metrics found")
		fmt.Println("\nTotal: 0 metrics")
		return nil
	}

	fmt.Printf("Found %d metric(s):\n\n", len(result.Data))
	for _, metric := range result.Data {
		fmt.Println(metric)
	}
	
	fmt.Printf("\nTotal: %d metrics\n", len(result.Data))

	return nil
}
