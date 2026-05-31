# prx 구현 문서

> README의 설계 명세(§1–§16)를 **구현 가능한 단계**로 분해한 문서다. 결정된 모든 항목
> (D1–D11·O1–O3)을 코드 수준 작업으로 옮긴다. **런타임(프록시 경로) 외부 바이너리 의존 0**,
> presentation 포함 전부 직접 구현(stdlib + 수동 ANSI). 단 `prx trust`의 트러스트 스토어
> 등록만은 OS 네이티브 도구(`security`/`update-ca-trust`/`certutil`)를 1회성으로 호출한다.

## 0. 원칙·범위

- **언어:** Go (stdlib 중심). 빌드 타깃 macOS(arm64/amd64)·Linux. Windows 미지원.
- **의존 정책 (2계층, README §4 보강):**
  - **core** (프록시·TLS·CA·ACME·네트워크·보안): stdlib + `golang.org/x`만. 트러스트 스토어 등록은 `smallstep/truststore`(Apache-2.0)를 **`internal/truststore`로 vendoring** 하여 사용 — upstream 모듈 의존 0. **vendored 패키지는 prx 코드를 import하지 않는다(도메인 분리, 자립 라이브러리).** prx 고유 동작(보안 하더닝·로깅)은 제네릭 시드(seam)로 바깥에서 주입한다.
  - **presentation** (CLI 출력·테이블·색·로그): **직접 구현.** TTY 감지 + 수동 ANSI. 데이터/`--json` 경로 불간섭.
- **불변식:**
  - `--json`·파이프 출력에는 색·로그·부가 텍스트 금지(파이프 안전).
  - core 코드는 presentation에 의존하지 않는다(단방향).
  - `:443` 소유 프록시는 한 시점에 하나(데몬 또는 standalone).

### 패키지 레이아웃

```
prx/
  cmd/prx/main.go              # 엔트리포인트, 서브커맨드 디스패치
  internal/
    cli/                       # 커맨드 파싱, human/json 렌더, exit code
      output.go                # 표/색(TTY-gated)·JSON 인코더
    paths/                     # XDG/macOS 경로 해석
    config/                    # prx.toml 로드·탐색·머지
    registry/                  # registry.json: 락·원자적 쓰기·스키마 버전
    port/                      # 할당·liveness probe
    dns/                       # provider 인터페이스 + localhost/hosts
    ca/                        # 루트 CA 생성·leaf 발급 (trust 등록은 truststore에 위임)
    truststore/                # vendored smallstep/truststore (자립, prx 비의존, Apache-2.0)
    tlsprov/                   # provider 인터페이스 + internal/acme, SNI 캐시
    proxy/                     # 리버스 프록시 data plane·route table·hot reload
    daemon/                    # 라이프사이클·admin 소켓·서비스 매니저
    expose/                    # provider 인터페이스 + local/lan/cloudflared/tailscale
    logx/                      # slog 셋업·핸들러·access log·회전
  docs/
  skills/prx/SKILL.md          # agentskills.io 규격 skill 폴더 (자기 폴더에 격리)
  AGENTS.md
  justfile  .golangci.yml      # 명령 러너 + 린트 설정(v2, default:none)
  go.mod
```

### 의존 순서 (빌드 그래프)

```
paths → registry → config → port → ca → tlsprov → proxy → daemon
                              dns ─┘                  │
logx ─────────────────────────────────────────────────┤
cli ── 모든 패키지 위에서 조립
expose ── proxy/daemon 이후
```

각 Phase는 위 순서를 따른다. 한 Phase는 **컴파일 + 테스트 통과**를 완료 기준으로 한다.

### 테스트·린트 전략 (cross-cutting)

모든 Phase에 공통 적용. dev/CI 도구는 **빌드 산출물 의존이 아니므로** 의존 2계층(부록 B)과 무관.

**테스트:**

- 프레임워크: stdlib `testing`(table-driven). 보조로 `testing/quick`(속성 테스트), `net/http/httptest`.
- **`go test -race ./...` 필수** — atomic route table 스왑·registry `flock`·cert 캐시 등 동시성 경로가 많다.
- **프록시 e2e:** `httptest`로 TLS 핸드셰이크·WebSocket 업그레이드·SSE flush·502 분기·80→443 리다이렉트·비로프백 차단 검증. ephemeral 백엔드 기동.
- **CLI 출력:** golden file 스냅샷(human/json 양쪽), `-update` 플래그로 갱신. 파이프 시 색 제거 회귀 방지.
- **시스템 의존 격리:** trust(`security`/`certutil`)·`/etc/hosts`·sudo는 seam(`WithElevator`/`WithExecHook`) fake 주입으로 단위 테스트 → root/CI 권한 없이 로직 검증. 실제 OS 등록은 수동·통합 단계에서만.
- **커버리지:** `go test -cover`. core 패키지(registry·port·proxy·ca)에 임계 게이트.
- OS별 파일은 빌드 태그(`//go:build darwin` 등)로 분리 테스트.

**린트:** `gopls`(에디터) + `go vet` + `golangci-lint`(v2) + `govulncheck`. 포맷은 `gofmt`/`goimports` 체크(미정렬 CI 실패).

