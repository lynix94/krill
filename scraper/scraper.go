package scraper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Scraper manages metric scraping from exporters
type Scraper struct {
	config      *Config
	serverURL   string
	httpClient  *http.Client
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup

	// Statistics
	mu                sync.RWMutex
	totalScrapes      int64
	successfulScrapes int64
	failedScrapes     int64
	metricsCollected  int64
}

// WriteRequest represents the Krill API write request
type WriteRequest struct {
	Metric string            `json:"metric"`
	Value  float64           `json:"value"`
	Time   int64             `json:"time,omitempty"`
	Tags   map[string]string `json:"tags,omitempty"`
}

// NewScraper creates a new scraper instance
func NewScraper(config *Config, serverURL string) (*Scraper, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	if serverURL == "" {
		return nil, fmt.Errorf("server URL is required")
	}

	// Ensure serverURL doesn't end with /
	serverURL = strings.TrimSuffix(serverURL, "/")

	ctx, cancel := context.WithCancel(context.Background())

	return &Scraper{
		config:    config,
		serverURL: serverURL,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		ctx:    ctx,
		cancel: cancel,
	}, nil
}

// Start starts the scraper
func (s *Scraper) Start() {
	log.Println("Starting scraper...")

	for _, scrapeConfig := range s.config.ScrapeConfigs {
		sc := scrapeConfig // capture loop variable
		s.wg.Add(1)
		go s.runScrapeJob(sc)
	}

	log.Printf("Started %d scrape jobs", len(s.config.ScrapeConfigs))
}

// Stop stops the scraper
func (s *Scraper) Stop() {
	log.Println("Stopping scraper...")
	s.cancel()
	s.wg.Wait()
	log.Println("Scraper stopped")
}

// runScrapeJob runs a single scrape job periodically
func (s *Scraper) runScrapeJob(config ScrapeConfig) {
	defer s.wg.Done()

	ticker := time.NewTicker(config.ScrapeInterval)
	defer ticker.Stop()

	log.Printf("Scrape job '%s' started (interval: %v)", config.JobName, config.ScrapeInterval)

	// Do initial scrape immediately
	s.scrapeTargets(config)

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.scrapeTargets(config)
		}
	}
}

// scrapeTargets scrapes all targets in a scrape config
func (s *Scraper) scrapeTargets(config ScrapeConfig) {
	for _, staticConfig := range config.StaticConfigs {
		for _, target := range staticConfig.Targets {
			go s.scrapeTarget(config, staticConfig, target)
		}
	}
}

// scrapeTarget scrapes a single target
func (s *Scraper) scrapeTarget(config ScrapeConfig, staticConfig StaticConfig, target string) {
	s.mu.Lock()
	s.totalScrapes++
	s.mu.Unlock()

	url := fmt.Sprintf("http://%s%s", target, config.MetricsPath)

	ctx, cancel := context.WithTimeout(s.ctx, config.ScrapeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		log.Printf("Error creating request for %s: %v", target, err)
		s.recordFailure()
		return
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		log.Printf("Error scraping %s: %v", target, err)
		s.recordFailure()
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Non-200 status from %s: %d", target, resp.StatusCode)
		s.recordFailure()
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading response from %s: %v", target, err)
		s.recordFailure()
		return
	}

	metrics, err := ParsePrometheusMetrics(strings.NewReader(string(body)))
	if err != nil {
		log.Printf("Error parsing metrics from %s: %v", target, err)
		s.recordFailure()
		return
	}

	// Send metrics to Krill server via HTTP API
	now := time.Now().Unix()
	
	// Build batch of write requests
	var batch []WriteRequest
	for _, metric := range metrics {
		// Merge labels from config and static config
		allLabels := make(map[string]string)
		for k, v := range config.Labels {
			allLabels[k] = v
		}
		for k, v := range staticConfig.Labels {
			allLabels[k] = v
		}
		for k, v := range metric.Labels {
			allLabels[k] = v
		}
		
		// Add job and instance labels
		allLabels["job"] = config.JobName
		allLabels["instance"] = target

		// Use metric name with optional prefix (no label flattening)
		metricName := metric.Name
		if config.MetricPrefix != "" {
			metricName = config.MetricPrefix + "." + metricName
		}

		// Use metric timestamp if available, otherwise use current time
		timestamp := metric.Timestamp
		if timestamp == 0 {
			timestamp = now
		} else if timestamp > 9999999999 { // Convert milliseconds to seconds
			timestamp = timestamp / 1000
		}

		batch = append(batch, WriteRequest{
			Metric: metricName,
			Value:  metric.Value,
			Time:   timestamp,
			Tags:   allLabels,
		})
	}

	// Send batch to Krill server (split into chunks if too large)
	const maxBatchSize = 1000 // Avoid "Txn is too big" error
	totalSent := 0
	
	for i := 0; i < len(batch); i += maxBatchSize {
		end := i + maxBatchSize
		if end > len(batch) {
			end = len(batch)
		}
		chunk := batch[i:end]
		
		if err := s.sendMetricBatch(chunk); err != nil {
			log.Printf("Error sending batch chunk to server: %v", err)
			s.recordFailure()
			return
		}
		totalSent += len(chunk)
	}

	s.recordSuccess(int64(totalSent))
	if len(batch) > maxBatchSize {
		log.Printf("Scraped %s: sent %d metrics to server in %d batches", target, totalSent, (len(batch)+maxBatchSize-1)/maxBatchSize)
	} else {
		log.Printf("Scraped %s: sent %d metrics to server in batch", target, totalSent)
	}
}

func (s *Scraper) recordSuccess(metricsCount int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.successfulScrapes++
	s.metricsCollected += metricsCount
}

func (s *Scraper) recordFailure() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failedScrapes++
}

// sendMetricBatch sends a batch of metrics to the Krill server via HTTP API
func (s *Scraper) sendMetricBatch(batch []WriteRequest) error {
	if len(batch) == 0 {
		return nil
	}

	jsonData, err := json.Marshal(batch)
	if err != nil {
		return fmt.Errorf("failed to marshal batch: %w", err)
	}

	url := s.serverURL + "/api/v1/write/batch"
	httpReq, err := http.NewRequestWithContext(s.ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// sendMetric sends a single metric to the Krill server via HTTP API (deprecated, use batch)
func (s *Scraper) sendMetric(metric string, value float64, timestamp int64, tags map[string]string) error {
	req := WriteRequest{
		Metric: metric,
		Value:  value,
		Time:   timestamp,
		Tags:   tags,
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal metric: %w", err)
	}

	url := s.serverURL + "/api/v1/write"
	httpReq, err := http.NewRequestWithContext(s.ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// GetStats returns scraper statistics
func (s *Scraper) GetStats() ScraperStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return ScraperStats{
		TotalScrapes:      s.totalScrapes,
		SuccessfulScrapes: s.successfulScrapes,
		FailedScrapes:     s.failedScrapes,
		MetricsCollected:  s.metricsCollected,
	}
}

// ScraperStats holds scraper statistics
type ScraperStats struct {
	TotalScrapes      int64
	SuccessfulScrapes int64
	FailedScrapes     int64
	MetricsCollected  int64
}
