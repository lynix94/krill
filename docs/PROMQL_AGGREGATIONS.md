# PromQL Aggregation Functions

Krill TSDB now supports PromQL-style aggregation functions for post-processing query results.

## Supported Aggregation Functions

### Basic Aggregations

#### `sum()` - Calculate sum over dimensions
```bash
# Sum all cpu_usage metrics
curl 'http://localhost:9090/api/v1/query?query=sum(cpu_usage)'

# Sum by environment
curl 'http://localhost:9090/api/v1/query?query=sum(cpu_usage)%20by%20(env)'
```

#### `avg()` - Calculate average over dimensions
```bash
# Average of all cpu_usage metrics
curl 'http://localhost:9090/api/v1/query?query=avg(cpu_usage)'

# Average by host
curl 'http://localhost:9090/api/v1/query?query=avg(cpu_usage)%20by%20(host)'
```

#### `min()` - Select minimum over dimensions
```bash
# Minimum value across all metrics
curl 'http://localhost:9090/api/v1/query?query=min(cpu_usage)'
```

#### `max()` - Select maximum over dimensions
```bash
# Maximum value across all metrics
curl 'http://localhost:9090/api/v1/query?query=max(cpu_usage)'
```

#### `count()` - Count number of elements
```bash
# Count total number of series
curl 'http://localhost:9090/api/v1/query?query=count(cpu_usage)'

# Count by environment
curl 'http://localhost:9090/api/v1/query?query=count(cpu_usage)%20by%20(env)'
```

### Statistical Functions

#### `stddev()` - Calculate population standard deviation
```bash
# Standard deviation of all values
curl 'http://localhost:9090/api/v1/query?query=stddev(cpu_usage)'
```

#### `stdvar()` - Calculate population standard variance
```bash
# Variance of all values
curl 'http://localhost:9090/api/v1/query?query=stdvar(cpu_usage)'
```

### Top/Bottom K Functions

#### `topk(k, metric)` - Largest k elements by sample value
```bash
# Get top 5 highest CPU usage
curl 'http://localhost:9090/api/v1/query?query=topk(5,%20cpu_usage)'
```

#### `bottomk(k, metric)` - Smallest k elements by sample value
```bash
# Get bottom 3 lowest CPU usage
curl 'http://localhost:9090/api/v1/query?query=bottomk(3,%20cpu_usage)'
```

### Quantile Function

#### `quantile(φ, metric)` - Calculate φ-quantile (0 ≤ φ ≤ 1)
```bash
# 95th percentile
curl 'http://localhost:9090/api/v1/query?query=quantile(0.95,%20cpu_usage)'

# Median (50th percentile)
curl 'http://localhost:9090/api/v1/query?query=quantile(0.5,%20cpu_usage)'
```

### Count Values Function

#### `count_values("label", metric)` - Count number of elements with the same value
```bash
# Count how many times each value appears
curl 'http://localhost:9090/api/v1/query?query=count_values("value",%20cpu_usage)'
```

## Grouping with `by` Clause

You can group aggregation results by specific labels using the `by` clause:

```bash
# Sum CPU usage by environment
sum(cpu_usage) by (env)

# Average memory by host and region
avg(memory_usage) by (host, region)

# Count metrics by job
count(http_requests) by (job)
```

## Query Examples

### Example 1: Monitor per-environment CPU usage
```bash
curl 'http://localhost:9090/api/v1/query?query=avg(cpu_usage)%20by%20(env)'
```

Response:
```json
{
  "status": "success",
  "data": {
    "resultType": "vector",
    "result": [
      {
        "metric": {"env": "prod"},
        "value": [1769172599, "65.000000"]
      },
      {
        "metric": {"env": "dev"},
        "value": [1769172633, "47.500000"]
      }
    ]
  }
}
```

### Example 2: Find top 5 busiest servers
```bash
curl 'http://localhost:9090/api/v1/query?query=topk(5,%20cpu_usage)'
```

### Example 3: Calculate 99th percentile response time
```bash
curl 'http://localhost:9090/api/v1/query?query=quantile(0.99,%20http_response_time)'
```

### Example 4: Total requests per endpoint
```bash
curl 'http://localhost:9090/api/v1/query?query=sum(http_requests)%20by%20(endpoint)'
```

## Range Queries

Aggregation functions also work with range queries (`/api/v1/query_range`):

```bash
START=$(date -d '1 hour ago' +%s)
END=$(date +%s)

# Sum over time range
curl "http://localhost:9090/api/v1/query_range?query=sum(cpu_usage)&start=$START&end=$END"
```

The aggregation is applied at each timestamp in the range.

## Implementation Notes

- Aggregations are performed as post-processing after querying the TSDB
- For instant queries (`/api/v1/query`), aggregation operates on the latest values
- For range queries (`/api/v1/query_range`), aggregation is applied per-timestamp
- Group-by operates on label dimensions, creating separate results per group
- All aggregations preserve Prometheus API compatibility

## Performance Considerations

- Aggregations are computed in-memory after data retrieval
- Large result sets may impact query performance
- Use label filters to reduce the initial result set before aggregation
- Consider using `topk`/`bottomk` for large datasets instead of retrieving all values

## Examples with Label Filtering

Combine label filtering with aggregations:

```bash
# Average CPU for production servers only
avg(cpu_usage{env="prod"})

# Sum of requests for specific endpoint
sum(http_requests{endpoint="/api/users"})

# Top 10 errors by status code
topk(10, http_errors) by (status_code)
```

## Dashboard Integration

The Krill web dashboard automatically supports these aggregation functions in the query interface. Simply enter PromQL queries like:

```
sum(cpu_usage) by (env)
avg(memory_usage) by (host)
topk(5, http_requests)
```

The results will be displayed in the same format as regular queries.
