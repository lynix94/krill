# Krill - Time Series Database with Gorilla Compression

고성능 시계열 데이터베이스 라이브러리로, Facebook의 Gorilla 압축 알고리즘을 사용합니다.

## Features

- **Gorilla 압축 알고리즘**: 타임스탬프와 값을 효율적으로 압축
- **메모리/영구 저장소**: 메모리 기반 또는 BadgerDB 영구 저장소 지원
- **Tag/Label 지원**: Prometheus 스타일 다차원 메트릭 (예: `cpu{host="server1",env="prod"}`)
- **PromQL 집계 함수**: sum, avg, min, max, count, stddev, topk, quantile 등 지원
- **Python 함수 파이프라인**: 커스텀 데이터 처리 (PyKrill Daemon - **10배 빠른 성능**)
- **고성능**: 빠른 읽기/쓰기 (33k+ writes/sec, 3M+ reads/sec)
- **Thread-safe**: 동시성 안전한 구현
- **TTL 지원**: 시간 기반 데이터 만료
- **Time-based 파티셔닝**: 효율적인 범위 쿼리
- **간단한 API**: 직관적인 `TsdbPut` 함수

## Performance Highlights

- **Compression**: Gorilla 알고리즘으로 90%+ 압축률
- **Query Speed**: 3M+ reads/sec (메모리), 100k+ reads/sec (BadgerDB)
- **Write Speed**: 33k+ writes/sec (메모리), 10k+ writes/sec (BadgerDB)
- **Python Functions**: PyKrill Daemon으로 16ms 평균 응답 (기존 대비 10배 향상)

## Gorilla Compression

### Timestamp Compression
타임스탬프 델타의 델타를 인코딩하여 저장:
- 델타가 같으면: 1 bit
- 작은 변화: 2-4 bits + 데이터
- 정규 간격 데이터에서 매우 효율적

### Value Compression (XOR encoding)
이전 값과의 XOR을 저장:
- 값이 같으면: 1 bit
- 유사한 값: 2 bits + 압축된 XOR 데이터
- Float64 값에 최적화됨

## Installation

```bash
go get github.com/lynix/krill
```

## Usage

### Basic Example

```go
package main

import (
    "fmt"
    "github.com/lynix/krill"
)

func main() {
    // TSDB 인스턴스 생성
    db := krill.NewTSDB()
    
    // 데이터 입력
    db.tsdb_put(1000, "cpu.usage", 45.5)
    db.tsdb_put(2000, "cpu.usage", 48.2)
    db.tsdb_put(3000, "cpu.usage", 52.1)
    
    // 데이터 조회
    timestamps, values, err := db.Get("cpu.usage")
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

**`MemoryTSDB() *TSDB`**
메모리 기반 TSDB를 생성합니다.

**`TsdbPut(ts int64, metric string, value float64) error`**
시계열 데이터 포인트를 저장합니다.

**Parameters:**
- `ts`: Unix 타임스탬프 (int64)
- `metric`: 메트릭 이름 (string)
- `value`: 값 (float64)

**`Get(metric string, startTs, endTs int64) ([]int64, []float64, error)`**
메트릭의 데이터 포인트를 조회합니다. startTs, endTs를 0으로 설정하면 모든 데이터를 조회합니다.

**`GetMetrics() ([]string, error)`**
저장된 모든 메트릭 이름을 반환합니다.

**`Close() error`**
데이터베이스를 닫습니다. (메모리 TSDB는 no-op)

#### Persistent TSDB (BadgerDB)

**`PersistentTSDB(path string) (*BadgerTSDB, error)`**
영구 저장소 TSDB를 생성합니다 (TTL 없음).

**`PersistentTSDBWithTTL(path string, ttl time.Duration) (*BadgerTSDB, error)`**
TTL이 있는 영구 저장소 TSDB를 생성합니다.

**`RunGC() error`**
디스크 공간 회수를 위한 가비지 컬렉션을 실행합니다.

모든 다른 메서드는 메모리 TSDB와 동일합니다.

## Performance

### 메모리 TSDB
일반적인 시계열 데이터(정규 간격, 유사한 값)에서:
- **압축률**: 2x - 10x (평균 18x)
- **타임스탬프 압축**: 평균 1-2 bits per value
- **값 압축**: 평균 2-4 bits per value (원본 64 bits)

### Persistent TSDB (BadgerDB)
- **Write 성능**: 33,000+ inserts/sec
- **Read 성능**: 3,300,000+ reads/sec
- **압축**: Gorilla + BadgerDB LSM tree 이중 압축
- **디스크 I/O**: 배치 쓰기로 최적화

## Storage Options

### 1. Memory TSDB
- ✅ 초고속 읽기/쓰기
- ✅ Zero 의존성
- ❌ 데이터 휘발성
- **사용 사례**: 실시간 모니터링, 임시 메트릭

### 2. BadgerDB TSDB
- ✅ 영구 저장
- ✅ TTL 지원
- ✅ 시간 기반 파티셔닝
- ✅ 가비지 컬렉션
- **사용 사례**: 프로덕션 메트릭, 장기 저장

## Testing

```bash
# 전체 테스트
go test -v