- **`golangci-lint` v2, `default: none` + 명시 enable** — `all`은 취향성 룰까지 켜져 AI가 엉뚱한 리팩터를 함. 명시 셋만.
  - enable: `govet` `staticcheck` `errcheck` `ineffassign` `unused` `errorlint` `nilnil` `bodyclose`
    `contextcheck` `exhaustive` `gocritic` `revive` `unparam` `nolintlint` **`gosec`**.
  - `gosec` 포함 이유: prx는 §12 보안 표면(`os/exec`·파일 권한 0600·hosts/ca 경로·sudo)이 실제로 커서 가치. 노이즈는 `//nolint:gosec` + `nolintlint`로 관리.
  - `revive`는 초반 약하게(스타일 강제 과하면 "규칙 맞추기 코드" 됨).
- **출력은 prx 규약(data=stdout / log=stderr)에 맞춘다:** 사람=text→stderr, AI/스크립트=json→stdout.
  ```bash
  golangci-lint run ./... --output.text.path=stderr --output.text.colors=false --output.json.path=stdout
  ```
- `govulncheck ./...` — 의존·stdlib 취약점을 *실제 호출 경로*까지 좁혀 보고(보안 도구라 게이트). JSON/SARIF 지원.

**명령 러너: `just`** (make 아님 — 빌드는 `go`가 하므로 incremental DAG 불요, just는 순수 명령 러너로 use case 일치). dev 도구지 배포 의존 아님.

```just
# justfile
test:
    go test -race ./...

lint:
    golangci-lint run ./...

# AI/CI용: stderr=사람, stdout=JSON diagnostics
lint-json:
    golangci-lint run ./... --output.text.path=stderr --output.text.colors=false --output.json.path=stdout

vuln:
    govulncheck ./...

check: test lint vuln
```

> AI 루프 권장: "코드 봐서 고쳐줘"가 아니라 "`just lint-json`의 JSON diagnostics 기준으로 고치고
> `just check` 통과까지 반복"이 안정적이다.

**CI (GitHub Actions):**

- matrix: macOS·Linux × 지원 Go 버전.
- 파이프라인: fmt 체크 → `go vet` → `golangci-lint` → `govulncheck` → `go test -race -cover ./...` → 4타깃 빌드(Phase 12). (로컬 `just check`와 동일 명령.)

### 테스트 매트릭스 (무엇을 검증하나)

각 패키지가 지켜야 할 불변식과 구체 케이스. "케이스 → 보장(불변식)" 형태.

**`paths` (Phase 0)**
- XDG 환경변수 set/unset × OS(darwin/linux) → 올바른 디렉터리(주입된 `GOOS`/env로 resolver 테스트).
- 디렉터리 생성 권한 `0700`.

**`registry` (Phase 1)**
- 고루틴 N개 동시 reserve → flock이 RMW 직렬화, **lost update 0**(각자 다른 포트 획득).
- tmp 작성 후 rename 전 크래시 모사 → 원본 `registry.json` 무손상; 깨진 tmp 무시.
- 스키마 vN → vN+1 마이그레이션 정확·idempotent.
- 동일 도메인 중복 reserve → 충돌 에러 + 점유 키 반환(D10).
- reserve/release/prune 상태 전이 정확.

**`config` (Phase 1)**
- 탐색: 중첩 디렉터리에서 CWD→상향 첫 `prx.toml` 정지; git 루트/`$HOME` 상한; `--config` override; 형제 미탐색(D6).
- 파싱: 유효/무효(잘못된 도메인·tls enum·`acme`인데 `acme_dns` 누락·서비스명 중복).
- **surgical 쓰기(1c·D3):** add는 블록 append하고 기존 라인·주석 **바이트 보존**; rm은 마커 블록만 제거; 편집 후 재파싱 유효; 무효 시 롤백.

**`port` (Phase 2)**
- 할당: 예약분 + OS 바인딩 포트 회피; 풀 소진 → 에러(D5).
- liveness: live 백엔드 → true, dead → false, 타임아웃 준수.
- `prx run`: `PORT` 주입; 자식 exit code 전파; SIGINT/SIGTERM 포워딩; stdio 패스스루(D4).
- `--write-env`: 기존 키 보존 upsert + 백업.

**`ca` / `tlsprov`(internal) (Phase 3)**
- 루트 CA: `IsCA`·KeyUsage·권한 `0600`/`0700`.
- leaf: SAN이 SNI와 일치, 루트로 체인 verify, 만료값.
- SNI 캐시: 동시 `GetCertificate`(race) 안전, hit/miss, 와일드카드.
- `prx ca export`: 파일 기록 + 지문 일치.

**`truststore`(vendored) (Phase 3c)**
- seam fake로: install/uninstall이 mac/linux/nss별 **기대 명령·인자**로 호출(실제 시스템 미접촉).
- NSS DB 탐색: fake FS의 프로필 발견; `certutil` 부재 → 경고(실패 아님).
- Elevator seam이 privileged op에 호출됨; 하더닝이 symlink/소유권 공격 대상 거부.

**`proxy` (Phase 4)**
- 라우트 테이블 atomic 스왑을 동시 요청 중 수행(race) → torn read·drop 커넥션 0.
- host 매칭 → upstream; 무매칭 → 404; 매칭+dead → 502 + 안내; 매칭+live → 프록시.
- 프로토콜 통과: WebSocket echo, SSE 스트리밍 flush, HTTP/2.
- `:80` → 동일 호스트 `https` 301.
- 비-loopback 요청: 미노출 라우트 → 403; 노출 라우트 → 허용.
- graceful shutdown이 in-flight drain.

