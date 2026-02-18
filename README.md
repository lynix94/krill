# Krill - High-Performance Time Series Database

A high-performance time series database with Gorilla compression, hybrid storage architecture, and Prometheus-compatible API.

## Features

- **Gorilla Compression**: Efficient timestamp and value compression using Facebook's Gorilla algorithm
- **Hybrid Storage**: In-memory cache with BadgerDB persistence for optimal performance
- **Embedded Scraper**: Built-in Prometheus-compatible scraper (10x+ faster than HTTP)
- **Multi-Dimensional Metrics**: Prometheus-style labels/tags support (e.g., `cpu{host="server1",env="prod"}`)
- **PromQL Aggregations**: Full support for sum, avg, min, max, count, stddev, topk, quantile, and more
- **Inverted Index**: Fast label-based queries with posting lists
- **String Interning**: Memory-efficient label storage
- **Auto Downsampling**: Configurable multi-level data retention with automatic aggregation
- **Interactive Dashboard**: Web UI with multi-series charts, step control, and real-time metrics
- **High Performance**: 137k+ writes/sec, millions of reads/sec
- **Thread-Safe**: Concurrent-safe implementation
- **Production Ready**: Battle-tested with comprehensive profiling and optimization

## Performance Highlights

- **Compression**: 90%+ compression ratio with Gorilla algorithm
- **Write Speed**: 137k+ points/sec (embedded scraper), 33k+ via HTTP API
- **Query Speed**: 3M+ reads/sec (memory), 100k+ reads/sec (disk)
- **Embedded Scraper**: Zero HTTP/JSON overhead, direct memory writes
- **Efficient Storage**: Delta-of-delta timestamps (1-2 bits/value), XOR value encoding (2-4 bits/value)

## Gorilla Compression

### Timestamp Compression (Delta-of-Delta)
Encodes the delta-of-delta of timestamps:
- Same delta: 1 bit
- Small changes: 2-4 bits + data
- Highly efficient for regular interval data

### Value Compression (XOR Encoding)
Stores XOR with previous value:
- Same value: 1 bit
- Similar values: 2 bits + compressed XOR data
- Optimized for Float64 values

## Quick Start

### Installation

```bash
go get github.com/lynix/krill
```

### Running the Server

```bash
# Build and run with embedded scraper
cd cmd/krill-server
go build
./krill-server --scrape=scraper.yaml

# Memory-only mode
./krill-server -memory

# Custom configuration
./krill-server -addr :8080 -data /var/lib/krill -cache 1h
```

### Web Dashboard

Open your browser and navigate to: **http://localhost:9090/**

Features:
- ✅ **Real-time Query**: Instant and range queries with autocomplete
- ✅ **Multi-Series Charts**: All time series displayed on line graphs with different colors
- ✅ **Step Control**: Manual or auto step calculation for query resolution
  - Auto mode: Automatically calculates appropriate step based on time range
  - Manual mode: Set custom step (1 = raw data, no resampling)
- ✅ **Time Range Selection**: Quick presets (Last 1h, 24h, etc.) or custom datetime picker
- ✅ **Performance Metrics**: Server processing time, network latency, UI rendering time
- ✅ **Write Interface**: Write data with tags/labels support
- ✅ **Live Statistics**: Total metrics, query/write counters

## API Usage

### Basic Example

```go
package main

import (
    "fmt"
    "github.com/lynix/krill"
)

func main() {
    // Create TSDB instance
    db := krill.NewTSDB()
    
    // Write data
    db.TsdbPut(1000, "cpu.usage", 45.5)
    db.TsdbPut(2000, "cpu.usage", 48.2)
    db.TsdbPut(3000, "cpu.usage", 52.1)
    
    // Query data
    timestamps, values, err := db.Get("cpu.usage", 0, 0)
    if err != nil {
        panic(err)
    }
    
    for i := 0; i < len(timestamps); i++ {
        fmt.Printf("ts=%d, value=%.2f\n", timestamps[i], values[i])
    }
}
```

