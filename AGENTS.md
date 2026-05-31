# AGENTS.md

Guide for AI agents working **on the prx codebase**. To use prx as a tool, see
[`skills/prx/SKILL.md`](skills/prx/SKILL.md) instead — do not duplicate usage docs here.

prx = local-dev global HTTPS reverse proxy + port registry, single Go binary.
Design spec: [`README.md`](README.md). Implementation plan: [`docs/IMPLEMENTATION.md`](docs/IMPLEMENTATION.md).

Module path: `prx` (bare; no VCS host dependency). Targets: macOS (arm64/amd64), Linux. Windows unsupported.

## Commands

Command runner is **`just`** (install: <https://github.com/casey/just>). Prefer recipes over raw `go`.

| recipe | action |
| --- | --- |
| `just build` | `go build -o bin/prx ./cmd/prx` |
| `just test` | `go test -race ./...` |
| `just lint` | `golangci-lint run ./...` |
| `just lint-json` | JSON diagnostics → stdout, human text → stderr |
| `just vuln` | `govulncheck ./...` |
| `just check` | test + lint + vuln (**must pass before opening a PR**) |
| `just fmt` | gofmt + goimports |

## Working loop (for agents)

Do **not** "read the code and fix it" blindly. Instead:

1. Run `just lint-json` and parse the JSON diagnostics from stdout.
2. Fix issues based on the structured output.
3. Repeat `just check` until green.

## Lint toolchain

- `golangci-lint` v2, config [`.golangci.yml`](.golangci.yml) — `default: none` + explicitly enabled linters.
- `govulncheck` — vulnerability scan, narrowed to actually-called code.
- `gosec` — security (prx has real surface: `os/exec`, file perms `0600`, `/etc/hosts`, sudo).
- `//nolint` requires a reason comment (enforced by `nolintlint`).

## Package map

```
cmd/prx/            entrypoint, subcommand dispatch
internal/
  paths/            XDG/macOS path resolution
  config/           prx.toml load / discovery / surgical (comment-preserving) write
  registry/         registry.json: flock, atomic write, schema version
  port/             allocation + liveness probe
  dns/              provider: localhost / hosts
  ca/               root CA gen, leaf issue
  truststore/       vendored smallstep/truststore (standalone, no prx import)
  tlsprov/          provider: internal / acme, SNI cert cache
  proxy/            reverse proxy data plane, route table, hot reload
  daemon/           lifecycle, admin socket, service manager
  expose/           provider: local / lan / cloudflared / tailscale
  logx/             slog setup, access log, rotation
  cli/              command parsing, human/json output
skills/prx/         agentskills.io skill (usage docs — not a dev concern)
```

## Conventions

- **stdlib first.** Dependencies are two-tier (see `docs/IMPLEMENTATION.md` 부록 B):
  - core (proxy / TLS / CA / network): stdlib + `golang.org/x` only.
  - presentation (CLI / config): minimal third-party.
- **Output split (pipe-safe):** program data → stdout; logs/diagnostics → stderr. `--json` emits a
  single object/array and nothing else.
- **Domain separation:** `internal/truststore` is a self-contained vendored library — it must **not**
  import prx packages. Inject prx behavior (logging, privileged-exec hardening) through its generic
  seams (`WithLogger`, `WithElevator`).
- Exit codes: `0` ok / `1` error / `2` usage / `3` permission (sudo) / `4` port·domain conflict.

## Out of scope here

prx usage, command examples, and the end-user `--json` schema live in `skills/prx/SKILL.md`.
