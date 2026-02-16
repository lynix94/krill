package web

import (
	"encoding/json"
	"log"
	"net/http"
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
	Addr string // Listen address (e.g., ":9090")
	TSDB krill.QueryableDB
}

// NewServer creates a new HTTP API server
func NewServer(opts ServerOptions) *Server {
	if opts.Addr == "" {
		opts.Addr = ":9090"
	}

	handler := NewPrometheusHandler(opts.TSDB)

	mux := http.NewServeMux()

	// Prometheus-compatible API endpoints
	mux.HandleFunc("/api/v1/query", handler.HandleQuery)
	mux.HandleFunc("/api/v1/query_range", handler.HandleQueryRange)
	mux.HandleFunc("/api/v1/write", handler.HandleWrite)
	mux.HandleFunc("/api/v1/write/batch", handler.HandleBatchWrite)
	mux.HandleFunc("/api/v1/metrics", handler.HandleMetrics)
	mux.HandleFunc("/api/v1/label/__name__/values", handler.HandleMetrics)

	// KrillQL API with JSON support for multiple queries
	mux.HandleFunc("/api/v1/krillql", handler.HandleKrillQL)

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
		Handler:      mux,
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