**`cli`/`output` (Phase 5)**
- 커맨드별 human/json golden 스냅샷.
- 비-TTY(파이프) → 색·기호 제거.
- exit code 0/1/2/3/4 시나리오별.
- `--json` 에러 봉투를 stderr로.

**`dns` (Phase 6)**
- localhost 모드 = no-op.
- hosts 블록: idempotent add/remove, 외부 라인 보존, 마커 경계, symlink/소유권 하더닝 거부, 비-sudo → exit 3.

**`daemon` (Phase 7)**
- admin 소켓: `PUT /routes` → 테이블 스왑 반영; status/reload.
- up 흐름: 데몬 live → push; 없음 → 포그라운드.
- 공존: 데몬 상주 + `up --foreground` → exit 4; `--standalone` 우회(D1).
- 소켓 권한 `0600`. (launchd/systemd 기동은 통합·수동 단계.)

**`logx` (Phase 8)**
- human 핸들러 TTY 색 / 비-TTY 평문.
- JSON 핸들러 → 유효 JSONL 스키마.
- access 로그 토글, 필드 존재.
- 회전: size 경계에서 `.1`→`.2` 시프트.

**`tlsprov`(acme) (Phase 9)**
- 단위: fake DNS provider로 TXT set/clear; 갱신 트리거(주입 clock, 잔여 <30d).
- 통합(게이트): LE staging 실발급 e2e.

**`expose` (Phase 10)**
- provider 인터페이스 적합성; `lan`은 `.local` 제약(D9); `--auth` 강제(자격 없으면 401); 노출 라우트 loopback 예외(D11).

---

## Phase 0 — 스캐폴드

**목표:** 빌드되는 빈 골격 + 경로 규약.

**산출물:** `go.mod`, `cmd/prx/main.go`, `internal/paths`, `AGENTS.md`.

**단계:**

1. `go mod init prx`. Go 1.22+ (slog·`http.ServeMux` 향상 활용).
2. `cmd/prx/main.go`: `os.Args` 기반 서브커맨드 디스패치 표(맵: name→handler). 표준 `flag`만 사용.
3. `internal/paths`:
   - `ConfigDir()` → `$XDG_CONFIG_HOME/prx` (기본 `~/.config/prx`).
   - `DataDir()`  → `$XDG_DATA_HOME/prx` (기본 `~/.local/share/prx`).
   - `StateDir()` → Linux `$XDG_STATE_HOME/prx`(기본 `~/.local/state/prx`), macOS `~/Library/Logs/prx`(`XDG_STATE_HOME` 설정 시 우선).
   - `RuntimeDir()` → admin 소켓 위치(`ConfigDir()/prx.sock`).
   - OS 분기는 `runtime.GOOS`. 디렉터리 생성은 `os.MkdirAll(dir, 0o700)`.
4. **`AGENTS.md`(루트, dev-facing):** prx **코드베이스에서 작업하는 에이전트**용 기여 가이드.
   반드시 아래를 **명시**한다(에이전트가 그대로 실행).
   - 1줄 개요 + `README.md`·`docs/IMPLEMENTATION.md` 포인터.
   - **명령 러너 = `just`** (전제: `just` 설치 필요). 모든 작업은 `go` 직접 호출보다 just 레시피로:
     - `just test` → `go test -race ./...`
     - `just lint` → `golangci-lint run ./...`
     - `just lint-json` → JSON diagnostics(stdout) + 사람용 text(stderr)
     - `just vuln` → `govulncheck ./...`
     - `just check` → test·lint·vuln 일괄 (**PR 전 통과 필수**)
     - 빌드 `go build ./cmd/prx`.
   - **AI 작업 루프 명시:** "코드 보고 고쳐줘" 금지. `just lint-json`의 JSON을 기준으로 수정하고
     **`just check`가 green일 때까지 반복**.
   - **린트 도구체인:** `golangci-lint` v2(`.golangci.yml`, `default: none` + 명시 enable),
     `govulncheck`, `gosec`(보안 표면). `//nolint`는 사유 주석 필수(`nolintlint`).
   - 패키지 맵(`internal/*` 역할).
   - 컨벤션: stdlib 우선, 의존 2계층(core/presentation), 도메인 분리, vendored `truststore` 수정 규칙(prx import 금지), 출력 규약(data=stdout/log=stderr).
   - **금지:** prx 사용법 서술(그건 `skills/prx/SKILL.md` 소관 — 중복 방지).

**검증:** `prx --version`·`prx --help` 동작. `paths` 단위 테스트(환경변수 set/unset 케이스).

---

## Phase 1 — 설정·레지스트리 (D3·D6·동시성)

**목표:** `prx.toml` 로드/탐색 + `registry.json` 락·원자적 쓰기·스키마 버전.

**산출물:** `internal/config`, `internal/registry`.

### 1a. config (TOML)

1. TOML 파서: stdlib에 없음 → `pelletier/go-toml/v2` 1개 허용(presentation 계열, core 아님). 순수 stdlib 의존, 활발 유지보수, `encoding/json` 스타일 API.
   - **읽기·검증 전용.** Unmarshal로 struct 매핑 + 유효성 검사에만 쓴다.
   - **주의:** v2 `Marshal`은 주석·포맷을 보존하지 않는다(v1의 document AST 제거됨). 따라서 `prx.toml` **쓰기에는 marshal 왕복을 쓰지 않는다**(아래 1c).
