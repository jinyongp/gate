# prx Specification

`prx` is a local-development HTTPS reverse proxy and port registry, shipped as a
single Go binary. It maps local domains to local dev servers, keeps domain and
port reservations stable across projects, and can expose selected local routes
to other devices for testing.

`prx` is not a production proxy. It is designed for developer machines, local
services, and temporary test exposure.

---

## 1. Product Boundary

### Goals

| Goal | What prx provides |
| --- | --- |
| Local HTTPS by domain | `https://app.localhost` or a custom local domain routes to a local upstream such as `127.0.0.1:4310`. |
| Stable port assignment | A machine-wide registry reserves ports so projects do not hard-code or guess ports. |
| Project-local config | `prx.toml` is the shareable source of truth for a repository's local routes. |
| Standalone mappings | A developer can reserve a domain and port without a project file. |
| Daemon hot reload | A resident proxy can receive new routes without restarting. |
| Script/agent compatibility | Commands keep stdout data separate from stderr diagnostics; JSON output is stable and parseable. |
| Temporary external test access | LAN, Cloudflared, and Tailscale providers can expose selected local services. |

### Non-goals

| Non-goal | Reason |
| --- | --- |
| Production traffic | prx terminates local dev traffic and assumes a developer-controlled machine. |
| Hosted environments | prx is not a server deployment platform. |
| Owning dev server processes | prx can run a child command with `PORT` injected, but it does not manage arbitrary service lifecycles as a process supervisor. |
| Replacing DNS infrastructure | prx only handles local `.localhost`, local hosts-file reflection, and provider-specific exposure workflows. |
| Default public exposure | Any route reachable outside loopback must be explicitly exposed. |

---

## 2. System Overview

```mermaid
flowchart LR
    browser["Browser / tool / device"]
    dns["Local DNS outcome<br/>.localhost or hosts entry"]
    proxy["prx proxy<br/>HTTPS :443 / HTTP :80"]
    route["Atomic route table<br/>host -> upstream"]
    dev["Local dev server<br/>127.0.0.1:PORT"]

    browser -->|"https://app.localhost"| dns
    dns -->|"127.0.0.1"| proxy
    proxy --> route
    route -->|"http://127.0.0.1:4310"| dev
```

```mermaid
flowchart TB
    subgraph control["Control Plane"]
        cli["CLI commands"]
        config["prx.toml<br/>project config"]
        registry["registry.json<br/>machine registry"]
        dnsctl["DNS reflection"]
        daemonctl["Admin socket client"]
    end

    subgraph data["Data Plane"]
        daemon["Resident daemon"]
        https["HTTPS listener"]
        http["HTTP redirect listener"]
        routes["Atomic route map"]
        tls["SNI certificate provider"]
    end

    cli --> config
    cli --> registry
    cli --> dnsctl
    cli --> daemonctl
    daemonctl -->|"PUT /routes over Unix socket"| daemon
    daemon --> https
    daemon --> http
    https --> routes
    https --> tls
```

The CLI computes desired state from project config and the registry. If the
daemon is running, the CLI pushes the active route table through the admin
socket. If the daemon is not running, route reservations still persist and can be
loaded later.

---

## 3. Core Concepts

### Project

A project is a repository with a `prx.toml` file. `prx` discovers the file by
walking upward from the current directory until it finds `prx.toml`, a `.git`
root, the user's home directory, or the filesystem root.

```toml
[project]
name = "myapp"
env_files = [".env.local"]

[services.web]
domain = "app.localhost"

[services.api]
domain = "api.localhost"
port = 3001
```

### Service

A service maps one domain to one upstream port.

| Field | Type | Default | Meaning |
| --- | --- | --- | --- |
| `domain` | string | required | Hostname prx routes. Canonicalized as lowercase without trailing dot. |
| `port` | integer or env string | auto-allocate | Local upstream port. `0` or omitted means allocate from the default pool. |
| `tls` | `internal` or `acme` | `internal` | Certificate provider for the domain. |
| `acme_dns` | string | required for `acme` | DNS-01 provider key. |

`domain` and `port` can include environment references through `${NAME}` or
`${NAME:-fallback}`. Values come from the process environment first, then
`[project].env_files` entries when the variable is not already set.

