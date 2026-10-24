# AGENTS.md

A minimal, feature-based Go web service template: stdlib `net/http`,
PostgreSQL through pgx, server-rendered HTML with vendored htmx, embedded
SQL migrations, a background job queue on PostgreSQL, `log/slog`, and
tests against a real database. There are
no frameworks, no ORM, and no DI container: `internal/app` wires
everything explicitly.

This file is for coding agents. `README.md` explains the same project to
people. The rules in `.agents/rules/` are the single source of truth; when
this file and a rule differ, the rule is correct.

## Hard Rules

Never break these rules. The rule file in brackets gives the reason and
the details.

- Do not add a dependency. The only direct dependency is pgx. A model
  provider calls its API with `net/http`; never add the SDK of a
  provider. [core.md]
- Features never import each other. `internal/platform` never imports a
  feature. Only `internal/app` wires features: `app.go` the routes,
  `worker.go` the jobs. [core.md]
- Handlers parse, call the service, and render. Business rules live in
  `service.go`. SQL lives only in `postgres*.go`. A service never opens a
  transaction. [core.md, database.md]
- Never edit or rename an applied migration. Create a new file with
  `just makemigration <name>`. [database.md]
- Browser routes use only GET and POST. Never change state in a GET
  handler. [http.md]
- Wrap every authenticated route as
  `auth.RequireIdentity(web.E(logger, h))`. Read the user only from
  `auth.IdentityFromContext`. Enforce ownership in SQL. [security.md]
- Do not weaken the middleware chain, the security headers, the CSP, the
  CSRF guard, the body cap, or the cookie flags. Never delete a security
  test to make a change pass. [security.md]
- No inline `<script>`, no inline `style=`, no CDN tag. Never use
  `template.HTML` with user data; model output is user data. [security.md]
- Only `config.Load` reads the environment. [core.md]
- A job handler returns when its ctx ends, and a second run of the job is
  safe. Enqueue a job only with `jobs.Enqueue`, in the transaction of the
  write. Never put a secret or private text in a payload.
  [core.md, database.md]
- Never log a key, a password, a token, a prompt, a state, or an answer.
  [security.md]
- Tests never call a live model and never mock the database. [testing.md]
- Do not add a `CLAUDE.md` or a `.claude/` directory. Claude Code then
  reads `CLAUDE.md` instead of this file.
- Write every comment in Simplified Technical English (see Comments).

## Find the Rule for a Task

| Task | Read | Start from |
|---|---|---|
| Add a feature | core.md "New Feature Checklist" | copy `internal/note/` |
| Add a feature that calls a model | core.md "Model Providers" | copy `internal/chat/` (LLM) or `internal/triage/` (System One) |
| Add a model provider | core.md "Add a Provider" | `internal/platform/llm/anthropic/`, `internal/platform/systemone/typesafe/` |
| Add an API with the Chat Completions shape | core.md "Add a Provider" | one `chatcompletions.New` in `newLLM` (`internal/app/models.go`) |
| Add a background or a scheduled job | core.md "Background Jobs" | `internal/auth/jobs.go`, `internal/app/worker.go` |
| Add or change a route, a handler, a template | http.md | `internal/note/http.go` |
| Stream a response (SSE) | http.md "Streaming (SSE)" | `internal/chat/http.go`, `internal/platform/web/sse.go` |
| Change the schema | database.md "Migrations" | `just makemigration <name>` |
| Write a list query | database.md "Queries at Scale" | `note.Service.List`, `note.Repo.ListByUser` |
| Call another feature's logic | core.md "Dependency Direction" | a consumer-side interface, wired in `app.New` |
| Add a config variable | core.md "Dependency Injection" | `internal/platform/config/config.go` |
| Add a rate limit | security.md "Rate Limits" | `internal/app/app.go` |
| Write or fix tests | testing.md | `internal/app/client_test.go`, `internal/note/service_test.go` |
| Touch sessions, passwords, headers, CSRF | security.md | `internal/auth/`, `internal/user/password.go` |
| Design a new package or an exported API | `.agents/skills/go/SKILL.md` | the local rules win where they differ |
| Review a design before a larger change | `.agents/skills/go-spec-reviewer/SKILL.md` | a new feature, provider, or cross-feature interface |

## Rules & Skills

The `@` lines below import all five rules, so Claude Code loads them
together with this file. An agent that does not expand `@` imports must
read the relevant rule before it edits code. The `paths:` frontmatter of a
rule lists the files that the rule covers.