### API Reference

#### Memory TSDB

**`NewMemoryTSDB() *TSDB`**
Creates a memory-based TSDB instance.

**`TsdbPut(ts int64, metric string, value float64) error`**
Stores a time series data point.

**Parameters:**
- `ts`: Unix timestamp (int64)
- `metric`: Metric name (string)
- `value`: Value (float64)

**`Get(metric string, startTs, endTs int64) ([]int64, []float64, error)`**
Queries data points for a metric. Set startTs and endTs to 0 to retrieve all data.

**`GetAllSeries() ([]storage.Labels, error)`**
Returns all stored metric series with their labels.

**`Close() error`**
Closes the database. (no-op for memory TSDB)

#### Persistent TSDB (BadgerDB)

**`NewBadgerTSDB(path string, bucketSize int64, retentionPeriod time.Duration) (*BadgerTSDB, error)`**
Creates a persistent storage TSDB with TTL support.

**`RunGC() error`**
Runs garbage collection to reclaim disk space.

All other methods are identical to Memory TSDB.

## Performance

### Memory TSDB
For typical time series data (regular intervals, similar values):
- **Compression Ratio**: 2x - 10x (average 18x)
- **Timestamp Compression**: Average 1-2 bits per value
- **Value Compression**: Average 2-4 bits per value (vs 64 bits original)

### Hybrid TSDB (Memory + BadgerDB)
- **Write Performance**: 137,000+ inserts/sec (embedded scraper)
- **Read Performance**: 3,300,000+ reads/sec (memory cache hit)
- **Compression**: Gorilla + BadgerDB LSM tree dual compression
- **Disk I/O**: Optimized with batch writes and async flushing

## Storage Architecture

### Hybrid Storage (Recommended)
- ✅ In-memory cache for recent data (configurable duration)
- ✅ BadgerDB persistence for historical data
- ✅ Automatic cache eviction and disk flush
- ✅ Fast queries for recent data, efficient long-term storage
- **Use Case**: Production metrics, long-term monitoring

### Memory-Only Mode
- ✅ Ultra-fast read/write
- ✅ Zero dependencies
- ❌ Data loss on restart
- **Use Case**: Real-time monitoring, temporary metrics

### Downsampling Levels
Krill supports automatic multi-level data retention:
- **Level 0 (raw)**: Full resolution data (2 days retention)
- **Level 1 (1m)**: 1-minute aggregated data (30 days retention)
- **Level 2 (1h)**: 1-hour aggregated data (3 years retention)

Aggregations: avg, min, max, count

## Testing

```bash
# Run all tests
go test -v

# Memory TSDB tests only
go test -v -run TestMemoryTSDB

# BadgerDB TSDB tests only
go test -v -run TestBadgerTSDB

# Hybrid TSDB tests
go test -v -run TestHybridTSDB
```

## Project Structure

