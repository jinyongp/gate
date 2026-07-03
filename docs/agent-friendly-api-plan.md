# Agent-Friendly API Plan

## Goal

Make gate's CLI and Node API easier for agents, scripts, and sandboxed runners
to use without fragile shell wrappers, output scraping, duplicate route
resolution, or hidden writes to user state.

The core product direction is to treat the selected service descriptor as the
stable automation contract: a caller can ask gate what it should run, where it
will be reachable, what environment it needs, what local state it will touch,
and what should be fixed when setup is incomplete.

## Scope

- Promote the existing `gate env --json` descriptor into the documented agent
  readiness contract.
- Add a first-class Node `ready()` API that returns the canonical descriptor
  wrapped as `service` plus `env`, daemon readiness, and diagnostics.
- Let Node `run()` accept either a service name or a previously resolved ready
  descriptor to avoid repeated CLI calls.
- Standardize JSON error envelopes and error taxonomy across agent-facing CLI
  commands and Node `GateError`.
- Keep CLI agent behavior explicit and composable through command-specific
  flags and environment controls instead of adding a broad `--agent` mode.
- Improve `doctor --json` so setup scripts can distinguish fatal, fixable,
  permission-gated, warning, and informational issues.
- Improve daemon/listener JSON status so agents can understand whether route
  serving is ready, stale, or blocked.
- Document agent recipes for CLI, Node, CI, Tauri/Vite, and sandboxed state.

## Non-goals

- Do not turn `gate run` into a process supervisor or readiness probe for child
  dev servers.
- Do not make isolated state the default for normal CLI or Node users.
- Do not change browser trust, DNS policy, proxy routing, or registry schema
  unless required for the agent contracts.
- Do not make human CLI output parseable as a contract. Agents should use JSON.
- Do not force every command to support JSON in one pass when the command has no
  stable machine-use case.
- Do not add a compatibility shim that keeps two independent descriptor builders
  alive.

## Constraints

- Use `just` recipes instead of raw `go` when a recipe exists.
- Run targeted checks first, then `just check` before closeout.
- Keep documentation boundaries:
  - `docs/spec.md` for product semantics and invariants.
  - `docs/usage.md` for CLI syntax, examples, JSON behavior, and recipes.
  - `packages/node/README.md` for Node API usage.
  - `skills/gate/SKILL.md` for concise agent operation.

## Assumptions

- `gate env --json` remains the canonical descriptor source for agent route/env
  data.
- Node `ready()` should call the same descriptor contract rather than rebuild
  URLs or env values independently.
- `gate run` still owns child process execution only when the user explicitly
  asks it to run a child command.
- Do not add a broad CLI `--agent` mode. Agent-friendly behavior should be a
  small set of explicit controls:
  - `--json` for structured command output and JSON error envelopes.
  - `--quiet` for suppressing parent route/status hints where supported.
  - `--isolated-root` / `GATE_ISOLATED_ROOT` for sandboxed state.
  - existing `CI`, `NO_COLOR`, and `GATE_NO_INDICATOR=1` policies for
    non-interactive output.
- Node `ready()` is the public API name.
- `run(ready, argv)` treats the ready descriptor as a snapshot and does not
  perform automatic staleness checks.
- Diagnostic schema is stable, but the presence of any specific diagnostic is
  best-effort and environment-dependent.
- Error taxonomy work should target the agent-facing commands first:
  `env`, `up`, `ls`, `port`, `run`, `daemon status`, and `doctor`.
  `trust` and `expose` should be audited for consistency, but full envelope
  standardization can remain follow-up unless the implementation naturally
  touches those paths.
- `doctor --json` should exit non-zero for `fatal`, `permission`, and `fixable`
  issues; warning/info-only reports should exit zero.
- Exit code changes must be backward-compatible where existing documented
  behavior exists.

## Proposed Contracts

### Agent Descriptor

The descriptor should extend the current `gate env --json` shape without
breaking existing consumers:

```json
{
  "service": "web",
  "project": "myapp",
  "domain": "web.myapp.localhost",
  "port": 4310,
  "url": "https://web.myapp.localhost",
  "loopbackUrl": "http://127.0.0.1:4310",
  "route": "active",
  "upstream": "down",
  "env": {
    "PORT": "4310",
    "GATE_WEB_PORT": "4310"
  },
  "daemon": {
    "required": true,
    "running": false,
    "httpsAddr": "127.0.0.1:443",
    "listener": "listener:https-443-http-80"
  },
  "diagnostics": [
    {
      "code": "daemon_not_running",
      "severity": "fixable",
      "message": "listener daemon is not running",
      "suggestedCommand": "gate up --daemon"
    }
  ]
}
```

