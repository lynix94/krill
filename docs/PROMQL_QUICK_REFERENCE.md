# PromQL Aggregation Quick Reference

## Function Syntax

| Function | Syntax | Description |
|----------|--------|-------------|
| **sum** | `sum(metric)` | Sum all values |
| **avg** | `avg(metric)` | Average of all values |
| **min** | `min(metric)` | Minimum value |
| **max** | `max(metric)` | Maximum value |
| **count** | `count(metric)` | Count of series |
| **stddev** | `stddev(metric)` | Standard deviation |
| **stdvar** | `stdvar(metric)` | Variance |
| **topk** | `topk(5, metric)` | Top 5 highest values |
| **bottomk** | `bottomk(3, metric)` | Bottom 3 lowest values |
| **quantile** | `quantile(0.95, metric)` | 95th percentile |
| **count_values** | `count_values("label", metric)` | Count occurrences |

## Group By

Add `by (label1, label2)` to group results:

```promql
sum(http_requests) by (endpoint)
avg(cpu_usage) by (env, region)
count(errors) by (status_code)
```

## Common Patterns

### Monitoring
```promql
# Total requests per service
sum(http_requests) by (service)

# Average response time per endpoint
avg(http_response_time) by (endpoint)

# Error rate by environment
sum(http_errors) by (env) / sum(http_requests) by (env)
```

### Performance
```promql
# 99th percentile latency
quantile(0.99, latency)

# Top 10 slowest endpoints
topk(10, avg(response_time) by (endpoint))

# CPU usage outliers
topk(5, cpu_usage)
```

### Capacity Planning
```promql
# Total memory across cluster
sum(memory_usage)

# Average per host
avg(memory_usage) by (host)

# Standard deviation (variability)
stddev(memory_usage)
```

## REST API Examples

### cURL
```bash
# Sum
curl 'http://localhost:9090/api/v1/query?query=sum(cpu_usage)'

# Average by environment
curl 'http://localhost:9090/api/v1/query?query=avg(cpu_usage)%20by%20(env)'

# Top 5
curl 'http://localhost:9090/api/v1/query?query=topk(5,%20http_requests)'
```

### krill-cli
```bash
# Interactive mode
krill-cli -server http://localhost:9090

# Then run queries:
krill> query sum(cpu_usage)
krill> query avg(memory_usage) by (host)
krill> query topk(10, http_requests)
```

## Implementation Notes

- All aggregations work with both instant and range queries
- Grouping creates separate results per group
- Parameters (k, φ) must be numeric literals
- Empty results return empty arrays
- Timestamps are preserved from source data
