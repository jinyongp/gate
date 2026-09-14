# CLI-Only Distribution Plan

## Goal

Ship gate as one Go CLI whose canonical artifacts live in GitHub Releases.
Homebrew and the standalone installer consume those artifacts. Remove the Node
SDK and every npm publication path from the product and repository.

## Settled Scope

- Keep `gate` as the product, repository, formula, and executable name.
- Keep macOS and Linux support for arm64 and amd64.
- Keep GitHub Release assets and `checksums.txt` as the distribution source of
  truth.
- Keep `brew install jinyongp/tap/gate` as the package-manager installation.
- Keep `scripts/install.sh` as the standalone and CI installation path.
- Keep `GATE_VERSION=vMAJOR.MINOR.PATCH` as the pinned-version installer
  contract.
- Remove the public Node API, npm launcher, npm platform packages, Node examples,
  and npm release job.
- Preserve CLI JSON contracts used by scripts and agents.

## Non-goals

- Do not add another package registry.
- Do not add a replacement JavaScript SDK or npm shim.
- Do not change the Go module path to support `go install`.
- Do not rename the CLI or GitHub repository.
- Do not change supported operating systems or architectures.
- Do not change proxy, registry, DNS, trust, exposure, or daemon behavior.

## Success Criteria

- A release publishes exactly four gate binaries plus `checksums.txt` to the
  matching GitHub Release.
- The Homebrew formula is generated and validated by the reusable
  `jinyongp/homebrew-tap` workflow from those release assets.
- Homebrew installs and tests gate on every declared operating-system and
  architecture pair.
- The standalone installer still installs latest and explicitly selected stable
  versions with checksum verification.
- Repository build, CI, release, documentation, and contributor setup no longer
  require Node.js, pnpm, or npm.
- No active product documentation advertises the removed Node API or npm
  packages.
- Published npm packages remain discoverable with a clear deprecation message,
  but no workflow can publish a new version.

## Work Plan

### 1. Make the CLI the canonical product interface

- Remove the Node integration goal and Node reimplementation non-goal from
  `docs/spec.md`.
- Remove Node-generated config ownership and Node-specific isolated-state rules
  from the specification.
- State that agents and scripts use the CLI's stable JSON output and process
  exit status.
- Keep `gate run`, `gate env --json`, explicit `--config`, and
  `--isolated-root` as the automation surface. These already cover the runtime
  behavior previously wrapped by Node.

Validation: documentation boundary check and a repository search for active
Node API claims.

### 2. Remove npm and Node source surfaces

Delete:

- `packages/node/`
- `packages/binaries/`
- `scripts/node/`
- `examples/node/`
- `package.json`
- `pnpm-lock.yaml`
- `pnpm-workspace.yaml`
- `tsconfig.base.json`
- `.node-version`

Remove all Node recipes from `justfile`. Keep examples such as
`gate run web -- pnpm dev`; there `pnpm` is the user's child command, not a gate
build or distribution dependency.

Validation: `rg` confirms no repository-owned Node build, package, publish, or
SDK path remains.

### 3. Simplify local and CI validation

- Remove the Node check step from `internal/devtool/devcmd/check.go`.
- Remove Node package validation from `scriptsCheck`.
- Remove Node setup/version assertions and `node-check` workflow contracts from
  `internal/devtool/devcmd/scripts.go`.
- Remove Corepack, dependency installation, and `just node-check` from the
  shared preflight action.
- Remove Node status from `.github/scripts/check-summary.sh`.
- Update devtool and workflow tests to assert the Go-only contract.
- Keep Go formatting, vet, race tests, coverage, lint, vulnerability scanning,
  shell validation, actionlint, Linux low-port testing, and cross-build checks.

Validation: targeted devtool tests, `just scripts-check`, then the integrated
`just check` gate.

### 4. Adopt reusable Homebrew GitHub Release publishing

Add `.github/homebrew/formula.yml` with:

- `distribution.type: github-release`
- tag template `v{version}`
- mappings for `gate-darwin-arm64`, `gate-darwin-amd64`,
  `gate-linux-arm64`, and `gate-linux-amd64`
- the current binary install, completion generation, caveats, and version test
  behavior

Replace the custom `homebrew_tap` job with:

```yaml
homebrew_tap:
  needs: [release_tag, build]
  if: |
    needs.release_tag.outputs.tag != '' &&
    needs.release_tag.outputs.on_main == 'true' &&
    needs.build.result == 'success'
  uses: jinyongp/homebrew-tap/.github/workflows/publish-formula.yml@dfe0050e6e8a3f6848c556ac9790ce34976cc64f # automation-v1.4.0
  with:
    formula: gate
    ref: ${{ needs.release_tag.outputs.target }}
    version: ${{ needs.release_tag.outputs.tag }}
  secrets:
    deploy_key: ${{ secrets.HOMEBREW_TAP_DEPLOY_KEY }}
```

The workflow remains pinned to the full commit SHA of an immutable automation
release and keeps its adjacent automation version comment. Regular pull-request
CI runs the same workflow with `dry-run: true` and `validation-mode: spec`, so
Formula contract changes are checked before a release exists.

The release build continues to verify the annotated tag identity before
creating or verifying release assets. GitHub immutable releases must be enabled
before publishing; the publisher refuses mutable releases and requires every
asset to be present at publication time. The Homebrew job starts only after that
build succeeds.