```
krill/
├── tsdb.go                     - Memory TSDB structure and API
├── hybrid_tsdb.go             - Hybrid storage (memory + BadgerDB)
├── embedded_scraper.go        - Built-in Prometheus scraper
├── interface.go                - Common interface definitions
├── tsdb_test.go               - TSDB tests
├── storage/
│   ├── gorilla/               - Gorilla compression algorithm
│   │   ├── bitstream.go       - Bit-level read/write operations
│   │   ├── timestamp.go       - Timestamp compression/decompression
│   │   └── value.go           - Value compression/decompression (XOR)
│   ├── badger/                - BadgerDB persistent storage
│   │   ├── badger.go          - BadgerDB TSDB implementation
│   │   ├── partitioned.go     - Partitioned BadgerDB for downsampling
│   │   └── badger_test.go     - BadgerDB tests
│   ├── memory/                - In-memory storage
│   │   ├── memory.go          - Memory storage implementation
│   │   └── memory_test.go     - Memory storage tests
│   ├── labels.go              - Label/tag management
│   ├── string_pool.go         - String interning for memory efficiency
│   └── persistence/           - Persistence layer
│       └── persistence.go     - Data serialization
├── web/
│   ├── server.go              - HTTP server
│   ├── prometheus.go          - Prometheus API handlers
│   ├── aggregation.go         - PromQL aggregation functions
│   ├── dashboard.go           - Interactive web UI
│   └── stats.go               - Statistics tracking
├── cmd/
│   ├── krill-server/          - TSDB server with embedded scraper
│   ├── krill-cli/             - CLI tool
│   └── krill-scraper/         - Standalone Prometheus scraper
├── scraper/                   - Scraper implementation
│   ├── scraper.go             - Core scraper logic
│   ├── config.go              - Configuration parsing
│   └── parser.go              - Prometheus text format parser
└── docs/
    ├── HYBRID_ARCHITECTURE.md - Hybrid storage architecture details
    ├── LABELS_ARCHITECTURE.md - Label indexing and querying
    ├── STRING_INTERNING.md    - String interning optimization
    ├── PROMQL_AGGREGATIONS.md - PromQL aggregation functions guide
    ├── PROMQL_QUICK_REFERENCE.md - Quick reference for PromQL
    └── KRILL_CLI_GUIDE.md     - CLI usage guide
```

## Technical Details

### Gorilla Compression Algorithm

#### Timestamp Compression (Delta-of-Delta)
```
First timestamp: Stored as-is
First delta: 14 bits
Subsequent deltas:
  - Same as previous: 1 bit
  - ±63: 2 bits + 7 bits data
  - ±255: 3 bits + 9 bits data
  - ±2047: 4 bits + 12 bits data
  - Other: 4 bits + 32 bits data
```

#### Value Compression (XOR)
```
First value: Stored as-is (64 bits)
Subsequent values:
  - Same as previous: 1 bit
  - Different:
    - Control bit: 1 bit
    - Leading zeros: 5 bits
    - Significant bits length: 6 bits
    - Significant bits: variable
```

### BadgerDB Integration

- **Time Partitioning**: 시간별 버킷 (3600초)
- **Key Format**: `metric:bucket_timestamp`
- **Value Format**: Serialized SeriesBlock
- **Iteration**: Prefix scan으로 메트릭별 조회
- **TTL**: BadgerDB native TTL 사용

## HTTP API

Krill provides a Prometheus-compatible REST API:

### Server Options

```bash
./krill-server [options]

Options:
  -addr string
        HTTP server address (default ":9090")
  -data string
        Persistent storage path (default "./data")
  -cache duration
        Memory cache duration (default 1h)
  -memory
        Memory-only mode (no persistence)
  -scrape string
        Scraper config file (enables embedded scraper)
```

### 1. Instant Query

Query the most recent value of a metric:

```bash
curl 'http://localhost:9090/api/v1/query?query=cpu.usage'
```

**Response:**
```json
{
  "status": "success",
  "data": {
    "resultType": "vector",
    "result": [{
      "metric": {"__name__": "cpu.usage"},
      "value": [1769154455, "51.200000"]
    }]
  }
}
```

### 2. Range Query

Query time series data over a time range:

```bash
curl "http://localhost:9090/api/v1/query_range?query=memory.used&start=1769150810&end=1769154410&step=30"
```

**Parameters:**
- `query`: Metric name or PromQL expression
- `start`: Start timestamp (Unix seconds)
- `end`: End timestamp (Unix seconds)
- `step`: Query resolution in seconds (1 = raw data, >1 = downsampled)

**Response:**
```json
{
  "status": "success",
  "data": {
    "resultType": "matrix",
    "result": [{
      "metric": {"__name__": "memory.used"},
      "values": [
        [1769152655, "8456.000000"],
        [1769153555, "8234.000000"],
        [1769154455, "8512.000000"]
      ]
    }]
  }
}
```