Additive fields only. Existing `service`, `domain`, `port`, `url`,
`loopbackUrl`, `route`, `upstream`, and `env` keep their current meaning.

Compatibility rules:

- Existing descriptor fields must not change type or meaning in a minor
  release.
- New descriptor fields must be additive and optional for consumers.
- Consumers should ignore unknown fields.
- Diagnostic codes and envelope fields are stable once documented, but the
  presence of any specific diagnostic remains best-effort.
- Breaking descriptor or error-envelope changes require an explicit major
  version boundary or a documented migration path.

### Node `ready()`

```ts
const ready = await gate.ready('web', {
  up: true,
  daemon: true,
})

console.log(ready.service.url)
console.log(ready.env.PORT)
```

Type sketch:

```ts
export interface GateReadyResult {
  service: GateService
  env: GateRunEnv
  daemon?: GateDaemonReadiness
  diagnostics: GateDiagnostic[]
}
```

`ready()` should:

- optionally perform the same `up` phase as `service()` / `run()`;
- return the canonical CLI descriptor;
- preserve custom DNS and scope options;
- not spawn a child process;
- not duplicate env or route construction in TypeScript.

### Node `run()` With Ready Input

```ts
const ready = await gate.ready('web', { up: true })
await gate.run(ready, ['pnpm', 'dev'])
```

This avoids repeated `up` / `env` calls when an agent needs to inspect the
descriptor before spawning.

The existing `gate.run('web', argv, options)` signature remains supported.

### Composable CLI Agent Controls

Do not add a global `--agent` mode. It hides too many independent behaviors
behind one vague switch and risks becoming an unclear alias for `--json`.

Agent-friendly CLI usage should be built from explicit controls:

- Use `--json` on commands that have structured output.
- Use `--quiet` on commands that otherwise emit parent hints, such as
  `gate run --up`.
- Use `--isolated-root path` or `GATE_ISOLATED_ROOT` for sandboxed local state.
- Use `CI=1`, `NO_COLOR=1`, or `GATE_NO_INDICATOR=1` when the environment does
  not already suppress decorative output.

Example:

```bash
GATE_NO_INDICATOR=1 gate --isolated-root .gate-agent env --up web --json
```

Mutations must remain controlled by explicit command flags such as `--up`,
`--fix`, or `--daemon`.

### Error Envelope

All agent-facing JSON errors should converge on:

```json
{
  "error": {
    "code": "service_not_found",
    "message": "service not found: web",
    "severity": "fatal",
    "retryable": false,
    "hint": "Check the selected scope or run gate up first.",
    "nextActions": [
      {
        "label": "List services",
        "command": "gate ls --json"
      }
    ]
  }
}
```

Node `GateError` should preserve:

- stable code;
- original gate code;
- command;
- exit code;
- stdout/stderr;
- retryability;
- next actions when present.

### `doctor --json`

`doctor --json` should return machine-classified issues:

```json
{
  "status": "fail",
  "issues": [
    {
      "code": "config_dir_unwritable",
      "severity": "fatal",
      "fixable": false,
      "requiresPrivilege": false,
      "paths": ["/Users/me/.config/gate"],
      "message": "config directory is not writable"
    },
    {
      "code": "hosts_update_required",
      "severity": "permission",
      "fixable": true,
      "requiresPrivilege": true,
      "suggestedCommand": "gate up --dns hosts"
    }
  ]
}
```

Setup scripts should be able to fail on blocking `fatal`, `permission`, and
`fixable` issues while allowing warning/info-only reports.

### Daemon Status

`gate daemon status --json` should expose listener health in a shape agents can
use without parsing logs:

- listener ref;
- socket path;
- pid path;
- pid alive;
- HTTP/HTTPS listen addresses;
- route count;
- config/state root paths when those paths are relevant to setup debugging.

## Work Items

- [x] Audit current CLI JSON and error paths for commands agents actually use:
      `env`, `up`, `ls`, `port`, `run`, `daemon status`, `doctor`, `trust`,
      `expose`. Standardize `trust` and `expose` only if the audit finds
      agent-visible inconsistency in paths already touched by this work.
