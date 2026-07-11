# yet-another-go-boilerplate

<p align="center">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white">
  <img alt="net/http" src="https://img.shields.io/badge/net%2Fhttp-stdlib-00ADD8?logo=go&logoColor=white">
  <img alt="PostgreSQL" src="https://img.shields.io/badge/PostgreSQL-18-4169E1?logo=postgresql&logoColor=white">
  <img alt="pgx" src="https://img.shields.io/badge/pgx-v5-336791?logo=postgresql&logoColor=white">
  <img alt="just" src="https://img.shields.io/badge/just-tasks-DE5FE9?logo=just&logoColor=white">
</p>

Minimal, agent-friendly Go HTTP backend template. Stdlib `net/http` (Go 1.22+
mux), PostgreSQL via [pgx](https://github.com/jackc/pgx), embedded
[goose](https://github.com/pressly/goose) migrations, `log/slog`, and tests
that run against a **real PostgreSQL** — no frameworks, no ORM, no DI
container, no mocks.

Built on two references:

- [How I write HTTP services in Go after 13 years](https://grafana.com/blog/how-i-write-http-services-in-go-after-13-years/) (Mat Ryer) — `NewServer`, `routes.go`, `run()`, handler makers, `Validator`
- [spf13/go-skills](https://github.com/spf13/go-skills) — domain packages instead of layer packages, stdlib-first, vendored into `.claude/skills/` ([why Go fits agentic coding](https://spf13.com/p/go-the-agentic-language/))

## Layout

```
main.go       run() pattern: config → pool → migrate → services → server; graceful shutdown
config/       env-driven configuration
api/          HTTP layer: server, routes.go (whole API surface), middleware, JSON + validation, handlers
auth/         bearer-token middleware + Identity-in-context (swap for JWT, keep the contract)
domains/      business domains, one package per domain
  notes/      example domain: types + errors, service (business logic), repo (plain SQL)
postgres/     pgx pool + embedded goose migrations (applied on startup)
testdb/       per-test isolated databases, cloned from a migrated template
```

Layers exist as **files inside a domain package** (`domains/notes/service.go`,
`domains/notes/repo.go`), not as layer-named packages — the Go way. Domains
live together under `domains/`, so the root stays fixed as the app grows. To
add a domain: copy `domains/notes/`, add a migration, register routes in
`api/routes.go`, wire it in `main.go`.

## Quickstart

```bash
cp .env.example .env
just compose                 # full stack in Docker; or:
docker compose up postgres -d && just app   # app on the host

curl localhost:8080/healthz
curl -X POST localhost:8080/api/v1/notes \
  -H "Authorization: Bearer dev-secret-key" \
  -H "Content-Type: application/json" \
  -d '{"title": "hello", "content": "world"}'
curl -H "Authorization: Bearer dev-secret-key" "localhost:8080/api/v1/notes?page=1&page_size=10"
```

## Endpoints

| Method | Path | Auth | Notes |
|---|---|---|---|
| GET | `/healthz` | – | liveness + DB ping |
| POST | `/api/v1/notes` | Bearer | 201; 422 with `problems` on validation |
| GET | `/api/v1/notes` | Bearer | paginated (`page`, `page_size` ≤ 100) |
| GET | `/api/v1/notes/{id}` | Bearer | 404 if missing |
| DELETE | `/api/v1/notes/{id}` | Bearer | 204 |

Envelopes: `{"data": ...}` · `{"data": [...], "pagination": {...}}` ·
`{"error": "...", "problems": {...}}`.

## Testing

```bash
just test        # starts postgres-test (compose) + go test ./...
```

Every test calls `testdb.New(t)` and gets a **private database** cloned from a
migrated template (`CREATE DATABASE ... TEMPLATE`, a few ms) — full isolation,
commits allowed, `t.Parallel()` safe, dropped on cleanup. HTTP tests go
through the complete `api.NewServer` handler (middleware + auth included).

## Commands

```bash
just app / compose / fmt / lint / test / ci
just migrate                          # apply migrations (startup does this too)
just makemigration create_users_table # new goose migration file
just vulncheck                        # govulncheck
```

## Working with AI agents

`CLAUDE.md` + path-scoped rules in `.claude/rules/` + vendored
[spf13/go-skills](https://github.com/spf13/go-skills) in `.claude/skills/`
give coding agents the project conventions up front. The codebase is
deliberately explicit and denormalized — every query written out, every
dependency injected by hand — so agents (and humans) can read any file in
isolation.

## Renaming

Replace the module path everywhere:

```bash
go mod edit -module github.com/you/your-service
grep -rl 'github.com/kczpl/yet-another-go-boilerplate' --include='*.go' . \
  | xargs sed -i '' 's|github.com/kczpl/yet-another-go-boilerplate|github.com/you/your-service|g'
```
