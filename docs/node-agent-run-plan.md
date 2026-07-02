# Node Agent Run Plan

## Goal

Extend the Node API so agent and dev-tool callers can run a service with inline config, workspace-local gate state, and injected service environment variables without writing to the user's home cache or config directories.

## Scope

- Add an `isolatedRoot` client option that relocates gate subprocess state below a caller-selected directory.
- Add an `env(service, options)` API that returns the same service environment shape a child process needs.
- Add a `run(service, command, options)` API that spawns the child process directly from Node and merges the environment returned by `env()`.
- Support inline config for `env()` and `run()`, including `env` and `routeEnv`.
- Preserve existing `up()`, `service()`, `port()`, `ls()`, and `down()` behavior.
- Keep default Node API behavior using normal user gate state when `isolatedRoot` is not set.

## Usage Guidance

- Human-facing Node integrations should use the default gate state unless they explicitly need isolation. This keeps Node API behavior aligned with normal `gate` CLI usage and the user's existing registry, trust, and route state.
- Human-facing integrations should prefer `run()` with default `stdio: "inherit"` when launching dev servers, so logs and failures appear directly in the terminal.
- Agent and sandboxed tooling should pass `isolatedRoot` to a workspace-local, git-ignored runtime directory. The directory should be suitable for cache, config, state, and generated material, and should not imply a dependency on a specific build tool. Example locations include `.gate-agent/` or an existing ignored workspace cache directory.
- Agent and sandboxed tooling should use `env()` when an external runner owns process spawning, and `run()` when the Node API should own child spawning and env injection.
- Agent and sandboxed tooling should use `stdio: "pipe"` when it needs structured failure diagnostics from `GateError`, including captured stdout and stderr.
- Browser-facing app env still needs framework-public variable names in config, such as `VITE_*` or `NEXT_PUBLIC_*`. Agents should map those names through `routeEnv` when the browser needs the route URL.

## Non-goals

- Do not change gate CLI `run` semantics.
- Do not change gate CLI DNS mode names.
- Do not add or change `.local` / `.localhost` documentation in this work.
- Do not add automatic trust or certificate installation.
- Do not change gate core registry path behavior outside environment variables passed by the Node API.
- Do not make `isolatedRoot` the default for normal Node API callers.

## Constraints

- Response style remains caveman mode until user says `stop caveman` or `normal mode`.
- Continue from the newest user request and current plan state after resume or compaction.
- Protect user work: do not revert unrelated changes.
- Do not commit unless the user asks for commit work.
- Use `just` recipes instead of raw `go` when a recipe exists.
- Run targeted Node checks first, then broader checks if shared behavior changes.
- Keep the implementation scoped to `packages/node/src`, related tests, and docs needed for the new API.

## Assumptions

- `isolatedRoot` may be relative; relative paths resolve from `cwd` when provided, otherwise `process.cwd()`.
- `isolatedRoot` maps to:
  - `GATE_NODE_CACHE_DIR=<isolatedRoot>/cache/node`
  - `XDG_CONFIG_HOME=<isolatedRoot>/xdg/config`
  - `XDG_STATE_HOME=<isolatedRoot>/xdg/state`
  - `XDG_DATA_HOME=<isolatedRoot>/xdg/data`
- `env(service, options)` ensures routes first when `up` defaults to true, matching `service()`.
- `env(service, options)` combines `ls()` reservation metadata with the selected project config declarations. `ls()` provides service, domain, and port; the project config provides service-declared `env` and `routeEnv` names.
- Inline project config declarations come from the inline object. File-backed project config declarations come from the selected `gate.toml` path, or the discovered project config when no explicit path is passed.
- Project scopes without an inline config, explicit config path, or discoverable current `gate.toml` cannot recover service-declared `env` / `routeEnv` names from `ls()` alone. In that case `env()` returns only registry-derived `PORT` and `GATE_*` values; matching CLI `gate run` service-declared env injection for detached project scopes would require a future gate JSON env contract.
- Global scope has no project config declarations, so `env()` only returns `PORT` for the selected service plus `GATE_*` peer metadata.
- `env(service, options)` builds:
  - `PORT` for the requested service.
  - `GATE_<SERVICE>_PORT`
  - `GATE_<SERVICE>_URL`
  - `GATE_<SERVICE>_ROUTE_URL`
  - service-declared `env` values as loopback URLs.
  - service-declared `routeEnv` values as route URLs.