2. 타입:
   ```go
   type Project struct {
     Name     string
     Services map[string]Service   // key = 서비스명
   }
   type Service struct {
     Domain   string
     Port     int      // 0 = 자동 할당
     TLS      string   // "internal"(기본)|"acme"
     ACMEDNS  string   // acme provider key
   }
   ```
3. **탐색(D6):** CWD에서 위로 올라가며 첫 `prx.toml` 정지. 상한 = git 루트(`.git` 발견) 또는 `$HOME`. `--config` override. 형제 디렉터리 미탐색.
4. 검증: 도메인 형식, TLS 값 enum, acme면 `acme_dns` 필수, 서비스명 중복 불가.

### 1c. prx.toml 쓰기 — 주석 보존 (D3)

`prx add`/`rm`이 `prx.toml`을 편집하되 **사용자 주석·포맷을 보존**해야 한다. marshal 왕복은
주석을 날리므로 금지. `/etc/hosts` 편집(Phase 6)과 같은 **surgical 블록 편집**으로 처리한다.

1. 각 서비스 블록을 마커 주석으로 식별: `[services.<name>]` 헤더 + 선택적 `# prx:managed` 태그.
2. **추가:** 파일 끝에 `[services.<name>]` 블록을 append. 기존 라인 불변.
3. **삭제:** 해당 `[services.<name>]` 헤더부터 다음 테이블 헤더(또는 EOF) 직전까지 라인 제거.
   사용자가 직접 쓴 다른 블록·주석은 불변.
4. 편집 후 temp+rename 원자적 교체 + `pelletier/go-toml/v2`로 재파싱해 유효성 확인. 깨지면 롤백.

> 손편집 블록과 명령형 추가 블록이 한 파일에 공존하되, prx는 자기 마커 블록만 안전하게 다룬다.

### 1b. registry (JSON, 도구 전용)

1. 스키마:
   ```go
   type Registry struct {
     Version  int                      // 마이그레이션용
     Services map[string]Reservation    // key = "project/service"
   }
   type Reservation struct {
     Project, Service, Domain string
     Port int
     TLS, DNS string
     Adhoc bool   // projectless 추가
   }
   ```
2. **동시성:** read-modify-write 전체를 advisory 파일 락으로 감쌈.
   - `flock(2)` (`golang.org/x/sys/unix`) 또는 별도 lockfile + `O_CREATE|O_EXCL` 폴백.
   - 패턴: `Lock() → Read() → mutate → WriteAtomic() → Unlock()`.
3. **원자적 쓰기:** temp 파일(`registry.json.tmp.<pid>`)에 기록 → `fsync` → `os.Rename`(원자적).
4. **스키마 버전:** 로드 시 `Version` 확인, 낮으면 마이그레이션 함수 체인 적용 후 재기록.
5. **도메인 유일성(D10):** reserve 시 동일 도메인이 다른 키에 있으면 충돌 에러(exit 4) + 점유 키 반환.

**검증:** 동시 쓰기 레이스 테스트(고루틴 N개 reserve), 크래시 후 파일 무결성(temp+rename), 버전 마이그레이션 테스트.

---

## Phase 2 — 포트 관리 (D4·D5)

**목표:** 자동 할당 + liveness probe.

**산출물:** `internal/port`.

**단계:**

1. **할당 풀(D5):** 기본 `4300–4999`. config로 변경 가능(`[ports] min/max`).
2. **할당 알고리즘:**
   - 후보 = 풀 순회. 제외 = ① 레지스트리 예약 포트 ② 현재 OS 바인딩 포트.
   - OS 바인딩 probe: `net.Listen("tcp", "127.0.0.1:p")` 성공 후 즉시 close → 비어있음 판정(best-effort).
   - 첫 빈 포트를 reserve(Phase 1b 경유, 락 안에서).
3. **liveness probe (활성 판정):** `net.DialTimeout("tcp", "127.0.0.1:port", 300ms)` 성공 = live. `prx ls` STATUS·프록시 502 분기에 사용.
4. **PORT 주입(D4):**
   - `prx run <svc> -- <cmd...>`: 포트 조회/할당 → `exec.Command`에 `Env = append(os.Environ(), "PORT="+p)` → `cmd.Run()` (stdio 패스스루, 시그널 전파).
   - `--write-env`: opt-in 시 프로젝트 `.env`에 `PORT=` upsert(기존 키 보존, 백업). 기본 비활성.

**검증:** 풀 소진 시 에러, 예약/라이브 포트 회피, probe 정확도, `prx run` 시그널·exit code 전파.

---

## Phase 3 — TLS internal (CA·leaf·SNI)

**목표:** 로컬 CA 자가발급 + 도메인별 leaf + SNI 동적 발급 + trust store 등록.

**산출물:** `internal/ca`, `internal/tlsprov`(provider 인터페이스 + internal 구현).

### 3a. 루트 CA

1. 키 생성: `ecdsa.GenerateKey(elliptic.P256())` 또는 ed25519. 루트는 장수명(10y).
2. 자가서명 인증서: `x509.CreateCertificate`, `IsCA=true`, `KeyUsageCertSign|CRLSign`.
3. 저장: `DataDir()/ca/root.key`(0600), `root.crt`(0644). 디렉터리 0700.
4. **보안(README §12):** 키 파일 권한·소유 검증, 심볼릭 링크 거부.

