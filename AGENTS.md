# AGENTS.md

## Code style

No comments. Names must express intent; a comment that restates the name adds noise, not signal.
Only add a comment when a constraint, invariant, or workaround would surprise a future reader and cannot be named away.

Clean Code principles apply throughout:
- Single Responsibility: one reason to change per type or function.
- Meaningful names: reveal intent, avoid noise words and abbreviations.
- Small functions: if a function needs a mental map to follow, extract.
- No duplication: the same logic in two places is a future bug waiting to diverge.
- Fail fast: validate at system boundaries; trust internal guarantees.

## 12-factor

- Config is injected via constructors; packages never call `os.Getenv` directly.
- Process state is held in `atomic.Pointer` snapshots; nothing is mutated in place after store.
- Backing services (audit sink, auth provider, clock) are attached via interfaces; swap without code change.
- Logs go to stdout as structured JSON lines; the runtime decides where they land.

## Patterns in use

- **Immutable value object** (`RuntimeConfig`): always swapped whole via `atomic.Pointer`, never mutated.
- **Parameter object** (`auditArgs`): groups related arguments to avoid long parameter lists.
- **Strategy** (`Authenticator`, `AuditSink`, `Clock`): inject behaviour; test with fakes, not mocks.
- **Sentinel errors**: `errors.New` at package level; wrap context with `fmt.Errorf("%w", ...)`.

## Testing

- Unit tests live in the same package (`package runtimeconfig`) for whitebox access.
- Build tag `//go:build integration` for tests that require real I/O; `//go:build e2e` for full-stack.
- Test doubles are plain structs in `_test.go`; no mocking framework.
- Table-driven tests with `t.Run` for behaviour variations.
- Every assertion helper calls `t.Helper()`.

## Packages and modules

- `internal/` packages are not part of the public API; break them freely.
- Interfaces are accepted as parameters, not returned from constructors.
- No package-level mutable state; all state lives in structs with explicit lifetimes.

## Branching and PRs

- Branch name: `feature/<issue>-<slug>` for auto-link to GitHub issue.
- One logical change per PR; keep diffs reviewable.
- `release/*` → `main` requires human approval; CI must be green before merge.
