# prx CLI/TUI 개선 — 구현 계획(IMPL)

> 기획 배경·범위는 [plan.md](./plan.md) 참고. 이 문서는 Phase별 구현을 파일·시그니처
> 수준까지 정의한다. 모든 Phase는 독립 PR로 머지 가능하도록 구성한다.

> **상태:** 구현 설계. 코드 미작성. 인용한 시그니처는 현재 코드 기준이며 구현 시 재확인.

---

## 0. 공통 기반 (Foundation)

모든 Phase가 공유하는 규약·헬퍼·구조. Phase 1 PR에 포함한다.

### 0.1 의존성 추가 정책

Charm 스택은 [docs/spec/impl.md 부록 B](../spec/impl.md) 개정으로 presentation 계층에 허용된다.
**core(proxy·TLS·CA·network·daemon)는 stdlib + `golang.org/x`만 유지하고 TUI 의존을 import하지 않는다**(§0.2 불변식). Phase 단위로 `go.mod`에 추가한다(미사용 의존 선반입 금지).

| Phase | 추가 모듈 | 비고 |
|---|---|---|
| 1 | `github.com/charmbracelet/lipgloss` | termenv·uniseg·x/text(기존) transitive |
| 2 | `github.com/charmbracelet/bubbletea`, `github.com/charmbracelet/bubbles` | |
| 3 | `github.com/lrstanley/bubblezone` | |
| 4 | `github.com/NimbleMarkets/ntcharts` | |

각 PR에서 `go mod tidy` 후 `go.sum` 동결. CI의 `govulncheck`(현재 `@v1.3.0`,
[.github/workflows/ci.yml](../../.github/workflows/ci.yml))가 신규 의존을 스캔한다.

### 0.2 렌더 게이트 (단일 판정)

리치 출력 진입점을 하나로 통일한다. 기존 `isTTY` ([internal/cli/output.go](../../internal/cli/output.go))를 재사용한다.

`internal/cli/output.go`에 추가:

```go
// richOut reports whether w should receive styled (non-plain) output.
// It is the single gate for all Phase-1 styling.
func richOut(w io.Writer, jsonOut bool) bool {
	return !jsonOut && isTTY(w) // isTTY already honors NO_COLOR + terminal check
}

// interactive reports whether a full bubbletea program may take over the
// terminal: both stdin and stdout must be TTYs.
func interactive(stdout io.Writer) bool {
	out, ok := stdout.(*os.File)
	if !ok || !term.IsTerminal(int(out.Fd())) {
		return false
	}
	return term.IsTerminal(int(os.Stdin.Fd()))
}
```

**불변식:** `--json`이거나 non-TTY이거나 `NO_COLOR`면 리치/인터랙티브 경로 진입 금지.
core 패키지(proxy·TLS·CA·network·daemon)는 `internal/ui`·`internal/tui`를 import하지 않는다(부록 B 개정).

### 0.3 패키지 구조

| 패키지 | 역할 | 도입 Phase |
|---|---|---|
| `internal/ui` | lipgloss 팔레트·공용 스타일·렌더 헬퍼. **lipgloss만 import**(사이클 없음) | 1 |
| `internal/cli` | 기존. Phase 1에서 `internal/ui` 사용 | 1 |
| `internal/tui` | bubbletea 모델/프로그램. `prx top`·진행 UI·picker | 2 |
| `internal/view` | 서비스 행 빌더(registry+port). cli·tui 공유, import 사이클 회피 | 2 |
| `internal/metrics` | 트래픽 집계(ring buffer)·admin /metrics | 4 |

`internal/ui`를 cli와 tui가 공유해 스타일 단일 출처를 유지한다.

### 0.4 색 팔레트 (`internal/ui/style.go`)

```go
package ui

import "github.com/charmbracelet/lipgloss"

var (
	Brand   = lipgloss.AdaptiveColor{Light: "#5A3FD6", Dark: "#9D86FF"}
	Success = lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#3FB950"}
	Muted   = lipgloss.AdaptiveColor{Light: "#6E7781", Dark: "#8B949E"}
	Warn    = lipgloss.AdaptiveColor{Light: "#9A6700", Dark: "#D29922"}
	Danger  = lipgloss.AdaptiveColor{Light: "#CF222E", Dark: "#F85149"}

	Header = lipgloss.NewStyle().Bold(true).Foreground(Brand)
	Dim    = lipgloss.NewStyle().Foreground(Muted)
)
```