### 3b. leaf 발급 (SNI 동적)

1. `tls.Config.GetCertificate(hello)`: SNI 호스트명 → 캐시 조회 → 없으면 즉석 발급.
2. leaf: 단수명(예 90d), SAN = 요청 도메인, 루트 CA로 서명.
3. **캐시:** `map[string]*tls.Certificate` + `sync.RWMutex`. hot reload 시 안전(Phase 4).
4. 와일드카드 옵션: `*.<proj>.localhost` leaf로 발급 수 절감(선택).

### 3c. trust store (`prx trust`) — vendored, 도메인 분리

`smallstep/truststore`(Apache-2.0)를 `internal/truststore`에 **자립 라이브러리**로 vendoring한다.
**원칙: "라이브러리 업그레이드" 개념** — vendored 패키지는 prx 코드를 일절 import하지 않고
(stdlib + `howett.net/plist`만), prx 고유 동작은 제네릭 시드로 바깥에서 주입한다.

**vendoring 절차 (하우스키핑):**

1. 복사: `truststore.go`·`truststore_darwin.go`·`truststore_linux.go`·`truststore_nss.go`·`errors.go`.
   드롭: `truststore_windows.go`·`_freebsd`·`_others`·`_java`(+ 코어의 `WithJava` 참조).
2. `go.mod`/`go.sum` 제거(internal 패키지화), import 경로 정리. 의존 `howett.net/plist`는 prx `go.mod`로 흡수.
3. `LICENSE`(Apache-2.0 원본) + `NOTICE`(출처 URL·커밋·변경 내역) — Apache-2.0 의무.

**라이브러리측 편집 (제네릭 개선, prx 비의존):**

| 편집 | 내용 | 성격 |
| ---- | ---- | ---- |
| `WithLogger` 시드 추가 | `debug()`의 `log.Printf`를 주입된 logger(`*log.Logger`/`io.Writer`)로 라우팅 | 제네릭 |
| `WithElevator`(or `WithExecHook`) 시드 추가 | privileged 명령(`sudo tee` 등) 실행 방식을 호출자가 주입 가능하게 | 제네릭 |
| 플랫폼 트림 | windows·freebsd·others·java 제거 | 범위 |
| (선택) darwin plist 재구현 | trust-settings 댄스 제거 → `howett.net/plist` 탈락 | 의존 축소 |

**prx측 통합 (truststore 밖, `internal/ca`·`cli`에서):**

- 옵션 전달만(소스 편집 X): `WithPrefix("prx")`, **`WithFirefox()`**(필수 — NSS 등록은 이 뒤),
  `WithDebug`↔`--verbose`, 필요 시 `WithNoSystem`.
- `§12 권한 하더닝`(sudo 전 소유·심볼릭 링크 검증)을 **Elevator로 구현해 `WithElevator`로 주입.**
- `logx`(slog) 로거를 **`WithLogger`로 주입** → 데몬 로그 일관·`--json` stdout 오염 방지.

**vendored 코드가 수행하는 동작 (검증 대상):**

- **macOS:** cgo 없이 `security add-trusted-cert`(System keychain) + trust-settings plist 갱신.
  제거 `security remove-trusted-cert`. keychain 프롬프트 1회.
- **Linux 시스템 스토어:** distro 감지 후 anchor 복사 + 갱신
  (Debian `update-ca-certificates` / RHEL `update-ca-trust` / Arch `trust`). `/usr`·`/etc`
  쓰기라 sudo 필요(exit 3 안내). **sudo 호출은 주입된 Elevator 경유.**
- **NSS(Firefox·Chrome-Linux):** `certutil -A -d sql:<db> -t "C,," -n <nick> -i root.crt`,
  알려진 DB 순회(`~/.pki/nssdb`, Firefox 프로필 glob, `cert9.db`→`sql:`/`cert8.db`→`dbm:`).
  `certutil` 없으면 경고 + 수동 안내(실패 아님).

**`prx ca export [--out]`(D9):** `root.crt`를 지정 경로로 복사 + SHA-256 지문 출력(타기기 설치용).

> **plist 의존 결정:** (a) `howett.net/plist` 유지(저위험, 빠름) — 기본. (b) darwin 댄스 제거로
> howett 탈락(trust-eval 미묘차 위험, 테스트 필요). 1차 (a) → 이후 (b) 시도.

> **도메인 분리 핵심:** truststore는 `io.Writer`·exec 추상만 안다. prx 보안정책·로깅·경로는
> 시드로 주입 → 라이브러리는 재사용 가능 상태 유지, prx 도메인 안 섞임.

> NSS 탐색이 엣지케이스 밀집 지점 — 보안 경로이므로 등록 결과를 재검증(해당 DB에서 verify)하고
> 부분 실패는 사용자에게 명시한다.

### 3d. provider 인터페이스

```go
type Provider interface {
  // SNI 핸드셰이크용 인증서 반환
  GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error)
  // 서비스 추가/갱신 시 사전 준비(발급·검증)
  Ensure(ctx context.Context, domain string) error
}
```

internal·acme 둘 다 이 인터페이스 구현. 서비스별 `tls` 값으로 선택.

**검증:** 발급 leaf 체인 검증(루트로 verify), SNI 다중 도메인, 캐시 동시성, trust 후 `curl https://x.localhost` 경고 없음.