### Reservation

A reservation is the persisted binding of `project/service -> domain -> port`.
It survives dev server restarts.

```mermaid
stateDiagram-v2
    [*] --> Reserved: prx up / prx add
    Reserved --> Active: route loaded
    Active --> Reserved: prx down
    Reserved --> Removed: prx rm
    Reserved --> Removed: prx prune when owning prx.toml is gone
```

### Active Route

An active route is a reservation currently loaded into the proxy. The `Active`
flag controls whether it is included in the daemon route table. `prx down`
deactivates project routes but preserves reservations.

### Liveness

Liveness is not persisted. prx checks whether a dev server is listening by
dialing the reserved upstream. A reserved service can be `down` when no process
is listening.

---

## 4. Storage Layout

```mermaid
flowchart LR
    project["Project repo"]
    prxtoml["prx.toml<br/>human-owned"]
    cfgdir["Config dir<br/>~/.config/prx"]
    registry["registry.json<br/>tool-owned"]
    sock["prx.sock<br/>admin socket"]
    datadir["Data dir<br/>~/.local/share/prx"]
    ca["root CA and cert cache"]
    statedir["State dir<br/>logs and runtime state"]
    logs["runtime logs<br/>access logs"]

    project --> prxtoml
    cfgdir --> registry
    cfgdir --> sock
    datadir --> ca
    statedir --> logs
```

| Data | Owner | Format | Notes |
| --- | --- | --- | --- |
| `prx.toml` | Human and CLI | TOML | Shareable project config. Edited surgically so comments and surrounding formatting survive. |
| `registry.json` | prx only | JSON | Machine-wide reservations. Uses schema versioning, advisory file locking, and atomic write by temp file + rename. |
| Admin socket | daemon | Unix socket | CLI talks to daemon over a local HTTP API. |
| CA material | prx | PEM files | Root key is private local state and must not be copied. Export only the root certificate. |
| Logs | prx / OS service manager | text or JSONL | Runtime and access logs are separate from command data output. |

Registry schema:

```json
{
  "version": 1,
  "services": {
    "myapp/web": {
      "project": "myapp",
      "service": "web",
      "domain": "app.localhost",
      "port": 4310,
      "tls": "internal",
      "dns": "localhost",
      "active": true,
      "config_path": "/repo/prx.toml"
    }
  }
}
```

---

## 5. Port Management

The default allocation pool is owned by `internal/port`. When a service omits
`port`, prx chooses an available port that is not already reserved and is not
currently bound by the OS.

```mermaid
flowchart TD
    start["service needs port"]
    fixed{"port set in prx.toml?"}
    existing{"existing reservation?"}
    used["build used-port set<br/>registry + OS probes"]
    allocate["allocate from pool"]
    reserve["write reservation"]

    start --> fixed
    fixed -->|"yes"| reserve
    fixed -->|"no"| existing
    existing -->|"yes"| reserve
    existing -->|"no"| used
    used --> allocate
    allocate --> reserve
```

Rules:

- Domains are globally unique on the machine.
- Reserved ports are globally unique on the machine when non-zero.
- Existing reservations keep their ports unless config changes.
- Fixed ports from `prx.toml` win over automatic allocation.
- Port reservation is best-effort; the OS can still let another process bind a
  reserved port while the dev server is down.

---

## 6. DNS Modes

| Mode | When used | Permission | Behavior |
| --- | --- | --- | --- |
| `localhost` | Domains ending in `.localhost` | none | No file changes. Modern resolvers map `.localhost` to loopback. |
| `hosts` | Custom local domains | sudo may be required | prx writes only its managed block in `/etc/hosts`. |

Mode is selected from the domain or forced with `--dns localhost|hosts`.

```text
# prx managed block
127.0.0.1  app.example.test
127.0.0.1  api.example.test
```

Hosts-file editing is guarded by ownership and symlink checks. Permission
failures return exit code `3`.

---

## 7. TLS

