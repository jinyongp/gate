# Agent Report Polish Plan

## Goal

Make gate's agent-facing JSON contracts easier to consume without changing the
core route/run model. Clarify that `doctor` is a setup and state-health command,
not a normal Node API dependency for `service`, `ready`, or `run`.

## Scope

- Normalize `doctor --json` so successful reports emit `issues: []` instead of
  `issues: null`.
- Add additive descriptor metadata that helps agents summarize a service without
  inspecting secret or noisy values, starting with `envKeys`.
- Separate descriptor diagnostics from recommended actions with additive
  structured fields while keeping existing fields compatible.
- Improve `gate run --up` parent hints so route readiness and fixable
  diagnostics are visible without touching child stdout.
- Audit JSON-mode error paths for agent-facing commands and fix raw stderr or
  usage paths that bypass the error envelope.
- Clarify docs and skill guidance for agent state policy:
  - isolated root for sandboxed inspection, tests, and temporary automation;
  - normal user gate state for real dev app execution when the app must share
    user routes, certs, and daemon state.
- Expand `route_env` recipes so apps prefer gate-calculated route URLs over
  hardcoded HTTPS URLs.
- Clarify `doctor` usage:
  - use `gate doctor --json` in install/setup/CI/preflight checks;
  - do not call `doctor` from normal Node `service()`, `ready()`, or `run()`
    flows;
  - Node callers should handle command errors through `GateError` metadata and
    only run `doctor` when they are explicitly diagnosing local gate state.

## Non-goals

- Do not make isolated state the default for normal CLI or Node use.
- Do not make Node API call `doctor` implicitly.
- Do not replace `GateError` with `doctor` reports.
- Do not remove existing descriptor fields such as `suggestedCommand`.
- Do not print route or diagnostic hints to child stdout from `gate run`.
- Do not change Streamliner or other external report shaping code in this
  repository.

## Constraints

- Preserve caveman response style in conversation until the user says
  `stop caveman` or `normal mode`; do not apply that style to docs or code.
- Preserve public JSON compatibility: field changes must be additive except for
  replacing nullable empty slices with stable empty arrays.
- Keep stdout/stderr contract: command data on stdout, diagnostics on stderr,
  child stdout untouched by `gate run`.
- Keep docs boundaries from `AGENTS.md`: product semantics in `docs/spec.md`,
  command output and usage semantics in `docs/usage.md`, concise operational
  agent guidance in `skills/gate/SKILL.md`.
- Use `just` recipes for validation when available.
- Do not commit unless the user asks.

## Assumptions

- Automation consumers benefit more from stable additive fields than from a new
  command.
- `doctor` is most valuable before or after a failure, not on every route/env
  lookup.
- `issues: []` is safer for consumers than `issues: null` and is compatible
  with the existing documented shape.
- `envKeys` should expose only sorted names, not redact or transform existing
  `env` values.

## Work Items

- [x] Update `doctor --json` report construction so empty issue lists encode as
      `[]`.
- [x] Add tests for empty `doctor --json` issue lists and non-empty issue
      reports.
- [x] Add `envKeys` to the shared run/env descriptor and Node type definitions.
- [x] Add descriptor tests proving `envKeys` is sorted and matches the `env`
      map keys.
- [x] Add structured diagnostic actions to descriptor diagnostics while keeping
      `suggestedCommand` for compatibility.
- [x] Update `gate run --up` stderr hints to include fixable descriptor
      diagnostics without polluting child stdout.
- [x] Audit agent-facing JSON error paths for `env`, `up`, `ls`, `port`, `run`,
      `daemon status`, and `doctor`; fix bypasses found in scope.
- [x] Update `docs/usage.md` with descriptor additions, diagnostic action
      semantics, and the clarified `doctor` role.
- [x] Update `packages/node/README.md` and Node docs/types to state that normal
      API flows do not call `doctor`; `doctor` belongs to explicit setup or
      preflight diagnostics only.
- [x] Update `skills/gate/SKILL.md` with the same agent policy in shorter form.
- [x] Add or expand `route_env` recipes for common dev app cases that should use
      gate-calculated route URLs.

## Validation

- [x] Run targeted Go tests for CLI descriptor, doctor, and run output paths.
- [x] Run targeted Node package tests for descriptor types/API parsing.
- [x] Run `just docs-check`.
- [x] Run `just test` if Go changes span shared CLI behavior.
- [x] Run `pnpm -C packages/node test` or the repo's Node package test command
      if Node types or README examples change.
- [x] Run `just check` before closeout if implementation touches CLI, docs, and
      Node together.
- [x] Run review-loop after validation and fix in-scope findings until clean.

## Risks

- Adding action fields may create two ways to express the same suggestion.
  Mitigation: document `actions[]` as preferred and `suggestedCommand` as
  compatibility.
- `gate run --up` hint expansion could become noisy. Mitigation: keep one route
  line plus concise fixable diagnostics, and preserve `--quiet`.
- `envKeys` may be mistaken for a replacement for `env`. Mitigation: document it
  as summary metadata only.
- Doctor role can remain confusing if docs imply it is a general health check
  for every API call. Mitigation: explicitly state that normal Node flows rely
  on command-specific JSON and `GateError`, not `doctor`.