- [x] Define shared Go types for agent descriptor diagnostics, next actions,
      and JSON error metadata.
- [x] Extend `gate env --json` descriptor with additive `daemon` and
      `diagnostics` fields.
- [x] Add tests proving existing `gate env --json` consumers remain compatible
      when new fields are present.
- [x] Add Node `ready()` public API and types.
- [x] Make Node `run()` accept `GateReadyResult` as input while preserving the
      existing service-name overload.
- [x] Add Node tests proving `ready()` and `run(ready, argv)` do not perform
      duplicate descriptor resolution.
- [x] Standardize JSON error envelopes for agent-facing commands.
- [x] Add Node `GateError` mapping for retryability, hint, and next actions.
- [x] Document and test the composable CLI agent controls: `--json`, `--quiet`,
      `--isolated-root`, and non-interactive output environment variables.
- [x] Improve `doctor --json` issue classification and tests.
- [x] Improve `daemon status --json` listener health shape and tests.
- [x] Update `docs/spec.md` with the agent descriptor and error-envelope
      invariants.
- [x] Update `docs/usage.md` with CLI agent recipes and JSON examples.
- [x] Update `packages/node/README.md` with `ready()` and `run(ready, argv)`.
- [x] Update `skills/gate/SKILL.md` with the preferred agent command/API
      patterns.

## Phases

### Phase 1: Descriptor and Node Readiness

- Extend the canonical `gate env --json` descriptor.
- Add Node `ready()` and `run(ready, argv)`.
- Prove the Node implementation consumes the canonical descriptor instead of
  rebuilding route/env values independently.

Success criteria:

- Existing descriptor consumers keep working with additive fields.
- A Node caller can resolve, inspect, and run a service without duplicate
  descriptor resolution.
- Route, loopback URL, port, env, daemon, and diagnostics values come from one
  shared contract.

### Phase 2: Structured Errors

- Add JSON error envelopes to the first-pass agent-facing commands:
  `env`, `up`, `ls`, `port`, `run`, `daemon status`, and `doctor`.
- Map the same metadata into Node `GateError`.

Success criteria:

- Scripts can branch on stable `error.code`, `severity`, `retryable`, and
  `nextActions` instead of scraping text.
- Human error output stays useful and is not treated as a machine contract.
- Existing documented exit-code behavior remains compatible.

### Phase 3: Diagnostics and Status

- Improve `doctor --json` classification.
- Improve `gate daemon status --json` listener health output.

Success criteria:

- Setup scripts can fail only on blocking issue classes.
- Agents can tell whether route serving is ready, stale, stopped, or blocked
  without reading logs.

### Phase 4: Recipes and Agent Reference

- Update end-user usage docs, Node README, and the gate skill.
- Add copy-paste recipes for CLI, Node, CI, Tauri/Vite, and sandboxed state.

Success criteria:

- Agents have one recommended CLI path and one recommended Node path.
- The docs explain when to use `--json`, `--quiet`, `--isolated-root`,
  `GATE_ISOLATED_ROOT`, `CI`, `NO_COLOR`, and `GATE_NO_INDICATOR=1`.

## Validation

- [x] `go test ./internal/cli ./cmd/gate` for CLI behavior.
- [x] `go test ./internal/paths ./internal/daemon` for state and daemon status
      behavior when touched.
- [x] `just node-test` for Node API behavior.
- [x] `just node-typecheck` for public type changes.
- [x] `just docs-check` for CLI docs drift.
- [x] `just fmt-check` after Go edits.
- [x] `just check` before closeout.
- [x] `review-loop` covering node, CLI/process/env, planning/docs, and
      code-quality axes.

## Risks

- Expanding JSON contracts can accidentally imply support guarantees for fields
  that are still operationally best-effort. Mitigation: mark additive
  diagnostics as stable schema but not guaranteed issue presence.
- Combining explicit CLI controls can be more verbose than a single mode.
  Mitigation: document copy-paste recipes and keep each control narrow and
  predictable.
- `run(ready, argv)` can run with stale descriptors. Mitigation: document that
  ready descriptors are snapshots and do not auto-refresh.
- Doctor classifications can overfit local macOS/Linux behavior. Mitigation:
  test classification with fakes and keep OS-specific privileged operations
  behind existing seams.
- Node overloads can make types harder to understand. Mitigation: add narrow
  overloads and keep the original `run(service, argv, options)` examples
  primary for simple use.