`AdaptiveColor`는 터미널 배경(light/dark)에 자동 대응한다. `NO_COLOR` 시 lipgloss가
escape를 생략한다(추가로 게이트에서도 차단).

---

## Phase 1 — lipgloss 출력 미화

**목표:** one-shot 출력의 TTY 경로만 lipgloss로 교체. 플레인(파이프/`--json`/`NO_COLOR`)
경로는 **바이트 동일** 유지. 인터랙션·신규 명령 없음.

**의존성:** lipgloss (+ `internal/ui` 신설).

### 1.1 신규/변경 파일

| 파일 | 변경 |
|---|---|
| `internal/ui/style.go` | 신규: 팔레트·스타일(§0.4) |
| `internal/ui/table.go` | 신규: 리치 테이블 렌더 헬퍼 |
| `internal/cli/output.go` | `richOut` 추가(§0.2), `statusDot` 색 경로 lipgloss화 |
| `internal/cli/commands.go` | `Ls` 리치 분기 |
| `cmd/prx/main.go` | `usage` 리치 분기 |
| `internal/cli/up.go` | `Up`/`Down` 요약 라인 스타일(선택) |
| `go.mod`/`go.sum` | lipgloss |

### 1.2 `internal/ui/table.go`

```go
package ui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

// Render returns an aligned, borderless table: bold header + adaptive colors.
// Borderless keeps output copy/grep-friendly and saves width. headers/rows are
// plain strings; caller pre-formats cell content (incl. status glyphs).
func Render(headers []string, rows [][]string) string {
	t := table.New().
		Border(lipgloss.HiddenBorder()).
		BorderTop(false).BorderBottom(false).
		BorderLeft(false).BorderRight(false).BorderColumn(false).
		Headers(headers...).
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == table.HeaderRow {
				return Header.PaddingRight(2)
			}
			return lipgloss.NewStyle().PaddingRight(2)
		})
	for _, r := range rows {
		t.Row(r...)
	}
	return t.String()
}
```

### 1.3 `Ls` 변경 ([internal/cli/commands.go](../../internal/cli/commands.go))

분기를 추가하되 **플레인 경로는 기존 코드 그대로** 둔다.

```go
if *jsonOut {
	return writeJSON(stdout, map[string]any{"services": rows})
}
if richOut(stdout, false) {
	headers := []string{"PROJECT", "SERVICE", "DOMAIN", "PORT", "TLS", "STATUS"}
	data := make([][]string, 0, len(rows))
	for _, r := range rows {
		data = append(data, []string{
			r.Project, r.Service, r.Domain, strconv.Itoa(r.Port), r.TLS,
			statusDot(r.Status, true),
		})
	}
	fmt.Fprintln(stdout, ui.Render(headers, data))
	return ExitOK
}
// --- plain path: 기존 tabwriter 블록 그대로 (변경 금지) ---
```

### 1.4 `statusDot` 색 경로 lipgloss화 ([internal/cli/output.go](../../internal/cli/output.go))

```go
func statusDot(status string, color bool) string {
	if !color {
		if status == "live" {
			return "* live"     // 플레인: 기존과 동일
		}
		return "o down"
	}
	if status == "live" {
		return ui.Success.Render("●") + " live"
	}
	return ui.Dim.Render("○") + " down"
}
```

플레인 분기 문자열(`* live`/`o down`)은 변경하지 않는다 → 골든 테스트 보존.

### 1.5 `usage` 리치 분기 ([cmd/prx/main.go](../../cmd/prx/main.go))

`usage(w)`에서 `richOut(w, false)`일 때 명령명을 `ui.Header`로 강조하고 정렬은
`lipgloss`(또는 기존 tabwriter 유지 + 색만). 플레인은 기존 tabwriter 그대로.
`cmd/prx`가 `internal/ui`를 import(사이클 없음).

