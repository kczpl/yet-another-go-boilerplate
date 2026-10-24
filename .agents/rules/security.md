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
- The cookie flags are fixed. Outside development the cookie is
  `__Host-session_id` with `Secure`, `HttpOnly`, `SameSite=Lax`, `Path=/`,
  and no `Domain`. The browser then refuses the cookie over plain HTTP and
  refuses a copy that a subdomain sets. Development runs on plain HTTP, so
  it uses `session_id` without `Secure`. Do not weaken these flags.
- A user keeps at most `maxSessionsPerUser` (10) sessions. `auth.Repo.Insert`
  removes the oldest sessions in the same transaction as the insert, so
  logins cannot fill the table.
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
  `template.JS`, or `template.URL` with user data. Model output is user
  data.
- Streamed model text reaches the page only as text: `append(string)` or
  `textContent` in `static/app.js`. Never use `innerHTML` for it.
- Validate path-parameter ids with `isUUID` before a uuid cast; treat
  garbage as not found (see `internal/note/http.go`).

## Response Headers and Body Limits

- `web.SecureHeaders` (in the chain in `internal/app/app.go`) sets
  `Content-Security-Policy: default-src 'self'; img-src 'self' data:;
  frame-ancestors 'none'; form-action 'self'; base-uri 'self'`,
  `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, and
  `Referrer-Policy: same-origin`. Outside development it also sets
  `Strict-Transport-Security: max-age=31536000`. HSTS has no
  `includeSubDomains`, because the template does not know the other
  hosts of the domain. Never remove or weaken these headers.
- The CSP works because every asset is self-hosted and embedded. Keep it
  that way: no CDN tags, no inline `<script>`, no inline `style=`
  attributes in templates. If you must extend the CSP, add the narrowest
  directive; never add `unsafe-inline` or `unsafe-eval`.
- `http.MaxBytesHandler` caps request bodies at `maxRequestBytes`
  (`internal/app/app.go`). Raise the constant consciously when a feature
  needs bigger uploads; never remove the cap.
- The regression tests for headers, the body cap, CSRF, and the unified
  errors live in `internal/app/security_test.go`. The cookie flag and
  login rate limit tests live in `internal/app/auth_test.go`. Extend these
  tests; never delete them.

## Rate Limits

- `POST /login` has a rate limit (`web.RateLimiter`, set up in
  `internal/app/app.go`). Each client gets a burst of 10 attempts, then
  one attempt per 20 seconds. A limited request gets 429 with
  `Retry-After` and does not compute a hash. This stops brute force and
  the CPU cost of PBKDF2 floods.
- The key is the client IP address. For IPv6 the key is the /64 prefix,
  because one client usually controls a full /64 network.
- Behind a reverse proxy, set `CLIENT_IP_HEADER` (for example
  `X-Forwarded-For`). The limiter uses the last address in that header,
  which the proxy adds. Do not set it without a proxy: the client then
  sends the header and chooses its own key.
- The state is in process memory. Each replica counts on its own, and a
  restart clears the counters. The table holds at most 100,000 keys. When
  it is full, new clients get 429 until idle keys expire (fail closed).
- There is no limit per account. Such a limit lets any person lock out any
  account. Add one only with a plan for that risk.
- `POST /chat` has its own limiter: a burst of 5 questions, then one
  question per 10 seconds. Each question is a paid model call. The limit
  runs before the stream starts, so a limited client gets a real 429. The
  service also caps the question length and `MaxTokens`.
- `POST /triage` has its own limiter: a burst of 10 messages, then one
  message per 5 seconds. A System One call is short, so the limit is
  higher than the chat limit. The service caps the message length.
- Put a new limiter on each endpoint that is expensive or that guesses a
  secret. Construct it in `app.New` and pass it to `Routes`.

## Debug Listener

- `DEBUG_ADDR` starts pprof and expvar on a second listener
  (`cmd/api/debug.go`). `Config.Validate` accepts only a loopback address
  (`localhost` or a loopback IP such as `127.0.0.1` or `[::1]`). The profiles show memory contents
  and internal state, so never expose this listener through the proxy or a
  published container port. Reach it with `kubectl port-forward` or SSH.

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
- A model API key (`ANTHROPIC_API_KEY`, `TYPESAFE_API_KEY`) goes from
  `Config` only to the constructor of its provider. A provider error must
  not contain the key or the request text: the TypeSafe adapter returns
  only the status code. Never log keys. Do not log prompts, states, or
  answers: they can hold private text.

## Not Included — Add Before Production

The template omits these on purpose (they are deployment-specific). Add
them before you expose the app publicly:

- **TLS termination** at a proxy or a load balancer. The app sends HSTS
  and a `Secure` cookie outside development, so it works only over HTTPS.
  Set `CLIENT_IP_HEADER` for that proxy.
- **Limits for many replicas.** The login limit is per process. With many
  replicas, or for limits per account, move the counters to a shared store
  (PostgreSQL or the proxy).
- **Limits on other endpoints.** Only `POST /login`, `POST /chat`, and
  `POST /triage` have a limit in the app. Add a general request limit at
  the proxy.
- **A model budget per user.** The chat and triage limits are per IP and
  per replica. Before production, add a quota per account (calls or
  tokens per day), and set a spend limit at each provider. Set
  `LLM_MODEL` and `SYSTEMONE_MODEL` to real models, or remove
  `internal/chat` and `internal/triage`.
- **Scheduled session cleanup.** SQL rejects a session after the 7-day
  TTL, but the TTL does not remove rows. Each login removes at most 100
  expired rows. Use a scheduled job if the table must stay small without
  logins.
- **Metrics and tracing.** The app has structured logs, probes, and a
  loopback pprof/expvar listener. Add metrics and tracing as the
  deployment needs them.
- **Backups, secret management, and monitoring of the database.**
- **Separate database roles.** The app applies migrations at startup, so
  its database role needs DDL rights. To run the app with a role without
  DDL rights, move the migrations to a separate deploy step.
