---
paths:
  - "**/*.go"
---

# Go Core Rules

Style and structure rules for this codebase. The vendored `go` skill
(`.agents/skills/go/SKILL.md`) is a reference for idiomatic Go. These local
rules take precedence where the skill differs: retain `internal/`,
feature-local HTTP code, repository interfaces, and stdlib-first tests.
Do not add Afero, go-cmp, or another dependency solely because the skill
suggests it. The application has one direct dependency: pgx. The model
providers call their APIs with `net/http` and use no SDK (see Model
Providers). The job queue (`internal/platform/queue`) calls the SQL API
of PgQue with pgx; PgQue itself is a vendored SQL migration, not a Go
module. Do not add a PgQue client module. The files in
`.agents/rules/` are the single source of truth for conventions.
`AGENTS.md` and `README.md` point to them and do not repeat them. Change
a convention here, not there.

## Package Layout: Features, Not Layers

```
cmd/api/            main + run() + serve(); subcommands migrate, adduser,
                    worker; debug listener; no business logic
migrations/         embedded SQL, append-only; new files are timestamp-prefixed;
                    *_install_pgque.sql is vendored PgQue
internal/platform/  shared infrastructure: config, database, logging, web,
                    queue, llm, systemone; one subpackage per provider API
                    (llm/anthropic, llm/chatcompletions, systemone/typesafe)
internal/app/       composition — the only place that wires features:
                    app.go the routes, worker.go the jobs; models.go
                    registers the model providers
internal/auth/      sessions; knows users only as id strings; jobs.go has
                    the hourly session cleanup
internal/user/      feature slice (also owns login and the /me dashboard)
internal/note/      feature slice — copy this one for a new feature
internal/chat/      feature slice without storage — the LLM + SSE example
internal/triage/    feature slice without storage — the System One example
internal/testdb/    test databases
```

- A new feature = a new package `internal/<name>`. Copy `internal/note/` and
  adapt. Keep the file split: `<name>.go` (types + errors), `service.go`,
  `repository.go` (interface), `postgres.go` (implementation), `http.go`
  (routes + handlers), `templates/`, `service_test.go`. A feature with
  background jobs adds `jobs.go` (see Background Jobs). A feature that
  stores nothing (`internal/chat`, `internal/triage`) omits
  `repository.go` and `postgres.go`; its package doc says why.
- Layers live as **files inside a feature package**, never as layer-named
  packages (`services/`, `repositories/`, `handlers/`, `models/` are banned).
- Grow a feature with more files, not with subpackages. When a canonical
  file outgrows one topic, split it by resource and keep the canonical name
  as the prefix: `http.go` → `http_share.go`, `postgres.go` →
  `postgres_share.go`. These prefixes identify files within a feature;
  they do not introduce layer packages.
- Never create `utils/`, `helpers/`, `common/`, or `pkg/`.
- Background work and scheduled work run in the worker: the `worker`
  subcommand of `cmd/api` (see Background Jobs). Do not add a cron binary
  for a schedule. A second binary for another purpose is a new
  `cmd/<name>/` that reuses `internal/*` and wires itself the same way
  `cmd/api` does.
- `internal/platform/` holds only application-agnostic infrastructure. It
  must not import any feature.

## New Feature Checklist

1. If the feature changes the schema, start with
   `just makemigration create_<name>s` (see database.md).
   Mirror value `CHECK`s in Go constants.
2. Copy `internal/note/` to `internal/<name>/`. Rename the types. Write the
   SQL. Keep the file split above.
3. Register the feature in `internal/app/app.go`: construct the service,
   call `<name>.Routes(...)` on the pages mux.
4. Wrap every authenticated route with `auth.RequireIdentity` inside
   `Routes` (see http.md).
5. Write `service_test.go` (business rules + SQL) and add
   `internal/app/<name>_test.go` (user-visible flows, see testing.md).
6. To show the feature on the `/me` dashboard, add an `hx-get`
   placeholder to `internal/user/templates/me.html` (see http.md).
7. If the feature has background jobs, add a migration that creates its
   queue and subscribes `worker`, add `jobs.go`, and call
   `<name>.Jobs(w, svc)` in `app.NewWorker` (see Background Jobs).
