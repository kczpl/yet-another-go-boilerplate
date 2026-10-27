# yet-another-go-boilerplate

<p align="center">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.27.3+-00ADD8?logo=go&logoColor=white">
  <img alt="net/http" src="https://img.shields.io/badge/net%2Fhttp-stdlib-00ADD8?logo=go&logoColor=white">
  <img alt="PostgreSQL" src="https://img.shields.io/badge/PostgreSQL-18-4169E1?logo=postgresql&logoColor=white">
  <img alt="pgx" src="https://img.shields.io/badge/pgx-v5-336791?logo=postgresql&logoColor=white">
  <img alt="htmx" src="https://img.shields.io/badge/htmx-2-3366CC?logoColor=white">
  <img alt="just" src="https://img.shields.io/badge/just-tasks-DE5FE9?logo=just&logoColor=white">
</p>

Minimal, feature-based Go web service template. Stdlib `net/http`,
PostgreSQL via [pgx](https://github.com/jackc/pgx), server-rendered HTML
with [htmx](https://htmx.org) (vendored, no JS build), embedded SQL
migrations, `log/slog`, and tests against a **real PostgreSQL** — no
frameworks, no ORM, no DI container, no mocks.

**One direct dependency: pgx.** Sessions, password hashing, CSRF
protection, rate limits, Server-Sent Events, templates, and migrations
come from the standard library. Background jobs run on
[PgQue](https://pgque.dev), a queue in PostgreSQL that a migration
installs as plain SQL; the worker calls it through pgx. Every model provider (Anthropic, Bedrock,
Mistral, xAI, local servers, TypeSafe) calls its API with `net/http` and
uses no SDK. `go.mod` pins the Go version, and the Dockerfile uses the
same one.

Built on two references:

- [How I write HTTP services in Go after 13 years](https://grafana.com/blog/how-i-write-http-services-in-go-after-13-years/) (Mat Ryer) — `run()`, handler makers, explicit wiring
- [spf13/go-skills](https://github.com/spf13/go-skills) — feature packages instead of layer packages, stdlib-first, vendored into `.agents/skills/` ([why Go fits agentic coding](https://spf13.com/p/go-the-agentic-language/))

## Contents

- [Quickstart](#quickstart)
- [Design principles](#design-principles)
- [Architecture](#architecture)
- [Build a feature](#build-a-feature)
- [Work with models](#work-with-models)
- [Background jobs](#background-jobs)
- [Testing](#testing)
- [Configuration](#configuration) · [Commands](#commands) · [Conventions](#conventions)
- [Working with AI agents](#working-with-ai-agents) · [Renaming](#renaming) · [Before production](#before-production)

## Quickstart

```bash
cp .env.example .env
just compose                                # full stack in Docker, or:
docker compose up postgres -d && just app   # the app on the host
just worker                                 # the background jobs (another terminal)
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
cmd/api/            main: config → pool → migrate → app.New → server; subcommands: migrate, adduser, worker
migrations/         embedded SQL migrations, applied on startup (append-only, timestamp-named);
                    one of them installs PgQue
internal/
  platform/         shared infrastructure — features import it, it imports no feature
    config/         env-driven configuration
    database/       pgx pool + embedded SQL migrator
    logging/        slog construction + context-carried log attributes
    queue/          background jobs on PgQue: consumers, retries, schedules, one active worker
    llm/            provider-neutral core for text models; anthropic/ (Claude, Bedrock),
                    chatcompletions/ (Mistral, xAI, local servers); both on net/http
    systemone/      provider-neutral core for System One models; typesafe/ (net/http)
    web/            layout, rendering, errors, middleware, rate limiter, SSE, static assets
  app/              composition: app.New wires the routes, NewWorker wires the jobs
  auth/             sessions: cookie ↔ hashed token in the database; hourly cleanup job
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
- `app` is the only package that imports everything. `app.New` wires the
  routes, and `app.NewWorker` wires the background jobs.

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
| Real providers | `anthropic`, `bedrock`, `mistral`, `xai`, `local` (see below) | `typesafe` — Jev, plain `net/http` |
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

The LLM providers:

| Provider | Example `LLM_MODEL` | Setting | Adapter |
|---|---|---|---|
| `anthropic` | `anthropic/claude-opus-5-5` | `ANTHROPIC_API_KEY` | `llm/anthropic` (the Messages API) |
| `bedrock` | `bedrock/us.anthropic.claude-sonnet-5` | `BEDROCK_API_KEY`, `BEDROCK_REGION` | `llm/anthropic` on the Anthropic route of Amazon Bedrock |
| `mistral` | `mistral/mistral-large-latest` | `MISTRAL_API_KEY` | `llm/chatcompletions` |
| `xai` | `xai/grok-4` | `XAI_API_KEY` | `llm/chatcompletions` |
| `local` | `local/llama3.2` | `LOCAL_LLM_URL` | `llm/chatcompletions` (Ollama, vLLM, llama.cpp) |

To change the model or the vendor, change `LLM_MODEL` and set the key. The
features do not change. Mistral, xAI, and the local servers use the shape
of the Chat Completions API, so one small adapter on `net/http` serves all
three.

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
- **No hidden retries.** The adapters do not retry. A rate limit or an
  overloaded server returns an error at once. When a call must succeed
  later, run it in a [background job](#background-jobs): the queue
  retries it with a backoff.
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

When the vendor has an API with the shape of the Chat Completions API
(for example Groq, OpenRouter, or a vLLM server), you need no new package.
Add the key or the URL to the config and register one more
`chatcompletions.New(baseURL, key)` in `newLLM`.

For another API, create a subpackage `internal/platform/llm/<name>/` or
`internal/platform/systemone/<name>/` that implements the core interface.
Call the API with plain `net/http`, not with the SDK of the vendor: an
SDK adds many indirect modules. Add the key to the config, register the
provider in `app.NewModels`, and test the adapter against an
`httptest.Server`. The
step-by-step list is "Add a Provider" in
[`.agents/rules/core.md`](.agents/rules/core.md).

## Background jobs

Background jobs run on [PgQue](https://pgque.dev)
([source](https://github.com/NikolayS/pgque)), a message queue in
PostgreSQL. PgQue is plain SQL and PL/pgSQL: a migration installs it, and
it needs no extension, no broker, and no extra Go module.
[`internal/platform/queue`](internal/platform/queue) calls its SQL API
with pgx. The same binary runs the HTTP server and the worker:

```bash
just worker                                                      # the worker on the host
docker compose up --build --scale worker=3 api worker postgres   # one active worker, two that wait
```

### How a message moves through PgQue

PgQue keeps a log of messages, not a table of job rows. A producer adds a
message to a queue. A consumer is a named cursor on the queue. A tick
marks a position in the log, and the messages between two ticks are one
batch. The consumer reads a batch, handles each message, and finishes the
batch. Then its cursor moves past the batch.

```
         send               tick             next_batch               ack
producer ─────► queue log ─────► batch ────────────► consumer ─────► the cursor moves on
                    ▲                                    │
                    │ due after the backoff              │ handler error
                    └──── retry queue ◄─── finish ◄──────┤
                                                         │ no retries left, or queue.Permanent
                                                         └──► dead letter queue (kept 7 days)
```

1. **Send.** `queue.Send` calls `pgque.send` in the transaction of the
   write. A schedule slot sends a message in the same way.
2. **Tick.** The active worker calls `pgque.ticker()` every second.
   PgQue makes a tick when a queue has new messages and its tick lag (3 s
   by default) has passed, or when 500 messages wait.
3. **Batch.** After a tick, each consumer opens its next batch
   (`pgque.next_batch`) and reads all its messages.
4. **Handle.** The worker calls the handler of each message type, in
   order. The context of a handler ends at the timeout of its type.
5. **Finish.** One transaction stores the results. A failed message goes
   to the retry queue (`pgque.event_retry_raw`) or to the dead letter
   queue (`pgque.event_dead`). One statement stores all failures and reads
   the batch only once, so a large batch also finishes quickly. Then
   `pgque.ack` closes the batch. A rollback leaves the batch open, so the
   next call delivers the whole batch again.
6. **Maintain.** Every 10 seconds, the active worker moves the due
   retries back to their queues, runs the table rotation of PgQue, and
   deletes the dead letters that are older than 7 days. The maintenance
   runs in its own goroutine on the pool, so a slow pass does not delay
   the ticks.

PgQue does not update or delete a message row. Each queue writes into
three tables, and PgQue empties the oldest table with `TRUNCATE` when all
consumers are past it (every 2 hours by default). The queue tables collect
no dead rows, so they need no autovacuum tuning.

### Create a queue

Each feature has its own queue with the name of the feature. A migration
creates the queue and subscribes the consumer of the worker. Subscribe the
consumer before the first message: a new consumer does not see the older
messages.

```sql
-- migrations/20261026215435_create_auth_queue.sql
SELECT pgque.create_queue('auth');
SELECT pgque.subscribe('auth', 'worker');
-- Optional: the retry limit (default 5) and a shorter tick lag (default 3 s).
-- SELECT pgque.set_queue_config('auth', 'max_retries', '10');
-- SELECT pgque.set_queue_config('auth', 'ticker_max_lag', '500 milliseconds');
```

`Run` and `RunDue` stop with an error when a consumer has no
subscription. A check that fails for another reason, for example during a
database restart, retries every 5 seconds.

### Send a message

A feature sends a message in the repository method that writes its rows,
in the same transaction. The message then exists only when the rows exist:

```go
// internal/toy/postgres.go
err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
	// ... INSERT INTO toys ...
	_, err := queue.Send(ctx, tx, "toy", "toy.send_receipt", receipt{ToyID: toy.ID}) // ids, never secrets
	return err
})
```

PgQue 0.2 has no delayed delivery: a message goes out after the next
tick.

### Handle a message

A feature registers its consumer and its handlers in `jobs.go`, as it
registers its routes in `http.go`. The session cleanup is the example
([`internal/auth/jobs.go`](internal/auth/jobs.go)):

```go
func Jobs(w *queue.Worker, svc *Service) {
	c := w.Consumer("auth", "worker")
	c.Handle("auth.delete_expired_sessions", time.Minute, handleDeleteExpired(svc))
	w.Schedule("auth", "auth.delete_expired_sessions", time.Hour, 0) // every hour, on the hour (UTC)
}
```

`app.NewWorker` ([`internal/app/worker.go`](internal/app/worker.go)) calls
the `Jobs` function of each feature. It is the wiring point of the jobs, as
`app.New` is the wiring point of the routes.

### Retries, schedules, and shutdown

- **Retries.** A handler error sends the message to the retry queue
  after a backoff: 15 s, 30 s, 1 min, and so on, up to 1 hour, plus up to
  25 % random jitter. After the retry limit of the queue (`max_retries`,
  default 5), the message goes to the dead letter queue with the error
  text as its reason. `queue.Permanent(err)` sends it there at once, for
  example for a bad payload. A panic and a timeout count as errors.
- **One failure, one retry.** A failure retries only its own message. The
  other messages of the batch are done.
- **A batch that never finishes.** PgQue counts a retry only when the
  worker sends the message to the retry queue. A handler that crashes the
  process (for example, out of memory) or hangs without its context
  leaves the batch open, and the batch comes back without a retry. So the worker counts
  these runs in `queue_batch_runs`. After 3 runs that did not finish, it
  skips the handlers and sends each message of the batch to the retry
  queue (ERROR log `batch did not finish in earlier runs`). A message that
  crashes each batch therefore reaches the dead letter queue in the end,
  and the queue does not stop.
- **A type without a handler** also retries and then goes to the dead
  letter queue. A consumer must handle every message type of its queue.
- **Schedules.** `Schedule(queue, type, every, offset)` sends one message
  in each slot, in UTC. `every = 24h` and `offset = 3h` send it daily at
  03:00 UTC. The row of the type in `job_schedules` keeps the last slot,
  so each slot goes out once, also when another worker becomes active. A
  slot that no worker saw does not run later. A failed slot retries with
  the backoff (about 8 minutes in total with 5 retries), so with a short
  interval it can run after the next slot. PgQue has no scheduler, so
  `job_schedules` and `queue_batch_runs` are the only tables of the
  template queue code.
- **Shutdown.** On SIGTERM, the worker stops opening batches and gives the
  running handlers 10 s. Then it cancels their contexts. A batch with a
  stopped handler stays open and comes back to the next active worker as
  a whole. The stopped run does not count as a retry or as a run that did
  not finish, so a deploy cannot send a message to the dead letter queue.
  A failure before the stop in the same batch runs again too. The compose
  `stop_grace_period` of the worker is 15 s for this reason.

To run a dead letter again, replay it. It goes back to its queue as a new
message:

```sql
SELECT dl_id, dl_reason, ev_type, ev_data FROM pgque.dlq_inspect('auth');
SELECT pgque.dlq_replay(42);                 -- one dead letter
SELECT * FROM pgque.dlq_replay_all('auth');  -- all dead letters of the queue
```

### Many replicas, one database

PgQue gives a batch to a consumer, not to a process: two processes that
read the same consumer get the same batch. So only one worker is active:

- Each worker tries to take a session advisory lock. The worker with the
  lock is active: it runs the ticker, the maintenance, the schedules, and
  the consumers. Each consumer runs in its own goroutine, one batch at a
  time. Only the ticker uses the lock connection, so each tick also checks
  the lock.
- The other workers wait and try again every 5 seconds. When the active
  worker stops or its database session ends, the lock is free, and a
  waiting worker takes over.
- An active worker that loses its lock connection stops its handlers at
  once. Their batches stay open for the next active worker.

Keep these points in mind:

- **Delivery is at least once.** A crash or a stop in the middle of a
  batch delivers the whole batch again, also the messages that were done.
  Write each handler so that a second run is safe.
- **More replicas give failover, not throughput.** The messages of one
  consumer run one after another in one process. To spread the load, give
  a feature its own queue. PgQue has cooperative consumers for parallel
  work on one queue, but they are experimental in PgQue 0.2, and the
  template does not use them.
- **Connections.** The active worker keeps one pool connection for the
  lock. Each process has its own pool (the pgx default is the larger of 4
  and the number of CPUs; set `pool_max_conns` in `DATABASE_URL` to change
  it). Keep the sum over all API and worker replicas below
  `max_connections` of PostgreSQL.
- **Payloads and logs.** Store ids in a payload, not secrets or private
  text. The worker logs the queue, the consumer, the message id, the type,
  the retry count, and the error, never the payload. The dead letter queue
  stores the error text, so keep private text out of the handler errors.
- **One ticker.** The active worker drives PgQue. Do not also start the
  pg_cron jobs of PgQue (`pgque.start()`).

### Operate the queue

The worker has no metrics endpoint. PgQue has SQL functions for
monitoring:

```sql
-- The queues: the time since the last tick and the new messages.
SELECT queue_name, ticker_lag, ev_new, last_tick_id FROM pgque.get_queue_info();

-- The consumers: the age of the finished position and the waiting messages.
SELECT queue_name, consumer_name, lag, last_seen, pending_events FROM pgque.get_consumer_info();

-- The last dead letters of a queue and their reasons.
SELECT dl_time, ev_type, dl_reason FROM pgque.dlq_inspect('auth', 50);
```

Set alerts on these signals:

- **A consumer falls behind.** Its `lag` grows, for example over 5
  minutes. This alert finds a stopped worker and a worker that cannot keep
  up. An idle queue ticks less often (down to once per minute), so allow
  for that.
- **A message went to the dead letter queue.** The worker logs `message
  failed; it goes to the dead letter queue` at ERROR. The maintenance
  deletes a dead letter after 7 days, so the alert must come before that.
- **The worker lost its lock.** The ERROR record `worker lost the worker
  lock` means that the database session of the active worker ended.

Know these limits before you depend on the queue:

- **A handler must return when its context ends.** Go cannot stop a
  goroutine from the outside. A handler that ignores its context blocks
  its consumer until the process exits. At shutdown, the worker logs `a
  handler ignored the end of its context` at ERROR.
- **Latency.** A message usually starts 1 to 4 seconds after the commit:
  the tick lag of its queue plus the tick period. Set a shorter
  `ticker_max_lag` on a queue for a shorter wait. A retry starts up to 10
  seconds after its backoff.
- **A batch stays in memory.** The worker reads a whole batch at once,
  and a burst between two ticks makes a large batch. Keep payloads small.
- **Not included:** priorities, delayed messages, unique messages, a
  cancel operation, LISTEN/NOTIFY wakeups, and an admin page. Add one only
  when a feature needs it.

### Roles and upgrades

The install migration creates three roles: `pgque_reader`,
`pgque_writer`, and `pgque_admin`. A role belongs to the server, not to
one database, so the role that runs the migrations needs `CREATEROLE`, or
an administrator creates the three roles first. The app and the worker use
the role that owns the migrations, so they can call every PgQue function.
With separate roles, the API needs `pgque_writer`, and the worker needs
`pgque_admin`: the ticker and the maintenance are admin functions.

To upgrade PgQue, add a migration with the `sql/pgque.sql` file of the
new release. The file upgrades an install in place. Record the version and
the checksum in [`docs/third-party.md`](docs/third-party.md).

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
| Provider adapters | `internal/platform/llm/anthropic`, `llm/chatcompletions`, `systemone/typesafe` | request mapping, response parsing, errors without the key — against `httptest.Server` |
| Job queue | `internal/platform/queue`, `internal/app/worker_test.go` | send, retries, dead letters, schedules, maintenance, shutdown, one active worker — against PgQue in a real database |
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
| `BEDROCK_API_KEY` | – | Registers the `bedrock` provider (an Amazon Bedrock API key). Needs `BEDROCK_REGION`. |
| `BEDROCK_REGION` | – | The AWS region of Bedrock, for example `us-east-1`. |
| `MISTRAL_API_KEY` | – | Registers the `mistral` provider. |
| `XAI_API_KEY` | – | Registers the `xai` provider. |
| `LOCAL_LLM_URL` | – | Registers the `local` provider: the base URL of a local server, for example `http://localhost:11434/v1` (Ollama). In Docker, use `host.docker.internal`. |
| `TYPESAFE_API_KEY` | – | Registers the `typesafe` provider. |

## Commands

```bash
just app / compose / build / fmt / lint / types / complexity / test / ci
just worker                           # run the background jobs on the host
just migrate                          # apply migrations (startup does this too)
just makemigration create_toys_table  # new migration file (timestamp-named)
just adduser bob@example.com "Bob"    # create an account, prints the password
just vulncheck                        # govulncheck
```

`just ci` runs lint (`go vet`, `go fix -diff`, staticcheck, gofmt), types,
cognitive complexity (gocognit, at most 10), race tests against
PostgreSQL, and govulncheck. The repository has no CI/CD pipeline, so
run `just ci` before each commit. The justfile pins the tool versions,
and the tools do not add application dependencies.

## Conventions

The rules in `.agents/rules/` are the single source of truth for the
conventions, for people and for coding agents. This README explains the
ideas; the rules hold the details.

| Rule | Covers |
|---|---|
| [`core.md`](.agents/rules/core.md) | package layout, dependency direction, services, model providers, errors, logging |
| [`http.md`](.agents/rules/http.md) | middleware chain, handlers, htmx fragments and redirects, SSE, templates, auth |
| [`database.md`](.agents/rules/database.md) | repositories, SQL, keyset pages, transactions, timeouts, migrations |
| [`testing.md`](.agents/rules/testing.md) | a real database per test, what to test where, the checks |
| [`security.md`](.agents/rules/security.md) | sessions, passwords, CSRF, headers, rate limits, the production checklist |

Third-party code (pgx, PgQue, htmx, the vendored skills) is listed
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

The template deliberately has no public registration, password reset, or
generic unit of work. Point the orchestrator at `/livez` (liveness, no
database) and `/readyz` (readiness, a database ping). Run the worker as a
separate deployment (`api worker`) with a stop grace period of at least
15 s, and set the alerts from [Operate the queue](#operate-the-queue).
The PgQue migration creates three database roles, so the role that runs
the migrations needs `CREATEROLE` (see [Roles and
upgrades](#roles-and-upgrades)).
Before a public deployment, work through "Not Included — Add Before
Production" in [`.agents/rules/security.md`](.agents/rules/security.md):
TLS, limits for many replicas, a model budget, metrics, backups, and
database roles.
