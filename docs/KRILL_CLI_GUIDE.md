# krill-cli - Command Line Tool for Krill TSDB

Krill TSDB를 위한 커맨드라인 클라이언트 도구입니다.

## 설치

```bash
go build -o krill-cli ./cmd/krill-cli
```

## 사용 모드

### 1. Interactive Mode (대화형 모드)

명령어 없이 실행하면 interactive 모드로 진입합니다:

```bash
krill-cli -server http://localhost:9090
```

#### Interactive 모드 기능

- **명령어 히스토리**: 위/아래 화살표로 이전 명령어 탐색
- **히스토리 저장**: `~/.krill_history` 파일에 자동 저장
- **라인 편집**: 좌/우 화살표, Home/End, Ctrl+A/E 등 지원
- **자동 완성**: Tab 키로 명령어 자동 완성 (향후 추가 예정)

#### Interactive 모드 예시

```
$ krill-cli -server http://localhost:9090
Krill CLI - Interactive Mode
Connected to: http://localhost:9090
Type 'help' for usage, 'exit' to quit

krill> put now cpu.usage 45.5
✓ Written: cpu.usage = 45.500000 (ts=1769162631)
Successfully written 1/1 metrics

krill> query cpu.usage 0 9999999999
Metric: __name__=cpu.usage
Data Points: 1
Timestamp                | Value
-------------------------|----------------
2026-01-23 19:03:51 | 42.500000

krill> put now 'http_requests{method="GET"}' 150 memory.free 2048
✓ Written: http_requests{method="GET"} = 150.000000 (ts=1769162650)
✓ Written: memory.free = 2048.000000 (ts=1769162650)
Successfully written 2/2 metrics

krill> help
(도움말 표시)

krill> exit
Goodbye!
```

#### Interactive 모드 단축키

- `Ctrl+C`: 현재 입력 취소 (빈 줄에서는 종료)
- `Ctrl+D`: 종료
- `Ctrl+A`: 줄 처음으로 이동
- `Ctrl+E`: 줄 끝으로 이동
- `Ctrl+W`: 이전 단어 삭제
- `Ctrl+U`: 줄 전체 삭제
- `↑/↓`: 명령어 히스토리 탐색
- `←/→`: 커서 이동

### 2. Single Command Mode (단일 명령 모드)

명령어를 인자로 전달하여 한 번만 실행:

```bash
krill-cli -server <url> <command> [arguments...]
```

## 기본 구문

```bash
krill-cli -server <url> <command> [arguments...]
```

### 옵션

- `-server <url>`: Krill 서버 URL (기본값: `http://localhost:9090`)

## 명령어

### 0. help - 도움말 표시

도움말을 표시합니다.

#### 구문 (Interactive 모드)
```bash
krill> help
```

#### 구문 (Single Command 모드)
```bash
krill-cli --help
```

### 1. query_range - 메트릭 데이터 조회

지정된 시간 범위 내의 메트릭 데이터를 조회합니다.

#### 구문
```bash
krill-cli -server <url> query_range <metric> <start_ts> <end_ts>
```

#### 예제 (Interactive 모드)
```bash
krill> query_range cpu.usage 1706000000 1706003600
krill> query 'http_requests{method="GET"}' 0 9999999999
krill> query memory.usage now-1h now
```

#### 예제 (Single Command 모드)
```bash
# 기본 메트릭 조회
krill-cli -server http://localhost:9090 query_range cpu.usage 1706000000 1706003600

# 태그가 있는 메트릭 조회
krill-cli -server http://localhost:9090 query_range 'http_requests{method="GET"}' 0 9999999999

# 상대 시간으로 조회
krill-cli -server http://localhost:9090 query_range memory.usage now-1h now

# 전체 데이터 조회
krill-cli query_range cpu.usage 0 9999999999
```

#### 출력 예시
```
Metric: __name__=cpu.usage
Data Points: 6

Timestamp                | Value
-------------------------|----------------
2026-01-23 17:55:55 | 45.500000
2026-01-23 18:25:55 | 52.300000
2026-01-23 18:40:55 | 48.700000
```

### 2. put - 메트릭 데이터 입력

하나 이상의 메트릭 데이터 포인트를 입력합니다.

#### 구문
```bash
krill-cli -server <url> put <timestamp> <metric> <value> [<metric> <value> ...]
```

#### 예제 (Interactive 모드)
```bash
krill> put now cpu.usage 45.5
krill> put now cpu.usage 45.5 memory.usage 78.3 disk.usage 62.1
krill> put now 'http_requests{method="GET",status="200"}' 150
krill> put 1706000000 cpu.usage 45.5
krill> put now-1h cpu.load 1.5
```

