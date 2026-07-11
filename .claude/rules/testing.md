---
paths:
  - "**/*_test.go"
  - "testdb/**/*.go"
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
  (`just test` does this for you). If it's down, tests fail with instructions.
- Schema changes are picked up automatically: the template name includes a
  hash of the embedded migrations.

## What to Test Where

- `api/*_test.go` — behavior through the **full server**: build it with
  `api.NewServer` exactly as `main` does (middleware + auth + routes), execute
  with `httptest.NewRecorder`. Covers status codes, envelopes, validation
  problems, auth (401), and 404s.
- `domains/<name>/service_test.go` — business logic + SQL against a real database
  (service tests exercise the repo; don't duplicate them with repo-only tests
  unless a query has behavior the service doesn't reach).
- `auth/` — pure middleware unit tests with `httptest`, no database.

## Conventions

- Table-driven tests with `t.Run` for input variations; separate test
  functions for distinct behaviors. Name as `Test<Thing><Behavior>`.
- Use external test packages (`package api_test`) — test through the public
  API.
- Helpers take `t *testing.T`, call `t.Helper()`, and fail with `t.Fatalf`.
- Use `t.Context()` instead of `context.Background()` inside tests, and
  `t.Cleanup` instead of manual teardown.
- Compare structs with `github.com/google/go-cmp/cmp.Diff`, not
  `reflect.DeepEqual`.
- Never `time.Sleep` to wait for anything; synchronize explicitly.
- Test files sit next to the code they test — there is no separate `tests/`
  tree.