Validation: actionlint and repository contract tests before merge; release-time
validation on macOS and Linux before the reusable workflow pushes the formula.

### 5. Remove obsolete Homebrew and npm release machinery

- Delete the `npm_publish` job from `.github/workflows/release.yml`.
- Remove npm from `release_summary` dependencies and output.
- Delete `.github/scripts/npm-summary.sh`.
- Remove the old custom Homebrew checkout, wait, formula generation, audit,
  install, commit, and push steps now owned by the reusable workflow.
- Remove `wait-release-assets` and `generate-homebrew-formula` from the
  `gate-dev ci` command surface after the workflow no longer calls them.
- Delete their implementation and tests from
  `internal/devtool/cirelease/assets.go` and related test cases.
- Keep release artifact construction, checksums, GitHub Release publication,
  tag identity verification used by publication, and release summaries.
- Update workflow contract tests to require the pinned reusable workflow and to
  reject npm publication steps.

Validation: focused `internal/devtool/cirelease` tests, workflow syntax checks,
and full `just check`.

### 6. Rewrite active user and agent documentation

- Remove the JavaScript/Agents npm section from `README.md`.
- Remove Node/npm contributor prerequisites and release instructions.
- Remove the full Node section and Node-only errors from `docs/usage.md`.
- Rewrite nearby isolation and diagnostic text in CLI terms.
- Remove Node API guidance from `skills/gate/SKILL.md`; keep CLI JSON examples
  and replace package-local `pnpm exec gate` invocations with `gate`.
- Delete `docs/node-agent-run-plan.md`, whose entire subject is the retired SDK.
- Keep mixed completed plans as historical records. They are not normative
  product documentation and should not be rewritten to describe a later state.
- Preserve `pnpm dev` examples where gate is intentionally launching a user's
  Node project.

Validation: link/anchor checks, documentation boundary check, and targeted
searches excluding historical `docs/*-plan.md` files for `@jinyongp/gate`,
`createGateClient`, `GateError`, `resolveGateBinary`, `packages/node`, and npm
publication language.

### 7. Retire published npm packages after the first CLI-only release succeeds

Deprecate every published version of:

- `@jinyongp/gate`
- `@jinyongp/gate-darwin-arm64`
- `@jinyongp/gate-darwin-x64`
- `@jinyongp/gate-linux-arm64`
- `@jinyongp/gate-linux-x64`

Recommended message:

```text
gate is distributed as a standalone Go CLI. Install with `brew install
jinyongp/tap/gate` or see https://github.com/jinyongp/gate#install.
```

Do this only after a new release proves both Homebrew and standalone installs.
Do not unpublish old versions; existing lockfiles should remain reproducible
and the old names should not become reusable supply-chain targets. After
deprecation is visible in the registry, remove or disable npm trusted-publisher
configuration for all five packages.

Validation: `npm view <package> deprecated --json` for all five packages and a
check that the repository contains no npm publication credential or workflow.

## Execution Order

1. Update specification and active documentation to the CLI-only product
   boundary.
2. Remove Node/npm sources and repository toolchain files.
3. Remove Node checks from local validation and CI.
4. Add the Homebrew formula spec and switch to the reusable workflow.
5. Remove obsolete release commands, scripts, summaries, and tests.
6. Run targeted checks, then `just check` on the completed tree.
7. Merge without publishing or deprecating anything.
8. Enable GitHub immutable releases for `jinyongp/gate` and verify the setting.
9. Publish the next stable GitHub Release through the simplified workflow.
10. Verify all four Homebrew platform installs and pinned/latest standalone
    installs.
11. Deprecate the five npm packages and revoke their trusted-publisher paths.

## Validation Matrix

| Area | Check | Required point |
| --- | --- | --- |
| Go behavior | `just test` | before merge |
| Formatting | `just fmt-check` | before merge |
| Lint and vulnerability scan | `just lint`, `just vuln` | before merge |
| Scripts and workflows | `just scripts-check` | after workflow cleanup |
| Integrated repository | `just check` | final pre-merge gate |
| Release assets | four binaries and `checksums.txt` attached to one tag | first CLI-only release |
| Immutable releases | repository setting enabled; published release reports `immutable: true` | before first CLI-only release |
| Homebrew | reusable workflow audits, installs, and tests all four platform pairs | first CLI-only release |
| Standalone latest | installer downloads and verifies latest release | first CLI-only release |
| Standalone pinned | `GATE_VERSION=vX.Y.Z` downloads and verifies selected release | first CLI-only release |
| npm retirement | all five packages show deprecation; no publish path remains | after install verification |

## Risks and Guardrails

- Existing Node SDK users lose a maintained API. Deprecation points them to CLI
  JSON rather than silently breaking old lockfiles.
- Formula publication moves across repository boundaries. Pin the reusable
  workflow by full SHA and retain the release job's tag identity checks.
- Broad deletion can remove legitimate Node-project examples. Distinguish gate's
  former Node toolchain from user commands passed to `gate run`.
- GitHub Release becomes the shared dependency for every install channel.
  Enable GitHub immutable releases before publishing, upload the complete asset
  set before publication, and refuse mutable releases or tag identity
  mismatches.
- npm retirement is irreversible as a product decision. Perform registry
  deprecation only after replacement installation paths pass production release
  checks.
