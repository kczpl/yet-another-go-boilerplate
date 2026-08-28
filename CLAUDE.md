# CLAUDE.md

Minimal feature-based Go web service template: stdlib `net/http` (Go 1.22+
mux), PostgreSQL via pgx, server-rendered HTML + htmx (vendored), hand-rolled
embedded migrations, slog, and tests against a real database.
No frameworks, no ORM, no DI container — explicit wiring in `internal/app`.

## Quick Reference

- Go 1.26 · router: stdlib `http.ServeMux` · DB: PostgreSQL 18 + `jackc/pgx/v5`
- **pgx is the only direct dependency.** Sessions, PBKDF2 password hashing,
  CSRF (`http.CrossOriginProtection`), templates, migrations: all stdlib.
- Frontend: `html/template` + htmx 2 (embedded in the binary, no JS build)
- Migrations: embedded SQL in `migrations/`, applied on startup (append-only)
- Logging: `log/slog` via `logging.New` (text in development, JSON otherwise);
  `*Context` methods pick up `request_id`/`user_id` from the request context;
  `LOG_LEVEL=debug` also logs SQL
- Task runner: `just` (see `justfile`)

## Rules & Skills

Coding rules live in `.claude/rules/` and auto-load when editing matching
files. **Read the relevant rule before editing:**

| File | Covers |
|---|---|
| `core.md` | Package layout (features, not layers), DI, errors, logging |
| `http.md` | Routes/handlers, htmx fragments vs redirects, templates, auth |
| `database.md` | Repos, SQL conventions, UUIDv7, migrations |
| `testing.md` | testdb (real Postgres per test), what to test where |
| `security.md` | Session/password/CSRF invariants, input rules, pre-production checklist |

`.claude/skills/go` (vendored from spf13/go-skills) is the idiomatic-Go
reference and loads automatically for any Go work.

## Comments

Write all comments in ASD-STE100 Simplified Technical English (STE). STE is
a controlled natural language that is designed to simplify and clarify
technical documentation. Apply the core STE rules:

- Write one instruction or one idea per sentence.
- Use the active voice. Use the passive voice only when the actor is
  unknown or irrelevant.
- Use simple verb forms only: imperative, simple present, simple past,
  simple future.
- Do not use gerunds as nouns. Keep noun clusters to 3 words or fewer.
- Use plain, common words. Use one consistent term per concept.
- Write full sentences with a subject, a verb, and articles.

This rule applies to Go comments, SQL comments, template comments, and
shell comments. Go doc comments still start with the symbol name
(`// New builds ...`).

## Architecture

Request flow: `internal/app (mux + middleware) → internal/<feature>/http.go → service.go → repository.go ← postgres.go`

```
cmd/api/          main + run(): config → pool → migrate → app.New → server
migrations/       embedded *.sql + Hash(); the single source of schema truth
internal/
  platform/       shared infra; imports no feature
    config/       Config, Load(getenv), Validate
    database/     Connect (pgx pool + SQL logging) + Migrate (advisory lock)
    logging/      logging.New + WithAttrs (context-carried log attributes)
    web/          layout.html, MustPage/RenderPage/RenderFragment, IsHTMX,
                  unified errors (HTTPError → error page or JSON),
                  static assets, LogRequests/RecoverPanics middleware
  app/            app.New(logger, cfg, pool) http.Handler — the only wiring point
  auth/           sessions + LoadIdentity/RequireIdentity; knows only user ids
  user/           example feature: login + /me dashboard (owns the
                  login flow; embeds the notes section via hx-get, not imports)
  note/           example feature: the one to copy for a new feature
  testdb/         per-test isolated databases cloned from a migrated template
```

A feature package owns, as files: `<name>.go` (types + errors), `service.go`,
`repository.go` (interface), `postgres.go` (implementation), `http.go`
(`Routes(mux, ...)` + handlers), `templates/`, tests. Copy `internal/note` for
a new feature, then register it in `internal/app/app.go`.

Dependency direction: features import `auth` and `platform`; features never
import each other; `auth` imports only `platform`; `app` imports everything.

## Development

```bash
just compose         # full stack in Docker (api + postgres)
just app             # run API on the host (needs: docker compose up postgres -d)
just build           # production binary → bin/api
just fmt             # gofmt
just lint            # go vet + gofmt check
just test            # spins up postgres-test, runs go test ./...
just migrate         # apply migrations (also happens on startup)
just makemigration create_toys_table
just ci              # lint + test
```

App at `http://localhost:8080`. There is no register page — create accounts
with `user.Service.Register` from code you control, then log in and use
`/me` and `/notes`.

## Testing

Needs PostgreSQL (no DB mocks): `just test` starts the `postgres-test`
compose service. Each test gets its own database via `testdb.New(t)` —
isolated, migrated, dropped afterwards; `t.Parallel()` is safe. HTTP tests
drive the full `app.New` handler and assert on rendered HTML.

## Database

- PostgreSQL 18, UUIDv7 primary keys (`DEFAULT uuidv7()`), `timestamptz`
  everywhere. IDs are plain `string` in Go — no uuid library.
- No ENUMs — `text` + `CHECK`. Every `UPDATE` sets `updated_at = now()`.
- Always add a migration for schema changes (`just makemigration ...`);
  migrations are append-only — never edit an applied file.
