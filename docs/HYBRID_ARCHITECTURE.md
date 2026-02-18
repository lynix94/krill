# Hybrid Storage Architecture

Krill uses a hybrid storage architecture that combines in-memory caching with persistent disk storage for optimal performance and data durability.

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                      HybridTSDB                             │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌─────────────────────┐      ┌──────────────────────────┐│
│  │  MemoryStorage      │      │  BadgerDB (Partitioned)  ││
│  │  (Hot Data)         │◄────►│  (Cold Data)             ││
│  │                     │      │                          ││
│  │  • Gorilla Codec   │      │  • LSM Tree Storage      ││
│  │  • Recent Data      │      │  • 8 Partitions          ││
│  │  • Ultra-fast       │      │  • Time-based Buckets    ││
│  │  • Auto-eviction    │      │  • Persistent            ││
│  └─────────────────────┘      └──────────────────────────┘│
│                                                             │
│  ┌──────────────────────────────────────────────────────┐ │
│  │          Inverted Index (Labels)                      │ │
│  │  • Posting Lists per Label                           │ │
│  │  • String Interning                                  │ │
│  │  • Fast Label Queries                                │ │
│  └──────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
```

## Components

### 1. MemoryStorage (Hot Data Cache)

**Purpose**: Store recent data in memory for ultra-fast queries.

**Features**:
- Gorilla compression for efficient memory usage
- Configurable cache duration (default: 1 hour)
- Automatic eviction to disk when data ages
- Thread-safe concurrent access

**Performance**:
- **Read**: 3,000,000+ reads/sec
- **Write**: Direct writes without disk I/O
- **Compression**: 90%+ reduction in memory footprint

**Data Structure**:
```go
type MemoryStorage struct {
    buckets     map[int64]*Bucket  // Time-based buckets
    bucketSize  int64              // Bucket interval (seconds)
    mu          sync.RWMutex       // Concurrent access control
}

type Bucket struct {
    data map[uint64]*gorillaStream  // seriesID -> compressed stream
}
```

### 2. BadgerDB (Cold Data Persistence)

**Purpose**: Persistent storage for historical data with automatic expiration.

**Features**:
- LSM tree-based storage for write optimization
- Time-based partitioning (8 partitions)
- Configurable retention period (default: 48 hours for raw data)
- Automatic garbage collection
- Crash recovery

**Performance**:
- **Write**: 137,000+ inserts/sec (async batch writes)
- **Read**: 100,000+ reads/sec
- **Storage**: Efficient compression with LSM compaction

**Partitioning Strategy**:
```
Partition 0: seriesID % 8 == 0
Partition 1: seriesID % 8 == 1
...
Partition 7: seriesID % 8 == 7
```

Benefits:
- Parallel writes across partitions
- Reduced lock contention
- Better LSM tree performance
- Easier data management

### 3. Inverted Index

**Purpose**: Fast label-based series discovery.

**Features**:
- Posting lists for each label key-value pair
- String interning for memory efficiency
- Persistent across restarts
- Incremental updates

**Index Structure**:
```
Label "host=server1" → [seriesID1, seriesID2, ...]
Label "env=prod"     → [seriesID1, seriesID3, ...]
Label "__name__=cpu" → [seriesID1, seriesID2, seriesID3, ...]
```

**Query Optimization**:
- Label matching without full series scan
- Intersection of posting lists for multiple label filters
- Efficient topk/bottomk operations

See [LABELS_ARCHITECTURE.md](LABELS_ARCHITECTURE.md) for details.

## Data Flow

### Write Path

```
1. TsdbPut(ts, metric, value)
   │
   ├─→ Parse metric string → Labels
   │
   ├─→ String interning (labels)
   │
   ├─→ Calculate seriesID (hash of labels)
   │
   ├─→ Update inverted index
   │
   ├─→ Write to MemoryStorage
   │   └─→ Gorilla compression
   │
   └─→ Async write to BadgerDB
       └─→ Batch accumulation
       └─→ Periodic flush (every N points or timeout)
```

### Read Path (Query)

```
1. Get(metric, startTs, endTs)
   │
   ├─→ Parse query → Label matchers
   │
   ├─→ Inverted index lookup
   │   └─→ Get matching seriesIDs
   │
   ├─→ Determine cache cutoff time
   │   cacheCutoff = max(now - cacheDuration, serverStartTime)
   │
   ├─→ Query MemoryStorage (if startTs >= cacheCutoff)
   │   └─→ Gorilla decompression
   │
   ├─→ Query BadgerDB (if startTs < cacheCutoff)
   │   └─→ Time-based partition selection
   │   └─→ Bucket iteration
   │
   └─→ Merge results and return