- `run(service, command, options)` calls `env()` and then spawns `command[0]` with `command.slice(1)`.
- `run()` inherits `process.env` plus client/options env, then overlays the generated gate env.
- `run()` supports `cwd`, `signal`, `timeoutMs`, and stdio options suitable for dev-server launchers.
- `run()` defaults to `stdio: "inherit"` so long-lived dev server output remains visible in the caller terminal.
- `run()` also supports `stdio: "pipe"` for agent/test callers that need captured output.
- `run()` returns `{ exitCode: 0 }` on a clean child exit.
- `run()` throws `GateError` on spawn failure, signal abort, timeout, or non-zero child exit.
- `GateError` from `run()` includes `command`, `exitCode` when available, `signal` when available, and captured `stdout` / `stderr` when `stdio: "pipe"` is used.
- `run()` does not call CLI `gate run`; Node owns child spawning so `env()` and `run()` share one path.

## Work Items

- [x] Add `isolatedRoot` to Node client option types and resolve it into gate subprocess env.
- [x] Add tests proving `isolatedRoot` sets `GATE_NODE_CACHE_DIR`, `XDG_CONFIG_HOME`, `XDG_STATE_HOME`, and `XDG_DATA_HOME`.
- [x] Extend project config metadata helpers so Node can read service `env` and `routeEnv` declarations from both inline config and file-backed `gate.toml`.
- [x] Add tests for project-only scopes without a resolvable config, proving `env()` returns registry-derived values without silently inventing service-declared env names.
- [x] Add `GateRunEnv`, `GateRunOptions`, and `GateRunResult` types.
- [x] Extend `GateErrorDetails` / `GateError` with `signal` so `run()` can expose signal aborts and process termination details.
- [x] Implement `env(service, options)` with inline config support and `PORT` / `GATE_*` / service-declared env output.
- [x] Add fake-gate tests for `env()` output, including loopback `env` and route `routeEnv` from inline config and file-backed `gate.toml`.
- [x] Implement `run(service, command, options)` using Node `spawn`.
- [x] Add fake child tests proving `run()` injects `PORT`, `env`, `routeEnv`, and `GATE_*`.
- [x] Add fake child tests proving `run()` reports non-zero exits with `GateError`, `exitCode`, and captured output when `stdio: "pipe"` is used.
- [x] Update `packages/node/README.md` with `isolatedRoot`, `env()`, and `run()` examples.
- [x] Run formatting for touched Node files.

## Validation

- [x] `just node-test`
- [x] `just node-typecheck`
- [x] `just docs-check` if docs change
- [x] `just node-smoke-examples`
- [x] `just check` before implementation closeout if the workstream proceeds beyond planning

## Risks

- `isolatedRoot` intentionally separates registry, daemon state, generated inline config, and CA data from the user's normal gate state. Callers must not expect reservations created by normal `gate` commands to appear in isolated runs.
- Direct Node spawning must avoid shell interpretation by default. `run()` should accept argv arrays, not shell strings.
- `run()` can start long-lived dev servers. Tests should use short-lived fake children and avoid persistent processes.
- `stdio: "inherit"` makes child failures visible to humans but does not provide captured output in errors. Agent callers that need diagnostic text should use `stdio: "pipe"`.
- `env()` duplicates part of CLI `gate run` behavior in Node. Tests must keep loopback and route URL semantics aligned with gate core behavior.
- Browser bundlers still require framework-specific public prefixes. `routeEnv` can inject names such as `VITE_API_BASE_URL` or `NEXT_PUBLIC_API_BASE_URL`, but gate cannot force bundlers to expose arbitrary names.
