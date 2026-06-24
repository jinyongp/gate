# Follow-ups

This file tracks useful follow-up work that is not required for the current
documentation boundary cleanup.

## Documentation

### Output Reference Shape

Problem: JSON and text output behavior is documented in prose, but there is no
single reference for scripts that need field-level contracts.

Suggested work:

- Decide whether `docs/usage.md` is enough for output contracts, or whether a
  dedicated `docs/reference.md` should hold JSON shapes and status values.
- If a reference file is added, link to it from usage and keep spec limited to
  stdout/stderr and automation-safety principles.

### Documentation Ownership Check

Problem: contributors need a low-friction way to decide which document to edit.

Suggested work:

- Add a small documentation checklist to PR review notes or contributor docs:
  product invariant -> spec, user command/output detail -> usage, agent shortcut
  -> skill, future idea -> follow-up document.

## Tooling

### Command Reference Drift

Current guard: `just docs-check` verifies the `docs/usage.md` Quick Reference
command rows and public flags against rendered CLI help.

Remaining problem: longer usage examples, completion-specific documentation,
and command help can still drift as command behavior changes.

Suggested work:

- Decide whether longer command examples should stay manually reviewed or get a
  separate narrow lint.
- Add a docs check for completion-specific reference text if completion behavior
  starts drifting from `docs/usage.md`.
- Keep exact command syntax out of `docs/spec.md`; usage remains the user-facing
  reference.

## Implementation

### Public TLS Scope Guard

Current guard: public TLS behavior is limited to gate's internal local CA.
Unsupported public TLS provider config fields fail validation instead of being
treated as supported options.

Suggested work:

- Add a public-docs check that rejects unsupported public TLS provider wording
  outside intentional developer/internal contexts.
