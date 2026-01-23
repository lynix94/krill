package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lynix/krill/scraper"
)

func main() {
	// Command-line flags
	configFile := flag.String("config", "scraper.yaml", "Path to scraper configuration file")
	serverURL := flag.String("server", "http://localhost:9090", "Krill server URL")
	statsInterval := flag.Duration("stats", 1*time.Minute, "Statistics reporting interval")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("Starting Krill TSDB Scraper...")

	// Load scraper configuration
	log.Printf("Loading configuration from %s", *configFile)
	config, err := scraper.LoadConfig(*configFile)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	log.Printf("Loaded %d scrape jobs:", len(config.ScrapeConfigs))
	for _, sc := range config.ScrapeConfigs {
		targetCount := 0
		for _, static := range sc.StaticConfigs {
			targetCount += len(static.Targets)
		}
		log.Printf("  - %s: %d targets (interval: %v)", sc.JobName, targetCount, sc.ScrapeInterval)
	}

	// Create scraper with server URL
	log.Printf("Sending metrics to Krill server: %s", *serverURL)
	sc, err := scraper.NewScraper(config, *serverURL)
	if err != nil {
		log.Fatalf("Failed to create scraper: %v", err)
	}

	sc.Start()
	defer sc.Stop()

	// Start statistics reporter
	go func() {
		ticker := time.NewTicker(*statsInterval)
		defer ticker.Stop()

		for range ticker.C {
			stats := sc.GetStats()
			successRate := float64(0)
			if stats.TotalScrapes > 0 {
				successRate = float64(stats.SuccessfulScrapes) / float64(stats.TotalScrapes) * 100
			}

			log.Printf("Stats: scrapes=%d (success=%d, failed=%d, rate=%.1f%%), metrics=%d",
				stats.TotalScrapes,
				stats.SuccessfulScrapes,
				stats.FailedScrapes,
				successRate,
				stats.MetricsCollected,
			)
		}
	}()

	log.Println("Scraper is running. Press Ctrl+C to stop.")

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("\nReceived shutdown signal")
}