**Step Parameter:**
- `step=1`: Returns raw data points without resampling
- `step=30`: Returns data resampled at 30-second intervals
- Omit `step`: Returns all raw data points (same as `step=1`)

### 3. Write Data

Write a single data point:

```bash
# Simple write
curl -X POST http://localhost:9090/api/v1/write \
  -H 'Content-Type: application/json' \
  -d '{"metric":"test.metric","value":123.45,"time":1234567890}'

# With tags/labels (multi-dimensional metrics)
curl -X POST http://localhost:9090/api/v1/write \
  -H 'Content-Type: application/json' \
  -d '{
    "metric": "cpu_usage",
    "value": 75.5,
    "tags": {
      "host": "server1",
      "env": "prod",
      "cpu": "0"
    }
  }'
```

### 4. Query with Labels

Query metrics with label filters:

```bash
# All instances of a metric
curl 'http://localhost:9090/api/v1/query?query=cpu_usage'

# Filter by label (Prometheus syntax)
curl 'http://localhost:9090/api/v1/query?query=cpu_usage{env="prod"}'

# Multiple label filters
curl 'http://localhost:9090/api/v1/query?query=cpu_usage{env="prod",host="server1"}'
```

### 5. PromQL Aggregations

Krill supports full Prometheus aggregation operators:

```bash
# Sum all CPU usage
curl 'http://localhost:9090/api/v1/query?query=sum(cpu_usage)'

# Average by environment
curl 'http://localhost:9090/api/v1/query?query=avg(cpu_usage)%20by%20(env)'

# Top 5 highest values
curl 'http://localhost:9090/api/v1/query?query=topk(5,%20cpu_usage)'

# 95th percentile
curl 'http://localhost:9090/api/v1/query?query=quantile(0.95,%20response_time)'

# Count distinct values
curl 'http://localhost:9090/api/v1/query?query=count(up)'
```

**Supported Aggregation Functions:**
- **Basic**: `sum`, `avg`, `min`, `max`, `count`
- **Statistical**: `stddev`, `stdvar`
- **Ranking**: `topk`, `bottomk`
- **Distribution**: `quantile`, `count_values`
- **Grouping**: `by (label1, label2, ...)`

See [docs/PROMQL_AGGREGATIONS.md](docs/PROMQL_AGGREGATIONS.md) for complete documentation.

### 6. List Metrics

Get all metric names:

```bash
curl 'http://localhost:9090/api/v1/label/__name__/values'
```

**Response:**
```json
{
  "status": "success",
  "data": ["cpu.usage", "memory.used", "node.node_cpu_seconds_total"]
}
```

### 7. Health Check

```bash
curl http://localhost:9090/health
# Response: OK
```

## Embedded Scraper (Recommended)

Krill includes a high-performance embedded scraper that writes directly to the TSDB, bypassing HTTP/JSON overhead.

### Performance Benefits

**Embedded Scraper** (Direct Memory Writes):
- 📈 **137,000+ points/sec**
- ⚡ Zero HTTP/JSON serialization overhead
- ⚡ Direct memory writes to TSDB
- ⚡ Single process (easier to manage)

**Traditional HTTP Scraper**:
- 📉 ~10,000 points/sec
- 🐌 HTTP round-trip + JSON encoding/decoding
- 🐌 Network latency
- 🐌 Separate processes

**Result: 10x+ performance improvement!**

### Configuration

Create a `scraper.yaml` file:

```yaml
global:
  scrape_interval: 15s  # Default scrape interval
  scrape_timeout: 10s   # Timeout

scrape_configs:
  - job_name: 'node-exporter'
    scrape_interval: 30s
    metrics_path: '/metrics'
    metric_prefix: 'node'  # Prefix added to metric names
    labels:
      cluster: 'prod'      # Labels added to all metrics
    static_configs:
      - targets:
          - 'localhost:9100'
        labels:
          environment: 'production'
          
  - job_name: 'prometheus'
    scrape_interval: 15s
    metrics_path: '/metrics'
    metric_prefix: 'prometheus'
    static_configs:
      - targets:
          - 'localhost:9090'
```

