# prx CLI/TUI 개선 — 기획(PLAN)

> Charm 계열 TUI 라이브러리를 도입해 prx의 CLI 경험을 개선하기 위한 기획 문서.
> 구현 세부는 [impl.md](./impl.md) 참고.

> **상태:** 제안 단계. 범위·우선순위 미확정. 이 문서는 "무엇을·왜"를 정의하고,
> "어떻게"는 impl.md가 다룬다.

---

## 1. 배경 (Why)

현재 CLI는 기능은 충분하나 표현이 빈약하다.

- `ls`/usage 출력이 `text/tabwriter` 정적 테이블이다. ([internal/cli/commands.go](../../internal/cli/commands.go), [cmd/prx/main.go](../../cmd/prx/main.go))
- 상태 표시는 손수 짠 ANSI escape(`\x1b[32m●`)다. ([internal/cli/output.go](../../internal/cli/output.go))
- 모든 명령이 one-shot이라 인터랙션·라이브 갱신이 없다.
- 장시간 작업(`up` reload, `trust` 설치, `expose` 터널, `upgrade` 다운로드, ACME 갱신)에 진행 표시가 없다.
- `add`/`rm`은 정확한 인자가 필수다(선택 UI 없음).
- 데몬·라우트·포트를 한눈에 보는 대시보드가 없다.

## 2. 목표 / 비목표

**목표**

1. one-shot 출력의 가독성을 끌어올린다(테이블·상태·요약·usage).
2. 라이브 대시보드(`prx top`)로 reservation·데몬·liveness를 실시간 표시한다.
3. 장시간 작업에 진행 피드백(spinner/progress)을 준다.
4. `add` 등 인자 많은 명령에 인터랙티브 선택 모드를 제공한다.
5. (선택) 프록시 트래픽·인증서 만료를 차트로 시각화한다.

**비목표**

- 기존 명령의 출력 계약·exit code·`--json` 스키마 변경. (호환 유지)
- `prx port` 등 스크립트 주입 경로에 장식 추가.
- TUI를 기본 동작으로 강제. (항상 opt-in/자동 감지)
- 설정 파일 포맷(`prx.toml`)이나 레지스트리 스키마 변경.

## 3. 핵심 원칙 — 출력 계약 보존

prx의 출력 계약([internal/cli/output.go](../../internal/cli/output.go))은 불변식이다.

- stdout = 데이터, stderr = 진단, `--json` = 단일 JSON 값, 안정적 exit code.
- `NO_COLOR` 또는 non-TTY → 색·장식 없는 plain 출력.
- `prx port`는 스크립트 주입용 → 장식/TUI 금지.

**원칙 1 — 단일 게이트.** 모든 리치 렌더링은 하나의 판정 함수로 결정한다:
리치 출력은 `stdout이 TTY && --json 아님 && NO_COLOR 미설정`일 때만.

**원칙 2 — 인터랙티브 분리.** bubbletea 풀스크린/인라인 UI는 `stdin과 stdout 모두 TTY`일 때만 진입한다. 아니면 비대화 폴백 또는 명확한 에러.

**원칙 3 — 신규 표면 우선.** 인터랙티브 기능은 가급적 신규 명령/플래그로 추가하고, 기존 one-shot 의미를 바꾸지 않는다.

## 4. 라이브러리 역할