8. `just ci` must pass.

A feature that stores nothing skips step 1 and omits `repository.go` and
`postgres.go`. Copy `internal/chat` (LLM) or `internal/triage` (System
One) when the feature calls a model. Such a feature also needs:

- The provider and the model reference in its constructor:
  `triage.NewService(models.SystemOne, cfg.SystemOneModel)`.
- Its prompt or its questions, the input caps, and the timeout of the
  call in `service.go` (see Call a Model).
- A `web.RateLimiter` from `app.New`, because each call costs money.
- Service tests with a stub `Provider` and HTTP tests with the fake
  model (see testing.md).

## Services: Business Logic

`service.go` is the home of the business rules. A handler parses input and
maps results. A repository runs SQL. Everything between the two belongs to
the service:

- Validate and normalize input (trim, length caps, allowed values). Return
  the feature's `ValidationError` with a message that is safe to show.
  Mirror each database `CHECK` as a Go constant (`maxTextLength`).
- Scope every method to its owner. Methods take `ctx` first, then the
  owner id, then the resource id: `Delete(ctx, userID, id)`. Repository
  methods use the same order, so an argument swap is easy to see.
- Make the product decisions: the page size, the default sort, what a
  missing row means. `note.Service.List` owns `pageSize` and the cursor of
  the next page. The repository only runs the query that it gets.
- Coordinate several repository calls for one use case. When the writes
  must succeed together, put them in one repository method (see
  database.md). A service never opens a transaction.
- Return feature types and sentinels (`ErrNotFound`). A service never
  takes `http.Request` or `http.ResponseWriter` and never sees pgx types.
  `auth.Service` is the documented exception (see http.md).
- Keep a service method small and linear. When a rule grows, give it an
  unexported function in `service.go` and a table test in
  `service_test.go`.

## Dependency Direction & Cross-Feature Calls

- Among application packages, features may import `auth` and `platform`.
  Features never import each other. `auth` may import only `platform`.
  `app` wires these packages. Standard library and pgx imports follow the
  dependency policy above. An import cycle means the boundary is wrong.
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

  Keep the interface minimal: only the methods A calls. Frequent calls
  between two features are a reason to review their boundaries. Merge them
  only when they represent the same business responsibility.
- Review cross-feature transaction needs explicitly (see database.md).

## Dependency Injection

- Explicit constructor injection only: `NewRepo(pool)`, `NewService(repo)`,
  `app.New(logger, cfg, pool, models)`. Adding a dependency means adding a
  parameter — the compiler then finds every wiring point.
- No DI frameworks, no package-level mutable state, no `init()`.
  Package-level values are limited to error sentinels, embedded files, and
  parsed templates that callers never modify. Tests may also keep compiled
  regular expressions. Do not use globals for application state.
- Only `config.Load` reads the environment, through its `getenv` argument
  (`testdb` is the test-side exception). Everything else receives `Config`
  or the field it needs.
- An unset `ENVIRONMENT` means production, so a missing variable fails
  closed: `DATABASE_URL` is then required, the cookie is Secure, and HSTS
  is on. Only development gets local defaults. The `just` host recipes set
  `ENVIRONMENT=development` when `.env` does not set it.
- `Config.Validate` must reject hard misconfiguration (unknown
  `ENVIRONMENT`, unknown `LOG_LEVEL`, missing `DATABASE_URL` outside
  development, invalid `PORT`, a `DEBUG_ADDR` that is not loopback, an
  `LLM_MODEL` or a `SYSTEMONE_MODEL` without the form `provider/model`). The
  service refuses to start. It never uses a silent fallback. Extend
  `Validate` when you add a config field with constraints.
- Repository interfaces are defined in the feature (`repository.go`), next
  to their consumer (`Service`), and implemented in `postgres.go`. Keep
  `var _ Repository = (*Repo)(nil)` as the compile-time check.

## Model Providers

