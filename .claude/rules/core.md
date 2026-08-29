---
paths:
  - "**/*.go"
---

# Go Core Rules

Style and structure rules for this codebase. The vendored `go` skill
(`.claude/skills/go/SKILL.md`) is the authoritative reference for idiomatic
Go; these rules pin down how this template applies it. The files in
`.claude/rules/` are the single source of truth for conventions —
`README.md` and `CLAUDE.md` only summarize them.

## Package Layout: Features, Not Layers

```
cmd/api/            main + run(); subcommands migrate, adduser; no business logic
migrations/         embedded SQL, append-only, timestamp-prefixed
internal/platform/  shared infrastructure: config, database, logging, web
internal/app/       composition — the only place that wires features
internal/auth/      sessions; knows users only as id strings
internal/user/      feature slice (also owns login — it needs user.Service)
internal/note/      feature slice — copy this one for a new feature
internal/testdb/    test databases
```

- A new feature = a new package `internal/<name>`. Copy `internal/note/` and
  adapt. Keep the file split: `<name>.go` (types + errors), `service.go`,
  `repository.go` (interface), `postgres.go` (implementation), `http.go`
  (routes + handlers), `templates/`, `service_test.go`.
- Layers live as **files inside a feature package**, never as layer-named
  packages (`services/`, `repositories/`, `handlers/`, `models/` are banned).
- Grow a feature with more files, not with subpackages. When a canonical
  file outgrows one topic, split it by resource and keep the canonical name
  as the prefix: `http.go` → `http_share.go`, `postgres.go` →
  `postgres_share.go`. Never reintroduce layer names in file names.
- Never create `utils/`, `helpers/`, `common/`, or `pkg/`.
- A second binary (a worker, a cron job) is a new `cmd/<name>/` that reuses
  `internal/*` and wires itself the same way `cmd/api` does.
- `internal/platform/` holds only application-agnostic infrastructure. It
  must not import any feature.

## New Feature Checklist

1. `just makemigration create_<name>s` — write the schema first
   (see database.md). Mirror value `CHECK`s in Go constants.
2. Copy `internal/note/` to `internal/<name>/`. Rename the types. Write the
   SQL. Keep the file split above.
3. Register the feature in `internal/app/app.go`: construct the service,
   call `<name>.Routes(...)` on the pages mux.
4. Wrap every authenticated route with `auth.RequireIdentity` inside
   `Routes` (see http.md).
5. Write `service_test.go` (business rules + SQL) and extend
   `internal/app/app_test.go` (user-visible flows).
6. `just ci` must pass.

## Dependency Direction & Cross-Feature Calls

- Features import `auth` and `platform`. Features never import each other.
  `auth` imports only `platform`. `app` imports everything and wires it.
  An import cycle means the boundary is wrong.
- UI composition between features happens in the browser (htmx `hx-get`
  embed — see http.md), never through Go imports.
- When feature A needs feature B's **logic**, A declares a small
  consumer-side interface next to its `Service`, and `app.New` passes B's
  service in. This is the same shape as `Repository`:

  ```go
  // internal/order/service.go — order declares what it needs.
  type UserDirectory interface {
      GetEmail(ctx context.Context, userID string) (string, error)
  }

  func NewService(repo Repository, users UserDirectory) *Service { ... }

  // internal/app/app.go — user.Service satisfies it implicitly.
  orders := order.NewService(order.NewRepo(pool), users)
  ```

  Keep the interface minimal: only the methods A calls. If two features
  need each other constantly, merge them — that is one feature.
- A transaction never spans two features (see database.md).

## Dependency Injection

- Explicit constructor injection only: `NewRepo(pool)`, `NewService(repo)`,
  `app.New(logger, cfg, pool)`. Adding a dependency means adding a
  parameter — the compiler then finds every wiring point.
- No DI frameworks, no package-level mutable state, no `init()`.
  The one documented exception: package-level **immutable** template vars
  (`var notesTmpl = web.MustPage(...)`) so bad templates fail at startup.
- Only `config.Load` reads the environment, through its `getenv` argument
  (`testdb` is the test-side exception). Everything else receives `Config`
  or the field it needs.
- `Config.Validate` must reject hard misconfiguration (unknown
  `ENVIRONMENT`, unknown `LOG_LEVEL`, missing `DATABASE_URL` outside
  development). The service refuses to start; it never runs on a silent
  fallback. Extend `Validate` when you add a config field with constraints.
- Repository interfaces are defined in the feature (`repository.go`), next
  to their consumer (`Service`), and implemented in `postgres.go`. Keep
  `var _ Repository = (*Repo)(nil)` as the compile-time check.

## Errors

- Wrap with action context: `fmt.Errorf("inserting note: %w", err)`.
- Each feature defines sentinel errors (`note.ErrNotFound`) and a
  `ValidationError` string type whose text is safe to show to end users.
- Repos translate driver errors (`pgx.ErrNoRows`, PgError 23505) into
  sentinels; nothing above `postgres.go` sees pgx error types.
- Handlers are `web.HandlerE` and **return** their errors (see http.md):
  - `ValidationError` / conflict sentinels → 422 re-render with a message;
  - expected terminal states → `web.BadRequest` / `web.Unauthorized` /
    `web.NotFound` (only `Msg` reaches the client);
  - everything else → `return err`; `web.RespondError` logs it once and
    sends an opaque 500.
- Never log and return the same error. Handlers normally do not log at
  all — `web.E` → `web.RespondError` does the one log call.
- Never `panic` in request paths; `RecoverPanics` is a last resort, not a
  control-flow mechanism.

## Logging

- `log/slog` only, passed as a `*slog.Logger` constructor/function
  argument. Build it only with `logging.New(w, cfg)`
  (`internal/platform/logging`): text in development, JSON otherwise,
  context-aware.
- On request paths, log with the `*Context` methods
  (`logger.ErrorContext(r.Context(), ...)`). The logging handler adds the
  context's correlation attributes to the record: `request_id` (seeded by
  `web.LogRequests`) and `user_id` (seeded by `auth.LoadIdentity`).
  Outside requests (startup, shutdown, CLI), plain `Info`/`Error` is fine.
- Middleware adds correlation attributes with
  `logging.WithAttrs(ctx, slog.String(...))` — never re-attach them by hand
  at call sites.
- SQL queries are logged at `Debug` via the pgx `tracelog` adapter in
  `database.Connect`. Run with `LOG_LEVEL=debug` to see them; they carry
  `request_id`/`user_id` automatically because pgx passes the request
  context through.
- Levels: `Debug` = high-volume internals (incl. SQL), `Info` = lifecycle +
  request log, `Warn` = recoverable oddities, `Error` = needs attention.

## Keep It Denormalized

This template optimizes for readability and agentic editing, not DRY:

- Prefer explicit, repeated code over clever abstraction. Two similar
  handlers are better than one generic one.
- No generic repositories, generic services, or base structs.
- Every SQL query is written out in the `postgres.go` that uses it.

## Use Current Go

- `go.mod` pins the version (currently 1.26) and is authoritative. When
  bumping, keep the Dockerfile (`golang:1.xx`) and CLAUDE.md in sync.
- Use: `any`, `for i := range n`, `min`/`max`, `cmp.Or` for defaults,
  `slices`/`maps`, `math/rand/v2`, `errors.Join`, `crypto/pbkdf2`,
  `http.CrossOriginProtection`, `http.MaxBytesHandler`.
- Loop variables are per-iteration — never write `x := x` captures.
- `gofmt -l -w .` before finishing any task. `just lint` must pass — it
  runs `go vet`, staticcheck (version-pinned in the justfile), and the
  gofmt check.