```mermaid
flowchart TB
    hello["TLS ClientHello<br/>SNI = app.localhost"]
    provider{"tls provider"}
    internal["internal CA<br/>issue or load leaf cert"]
    acme["ACME DNS-01<br/>issue public cert"]
    cache["certificate cache"]
    handshake["complete TLS handshake"]

    hello --> provider
    provider -->|"internal"| internal
    provider -->|"acme"| acme
    internal --> cache
    acme --> cache
    cache --> handshake
```

### Internal CA

The default provider creates a local root CA and issues leaf certificates for
local domains. Run `prx trust` once to install the root certificate into OS and
browser trust stores.

For another device, export the root certificate:

```bash
prx ca export --out prx-root.crt
```

Never copy or share the root private key.

### ACME

The `acme` provider is for domains the developer actually controls. It uses
DNS-01 so local inbound ports are not required. `acme_dns` identifies the DNS
provider integration.

```toml
[services.api]
domain = "api.dev.example.com"
tls = "acme"
acme_dns = "cloudflare"
```

---

## 8. Proxy Behavior

```mermaid
sequenceDiagram
    participant C as Client
    participant P as prx HTTPS handler
    participant R as Route table
    participant U as Upstream dev server

    C->>P: GET https://app.localhost/path
    P->>R: lookup host
    alt no route
        P-->>C: 404
    else non-loopback and not exposed
        P-->>C: 403
    else auth required and invalid
        P-->>C: 401
    else route exists
        P->>U: reverse proxy to http://127.0.0.1:PORT
        alt upstream down
            P-->>C: 502 with local-service message
        else upstream responds
            U-->>P: response / stream / websocket
            P-->>C: proxied response
        end
    end
```

Implementation notes:

- Route lookup is by canonical host, excluding any request port.
- Route table reload uses `atomic.Pointer`; new requests see the new table,
  in-flight requests keep their current route.
- HTTP requests on the plaintext listener redirect to HTTPS.
- The reverse proxy preserves streaming behavior with immediate flushing.
- WebSocket, SSE, HMR, and HTTP/2 are treated as ordinary reverse-proxy traffic.
- Non-loopback clients are blocked unless the route has been explicitly exposed.
- Optional per-route basic auth is enforced before proxying.

---

## 9. Daemon and Admin Socket

The daemon owns the front proxy listeners. The CLI controls it over a Unix-domain
socket.

```mermaid
flowchart LR
    cli["prx up -d"]
    store["update registry"]
    dns["ensure DNS"]
    start{"daemon running?"}
    launch["start daemon"]
    push["PUT /routes"]
    active["new route table active"]

    cli --> store --> dns --> start
    start -->|"no and --daemon/-d"| launch --> push
    start -->|"yes"| push
    start -->|"no and no -d"| note["print note: no daemon running"]
    push --> active
```

Admin API:

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/status` | Return daemon PID, route count, uptime, and listen addresses. |
| `PUT` | `/routes` | Replace the active route table. |
| `POST` | `/reload` | Reserved reload endpoint; currently reports reload success. |

Only one daemon should own the configured HTTPS/HTTP listen addresses. If a
daemon is already running on different addresses, `prx up -d` refuses with a
conflict.

---

## 10. Command Surface

| Command | Purpose | Data mode |
| --- | --- | --- |
| `prx init` | Scaffold a starter `prx.toml`. | human |
| `prx up [-d]` | Reserve ports, reflect DNS, activate routes, optionally start daemon. | human / JSON |
| `prx down` | Deactivate current project routes and preserve reservations. | human / JSON |
| `prx ls [-a] [--status live|down]` | List reservations and liveness. | human / JSON |
| `prx port [service]` | Print one port or list reserved ports. | human / JSON |
| `prx add <domain> <port>` | Add a project service or standalone reservation. | human / JSON |
| `prx rm <domain>` | Remove a domain reservation and project service block when applicable. | human / JSON |
| `prx rm --project [name]` | Remove project reservations. | human / JSON |
| `prx prune` | Remove reservations whose owning config no longer exists. | human / JSON |
| `prx run <service> -- <cmd>` | Run a child command with `PORT` injected. | child stdio |
| `prx daemon start|stop|restart|status|logs` | Manage the resident proxy. | human / JSON where applicable |
| `prx trust` | Install the local root CA into trust stores. | human / JSON |
| `prx ca export` | Export the local root certificate. | human |
| `prx expose <service> --via <provider>` | Expose a project service through a provider. | human / JSON |
| `prx completion <shell>` | Print shell completion. | script |
| `prx upgrade` | Upgrade to the latest release. | human |
| `prx skill path|print` | Locate or print the bundled agent skill. | human |

Exit codes:

| Code | Meaning |
| --- | --- |
| `0` | success |
| `1` | general error |
| `2` | usage error |
| `3` | permission required |
| `4` | port, domain, or daemon-listen conflict |

---

## 11. Output Contract

```mermaid
flowchart TD
    cmd["command result"]
    json{"--json?"}
    tty{"stdout is TTY<br/>and NO_COLOR unset?"}
    data["stdout: single JSON object/array"]
    rich["stdout: styled human output"]
    plain["stdout: plain human output"]
    diag["stderr: diagnostics, warnings, logs"]

    cmd --> json
    json -->|"yes"| data
    json -->|"no"| tty
    tty -->|"yes"| rich
    tty -->|"no"| plain
    cmd --> diag