- The app has two kinds of model. They serve different purposes, so each
  kind has its own core package, its own providers, and its own default
  model:
  - An **LLM** writes text: an answer, a summary, a draft. The core is
    `internal/platform/llm`. `LLM_MODEL` sets the default model
    (`fake/echo`).
  - A **System One** model judges a state. It picks an option (choice),
    places the state on a scale (score), or checks a statement (noul),
    and it returns calibrated probabilities. TypeSafe Jev and Clef are
    examples. The core is `internal/platform/systemone`.
    `SYSTEMONE_MODEL` sets the default model (`fake/uniform`).
- Keep the two kinds apart. Do not add fields to `llm.Request` for a
  System One model, and do not add text generation to `systemone`. Keep
  the cores neutral: do not add a field to a request for one vendor.
- The template has these adapters. One adapter can serve many providers:
  - `internal/platform/llm/anthropic` (`net/http`): the providers
    `anthropic` and `bedrock`. Bedrock uses the same adapter with the
    base URL `https://bedrock-runtime.<region>.amazonaws.com/anthropic/`
    and a Bedrock API key.
  - `internal/platform/llm/chatcompletions` (`net/http`): the APIs with
    the shape of the Chat Completions API. The providers are `mistral`,
    `xai`, and `local` (Ollama, vLLM, llama.cpp at `LOCAL_LLM_URL`).
  - `internal/platform/systemone/typesafe` (`net/http`): Jev.
- `app.NewModels` (`internal/app/models.go`) returns `app.Models{LLM,
  SystemOne}`. `newLLM` and `newSystemOne` register the providers of each
  kind and resolve the default model at startup. A real provider is
  registered only when its key or its URL is set (`ANTHROPIC_API_KEY`,
  `BEDROCK_API_KEY` with `BEDROCK_REGION`, `MISTRAL_API_KEY`,
  `XAI_API_KEY`, `LOCAL_LLM_URL`, `TYPESAFE_API_KEY` in `.env`). When the
  default model names a provider that is not registered, the startup
  stops, and the error names the keys.
- To change the model or the vendor, change `LLM_MODEL` and set the key.
  Do not change a feature for it.
- A feature takes only the kind of model that it needs, together with its
  default model: `chat.NewService(models.LLM, cfg.LLMModel)`. A feature
  that needs both kinds takes both providers. Features depend only on
  `llm.Provider` or `systemone.Provider`. A feature never imports a
  provider package or an SDK.
- When a feature needs a model other than the default, add a config
  field such as `SUMMARY_MODEL` with the same `provider/model` check in
  `Validate`, and pass it to the constructor of the feature.
- A model reference has the form `provider/model`
  (`anthropic/claude-opus-5-5`, `typesafe/jev-latest`). Each Router cuts
  at the first slash, so the model part can contain more slashes.

### LLM

- `internal/platform/llm` has `Provider` (one `Stream(ctx, req, onText)`
  method), `Generate`, `Router`, and `Fake`.
- A feature owns its prompt and its cost caps: the system prompt, the
  input length, and `MaxTokens` live in its `service.go` (see
  `internal/chat`). `llm.Fake` rejects a request without `MaxTokens`, so
  a feature that forgets the cap fails its tests. The chat cap is 4096
  tokens. On a model with thinking, the thinking tokens count toward the
  cap too.

### System One

- `internal/platform/systemone` has `Provider` (one `Ask(ctx, req)`
  method), `Router`, and `Fake`. A request holds the model, the state,
  and the questions by id. Send structured state as JSON text. The
  response holds one answer for each question id, the exact model
  version, and the usage.
- `systemone.Fake` (`fake/uniform`) knows nothing: each option and each
  level gets the same probability, the confidence is zero, and a noul is
  0.5. It rejects the requests that a real provider rejects: no state, no
  questions, no instructions, a choice with fewer than 2 options, or a
  score with fewer than 2 levels.
- A feature owns its questions and their meaning: the instructions, the
  options, the levels, and the thresholds for a decision live in its
  `service.go`.

### Call a Model

- The feature sets the deadline of each call with `context.WithTimeout`
  in its `service.go` (`answerTimeout` in chat, `triageTimeout` in
  triage). The call also ends when the client leaves.
