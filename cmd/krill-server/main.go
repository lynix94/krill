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
	retention := flag.Duration("retention", 30*24*time.Hour, "Data retention period (e.g., 7d, 15d, 30d). Default: 30d")
	memoryOnly := flag.Bool("memory", false, "Use memory-only storage (no persistence)")
	scrapeConfig := flag.String("scrape", "", "Path to scraper config YAML file (enables embedded scraping for 10x+ performance)")
	printQuery := flag.Bool("printQuery", false, "Print all incoming HTTP requests for debugging")
	debugIndex := flag.Bool("debugIndex", false, "Enable debug logging for index operations")
	chunkSize := flag.Int("chunkSize", 10000, "BadgerDB batch chunk size (default: 10000, larger = faster writes but more memory)")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("Starting Krill TSDB Server...")

	// Create TSDB instance
	var tsdb krill.QueryableDB
	var err error

	if *memoryOnly {
		log.Println("Using memory-only storage")
		tsdb = krill.NewTSDB()
	} else {
		if *retention > 0 {
			log.Printf("Using hybrid storage (cache: %v, retention: %v, data: %s)", *cacheDuration, *retention, *dataDir)
		} else {
			log.Printf("Using hybrid storage (cache: %v, no retention, data: %s)", *cacheDuration, *dataDir)
		}
		tsdb, err = krill.NewHybridTSDB(krill.HybridOptions{
			PersistencePath: *dataDir,
			CacheDuration:   *cacheDuration,
			TTL:             *retention,
			DebugIndex:      *debugIndex,
			ChunkSize:       *chunkSize,
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
	if *scrapeConfig != "" {
		log.Printf("Loading scraper config from %s", *scrapeConfig)
		config, err := scraper.LoadConfig(*scrapeConfig)
		if err != nil {
			log.Fatalf("Failed to load scraper config: %v", err)
		}

		embeddedScraper, err = krill.NewEmbeddedScraper(config, tsdb)
		if err != nil {
			log.Fatalf("Failed to create embedded scraper: %v", err)
		}

		embeddedScraper.Start()
		defer embeddedScraper.Stop()
		log.Println("✓ Embedded scraper enabled - Direct TSDB writes (10x+ faster than HTTP)")
	}

	// Create and start web server
	// Create web server
	server := web.NewServer(web.ServerOptions{
		Addr:       *addr,
		TSDB:       tsdb,
		PrintQuery: *printQuery,
		DebugIndex: *debugIndex,
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

	if embeddedScraper != nil {
		log.Println("\n🚀 Performance Mode: EMBEDDED SCRAPER ACTIVE")
		log.Println("  ✓ Zero HTTP/JSON overhead")
		log.Println("  ✓ Direct memory writes to TSDB")
		log.Println("  ✓ 10x+ faster than external scraper")
		log.Println("  ✓ Single process (easier to manage)")
	}

	log.Println("\nAPI Endpoints:")
	log.Printf("  - http://localhost%s/", *addr)
	log.Printf("  - http://localhost%s/api/v1/query?query=cpu.usage", *addr)
	log.Printf("  - http://localhost%s/api/v1/query_range?query=cpu.usage&start=%d&end=%d", *addr, now-3600, now)
	log.Printf("  - http://localhost%s/api/v1/metrics", *addr)
	log.Printf("  - http://localhost%s/health", *addr)
	log.Println("\nExample curl commands:")
	log.Printf("  curl 'http://localhost%s/api/v1/query?query=cpu.usage'", *addr)
	log.Printf("  curl 'http://localhost%s/api/v1/query_range?query=memory.used&start=%d&end=%d'", *addr, now-3600, now)
	if embeddedScraper == nil {
		log.Printf("  curl -X POST http://localhost%s/api/v1/write -H 'Content-Type: application/json' -d '{\"metric\":\"test.metric\",\"value\":123.45}'", *addr)
		log.Printf("  curl -X POST http://localhost%s/api/v1/write/batch -H 'Content-Type: application/json' -d '[{\"metric\":\"m1\",\"value\":1},{\"metric\":\"m2\",\"value\":2}]'", *addr)
	}
	log.Println()

	if err := server.Start(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
