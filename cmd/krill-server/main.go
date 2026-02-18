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
		BucketDuration string `yaml:"bucket_duration"`
	} `yaml:"storage"`
	
	Sampling []SamplingLevel `yaml:"sampling"`
	
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

type SamplingLevel struct {
	Name      string        `yaml:"name"`
	Interval  string        `yaml:"interval"`
	Retention string        `yaml:"retention"`
	Storage   StorageConfig `yaml:"storage"`
}

type StorageConfig struct {
	Type          string        `yaml:"type"`
	Badger        *BadgerConfig `yaml:"badger,omitempty"`
	ClickHouse    *ClickHouseConfig `yaml:"clickhouse,omitempty"`
}

type BadgerConfig struct {
	Path       string `yaml:"path"`
	Partitions int    `yaml:"partitions"`
	ChunkSize  int    `yaml:"chunk_size"`
}

type ClickHouseConfig struct {
	Host      string `yaml:"host"`
	Database  string `yaml:"database"`
	Table     string `yaml:"table"`
	User      string `yaml:"user"`
	Password  string `yaml:"password"`
	BatchSize int    `yaml:"batch_size"`
}

func validateConfig(config *Config) error {
	if len(config.Sampling) == 0 {
		return fmt.Errorf("sampling configuration is empty")
	}
	
	// Check level 0 (raw) is mandatory
	if config.Sampling[0].Name != "raw" {
		return fmt.Errorf("level 0 must be 'raw' (mandatory)")
	}
	
	rawInterval, err := parseFlexibleDuration(config.Sampling[0].Interval)
	if err != nil || rawInterval != 0 {
		return fmt.Errorf("level 0 'raw' must have interval: 0s")
	}
	
	// Check for duplicate intervals
	intervals := make(map[string]bool)
	for _, level := range config.Sampling {
		if intervals[level.Interval] {
			return fmt.Errorf("duplicate sampling interval found: %s", level.Interval)
		}
		intervals[level.Interval] = true
		
		// Validate storage type
		if level.Storage.Type != "badger" {
			return fmt.Errorf("unsupported storage type '%s' for level '%s' (only 'badger' is currently supported)", 
				level.Storage.Type, level.Name)
		}
		
		// Validate badger config exists
		if level.Storage.Badger == nil {
			return fmt.Errorf("badger configuration is required for level '%s'", level.Name)
		}
	}
	
	return nil
}

func parseFlexibleDuration(input string) (time.Duration, error) {
	if input == "" {
		return 0, fmt.Errorf("duration is empty")
	}

	if d, err := time.ParseDuration(input); err == nil {
		return d, nil
	}

	// Support suffixes: d (day), w (week), y (year)
	unit := input[len(input)-1]
	value := input[:len(input)-1]

	var multiplier time.Duration
	switch unit {
	case 'd':
		multiplier = 24 * time.Hour
	case 'w':
		multiplier = 7 * 24 * time.Hour
	case 'y':
		multiplier = 365 * 24 * time.Hour
	default:
		return 0, fmt.Errorf("unsupported duration unit: %q", string(unit))
	}

	var n int64
	_, err := fmt.Sscanf(value, "%d", &n)
	if err != nil {
		return 0, fmt.Errorf("invalid duration value: %s", input)
	}

	return time.Duration(n) * multiplier, nil
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
	
	// Validate configuration
	if err := validateConfig(&config); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &config, nil
}

func main() {
	// Command-line flags
	configFile := flag.String("config", "", "Path to config YAML file (default: ./conf.yaml in executable directory)")
	addr := flag.String("addr", ":9090", "HTTP server listen address")
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

	// Get level 0 (raw) configuration
	rawLevel := config.Sampling[0]
	dataDir := rawLevel.Storage.Badger.Path
	partitions := rawLevel.Storage.Badger.Partitions
	chunkSize := rawLevel.Storage.Badger.ChunkSize
	
	// Parse bucket duration from config
	bucketSize, err := parseFlexibleDuration(config.Storage.BucketDuration)
	if err != nil {
		log.Fatalf("Invalid bucket_duration in config: %v", err)
	}
	
	// Parse retention for raw level
	retention, err := parseFlexibleDuration(rawLevel.Retention)
	if err != nil {
		log.Fatalf("Invalid retention in raw level: %v", err)
	}
	
	// Create TSDB instance for level 0 (raw)
	log.Printf("Configuring level 0 'raw': path=%s, partitions=%d, chunk=%d", 
		dataDir, partitions, chunkSize)
	
	bucketSizeSeconds := int64(bucketSize.Seconds())
	cacheDuration := bucketSize // level 0 cache always equals bucket_duration
	
	log.Printf("Using hybrid storage (bucket: %v, retention: %v, data: %s, partitions: %d, chunk: %d, cache: %v)", 
		bucketSize, retention, dataDir, partitions, chunkSize, cacheDuration)
	
	tsdb, err := krill.NewHybridTSDB(krill.HybridOptions{
		PersistencePath: dataDir,
		CacheDuration:   cacheDuration,
		TTL:             retention,
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
	
	// Log downsampling levels
	log.Printf("Configured %d sampling levels:", len(config.Sampling))
	for i, level := range config.Sampling {
		log.Printf("  Level %d: name=%s, interval=%s, retention=%s, type=%s, path=%s",
			i, level.Name, level.Interval, level.Retention, 
			level.Storage.Type, level.Storage.Badger.Path)
	}
	log.Println("Note: Downsampling aggregates: [avg, min, max, count]")
	
	// Create downsampling manager and configure levels
	dsManager := krill.NewDownsamplingManager(tsdb)
	
	// Add downsampling levels (skip level 0 which is raw)
	for i := 1; i < len(config.Sampling); i++ {
		level := config.Sampling[i]
		
		// Parse interval and retention
		interval, err := parseFlexibleDuration(level.Interval)
		if err != nil {
			log.Fatalf("Invalid interval for level '%s': %v", level.Name, err)
		}
		
		retention, err := parseFlexibleDuration(level.Retention)
		if err != nil {
			log.Fatalf("Invalid retention for level '%s': %v", level.Name, err)
		}
		
		// Create storage for this level
		dsStorage, err := krill.CreateDownsamplingStorage(
			level.Storage.Badger.Path,
			level.Storage.Badger.Partitions,
			level.Storage.Badger.ChunkSize,
			bucketSizeSeconds,
			retention,
		)
		if err != nil {
			log.Fatalf("Failed to create storage for level '%s': %v", level.Name, err)
		}
		
		// Add level to manager
		if err := dsManager.AddLevel(level.Name, interval, retention, dsStorage); err != nil {
			log.Fatalf("Failed to add downsampling level '%s': %v", level.Name, err)
		}
	}
	
	// Start downsampling
	dsManager.Start()
	defer dsManager.Stop()

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
