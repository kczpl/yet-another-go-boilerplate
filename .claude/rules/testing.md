---
paths:
  - "**/*_test.go"
  - "internal/testdb/**/*.go"
---

# Testing Rules

Tests run against a **real PostgreSQL** — never mock the database.

## The testdb Pattern

```go
pool := testdb.New(t) // private, fully migrated database; dropped on cleanup
```

- `testdb.New` clones a migrated template database (`CREATE DATABASE ...
  TEMPLATE`, a few ms) so every test owns a pristine database. Commits are
  fine; there is no shared state and no rollback trickery.
- `t.Parallel()` is safe and encouraged — databases are independent.
- Requires the compose service: `docker compose up postgres-test -d`
  (`just test` does this for you). If it's down, tests fail with
  instructions. `POSTGRES_TEST_PORT` overrides the port (default 5433).
- Schema changes are picked up automatically: the template name includes a
  hash of the embedded migrations.

## The Race Detector

- `just test` runs `go test -race ./...`. The race detector is always on;
  code that fails under `-race` is broken, not "flaky".
- Never "fix" a race with `time.Sleep` — synchronize explicitly.

## What to Test Where

- `internal/app/app_test.go` — user-visible flows through the **full
  handler**: build it with `app.New` exactly as `cmd/api` does (middleware +
  sessions + CSRF + routes), drive it with `httptest`, assert on status
  codes, redirects, and rendered HTML. Test both paths for every mutation:
  htmx (fragment swap) and the plain form (redirect / full page) —
  including delete (`POST /notes/{id}/delete`).
- The security invariants have regression tests in `app_test.go`: CSRF
  rejection, the security headers, the request body cap, and the unified
  404 (HTML and JSON). When you change the middleware chain or the error
  path, extend these tests — do not delete them to make a change pass.
- `internal/<feature>/service_test.go` — business logic + SQL against a real
  database. Service tests exercise the repo; don't duplicate them with
  repo-only tests unless a query has behavior the service doesn't reach.
- Pure helpers (e.g. password hashing) get white-box tests in the same
  package when the functions are deliberately unexported.

## Conventions

- Table-driven tests with `t.Run` for input variations; separate test
  functions for distinct behaviors. Name as `Test<Thing><Behavior>`.
- Use external test packages (`package note_test`) — test through the public
  API. White-box tests are the documented exception above.
- Helpers take `t *testing.T`, call `t.Helper()`, and fail with `t.Fatalf`.
- Seed rows from other features with raw SQL helpers (see `seedUser` in
  `internal/note/service_test.go`) — feature tests must not import other
  feature packages.
- Use `t.Context()` instead of `context.Background()` inside tests, and
  `t.Cleanup` instead of manual teardown.
- Never `time.Sleep` to wait for anything; synchronize explicitly.
- Test files sit next to the code they test — there is no separate `tests/`
  tree.

## CI

- `.github/workflows/ci.yml` runs `just ci` (lint + test, with the
  `postgres-test` compose service) plus `govulncheck` on every push and
  pull request. Keep `just ci` green locally before you push — CI runs
  the same recipes, nothing more.