---

## Phase 4 — 리버스 프록시 data plane (D1·D2)

**목표:** `:443`/`:80` 서빙 + route table + 무중단 hot reload + 502/리다이렉트/비로프백 차단.

**산출물:** `internal/proxy`.

**단계:**

1. **route table:**
   ```go
   type Route struct {
     Domain   string
     Upstream string   // "127.0.0.1:4310"
     TLS      tlsprov.Provider
   }
   type Table struct{ routes map[string]*Route }  // key = host
   ```
   `atomic.Pointer[Table]`로 보관 → **hot reload(D2)**: 새 Table 만들어 `Store`. 기존 커넥션 무중단.
2. **핸들러:** Host 헤더 → Table 조회.
   - 매칭 없음 → 404.
   - 매칭 + upstream 비활성(liveness fail) → **502 + "서비스 미기동" 안내**(README §9).
   - 매칭 + 활성 → `httputil.NewSingleHostReverseProxy` 위임.
3. **프로토콜 통과:** `httputil.ReverseProxy`는 HTTP/2·WebSocket·SSE·HMR 통과. `FlushInterval=-1`(SSE 즉시 flush), `Transport`에 keep-alive. WebSocket 업그레이드는 stdlib가 처리(hijack).
4. **TLS 서버:** `http.Server{TLSConfig: &tls.Config{GetCertificate: provider.GetCertificate}}` on `:443`. `ListenAndServeTLS("","")`(인증서는 콜백 경유).
5. **`:80` 핸들러(README §3):** 평문 → 동일 호스트 `https://` **301 리다이렉트**. HSTS 미적용.
6. **비로프백 차단(README §12):** `RemoteAddr` 비-loopback이고 해당 라우트가 expose 안 됨 → 거부(403). expose된 라우트만 예외.
7. **graceful:** reload·종료 시 `http.Server.Shutdown(ctx)`로 in-flight drain.

**검증:** WebSocket/SSE 통과 e2e, reload 중 기존 커넥션 유지, 502 분기, 80→443 리다이렉트, 비로프백 거부.

---

## Phase 5 — CLI 표면·출력 규약 (출력 §13)

**목표:** 핵심 커맨드 + human/json 렌더 + exit code.

**산출물:** `internal/cli`, `internal/cli/output.go`.

**커맨드(이 Phase 범위):** `up`·`down`·`ls`·`run`·`add`·`rm`·`prune`·`port`.

**단계:**

1. **디스패치·플래그:** 각 커맨드 `flag.FlagSet`. 공통 `--json`.
2. **출력 분리(파이프 안전):** 데이터=stdout, 로그·진행=stderr.
3. **human 렌더(`output.go`):** 수동 정렬 표(`text/tabwriter` stdlib). 색·기호(● live/○ down)는 **TTY일 때만**(`term.IsTerminal` via `golang.org/x/term`, `NO_COLOR` 존중). 비-TTY면 평문.
4. **json 렌더:** 단일 객체/배열만. `encoding/json`, 부가 텍스트 0.
5. **exit code:** `0` 성공 / `1` 일반 / `2` 사용법 / `3` 권한(sudo) / `4` 포트·도메인 충돌. 에러 봉투:
   ```json
   { "error": { "code": "port_conflict", "message": "..." } }
   ```
   (stderr, `--json` 시).
6. **커맨드 동작 매핑:**
   - `up`: config 로드 → 각 서비스 reserve/할당 → DNS 반영(Phase 6) → 라우트 push(데몬) 또는 포그라운드 기동(Phase 7) → 결과 출력.
   - `down`: 현재 프로젝트 라우트 비활성(예약 보존).
   - `ls`: 레지스트리 + liveness 조합 → 표/JSON.
   - `add/rm`: prx.toml(주석 보존 surgical 편집, Phase 1c) + 레지스트리 동시 갱신(D3).
   - `prune`: prx.toml 사라진 프로젝트 예약 GC.
   - `port`: 숫자만 stdout(개행) / `--json` 객체.
   - `run`: Phase 2 주입 exec.

**검증:** 각 커맨드 human/json 출력 스냅샷, exit code, 파이프 시 색 제거.

---

## Phase 6 — DNS provider (모드 A/B)

**목표:** `.localhost` 자동 + `/etc/hosts` 블록 안전 편집.

**산출물:** `internal/dns`.

**인터페이스:**
```go
type Provider interface {
  Ensure(domain string) error   // 해석되게 보장
  Remove(domain string) error
}
```

**단계:**

1. **모드 A `localhost`:** `*.localhost`는 OS/브라우저가 127.0.0.1 해석 → **no-op** provider. 권한 0.
2. **모드 B `hosts`:**
   - 관리 블록 마커: `# >>> prx managed >>>` … `# <<< prx managed <<<` 사이만 편집.
   - 항목: `127.0.0.1  <domain>      # prx:<proj>:<svc>`.
   - **권한(sudo):** 편집 전 `/etc/hosts` 소유·심볼릭 링크 검증(README §12). temp+rename 원자적 교체. 권한 부족 시 exit 3.
3. **모드 판별:** 도메인이 `.localhost`로 끝나면 A, 아니면 B. `--dns localhost|hosts` override.

**검증:** 블록 idempotent add/remove, 외부 라인 보존, 심볼릭 링크 공격 거부, 비-sudo 시 exit 3.

