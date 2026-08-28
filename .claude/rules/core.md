---
paths:
  - "**/*.go"
---

# Go Core Rules

Style and structure rules for this codebase. The vendored `go` skill
(`.claude/skills/go/SKILL.md`) is the authoritative reference for idiomatic
Go; these rules pin down how this template applies it.

## Package Layout: Features, Not Layers

```
cmd/api/            main + run(); no business logic
migrations/         embedded SQL, append-only
internal/platform/  shared infrastructure: config, database, web
internal/app/       composition — the only place that wires features
internal/auth/      sessions; knows users only as id strings
internal/user/      feature slice (also owns login — it needs user.Service)
internal/note/      feature slice — copy this one for a new feature
internal/testdb/    test databases
```

- A new feature = a new package `internal/<name>`. Copy `internal/note/` and
  adapt. Keep the file split: `<name>.go` (types + errors), `service.go`,
  `repository.go` (interface), `postgres.go` (implementation), `http.go`
  (routes + handlers), `templates/`.
- Layers live as **files inside a feature package**, never as layer-named
  packages (`services/`, `repositories/`, `handlers/`, `models/` are banned).
- Grow a feature with more files, not with subpackages.
- Never create `utils/`, `helpers/`, `common/`, or `pkg/`.
- Dependency direction: features import `auth` and `platform`. Features never
  import each other. `auth` imports only `platform`. `app` imports everything
  and wires it. An import cycle means the boundary is wrong.
- `internal/platform/` holds only application-agnostic infrastructure. It
  must not import any feature.

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
- Repository interfaces are defined in the feature (`repository.go`), next
  to their consumer (`Service`), and implemented in `postgres.go`. Keep
  `var _ Repository = (*Repo)(nil)` as the compile-time check.

## Errors

- Wrap with action context: `fmt.Errorf("inserting note: %w", err)`.
- Each feature defines sentinel errors (`note.ErrNotFound`) and a
  `ValidationError` string type whose text is safe to show to end users.
- Repos translate driver errors (`pgx.ErrNoRows`, PgError 23505) into
  sentinels; nothing above `postgres.go` sees pgx error types.
- Handlers map errors to responses: `ValidationError`/conflict sentinels →
  422 re-render with a message; everything else → `web.InternalError`
  (log once, opaque 500). Never log and return the same error.
- Terminal statuses (400/401/404) use the unified path in `platform/web`:
  `web.HTTPError` + `web.E`/`web.RespondError` — one format for HTML and
  JSON clients. Put only client-safe text in `Msg`; the cause goes in `Err`.
- Never `panic` in request paths; `RecoverPanics` is a last resort, not a
  control-flow mechanism.

## Logging

- `log/slog` only, passed as `*slog.Logger` constructor/function argument.
  Build it only with `logging.New(w, cfg)` (`internal/platform/logging`):
  text in development, JSON otherwise, context-aware.
- On request paths, log with the `*Context` methods
  (`logger.ErrorContext(r.Context(), ...)`). The logging handler then adds
  the context's correlation attributes to the record: `request_id` (seeded
  by `web.LogRequests`) and `user_id` (seeded by `auth.LoadIdentity`).
  Outside requests (startup, shutdown), plain `Info`/`Error` is fine.
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
  `http.CrossOriginProtection`.
- Loop variables are per-iteration — never write `x := x` captures.
- `gofmt -l -w .` before finishing any task; `go vet ./...` must be clean.
