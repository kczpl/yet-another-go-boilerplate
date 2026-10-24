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
  hash of the embedded migrations. Setup and cleanup have finite deadlines.
  The postgres-test service uses the test profile and tmpfs.
- `testdb.New` opens the pool with `database.Connect`, so tests get the
  production pool settings, for example `statement_timeout`.

## The Race Detector

- `just test` runs `go test -race ./...`. The race detector is always on;
  code that fails under `-race` is broken, not "flaky".
- Never "fix" a race with `time.Sleep` — synchronize explicitly.

## What to Test Where

- `internal/app/*_test.go` — user-visible flows through the **full
  handler**: build it with `app.New` exactly as `cmd/api` does (middleware +
  sessions + CSRF + routes), drive it with `httptest`, assert on status
  codes, redirects, and rendered HTML. Test both paths for every mutation:
  htmx (fragment swap) and the plain form (redirect / full page) —
  including delete (`POST /notes/{id}/delete`). The files:
  - `client_test.go` — the test client and the shared helpers. Use them
    instead of a hand-made request or recorder:
    - Build: `newClient`, `newClientWithModels`, `register`.
    - Send: `get`, `postForm` (the no-JS path), `htmxGet` and `htmxPost`
      (the htmx path), `do`, and `send` with `newRequest` for custom
      headers.
    - Check: `wantStatus`, `wantContains`, `wantLocation`, `wantBody`
      (an exact body, such as an event stream), `wantPage` (a full
      page), and `wantFragment` (a bare htmx fragment).
  - `auth_test.go` — login, logout, the session cookie flags, the login
    rate limit, and the guard.
  - `profile_test.go`, `notes_test.go`, `chat_test.go`,
    `triage_test.go` — one file per feature. A new feature adds
    `internal/app/<name>_test.go`. `profile_test.go` also has
    `TestDashboardEmbedsFeatures`: each dashboard card loads its fragment
    with htmx and falls back to a full page.
    `chat_test.go` checks the exact event stream, the no-JS page, a model
    failure on both paths, and the chat rate limit.
    `TestChatOutlivesWriteTimeout` runs the full handler
    on a real server with a short `WriteTimeout`, because only a real
    connection has a write deadline.
  - `models_test.go` — `app.NewModels`: both default models answer, each
    provider registers with its key or its URL, and a model of a provider
    without its key stops startup.
  - `worker_test.go` — `app.NewWorker` as `cmd/api worker` wires it: the
    hourly schedule enqueues the session cleanup, and the cleanup runs.
  - `health_test.go` — the probes and the static assets.
  - `security_test.go` — the security invariants.
- `newClient` loads the production config (an empty environment), so the
  tests see the `__Host-` cookie and HSTS exactly as a deployment does.
  The test client carries the cookie itself and ignores the `Secure`
  flag, so the flows still work over plain HTTP.
- The security invariants have regression tests in `security_test.go`:
  CSRF rejection, the security headers (HSTS included), `no-store` on
  private pages, the request body cap, and the unified 404 (HTML and
  JSON). The cookie flags and the login rate limit have tests in
  `auth_test.go`. When you change the middleware chain or the error path,
  extend these tests — do not delete them to make a change pass.
- `internal/<feature>/service_test.go` — business logic + SQL against a real
  database. Service tests exercise the repo; don't duplicate them with
  repo-only tests unless a query has behavior the service doesn't reach.
- A feature without storage (`internal/chat`, `internal/triage`) tests
  its service with a stub `llm.Provider` (or `systemone.Provider`) and no
  database.
- A provider adapter (`internal/platform/llm/anthropic`,
  `internal/platform/llm/chatcompletions`,
  `internal/platform/systemone/typesafe`) tests the request mapping, the
  response parsing, and the error path against an `httptest.Server`. An
  error must not contain the key or the request text.
  `TestNewIgnoresEnvironment` uses `t.Setenv`, so it does not run in
  parallel. `TestNewKeepsTheBasePath` checks the Bedrock route
  (`/anthropic/`).
- The job queue (`internal/platform/jobs`) tests against a real database.
  A test drives the worker with `RunDue`: it enqueues the due schedule
  slots, runs the cleanup, and then runs the due jobs one at a time. A
  test never waits for a backoff or a lease. It moves `run_at` or
  `locked_until` with SQL (`makeDue`). `export_test.go` gives the tests
  `SetClock` (the clock of the schedules), `SetShutdownGrace`, and
  `CleanupBatch`. Keep the tests of these invariants: many workers run
  each job once, a stale worker cannot store a result, an expired lease
  returns the job, the cleanup works in batches, each schedule slot
  enqueues one job, the logs omit the payload, and a shutdown finishes the
  running jobs or returns them without the cost of an attempt.
- A feature with jobs tests the rules in `service_test.go` and the
  wiring through `app.NewWorker` and `RunDue` in
  `internal/app/worker_test.go`.
- Tests never call a live model. The production config of the tests uses
  `fake/echo` (`llm.Fake`, no delay) and `fake/uniform`
  (`systemone.Fake`). To test a failure, pass a stub provider to
  `newClientWithModels`: `newClientWithModels(t, app.Models{LLM:
  failingModel{}})`. A nil field keeps the configured model.
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
- Use `t.Context()` for test operations and `t.Cleanup` for cleanup.
  The test context is canceled before cleanup starts. Cleanup that uses
  the database must use a fresh context with a finite deadline.
- Never `time.Sleep` to wait for anything; synchronize explicitly. The
  exceptions are `TestEventStreamOutlivesServerTimeouts` and
  `TestChatOutlivesWriteTimeout`: they must wait past a real server
  deadline, so the deadline itself is the condition.
- Test files sit next to the code they test — there is no separate `tests/`
  tree.

## CI

- `.github/workflows/ci.yml` runs `just ci` (lint, types, complexity, tests,
  and vulncheck) on main pushes and pull requests, then builds the Docker
  image. `just test` passes extra arguments safely and disables result
  caching so database tests run on every invocation.
- Keep the job queue tests (see What to Test Where).
- Keep migration tests for rollback, parallel calls, late files,
  cancellation, and the statement timeout (a migration that runs longer
  than the pool's `statement_timeout` must still succeed). Keep HTTP tests
  for database failures, the probes, and htmx cache headers.
- Browser verification complements httptest. HTTP tests cannot detect CSP
  violations or a broken DOM swap. Use a browser when the htmx flow changes.
