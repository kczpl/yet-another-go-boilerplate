---
paths:
  - "internal/**/*.go"
  - "cmd/**/*.go"
  - "internal/**/templates/*.html"
  - "migrations/*"
  - "Dockerfile"
  - "docker-compose.yml"
---

# Security Rules

The standard library carries the security model: `crypto/pbkdf2`,
`crypto/rand`, `html/template`, `http.CrossOriginProtection`,
`http.MaxBytesHandler`. Keep the invariants below when you change or
extend the code. The last section lists what the template leaves out on
purpose.

## Sessions

- Create session tokens only with `crypto/rand` (32 bytes, base64url) —
  see `internal/auth/service.go`. Never derive a token from user data.
- The database stores only the SHA-256 hash of a token. Never store or
  log a raw token. A database leak must not let an attacker hijack a
  session.
- SQL enforces expiry (`expires_at > now()` in the session query,
  `internal/auth/postgres.go`). Do not check expiry in Go alone.
- The cookie flags are fixed: `HttpOnly`, `SameSite=Lax`, `Path=/`, and
  `Secure` outside development. Do not weaken them.
- Accept the session token only from the cookie — never from a query
  parameter, a header, or a form field.

## Passwords

- Hash with PBKDF2-HMAC-SHA256: 600k iterations (the OWASP minimum),
  16-byte salt, 32-byte key (`internal/user/password.go`). To raise the
  cost, raise the constant only — the self-describing format
  (`pbkdf2-sha256$<iterations>$<salt>$<key>`) keeps old hashes valid.
- Compare hashes only with `hmac.Equal` (constant time).
- Keep the length limits: minimum 8, maximum 512 characters. The maximum
  protects the hash function from megabyte inputs (CPU DoS).
- `Authenticate` burns a full hash on an unknown email and returns one
  generic `ErrInvalidCredentials` for every failure. Never tell the
  client which part was wrong. Never remove the timing burn.
- There is no register page. `user.Service.Register` is the only way to
  create an account; the sanctioned callers are `cmd/api adduser` and
  code you control. Never expose Register as an open endpoint.
- `adduser` generates the password with `crypto/rand` and prints it once
  to stdout. Never log a password; never accept one as a CLI argument
  (shell history).

## Input and Output

- All SQL goes through `pgx.NamedArgs`. Never build SQL from user input —
  no `fmt.Sprintf`, no string concatenation. (`testdb` is the one
  exception: test-only code with internally generated names.)
- Every form handler calls `r.ParseForm()` first and returns
  `web.BadRequest` on failure — this is also where the body cap surfaces.
- Validate and cap all user input in the service layer (lengths, allowed
  values). Database `CHECK` constraints are the backstop, not the first
  line of defense.
- `html/template` escapes all output. Never use `template.HTML`,
  `template.JS`, or `template.URL` with user data.
- Validate path-parameter ids with `isUUID` before a uuid cast; treat
  garbage as not found (see `internal/note/http.go`).

## Response Headers and Body Limits

- `web.SecureHeaders` (in the chain in `internal/app/app.go`) sets
  `Content-Security-Policy: default-src 'self'; img-src 'self' data:;
  frame-ancestors 'none'; form-action 'self'; base-uri 'self'`,
  `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, and
  `Referrer-Policy: same-origin`. Never remove or weaken it.
- The CSP works because every asset is self-hosted and embedded. Keep it
  that way: no CDN tags, no inline `<script>`, no inline `style=`
  attributes in templates. If you must extend the CSP, add the narrowest
  directive; never add `unsafe-inline` or `unsafe-eval`.
- `http.MaxBytesHandler` caps request bodies at `maxRequestBytes`
  (`internal/app/app.go`). Raise the constant consciously when a feature
  needs bigger uploads; never remove the cap.
- The regression tests for headers, the body cap, and CSRF live in
  `internal/app/app_test.go` — extend them, never delete them.

## Authorization

- Guard routes at registration in `Routes`:
  `auth.RequireIdentity(web.E(logger, handler))`. Wrap every new
  authenticated route there.
- Read the user only from `auth.IdentityFromContext` — never from a
  cookie, a header, or a form field inside a handler.
- Enforce ownership in SQL (`WHERE ... AND user_id = @user_id`). A
  foreign resource returns the same `ErrNotFound` as a missing one — the
  two must stay indistinguishable to the client.

## CSRF and State Changes

- `http.CrossOriginProtection` in `internal/app/app.go` is the CSRF
  guard. Never remove it from the middleware chain.
- Every state change uses POST (browser-facing routes are GET/POST only —
  see http.md). Never mutate state in a GET handler — the CSRF guard does
  not cover GET.

## Errors, Logs, Secrets

- Unexpected errors are returned from handlers and end in
  `web.RespondError`: it logs the real error and sends an opaque 500.
  Never put internal details in a response.
- Only `ValidationError` text, conflict sentinels, and `web.HTTPError.Msg`
  are safe to render to users. Keep internal detail in `HTTPError.Err` —
  it goes only to the log.
- Never log passwords, raw tokens, or session cookies. The debug SQL log
  is safe because Go hashes both before they reach SQL.
- Keep secrets out of the repository. `.env` is gitignored;
  configuration comes only from the environment (`config.Load`).

## Not Included — Add Before Production

The template omits these on purpose (they are deployment-specific). Add
them before you expose the app publicly:

- **Rate limiting on `POST /login`.** Without it the endpoint allows
  brute force, and each attempt costs the server a full PBKDF2 hash —
  a cheap CPU denial of service.
- **TLS in front of the app** (proxy or load balancer) plus HSTS there.
  With TLS in place, rename the cookie to use the `__Host-` prefix.
- **A session cap per user.** Today every login adds a session row until
  the 7-day TTL removes it.
- **Observability beyond logs**: a localhost-only pprof listener,
  metrics, tracing — pick what the deployment needs.
