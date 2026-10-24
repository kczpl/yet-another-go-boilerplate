# yet-another-go-boilerplate

<p align="center">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.27.3+-00ADD8?logo=go&logoColor=white">
  <img alt="net/http" src="https://img.shields.io/badge/net%2Fhttp-stdlib-00ADD8?logo=go&logoColor=white">
  <img alt="PostgreSQL" src="https://img.shields.io/badge/PostgreSQL-18-4169E1?logo=postgresql&logoColor=white">
  <img alt="pgx" src="https://img.shields.io/badge/pgx-v5-336791?logo=postgresql&logoColor=white">
  <img alt="htmx" src="https://img.shields.io/badge/htmx-2-3366CC?logoColor=white">
  <img alt="just" src="https://img.shields.io/badge/just-tasks-DE5FE9?logo=just&logoColor=white">
  <img alt="CI" src="https://github.com/kczpl/yet-another-go-boilerplate/actions/workflows/ci.yml/badge.svg">
</p>

Minimal, feature-based Go web service template. Stdlib `net/http`,
PostgreSQL via [pgx](https://github.com/jackc/pgx), server-rendered HTML
with [htmx](https://htmx.org) (vendored, no JS build), embedded SQL
migrations, `log/slog`, and tests against a **real PostgreSQL** — no
frameworks, no ORM, no DI container, no mocks.

**Two direct dependencies: pgx and the official Anthropic SDK.** Only the
Anthropic provider package imports the SDK. Sessions, password hashing,
CSRF protection, rate limits, Server-Sent Events, templates, and
migrations come from the standard library. `go.mod` pins the Go version,
and the Dockerfile uses the same one.

Built on two references:

- [How I write HTTP services in Go after 13 years](https://grafana.com/blog/how-i-write-http-services-in-go-after-13-years/) (Mat Ryer) — `run()`, handler makers, explicit wiring
- [spf13/go-skills](https://github.com/spf13/go-skills) — feature packages instead of layer packages, stdlib-first, vendored into `.agents/skills/` ([why Go fits agentic coding](https://spf13.com/p/go-the-agentic-language/))

## Contents

- [Quickstart](#quickstart)
- [Design principles](#design-principles)
- [Architecture](#architecture)
- [Build a feature](#build-a-feature)
- [Work with models](#work-with-models)
- [Testing](#testing)
- [Configuration](#configuration) · [Commands](#commands) · [Conventions](#conventions)
- [Working with AI agents](#working-with-ai-agents) · [Renaming](#renaming) · [Before production](#before-production)

## Quickstart

```bash
cp .env.example .env
just compose                                # full stack in Docker, or:
docker compose up postgres -d && just app   # the app on the host
just adduser bob@example.com "Bob"          # prints a generated password, once
```

There is no register page: `just adduser` creates accounts. Log in at
<http://localhost:8080>. The `/me` dashboard has one card per example
feature:

| Card | Shows | Model |
|---|---|---|
| Notes | a database read with keyset pages | – |
| Chat | an LLM answer streamed over SSE | `LLM_MODEL` |
| Triage | a System One decision with probabilities | `SYSTEMONE_MODEL` |

Without keys, the cards use offline fake models. To call real models, see
[Work with models](#work-with-models).

## Design principles

The template makes a small number of deliberate choices. Each choice keeps
the code easy to read for a person and easy to change for a coding agent.

- **The standard library first.** A dependency must earn its place. The
  standard library already has a good router, templates, PBKDF2, CSRF
  protection, and structured logs. Fewer packages mean fewer upgrades and
  fewer security advisories.
- **Features, not layers.** Code that changes together lives together.
  One package holds one feature: its types, rules, SQL, handlers, and
  templates. There are no `services/`, `handlers/`, or `models/` packages.
- **Explicit wiring.** There is no DI container, no `init()`, and no
  global state. `app.New` constructs everything. A new dependency is a new
  constructor parameter, so the compiler finds every call site.
- **Fail closed.** An unset `ENVIRONMENT` means production. A bad config
  value, or a model without its key, stops the startup with a clear
  error. A silent fallback hides a mistake until production.
- **HTML first, JavaScript optional.** The server renders the HTML. Every
  flow works without JavaScript. htmx then upgrades forms to partial page
  swaps. Browser routes use only GET and POST, because an HTML form can
  send only these two methods.
- **A real database in tests.** A mock of the database tests the mock.
  Each test gets its own migrated PostgreSQL database in a few
  milliseconds, so the tests check the real SQL, constraints, and
  transactions.
- **Plain code over clever code.** Two similar handlers are better than
  one generic handler. There is no generic repository and no base struct.
  Each SQL query is written out next to the code that uses it. A reader
  (or an agent) understands one file without a tour of abstractions.
- **Simple English in comments.** All comments follow
  [ASD-STE100 Simplified Technical English](https://www.asd-ste100.org/):
  short sentences, the active voice, and one term for each concept.

## Architecture

### Layout

```
cmd/api/            main: config → pool → migrate → app.New → server; subcommands: migrate, adduser
migrations/         embedded SQL migrations, applied on startup (append-only, timestamp-named)
internal/
  platform/         shared infrastructure — features import it, it imports no feature
    config/         env-driven configuration
    database/       pgx pool + embedded SQL migrator
    logging/        slog construction + context-carried log attributes
    llm/            provider-neutral core for text models; anthropic/ (official SDK)
    systemone/      provider-neutral core for System One models; typesafe/ (net/http)
    web/            layout, rendering, errors, middleware, rate limiter, SSE, static assets
  app/              composition: wires every feature into one http.Handler
  auth/             sessions: cookie ↔ hashed token in the database
  user/             example feature: login, /me dashboard
  note/             example feature: a list with keyset pages — the one to copy
  chat/             example LLM feature: ask a question, stream the answer
  triage/           example System One feature: route a support message
  testdb/           per-test isolated databases, cloned from a migrated template
```

### A feature is a vertical slice

Each feature package owns all of its layers as files:

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

Each file has one job:

| File | Does | Never does |
|---|---|---|
| `http.go` | parses the form, calls the service, renders or redirects | business rules, SQL |
| `service.go` | validates input, applies rules, picks the page size, owns prompts | sees `http.Request` or pgx types |
| `postgres.go` | runs SQL, maps driver errors to the feature's sentinels | makes product decisions |

A feature that grows gets more files (`http_share.go`), not subpackages.

### Request flow

```
request
  → LogRequests → RecoverPanics → SecureHeaders → MaxBytesHandler
  → root mux ─── /livez, /readyz, /static/          (no session, no CSRF)
  → PageHeaders → CrossOriginProtection → LoadIdentity
  → pages mux → <feature>.Routes
      → http.go → service.go → repository.go ← postgres.go → PostgreSQL
```

The whole chain lives in one function, `app.New`
([`internal/app/app.go`](internal/app/app.go)). Tests build the same
handler as production, so the tests also cover the middleware, sessions,
and CSRF.

Handlers return errors instead of writing them. `web.E` turns a returned
error into one log line and one response:

| A handler returns | The client gets |
|---|---|
| `web.BadRequest("…")`, `web.NotFound("…")` | 400 or 404 with that message |
| any other error | an opaque 500; the log has the real error |

A validation error is not an error response. The handler renders the form
again with status 422, the message, and the submitted values. Clients that
send `Accept: application/json` get a JSON error envelope instead of the
error page.

### Dependency direction

```
cmd/api ──► app ──► features (user, note, chat, triage) ──► auth ──► platform
                         └─────────────────────────────────────────► platform
```

- Features import `auth` and `platform`. Features never import each other.
- `platform` imports no feature.
- `app` is the only package that imports everything.

When feature A needs the logic of feature B, A declares a small interface
with only the methods that it calls. `app.New` passes B's service in. When
a page needs the HTML of another feature, the browser loads it with htmx
(`hx-get`). The `/me` dashboard shows its cards this way.

## Build a feature

This walkthrough adds a `toy` feature. The full checklist is in
[`.agents/rules/core.md`](.agents/rules/core.md).

1. **Schema.** Run `just makemigration create_toys` and write the SQL.
   Use `uuidv7()` keys, `timestamptz`, `text` with a `CHECK` instead of an
   enum, and an index on the owner plus the list order. Migrations are
   append-only: to change a table, add a new file.
2. **Copy.** Copy `internal/note/` to `internal/toy/` and rename the
   types.
3. **Rules.** Put the validation and the decisions in `service.go`. Each
   method takes `ctx`, then the owner id, then the resource id. Return the
   feature's `ValidationError` for bad input.
4. **SQL.** Write the queries in `postgres.go` with `pgx.NamedArgs`. Each
   list query has a `LIMIT`. Ownership is a `WHERE ... AND user_id =
   @user_id` condition, so a foreign row looks the same as a missing row.
5. **Routes.** Register the routes in `http.go`. Every authenticated route
   gets the guard and the error adapter:

   ```go
   func Routes(mux *http.ServeMux, logger *slog.Logger, svc *Service) {
   	mux.Handle("GET /toys", auth.RequireIdentity(web.E(logger, handleToys(svc))))
   	mux.Handle("POST /toys", auth.RequireIdentity(web.E(logger, handleToyAdd(svc))))
   }
   ```

   A handler answers an htmx request with a fragment and a plain form
   with a redirect, so the page works with and without JavaScript.
6. **Wiring.** Add two lines to `app.New`:

   ```go
   toys := toy.NewService(toy.NewRepo(pool))
   toy.Routes(pages, logger, toys)
   ```

7. **Tests.** Write `internal/toy/service_test.go` for the rules and the
   SQL, and `internal/app/toys_test.go` for the user flows. See
   [Testing](#testing).
8. **Dashboard (optional).** Add an `hx-get="/toys"` placeholder to
   `internal/user/templates/me.html`.
9. **Check.** Run `just ci`.

A feature that stores nothing (like `chat` or `triage`) skips the schema
and has no `repository.go` or `postgres.go`.

## Work with models

### Two kinds of model

The template separates two kinds of model, because they do different
jobs:

| | LLM | System One |
|---|---|---|
| Job | writes text: an answer, a summary, a draft | judges a state: picks an option, gives a score, or checks a statement, with calibrated probabilities |
| Core package | [`internal/platform/llm`](internal/platform/llm) | [`internal/platform/systemone`](internal/platform/systemone) |
| Interface | `Stream(ctx, req, onText)` | `Ask(ctx, req)` |
| Default model | `LLM_MODEL` (`fake/echo`) | `SYSTEMONE_MODEL` (`fake/uniform`) |
| Real provider | `anthropic` — Claude, official SDK | `typesafe` — Jev, plain `net/http` |
| Example feature | [`internal/chat`](internal/chat) | [`internal/triage`](internal/triage) |

A feature depends only on the core interface. It never imports a provider
package or an SDK. To change the vendor, you change one environment
variable.

### Configure a model

A model reference has the form `provider/model`. Set the model and its key
in `.env`, then restart the app:

```bash
LLM_MODEL=anthropic/claude-opus-5-5
ANTHROPIC_API_KEY=sk-ant-...
SYSTEMONE_MODEL=typesafe/jev-latest
TYPESAFE_API_KEY=...
```

`app.NewModels` ([`internal/app/models.go`](internal/app/models.go))
registers a provider only when its key is set. A model of a provider
without its key stops the startup, and the error names the missing key.

Without keys, the app uses two offline fakes. `fake/echo` returns the
question as the answer. `fake/uniform` gives every option the same
probability. You can develop and test every flow without an account at a
model vendor.

### Call a model from a feature

A feature owns its prompt and its limits. They are constants in its
`service.go`, next to the code that uses them
([`internal/chat/service.go`](internal/chat/service.go)):

```go
const maxQuestionLength = 2000      // caps the input cost
const maxAnswerTokens = 4096        // caps the output cost
const answerTimeout = 2 * time.Minute

func (s *Service) Stream(ctx context.Context, question string, onText func(string) error) (llm.Response, error) {
	req, err := s.request(question) // validates the input, builds llm.Request
	if err != nil {
		return llm.Response{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, answerTimeout)
	defer cancel()
	return s.models.Stream(ctx, req, onText)
}
```

`app.New` passes the provider and the default model to the constructor:
`chat.NewService(models.LLM, cfg.LLMModel)`. When one feature needs a
different model, add a config field such as `SUMMARY_MODEL`.

### Cost and safety guards

Each model call costs money and handles private text. The examples show
the guards that a model feature needs:

- **Input cap.** The service rejects a question that is too long.
- **Output cap.** Each request sets `MaxTokens`. The fake model rejects a
  request without it, so a forgotten cap fails the tests.
- **Timeout.** Each call has its own `context.WithTimeout`. The call also
  stops when the client leaves.
- **Rate limit.** Each model endpoint has its own `web.RateLimiter` in
  `app.New`: chat allows 5 questions at once, then 1 every 10 seconds.
- **No transactions.** Never call a model inside a database transaction.
  Call the model first, then write the result.
- **Untrusted output.** Check the model output like user input.
  `triage` rejects an unknown option and a probability outside 0..1. The
  page shows model text only as text, never as HTML.
- **Private logs.** The app logs only the model version and the token
  counts. It never logs a key, a prompt, or an answer.

Before production, also add a budget for each account and a spend limit at
each provider (see [Before production](#before-production)).

### Stream an answer

The chat feature streams the answer with Server-Sent Events. The browser
sends a POST with `fetch` ([`static/app.js`](internal/platform/web/static/app.js))
instead of `EventSource`, because `EventSource` can send only GET, and the
CSRF guard does not cover GET. The server does all checks that can fail
with an error status (session, rate limit, form) before the stream
starts. After that, an error becomes an `error` event. Without
JavaScript, the same POST returns a full page with the complete answer.

### Add a provider

Create a subpackage `internal/platform/llm/<name>/` or
`internal/platform/systemone/<name>/` that implements the core interface.
Use the official Go SDK of the vendor when one exists, and plain
`net/http` when not. Add the key to the config, register the provider in
`app.NewModels`, and test the adapter against an `httptest.Server`. The
step-by-step list is "Add a Provider" in
[`.agents/rules/core.md`](.agents/rules/core.md).

## Testing

```bash
just test                        # starts postgres-test, runs go test -race ./...
just test -run TestNotes         # extra arguments go to go test
just ci                          # lint, types, complexity, tests, vulncheck
```

`just test` starts a PostgreSQL container for tests on port 5433. It keeps
its data in memory (tmpfs). Set `POSTGRES_TEST_PORT` when that port is
busy. The race detector is always on.

### A database per test

```go
pool := testdb.New(t) // a private, migrated database; dropped after the test
```

`testdb.New` clones a migrated template database. A clone takes a few
milliseconds, so every test gets its own database and can run with
`t.Parallel()`. Tests commit freely: there is no shared state and no
rollback trick. The template name includes a hash of the migrations, so a
new migration rebuilds the template automatically.

### What to test where

| Layer | Where | Checks |
|---|---|---|
| User flows | `internal/app/<feature>_test.go` | status codes, redirects, and HTML through the full `app.New` handler, on the htmx path and the no-JS path |
| Security invariants | `internal/app/security_test.go`, `auth_test.go` | headers, CSRF, body cap, unified 404, cookie flags, login rate limit |
| Business rules + SQL | `internal/<feature>/service_test.go` | validation, ownership, pages — against a real database |
| Model features | `internal/chat`, `internal/triage` | prompt, caps, and decisions with a stub provider, no database |
| Provider adapters | `internal/platform/llm/anthropic`, `internal/platform/systemone/typesafe` | request mapping, response parsing, errors without the key — against `httptest.Server` |
| Platform | `internal/platform/*`, `cmd/api` | migrator, middleware, config, SSE, startup |

A flow test reads like a user session. The test client in
[`client_test.go`](internal/app/client_test.go) keeps the session cookie
and has helpers for both paths:

```go
func TestNotesFlow(t *testing.T) {
	t.Parallel()
	c := newClient(t) // the production config: __Host- cookie, HSTS
	c.register("bob@example.com", "Bob", "s3cret-pass")

	rec := c.htmxPost("/notes", url.Values{"text": {"Buy milk"}})
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Buy milk")
	wantFragment(t, rec) // a bare htmx fragment, not a full page
}
```

Three rules keep the suite trustworthy:

- **No live models.** Tests use the fakes or a stub provider:
  `newClientWithModels(t, app.Models{LLM: failingModel{}})`.
- **No sleeps.** A test waits for an explicit signal, never for time. The
  only exceptions wait for a real server deadline.
- **Extend, do not delete.** When a change breaks a security test, fix the
  change or extend the test.

Browser checks complement these tests. An HTTP test cannot see a CSP
violation or a broken DOM swap, so check the page in a browser when an
htmx flow changes. The full test rules are in
[`.agents/rules/testing.md`](.agents/rules/testing.md).

## Configuration

All settings come from the environment (`config.Load`). `Config.Validate`
stops the startup on a bad value.

| Variable | Default | Notes |
|---|---|---|
| `ENVIRONMENT` | `production` | `development`, `staging`, or `production`. Unset means production: `DATABASE_URL` is required, the cookie is `Secure`, and HSTS is on. The `just` host recipes default to `development`. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. `debug` also logs every SQL query. |
| `HOST` / `PORT` | `localhost` / `8080` | The bind address. The Docker image sets `HOST=0.0.0.0`. |
| `DATABASE_URL` | – (development: local) | Each connection gets `statement_timeout=5s`; add `?statement_timeout=…` to change it. |
| `CLIENT_IP_HEADER` | – | The header in which a trusted proxy sends the client address (`X-Forwarded-For`). The rate limits use its last address. Leave it empty without a proxy. |
| `DEBUG_ADDR` | – | A loopback `host:port` for pprof and expvar (`localhost:6060`). Never expose it. |
| `LLM_MODEL` | `fake/echo` | The default text model as `provider/model`. |
| `SYSTEMONE_MODEL` | `fake/uniform` | The default System One model as `provider/model`. |
| `ANTHROPIC_API_KEY` | – | Registers the `anthropic` provider. |
| `TYPESAFE_API_KEY` | – | Registers the `typesafe` provider. |

## Commands

```bash
just app / compose / build / fmt / lint / types / complexity / test / ci
just migrate                          # apply migrations (startup does this too)
just makemigration create_toys_table  # new migration file (timestamp-named)
just adduser bob@example.com "Bob"    # create an account, prints the password
just vulncheck                        # govulncheck
```

`just ci` runs lint (`go vet`, `go fix -diff`, staticcheck, gofmt), types,
cognitive complexity (gocognit, at most 10), race tests against
PostgreSQL, and govulncheck. CI runs it on main pushes and pull requests,
then builds the Docker image. The justfile pins the tool versions, and
the tools do not add application dependencies.

## Conventions

The rules in `.agents/rules/` are the single source of truth for the
conventions, for people and for coding agents. This README explains the
ideas; the rules hold the details.

| Rule | Covers |
|---|---|
| [`core.md`](.agents/rules/core.md) | package layout, dependency direction, services, model providers, errors, logging |
| [`http.md`](.agents/rules/http.md) | middleware chain, handlers, htmx fragments and redirects, SSE, templates, auth |
| [`database.md`](.agents/rules/database.md) | repositories, SQL, keyset pages, transactions, timeouts, migrations |
| [`testing.md`](.agents/rules/testing.md) | a real database per test, what to test where, CI |
| [`security.md`](.agents/rules/security.md) | sessions, passwords, CSRF, headers, rate limits, the production checklist |

Third-party code (htmx, the Anthropic SDK, the vendored skills) is listed
in [`docs/third-party.md`](docs/third-party.md).

## Working with AI agents

`AGENTS.md` is the entry point for coding agents: the hard rules, a map
from a task to the rule to read, and the checks to run before a change is
done. The repository has no agent-specific files: no `CLAUDE.md`, no
`.claude/`, no `.cursor/`.

- Claude Code (v2.1.277 or later) reads `AGENTS.md` when the repository has
  no `CLAUDE.md`. The `@.agents/rules/*.md` import lines in `AGENTS.md` load
  all rules at session start. Run `/memory` to see the loaded files.
- Other agents that read `AGENTS.md` (Codex, Cursor, and others) get the
  rule paths and the instruction to read the relevant rule before an edit.
- Agents do not discover `.agents/skills/` automatically. `AGENTS.md` tells
  them when to read the skill files.

Do not add a `CLAUDE.md`, not even as a symlink. Claude Code then reads it
instead of `AGENTS.md`.

## Renaming

Replace the module path everywhere:

```bash
go mod edit -module github.com/you/your-service
grep -rl 'github.com/kczpl/yet-another-go-boilerplate' --include='*.go' . \
  | xargs sed -i '' 's|github.com/kczpl/yet-another-go-boilerplate|github.com/you/your-service|g'
```

## Before production

The template deliberately has no public registration, password reset,
workers, or generic unit of work. Point the orchestrator at `/livez`
(liveness, no database) and `/readyz` (readiness, a database ping). Before
a public deployment, work through "Not Included — Add Before Production"
in [`.agents/rules/security.md`](.agents/rules/security.md): TLS, limits
for many replicas, a model budget, session cleanup, metrics, backups, and
database roles.