```

Rules:

- Program data goes to stdout.
- Diagnostics, warnings, progress, and logs go to stderr.
- `--json` emits one JSON value and no extra text on stdout.
- JSON-mode errors are written to stderr as a JSON error envelope.
- Rich output is enabled only when stdout is a terminal and `NO_COLOR` is unset.
- Piped output stays plain and grep-friendly.

The current presentation layer uses `lipgloss` for TTY-only styling and
borderless tables. There is no fullscreen TUI command in the current public
surface. Any future interactive TUI must keep the same output contract and must
not affect non-TTY or JSON behavior.

---

## 12. Exposure Providers

```mermaid
flowchart LR
    route["project service route"]
    local["local<br/>print local HTTPS URL"]
    lan["lan<br/>same network, .local only"]
    cf["cloudflared<br/>temporary public URL"]
    ts["tailscale<br/>tailnet access"]

    route --> local
    route --> lan
    route --> cf
    route --> ts
```

| Provider | Scope | Requirements | Notes |
| --- | --- | --- | --- |
| `local` | Same machine | active route | No external exposure. |
| `lan` | Same network | `.local` domain, reachable machine, trusted CA on clients | prx validates and marks the route exposed; it does not configure other devices' DNS. |
| `cloudflared` | Public temporary URL | `cloudflared` in `PATH` | Prefer `--auth user:pass`; quick tunnel URL is temporary. |
| `tailscale` | Tailnet | logged-in `tailscale` in `PATH` | Uses Tailscale Serve; detailed teardown is handled with Tailscale commands. |

`prx expose` currently targets project services, not standalone domains.

Security rule: exposing a route is the only way non-loopback clients can pass
the proxy's loopback guard.

---

## 13. Security Model

```mermaid
flowchart TB
    user["User shell"]
    prx["prx CLI"]
    privileged{"privileged operation?"}
    normal["normal user operation"]
    sudo["OS approval / sudo path"]
    hosts["/etc/hosts managed block"]
    trust["OS/browser trust store"]
    key["local root CA key<br/>0600 under private data dir"]

    user --> prx
    prx --> privileged
    privileged -->|"no"| normal
    privileged -->|"yes"| sudo
    sudo --> hosts
    sudo --> trust
    prx --> key
```

Privileged operations:

| Operation | Why permission can be needed | Guardrail |
| --- | --- | --- |
| Trusting root CA | OS/browser trust stores are protected | Trust-store integration is isolated behind seams and uses OS-native mechanisms. |
| Editing `/etc/hosts` | System file | prx edits only its managed block and validates target ownership/symlink state. |
| Binding low ports | `:443` and `:80` can require privileges on some systems | Daemon/service manager owns the listener process. |

Other security properties:

- Root CA private key is local private state.
- Export command writes only the public root certificate.
- Non-loopback clients are blocked unless a route is explicitly exposed.
- Basic auth uses constant-time comparison when configured on an exposed route.
- `internal/truststore` is a vendored, self-contained library and must not import
  prx packages.

---

## 14. Package Architecture

```mermaid
flowchart TB
    paths["internal/paths"]
    config["internal/config"]
    registry["internal/registry"]
    port["internal/port"]
    dns["internal/dns"]
    ca["internal/ca"]
    truststore["internal/truststore"]
    tlsprov["internal/tlsprov"]
    proxy["internal/proxy"]
    daemon["internal/daemon"]
    expose["internal/expose"]
    logx["internal/logx"]
    ui["internal/ui"]
    cli["internal/cli"]
    main["cmd/prx"]

    paths --> config
    paths --> registry
    registry --> cli
    config --> cli
    port --> cli
    dns --> cli
    ca --> tlsprov
    truststore --> ca
    tlsprov --> daemon
    proxy --> daemon
    daemon --> cli
    expose --> cli
    logx --> daemon
    ui --> cli
    cli --> main