| 라이브러리 | 역할 | 단독 사용 |
|---|---|---|
| [lipgloss](https://github.com/charmbracelet/lipgloss) | 스타일·레이아웃·테이블·적응형 색 | O (bubbletea 불필요) |
| [bubbletea](https://github.com/charmbracelet/bubbletea) | Elm 아키텍처 TUI 런타임 | — |
| [bubbles](https://github.com/charmbracelet/bubbles) | 기성 컴포넌트(table/spinner/progress/list/textinput/viewport) | bubbletea 필요 |
| [bubblezone](https://github.com/lrstanley/bubblezone) | 마우스 영역 추적 | bubbletea 필요 |
| [ntcharts](https://github.com/NimbleMarkets/ntcharts) | 터미널 차트 | bubbletea+lipgloss 필요 |

## 5. 사용자 시나리오 (Before → After)

- **상태 확인:** `prx ls`가 단색 표 → 테두리·정렬·적응형 색 표. `prx top`으로 2초 주기 라이브 갱신.
- **프로젝트 기동:** `prx up` 텍스트 나열 → 서비스별 결과 박스 + reload 스피너.
- **노출:** `prx expose web --via cloudflare`가 무응답 대기 → 연결 단계 스피너 + 완료 시 공개 URL 강조.
- **업그레이드:** `prx upgrade` 침묵 → 단계 스피너 + 설치 스크립트 출력 스트림.
- **포트 추가:** `prx add` 인자 암기 → `prx add -i`로 프로젝트/서비스 검색·선택.

## 6. 로드맵

| Phase | 범위 | 의존 | 리스크 | 효과 |
|---|---|---|---|---|
| 1 | lipgloss로 `ls`·status·요약·usage 미화 | lipgloss | 낮음 | 큼 |
| 2 | bubbletea+bubbles: `prx top` 대시보드, `expose`/`upgrade`/`trust` 진행 표시 | +bubbletea,bubbles | 중 | 큼 |
| 3 | bubblezone 마우스 + `prx add -i` 인터랙티브 | +bubblezone | 중 | 중 |
| 4 | ntcharts + 메트릭 수집(access 로그 집계) | +ntcharts | 높음 | 중 |

의존 그래프:

```
lipgloss ─(단독)─> 출력 미화 [Phase 1, 독립]
   └> bubbletea ─> prx top [Phase 2]
        ├> bubbles    (table/spinner/progress/list/viewport)
        ├> bubblezone (마우스) [Phase 3]
        └> ntcharts   (차트) [Phase 4, +메트릭 수집 선행]
```

권장: **Phase 1부터.** 계약을 유지한 채 체감 개선이 가장 크다. Phase 4는 메트릭 수집 신규 파이프라인이 선행하므로 별도 설계가 필요하다.

## 7. 빌드 크기 영향

go1.26 darwin/arm64, 릴리스 플래그(`-trimpath -ldflags "-s -w"`) 실측:

| 빌드 | 크기 |
|---|---|
| 빈 Go 바이너리(baseline) | 1.16MB |
| TUI 5종 전부 임포트(실사용 포함) | 3.08MB |
| TUI 순증가분 | ~1.9MB |
| 현재 prx | 7.83MB |
| prx + TUI(예상) | ~9.5MB (+약 1.8MB, +23%) |

- 전부 순수 Go, CGO·대용량 에셋 없음. 무게 대부분은 bubbletea+lipgloss+termenv. bubblezone·ntcharts는 미미.
- `golang.org/x/text`·`x/sys`는 prx에 이미 있어 중복 없음. 순신규 transitive는 `termenv`·`uniseg`·`terminfo`.
- Phase 1만 채택 시 증가분은 위보다 작다(lipgloss+termenv만 링크).

## 8. 리스크

- **출력 계약 회귀** — 파이프/`--json`/CI에 escape 누출. → 단일 게이트 + 골든 테스트로 차단(IMPL §테스트).
- **liveness 폴링 비용** — `port.IsLive`는 포트당 최대 300ms 다이얼([internal/port/port.go](../../internal/port/port.go)). 대시보드는 동시 폴링+캐시 필요.
- **업그레이드 진행 표시 한계** — 현재 `upgrade`는 install.sh를 shell-out하므로([internal/cli/upgrade.go](../../internal/cli/upgrade.go)) Go progress bar 불가. 스피너+출력 스트림으로 시작, 직접 다운로드 리팩터는 선택.
- **메트릭 부재** — access 로그는 opt-in JSONL뿐([internal/logx/access.go](../../internal/logx/access.go)). Phase 4는 수집/집계 신규 설계 필요.
- **바이너리 +23%** — 수용 가능선. Phase 단계 도입으로 점증.

## 9. 성공 기준

- Phase 1: `ls`/usage가 TTY에서 리치, 파이프/`--json`/`NO_COLOR`에서 기존과 바이트 동일(골든 테스트 통과).
- Phase 2: `prx top`이 라이브 갱신·키 종료(q/Ctrl-C)·non-TTY 시 명확한 에러. 기존 명령 회귀 0.
- Phase 3: `prx add -i`가 선택 결과를 기존 `add` 경로로 위임(중복 로직 없음).
- Phase 4: 차트 명령이 데이터 없을 때 graceful, 수집 오버헤드 측정·문서화.

## 10. 미해결 질문

- Phase 채택 범위/순서 확정.
- `prx top` 데이터 소스 폴링 주기와 liveness 캐시 TTL.
- `upgrade` 직접 다운로드 리팩터 여부(실제 progress bar 위함).
- Phase 4 메트릭 수집 모델(상시 ring buffer vs 로그 파일 재파싱)과 access 로그 sink/rotation 설계([internal/logx/rotate.go](../../internal/logx/rotate.go)).
