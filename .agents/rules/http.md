---
paths:
  - "internal/**/http*.go"
  - "internal/auth/**/*.go"
  - "internal/app/**/*.go"
  - "internal/platform/web/**"
  - "internal/**/templates/*.html"
  - "cmd/**/*.go"
---

# HTTP & HTML Rules

The HTTP layer follows Mat Ryer's patterns
(https://grafana.com/blog/how-i-write-http-services-in-go-after-13-years/),
adapted to server-rendered HTML + htmx and to error-returning handlers.

## Composition & Routes

- `app.New(logger, cfg, pool, models)` builds the complete handler: repositories →
  services → feature routes → middleware. Tests construct the exact same
  handler as production. `cmd/api` adds only `http.Server` around it.
- Each feature exposes `Routes(mux, logger, deps...)` in its `http.go` and
  registers its own endpoints there. Registration composes the guard and
  the error adapter:
  `mux.Handle("GET /notes", auth.RequireIdentity(web.E(logger, handleNotes(svc))))`.
- Use method+wildcard mux patterns (`"POST /notes/{id}/delete"`,
  `r.PathValue("id")`). `"GET /{$}"` matches exactly `/`. No router
  libraries.
- **Browser-facing routes use only GET and POST.** HTML forms cannot send
  other methods, and every flow must work without JavaScript. A delete is
  `POST /notes/{id}/delete` with a real `<form>`; htmx upgrades the same
  URL with `hx-post`. Never register a browser-facing DELETE/PUT/PATCH.
- A feature owns the URL prefix that matches its package name
  (`note` → `/notes`, `chat` → `/chat`, `triage` → `/triage`). The
  account feature owns `/`, `/login`, and `/me`;
  auth owns `/logout`. `app.New` registers infrastructure paths:
  `/livez`, `/readyz`, `/static/`, and the `"/"` catch-all that turns every
  unmatched request into the unified 404. The catch-all sits behind the
  CSRF guard, so a cross-origin POST to an unknown path gets 403, not
  404 — tests must expect that.
- The middleware chain lives only in `app.New`. Keep this order (outermost
  first): `LogRequests` → `RecoverPanics` → `SecureHeaders` →
  `MaxBytesHandler` → outer mux (`/livez`, `/readyz`, `/static/`) →
  `PageHeaders` → `CrossOriginProtection` → `LoadIdentity` → pages mux.
  `SecureHeaders` adds HSTS outside development.
- `/livez`, `/readyz`, and `/static/` sit outside sessions and CSRF. None
  of them queries sessions. `/livez` does not use the database: an
  orchestrator restarts the process when liveness fails, and a restart does
  not repair a database outage. `/readyz` pings PostgreSQL, so a load
  balancer stops traffic during an outage. `LogRequests` does not log the
  two probes. Static assets do not use the database. `web.Static()`
  sets `Cache-Control: public, no-cache` because asset URLs have no version.
  A deploy does not clear a browser cache. PageHeaders sets `no-store` and
  `Vary: HX-Request, Accept` on dynamic responses, including auth redirects.
- A rate limit wraps the handler inside the error adapter:
  `web.E(logger, limiter.Limit(handleLogin(...)))`. `app.New` constructs
  each `web.RateLimiter` and passes it to `Routes`, so all limits and
  `CLIENT_IP_HEADER` stay in one place. A limited request gets 429 with
  `Retry-After` through `RespondError`. See security.md.
- `LogRequests` puts the request id in the response header and adds it to
  the request's log attributes with `logging.WithAttrs`. `LoadIdentity`
  adds `user_id` to the context passed to authenticated handlers. Log calls
  with that context include both attributes. The outer access log has the
  request id; it does not receive the inner user context.

## Handlers

- Handlers are maker functions that close over their dependencies and
  return `web.HandlerE`:
  `func handleNotes(svc *Service) web.HandlerE`. Handlers do not take a
  logger — `web.E`/`web.RespondError` owns error logging.
- One handler per endpoint. Handlers only: parse the form → call the
  service → map the result to a response. **No business logic in
  handlers.**
- Every handler that reads a form starts with:

  ```go
  if err := r.ParseForm(); err != nil {
      return web.BadRequest("invalid form data")
  }
  ```

  An oversized body (the `MaxBytesHandler` cap) surfaces here as a clean
  400.
- Read the user with `auth.IdentityFromContext(ctx)` — never from cookies
  or headers directly. Behind `RequireIdentity` the identity always exists.
- Page-data structs live next to their handlers and embed `web.Page`.
  Set `LoggedIn` explicitly per handler.
- The probe handlers `handleLivez` and `handleReadyz` in `app.New` are the
  only plain `http.Handler` values: probes repeat every few seconds and
  must not go through error logging.
- A list page reads its keyset cursor from the query string
  (`GET /notes?after=<cursor>`). A malformed cursor is
  `web.BadRequest`. A form that changes a row on that page sends the cursor
  back, so the redirect or the fragment shows the same page. See
  `internal/note/http.go` and database.md.

## Responses: Fragments and Redirects

Progressive enhancement — every flow must work without JavaScript:

- Check `web.IsHTMX(r)` (the `HX-Request` header) to pick the response kind.
- htmx request → `return web.RenderFragment(w, status, tmpl, "notes-section",
  data)` with the page's single swap-target section (`#profile-section`,
  `#notes-section`, `#chat-section`, `#triage-section`), whole, on
  success **and** on validation errors.