---

## Phase 7 — 데몬·admin 소켓 (D1·D2)

**목표:** 상주 데몬 + unix 소켓 IPC + 핫 reload + 서비스 매니저 + 공존 규칙.

**산출물:** `internal/daemon`.

**단계:**

1. **admin 소켓:** `net.Listen("unix", RuntimeDir()/prx.sock)`, 권한 0600. JSON-RPC 류 간단 프로토콜:
   - `PUT /routes` (라우트 push → 핫 reload), `GET /status`, `POST /reload`, `GET /logs`.
2. **`prx up` 흐름(D1):**
   - 데몬 live(`/status` 응답) → 소켓으로 라우트 push → 데몬이 Table 스왑(Phase 4).
   - 데몬 없음 → 그 자리에서 on-demand 포그라운드 프록시 기동(`--foreground` 묵시).
3. **공존(D1):** 데몬 상주 중 `prx up --foreground`는 `:443` 충돌 → **exit 4**. `--standalone`만 강제 독립 기동.
4. **서비스 매니저:**
   - macOS: `launchd` plist 생성(`~/Library/LaunchAgents/io.prx.plist`), `StandardErrorPath`로 로그 리다이렉트. `prx daemon start`가 `launchctl load`.
   - Linux: `systemd` user unit(`~/.config/systemd/user/prx.service`), 로그는 journald. `systemctl --user`.
5. **`:443`/`:80` 바인딩:** 데몬 기동 시 권한 필요(README §12) → 서비스 매니저에 위임 권장.
6. **lifecycle:** `start`·`stop`·`status`·`logs`. 종료 시 `Shutdown` graceful drain.

**검증:** push→reload 무중단, 공존 충돌 exit 4, launchd/systemd 기동·재부팅 자동기동, 소켓 권한.

---

## Phase 8 — 로깅 (O2)

**목표:** 3종 로그 + JSONL + 회전. **직접 구현**(stdlib `slog` 기반).

**산출물:** `internal/logx`.

**단계:**

1. **인터페이스 = stdlib `log/slog`.** 핸들러 2종:
   - **human 핸들러(직접 구현):** `slog.Handler` 구현체. TTY면 색·레벨 컬러, 비-TTY면 평문 logfmt.
   - **JSON 핸들러:** stdlib `slog.NewJSONHandler` 그대로 → JSONL.
   - 선택: `--log-format json` 또는 `PRX_LOG=json`.
2. **레벨:** info 기본, `--verbose`=debug.
3. **출력 위치:**
   - 포그라운드 → stderr.
   - 서비스 매니저 데몬 → stdout/stderr (journald/launchd 캡처).
   - manual 데몬 → `StateDir()/prx.log`.
4. **접근 로그(access):** 기본 **off**. `prx up --access-log` 또는 toml `[log] access=true`. 프록시 미들웨어가 요청당 JSONL 1줄:
   ```json
   {"ts":"...","host":"...","method":"GET","path":"/api","status":200,
    "dur_ms":12,"upstream":"127.0.0.1:4310","bytes":3344,"proto":"h2"}
   ```
   파일 `StateDir()/access.log`. `prx daemon logs --access -f`로 tail.
5. **회전(직접 구현):** size 기반(기본 10MB×3). 쓰기 전 크기 확인 → 초과 시 `prx.log.1`→`.2` 시프트. 외부 logrotate 의존 0.

**검증:** human/json 전환, TTY/파이프 색, access 토글, 회전 경계, `daemon logs` tail.

---

## Phase 9 — ACME (D8) + DNS-01 provider

**목표:** 실인증서 발급(DNS-01) + 갱신 루프 + staging.

**산출물:** `internal/tlsprov`(acme 구현), `internal/dns` 확장(레코드 API).

**단계:**

1. **ACME 클라이언트:** `golang.org/x/crypto/acme`. account 키 생성·저장(`DataDir()/acme/`).
2. **DNS-01 챌린지:** provider HTTP API 직접 호출(stdlib `net/http`). 인터페이스:
   ```go
   type DNSProvider interface {
     SetTXT(domain, value string) error
     ClearTXT(domain, value string) error
   }
   ```
   1차: `cloudflare`(토큰 환경변수/config). 이후 플러그인 추가.
3. **발급 흐름:** authorize → TXT 레코드 set → 전파 대기(polling) → finalize → 인증서 저장 → SNI 캐시 주입.
4. **갱신(D8):** 데몬 백그라운드 goroutine, 만료 잔여 <30d 재발급. on-demand는 `prx up` 시 기회적 갱신.
5. **staging:** `--acme-staging` → Let's Encrypt 스테이징 디렉터리(rate limit 회피).
6. **인바운드 포트 불필요**(DNS-01): README §7 유지.

**검증:** staging 환경 e2e 발급, TXT set/clear, 갱신 트리거, 만료 임계.

---

## Phase 10 — expose provider (D9·D11)

**목표:** 외부 노출 추상화 + 4 provider + 인증.

**산출물:** `internal/expose`.

**인터페이스:**
```go
type Provider interface {
  Expose(ctx context.Context, route Route, opts Opts) (publicURL string, err error)
  Close() error
}
```

**단계:**

