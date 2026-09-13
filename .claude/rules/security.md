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
  cost, raise the cost constant and review the verification ceiling. The format
  (`pbkdf2-sha256$<iterations>$<salt>$<key>`) keeps old hashes valid.
- Compare hashes only with `hmac.Equal` (constant time).
- New passwords require at least 8 Unicode characters and at most 512
  bytes. Authenticate enforces the byte cap before the DB query or hash.
  Verification accepts only 16-byte salts, 32-byte keys, and iteration
  counts from 1 to 1,200,000. Review the ceiling when the cost changes.
- For a valid email and a password within the byte limit, `Authenticate`
  burns a full hash when the account does not exist. Invalid credentials
  return the same `ErrInvalidCredentials`. Infrastructure errors propagate
  to the error handler. Never reveal which credential was wrong. Keep the
  timing burn for unknown accounts.
- There is no register page. `user.Service.Register` is the only way to
  create an account; the sanctioned callers are `cmd/api adduser` and
  code you control. Never expose Register as an open endpoint.
- `adduser` generates the password with `crypto/rand` and prints it once
  to stdout. Never log a password; never accept one as a CLI argument
  (shell history).

## Input and Output

- Bind external values as SQL parameters. Feature repositories use
  `pgx.NamedArgs`; small fixed queries in platform and testdb may use `$1`.
  Constant SQL fragments, such as a column list, may be concatenated.
  Never interpolate user input into SQL. Testdb quotes generated database
  names and names read from the database catalog with `pgx.Identifier`.
- Every handler that reads form fields calls `r.ParseForm()` first and
  returns `web.BadRequest` on failure. This is where the body cap surfaces
  for form input. Handlers that use only the path need not parse a form.
- Reject invalid UTF-8 and NUL in text sent to PostgreSQL.
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
- Never log passwords, password hashes, raw tokens, session cookies, or
  SQL arguments. Hashes remain sensitive. Debug traces omit SQL arguments
  and driver error payloads. Operational errors are logged at the boundary.
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
- **A session cap per user.** Every login adds a session row.
  SQL rejects it after the 7-day TTL. TTL does not remove rows. Each login
  attempts to remove at most 100 expired rows. Use a scheduled cleanup if
  the application must remove old sessions without further logins.
- **Observability beyond logs**: a localhost-only pprof listener,
  metrics, tracing — pick what the deployment needs.