# 메모리 TSDB 테스트만
go test -v -run TestTSDB

# BadgerDB TSDB 테스트만
go test -v -run TestBadgerTSDB
```

## Examples

```bash
# 메모리 TSDB 예제
cd example/memory_example
go run main.go

# BadgerDB 영구 저장소 예제
cd example/badger_example
go run main.go
```

## Architecture

```
krill/
├── tsdb.go                     - 메모리 TSDB 구조체 및 API
├── interface.go                - 공통 인터페이스 정의
├── tsdb_test.go               - 메모리 TSDB 테스트
├── pykrill.py                 - Python 함수 실행 데몬 (고성능)
├── storage/
│   ├── gorilla/               - Gorilla 압축 알고리즘
│   │   ├── bitstream.go       - 비트 단위 읽기/쓰기
│   │   ├── timestamp.go       - 타임스탬프 압축/해제
│   │   └── value.go           - 값 압축/해제 (XOR)
│   ├── badger/                - BadgerDB 영구 저장소
│   │   ├── badger.go          - BadgerDB TSDB 구현
│   │   └── badger_test.go     - BadgerDB 테스트
│   └── labels.go              - 라벨/태그 관리
├── web/
│   ├── server.go              - HTTP 서버
│   ├── prometheus.go          - Prometheus API 핸들러
│   ├── function.go            - 파이프라인 함수 처리 (Python 데몬 통신)
│   ├── aggregation.go         - PromQL 집계 함수
│   └── dashboard.go           - Web UI
├── cmd/
│   ├── krill-server/          - TSDB 서버
│   ├── krill-cli/             - CLI 도구
│   └── krill-scraper/         - Prometheus 스크래퍼
├── docs/
│   ├── PYKRILL_DAEMON.md      - Python 데몬 상세 문서 (성능 최적화)
│   ├── PROMQL_AGGREGATIONS.md - PromQL 집계 함수 가이드
│   └── KRILL_CLI_GUIDE.md     - CLI 사용 가이드
└── example/
    ├── memory_example/        - 메모리 TSDB 예제
    └── badger_example/        - BadgerDB TSDB 예제
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

## HTTP API Server

### Quick Start

서버 빌드 및 실행:

```bash
# Build server
go build -o krill-server ./cmd/krill-server

# Run with default settings (port 9090, hybrid storage)
./krill-server

# Memory-only mode
./krill-server -memory

# Custom configuration
./krill-server -addr :8080 -data /var/lib/krill -cache 1h
```

### Prometheus-Compatible API

Krill은 Prometheus 호환 REST API를 제공합니다:

#### 1. Instant Query

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

#### 2. Range Query

```bash
curl "http://localhost:9090/api/v1/query_range?query=memory.used&start=1769150810&end=1769154410"
```

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

#### 3. Write Data

```bash
# Without tags
curl -X POST http://localhost:9090/api/v1/write \
  -H 'Content-Type: application/json' \
  -d '{"metric":"test.metric","value":123.45,"time":1234567890}'

# With tags (Prometheus-style labels)
curl -X POST http://localhost:9090/api/v1/write \
  -H 'Content-Type: application/json' \
  -d '{
    "metric": "cpu_usage",
    "value": 75.5,
    "tags": {
      "host": "server1",
      "env": "prod"
    }
  }'
```

#### 3a. Query with Tags

```bash
# Query all instances of a metric
curl 'http://localhost:9090/api/v1/query?query=cpu_usage'

# Query with tag filter (URL encode the query)
QUERY=$(printf 'cpu_usage{env="prod"}' | jq -sRr @uri)
curl "http://localhost:9090/api/v1/query?query=$QUERY"
```

#### 3b. PromQL Aggregation Functions

```bash
# Sum all CPU usage
curl 'http://localhost:9090/api/v1/query?query=sum(cpu_usage)'

# Average by environment
curl 'http://localhost:9090/api/v1/query?query=avg(cpu_usage)%20by%20(env)'

# Top 5 highest values
curl 'http://localhost:9090/api/v1/query?query=topk(5,%20cpu_usage)'

# 95th percentile
curl 'http://localhost:9090/api/v1/query?query=quantile(0.95,%20response_time)'
```

