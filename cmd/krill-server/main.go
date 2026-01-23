package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lynix/krill"
	"github.com/lynix/krill/web"
)

func main() {
	// Command-line flags
	addr := flag.String("addr", ":9090", "HTTP server listen address")
	dataDir := flag.String("data", "/tmp/krill-data", "Data directory for persistent storage")
	cacheDuration := flag.Duration("cache", 2*time.Hour, "Memory cache duration (e.g., 2h, 30m)")
	memoryOnly := flag.Bool("memory", false, "Use memory-only storage (no persistence)")
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
	log.Println("\nExample curl commands:")
	log.Printf("  curl 'http://localhost%s/api/v1/query?query=cpu.usage'", *addr)
	log.Printf("  curl 'http://localhost%s/api/v1/query_range?query=memory.used&start=%d&end=%d'", *addr, now-3600, now)
	log.Printf("  curl -X POST http://localhost%s/api/v1/write -H 'Content-Type: application/json' -d '{\"metric\":\"test.metric\",\"value\":123.45}'", *addr)
	log.Println()

	if err := server.Start(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
