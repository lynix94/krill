package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/lynix/krill"
	"github.com/lynix/krill/scraper"
	"github.com/lynix/krill/web"
	"gopkg.in/yaml.v3"
)

// Config represents the YAML configuration file
type Config struct {
	Storage struct {
		Type           string `yaml:"type"`
		BucketDuration string `yaml:"bucket_duration"`
	} `yaml:"storage"`
	
	Badger struct {
		Path       string `yaml:"path"`
		Partitions int    `yaml:"partitions"`
		ChunkSize  int    `yaml:"chunk_size"`
	} `yaml:"badger"`
	
	Logging struct {
		Level          string `yaml:"level"`
		Format         string `yaml:"format"`
		LogToStderr    bool   `yaml:"logtostderr"`
		LogDir         string `yaml:"log_dir"`
		LogBacktraceAt string `yaml:"log_backtrace_at"`
		LogBufLevel    int    `yaml:"logbuflevel"`
		LogLink        string `yaml:"log_link"`
	} `yaml:"logging"`
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return &config, nil
}

func main() {
	// Command-line flags
	configFile := flag.String("config", "", "Path to config YAML file (default: ./conf.yaml in executable directory)")
	addr := flag.String("addr", ":9090", "HTTP server listen address")
	retention := flag.Duration("retention", 30*24*time.Hour, "Data retention period (e.g., 7d, 15d, 30d). Default: 30d")
	scrapeConfig := flag.String("scrape", "", "Path to scraper config YAML file (enables embedded scraping for 10x+ performance)")
	printQuery := flag.Bool("printQuery", false, "Print all incoming HTTP requests for debugging")
	debugIndex := flag.Bool("debugIndex", false, "Enable debug logging for index operations")
	
	// Custom usage function to hide glog flags
	glogFlags := map[string]bool{
		"log_backtrace_at": true, "log_dir": true, "log_link": true,
		"logbuflevel": true, "logtostderr": true, "alsologtostderr": true,
		"stderrthreshold": true, "vmodule": true, "v": true,
	}
	
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage of %s:\n", os.Args[0])
		flag.VisitAll(func(f *flag.Flag) {
			if !glogFlags[f.Name] {
				fmt.Fprintf(flag.CommandLine.Output(), "  -%s\n", f.Name)
				if f.DefValue != "" {
					fmt.Fprintf(flag.CommandLine.Output(), "        %s (default %s)\n", f.Usage, f.DefValue)
				} else {
					fmt.Fprintf(flag.CommandLine.Output(), "        %s\n", f.Usage)
				}
			}
		})
	}
	
	flag.Parse()

	// Determine config file path
	configPath := *configFile
	if configPath == "" {
		// Look for conf.yaml in the same directory as the executable
		exePath, err := os.Executable()
		if err == nil {
			exeDir := filepath.Dir(exePath)
			configPath = filepath.Join(exeDir, "conf.yaml")
		} else {
			configPath = "conf.yaml"
		}
	}

	// Load configuration
	config, err := loadConfig(configPath)
	if err != nil {
		log.Fatalf("Failed to load config from %s: %v", configPath, err)
	}
	log.Printf("Loaded config from %s", configPath)

	// Validate storage type
	if config.Storage.Type != "badger" {
		log.Fatalf("Unsupported storage type: %s (only 'badger' is currently supported)", config.Storage.Type)
	}

	// Apply logging config
	if config.Logging.LogToStderr {
		flag.Set("logtostderr", "true")
	} else {
		flag.Set("logtostderr", "false")
	}
	
	if config.Logging.LogDir != "" {
		flag.Set("log_dir", config.Logging.LogDir)
	}
	
	if config.Logging.LogBacktraceAt != "" {
		flag.Set("log_backtrace_at", config.Logging.LogBacktraceAt)
	}
	
	if config.Logging.LogBufLevel >= -1 {
		flag.Set("logbuflevel", string(rune(config.Logging.LogBufLevel+'0')))
	}
	
	if config.Logging.LogLink != "" {
		flag.Set("log_link", config.Logging.LogLink)
	}
	
	log.Printf("Applied logging config: level=%s, format=%s, logtostderr=%v", 
		config.Logging.Level, config.Logging.Format, config.Logging.LogToStderr)

	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("Starting Krill TSDB Server...")

	// Create TSDB instance using config values
	dataDir := config.Badger.Path
	partitions := config.Badger.Partitions
	chunkSize := config.Badger.ChunkSize
	
	// Parse bucket duration from config
	bucketSize, err := time.ParseDuration(config.Storage.BucketDuration)
	if err != nil {
		log.Fatalf("Invalid bucket_duration in config: %v", err)
	}
	
	bucketSizeSeconds := int64(bucketSize.Seconds())
	if *retention > 0 {
		log.Printf("Using hybrid storage (bucket: %v, retention: %v, data: %s, partitions: %d, chunk: %d)", 
			bucketSize, *retention, dataDir, partitions, chunkSize)
	} else {
		log.Printf("Using hybrid storage (bucket: %v, no retention, data: %s, partitions: %d, chunk: %d)", 
			bucketSize, dataDir, partitions, chunkSize)
	}
	tsdb, err := krill.NewHybridTSDB(krill.HybridOptions{
		PersistencePath: dataDir,
		CacheDuration:   bucketSize, // Memory cache keeps 1 bucket only
		TTL:             *retention,
		DebugIndex:      *debugIndex,
		ChunkSize:       chunkSize,
		Partitions:      partitions,
		BucketSize:      bucketSizeSeconds,
	})
	if err != nil {
		log.Fatalf("Failed to create HybridTSDB: %v", err)
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