**Supported aggregation functions:**
- `sum`, `avg`, `min`, `max`, `count`
- `stddev`, `stdvar`
- `topk`, `bottomk`
- `quantile`, `count_values`

**See [docs/PROMQL_AGGREGATIONS.md](docs/PROMQL_AGGREGATIONS.md) for complete documentation.**

#### 4. List Metrics

```bash
curl 'http://localhost:9090/api/v1/label/__name__/values'
```

**Response:**
```json
{
  "status": "success",
  "data": ["cpu.usage", "memory.used", "test.metric"]
}
```

### Interactive Dashboard

Krill은 웹 기반 대시보드를 제공합니다:

**URL**: `http://localhost:9090/`

**주요 기능**:
- ✅ **탭 기반 UI**: Read/Write 기능을 탭으로 분리
- ✅ **Autocomplete**: Metric 입력 시 자동완성 (키보드 네비게이션 지원)
- ✅ **Instant Query**: 최신 값 조회
- ✅ **Range Query**: 시간 범위 데이터 + Chart.js 차트
- ✅ **Tag 지원**: JSON 형식으로 tag 입력 가능
- ✅ **실시간 통계**: 총 메트릭 수, 쿼리/쓰기 횟수

**Read 탭**:
- Metric name 입력 (autocomplete 지원)
- Query type 선택 (instant/range)
- 시간 범위 설정 (range query)
- JSON 결과 및 시각화 차트

**Write 탭**:
- Metric name 입력 (autocomplete 지원)
- Value 입력
- Tags 입력 (JSON 형식, 예: `{"host":"web1","env":"prod"}`)
- Timestamp (선택적)

**Autocomplete 사용법**:
- 메트릭 이름 입력 시 자동으로 드롭다운 표시
- 화살표 키(↑↓)로 선택, Enter로 확정
- ESC로 닫기

#### 5. Health Check

```bash
curl http://localhost:9090/health
# Response: OK
```

### Server Options

- `-addr`: HTTP 서버 주소 (기본값: `:9090`)
- `-data`: 영구 저장소 경로 (기본값: `/tmp/krill-data`)
- `-cache`: 메모리 캐시 기간 (기본값: `2h`)
- `-memory`: 메모리 전용 모드

### Test API

API 테스트 스크립트 실행:

```bash
./test_api.sh
```

## Metrics Scraper

Krill scraper는 Prometheus 호환 exporter들로부터 메트릭을 수집하여 Krill server로 전송합니다.

### Architecture

```
Prometheus Exporter → Scraper → HTTP API → Krill Server → TSDB
```

### Scraper 실행

```bash
# 1. Start Krill server first
./krill-server

# 2. Build scraper
go build -o krill-scraper ./cmd/krill-scraper

# 3. Run scraper
./krill-scraper -config scraper.yaml -server http://localhost:9090

# Connect to remote server
./krill-scraper -config scraper.yaml -server http://krill.example.com:9090

# Custom statistics interval
./krill-scraper -config scraper.yaml -stats 30s
```

### Configuration (scraper.yaml)

```yaml
global:
  scrape_interval: 15s  # 기본 수집 주기
  scrape_timeout: 10s   # 타임아웃

scrape_configs:
  - job_name: 'node-exporter'
    scrape_interval: 30s
    metrics_path: '/metrics'
    metric_prefix: 'node'  # 메트릭 이름 앞에 추가
    labels:
      cluster: 'prod'      # 모든 메트릭에 추가할 레이블
    static_configs:
      - targets:
          - 'localhost:9100'
        labels:
          environment: 'production'
```

### Demo

Mock exporter와 Krill server를 함께 실행하는 데모:

```bash
./demo_scraper.sh
```

### Features

- ✅ **Prometheus 호환**: 표준 Prometheus 메트릭 포맷 지원
- ✅ **Tag/Label 지원**: 메트릭 레이블을 그대로 Krill에 전송 (다차원 시계열)
- ✅ **HTTP API 전송**: Krill server의 /api/v1/write 엔드포인트로 전송
- ✅ **자동 수집**: 설정된 주기마다 자동으로 메트릭 수집
- ✅ **병렬 처리**: 여러 타겟 동시 scrape
- ✅ **레이블 관리**: job, instance, custom labels 자동 추가
- ✅ **통계**: 수집 성공률, 메트릭 수 등 실시간 통계
- ✅ **원격 서버**: 중앙 집중식 Krill server로 메트릭 전송

### Supported Exporters

- Node Exporter (system metrics)
- Prometheus (self-monitoring)
- Custom application exporters
- Any Prometheus-compatible exporter

