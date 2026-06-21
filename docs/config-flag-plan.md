# Config Flag Plan

## Goal

Allow project-mode commands to use an explicit TOML config path through
`--config`, while keeping existing `gate.toml` discovery as the default.

## Scope

- Add a shared CLI option for commands that resolve the current project config.
- Load the explicit config path directly when `--config` is present.
- Store the explicit path in registry reservations the same way discovered
  `gate.toml` paths are stored today.
- Update usage docs and command help.
- Add regression tests for explicit config paths and default discovery.

## Non-goals

- Do not change the default project config filename.
- Do not make `gate init` scaffold arbitrary filenames.
- Do not change the TOML schema.
- Do not change global reservation behavior.

## Constraints

- Existing behavior without `--config` must remain byte-for-byte compatible where
  output text is not intentionally documented.
- Relative `--config` paths resolve against the process working directory.
- `env_files` continue to resolve relative to the selected config file.
- Explicit config selection must not require a nearby `.git` root or `gate.toml`.
- Do not commit unless the user asks.
- Caveman response style remains active until the user says `stop caveman` or
  `normal mode`.
- Continue from current plan/task state after resume or context compaction.
- Protect unrelated user work.

## Assumptions

- `--config` applies to commands that read or mutate project-scoped config and
  reservations.
- Existing scope flags such as `--global`, `--project`, and `--all` stay mutually
  exclusive with current semantics.
- When `--config` and `--project <name>` are both present, the loaded config must
  belong to the named project.

## Work Items

- [x] Add shared explicit config path handling in CLI project resolution.
- [x] Wire `--config <path>` into project-aware commands.
- [x] Update help, completion metadata, and docs.
- [x] Add tests for explicit config load, mutation, and run-env behavior.

## Validation

- [x] Run focused Go tests for config and CLI command behavior.
- [x] Run `just test` after implementation.
- [x] Run `just lint` if code shape changes beyond tests/docs.

## Risks

- `DisableFlagParsing` means root-level flags after a subcommand are not parsed by
  Cobra, so command-level parsing must be deliberate.
- Commands that can operate globally must keep fallback-to-global behavior when no
  project config is discoverable and `--config` is absent.
- Explicit paths stored in the registry affect prune behavior if the selected
  config later moves or is deleted.