### 1.6 요약 라인(선택, [internal/cli/up.go](../../internal/cli/up.go))

`Up`의 `proxy reloaded · N routes active`, `Add`의 `reserved ...` 등에 색 강조.
**플레인 경로 문자열 불변** 원칙 유지(색만 TTY에서 덧입힘).

### 1.7 엣지 케이스

- `NO_COLOR=1` → `isTTY` false → 플레인. (이미 보장)
- `prx ls | cat` → non-TTY → 플레인.
- `prx ls --json` → JSON, 스타일 미적용.
- 좁은 터미널 폭: lipgloss table은 폭 초과 시 줄바꿈. 필요 시 `MaxWidth` 캡 추가.
- 유니코드 폭(uniseg): 도메인에 전각문자 거의 없음. 기본 처리로 충분.

### 1.8 테스트

- **골든(회귀 차단, 최우선):** `internal/cli/commands_test.go`에 non-TTY writer(`*bytes.Buffer`)로 `Ls`/usage 호출 → 출력이 기존 기대값과 **바이트 동일**. `richOut`는 `*bytes.Buffer`에서 false이므로 플레인 보장.
- **`NO_COLOR` 테스트:** 환경 설정 후 escape 미포함 단언.
- **리치 스모크:** 실제 TTY 판정은 단위테스트 불가 → `ui.Render`/`statusDot(_, true)`를 직접 호출해 escape 포함·내용 정확성만 단언(터미널 가정 X).
- **`--json` 불변:** 기존 JSON 스키마 테스트 유지.

### 1.9 수용 기준

- 파이프/`--json`/`NO_COLOR` 출력 바이트 동일(골든 통과).
- TTY에서 `ls`가 테두리 테이블 + 적응형 색.
- `go test ./...`·`gofmt`·`go vet`·`golangci-lint` 통과.
- 바이너리 증가 ≤ ~1.3MB(lipgloss만).

### 1.10 롤백

`internal/ui` 미사용화 + 분기 제거로 단일 PR 되돌림. 플레인 경로는 손대지 않았으므로 위험 0.

---

## Phase 2 — bubbletea + bubbles (라이브 대시보드 + 진행 표시)

**목표:** 신규 `prx top` 대시보드와 장시간 작업 진행 UI. 기존 one-shot 명령 의미 불변.

**의존성:** bubbletea, bubbles (+ Phase 1).

### 2.1 신규 패키지 `internal/tui`

| 파일 | 내용 |
|---|---|
| `internal/tui/dashboard.go` | `prx top` 모델/프로그램 |
| `internal/tui/poll.go` | liveness·daemon·registry 폴링 `tea.Cmd` |
| `internal/tui/progress.go` | 단계 spinner·progress 공용 모델 |
| `internal/tui/run.go` | `Run(model) error` 래퍼(프로그램 옵션 표준화) |
| `internal/cli/top.go` | `Top` 명령(게이트 + tui 위임) |

### 2.2 `prx top` 명령 등록

[cmd/prx/main.go](../../cmd/prx/main.go) `commands` 맵에 추가:

```go
"top": cli.Top,
```

`commandHelp`에도 한 줄 추가: `{"top", "live dashboard of reservations, liveness, and the daemon"}`.

`internal/cli/top.go`:

```go
func Top(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("top", flag.ContinueOnError)
	fs.SetOutput(stderr)
	interval := fs.Duration("interval", 2*time.Second, "refresh interval")
	if err := fs.Parse(args); err != nil {
		return parseExit(err)
	}
	if !interactive(stdout) {
		return fail(stderr, false, ExitUsage, "not_a_tty",
			"prx top requires an interactive terminal; use `prx ls`")
	}
	m := tui.NewDashboard(registryStore(), paths.SocketPath(), *interval)
	if err := tui.Run(m); err != nil {
		return fail(stderr, false, ExitError, "tui", err.Error())
	}
	return ExitOK
}
```

### 2.3 데이터 소스

대시보드는 세 소스를 합친다:

1. **레지스트리** — `registryStore().Read()` → reservation 목록([internal/registry/registry.go](../../internal/registry/registry.go)). 매 tick 재읽기(파일 변경 반영).
2. **liveness** — `port.IsLive(p)`([internal/port/port.go](../../internal/port/port.go)). 포트당 최대 300ms → **동시 폴링 + 캐시 필수**.
3. **데몬 상태** — `daemon.NewClient(sock).Status()` → `Status{Running,PID,Routes,UptimeSec}`([internal/daemon/client.go](../../internal/daemon/client.go), [internal/daemon/admin.go](../../internal/daemon/admin.go)). **주의:** 데몬엔 "라우트 목록" 엔드포인트가 없고 개수만 준다. 행 단위 라우트는 레지스트리에서 구성한다.

### 2.4 폴링 (`internal/tui/poll.go`)

```go
type tickMsg time.Time
type livenessMsg map[int]bool
type registryMsg struct{ rows []service }      // service: 기존 ls 행과 동일 형태
type daemonMsg struct{ st daemon.Status; ok bool }

func tick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// pollLiveness probes all ports concurrently with bounded parallelism.
func pollLiveness(ports []int) tea.Cmd {
	return func() tea.Msg {
		out := make(map[int]bool, len(ports))
		var mu sync.Mutex
		var wg sync.WaitGroup
		sem := make(chan struct{}, 16)
		for _, p := range ports {
			wg.Add(1)
			sem <- struct{}{}
			go func(p int) {
				defer wg.Done()
				defer func() { <-sem }()
				live := port.IsLive(p)
				mu.Lock()
				out[p] = live
				mu.Unlock()
			}(p)
		}
		wg.Wait()
		return livenessMsg(out)
	}
}

func pollDaemon(sock string) tea.Cmd {
	return func() tea.Msg {
		st, err := daemon.NewClient(sock).Status()
		return daemonMsg{st: st, ok: err == nil}
	}
}
```

### 2.5 대시보드 모델 (`internal/tui/dashboard.go`)

```go
type Dashboard struct {
	store    *registry.Store
	sock     string
	interval time.Duration

	tbl     table.Model        // bubbles/table
	spin    spinner.Model      // bubbles/spinner (폴링 인디케이터)
	help    help.Model         // bubbles/help
	live    map[int]bool       // liveness 캐시
	daemon  daemon.Status
	dUp     bool
	lastErr error
	width   int
	height  int
}

func (m Dashboard) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, refreshAll(m.store, m.sock), tick(m.interval))
}

func (m Dashboard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "r":
			return m, refreshAll(m.store, m.sock)
		}
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.tbl.SetWidth(msg.Width)
	case tickMsg:
		return m, tea.Batch(refreshAll(m.store, m.sock), tick(m.interval))
	case registryMsg:
		m.tbl.SetRows(toRows(msg.rows, m.live))
	case livenessMsg:
		m.live = msg
		m.tbl.SetRows(reapply(m.tbl, m.live))
	case daemonMsg:
		m.daemon, m.dUp = msg.st, msg.ok
	}
	var cmd tea.Cmd
	m.spin, cmd = m.spin.Update(msg)
	return m, cmd
}

func (m Dashboard) View() string {
	header := ui.Header.Render(" prx ") + "  " + daemonLine(m.daemon, m.dUp)
	footer := m.help.View(keymap)
	return lipgloss.JoinVertical(lipgloss.Left, header, m.tbl.View(), footer)
}
```

`refreshAll`은 레지스트리 읽기 → 행 구성 → liveness 폴링 cmd + daemon 폴링 cmd를
`tea.Batch`로 묶어 반환한다. liveness는 행 포트 집합으로 호출한다.

**행 빌더 공유(결정):** 서비스 행 구성(`Ls`의 `service`/`liveness` 형태)을 신규
`internal/view`(registry+port 의존)로 추출한다. `internal/cli`와 `internal/tui`가 함께
호출해 중복을 없애고 `tui→cli` import 사이클을 피한다. `Ls`도 이 빌더를 쓰도록 리팩터한다.

### 2.6 진행 표시 (`internal/tui/progress.go`)

장시간 작업 3종에 인라인(풀스크린 아님) 진행 UI.

