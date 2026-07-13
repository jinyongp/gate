# Isolated Daemon Guard Plan

## Goal

Prevent `isolatedRoot` Node API callers from starting a listener daemon, while
still allowing explicit `daemon: false`, and make CLI daemon conflicts easier to
diagnose when another gate state root already owns the default listener ports.

## Scope

- Add Node API type-level constraints so object-literal isolated clients and
  isolated per-call options do not accept `daemon: true`, while `daemon: false`
  remains valid.
- Add Node API runtime validation that throws `GateError` before invoking the
  gate binary when an isolated call requests `daemon: true`.
- Improve CLI diagnostics for `:443` / `:80` bind conflicts caused by another
  `gate __serve` process from a different state root.
- Improve `gate up`'s "no daemon running" note when a gate daemon is listening
  on the expected port but is unreachable through the current state root.
- Update Node API, usage, and agent-facing docs to describe the boundary:
  `isolatedRoot` isolates state, not kernel listener ports.
- Add focused tests for Node API type/runtime behavior and CLI diagnostic
  helpers.

## Non-goals

- Do not add Node API listener address options in this workstream.
- Do not allow `isolatedRoot + daemon: true` through an escape hatch.
- Do not auto-stop daemons from other state roots.
- Do not change CLI support for isolated daemon tests that use explicit
  non-default listener addresses.
- Do not change the meaning of `GATE_ISOLATED_ROOT` for registry, cache, CA, or
  daemon state paths.

## Constraints

- User decision: if `isolatedRoot` is set, `daemon: true` must be forbidden in
  types and rejected internally; `daemon: false` is allowed.
- Active conversation style: caveman mode remains active until the user says
  `stop caveman` or `normal mode`; keep status short and direct.
- Continue from the newest user request and this plan; do not restart after
  resume or context compaction.
- Protect user work; do not revert unrelated changes.
- Do not commit unless the user asks.
- Use `just` recipes instead of raw `go` when a recipe exists.
- Run narrow checks first, then broader checks when implementation is ready.
- Keep docs within existing boundaries: product/architecture in `docs/spec.md`,
  user syntax and troubleshooting in `docs/usage.md`, agent operational
  reference in `skills/gate/SKILL.md`.

## Assumptions

- Existing public Node API types can be evolved with generic/conditional method
  option types while preserving the common `createGateClient()` call shape.
- Runtime guard should reject `daemon: true` when the effective call has
  `isolatedRoot`. `daemon: false`, `daemon: undefined`, and omission are allowed.
- CLI conflict diagnostics can depend on best-effort process inspection. If
  process inspection fails or `lsof` is unavailable, existing error behavior
  remains acceptable.
- The current default listener pair remains HTTPS `:443` and HTTP `:80`.

## Work Items

- [x] Refine Node API types for isolated clients and per-call isolated options.
- [x] Add Node API runtime guard for effective `isolatedRoot` plus provided
      `daemon: true`.
- [x] Add Node API tests for compile-time and runtime daemon-start rejection and
      `daemon: false` allowance.
- [x] Add CLI helpers to detect TCP listener owners and identify `gate __serve`
      socket paths.
- [x] Add CLI daemon-start conflict diagnostics for cross-state-root gate
      daemons.
- [x] Add `gate up` note diagnostics when the current state root has no
      reachable daemon but the listener port is owned by another gate daemon.
- [x] Update docs and skill text for the isolated daemon boundary.
- [x] Run targeted validation, then full project validation if targeted checks
      pass.
- [x] Run review-loop after implementation and validation before closeout.

## Implementation Notes

### Node API Types

Introduce type shapes that distinguish normal and isolated clients:

- `createGateClient({ isolatedRoot: string })` returns an isolated client whose
  `up`, `service`, `env`, `ready`, and `run` option types reject `daemon: true`
  but allow `daemon: false`.
- `createGateClient()` and `createGateClient({ isolatedRoot: undefined })`
  return normal clients whose per-call options allow either:
  - normal options with `isolatedRoot?: undefined` and `daemon?: boolean`, or
  - isolated per-call options with `isolatedRoot: string` and `daemon?: false`.
- Isolated client methods use the isolated option branch, so method calls reject
  `daemon: true` even when the method argument does not repeat `isolatedRoot`.

Use a generic client type rather than relying only on the per-call option union,
because `createGateClient({ isolatedRoot: '.gate-agent' }).up({ daemon: true })`
does not carry `isolatedRoot` in the method argument.

Add type-level regression coverage with `@ts-expect-error` in a TypeScript file
included by `packages/node` typecheck, or a small dedicated typecheck fixture if
that keeps production tests cleaner. Cover object literals that try to pass
`daemon: true` to isolated option types, and positive fixtures for `daemon:
false` plus omission. Broad public option types can still be predeclared with
`isolatedRoot` plus `daemon: true`; cover those with runtime tests as a defense
against structural typing, `any`, or dynamic option construction.

### Node API Runtime Guard

Add a small helper in `packages/node/src/client.ts` or nearby:

```ts
function assertDaemonAllowed(
  options: GateCommandOptions & { daemon?: boolean },
  command: string[],
): void
```

The helper throws:

- `GateError`
- `code: 'GATE_INVALID_OPTIONS'`
- message: `isolatedRoot cannot be combined with daemon: true; use normal gate
  state for real dev launches, or use the CLI with explicit non-default listener
  addresses for isolated daemon tests`

Call it after defaults merge and before DNS checks, branching, or subprocess
execution in every public Node API path that accepts service/run/up options:

- `up()`
- `service()`
- `env()`
- `ready()`
- `run()`

Do not rely only on `up()`: callers can pass `up: false`, and `run()` can accept
an existing readiness snapshot. Runtime validation must reject an effective
`isolatedRoot` plus `daemon: true` before those paths skip route activation.

### CLI Diagnostics

Add best-effort helpers near daemon lifecycle code:

- `findTCPListenOwners(port string) []listenerPortOwner`
- `gateDaemonSocketPath(args string) string`
- `gateDaemonConflictHint(pair listener.Pair, currentRef listenerDaemonRef)`

Use `lsof -nP -iTCP:<port> -sTCP:LISTEN` when available, then use `ps` to read
full args for candidate PIDs. Reuse `isGateDaemonArgs`.

On daemon start bind conflict:

- Preserve the existing primary error.
- If owner is `gate __serve`, append hint lines with owner PID, owner socket,
  current socket, and safe next actions.
- If owner is not gate or inspection fails, keep existing behavior.

On `gate up` when no reload happened:

- If another gate daemon owns the relevant listener port but current socket is
  unreachable, replace or extend the note with a cross-state-root hint.
- Keep the success exit code because route activation/reservation succeeded.

## Validation

- [ ] `just node-typecheck`
- [ ] `just node-test`
- [ ] `go test ./internal/cli -run 'TestDaemon|TestUp'`
- [ ] `just fmt`
- [ ] `just test`
- [ ] `just check`

## Risks

- Type-level strictness should reject only `daemon: true` with `isolatedRoot`.
  Accidentally rejecting `daemon: false` would make shared option spreading
  unnecessarily brittle.
- Process inspection differs by OS and may be unavailable in restricted
  environments; diagnostics must be best-effort and must not turn a clear bind
  conflict into a misleading error.
- Overly noisy `gate up` diagnostics could annoy normal users; only show
  cross-state-root hints when the owner is confidently identified as gate.
