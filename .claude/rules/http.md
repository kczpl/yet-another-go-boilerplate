---
paths:
  - "api/**/*.go"
  - "auth/**/*.go"
  - "main.go"
---

# HTTP Layer Rules

The `api/` package follows Mat Ryer's patterns
(https://grafana.com/blog/how-i-write-http-services-in-go-after-13-years/).

## Server & Routes

- `api.NewServer(...)` takes **all** dependencies as explicit arguments and
  returns `http.Handler`. It builds the mux, calls `addRoutes`, and wraps the
  result in middleware. Tests construct the exact same server as `main`.
- `api/routes.go` is the single map of the whole API surface. Every route is
  registered there and nowhere else; auth wrapping happens there too. Use
  method+wildcard mux patterns — `mux.Handle("GET /api/v1/notes/{id}", ...)` —
  and read path parameters with `r.PathValue("id")`. No router libraries.
- The fallback route `mux.Handle("/", ...)` returns a JSON 404.

## Handlers

- Handlers are maker functions closing over their dependencies:
  `func handleNotesCreate(logger *slog.Logger, service *notes.Service) http.Handler`.
- One handler per endpoint. Handlers only: decode → validate → call service →
  map domain result/error to HTTP. **No business logic in handlers.**
- Request/response types live in `api/` next to their handlers and are mapped
  to/from domain types explicitly (`toNoteResponse`). Never JSON-encode domain
  structs directly — the wire format is owned by the API layer.

## Validation

- Request types implement `Validator`:
  `Valid(ctx context.Context) map[string]string` (field → problem).
- Decode with `decodeValid[T]`; on problems respond
  `422 {"error": "validation failed", "problems": {...}}`.
- Malformed JSON → `400 {"error": "invalid json body"}`.
- Bodies larger than 1 MiB → `413 {"error": "request body too large"}`
  (`http.MaxBytesReader` in `decode`).
- Path/query parameters are validated in the handler (e.g. `uuid.Parse`) and
  also answer 422 with a problems map.

## Responses

- Envelopes are defined once in `api/respond.go`:
  success `{"data": ...}`, lists `{"data": [...], "pagination": {...}}`,
  errors `{"error": "...", "problems": {...}?}`.
- Status codes: 200 read/update, 201 create, 204 delete, 400 bad body,
  401 auth, 404 missing, 422 validation, 500 unexpected.
- Unexpected errors go through `respondInternalError`: log the real error with
  the request context, return an opaque 500. Never leak internals to clients.

## Middleware & Auth

- Middleware is `func(http.Handler) http.Handler`; middleware with
  dependencies is a function returning that. Composed only in `NewServer`
  (cross-cutting) or `addRoutes` (per-route, e.g. auth).
- `logRequests` assigns `X-Request-ID` (propagate or generate) and stores it
  on the request context (`RequestIDFromContext`); error/panic logs include it.
- Auth lives in `auth/` and **only** there: it verifies credentials and stores
  `auth.Identity` in the context. Handlers/services read
  `auth.FromContext(ctx)` — they never touch the Authorization header.
- The shipped `RequireAPIKey` is a placeholder for real auth (JWT/OIDC): swap
  the middleware, keep the Identity contract, and nothing else changes.
- Auth must fail closed: an empty configured key rejects every request.

## Server Lifecycle (`main.go`)

- `main` stays trivial; `run(ctx, args, getenv, stdout)` owns startup and takes
  its environment as arguments so tests can call it.
- `http.Server` always sets `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`,
  `IdleTimeout`. Graceful shutdown drains with its own fresh 10s context.
- Migrations run on startup (idempotent); `go run . migrate` applies them and
  exits.
