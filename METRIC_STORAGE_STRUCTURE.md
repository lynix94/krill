# Krill TSDB 메트릭 저장 구조

## 저장 형식

### 1. 기본 구조
메트릭은 **단일 문자열 키**로 저장되며, 메트릭 이름과 태그를 하나의 키로 결합합니다.

```
형식: metric_name{tag1="value1",tag2="value2",...}
```

### 2. 태그 없는 메트릭
```
cpu.usage
memory.used
disk.free
```
- 단순 메트릭 이름만 저장
- 태그가 없으면 중괄호 없음

### 3. 태그 포함 메트릭
```
cpu.usage{env="prod",host="server1"}
http_requests{method="GET",status="200"}
node_cpu_seconds_total{cpu="0",mode="idle",instance="localhost:9100",job="node"}
```
- 메트릭 이름 + `{` + 태그들 + `}`
- 태그는 `key="value"` 형식
- 여러 태그는 콤마(`,`)로 구분
- **태그는 알파벳 순으로 자동 정렬**됨

### 4. 실제 저장 예시

서버에 현재 저장된 메트릭들:
```bash
# 태그 없는 메트릭
cpu
mem

# 태그 포함 메트릭
cpu.usage{env="prod",host="web1"}
cpu.usage{env="prod",host="web2"}
disk.free{host="web1",mount="/data"}
node.node_cpu_seconds_total{cpu="0",datacenter="dc1",environment="production",instance="localhost:9100",job="node",mode="idle"}
```

## 코드 구현

### buildMetricKey() 함수
([web/prometheus.go](web/prometheus.go#L249))

```go
// Format: metric_name{tag1="value1",tag2="value2"}
func buildMetricKey(name string, tags map[string]string) string {
	if len(tags) == 0 {
		return name  // 태그 없으면 이름만 반환
	}

	// Sort tags for consistent ordering
	var sortedTags []string
	for k, v := range tags {
		sortedTags = append(sortedTags, fmt.Sprintf("%s=\"%s\"", k, v))
	}
	
	// 태그를 알파벳 순으로 정렬 (일관성 유지)
	for i := 0; i < len(sortedTags); i++ {
		for j := i + 1; j < len(sortedTags); j++ {
			if sortedTags[i] > sortedTags[j] {
				sortedTags[i], sortedTags[j] = sortedTags[j], sortedTags[i]
			}
		}
	}

	return fmt.Sprintf("%s{%s}", name, strings.Join(sortedTags, ","))
}
```

**동작 원리:**
1. 태그가 없으면 → 메트릭 이름만 반환
2. 태그가 있으면:
   - 각 태그를 `key="value"` 형식으로 변환
   - 알파벳 순으로 정렬 (일관된 키 생성)
   - `metric_name{tag1="v1",tag2="v2"}` 형식으로 결합

### parseMetricKey() 함수
([web/prometheus.go](web/prometheus.go#L275))

```go
// 저장된 키에서 메트릭 이름과 태그 추출
func parseMetricKey(key string) (string, map[string]string) {
	tags := make(map[string]string)
	
	// '{' 위치 찾기
	bracePos := strings.Index(key, "{")
	if bracePos < 0 {
		return key, tags  // 태그 없음
	}
	
	name := key[:bracePos]  // 메트릭 이름 추출
	
	// '}' 위치 찾기
	endPos := strings.Index(key[bracePos:], "}")
	tagStr := key[bracePos+1 : endPos]
	
	// 태그 파싱: "k1="v1",k2="v2"" → map[k1:v1, k2:v2]
	pairs := strings.Split(tagStr, ",")
	for _, pair := range pairs {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 2 {
			k := strings.TrimSpace(parts[0])
			v := strings.Trim(strings.TrimSpace(parts[1]), "\"")
			tags[k] = v
		}
	}
	
	return name, tags
}
```

## Storage Layer

### 실제 저장 위치
메트릭 키는 두 곳에 저장됩니다:

1. **Memory Storage** (`storage/memory/memory.go`)
   - `map[string]*MetricSeries`
   - 키: `metric_name{tags...}`
   - 값: Gorilla 압축된 시계열 데이터

2. **BadgerDB** (`storage/badger/badger.go`)
   - Key: `metric_name{tags...}|timestamp`
   - Value: Gorilla 압축된 데이터
   - 시간 기반 파티셔닝 사용

### 예시
```go
// Memory storage
db.series = map[string]*MetricSeries{
    "cpu.usage": {...},
    "cpu.usage{env=\"prod\",host=\"server1\"}": {...},
    "cpu.usage{env=\"prod\",host=\"server2\"}": {...},
}

// BadgerDB
Key: "cpu.usage{env=\"prod\",host=\"server1\"}|1769159258"
Value: [compressed_gorilla_data]
```

## API 응답 구조

### Write 요청
```json
{
  "metric": "cpu_usage",
  "value": 75.5,
  "tags": {
    "host": "server1",
    "env": "prod"
  }
}
```
↓ 변환 ↓
```
저장 키: cpu_usage{env="prod",host="server1"}
```
(태그가 알파벳 순으로 정렬됨: env → host)

### Query 응답
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
          "host": "server1"
        },
        "value": [1769159258, "75.500000"]
      }
    ]
  }
}
```

**응답에서는**:
- 저장된 키를 파싱하여 분리
- `__name__`: 메트릭 이름
- 나머지: 개별 태그로 분리

## 태그 정렬의 중요성

**왜 정렬하는가?**
```go
// 정렬 안하면
tags1 := {"host":"s1", "env":"prod"}  → cpu{host="s1",env="prod"}
tags2 := {"env":"prod", "host":"s1"}  → cpu{env="prod",host="s1"}
// 같은 메트릭이지만 다른 키! (중복 저장)

// 정렬하면
tags1 := {"host":"s1", "env":"prod"}  → cpu{env="prod",host="s1"}
tags2 := {"env":"prod", "host":"s1"}  → cpu{env="prod",host="s1"}
// 항상 같은 키! (올바른 동작)
```

## 쿼리 매칭

### 메트릭 이름만 쿼리
```
Query: cpu_usage
Match: 
  - cpu_usage
  - cpu_usage{env="prod",host="server1"}
  - cpu_usage{env="dev",host="server2"}
```

### 태그 필터링 쿼리
```
Query: cpu_usage{env="prod"}
Match:
  - cpu_usage{env="prod",host="server1"}  ✓
  - cpu_usage{env="prod",host="server2"}  ✓
  - cpu_usage{env="dev",host="server3"}   ✗
```

### 다중 태그 필터링
```
Query: cpu_usage{env="prod",host="server1"}
Match:
  - cpu_usage{env="prod",host="server1"}           ✓
  - cpu_usage{env="prod",host="server1",cpu="0"}   ✓ (추가 태그 있어도 OK)
  - cpu_usage{env="prod",host="server2"}           ✗
```

## 장단점

### 장점
✅ **단순함**: 복잡한 인덱스 불필요, 단일 문자열 키
✅ **성능**: Map/DB lookup 1회로 즉시 조회
✅ **호환성**: Prometheus 형식과 동일
✅ **정렬 보장**: 태그 순서 상관없이 일관된 키

### 단점
❌ **태그별 인덱스 없음**: 특정 태그 값으로 검색 시 전체 스캔 필요
❌ **메모리**: 메트릭마다 전체 키 문자열 저장 (중복 가능)
❌ **쿼리 성능**: 복잡한 태그 필터링 시 O(N) 스캔

### 개선 가능성
- Inverted index 추가 (tag → metrics)
- Tag value 압축/dedupe
- Metric name과 tags 분리 저장
