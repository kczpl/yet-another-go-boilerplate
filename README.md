# yet-another-go-boilerplate

<p align="center">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.26.8+-00ADD8?logo=go&logoColor=white">
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
protection, the login rate limit, templates, and migrations all come from
the standard library. The app requires Go 1.26.8 or newer; go.mod and the
Dockerfile pin the same patch version.

Built on two references:

- [How I write HTTP services in Go after 13 years](https://grafana.com/blog/how-i-write-http-services-in-go-after-13-years/) (Mat Ryer) — `run()`, handler makers, explicit wiring
- [spf13/go-skills](https://github.com/spf13/go-skills) — feature packages instead of layer packages, stdlib-first, vendored into `.agents/skills/` ([why Go fits agentic coding](https://spf13.com/p/go-the-agentic-language/))

## Layout

```
cmd/api/            main: config → pool → migrate → app.New → server; subcommands: migrate, adduser;
                    optional loopback pprof/expvar listener (DEBUG_ADDR)
migrations/         embedded SQL migrations, applied on startup (append-only; new files are timestamp-named)
internal/
  platform/         shared infrastructure — features import it, it imports no feature
    config/         env-driven configuration
    database/       pgx pool (SQL traces at debug) + embedded SQL migrator
    logging/        slog construction + context-carried log attributes
    web/            layout, template rendering, static assets, middleware, rate limiter
  app/              composition: wires every feature into one http.Handler
  auth/             sessions: cookie ↔ hashed token in DB, LoadIdentity/RequireIdentity
  user/             example feature: login, /me profile
  note/             example feature: CRUD list with keyset pages — the one to copy
  testdb/           per-test isolated databases, cloned from a migrated template
```

Every feature is a **vertical slice**: one package that owns its types,
service, repository interface, SQL, HTTP handlers, and templates as files —
never as layer packages.

```
internal/note/
  note.go           types, sentinel errors, ValidationError
  service.go        business rules (ownership, validation, page size)
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
| GET | `/` | – | redirects to `/me`, or to `/login` when logged out |
| GET | `/livez` | – | liveness: the process answers; no DB, not logged |
| GET | `/readyz` | – | readiness: DB ping (2s), 503 on failure; not logged |
| GET/POST | `/login` | – | redirects to `/me` when logged in; POST is rate limited |
| POST | `/logout` | – | idempotent |
| GET/POST | `/me` | session | dashboard: profile + embedded notes section |
| GET/POST | `/notes` | session | list + add (fragment for htmx, page otherwise); `?after=<cursor>` shows older notes |
| POST | `/notes/{id}/delete` | session | owner-scoped; keeps the current page; plain form, upgraded by htmx |
| GET | `/static/*` | – | embedded htmx + CSS |

## Frontend

- htmx 2 is vendored into `internal/platform/web/static/` and embedded into
  the binary. There is no npm, no bundler, no build step.
- Every form is a plain HTML form first; htmx attributes upgrade it. With
  JavaScript disabled, handlers answer with redirects (POST → redirect → GET)
  and full pages. Even delete is a real form (`POST /notes/{id}/delete`) —
  browser-facing routes use only GET and POST.
- htmx disables eval, script tags in fragments, inline indicator styles, and
  local history storage. A small external `static/app.js` shows request
  failures without clearing the form.
- Dynamic responses use `Cache-Control: no-store` and vary by `HX-Request`
  and `Accept`. Unversioned static assets require revalidation.
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

- Login stores a 43-character random token in a `__Host-session_id` cookie
  (`Secure`, `HttpOnly`, `SameSite=Lax`, `Path=/`). Development runs on
  plain HTTP and uses `session_id` without `Secure`.
- The database stores only the SHA-256 hash of the token; expiry is enforced
  in SQL. A user keeps at most 10 sessions: a new login removes the oldest
  in the same transaction. Each login also removes up to 100 expired rows.
  A database failure returns 500 and preserves the session cookie.
- `POST /login` has a per-IP rate limit (a burst of 10, then one attempt
  per 20s; 429 with `Retry-After`). Behind a proxy, set `CLIENT_IP_HEADER`.
  The counters live in process memory, one set per replica.
- Passwords are hashed with stdlib PBKDF2-HMAC-SHA256 (600k iterations) in a
  self-describing format, so the cost can be raised later. New passwords
  require at least 8 Unicode characters and at most 512 bytes; login enforces
  the byte cap before hashing.
- CSRF: stdlib `http.CrossOriginProtection` rejects cross-origin unsafe
  requests via `Sec-Fetch-Site`, with an `Origin` fallback — no tokens needed.
- Every response carries security headers (a strict CSP — possible because
  all assets are self-hosted — plus `nosniff`, `X-Frame-Options`,
  `Referrer-Policy`, and HSTS outside development), and
  `http.MaxBytesHandler` caps request bodies.

## Configuration

All settings come from the environment (`config.Load`). `Config.Validate`
stops the startup on a bad value.

| Variable | Default | Notes |
|---|---|---|
| `ENVIRONMENT` | `production` | `development`, `staging`, or `production`. Unset means production: `DATABASE_URL` is required, the cookie is `Secure`, and HSTS is on. The `just` host recipes default to `development`. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. `debug` also logs every SQL query. |
| `HOST` / `PORT` | `localhost` / `8080` | The bind address. The Docker image sets `HOST=0.0.0.0`. |
| `DATABASE_URL` | – (development: local) | Each connection gets `statement_timeout=5s`; add `?statement_timeout=…` to change it. |
| `CLIENT_IP_HEADER` | – | The header in which a trusted proxy sends the client address (`X-Forwarded-For`). The rate limit uses its last address. Leave it empty without a proxy. |
| `DEBUG_ADDR` | – | A loopback `host:port` for pprof and expvar (`localhost:6060`). Never expose it. |

## Logging

One `slog.Logger` for the whole application, built by `logging.New` (text in
development, JSON otherwise) and passed down explicitly — no globals. The
request log skips the `/livez` and `/readyz` probes.
Middleware stores correlation attributes in the request context:

- `web.LogRequests` adds `request_id` (reused from `X-Request-ID`, echoed
  back);
- `auth.LoadIdentity` adds `user_id` for logged-in requests.

Every record logged with a `*Context` method repeats them automatically —
handler errors, template-render errors, panics, and even SQL. Set
`LOG_LEVEL=debug` to log every query (via pgx `tracelog`), correlated with
the request that ran it. Traces omit SQL arguments and driver error payloads;
password hashes and private text must not enter logs:

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
included) with the production configuration and assert on rendered HTML.
`internal/app` has one test file per area (`auth_test.go`,
`notes_test.go`, `security_test.go`, ...); a new feature adds its own.
`security_test.go` pins the security behavior — CSRF rejection, response
headers, the body cap, the unified 404. The race detector is always on.

## Commands

```bash
just app / compose / build / fmt / lint / types / complexity / test / ci
just migrate                          # apply migrations (startup does this too)
just makemigration create_toys_table  # new migration file (timestamp-named)
just adduser bob@example.com "Bob"    # create an account, prints the password
just vulncheck                        # govulncheck
```

`just types` uses the Go compiler to check application and test code without
running tests or connecting to a database. A separate type checker is not
needed. `just complexity` uses gocognit with a cognitive complexity limit of
10 for application code and test support; test functions are excluded.
Staticcheck, gocognit, and govulncheck versions are pinned in the justfile.
They do not add application dependencies.

`just ci` runs lint, types, complexity, race tests, and vulnerability checks.
CI runs that recipe on main pushes and pull requests, then builds the Docker
image. `just test` disables the test result cache and accepts extra Go flags.
The test database uses a separate Compose profile and tmpfs. Its data is
disposable; the application database keeps its persistent volume.

## Working with AI agents

`AGENTS.md` + path-scoped rules in `.agents/rules/` + vendored
[spf13/go-skills](https://github.com/spf13/go-skills) in `.agents/skills/`
give coding agents the project conventions up front. Local rules take
precedence over the generic skill where they differ. Agents must read the
relevant rules; path-based auto-loading depends on the agent tool. The codebase is
deliberately explicit and denormalized — every query written out, every
dependency injected by hand — so agents (and humans) can read any file in
isolation.

The rules in `.agents/rules/` are the canonical convention reference —
this README and `AGENTS.md` only summarize them. Claude Code reads
`CLAUDE.md` and `.claude/`, so `CLAUDE.md`, `.claude/rules`, and
`.claude/skills` are symlinks to the `AGENTS.md` and `.agents/` files.
`.claude/settings.json` stays a regular file because it is Claude Code
configuration.

## Renaming

Replace the module path everywhere:

```bash
go mod edit -module github.com/you/your-service
grep -rl 'github.com/kczpl/yet-another-go-boilerplate' --include='*.go' . \
  | xargs sed -i '' 's|github.com/kczpl/yet-another-go-boilerplate|github.com/you/your-service|g'
```

## Scope and production setup

The template deliberately has no public registration, password reset,
workers, ORM, or generic unit of work.

The app already has a login rate limit, a per-user session cap, HSTS, the
`__Host-` cookie, liveness and readiness probes, a statement timeout, and
an optional loopback pprof listener. Before public deployment, add these at
the edge or in the deployment:

- TLS termination at a proxy, with `CLIENT_IP_HEADER` set for it. Outside
  development the app works only over HTTPS.
- A shared or per-account rate limit when you run many replicas, and a
  general request limit at the proxy.
- A scheduled session cleanup if the table must stay small without logins.
  Expiry blocks access but does not remove a row by itself.
- Metrics, tracing, backups, and secret management.

The migrator records filenames and requires append-only review; it does not
verify applied file checksums. See `.agents/rules/security.md` and
[the review report](docs/review.md).
