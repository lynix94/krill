# Tag/Label Support in Krill TSDB

## Overview
Krill now supports Prometheus-style tags (labels) for metrics, allowing for multi-dimensional time series data.

## Metric Format
Metrics are stored with tags using the Prometheus format:
```
metric_name{tag1="value1",tag2="value2"}
```

## API Changes

### Write API
The `/api/v1/write` endpoint now accepts an optional `tags` field:

```bash
curl -X POST http://localhost:9090/api/v1/write \
  -H 'Content-Type: application/json' \
  -d '{
    "metric": "cpu_usage",
    "value": 75.5,
    "tags": {
      "host": "server1",
      "env": "prod",
      "region": "us-west"
    }
  }'
```

**Fields:**
- `metric` (required): Base metric name
- `value` (required): Numeric value
- `time` (optional): Unix timestamp in seconds (defaults to current time)
- `tags` (optional): Map of tag key-value pairs

### Query API
Both `/api/v1/query` and `/api/v1/query_range` support tag filtering using Prometheus query syntax:

**Query all instances of a metric:**
```bash
curl 'http://localhost:9090/api/v1/query?query=cpu_usage'
```

**Query with single tag filter:**
```bash
QUERY=$(printf 'cpu_usage{env="prod"}' | jq -sRr @uri)
curl "http://localhost:9090/api/v1/query?query=$QUERY"
```

**Query with multiple tag filters:**
```bash
QUERY=$(printf 'cpu_usage{env="prod",host="server1"}' | jq -sRr @uri)
curl "http://localhost:9090/api/v1/query?query=$QUERY"
```

**Range query with tags:**
```bash
NOW=$(date +%s)
START=$((NOW - 3600))
QUERY=$(printf 'cpu_usage{region="us-west"}' | jq -sRr @uri)
curl "http://localhost:9090/api/v1/query_range?query=$QUERY&start=$START&end=$NOW"
```

### Response Format
Query responses include all tags for each time series:

```json
{
  "status": "success",
  "data": {
    "resultType": "vector",
    "result": [
      {
        "metric": {
          "__name__": "cpu_usage",
          "env": "prod",
          "host": "server1",
          "region": "us-west"
        },
        "value": [1769158299, "75.500000"]
      }
    ]
  }
}
```

## Scraper Integration
The scraper automatically sends tags to the Krill server:

1. **Prometheus exporter labels** are preserved
2. **Job labels** from scraper config are added
3. **Static labels** from targets are merged
4. **job** and **instance** labels are automatically added

**Example scraper config:**
```yaml
scrape_configs:
  - job_name: 'node'
    labels:
      region: 'us-west'
    static_configs:
      - targets: ['localhost:9100']
        labels:
          env: 'production'
```

**Resulting metrics:**
```
node_cpu_seconds_total{cpu="0",mode="idle",job="node",instance="localhost:9100",region="us-west",env="production"}
```

## Storage Format
Tags are serialized into the metric key using sorted order for consistency:
- `cpu_usage{env="prod",host="server1"}` 
- Tags are sorted alphabetically by key
- All instances with different tag combinations are stored as separate time series

## Tag Matching Rules
- **Empty query**: Returns all metrics (no filtering)
- **Name only** (`cpu_usage`): Returns all time series for that metric regardless of tags
- **Name + tags** (`cpu_usage{env="prod"}`): Returns only time series matching ALL specified tags
- **Tag values must match exactly** (no wildcards or regex support yet)

## Examples

### Writing Time Series Data
```bash
# Write with multiple tags
curl -X POST http://localhost:9090/api/v1/write \
  -H 'Content-Type: application/json' \
  -d '{
    "metric": "http_requests_total",
    "value": 1234,
    "tags": {
      "method": "GET",
      "status": "200",
      "endpoint": "/api/users"
    }
  }'

# Write without tags (still supported)
curl -X POST http://localhost:9090/api/v1/write \
  -H 'Content-Type: application/json' \
  -d '{"metric": "simple_metric", "value": 42}'
```

### Querying Tagged Metrics
```bash
# Get latest value for all endpoints
QUERY=$(printf 'http_requests_total' | jq -sRr @uri)
curl "http://localhost:9090/api/v1/query?query=$QUERY"

# Filter by status code
QUERY=$(printf 'http_requests_total{status="200"}' | jq -sRr @uri)
curl "http://localhost:9090/api/v1/query?query=$QUERY"

# Multiple filters
QUERY=$(printf 'http_requests_total{method="GET",status="200"}' | jq -sRr @uri)
curl "http://localhost:9090/api/v1/query?query=$QUERY"

# Range query with tags
NOW=$(date +%s)
START=$((NOW - 3600))
QUERY=$(printf 'http_requests_total{endpoint="/api/users"}' | jq -sRr @uri)
curl "http://localhost:9090/api/v1/query_range?query=$QUERY&start=$START&end=$NOW"
```

## Backward Compatibility
- Metrics without tags continue to work as before
- Old API calls without `tags` field work unchanged
- Queries without tag filters match tag-less metrics

## Implementation Details

### Data Model
- Metric key format: `name{key1="val1",key2="val2"}`
- Tags are sorted alphabetically for consistent key generation
- Storage layer sees complete metric key (name + tags)

### Query Processing
1. Parse query into metric name and tag matchers
2. Get all metrics from storage
3. Filter metrics by name and tag values
4. Return matching time series with parsed tags in response

### Code Changes
- **web/prometheus.go**: Added `Tags` field to `WriteRequest`, tag parsing/matching functions
- **scraper/scraper.go**: Modified to send tags instead of flattening into metric name
- **Storage layer**: No changes needed (tags are part of metric key)

## Future Enhancements
- Regex matching for tag values (`{env=~"prod|staging"}`)
- Tag name listing (`/api/v1/labels`)
- Tag value listing for specific tag (`/api/v1/label/env/values`)
- PromQL operators (sum, rate, etc.)