- **`expose`** ([internal/expose/expose.go](../../internal/expose/expose.go)): 터널 연결 단계 spinner.
  *의존:* 연결 함수가 단계 이벤트/콜백을 노출해야 함 → expose에 진행 채널(`<-chan Stage`)
  추가가 선행. 완료 시 공개 URL을 `ui.Brand`로 강조.
- **`upgrade`** ([internal/cli/upgrade.go](../../internal/cli/upgrade.go)): 현재 install.sh를
  shell-out하므로 **Go progress bar 불가**. 대안:
  - (기본) `sh script`의 stdout/stderr를 파이프로 받아 `viewport`에 스트림 + 헤더 spinner.
  - (선택, 별도) upgrade를 Go 직접 다운로드로 리팩터 → `bubbles/progress` 실제 진행률.
    범위 큼, Phase 2 필수 아님.
- **`trust`** ([internal/ca/trust.go](../../internal/ca/trust.go)): OS/브라우저 store 설치
  단계별 체크리스트 spinner.

모두 `richOut`/`interactive` 게이트 뒤에서만. 비대화/파이프 시 기존 텍스트 출력 유지.

### 2.7 `internal/tui/run.go`

```go
func Run(m tea.Model) error {
	p := tea.NewProgram(m, tea.WithAltScreen()) // top은 AltScreen, 진행 UI는 인라인(옵션 분리)
	_, err := p.Run()
	return err
}
```

진행 UI용은 AltScreen 없이 별도 헬퍼(`RunInline`).

### 2.8 엣지 케이스

- 데몬 미기동: `daemonLine`이 `stopped` 표시(에러 아님). 폴링은 계속.
- 레지스트리 빈 상태: "no reservations — run `prx up`" 안내 행.
- 폴링 중 레지스트리 파일 교체/락: `Read` 에러는 `lastErr`로 표시하고 다음 tick 재시도.
- 터미널 리사이즈: `WindowSizeMsg`로 테이블 폭 갱신.
- 많은 포트(수십 개): 동시성 16 캡으로 tick 내 완료. 필요 시 캡/주기 조정.
- non-TTY: `Top`이 진입 전 에러 반환(§2.2).

### 2.9 테스트

- `Update`를 메시지 주입으로 순수 테스트: `tickMsg`→폴링 cmd 반환, `livenessMsg`→캐시 갱신,
  `q`→`tea.Quit`. 터미널 불필요.
- `pollLiveness`를 로컬 리스너(`net.Listen`)로 live/down 검증.
- `Top` 게이트: `*bytes.Buffer` 전달 시 `not_a_tty` 에러·exit 2.
- 진행 UI: 단계 메시지 시퀀스 → 뷰 문자열 단언.
- 회귀: 기존 `expose`/`upgrade`/`trust`의 비대화 출력 골든 유지.

### 2.10 수용 기준

- `prx top`이 2초 주기 갱신, `q`/`Ctrl-C` 종료, 리사이즈 대응.
- non-TTY에서 명확한 에러(exit 2), 기존 명령 회귀 0.
- 진행 UI는 게이트 뒤에서만, 파이프 시 기존 텍스트 동일.

---

## Phase 3 — bubblezone(마우스) + `prx add -i`(인터랙티브)

**목표:** 대시보드 마우스 조작과 `add` 인터랙티브 입력. 기존 `add` 인자 모드 유지.

**의존성:** bubblezone (+ Phase 2).

### 3.1 `prx add -i` ([internal/cli/commands.go](../../internal/cli/commands.go))

`Add`에 `-i/--interactive` 플래그 추가. 핵심 로직을 공유 함수로 추출해 중복 제거.

```go
// 추출: Add와 인터랙티브 모델이 공유
func addReservation(domain string, p int) (added bool, err error) {
	res := registry.Reservation{Service: domain, Domain: domain, Port: p, TLS: config.TLSInternal, Adhoc: true}
	err = registryStore().Update(func(r *registry.Registry) error { return r.Reserve(res) })
	return err == nil, err
}
```

인터랙티브 모델(`internal/tui/addform.go`): `bubbles/textinput` 2개(domain, port) +
충돌 미리보기(기존 reservation을 `bubbles/list`로 표시, conflict 시 경고). 제출 →
`addReservation` 위임 → 결과를 기존 `Add`와 동일 문자열로 출력(`reserved <domain> -> :<port>`).
게이트: `-i`인데 non-TTY면 `not_a_tty` 에러.