- The adapters do not retry. A rate limit or a server error returns an
  error with the status code. When a call must succeed later, run it in
  a job: the job queue retries it with a backoff (see Background Jobs).
- Never call a model inside a database transaction. The transaction then
  holds a connection and its locks for the whole call.
- Check the output of a model like user input. `triage.decide` rejects an
  unknown option and a probability outside 0..1.
- Log only the usage counts and the model version. Never log a key, a
  prompt, a state, or an answer.

### Add a Provider

When the API has the shape of the Chat Completions API (`POST
<base>/chat/completions` with a `data:` event stream), do not write a new
adapter. Add the key or the URL to the config (step 3 below) and register
`chatcompletions.New(baseURL, key)` in `newLLM` (step 4). Set
`StreamUsage` when the server sends the token counts only on request
(`stream_options.include_usage`). Mistral sends them without the option
and can reject an unknown field, so `mistral` leaves it off.

For another API:

1. Create `internal/platform/llm/<name>/` or
   `internal/platform/systemone/<name>/` with a type that implements the
   `Provider` of that core. Map the values of the provider to the core
   types (`llm.StopReason`, `systemone.QuestionType`), and fill `Usage`.
   The constructor takes all its values: `New(baseURL, apiKey string)`.
   Tests then point it at an `httptest.Server`.
2. Call the API with `net/http`. Copy the structure of
   `internal/platform/llm/anthropic` or `chatcompletions`: one request
   type, one stream reader, and a cap on the length of one line. Return
   only the status code on an error, because the error body can repeat
   the request. A provider error must not contain the key or the request
   text.
3. Add the key to `Config`, `config.Load`, `.env.example`, and the `api`
   environment in `docker-compose.yml` (also the `worker` environment
   when a job calls the model). Extend `Validate` when a field has
   constraints. Bedrock uses a Bedrock API key and `BEDROCK_REGION`. A
   provider that needs the credential chain of its cloud (IAM roles,
   Vertex) needs the SDK of that cloud: that is a new dependency, so ask
   first.
4. Register the provider in `newLLM` or `newSystemOne` only when its key
   or endpoint is set. Name the key in the startup error.
5. Test the request mapping, the response parsing, and the error path
   against an `httptest.Server`. Tests never call a live model.

Do not add the SDK of a provider. An SDK adds many indirect modules, and
an adapter on `net/http` has about 200 lines. When an API cannot work
without an SDK (for example, a signature scheme of a cloud), ask first.
Then keep the SDK import inside the provider package, and record the
dependency in `docs/third-party.md`.

## Background Jobs