- @.agents/rules/core.md — package layout, dependencies, services, model providers, errors, logging
- @.agents/rules/http.md — middleware chain, handlers, htmx fragments and redirects, SSE, templates, auth
- @.agents/rules/database.md — repositories, SQL, keyset pages, transactions, timeouts, migrations
- @.agents/rules/testing.md — a real database per test, the test helpers, what to test where, the checks
- @.agents/rules/security.md — sessions, passwords, input, headers, rate limits, the production checklist

Agents do not discover the skills in `.agents/skills/` automatically. Read
a skill file when the table above points to it.

## Verify Before You Finish

A task is done only when all these checks pass:

1. `gofmt -l -w .`
2. `just lint` — `go vet`, `go fix -diff`, staticcheck, and the gofmt
   check. Run `go fix ./...` to apply the modernizers.
3. `just types` — compiles the code and the tests without a database.
4. `just complexity` — cognitive complexity must be 10 or less.
5. `just test` — needs Docker. It starts `postgres-test` on port 5433.
   When that port is busy, set `POSTGRES_TEST_PORT`.

`just ci` runs all of these plus govulncheck. When a template or an htmx
flow changes, also check the page in a browser: httptest cannot see a CSP
violation or a broken swap. When a check cannot run (for example, Docker
is not available), report that. Never report a check that did not run as
passed.

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

Request flow: `internal/app` (middleware + mux) → `internal/<feature>/http.go`
→ `service.go` → `repository.go` ← `postgres.go`

Job flow: `cmd/api worker` → `app.NewWorker` → `internal/<feature>/jobs.go`
→ `service.go` → `repository.go` ← `postgres.go`

Job life cycle: one row in `jobs`; only `internal/platform/jobs` writes
the tables `jobs` and `job_schedules`.

- `jobs.Enqueue` or a schedule slot inserts the row as `queued`.
- A claim (`FOR UPDATE SKIP LOCKED`, one `UPDATE`) sets `running`, adds 1
  to `attempts`, and sets the lease `locked_until` (the timeout + 30 s).
- The handler result sets `done`, `queued` with a backoff (an error), or
  `failed` (`jobs.Permanent`, or no attempts left). Each result `UPDATE`
  matches the attempt number, so a stale worker changes nothing.
- SIGTERM returns a running job to `queued` and gives back its attempt.
  The cleanup (each minute) returns a job with an expired lease, and
  deletes `done` after 24 h and `failed` after 7 days.
- A handler that ignores ctx blocks its claim loop until the process
  exits. A kind without a handler stays `queued` forever.
- The rules: core.md "Background Jobs" and database.md "Background Jobs".
  Monitoring queries and limits: README "Operate the queue".

```
cmd/api/          main, run(), subcommands (migrate, adduser, worker), debug listener
migrations/       embedded SQL, append-only
internal/
  platform/       shared infrastructure; imports no feature
    config/       Config, Load(getenv), Validate
    database/     pgx pool + migrator
    logging/      slog + context-carried log attributes
    jobs/         job queue: Enqueue, Worker (Handle, Schedule, Run), retries, leases
    llm/          text models: Provider, Router, Fake; anthropic/ (Claude,
                  Bedrock), chatcompletions/ (Mistral, xAI, local servers);
                  both on net/http
    systemone/    System One models: Provider, Router, Fake; typesafe/
    web/          templates, errors, middleware, rate limiter, SSE, assets
  app/            the only wiring point: app.New (routes), NewWorker (jobs);
                  models.go: model providers
  auth/           sessions; knows users only as ids; jobs.go: hourly cleanup
  user/           login + the /me dashboard
  note/           list with keyset pages — copy it for a new feature
  chat/           LLM example: streams the answer over SSE (no storage)
  triage/         System One example: routes a message (no storage)
  testdb/         a private, migrated database per test
```

## Commands

```bash
just compose         # full stack in Docker (api + worker + postgres)
just app             # API on the host (needs: docker compose up postgres -d)
just worker          # the background jobs on the host
just test            # starts postgres-test, runs go test -race ./...
just test -run TestNotes   # extra arguments go to go test
just ci              # lint + types + complexity + test + vulncheck
just fmt / lint / types / complexity / build / migrate
just makemigration create_toys_table
just adduser bob@example.com "Bob"   # prints a generated password once
```

The app runs at `http://localhost:8080`. Create an account with
`just adduser`, then log in. Without model keys in `.env`, the chat and
triage cards on `/me` use the fake models.
