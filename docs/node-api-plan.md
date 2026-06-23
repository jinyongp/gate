# Node API Plan

## Goal

Provide an agent-friendly Node API that lets JavaScript automation inspect and
control gate with deterministic installs, stable typed errors, timeout support,
and no separate system-wide gate install requirement.

## Scope

- Add a core Node package under `packages/node`.
- Ship platform-specific optional binary packages consumed by `@gate/node`.
- Execute the gate binary instead of reimplementing gate logic in JavaScript.
- Consume stable `--json` output and expose typed results.
- Provide helpers for type-safe error handling.
- Keep framework and builder integration out of package scope. Child-process
  startup should use the CLI, especially `gate run --up`.

## Public Package

| Package | Purpose |
| --- | --- |
| `@gate/node` | Core JavaScript client and binary carrier |

Internal platform packages:

| Package | Purpose |
| --- | --- |
| `@gate/binary-darwin-arm64` | macOS arm64 gate binary |
| `@gate/binary-darwin-x64` | macOS x64 gate binary |
| `@gate/binary-linux-arm64` | Linux arm64 gate binary |
| `@gate/binary-linux-x64` | Linux x64 gate binary |

## API Shape

```ts
import { createGateClient, isGateError } from "@gate/node";

const gate = createGateClient();
const web = await gate.service("web", { up: true });

try {
  await gate.service("web");
} catch (error) {
  if (isGateError(error, "GATE_DNS_REQUIRED")) {
    // Use a .localhost base or pass dns: "hosts"/"preconfigured".
  }
  throw error;
}
```

The first argument of `service()` is the required service name. The second
argument is an options object.

## Permission Policy

The default high-level service path is:

```text
gate up --json --dns localhost
```

That is valid for `.localhost` service domains and avoids surprise OS
permission prompts. Custom domains fail before mutation with
`GATE_DNS_REQUIRED` unless the caller explicitly chooses `dns: "hosts"` or
`dns: "preconfigured"`.

## Validation

- `pnpm node-api:check` builds, typechecks, and runs package tests.
- `pnpm node-api:pack:dry-run:js` verifies publish contents for `@gate/node`.
- `pnpm node-api:smoke:examples` packs `@gate/node`, installs it into temporary
  example projects, and verifies service lookup plus typed custom-domain errors.
