# Run Route Info Plan

## Goal

Make `gate run --up` and related automation APIs expose the selected service's
route, loopback URL, and port before a child dev server starts, without forcing
callers to duplicate `service()` / `ls()` / `run()` calls or parse human output.

## Scope

- Add a shared route/env descriptor model used by CLI `run`, CLI `env`, and the
  Node API.
- Make `gate run --up <service> -- <cmd...>` show the selected service details
  before child process execution when stderr is an interactive terminal.
- Add `gate run --quiet` to suppress parent route/status hints for callers that
  want only child stderr.
- Add `gate env <service> --json` for shell scripts and external runners that
  need the same environment data without spawning a child process.
- Keep the Node API aligned with the shared descriptor so `run()` can expose the
  selected `GateService` and generated env without requiring a separate
  `service()` call.
- Document Tauri/Vite usage as an official recipe.
- Add a CLI and environment entry point for agent/sandbox isolation so gate
  state can live below a caller-controlled root.
- Clarify agent/sandbox isolation and daemon-start semantics in docs.

## Non-goals

- Do not turn gate into a process supervisor or dev-server readiness checker.
- Do not wait for the child server to bind its port before reporting route data.
- Do not print route data to stdout from `gate run`; stdout belongs to the
  child process.
- Do not make non-interactive `gate run` noisier by default.
- Do not change DNS, trust, registry, daemon, or proxy routing behavior.
- Do not require Node API callers to use isolated state by default.
- Do not change `doctor --json` in this workstream; setup health reporting is a
  separate concern from route/env discovery.
- Do not make CLI or Node isolated state the default.

## API Design Principles

- Single source of truth: derive `PORT`, loopback URL, route URL, peer service
  env, and service-declared env from one descriptor builder, then reuse it from
  CLI and Node paths.
- Stable structured data first: JSON contracts are for scripts and Node; human
  text is secondary and must not be parsed.
- Child stdio isolation: `gate run` must not pollute child stdout. Human route
  hints go to stderr before exec.
- Backward compatibility: existing commands, defaults, env names, and JSON
  shapes keep working unless an opt-in flag is used.
- Terminal-sensitive UX: route hints are useful in interactive terminals but
  should stay quiet for redirected stderr, CI, and script contexts.
- Composable lifecycle: `up` reserves/activates routes, `env` describes process
  environment, `run` spawns a child with that environment. New API should make
  these phases explicit, not hide extra behavior.
- Read-only inspection by default: commands named `env` must not mutate
  registry, DNS, or daemon state unless the caller passes an explicit mutating
  flag such as `--up`.
- Listener clarity: daemon options guarantee listener daemon start/reuse and
  route reload for the selected listener, not child server readiness.
- Scope symmetry: project, explicit config, named project, and global scopes
  should behave consistently where the underlying data exists.
- Agent safety: sandbox-friendly isolation should use explicit roots or XDG env
  mapping, never hidden writes outside caller-controlled state.

## Constraints

- Response style remains caveman mode until user says `stop caveman` or
  `normal mode`.
- Continue from the newest user request and current plan state after resume or
  compaction.
- Protect user work: do not revert unrelated changes.
- Do not commit unless the user asks for commit work.
- Use `just` recipes instead of raw `go` when a recipe exists.
- Run the narrowest relevant checks first, then broader checks when ready.
- Keep documentation boundaries:
  - `docs/spec.md` for product semantics and invariants.
  - `docs/usage.md` for CLI syntax, examples, JSON behavior, and recipes.
  - `packages/node/README.md` for Node API usage.
  - `skills/gate/SKILL.md` for concise agent operation.

## Assumptions

- The selected route URL is the local HTTPS gate route, such as
  `https://web.demo.localhost`.
- The selected loopback URL is the upstream URL, such as
  `http://127.0.0.1:4312`.
- `gate run --up` reports selected route data after `up` succeeds and before
  spawning the child command when stderr is an interactive terminal.
- `gate run --quiet` suppresses parent route/status hints. It does not suppress
  child stdout or child stderr.
- If a daemon uses a non-default HTTPS listener port, route URLs should include
  the listener port just like `gate up` output does when routes are reloaded.