#### 예제 (Single Command 모드)
```bash
# 단일 메트릭 입력 (현재 시간)
krill-cli -server http://localhost:9090 put now cpu.usage 45.5

# 여러 메트릭 동시 입력
krill-cli put now \
  cpu.usage 45.5 \
  memory.usage 78.3 \
  disk.usage 62.1

# 태그가 있는 메트릭 입력
krill-cli put now \
  'http_requests{method="GET",status="200"}' 150 \
  'http_requests{method="POST",status="201"}' 42

# 특정 시간에 입력
krill-cli put 1706000000 cpu.usage 45.5

# 상대 시간으로 입력 (1시간 전)
krill-cli put now-1h cpu.load 1.5
```

#### 출력 예시
```
✓ Written: cpu.usage = 45.500000 (ts=1769162199)
✓ Written: memory.usage = 78.300000 (ts=1769162199)

Successfully written 2/2 metrics
```

## 타임스탬프 형식

krill-cli는 다양한 타임스탬프 형식을 지원합니다:

### 1. 현재 시간
```bash
now
```

### 2. Unix 타임스탬프
```bash
1706000000
```

### 3. 상대 시간

#### 과거 시간 (now-duration)
```bash
now-1h    # 1시간 전
now-30m   # 30분 전
now-2h    # 2시간 전
now-1d    # 1일 전
```

#### 미래 시간 (now+duration)
```bash
now+1h    # 1시간 후
now+30m   # 30분 후
```

#### 지원하는 단위
- `s`: 초 (seconds)
- `m`: 분 (minutes)
- `h`: 시간 (hours)
- `d`: 일 (days)

## 메트릭 형식

### 기본 메트릭 (태그 없음)
```bash
cpu.usage
memory.free
disk.total
```

### 태그가 있는 메트릭 (Prometheus 형식)
```bash
http_requests{method="GET",status="200"}
cpu.usage{host="server1",core="0"}
memory.used{instance="localhost:9100"}
```0: Interactive 모드로 작업하기

```bash
# Interactive 모드 시작
$ krill-cli -server http://localhost:9090

Krill CLI - Interactive Mode
Connected to: http://localhost:9090
Type 'help' for usage, 'exit' to quit

# 데이터 입력
krill> put now cpu.usage 45.5 memory.used 8192

✓ Written: cpu.usage = 45.500000 (ts=1769162631)
✓ Written: memory.used = 8192.000000 (ts=1769162631)
Successfully written 2/2 metrics

# 조회
krill> query cpu.usage 0 9999999999

Metric: __name__=cpu.usage
Data Points: 1
Timestamp                | Value
-------------------------|----------------
2026-01-23 19:03:51 | 45.500000

# 태그가 있는 메트릭
krill> put now 'http_requests{method="GET",status="200"}' 150

✓ Written: http_requests{method="GET",status="200"} = 150.000000 (ts=1769162650)
Successfully written 1/1 metrics

# 여러 명령어 - history로 이전 명령어 재사용 가능 (↑ 키)
krill> query 'http_requests{method="GET"}' 0 9999999999

# 종료
krill> exit
Goodbye!
```

### 시나리오 

**주의**: 태그가 있는 메트릭은 쉘에서 인용 부호로 감싸야 합니다:
```bash
krill-cli put now 'http_requests{method="GET"}' 100
```

## 전체 사용 예제

### 시나리오 1: 실시간 모니터링 데이터 입력

```bash
# 현재 시스템 메트릭 수집 및 입력
krill-cli put now \
  'cpu.usage{host="server1"}' 45.5 \
  'memory.used{host="server1"}' 8192 \
  'disk.free{host="server1",mount="/"}' 102400

# 결과 확인
krill-cli query_range 'cpu.usage{host="server1"}' now-5m now
```

### 시나리오 2: 과거 데이터 입력

```bash
# 1시간 전 데이터 입력
krill-cli put now-1h cpu.load 1.2

# 30분 전 데이터 입력
krill-cli put now-30m cpu.load 1.5

# 입력한 데이터 조회
krill-cli query_range cpu.load now-2h now
```

### 시나리오 3: HTTP 메트릭 모니터링

```bash
# HTTP 요청 메트릭 입력
krill-cli put now \
  'http_requests{method="GET",status="200",endpoint="/api/users"}' 1523 \
  'http_requests{method="GET",status="404",endpoint="/api/users"}' 12 \
  'http_requests{method="POST",status="201",endpoint="/api/users"}' 89 \
  'http_duration_ms{method="GET",endpoint="/api/users"}' 45.2

# 특정 메서드의 요청 조회
krill-cli query_range 'http_requests{method="GET"}' now-1h now
```

