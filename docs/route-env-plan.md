# Route Env Plan

## Goal

Add `route_env` to `gate.toml` service config so `gate run` can inject browser-safe local HTTPS route URLs separately from existing loopback runtime URLs.

## Scope

- Keep existing `env` behavior unchanged: service-declared `env` names receive `http://127.0.0.1:<port>`.
- Add service-level `route_env` accepting a string or string array.
- Inject `route_env` names as `https://<service-domain>` in child process environments created by `gate run`.
- Keep existing automatic `GATE_<SERVICE>_ROUTE_URL` behavior unchanged.
- Support `route_env` in Go config parsing, validation, config editing/rendering, CLI run env assembly, docs, agent skill reference, and Node inline config materialization.

## Non-goals

- Do not rename or change `env`.
- Do not add `public_env`.
- Do not model cloudflared, Tailscale, or other externally exposed URLs in this change.
- Do not load `env_files` into child process environments.
- Do not change daemon, registry, DNS, certificate, or proxy routing behavior.

## Constraints

- Response style remains caveman mode until user says `stop caveman` or `normal mode`.
- Continue from the newest user request and current plan state after resume or compaction.
- Protect user work: do not revert unrelated changes.
- Do not commit unless the user asks for commit work.
- Use `just` recipes instead of raw `go` when a recipe exists.
- Run the narrowest relevant checks first, then broader checks when ready.
- Keep documentation boundaries: `docs/spec.md` for semantics, `docs/usage.md` for examples and command behavior, `skills/gate/SKILL.md` for agent operational reference.

## Assumptions

- `route_env` means the local gate route URL, not a tunnel or internet-public URL.
- For internal TLS routes, the injected value includes `https://`.
- `route_env` publishes values for all services in the selected project scope, matching current `env` behavior.
- `route_env` names follow the same validation rules as `env`: valid env identifiers, no `GATE_` prefix, and no duplicate published env names.
- If an env name appears in both `env` and `route_env`, config validation rejects it.
- Node `dist` files are generated from `packages/node/src`; if tracked output must change, regenerate with repo scripts rather than manual edits.

## Work Items

- [x] Extend Go config model with `RouteEnv []string` and TOML key `route_env`.
- [x] Reuse or generalize service env parsing for `env` and `route_env`.
- [x] Validate `route_env` names, reserved prefixes, and duplicate published names across both fields.
- [x] Render `route_env` in config edit helpers when service blocks are generated.
- [x] Update `gate run` env assembly so `route_env` receives `displayDomainURL(res.Domain)`.
- [x] Add CLI regression coverage for simultaneous loopback and route env injection.
- [x] Add config parser and validation coverage for `route_env` string, array, invalid names, reserved prefix, and duplicate names.
- [x] Add Node API support with `routeEnv?: string | string[]`, TOML output as `route_env`, option validation, and tests.
- [x] Update `docs/spec.md`, `docs/usage.md`, `README.md`, `packages/node/README.md`, and `skills/gate/SKILL.md`.
- [x] Run formatting for touched Go and Node files.

## Validation

- [x] `just test`
- [x] `just node-test`
- [x] `just node-typecheck`
- [x] `just docs-check`
- [x] `just fmt-check`
- [x] `just check` before PR or final implementation closeout

## Risks

- Env name collision rules can surprise users if they reuse the same name in `env` and `route_env`; reject early with a clear config error.
- Route URL injection depends on reservation data. Missing reservations should fail like current service-declared `env` behavior.
- `public_env` naming would be ambiguous with tunnel exposure. `route_env` avoids that by tying the field to gate route domains.
- Browser bundlers only expose variables with framework-specific prefixes. Gate can inject `route_env`, but project scripts may still need names such as `VITE_API_BASE_URL` or `NEXT_PUBLIC_API_BASE_URL`.
