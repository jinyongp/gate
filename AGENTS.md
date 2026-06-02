# AGENTS.md

Guide for AI agents working **on the gate codebase**. To use gate as a tool, see
[`skills/gate/SKILL.md`](skills/gate/SKILL.md) instead — do not duplicate usage docs here.

gate = local-dev global HTTPS reverse proxy + port registry, single Go binary.
Design and implementation spec: [`docs/spec.md`](docs/spec.md).

Module path: `gate` (bare; no VCS host dependency).

## Commands

Use `just` recipes instead of raw `go` when a recipe exists.

- `just test`: race-enabled Go tests.
- `just lint`: golangci-lint.
- `just lint-json`: structured lint diagnostics; use for lint-fix work.
- `just check`: test + lint + vuln; run before opening a PR.
- `just fmt`: gofmt + goimports.

For ordinary changes, run the narrowest relevant check first, then `just check`
when the change is ready.

## Source of truth

Use [`docs/spec.md`](docs/spec.md) for architecture, implementation constraints,
platform support, command behavior, output contracts, and exit codes.

Use [`skills/gate/SKILL.md`](skills/gate/SKILL.md) for gate usage docs. Do not
duplicate end-user command examples or JSON schema details here.