### 시나리오 4: 배치 데이터 입력

```bash
#!/bin/bash
# 여러 호스트의 메트릭을 한 번에 입력

TIMESTAMP=$(date +%s)

krill-cli put $TIMESTAMP \
  'cpu.usage{host="server1"}' 45.5 \
  'cpu.usage{host="server2"}' 52.3 \
  'cpu.usage{host="server3"}' 38.7 \
  'memory.used{host="server1"}' 8192 \
  'memory.used{host="server2"}' 12288 \
  'memory.used{host="server3"}' 6144
```

## 에러 처리

krill-cli는 다양한 상황에서 적절한 에러 메시지를 출력합니다:

```bash
# 잘못된 타임스탬프
$ krill-cli put invalid cpu.usage 45.5
Error: invalid timestamp: invalid timestamp format (use 'now', 'now-1h', or Unix timestamp): ...

# 서버 연결 실패
$ krill-cli -server http://localhost:9999 query_range cpu.usage 0 9999999999
Error: failed to query server: ...

# 잘못된 메트릭-값 쌍
$ krill-cli put now cpu.usage
Error: metric-value pairs must come in pairs
```
+ Interactive 모드

```bash
# .bashrc 또는 .zshrc에 추가
export KRILL_SERVER="http://192.168.1.100:9090"
alias krill='krill-cli -server $KRILL_SERVER'

# Interactive 모드로 시작
$ krill
Krill CLI - Interactive Mode
Connected to: http://192.168.1.100:9090
Type 'help' for usage, 'exit' to quit

krill> put now cpu.usage 45.5
krill> query cpu.usage now-1h now
krill> exit
```

### 3. Interactive 모드에서 스크립트 실행

```bash
# 명령어를 파이프로 전달
echo -e "put now cpu.usage 45.5\nquery cpu.usage 0 9999999999\nexit" | \
  krill-cli -server http://localhost:9090

# 파일에서 명령어 읽기
cat << EOF > commands.txt
put now cpu.usage 45.5
put now memory.used 8192
query cpu.usage 0 9999999999
exit
EOF

cat commands.txt | krill-cli -server http://localhost:9090
```
## 명령어 별칭

더 짧은 명령어를 사용할 수 있습니다:

```bash
# query_range 대신 query 사용
krill-cli query cpu.usage 0 9999999999

# put 대신 write 사용
krill-cli write now cpu.usage 45.5
```

## 팁과 트릭

### 1. 쉘 스크립트와 통합

```bash
#!/bin/bash
# CPU 사용률을 1분마다 수집

while true; do
  CPU=$(top -bn1 | grep "Cpu(s)" | awk '{print $2}' | cut -d'%' -f1)
  krill-cli put now 'cpu.usage{host="'$(hostname)'"}' $CPU
  sleep 60
done
```

### 2. 환경 변수 사용

```bash
# .bashrc 또는 .zshrc에 추가
export KRILL_SERVER="http://192.168.1.100:9090"
alias krill='krill-cli -server $KRILL_SERVER'

# 사용
krill put now cpu.usage 45.5
krill query cpu.usage now-1h now
```

### 3. JSON 출력 파싱

```bash
# jq를 사용하여 서버 응답 직접 파싱
curl -s 'http://localhost:9090/api/v1/query_range?query=cpu.usage&start=0&end=9999999999' | \
  jq '.data.result[].values[] | @tsv'
```

## 문제 해결

### 서버에 연결할 수 없음

```bash
# 서버 상태 확인
curl http://localhost:9090/health

# 서버가 실행 중인지 확인
ps aux | grep krill-server
```

### 데이터가 조회되지 않음

```bash
# 메트릭 목록 확인
curl http://localhost:9090/api/v1/labels/__name__/values

# 시간 범위 확대
krill-cli query_range cpu.usage 0 9999999999
```

### 태그 파싱 오류

태그가 있는 메트릭은 반드시 인용 부호로 감싸야 합니다:

```bash
# ❌ 잘못된 예
krill-cli put now http_requests{method="GET"} 100

# ✅ 올바른 예
krill-cli put now 'http_requests{method="GET"}' 100
```

## 참고

- Krill TSDB 서버: [krill-server](../cmd/krill-server/)
- Prometheus API 호환성: krill-cli는 Prometheus API 형식을 따릅니다
- 메트릭 형식: Prometheus 메트릭 명명 규칙을 권장합니다