### Running with Embedded Scraper

```bash
# Start server with embedded scraper
./krill-server --scrape=scraper.yaml

# You'll see output like:
# ✓ Embedded scraper enabled - Direct TSDB writes (10x+ faster than HTTP)
# Scraped localhost:9100: wrote 984 metrics directly to TSDB (embedded)
# [ASYNC-WRITE] Completed batch #1: 984 points in 7.14ms (137727 pts/sec)
```

### Features

- ✅ **Direct TSDB writes**: No HTTP/JSON overhead
- ✅ **Prometheus compatible**: Standard Prometheus metrics format
- ✅ **Multi-dimensional labels**: Full tag/label support
- ✅ **Automatic scheduling**: Configurable scrape intervals per job
- ✅ **Parallel scraping**: Multiple targets scraped concurrently
- ✅ **Label management**: Automatic job, instance, and custom labels
- ✅ **Error handling**: Continues scraping on individual target failures
- ✅ **Statistics**: Real-time scrape success rate and metrics count

### Supported Exporters

- Node Exporter (system metrics)
- Prometheus (self-monitoring)
- Custom application exporters
- Any Prometheus-compatible exporter

## Standalone Scraper (Optional)

For distributed setups, you can run a standalone scraper that sends data via HTTP:

```bash
# Build scraper
cd cmd/krill-scraper
go build

# Run scraper pointing to remote Krill server
./krill-scraper -config scraper.yaml -server http://krill.example.com:9090
```

## Technical Details

### Gorilla Compression Algorithm

#### Timestamp Compression (Delta-of-Delta)
```
First timestamp: Stored as-is (64 bits)
First delta: 14 bits
Subsequent deltas:
  - Same as previous: 1 bit
  - ±63: 2 bits + 7 bits data
  - ±255: 3 bits + 9 bits data
  - ±2047: 4 bits + 12 bits data
  - Other: 4 bits + 32 bits data
```

#### Value Compression (XOR)
```
First value: Stored as-is (64 bits)
Subsequent values:
  - Same as previous: 1 bit
  - Different:
    - Control bit: 1 bit
    - Leading zeros: 5 bits
    - Significant bits length: 6 bits
    - Significant bits: variable
```

### Hybrid Storage Architecture

**Memory Cache (Hot Data)**:
- Recent data (default: 1 hour)
- Gorilla compression
- Ultra-fast queries (3M+ reads/sec)
- Automatic eviction to disk

**BadgerDB (Cold Data)**:
- Historical data (configurable retention)
- LSM-tree storage with compression
- Time-based partitioning (3600s buckets)
- Efficient range queries

**Inverted Index**:
- Label-based posting lists
- Fast label matching
- Memory-efficient with string interning
- Persistent across restarts

See [docs/HYBRID_ARCHITECTURE.md](docs/HYBRID_ARCHITECTURE.md) and [docs/LABELS_ARCHITECTURE.md](docs/LABELS_ARCHITECTURE.md) for more details.

## License

MIT License

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## Documentation

- [Hybrid Architecture](docs/HYBRID_ARCHITECTURE.md) - Detailed architecture of hybrid storage
- [Labels Architecture](docs/LABELS_ARCHITECTURE.md) - Label indexing and query optimization
- [String Interning](docs/STRING_INTERNING.md) - Memory optimization techniques
- [PromQL Aggregations](docs/PROMQL_AGGREGATIONS.md) - Complete PromQL aggregation guide
- [PromQL Quick Reference](docs/PROMQL_QUICK_REFERENCE.md) - Quick PromQL syntax reference
- [CLI Guide](docs/KRILL_CLI_GUIDE.md) - Command-line interface documentation