1. `local`(기본): no-op, 내장 CA.
2. `lan`: **mDNS 광고(직접 구현 or 경량)** → 도메인 `<name>.local` 한정(D9). 타기기는 `prx ca export` CA 설치.
3. `cloudflared`: cloudflared 터널 API/프로세스 연동. 공개 URL 발급.
4. `tailscale`: tailscale serve/funnel 연동.
5. **인증(D11):** 기본 경고 출력. `--auth` → 프록시 레벨 basic auth(또는 cloudflared Access). expose된 라우트만 비로프백 예외(Phase 4 연동).

> mDNS·cloudflared·tailscale 연동 세부는 provider별 하위 문서로 분리 가능. 인터페이스만 먼저 고정.

**검증:** provider 교체, lan `.local` 해석, `--auth` 강제, expose 라우트 비로프백 허용.

---

## Phase 11 — AI 친화 (O3)

**목표:** skill 배포 위임 + 진입점.

**산출물:** `skills/prx/SKILL.md`, `prx skill path`.

> `AGENTS.md`(dev-facing 기여 가이드)는 별개 도메인이라 Phase 0 산출물이다 — 여기엔 prx *사용*
> skill만 둔다.

**단계:**

1. **`skills/prx/SKILL.md`:** agentskills.io 규격 skill **폴더**. frontmatter `name`+`description` 필수.
   prx 명령·`--json` 스키마·예시 수록. 루트가 아니라 `skills/prx/`에 격리(도메인 분리 — repo 루트는
   순수 Go 프로젝트). skills.sh가 `skills/<name>/SKILL.md` 평면 레이아웃을 스캔하므로 자동 인식.
2. **배포 위임:** 자체 인스톨러 없음. 두 매니저 모두 repo의 skill 폴더에서 설치.
   - skills.sh: `npx skills add jinyongp/prx`.
   - apm: `apm install jinyongp/prx`. (apm.yml은 *소비자 프로젝트*가 의존성 선언용으로 두는 파일이지
     prx repo의 산출물이 아니다.)
3. **`prx skill path`:** 동봉 `skills/prx/SKILL.md` 경로만 stdout 출력(수동 설치·디버그용).

**검증:** skills.sh/apm 설치 e2e, `prx skill path` 출력.

---

## Phase 12 — 패키징·릴리스

**목표:** 크로스컴파일·배포.

**단계:**

1. `GOOS=darwin/linux × GOARCH=arm64/amd64` 4종 빌드.
2. 릴리스 자동화(goreleaser 또는 직접 스크립트). 버전 `-ldflags -X main.version`.
3. 설치 경로: Homebrew tap(macOS) + `curl | sh` 스크립트(Linux).
4. CI: 「테스트·린트 전략」 파이프라인 + 릴리스 시 4타깃 빌드·아티팩트 업로드.

**검증:** 4 타깃 바이너리 실행, 버전 임베드, 설치 스크립트.

---

## 부록 A. 결정 ↔ Phase 추적표

| 결정 | 내용 | Phase |
| ---- | ---- | ----- |
| D1 | 데몬↔on-demand 공존·admin 소켓 | 7 |
| D2 | 핫 reload | 4·7 |
| D3 | `prx.toml` 단일 진실·add/rm 동기 | 1·5 |
| D4 | PORT 주입(`prx run`) | 2 |
| D5 | 할당 풀 4300–4999 | 2 |
| D6 | config 상향 탐색 | 1 |
| D7 | XDG·macOS 경로 | 0 |
| D8 | ACME 갱신·staging | 9 |
| D9 | lan=mDNS `.local`·`ca export` | 3·10 |
| D10 | 도메인 전역 유일성 | 1 |
| D11 | expose 인증/경고 | 10 |
| O1 | Windows 미지원 | — (범위 제외) |
| O2 | 로그 포맷 | 8 |
| O3 | skill 배포 위임 | 11 |

## 부록 B. 외부 의존 목록 (확정)

| 의존 | 계층 | 용도 | 적용 범위 |
| ---- | ---- | ---- | -------- |
| `golang.org/x/crypto/acme` | core | ACME | acme TLS 한정 |
| `golang.org/x/sys/unix` | core | flock·시그널 | 전역 |
| `golang.org/x/term` | presentation | TTY 감지 | CLI 출력 |
| `pelletier/go-toml/v2` | presentation | prx.toml 읽기·검증(쓰기는 surgical) | config 한정 |
| `howett.net/plist` | core | macOS trust-settings plist | internal TLS·macOS 한정, **제거 후보(Phase 3c (b))** |

> `smallstep/truststore`는 모듈 의존이 아니라 `internal/truststore`로 **vendoring**(Apache-2.0,
> 자립·prx 비의존)한다 → upstream 모듈 의존 0(Phase 3c). prx 동작은 시드(`WithLogger`/`WithElevator`)로
> 주입하며 라이브러리에 prx import는 없다. vendored darwin 경로가 `howett.net/plist`를 끌어오며,
> 이는 trust-settings 댄스를 직접 구현하면 제거 가능하다.

> 그 외 프록시·TLS·CA·라우팅·로그·테이블·회전은 모두 stdlib + 직접 구현.

## 부록 C. MVP 경로 (최소 동작)

가장 빠른 "신뢰 HTTPS" 체험까지: **Phase 0 → 1 → 2 → 3(internal만) → 4 → 5(up/ls/run) → 6(localhost 모드만)**.
이후 7(데몬)·8(로그)·9(acme)·10(expose)·11(skill)·12(릴리스)를 순차 확장.