- Plain request, success → redirect (PRG): `http.Redirect(w, r, "/me?saved=1",
  http.StatusSeeOther); return nil`. Plain request, validation error →
  `return web.RenderPage(w, http.StatusUnprocessableEntity, tmpl, data)`
  with the submitted values kept.
- Cross-feature composition happens in the browser, never through Go
  imports: a page embeds another feature's section with
  `hx-get="/other" hx-trigger="load" hx-swap="outerHTML"` and a fallback
  link inside the placeholder. The `/me` dashboard embeds
  `#notes-section`, `#chat-section`, and `#triage-section` this way
  (`internal/user/templates/me.html`). The embedded feature's GET handler
  returns the fragment for htmx requests and the full page otherwise.
  `TestDashboardEmbedsFeatures` checks both paths for each card.
- Status codes: 200 render, 303 redirect after POST, 400 bad form data,
  401 bad login / htmx-unauthenticated, 404 unknown page or foreign
  resource, 422 validation, 500 unexpected (opaque).

## Streaming (SSE)

- `web.NewEventStream(w)` starts a Server-Sent Events response: status
  200, `Content-Type: text/event-stream`, `Cache-Control: no-store`,
  `X-Accel-Buffering: no`, and an immediate flush. It removes the write
  deadline of that response, so a stream can outlive
  `http.Server.WriteTimeout`. `ReadTimeout` does not stop a running
  handler, because the server clears the read deadline itself.
- A handler that waits for slow work before a normal page (the no-JS chat
  answer) calls `web.RemoveWriteDeadline(w)` first. When the deadline
  passes, the server drops the response, but the request context does
  not end, so the model call runs and costs money for nothing.
- `http.ResponseController` must reach the connection through every
  middleware. A middleware that wraps the `ResponseWriter` implements
  `Unwrap` (see `statusRecorder`). Otherwise the deadline change fails
  silently, and production cuts each answer at `WriteTimeout`.
  `TestChatOutlivesWriteTimeout` checks the full chain.
- `stream.Send(event, data)` writes one event and flushes it. Each line of
  data becomes one `data:` field. An event name with a line break is an
  error.
- Choose the stream with `web.WantsEventStream(r)` (`Accept:
  text/event-stream`). Without that header, answer with the full page, so
  the flow works without JavaScript (see `internal/chat/http.go`).
- Do every check that can need an error status before `NewEventStream`:
  auth, rate limit, form parse. After the stream starts, the status is
  200 and cannot change:
  - Send a `ValidationError` as an `error` event with its message.
  - For an unexpected error, send a generic `error` event and return the
    error. `web.E` sees that the response started and only logs the
    error: INFO when the client left, ERROR otherwise.
- The browser reads the stream with `fetch` and POST (`static/app.js`),
  not with `EventSource`. `EventSource` sends only GET, and the CSRF guard
  does not cover GET. A request that costs money or changes state stays a
  POST.
- The chat events are `delta` (a part of the text), `done` (the stop
  reason), and `error` (a safe message). The client adds text with
  `append` or `textContent`, never with `innerHTML`.
- Graceful shutdown waits 10 seconds. A longer stream ends with the
  shutdown, and the client shows that the answer stopped early.

## Errors

- Handlers return errors. `web.E(logger, h)` adapts a `web.HandlerE` to
  `http.Handler` and routes errors through `web.RespondError` — the only
  place that writes an error response.
- Expected terminal states: return `web.BadRequest` / `web.NotFound`
  (or a `*web.HTTPError` literal). Only `Msg` reaches the client; put the internal cause in `Err` — it goes only to the log.
- Unexpected errors: `return err`. RespondError logs it once (with
  `request_id`/`user_id` from the context) and answers an opaque 500.
  Never leak internals to clients; never log and return the same error.
- Content negotiation is centralized in RespondError: JSON clients
  (`Accept: application/json`) get
  `{"error":{"status":...,"message":...}}`; everyone else gets the shared
  error page (`internal/platform/web/error.html`).
- Validation errors are **not** terminal — they stay feature-local 422
  re-renders of the form with the message and the submitted values.

## Templates

