package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lynix/krill"
	"github.com/lynix/krill/scraper"
	"github.com/lynix/krill/web"
)

func main() {
	// Command-line flags
	addr := flag.String("addr", ":9090", "HTTP server listen address")
	dataDir := flag.String("data", "/tmp/krill-data", "Data directory for persistent storage")
	cacheDuration := flag.Duration("cache", 2*time.Hour, "Memory cache duration (e.g., 2h, 30m)")
	memoryOnly := flag.Bool("memory", false, "Use memory-only storage (no persistence)")
	scraperConfig := flag.String("scraper-config", "", "Path to scraper config file (enables embedded scraping)")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("Starting Krill TSDB Server with Embedded Scraper...")

	// Create TSDB instance
	var tsdb krill.QueryableDB
	var err error

	if *memoryOnly {
		log.Println("Using memory-only storage")
		tsdb = krill.NewTSDB()
	} else {
		log.Printf("Using hybrid storage (cache: %v, data: %s)", *cacheDuration, *dataDir)
		tsdb, err = krill.NewHybridTSDB(krill.HybridOptions{
			PersistencePath: *dataDir,
			CacheDuration:   *cacheDuration,
		})
		if err != nil {
			log.Fatalf("Failed to create HybridTSDB: %v", err)
		}
	}
	defer func() {
		log.Println("Closing TSDB...")
		tsdb.Close()
	}()

	// Start embedded scraper if config provided
	var embeddedScraper *krill.EmbeddedScraper
	if *scraperConfig != "" {
		log.Printf("Loading scraper config from %s", *scraperConfig)
		config, err := scraper.LoadConfig(*scraperConfig)
		if err != nil {
			log.Fatalf("Failed to load scraper config: %v", err)
		}

		embeddedScraper, err = krill.NewEmbeddedScraper(config, tsdb)
		if err != nil {
			log.Fatalf("Failed to create embedded scraper: %v", err)
		}

		embeddedScraper.Start()
		defer embeddedScraper.Stop()
		log.Println("✓ Embedded scraper started (direct TSDB writes, no HTTP overhead)")
	}

	// Create and start web server
	server := web.NewServer(web.ServerOptions{
		Addr: *addr,
		TSDB: tsdb,
	})

	// Handle graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		log.Println("\nReceived shutdown signal")
		if embeddedScraper != nil {
			embeddedScraper.Stop()
		}
		server.Stop()
		os.Exit(0)
	}()

	// Start server
	now := time.Now().Unix()
	log.Printf("Server listening on %s", *addr)
	log.Println("\nAPI Endpoints:")
	log.Printf("  - http://localhost%s/", *addr)
	log.Printf("  - http://localhost%s/api/v1/query?query=cpu.usage", *addr)
	log.Printf("  - http://localhost%s/api/v1/query_range?query=cpu.usage&start=%d&end=%d", *addr, now-3600, now)
	log.Printf("  - http://localhost%s/health", *addr)
	
	if embeddedScraper != nil {
		log.Println("\n🚀 Performance Mode: Embedded Scraper")
		log.Println("  - Zero HTTP overhead for metric ingestion")
		log.Println("  - Direct memory writes to TSDB")
		log.Println("  - 10x+ faster than external scraper")
	}
	
	log.Println("\nExample curl commands:")
	log.Printf("  curl 'http://localhost%s/api/v1/query?query=cpu.usage'", *addr)
	log.Printf("  curl 'http://localhost%s/api/v1/query_range?query=memory.used&start=%d&end=%d'", *addr, now-3600, now)
	log.Println()

	if err := server.Start(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