```

| Package | Responsibility |
| --- | --- |
| `cmd/prx` | Entrypoint, cobra root command, subcommand dispatch, top-level usage. |
| `internal/cli` | Command parsing, command orchestration, human/JSON output, exit codes. |
| `internal/ui` | TTY-only styling helpers. Presentation tier only. |
| `internal/paths` | XDG/macOS config, data, state, and runtime path resolution. |
| `internal/config` | `prx.toml` discovery, parsing, validation, env interpolation, surgical editing. |
| `internal/registry` | Registry schema, conflict checks, file locking, atomic persistence. |
| `internal/port` | Port allocation, liveness checks, `PORT` env injection, child process run behavior. |
| `internal/dns` | DNS mode selection, `.localhost` no-op, hosts-file provider. |
| `internal/ca` | Root CA creation, leaf issuance, trust/export commands. |
| `internal/truststore` | Vendored trust-store implementation. Self-contained, no prx imports. |
| `internal/tlsprov` | TLS provider abstraction, internal CA provider, ACME/DNS-01 flow. |
| `internal/proxy` | Host-routing reverse proxy, TLS termination hooks, route hot reload. |
| `internal/daemon` | Resident process, admin socket API, lifecycle status. |
| `internal/expose` | Local/LAN/Cloudflared/Tailscale exposure providers and auth handling. |
| `internal/logx` | Runtime logging, access logs, rotation. |

Dependency policy:

| Tier | Allowed dependency shape |
| --- | --- |
| Core data plane and security packages | Prefer stdlib and `golang.org/x`; avoid presentation dependencies. |
| Presentation and CLI packages | May use small, targeted libraries such as `cobra`, `go-toml`, and `lipgloss`. |
| Vendored trust-store code | Must stay self-contained and receive prx-specific behavior through generic seams. |

---

## 15. Development Gates

The project command runner is `just`.

| Recipe | Purpose |
| --- | --- |
| `just build` | Build `bin/prx`. |
| `just test` | Run `go test -race ./...`. |
| `just lint-json` | Emit structured lint diagnostics on stdout and human text on stderr. |
| `just lint` | Run human lint output. |
| `just vuln` | Run `govulncheck ./...`. |
| `just check` | Run tests, lint, and vulnerability scan. Must pass before PR. |
| `just fmt` | Run gofmt and goimports. |

Validation priorities:

- Output contract tests for plain, TTY-gated, `NO_COLOR`, and JSON paths.
- Registry concurrency and atomic-write tests.
- Proxy tests for routing, hot reload, loopback guard, auth, 502 classification,
  streaming, and redirects.
- Trust/hosts privileged paths tested through fakes rather than mutating the
  developer or CI machine.
- `internal/truststore` domain separation: no imports from `prx/internal/...`.

---

## 16. Current TUI Scope

The previous TUI documents were planning artifacts. The current implemented
scope is intentionally smaller and is now part of this spec:

| Area | Current state |
| --- | --- |
| Rich CLI usage | Implemented for TTY output through `internal/ui`. |
| Rich tables/status | Implemented as TTY-only presentation sugar. |
| JSON and pipe output | Plain and stable; rich rendering is disabled. |
| Fullscreen dashboard | Not part of the current command surface. |
| Interactive pickers | Not part of the current command surface. |
| Charts/metrics TUI | Not part of the current command surface. |

If fullscreen or interactive TUI features are added later, they must be specified
in this file before implementation and must preserve the output contract in
section 11.