```

## Cache Management

### Cache Eviction Policy

**Time-based Eviction**:
- Data older than `cacheDuration` is automatically evicted
- Background goroutine checks every minute
- Evicted data remains in BadgerDB

**Memory Pressure** (future enhancement):
- Monitor heap usage
- Evict oldest buckets when memory is low
- Configurable memory limits

### Async Write to Disk

**Batch Write Strategy**:
```go
type AsyncWriter struct {
    buffer      []DataPoint
    flushSize   int           // Trigger flush after N points
    flushTimer  *time.Timer   // Trigger flush after timeout
}
```

**Flush Triggers**:
1. Buffer size reaches threshold (e.g., 1000 points)
2. Timer expires (e.g., every 5 seconds)
3. Graceful shutdown

**Benefits**:
- Amortized disk I/O cost
- Better LSM tree performance
- Reduced write amplification
- Higher throughput

### Cache Coherence

**Write-through** strategy:
- Data is written to both memory and disk (async)
- Memory is the authoritative source for hot data
- Disk is the backup for durability

**Read consistency**:
- Memory cache always checked first for recent data
- Disk is only queried for historical data
- No cache invalidation needed (time-based eviction)

## Configuration

### Server Options

```bash
./krill-server \
  -addr :9090 \
  -data ./data \          # BadgerDB storage path
  -cache 1h \             # Memory cache duration
  -retention 48h          # Disk retention period
```

### YAML Configuration

```yaml
# conf.yaml
storage:
  type: "hybrid"
  
  memory:
    bucket_size: "1h"     # Cache duration
    
  badger:
    path: "./data/raw"
    partitions: 8         # Number of BadgerDB partitions
    retention: "48h"      # Data retention period
```

## Downsampling

Krill supports multi-level data retention with automatic downsampling:

```
Level 0 (raw):      Full resolution → 48h retention
Level 1 (1m):       1-minute avg    → 30d retention
Level 2 (1h):       1-hour avg      → 3y retention
```

**Downsampling Process**:
1. Background goroutine runs every minute
2. Reads raw data from last processed timestamp
3. Aggregates data by time window (1m, 1h, etc.)
4. Computes: avg, min, max, count
5. Writes aggregated data to level-specific BadgerDB

**Query Routing**:
- Queries automatically routed to appropriate level based on time range
- Level 0: Recent data (last 48h)
- Level 1: Medium-term data (2d - 30d)
- Level 2: Long-term data (>30d)

See configuration in `cmd/krill-server/main.go` for downsampling setup.

## Performance Characteristics

### Memory Cache

| Operation | Performance | Notes |
|-----------|------------|-------|
| Write | O(1) | Append to Gorilla stream |
| Read (single series) | O(n) | n = # of points, but compressed |
| Read (all series) | O(s * n) | s = # of series |
| Memory | 90% less | Gorilla compression |

### BadgerDB

| Operation | Performance | Notes |
|-----------|------------|-------|
| Write | O(log n) | LSM tree write |
| Read (range) | O(log n + k) | k = # of points returned |
| Read (scan) | O(n) | Sequential scan within bucket |
| Disk | Compressed | LSM compaction + Gorilla |

### Inverted Index

| Operation | Performance | Notes |
|-----------|------------|-------|
| Label lookup | O(1) | Hash map lookup |
| Series matching | O(m * log n) | m = # of label filters, n = # of series |
| Index update | O(1) | Append to posting list |

## Monitoring

### Key Metrics

**Memory Usage**:
- Current cache size
- Number of series in cache
- Number of buckets in cache

**Disk Usage**:
- BadgerDB size per partition
- LSM tree levels
- Compaction statistics

**Performance**:
- Write throughput (points/sec)
- Read latency (p50, p95, p99)
- Cache hit rate
- Async write queue size

**Data Integrity**:
- Number of corrupted series skipped
- BadgerDB errors
- Index consistency checks

## Best Practices

### Cache Duration

**Short cache (15m - 1h)**:
- Lower memory usage
- More disk reads for recent queries
- Suitable for high-cardinality metrics

**Long cache (2h - 24h)**:
- Higher memory usage
- Faster queries for recent data
- Suitable for frequently queried metrics

### Retention Period

**Short retention (24h - 7d)**:
- Smaller disk footprint
- Use downsampling for longer retention
- Suitable for high-frequency metrics

**Long retention (30d - 1y)**:
- Larger disk footprint
- Enable automatic downsampling
- Consider multiple retention levels

### Partitioning

**8 partitions (default)**:
- Good balance for most workloads
- Parallel writes without excessive overhead

**More partitions (16, 32)**:
- Better parallelism for high write load
- More file descriptors required
- Slightly higher memory overhead

## Troubleshooting

### High Memory Usage

**Symptoms**:
- OOM kills
- Slow queries
- Increased GC time

**Solutions**:
1. Reduce cache duration: `-cache 30m`
2. Enable downsampling for long-term data
3. Limit cardinality (number of unique label combinations)

### Slow Queries

**Symptoms**:
- High query latency
- Timeout errors

**Solutions**:
1. Use inverted index (avoid full series scan)
2. Add label filters to queries
3. Use appropriate step parameter for long time ranges
4. Check cache hit rate

### Disk Space Issues

**Symptoms**:
- Disk full errors
- BadgerDB compaction failures

**Solutions**:
1. Reduce retention period: `-retention 24h`
2. Enable automatic GC
3. Configure downsampling with shorter retention
4. Monitor disk usage and set up alerts

## Future Enhancements

1. **Memory Limit**: Automatic eviction based on memory pressure
2. **Cache Warming**: Pre-load frequently queried series
3. **Tiered Storage**: S3/Cloud storage for very old data
4. **Replication**: Master-slave replication for HA
5. **Sharding**: Horizontal scaling across multiple nodes
