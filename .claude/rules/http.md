---
paths:
  - "internal/**/http.go"
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

- `app.New(logger, cfg, pool)` builds the complete handler: repositories →
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
  (`note` → `/notes`). Only `app.New` registers cross-feature paths:
  `/healthz`, `/static/`, and the `"/"` catch-all that turns every
  unmatched request into the unified 404. The catch-all sits behind the
  CSRF guard, so a cross-origin POST to an unknown path gets 403, not
  404 — tests must expect that.
- The middleware chain lives only in `app.New`. Keep this order (outermost
  first): `LogRequests` → `RecoverPanics` → `SecureHeaders` →
  `MaxBytesHandler` → outer mux (`/healthz`, `/static/`) →
  `CrossOriginProtection` → `LoadIdentity` → pages mux.
- `/healthz` and `/static/` sit outside sessions and CSRF on purpose: an
  asset or probe request must not cost a database query. `web.Static()`
  sets `Cache-Control` — assets are embedded in the binary, so a deploy is
  the cache invalidation.
- `LogRequests` seeds the request id into the response header, the request
  context, and the log context (`logging.WithAttrs`); `LoadIdentity` adds
  `user_id` for logged-in requests. Every deeper `*Context` log call —
  handlers, RespondError, pgx queries — repeats both automatically.

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
- `handleHealthz` in `app.New` is the one plain `http.Handler`: probes
  repeat every few seconds and must not go through error logging.

## Responses: Fragments and Redirects

Progressive enhancement — every flow must work without JavaScript:

- Check `web.IsHTMX(r)` (the `HX-Request` header) to pick the response kind.
- htmx request → `return web.RenderFragment(w, status, tmpl, "notes-section",
  data)` with the page's single swap-target section (`#profile-section`,
  `#notes-section`), whole, on success **and** on validation errors.
- Plain request, success → redirect (PRG): `http.Redirect(w, r, "/me?saved=1",
  http.StatusSeeOther); return nil`. Plain request, validation error →
  `return web.RenderPage(w, http.StatusUnprocessableEntity, tmpl, data)`
  with the submitted values kept.
- Cross-feature composition happens in the browser, never through Go
  imports: a page embeds another feature's section with
  `hx-get="/other" hx-trigger="load" hx-swap="outerHTML"` and a fallback
  link inside the placeholder (see the `/me` dashboard embedding
  `#notes-section`). The embedded feature's GET handler returns the fragment
  for htmx requests and the full page otherwise.
- Status codes: 200 render, 303 redirect after POST, 400 bad form data,
  401 bad login / htmx-unauthenticated, 404 unknown page or foreign
  resource, 422 validation, 500 unexpected (opaque).

## Errors

- Handlers return errors. `web.E(logger, h)` adapts a `web.HandlerE` to
  `http.Handler` and routes errors through `web.RespondError` — the only
  place that writes an error response.
- Expected terminal states: return `web.BadRequest` / `web.Unauthorized` /
  `web.NotFound` (or a `*web.HTTPError` literal). Only `Msg` reaches the
  client; put the internal cause in `Err` — it goes only to the log.
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
- The htmx-config meta in `layout.html` enables swapping on 422 — keep it.
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
- Session cookie: `session_id`, HttpOnly, SameSite=Lax, Secure outside
  development. The DB stores only the SHA-256 hash of the token.
- CSRF is `http.CrossOriginProtection` (stdlib) in the middleware chain —
  no hidden form tokens. Do not remove it when adding routes.
- The login handlers live in `internal/user` (they need `user.Service`);
  `auth` must never import a feature package. There is no register page —
  `user.Service.Register` creates accounts; the sanctioned callers are
  `cmd/api adduser` and code you control.

## Server Lifecycle (`cmd/api`)

- `main` stays trivial; `run(ctx, args, getenv, stdout)` owns startup and
  takes its environment as arguments so tests can call it.
- Subcommands: `migrate` applies migrations and exits; `adduser <email>
  <name>` creates an account with a generated password and prints it once
  to stdout. An unknown subcommand is an error — never fall through to
  serving.
- `http.Server` always sets `ReadHeaderTimeout`, `ReadTimeout`,
  `WriteTimeout`, `IdleTimeout`. Graceful shutdown drains with its own
  fresh 10s context.
- Migrations run on startup (idempotent) under an advisory lock, so
  parallel replicas can boot safely.
