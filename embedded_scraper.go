package krill

import (
	"context"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lynix/krill/scraper"
	"github.com/lynix/krill/storage"
)

// EmbeddedScraper manages metric scraping directly within krill-server
// This eliminates HTTP/JSON overhead by writing directly to TSDB
type EmbeddedScraper struct {
	config     *scraper.Config
	tsdb       TimeSeriesDB
	httpClient *http.Client
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup

	// Statistics
	mu                sync.RWMutex
	totalScrapes      int64
	successfulScrapes int64
	failedScrapes     int64
	metricsCollected  int64
}

// NewEmbeddedScraper creates a new embedded scraper
func NewEmbeddedScraper(config *scraper.Config, tsdb TimeSeriesDB) (*EmbeddedScraper, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &EmbeddedScraper{
		config: config,
		tsdb:   tsdb,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		ctx:    ctx,
		cancel: cancel,
	}, nil
}

// Start starts the embedded scraper
func (es *EmbeddedScraper) Start() {
	log.Println("Starting embedded scraper...")

	for _, scrapeConfig := range es.config.ScrapeConfigs {
		sc := scrapeConfig
		es.wg.Add(1)
		go es.runScrapeJob(sc)
	}

	log.Printf("Started %d embedded scrape jobs", len(es.config.ScrapeConfigs))
}

// Stop stops the embedded scraper
func (es *EmbeddedScraper) Stop() {
	log.Println("Stopping embedded scraper...")
	es.cancel()
	es.wg.Wait()
	log.Println("Embedded scraper stopped")
}

func (es *EmbeddedScraper) runScrapeJob(config scraper.ScrapeConfig) {
	defer es.wg.Done()

	ticker := time.NewTicker(config.ScrapeInterval)
	defer ticker.Stop()

	log.Printf("Scrape job '%s' started (interval: %v)", config.JobName, config.ScrapeInterval)

	// Scrape immediately on start
	for _, staticConfig := range config.StaticConfigs {
		for _, target := range staticConfig.Targets {
			go es.scrapeTarget(config, staticConfig, target)
		}
	}

	for {
		select {
		case <-es.ctx.Done():
			return
		case <-ticker.C:
			for _, staticConfig := range config.StaticConfigs {
				for _, target := range staticConfig.Targets {
					go es.scrapeTarget(config, staticConfig, target)
				}
			}
		}
	}
}

func (es *EmbeddedScraper) scrapeTarget(config scraper.ScrapeConfig, staticConfig scraper.StaticConfig, target string) {
	// Build target URL
	targetURL := target
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		targetURL = "http://" + target
	}
	if config.MetricsPath != "" {
		targetURL = strings.TrimSuffix(targetURL, "/") + "/" + strings.TrimPrefix(config.MetricsPath, "/")
	}

	// Scrape metrics
	req, err := http.NewRequestWithContext(es.ctx, "GET", targetURL, nil)
	if err != nil {
		log.Printf("Error creating request for %s: %v", target, err)
		es.recordFailure()
		return
	}

	resp, err := es.httpClient.Do(req)
	if err != nil {
		log.Printf("Error scraping %s: %v", target, err)
		es.recordFailure()
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Non-200 status from %s: %d", target, resp.StatusCode)
		es.recordFailure()
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading response from %s: %v", target, err)
		es.recordFailure()
		return
	}

	metrics, err := scraper.ParsePrometheusMetrics(strings.NewReader(string(body)))
	if err != nil {
		log.Printf("Error parsing metrics from %s: %v", target, err)
		es.recordFailure()
		return
	}

	// Direct write to TSDB (NO HTTP/JSON overhead!)
	now := time.Now().Unix()

	// Build batch of DataPoints for direct TSDB write
	points := make([]storage.DataPoint, 0, len(metrics))

	for _, metric := range metrics {
		// Build labels directly
		labels := make(storage.Labels, 0, len(metric.Labels)+len(config.Labels)+len(staticConfig.Labels)+2)

		// Metric name
		metricName := metric.Name
		if config.MetricPrefix != "" {
			metricName = config.MetricPrefix + "." + metricName
		}
		labels = append(labels, storage.Label{Name: "__name__", Value: metricName})

		// Add all labels
		for k, v := range config.Labels {
			labels = append(labels, storage.Label{Name: k, Value: v})
		}
		for k, v := range staticConfig.Labels {
			labels = append(labels, storage.Label{Name: k, Value: v})
		}
		for k, v := range metric.Labels {
			labels = append(labels, storage.Label{Name: k, Value: v})
		}
		// Add job and instance
		labels = append(labels, storage.Label{Name: "job", Value: config.JobName})
		labels = append(labels, storage.Label{Name: "instance", Value: target})

		// IMPORTANT: Sort labels for consistent hashing and key generation
		sort.Sort(labels)

		// Use metric timestamp if available
		timestamp := metric.Timestamp
		if timestamp == 0 {
			timestamp = now
		} else if timestamp > 9999999999 {
			timestamp = timestamp / 1000
		}

		points = append(points, storage.DataPoint{
			Timestamp: timestamp,
			Labels:    labels,
			Value:     metric.Value,
		})
	}

	// Direct batch write - bypasses ALL network overhead!
	if err := es.tsdb.TsdbPutBatch(points); err != nil {
		log.Printf("Error writing batch to TSDB: %v", err)
		es.recordFailure()
		return
	}

	es.recordSuccess(int64(len(points)))
	log.Printf("Scraped %s: wrote %d metrics directly to TSDB (embedded)", target, len(points))
}

func (es *EmbeddedScraper) recordSuccess(metricsCount int64) {
	es.mu.Lock()
	defer es.mu.Unlock()
	es.successfulScrapes++
	es.metricsCollected += metricsCount
}

func (es *EmbeddedScraper) recordFailure() {
	es.mu.Lock()
	defer es.mu.Unlock()
	es.failedScrapes++
}

// GetStats returns scraper statistics
func (es *EmbeddedScraper) GetStats() EmbeddedScraperStats {
	es.mu.RLock()
	defer es.mu.RUnlock()

	return EmbeddedScraperStats{
		TotalScrapes:      es.totalScrapes,
		SuccessfulScrapes: es.successfulScrapes,
		FailedScrapes:     es.failedScrapes,
		MetricsCollected:  es.metricsCollected,
	}
}

// EmbeddedScraperStats holds scraper statistics
type EmbeddedScraperStats struct {
	TotalScrapes      int64
	SuccessfulScrapes int64
	FailedScrapes     int64
	MetricsCollected  int64
}