- One template set per page: `var notesTmpl = web.MustPage(templatesFS,
  "templates/notes.html")` at package level (immutable — the documented
  exception to no-globals). MustPage panics at startup on bad templates.
- A page file defines `content` (the layout's block) plus its named
  fragments (`{{define "notes-section"}}`). The fragment wraps everything
  htmx swaps, including the error notice and the form.
- `web.RenderPage` / `web.RenderFragment` buffer the output and return an
  error. The handler returns that error, so a mid-render failure becomes a
  clean 500 through the same path as every other error.
- Forms carry both plain attributes (`method="post"`, `action`) and htmx
  attributes (`hx-post`, `hx-target`, `hx-swap="outerHTML"`) on the same
  `<form>` element. The plain attributes are the no-JS path — never omit
  them. Mutating buttons live inside a `<form>`, never as bare
  `hx-*`-only buttons.
- The htmx-config meta enables swaps on 422. It disables eval, script tags,
  inline indicator styles, and local history storage. It also removes
  `style` from `attributesToSettle`: a browser extension can add a `style`
  attribute to a field, and the CSP blocks the copy of that attribute
  after a swap. Keep these settings compatible with the CSP. `static/app.js` shows terminal request failures
  in the shared alert without discarding submitted form values.
- Static assets are embedded in `internal/platform/web/static/` and served
  at `/static/`. htmx is vendored there; never add a CDN script tag, an
  inline `<script>`, or an inline `style=` attribute — the CSP forbids
  them (see security.md).

## Auth Contract

- `auth` owns sessions: `Start` (login), `Identify`, `End` (logout),
  `LoadIdentity` (middleware, attaches Identity), `RequireIdentity` (guard).
- `RequireIdentity` answers 303 → `/login` for browsers and
  401 + `HX-Redirect: /login` for htmx requests. At registration it wraps
  outside the error adapter: `auth.RequireIdentity(web.E(logger, h))`.
- A database error in LoadIdentity returns the unified 500 and preserves
  the cookie. Only ErrNoSession means anonymous access.
- Session cookie outside development: `__Host-session_id` with Secure,
  HttpOnly, SameSite=Lax, Path=/, and no Domain. Development runs on plain
  HTTP, so it uses `session_id` without Secure. The DB stores only the
  SHA-256 hash of the token. A user keeps at most 10 sessions.
- CSRF is `http.CrossOriginProtection` (stdlib) in the middleware chain —
  no hidden form tokens. Do not remove it when adding routes.
- The login handlers live in `internal/user` (they need `user.Service`);
  `auth` must never import a feature package. There is no register page —
  `user.Service.Register` creates accounts; the sanctioned callers are
  `cmd/api adduser` and code you control.

## Server Lifecycle (`cmd/api`)

- `main` stays trivial; `run(ctx, args, getenv, stdout)` owns startup and
  takes its environment as arguments so tests can call it. `run` picks the
  log format: text in development, JSON otherwise. `serve` owns the HTTP
  servers.
- `parseCommand` is the single list of subcommands. It checks command
  names and argument counts before any configuration or database work.
  Subcommands: `migrate` applies migrations and exits; `adduser <email>
  <name>` creates an account with a generated password and prints it once
  to stdout; `worker` runs the background jobs of `app.NewWorker` until a
  signal stops it (see core.md "Background Jobs"). Every command applies
  the migrations first. An unknown subcommand is an error — never fall
  through to serving. Add a subcommand as one more case in
  `parseCommand`.
- `http.Server` always sets `ReadHeaderTimeout`, `ReadTimeout`,
  `WriteTimeout`, `IdleTimeout`. Graceful shutdown drains with its own
  fresh 10s context.
- Migrations run on startup (idempotent) under an advisory lock, so
  parallel replicas can boot safely.
- `DEBUG_ADDR` starts a second listener with pprof and expvar
  (`cmd/api/debug.go`). `Config.Validate` accepts only a loopback address.
  The listener opens before the main server starts, so a bad address stops
  the startup.

## Explicit Exceptions

- Note deletion is idempotent in the UI. Missing, malformed, and foreign
  IDs all cause the same successful refresh. The service still returns
  ErrNotFound; SQL still enforces ownership.
- Login uses a plain form and a full 401 page. Profile and note forms use
  htmx. Do not treat the terminal error page as a fragment for a form.
- CSRF rejections use the stdlib 403 response. Probes and missing static
  assets also use stdlib responses. The unified error contract
  applies to feature handlers and recoverable panics before a response.
- `POST /chat` and `POST /triage` without JavaScript render the answer
  page with 200, not a redirect. The features store nothing, so PRG has
  no resource to redirect to. A reload asks the model again; the browser
  warns before it sends the form again.
- auth.Service includes the cookie adapter. It is specific to browser
  sessions; feature business services remain independent of HTTP.
