# CLAUDE.md

Minimal Go HTTP backend boilerplate: stdlib `net/http` (Go 1.22+ mux),
PostgreSQL via pgx, goose migrations, slog, and tests against a real database.
No frameworks, no ORM, no DI container — explicit wiring in `main.go`.

## Quick Reference

- Go 1.25 · router: stdlib `http.ServeMux` · DB: PostgreSQL 18 + `jackc/pgx/v5`
- Migrations: `pressly/goose` (embedded SQL, auto-applied on startup)
- Logging: `log/slog` (text in development, JSON otherwise)
- Task runner: `just` (see `justfile`)

## Rules & Skills

Coding rules live in `.claude/rules/` and auto-load when editing matching
files. **Read the relevant rule before editing:**

| File | Covers |
|---|---|
| `core.md` | Package layout (domains, not layers), DI, errors, logging, style |
| `http.md` | `NewServer`/`routes.go`/handler patterns, validation, envelopes, auth |
| `database.md` | Repos, SQL conventions, UUIDv7, migrations, transactions |
| `testing.md` | testdb (real Postgres per test), what to test where |

`.claude/skills/go` (vendored from spf13/go-skills) is the idiomatic-Go
reference and loads automatically for any Go work.

## Architecture

Request flow: `api/routes.go → api handlers → domains/<name>/service.go → domains/<name>/repo.go`

```
main.go       run() pattern: config → pool → migrate → services → server; graceful shutdown
config/       env-driven Config (Load(getenv))
api/          HTTP layer: server.go, routes.go, middleware, json.go (decode/validate), handlers
auth/         RequireAPIKey middleware + Identity-in-context (swap for JWT, keep the contract)
domains/      business domains, one package per domain; root gains no new packages
  notes/      example domain slice: types+errors, service (business logic), repo (SQL), tests
postgres/     Connect, Migrate, embedded migrations/
testdb/       per-test isolated databases cloned from a migrated template
```

`domains/notes` is the complete example vertical slice (migration → repo →
service → handlers → tests). Copy it for a new domain, then delete it.

## Development

```bash
just compose         # full stack in Docker (api + postgres)
just app             # run API on the host (needs: docker compose up postgres -d)
just fmt             # gofmt
just lint            # go vet + gofmt check
just test            # spins up postgres-test, runs go test ./...
just migrate         # apply migrations (also happens on startup)
just makemigration create_users_table
just ci              # lint + test
```

API at `http://localhost:8080`. `/api/v1/*` requires `Authorization: Bearer $API_KEY`.

## Testing

Needs PostgreSQL (no DB mocks): `just test` starts the `postgres-test`
compose service. Each test gets its own database via `testdb.New(t)` —
isolated, migrated, dropped afterwards; `t.Parallel()` is safe. HTTP tests go
through the full `api.NewServer` handler.

## Database

- PostgreSQL 18, UUIDv7 primary keys (`DEFAULT uuidv7()`), `timestamptz` everywhere.
- No ENUMs — `text` + `CHECK`. Every `UPDATE` sets `updated_at = now()`.
- Always add a migration for schema changes (`just makemigration ...`);
  migrations are append-only.
