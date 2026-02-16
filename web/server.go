package web

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/lynix/krill"
	"github.com/lynix/krill/storage"
)

// Server represents the HTTP API server
type Server struct {
	addr    string
	tsdb    krill.QueryableDB
	handler *PrometheusHandler
	server  *http.Server
}

// ServerOptions configures the web server
type ServerOptions struct {
	Addr       string // Listen address (e.g., ":9090")
	TSDB       krill.QueryableDB
	PrintQuery bool // Print all incoming requests for debugging
}

// NewServer creates a new HTTP API server
func NewServer(opts ServerOptions) *Server {
	if opts.Addr == "" {
		opts.Addr = ":9090"
	}

	handler := NewPrometheusHandler(opts.TSDB)

	mux := http.NewServeMux()

	// Request logging middleware
	var finalHandler http.Handler = mux
	if opts.PrintQuery {
		finalHandler = requestLoggingMiddleware(mux)
	}

	// Prometheus-compatible API endpoints
	mux.HandleFunc("/api/v1/query", handler.HandleQuery)
	mux.HandleFunc("/api/v1/query_range", handler.HandleQueryRange)
	mux.HandleFunc("/api/v1/write", handler.HandleWrite)
	mux.HandleFunc("/api/v1/write/batch", handler.HandleBatchWrite)
	mux.HandleFunc("/api/v1/metrics", handler.HandleMetrics)

	// Grafana-compatible API endpoints (must be before specific paths)
	mux.HandleFunc("/api/v1/labels", handler.HandleLabels)
	mux.HandleFunc("/api/v1/series", handler.HandleSeries)

	// Label values endpoint - handles both __name__ and custom labels
	// This must be registered as a catch-all for /api/v1/label/
	mux.HandleFunc("/api/v1/label/", func(w http.ResponseWriter, r *http.Request) {
		// Check if it's asking for __name__ values (metric names)
		if strings.HasPrefix(r.URL.Path, "/api/v1/label/__name__/values") {
			handler.HandleMetrics(w, r)
		} else {
			handler.HandleLabelValues(w, r)
		}
	})

	// KrillQL API with JSON support for multiple queries
	mux.HandleFunc("/api/v1/krillql", handler.HandleKrillQL)

	// Prometheus buildinfo endpoint (required by Grafana)
	// Mimics Prometheus 2.x response format for compatibility
	mux.HandleFunc("/api/v1/status/buildinfo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Exact Prometheus format - critical for Grafana compatibility
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data": map[string]string{
				"version":   "2.45.0",  // Pretend to be Prometheus 2.45.0
				"revision":  "8ef767e396bf8445f009f945b0162fd71827f445",
				"branch":    "HEAD",
				"buildUser": "root@localhost",
				"buildDate": "20240101-00:00:00",
				"goVersion": "go1.21.5",
			},
		})
	})

	// Prometheus runtime info endpoint (optional, for compatibility)
	mux.HandleFunc("/api/v1/status/runtimeinfo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"startTime":           "2026-02-16T18:00:00Z",
				"CWD":                 "/app",
				"reloadConfigSuccess": true,
				"lastConfigTime":      "2026-02-16T18:00:00Z",
				"storageRetention":    "15d",
			},
		})
	})

	// Prometheus config endpoint (optional, for compatibility)
	mux.HandleFunc("/api/v1/status/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"yaml": "# Krill TSDB Configuration\nglobal:\n  scrape_interval: 15s\n",
			},
		})
	})

	// String pool statistics endpoint
	mux.HandleFunc("/api/v1/stats/string_pool", func(w http.ResponseWriter, r *http.Request) {
		stats := map[string]interface{}{
			"unique_strings": storage.GlobalStringPool.Size(),
			"description":    "Number of unique strings in the global string pool",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
	})

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Root handler with dashboard
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(dashboardHTML))
	})

	srv := &http.Server{
		Addr:         opts.Addr,
		Handler:      finalHandler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second, // Increased for large metric lists
		IdleTimeout:  120 * time.Second,
	}

	return &Server{
		addr:    opts.Addr,
		tsdb:    opts.TSDB,
		handler: handler,
		server:  srv,
	}
}

// Start starts the HTTP server
func (s *Server) Start() error {
	log.Printf("Starting Krill TSDB API server on %s", s.addr)
	log.Printf("Prometheus API endpoints available at http://%s/api/v1/", s.addr)
	return s.server.ListenAndServe()
}

// Stop gracefully stops the HTTP server
func (s *Server) Stop() error {
	log.Println("Shutting down server...")
	return s.server.Close()
}

// Addr returns the server address
func (s *Server) Addr() string {
	return s.addr
}

// TSDB returns the underlying TSDB instance
func (s *Server) TSDB() krill.QueryableDB {
	return s.tsdb
}

// Example usage function for testing
func ExampleUsage() {
	// Create HybridTSDB
	tsdb, err := krill.NewHybridTSDB(krill.HybridOptions{
		PersistencePath: "/tmp/krill-data",
		CacheDuration:   2 * time.Hour,
	})
	if err != nil {
		log.Fatalf("Failed to create TSDB: %v", err)
	}
	defer tsdb.Close()

	// Create and start server
	server := NewServer(ServerOptions{
		Addr: ":9090",
		TSDB: tsdb,
	})

	if err := server.Start(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// LoggingMiddleware logs HTTP requests
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		log.Printf("%s %s %s", r.Method, r.URL.Path, r.RemoteAddr)
		next.ServeHTTP(w, r)
		log.Printf("Completed in %v", time.Since(start))
	})
}

// requestLoggingMiddleware logs all incoming HTTP requests
func requestLoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Log request details
		log.Printf("[REQUEST] %s %s", r.Method, r.URL.String())

		// Log query parameters if present
		if len(r.URL.Query()) > 0 {
			log.Printf("[QUERY PARAMS]")
			for key, values := range r.URL.Query() {
				for _, value := range values {
					log.Printf("  %s = %s", key, value)
				}
			}
		}

		// Log headers (useful for debugging Grafana requests)
		log.Printf("[HEADERS]")
		for key, values := range r.Header {
			for _, value := range values {
				log.Printf("  %s: %s", key, value)
			}
		}

		// Call next handler
		next.ServeHTTP(w, r)
	})
}
