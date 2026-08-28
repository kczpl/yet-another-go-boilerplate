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
adapted to server-rendered HTML + htmx.

## Composition & Routes

- `app.New(logger, cfg, pool)` builds the complete handler: repositories →
  services → feature routes → middleware. Tests construct the exact same
  handler as production. `cmd/api` adds only `http.Server` around it.
- Each feature exposes `Routes(mux, logger, deps...)` in its `http.go` and
  registers its own endpoints there. Auth wrapping happens at registration:
  `mux.Handle("GET /notes", auth.RequireIdentity(handleNotes(...)))`.
- Use method+wildcard mux patterns (`"DELETE /notes/{id}"`,
  `r.PathValue("id")`). `"GET /{$}"` matches exactly `/`. No router
  libraries.
- Middleware is `func(http.Handler) http.Handler` (or a method returning
  one). The chain lives only in `app.New`, outermost first: LogRequests →
  RecoverPanics → CrossOriginProtection → LoadIdentity → mux.
- LogRequests seeds the request id into the response header, the request
  context, and the log context (`logging.WithAttrs`); LoadIdentity adds
  `user_id` for logged-in requests. Every deeper `*Context` log call —
  handlers, render errors, pgx queries — repeats both automatically.

## Handlers

- Handlers are maker functions closing over their dependencies:
  `func handleNotes(logger *slog.Logger, svc *Service) http.Handler`.
- One handler per endpoint. Handlers only: read form values → call service →
  map the result to a response. **No business logic in handlers.**
- Read the user with `auth.IdentityFromContext(ctx)` — never from cookies or
  headers directly. Behind `RequireIdentity` the identity always exists.
- Page-data structs live next to their handlers and embed `web.Page`.
  Set `LoggedIn` explicitly per handler.

## Responses: Fragments and Redirects

Progressive enhancement — every flow must work without JavaScript:

- Check `web.IsHTMX(r)` (the `HX-Request` header) to pick the response kind.
- htmx request → `web.RenderFragment` of the page's single swap-target
  section (`#profile-section`, `#notes-section`), whole, on success **and**
  on validation errors.
- Plain request, success → redirect (PRG): `http.Redirect(w, r, "/me?saved=1",
  http.StatusSeeOther)`. Plain request, validation error →
  `web.RenderPage` with status 422 and the submitted values kept.
- Cross-feature composition happens in the browser, never through Go
  imports: a page embeds another feature's section with
  `hx-get="/other" hx-trigger="load" hx-swap="outerHTML"` and a fallback
  link inside the placeholder (see the `/me` dashboard embedding
  `#notes-section`). The embedded feature's GET handler returns the fragment
  for htmx requests and the full page otherwise.
- Status codes: 200 render, 303 redirect after POST, 401 bad login /
  htmx-unauthenticated, 422 validation, 500 unexpected (opaque).
- Unexpected errors go through `web.InternalError`: log the real error with
  the request id, return a generic 500. Never leak internals to clients.
- Terminal errors (400/401/404) are unified in `platform/web`: build them
  with `web.BadRequest/Unauthorized/NotFound`, return them from a
  `web.HandlerE` wrapped in `web.E(logger, h)`, or pass any error to
  `web.RespondError`. JSON clients (`Accept: application/json`) get
  `{"error":{"status":...,"message":...}}`; everyone else gets the shared
  error page (`internal/platform/web/error.html`). Only `Msg` reaches the
  client. Validation errors are not terminal — they stay feature-local 422
  re-renders with the form state.
- The `"/"` catch-all in `app.New` turns every unmatched request into the
  unified 404. Keep it registered last-resort only — real routes always use
  method+path patterns.

## Templates

- One template set per page: `var notesTmpl = web.MustPage(templatesFS,
  "templates/notes.html")` at package level (immutable — the documented
  exception to no-globals). MustPage panics at startup on bad templates.
- A page file defines `content` (the layout's block) plus its named
  fragments (`{{define "notes-section"}}`). The fragment wraps everything
  htmx swaps, including the error notice and the form.
- Forms carry both plain attributes (`method`, `action`) and htmx attributes
  (`hx-post`, `hx-target`, `hx-swap="outerHTML"`).
- The htmx-config meta in `layout.html` enables swapping on 422 — keep it.
- Static assets are embedded in `internal/platform/web/static/` and served
  at `/static/`. htmx is vendored there; never add a CDN script tag.

## Auth Contract

- `auth` owns sessions: `Start` (login), `Identify`, `End` (logout),
  `LoadIdentity` (middleware, attaches Identity), `RequireIdentity` (guard).
- `RequireIdentity` answers 303 → `/login` for browsers and
  401 + `HX-Redirect: /login` for htmx requests.
- Session cookie: `session_id`, HttpOnly, SameSite=Lax, Secure outside
  development. The DB stores only the SHA-256 hash of the token.
- CSRF is `http.CrossOriginProtection` (stdlib) in the middleware chain —
  no hidden form tokens. Do not remove it when adding routes.
- The login handlers live in `internal/user` (they need `user.Service`);
  `auth` must never import a feature package. There is no register page —
  `user.Service.Register` creates accounts, called only from code you
  control.

## Server Lifecycle (`cmd/api`)

- `main` stays trivial; `run(ctx, args, getenv, stdout)` owns startup and
  takes its environment as arguments so tests can call it.
- `http.Server` always sets `ReadHeaderTimeout`, `ReadTimeout`,
  `WriteTimeout`, `IdleTimeout`. Graceful shutdown drains with its own
  fresh 10s context.
- Migrations run on startup (idempotent); `go run ./cmd/api migrate`
  applies them and exits.
