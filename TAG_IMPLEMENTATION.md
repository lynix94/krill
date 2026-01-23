## Tag 지원 추가 완료

### 변경 사항

#### 1. API 변경
- **web/prometheus.go**
  - `WriteRequest`에 `Tags map[string]string` 필드 추가
  - `buildMetricKey()`: tag를 Prometheus 형식으로 직렬화
  - `parseMetricKey()`: 저장된 key에서 metric name과 tag 추출
  - `parseQuery()`: Prometheus 쿼리 파싱 (예: `cpu{env="prod"}`)
  - `matchesQuery()`: metric과 query의 tag 매칭
  - `HandleQuery()`: tag 필터링 지원으로 수정
  - `HandleQueryRange()`: tag 필터링 지원으로 수정

#### 2. Scraper 변경
- **scraper/scraper.go**
  - `WriteRequest`에 `Tags` 필드 추가
  - `sendMetric()` 시그니처에 tags 파라미터 추가
  - Label을 flat metric name으로 변환하지 않고 그대로 전송
  - Prometheus exporter의 label을 보존하여 전송

#### 3. 문서화
- **TAG_SUPPORT.md**: 완전한 tag 기능 문서
  - API 사용 예제
  - Query 문법
  - Scraper 통합
  - 저장 포맷
  - Tag 매칭 규칙
  
- **README.md**: Tag 기능 추가 명시
  - Features 섹션에 tag 지원 추가
  - API 예제에 tag 사용법 추가
  - Scraper features에 tag 지원 명시

### 기능 테스트 결과

✅ **Write with tags**: 성공
```bash
curl -X POST http://localhost:9090/api/v1/write \
  -d '{"metric":"cpu_usage","value":75.5,"tags":{"host":"server1","env":"prod"}}'
```

✅ **Query all instances**: 성공
```bash
curl 'http://localhost:9090/api/v1/query?query=cpu_usage'
# 모든 cpu_usage 메트릭 반환 (tag 무관)
```

✅ **Query with single tag**: 성공
```bash
QUERY=$(printf 'cpu_usage{env="prod"}' | jq -sRr @uri)
curl "http://localhost:9090/api/v1/query?query=$QUERY"
# env="prod" tag를 가진 메트릭만 반환
```

✅ **Query with multiple tags**: 성공
```bash
QUERY=$(printf 'cpu_usage{env="prod",host="server1"}' | jq -sRr @uri)
curl "http://localhost:9090/api/v1/query?query=$QUERY"
# 두 tag 모두 매칭하는 메트릭만 반환
```

✅ **Range query with tags**: 성공
```bash
QUERY=$(printf 'disk_usage{server="web1"}' | jq -sRr @uri)
curl "http://localhost:9090/api/v1/query_range?query=$QUERY&start=$START&end=$END"
# 시간 범위 + tag 필터링
```

✅ **Scraper tag integration**: 성공
- Prometheus exporter의 label을 그대로 전송
- job, instance label 자동 추가
- Config의 static label 병합

### 저장 형식

Metric key에 tag가 포함되어 저장:
```
cpu_usage{env="prod",host="server1"}
http_requests{method="GET",status="200"}
disk_usage{mount="/data",server="web1"}
```

Tag는 알파벳 순으로 정렬되어 일관성 유지.

### Query 응답 예시

```json
{
  "status": "success",
  "data": {
    "resultType": "vector",
    "result": [
      {
        "metric": {
          "__name__": "http_requests",
          "method": "GET",
          "status": "500"
        },
        "value": [1769158473, "5.000000"]
      }
    ]
  }
}
```

### 호환성

✅ **하위 호환성 유지**
- Tag 없는 메트릭도 정상 작동
- 기존 API 호출 (tags 필드 없음) 동작
- 기존 쿼리 (tag 필터 없음) 동작

✅ **모든 테스트 통과**
```
PASS: TestHybridTSDBBasic
PASS: TestHybridTSDBCacheQuery
PASS: TestHybridTSDBPersistence
PASS: TestHybridTSDBGetMetrics
PASS: TestHybridTSDBCleanup
PASS: TestTSDBPut
PASS: TestTSDBMultipleMetrics
PASS: TestTSDBTimestampValidation (idempotent writes)
PASS: TestTSDBCompression (18.18x ratio)
PASS: TestTSDBEmptyMetric
```

### 사용 예시

#### 다차원 메트릭 저장
```bash
# HTTP 요청 메트릭 (method, status, endpoint별로 분리)
curl -X POST http://localhost:9090/api/v1/write -d '{
  "metric": "http_requests_total",
  "value": 1234,
  "tags": {"method": "GET", "status": "200", "endpoint": "/api/users"}
}'

# CPU 사용률 (서버, 환경별로 분리)
curl -X POST http://localhost:9090/api/v1/write -d '{
  "metric": "cpu_usage_percent",
  "value": 75.5,
  "tags": {"host": "web-01", "env": "production", "region": "us-west"}
}'
```

#### Tag 기반 쿼리
```bash
# 특정 환경의 모든 서버 CPU
QUERY=$(printf 'cpu_usage_percent{env="production"}' | jq -sRr @uri)
curl "http://localhost:9090/api/v1/query?query=$QUERY"

# 특정 엔드포인트의 200 응답
QUERY=$(printf 'http_requests_total{endpoint="/api/users",status="200"}' | jq -sRr @uri)
curl "http://localhost:9090/api/v1/query?query=$QUERY"
```

### 제한사항 (향후 개선 가능)

- Regex matching 미지원 (`{env=~"prod|staging"}`)
- PromQL 연산자 미지원 (sum, rate, etc.)
- Label listing API 미제공 (`/api/v1/labels`)

### 결론

✅ Krill TSDB에 Prometheus 스타일 tag/label 지원 완전 구현
✅ Write, Query, QueryRange 모두 tag 지원
✅ Scraper가 Prometheus exporter label을 보존하여 전송
✅ 하위 호환성 유지
✅ 모든 기존 테스트 통과