Background jobs run on PgQue (https://pgque.dev), a message queue in
PostgreSQL. The migration `*_install_pgque.sql` installs PgQue 0.2.2 as
plain SQL; it is vendored without changes (see `docs/third-party.md`).
`internal/platform/queue` calls the SQL API of PgQue with pgx. The
`worker` subcommand of `cmd/api` runs it. Use a job for work that must not
block a request, for a call to another service that needs retries, and
for scheduled work.

- **Queues.** Each feature with jobs has its own queue with the name of
  the feature, and the worker reads it as the consumer `worker`. A
  migration creates both: `SELECT pgque.create_queue('toy'); SELECT
  pgque.subscribe('toy', 'worker');`. Subscribe before the first send: a
  new consumer does not see older messages. `Run` and `RunDue` fail when a
  consumer has no subscription. Set `max_retries` (default 5) or
  `ticker_max_lag` (default 3 s) with `pgque.set_queue_config` in the
  same migration when the defaults do not fit.
- **Register.** A feature puts its consumer, its handlers, and its
  schedules in `jobs.go`: `func Jobs(w *queue.Worker, svc *Service)`.
  This is the same shape as `Routes`. Keep the queue name, the consumer
  name, and each message type in constants there. `app.NewWorker`
  (`internal/app/worker.go`) constructs the services and calls each
  `Jobs`. It is the only wiring point of the jobs. The example is
  `internal/auth/jobs.go`.
- **Types.** A message type has the form `<feature>.<action>`, for example
  `auth.delete_expired_sessions`. It has 1 to 57 characters: `a-z`,
  `0-9`, `_`, and `.`. A consumer gets every message of its queue, so it
  must handle every type of the queue. A type without a handler retries
  and then goes to the dead letter queue. To rename a type, add the new
  type and keep the old handler until no old message waits.
- **Handlers.** A handler is a maker function that closes over the
  service and returns `queue.Handler`, like an HTTP handler. It decodes
  the payload, calls the service, and returns the error. The business
  rules stay in `service.go`. A handler does not log its error: the worker
  logs each result once.
- **Idempotency.** Delivery is at least once. A crash, a deploy, or a lost
  lock delivers the whole batch again, also its finished messages. Make a
  second run safe: check the state first, use an upsert, or send the
  message id as the idempotency key to an external service.
- **Errors.** An error sends the message back (`pgque.nack`) after a
  backoff: 15 s, doubled on each retry, up to 1 h, plus up to 25 %
  jitter. After the `max_retries` of the queue, the message goes to the
  dead letter queue (`pgque.dead_letter`). Wrap an error that a retry
  cannot fix with `queue.Permanent` (a bad payload, a deleted row): the
  message goes to the dead letter queue at once. A panic counts as an
  error. The log and the dead letter reason get the error text, so keep
  private text out of it.
- **Timeouts.** `Handle(type, timeout, h)` sets the deadline of each run.
  The handler must return when ctx ends. The messages of one consumer run
  one after another, so a slow handler delays the rest of its queue.
- **Payloads.** Store ids and small values, and read the rows in the
  handler. Never put a key, a password, a token, a prompt, or private
  text in a payload. The worker never logs a payload.
- **Send.** Send in a repository method, in the transaction that writes
  the rows: `queue.Send(ctx, tx, "toy", "toy.send_receipt", payload)` (see
  database.md). A service has no pool, so a service never calls
  `queue.Send` itself. PgQue 0.2 has no delayed delivery.
- **Schedules.** `Schedule(queue, type, every, offset)` sends one message
  for each slot, in UTC. `every = 24h` with `offset = 3h` runs daily at
  03:00 UTC; a weekly slot starts on Monday. The `job_schedules` row of
  the type keeps the last slot, so each slot goes out once, also after a
  change of the active worker; a retry or a crash can run that message
  again. The worker does not send the slots that it missed while no
  worker ran. Do not add a cron library, and do not start the pg_cron
  jobs of PgQue (`pgque.start()`): the active worker is the ticker.
- **Models.** A job may call a model. The service sets the timeout of the
  call, and the call never runs inside a transaction (see Call a Model).
  Add the key to the `worker` environment in `docker-compose.yml`.
- **Replicas.** PgQue gives a batch to a consumer, not to a process, so
  only one worker is active: the worker that holds the session advisory
  lock. It runs the ticker, the maintenance, the schedules, and one
  goroutine for each consumer. The other workers wait and take over when
  the lock is free. More replicas give failover, not throughput. To spread
  the load, give a feature its own queue. Do not use the cooperative
  consumers of PgQue: they are experimental in 0.2. Each process has its
  own pool, and the active worker keeps one connection for the lock, so
  keep the sum of the pools of all API and worker replicas below
  `max_connections`.
- **Shutdown.** SIGTERM stops new batches. The running handlers get 10 s;
  then their contexts end. A batch with a stopped handler stays open and
  comes back as a whole, and the stopped run does not count as a retry.
  Give the worker container a stop grace period of at least 15 s.
- **Upgrade.** Never edit the vendored migration. To upgrade PgQue, add a
  migration with the `sql/pgque.sql` of the new release (it upgrades in
  place) and update `docs/third-party.md`.
- **Not included.** Priorities, delayed messages, unique messages, and
  LISTEN/NOTIFY wakeups (an idle consumer waits for a tick of the active
  worker or 5 s). Add one only when a feature needs it.

## Errors

- Wrap with action context: `fmt.Errorf("inserting note: %w", err)`.
- Each feature defines sentinel errors (`note.ErrNotFound`) and a
  `ValidationError` string type whose text is safe to show to end users.
- Repos translate driver errors (`pgx.ErrNoRows`, PgError 23505) into
  sentinels; nothing above `postgres.go` sees pgx error types.
- Handlers are `web.HandlerE` and **return** their errors (see http.md):
  - `ValidationError` / conflict sentinels → 422 re-render with a message;
  - expected terminal states → `web.BadRequest` / `web.NotFound` /
    `web.HTTPError` (only `Msg` reaches the client);
  - everything else → `return err`; `web.RespondError` logs it once and
    sends an opaque 500.
- Never log and return the same error. Handlers normally do not log at
  all — `web.E` → `web.RespondError` does the one log call.
- Never `panic` in request paths; `RecoverPanics` is a last resort, not a
  control-flow mechanism.
- Job handlers also return their errors. The worker logs each result
  once. A dead letter stores the error text as its reason.

## Logging

- `log/slog` only, passed as a `*slog.Logger` constructor/function
  argument. Build it only with `logging.New(w, level, format)`
  (`internal/platform/logging`). The logger is context-aware. `cmd/api`
  picks `logging.Text` in development and `logging.JSON` otherwise; the
  logging package does not import config.
- On request paths, log with the `*Context` methods
  (`logger.ErrorContext(r.Context(), ...)`). The logging handler adds the
  context's correlation attributes to the record: `request_id` (seeded by
  `web.LogRequests`) and `user_id` (seeded by `auth.LoadIdentity`).
  In a job, the worker adds `queue`, `consumer`, `message_id`,
  `message_type`, and `message_retries` to the context of the handler.
  Outside requests (startup, shutdown, CLI), plain `Info`/`Error` is fine.