비인터랙티브(`prx add <domain> <port>`)는 **완전히 동일하게 유지**(골든).

### 3.2 대시보드 마우스 (bubblezone)

`internal/tui/dashboard.go`(Phase 2 신규) 확장:

- 프로그램 옵션에 `tea.WithMouseCellMotion()` 추가, `zone.NewGlobal()` 초기화.
- `View`를 `zone.Scan(...)`로 감싸고, 각 테이블 행과 탭을 `zone.Mark(id, cell)`로 표시.
- `Update`의 `tea.MouseMsg`에서 `zone.Get(rowID).InBounds(msg)`로 클릭 행 판정.
- 액션: 행 클릭 → 선택, Enter/더블클릭 → 액션 메뉴(브라우저 열기 / 포트 복사 / `rm`).
  - 브라우저 열기: `https://<domain>` (darwin `open`, linux `xdg-open`).
  - 포트 복사: 클립보드 의존 회피 위해 우선 "포트 표시/echo"로 시작(클립보드는 선택).
  - `rm`: `ReleaseDomain` 경로 위임([internal/registry/registry.go](../../internal/registry/registry.go)), 확인 프롬프트.
- 탭 스캐폴드: `routes` / `daemon` (Phase 4에서 `charts` 추가).

### 3.3 엣지/테스트/수용

- 마우스 미지원 터미널: 키보드 경로 완전 동작(마우스는 부가). 회귀 없음.
- `zone` 미초기화 시 패닉 방지: `View`에서 nil 가드.
- 테스트: `MouseMsg` 주입 → 좌표별 행 선택 단언(zone bounds는 결정적). `addReservation`
  단위 테스트(충돌/정상). `add` 비인터랙티브 골든 유지.
- 수용: 마우스로 행 선택·액션, `add -i`가 인자 모드와 동일 결과, non-TTY 폴백.

---

## Phase 4 — ntcharts(차트) + 메트릭 수집

**목표:** 트래픽·지연·인증서 만료를 대시보드 차트로 시각화. **메트릭 수집 신규 설계 선행.**

**의존성:** ntcharts (+ Phase 2/3).

> **범위 결정:** Phase 4는 현재 **보류**. Phase 1~3 완료 후 별도 합의로 착수한다. 아래는 착수 시 설계.

### 4.1 메트릭 소스 설계 (선행 결정)

현재 트래픽 집계가 없다. access 로그는 opt-in JSONL뿐([internal/logx/access.go](../../internal/logx/access.go), `AccessEntry{ts,host,method,path,status,dur_ms,bytes,proto}`). 두 방안:

- **(권장) 데몬 인메모리 ring buffer.** 프록시가 요청마다 host별 시간버킷(예: 1초×60)에
  count·status class·지연 히스토그램을 누적. admin에 `GET /metrics` 추가
  ([internal/daemon/admin.go](../../internal/daemon/admin.go))로 최근 버킷 반환. 디스크/플래그
  무관, `--access-log` 없이 동작. 신규 `internal/metrics` 패키지.
- **(대안) access 로그 재파싱.** `--access-log` 필요 + sink 경로·rotation 설계
  ([internal/logx/rotate.go](../../internal/logx/rotate.go)) 의존. 과거 데이터엔 유리하나
  상시성/오버헤드 불리.

설계 의존이므로 **별도 합의 후** 진행. 아래는 (권장)안 가정.

### 4.2 `GET /metrics` 계약

```go
// internal/metrics
type Bucket struct {
	TsUnix   int64          `json:"ts"`
	Requests int            `json:"requests"`
	Status   map[string]int `json:"status"`   // "2xx","3xx","4xx","5xx"
	P50Ms    int64          `json:"p50_ms"`
	P95Ms    int64          `json:"p95_ms"`
}
type Snapshot struct {
	Window  int                 `json:"window_sec"`
	PerHost map[string][]Bucket `json:"per_host"`
}
```

