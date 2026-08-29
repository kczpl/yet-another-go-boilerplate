# yet-another-go-boilerplate

<p align="center">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white">
  <img alt="net/http" src="https://img.shields.io/badge/net%2Fhttp-stdlib-00ADD8?logo=go&logoColor=white">
  <img alt="PostgreSQL" src="https://img.shields.io/badge/PostgreSQL-18-4169E1?logo=postgresql&logoColor=white">
  <img alt="pgx" src="https://img.shields.io/badge/pgx-v5-336791?logo=postgresql&logoColor=white">
  <img alt="htmx" src="https://img.shields.io/badge/htmx-2-3366CC?logoColor=white">
  <img alt="just" src="https://img.shields.io/badge/just-tasks-DE5FE9?logo=just&logoColor=white">
  <img alt="CI" src="https://github.com/kczpl/yet-another-go-boilerplate/actions/workflows/ci.yml/badge.svg">
</p>

Minimal, feature-based Go web service template. Stdlib `net/http` (Go 1.22+
mux), PostgreSQL via [pgx](https://github.com/jackc/pgx), server-rendered
HTML with [htmx](https://htmx.org) (vendored, zero JS build), hand-rolled
embedded migrations, `log/slog`, and tests against a **real PostgreSQL** —
no frameworks, no ORM, no DI container, no mocks.

**One direct dependency: pgx.** Sessions, password hashing (PBKDF2), CSRF
protection, templates, and migrations all come from the standard library.

Built on two references:

- [How I write HTTP services in Go after 13 years](https://grafana.com/blog/how-i-write-http-services-in-go-after-13-years/) (Mat Ryer) — `run()`, handler makers, explicit wiring
- [spf13/go-skills](https://github.com/spf13/go-skills) — feature packages instead of layer packages, stdlib-first, vendored into `.claude/skills/` ([why Go fits agentic coding](https://spf13.com/p/go-the-agentic-language/))

## Layout

```
cmd/api/            main: config → pool → migrate → app.New → server; subcommands: migrate, adduser
migrations/         embedded SQL migrations, timestamp-named, applied on startup (append-only)
internal/
  platform/         shared infrastructure — features import it, it imports no feature
    config/         env-driven configuration
    database/       pgx pool (SQL logging at debug) + ~80-line migrator
    logging/        slog construction + context-carried log attributes
    web/            layout, template rendering, static assets, middleware
  app/              composition: wires every feature into one http.Handler
  auth/             sessions: cookie ↔ hashed token in DB, LoadIdentity/RequireIdentity
  user/             example feature: login, /me profile
  note/             example feature: CRUD list — the one to copy
  testdb/           per-test isolated databases, cloned from a migrated template
```

Every feature is a **vertical slice**: one package that owns its types,
service, repository interface, SQL, HTTP handlers, and templates as files —
never as layer packages.

```
internal/note/
  note.go           types, sentinel errors, ValidationError
  service.go        business rules (ownership, validation)
  repository.go     Repository interface — defined next to its consumer
  postgres.go       Repo implementation, plain SQL
  http.go           Routes(mux, ...) + handlers
  templates/        pages + htmx fragments
  service_test.go   tests against a real database
```

To add a feature: copy `internal/note/`, add a migration, call
`yourfeature.Routes(...)` in `internal/app/app.go`.

## Quickstart

```bash
cp .env.example .env
just compose                 # full stack in Docker; or:
docker compose up postgres -d && just app   # app on the host
```

```bash
just adduser bob@example.com "Bob"   # prints a generated password, once
```

There is no register page — `just adduser` (a thin CLI wrapper around
`user.Service.Register`) is how accounts are created. Log in at
<http://localhost:8080> and add some notes.

## Routes

| Method | Path | Auth | Notes |
|---|---|---|---|
| GET | `/healthz` | – | liveness + DB ping |
| GET/POST | `/login` | – | redirects to `/me` when logged in |
| POST | `/logout` | – | idempotent |
| GET/POST | `/me` | session | dashboard: profile + embedded notes section |
| GET/POST | `/notes` | session | list + add (fragment for htmx, page otherwise) |
| POST | `/notes/{id}/delete` | session | owner-scoped; plain form, upgraded by htmx |
| GET | `/static/*` | – | embedded htmx + CSS |

## Frontend

- htmx 2 is vendored into `internal/platform/web/static/` and embedded into
  the binary. There is no npm, no bundler, no build step.
- Every form is a plain HTML form first; htmx attributes upgrade it. With
  JavaScript disabled, handlers answer with redirects (POST → redirect → GET)
  and full pages. Even delete is a real form (`POST /notes/{id}/delete`) —
  browser-facing routes use only GET and POST.
- With htmx, handlers re-render one fragment (`#profile-section`,
  `#notes-section`) and swap it — on success and on 422 validation errors.
- `/me` is a small dashboard: one screen with the profile card and the note
  feature's section. The notes section loads itself with
  `hx-get="/notes" hx-trigger="load"` — cross-feature composition happens in
  the browser, not through Go imports. Without JavaScript the placeholder
  links to the full `/notes` page.
- The look is one hand-written stylesheet (`static/style.css`): noir —
  dark only, monochrome, monospace, flat 1px borders, no radii, no
  shadows. One red, for errors. No CSS framework.

## Auth

- Login stores a 43-character random token in a `session_id` cookie
  (`HttpOnly`, `SameSite=Lax`, `Secure` outside development).
- The database stores only the SHA-256 hash of the token; expiry is enforced
  in SQL.
- Passwords are hashed with stdlib PBKDF2-HMAC-SHA256 (600k iterations) in a
  self-describing format, so the cost can be raised later.
- CSRF: stdlib `http.CrossOriginProtection` rejects cross-origin unsafe
  requests via `Sec-Fetch-Site` — no tokens needed.
- Every response carries security headers (a strict CSP — possible because
  all assets are self-hosted — plus `nosniff`, `X-Frame-Options`,
  `Referrer-Policy`), and `http.MaxBytesHandler` caps request bodies.

## Logging

One `slog.Logger` for the whole application, built by `logging.New` (text in
development, JSON otherwise) and passed down explicitly — no globals.
Middleware stores correlation attributes in the request context:

- `web.LogRequests` adds `request_id` (reused from `X-Request-ID`, echoed
  back);
- `auth.LoadIdentity` adds `user_id` for logged-in requests.

Every record logged with a `*Context` method repeats them automatically —
handler errors, template-render errors, panics, and even SQL. Set
`LOG_LEVEL=debug` to log every query (via pgx `tracelog`), correlated with
the request that ran it:

```
level=INFO  msg=request method=POST path=/notes status=200 duration=6.5ms request_id=18ae0ca4eba5e38b
level=DEBUG msg="pgx: Query" sql="INSERT INTO notes ..." time=2.39ms request_id=18ae0ca4eba5e38b user_id=01a04756-...
```

## Testing

```bash
just test        # starts postgres-test (compose) + go test -race ./...
```

Every test calls `testdb.New(t)` and gets a **private database** cloned from
a migrated template (`CREATE DATABASE ... TEMPLATE`, a few ms) — full
isolation, commits allowed, `t.Parallel()` safe, dropped on cleanup. HTTP
tests drive the complete `app.New` handler (middleware + sessions + CSRF
included) and assert on rendered HTML; they also pin the security
behavior — CSRF rejection, response headers, the body cap, the unified 404.
The race detector is always on.

## Commands

```bash
just app / compose / build / fmt / lint / test / ci
just migrate                          # apply migrations (startup does this too)
just makemigration create_toys_table  # new migration file (timestamp-named)
just adduser bob@example.com "Bob"    # create an account, prints the password
just vulncheck                        # govulncheck
```

CI (`.github/workflows/ci.yml`) runs `just ci` and `govulncheck` on every
push and pull request — the same recipes you run locally, nothing more.

## Working with AI agents

`CLAUDE.md` + path-scoped rules in `.claude/rules/` + vendored
[spf13/go-skills](https://github.com/spf13/go-skills) in `.claude/skills/`
give coding agents the project conventions up front. The codebase is
deliberately explicit and denormalized — every query written out, every
dependency injected by hand — so agents (and humans) can read any file in
isolation.

The rules in `.claude/rules/` are the canonical convention reference —
this README and `CLAUDE.md` only summarize them. `AGENTS.md` is a symlink
to `CLAUDE.md` for tools that look for that name.

## Renaming

Replace the module path everywhere:

```bash
go mod edit -module github.com/you/your-service
grep -rl 'github.com/kczpl/yet-another-go-boilerplate' --include='*.go' . \
  | xargs sed -i '' 's|github.com/kczpl/yet-another-go-boilerplate|github.com/you/your-service|g'
```