- When no listener daemon is running, route URLs still use the configured domain
  without claiming the proxy is actively serving.
- The shared descriptor builder owns route URL construction and reads listener
  daemon status when available. This keeps `gate env`, `gate run`, and Node
  behavior aligned even when `gate env` is used read-only.
- `gate env <service> --json` is read-only by default.
- `gate env --up <service> --json` is explicit opt-in mutation that mirrors the
  `up` phase before returning env data.
- Node `run()` should continue to own child spawning instead of shelling out to
  CLI `gate run`, but route/env resolution should consume the same
  `gate env --json` descriptor used by scripts.
- Node `run()` exposes route/env metadata through an `onReady` callback before
  child spawn, and also includes the same metadata in `GateRunResult` for
  short-lived commands and tests.
- CLI sandbox isolation is explicit through `--isolated-root path` or
  `GATE_ISOLATED_ROOT`. The flag/env maps config, state, and data below the
  root and takes precedence over the caller's normal XDG locations.

## Proposed Contracts

### Shared Descriptor

Internal model:

```go
type RunDescriptor struct {
	Service     string            `json:"service"`
	Project     string            `json:"project,omitempty"`
	Standalone  bool              `json:"standalone,omitempty"`
	Domain      string            `json:"domain"`
	Port        int               `json:"port"`
	URL         string            `json:"url"`
	LoopbackURL string            `json:"loopbackUrl"`
	Route       string            `json:"route"`
	Upstream    string            `json:"upstream"`
	Env         map[string]string `json:"env"`
}
```

The exact Go type can stay internal. JSON field names should match existing
Node `GateService` names where possible.

### CLI `gate run` Route Hint

Command:

```bash
gate run --up web -- pnpm dev
```

Behavior:

- Runs `up` first when `--up` is present.
- Builds the same env descriptor used for child env injection.
- Prints a compact selected-service hint to stderr when stderr is an
  interactive terminal.
- Skips the hint when stderr is redirected, CI-like, or `--quiet` is set.
- Spawns the child with the current `gate run` env semantics.

Suggested stderr text:

```text
gate route  web  https://web.demo.localhost  ->  http://127.0.0.1:4312
```

No JSON mode for `gate run` in this pass. Structured consumers should use
`gate env --json`.

### Node API

Node `run()` exposes the selected service and generated env through `onReady`
before spawning the child process:

```ts
await gate.run('web', ['pnpm', 'dev'], {
  onReady({ service, env }) {
    console.log(service.url)
  },
})
```

Type shape:

```ts
export interface GateRunReady {
  service: GateService
  env: GateRunEnv
}

export interface GateRunOptions extends GateServiceOptions {
  stdio?: 'inherit' | 'pipe'
  onReady?: (ready: GateRunReady) => void | Promise<void>
}

export interface GateRunResult {
  exitCode: number
  signal?: NodeJS.Signals
  stdout?: string
  stderr?: string
  service?: GateService
  env?: GateRunEnv
}
```

`onReady` fires after route/env resolution and before child spawn. If `onReady`
throws or rejects, `run()` must not spawn the child. The thrown cause should be
preserved in a `GateError`. `GateRunResult` carries the same metadata for
short-lived commands and tests; long-running dev servers should use `onReady`.
Node `env()` and `run()` resolve their metadata from `gate env --json` so route
URLs cannot drift from CLI descriptor behavior, including non-default listener
ports.

### CLI `gate env`

Commands:

```bash
gate env web
gate env web --json
gate env --up web --json
gate env -g web --json
gate env --config path/to/gate.toml web --json
```

JSON success shape:

```json
{
  "service": "web",
  "project": "demo",
  "domain": "web.demo.localhost",
  "port": 4312,
  "url": "https://web.demo.localhost",
  "loopbackUrl": "http://127.0.0.1:4312",
  "route": "active",
  "upstream": "down",
  "env": {
    "PORT": "4312",
    "GATE_WEB_PORT": "4312",
    "GATE_WEB_URL": "http://127.0.0.1:4312",
    "GATE_WEB_ROUTE_URL": "https://web.demo.localhost"
  }
}
```

Text output should be shell-friendly, not decorative:

```text
PORT=4312
GATE_WEB_PORT=4312
GATE_WEB_URL=http://127.0.0.1:4312
GATE_WEB_ROUTE_URL=https://web.demo.localhost
```

Use JSON for robust scripts. Text output is convenience only.

### Tauri/Vite Recipe

Document this shape:

```toml
[project]
name = "desktop"
base = "app.localhost"

[services.desktop]
route_env = "TAURI_DEV_URL"
```

```ts
// vite.config.ts
export default defineConfig({
  server: {
    host: '127.0.0.1',
    port: Number(process.env.PORT),
    strictPort: true,
  },
})
```

```json
{
  "build": {
    "devUrl": "https://desktop.app.localhost"
  }
}
```

If Tauri config cannot read env directly, document a small config-loading
example that reads `process.env.TAURI_DEV_URL` in a JS/TS config variant.

## Work Items

- [x] Extract a shared descriptor builder from current `lookupScopedReservation`
      and `runEnvForScope` behavior.
- [x] Add descriptor route URL construction that reads actual daemon HTTPS
      listener address when available.
- [x] Add default interactive pre-child route output for `gate run --up`.
- [x] Add `gate run --quiet` to suppress parent route/status hints without
      changing child stdio.
- [x] Add CLI tests proving route hints print before child output, write only
      to stderr, are hidden for non-interactive stderr, and are suppressed by
      `--quiet`.
- [x] Add `gate env <service>` command with `--json`, `--up`, and existing scope
      flags.
- [x] Add CLI tests for `gate env --json` across current project, explicit
      config, named project, and global scope.
- [x] Add completion specs and completion tests for `env` and `run --quiet`.
- [x] Add Node `run()` `onReady` and result `service` / `env` metadata using
      `gate env --json` as the shared descriptor contract.
- [x] Add Node tests proving `onReady` receives selected service and env before
      child spawn, and result metadata is returned for short-lived children.
- [x] Add CLI `--isolated-root` and `GATE_ISOLATED_ROOT` for sandbox-friendly
      gate state isolation.
- [x] Update `docs/usage.md` with `gate run` route hints, `--quiet`, `gate env`,
      JSON shape, Tauri/Vite recipe, and daemon semantics.
- [x] Update `packages/node/README.md` with `onReady`, result metadata,
      `env()`, `run()` examples, and isolated root behavior.
- [x] Update `skills/gate/SKILL.md` with concise agent guidance for `gate env
      --json`, `gate run --up`, `gate run --quiet`, CLI `--isolated-root`, and
      Node isolated usage.
- [x] Clarify sandbox guidance: Node `isolatedRoot` is available; CLI users can
      use `--isolated-root` or `GATE_ISOLATED_ROOT` for explicit isolation.

## Validation

- [x] `just test` for Go CLI behavior.
- [x] `just node-test` for Node API behavior.
- [x] `just node-typecheck` for API type changes.
- [x] `just docs-check` after docs updates.
- [x] `just fmt` after Go edits.
- [x] `just fmt-check` before closeout.
- [x] `just check` before final implementation closeout.

## Risks

- Printing parent route data from `gate run` can break callers if stdout is
  used or if non-interactive stderr is expected to contain only child output.
  Mitigation: never write hints to stdout, show by default only on interactive
  stderr, and provide `--quiet`.
- Route URL can differ when the listener uses a non-default HTTPS port.
  Mitigation: descriptor URL construction reads listener daemon status when
  available and covers daemon-port cases in tests.
- `gate env` can drift from `gate run` env injection.
  Mitigation: both paths must use one descriptor builder.
- Node `run()` callback failures can blur child lifecycle semantics. Mitigation:
  call `onReady` before spawn, do not spawn the child when it fails, preserve the
  cause in `GateError`, and return result metadata only after child completion.
- Project-only named scope may not recover service-declared env names when no
  config path exists.
  Mitigation: preserve current behavior: return registry-derived `PORT` and
  `GATE_*` values, document the limitation.
- Tauri config formats vary by version.
  Mitigation: keep recipe focused on invariant pieces: Vite reads `PORT`, Tauri
  dev URL equals the gate route.