- Middleware adds correlation attributes with
  `logging.WithAttrs(ctx, slog.String(...))` — never re-attach them by hand
  at call sites.
- SQL traces exist only at `Debug`. The pgx adapter omits arguments and
  driver error payloads because they can contain private data. Keep SQL
  parameterized even in debug mode. The HTTP error boundary owns the
  operational error record. Query traces carry the request context.
- Levels: `Debug` = high-volume internals (incl. SQL), `Info` = lifecycle +
  request log, `Warn` = recoverable oddities, `Error` = needs attention.

## Keep It Denormalized

This template optimizes for readability and agentic editing, not DRY:

- Prefer explicit, repeated code over clever abstraction. Two similar
  handlers are better than one generic one.
- No generic repositories, generic services, or base structs.
- Every SQL query is written out in the `postgres.go` that uses it.

## Use Current Go

- `go.mod` pins the version (currently 1.27.3) and is authoritative. When
  bumping, keep the Dockerfile (`golang:1.xx.y`) and the Go badge in
  README.md in sync.
- Use: `any`, `for i := range n`, `min`/`max`, `cmp.Or` for defaults,
  `slices`/`maps`, `math/rand/v2`, `errors.Join`, `crypto/pbkdf2`,
  `http.CrossOriginProtection`, `http.MaxBytesHandler`.
- Loop variables are per-iteration — never write `x := x` captures.
- `gofmt -l -w .` before finishing any task. `just lint` must pass — it
  runs `go vet`, `go fix -diff` (the Go modernizers; run `go fix ./...`
  to apply them), staticcheck with all checks (version-pinned in the
  justfile), and the gofmt check.

## Development Checks

- `just types` compiles all packages and tests with the Go compiler. Go does
  not need a separate type checker. The recipe does not run tests or use a DB.
- `just complexity` uses pinned gocognit. Cognitive complexity must be <= 10
  in application code and test support (`internal/testdb`). Test functions
  are excluded because sequential assertions do not model application flow.
  Keep tests readable; do not split them merely to lower a score.
- `just ci` runs lint, types, complexity, race tests, and govulncheck.
  Tool versions live in the justfile. Keep tools outside the application
  module, so they do not add dependencies to `go.mod`.
- The repository has no CI/CD pipeline. Run `just ci` before each commit.
  When the Dockerfile or the Go version changes, also run `docker build .`.
  Update the Go patch version in go.mod and Dockerfile together when
  security fixes arrive.