`proxy` 핸들러 체인에 집계 미들웨어 추가(기존 `logx.AccessLog`와 유사 위치), 데몬이
`Snapshot` 노출. CLI/TUI는 `daemon.Client`에 `Metrics()` 추가해 폴링.

### 4.3 차트 (ntcharts, 대시보드 `charts` 탭)

- **요청률·지연:** `streamlinechart`/`timeserieslinechart`로 `PerHost` 버킷 시계열.
- **host별 트래픽:** `barchart`.
- **상태 분포:** sparkline 또는 stacked bar(2xx/4xx/5xx).
- **인증서 만료:** `tlsprov`의 인증서 `NotAfter`([internal/tlsprov/renew.go](../../internal/tlsprov/renew.go))로 잔여일 게이지/바.

`charts` 탭은 Phase 3 탭 스캐폴드에 콘텐츠를 채운다. 차트는 `tickMsg` 주기로 갱신.

### 4.4 엣지/테스트/수용

- 데이터 없음(데몬 미기동/초기): "no traffic yet" 플레이스홀더.
- 버킷 경계/시간 정렬: 단위 테스트로 누적·롤오버 검증.
- 오버헤드: 집계 미들웨어 벤치(요청당 ns)·메모리 상한(고정 버킷 수) 문서화.
- 수용: 차트 탭이 실데이터로 갱신, 데이터 없을 때 graceful, 집계 오버헤드 측정치 기재.

---

## 부록 A. 의존성 매트릭스

| 모듈 | Phase | 바이너리 영향 | 비고 |
|---|---|---|---|
| lipgloss | 1 | 소(~1MB) | termenv·uniseg transitive |
| bubbletea | 2 | 중 | 런타임 핵심 |
| bubbles | 2 | 소 | 컴포넌트 |
| bubblezone | 3 | 미미 | 마우스 |
| ntcharts | 4 | 소~중 | 차트 |

총합 측정치는 [plan.md §7](./plan.md) 참고(~+1.8MB).

## 부록 B. 테스트 전략

- **골든 회귀(전 Phase 공통):** 모든 비대화 경로(`*bytes.Buffer`, `--json`, `NO_COLOR`)
  출력 바이트 동일. 이게 1순위 안전망.
- **모델 단위 테스트:** bubbletea `Update`를 메시지 주입으로 순수 검증(터미널 불필요).
- **폴링/네트워크:** `net.Listen` 로컬 리스너로 liveness·메트릭 검증.
- **게이트:** `richOut`/`interactive`가 버퍼·`NO_COLOR`에서 false임을 단언.
- **CI:** 기존 `go test -race -cover`, `gofmt`, `go vet`, `golangci-lint`, `govulncheck`
  ([.github/workflows/ci.yml](../../.github/workflows/ci.yml)) 그대로 적용. 신규 패키지 포함.

## 부록 C. 파일 변경 요약

| 파일/패키지 | P1 | P2 | P3 | P4 |
|---|:--:|:--:|:--:|:--:|
| `internal/ui/*` | 신규 | · | · | · |
| `internal/cli/output.go` | 변경 | · | · | · |
| `internal/cli/commands.go` (`Ls`,`Add`) | 변경 | · | 변경 | · |
| `cmd/prx/main.go` (`usage`,`commands`) | 변경 | 변경 | · | · |
| `internal/cli/top.go` | · | 신규 | · | · |
| `internal/tui/*` | · | 신규 | 확장 | 확장 |
| `internal/expose/expose.go` | · | 변경(진행 이벤트) | · | · |
| `internal/daemon/admin.go`,`client.go` | · | · | · | 변경(/metrics) |
| `internal/metrics/*` | · | · | · | 신규 |
| `go.mod`/`go.sum` | lipgloss | +bubbletea,bubbles | +bubblezone | +ntcharts |

## 부록 D. 호환성·마이그레이션

- 설정/레지스트리 스키마·`prx.toml` 변경 없음.
- `--json` 스키마 불변. exit code 불변.
- 신규 표면: `prx top`, `prx add -i`, (P4) `prx daemon`/대시보드 차트 탭, admin `/metrics`.
- 모든 리치/인터랙티브는 opt-in 또는 TTY 자동 감지 — 스크립트 사용자 영향 없음.
