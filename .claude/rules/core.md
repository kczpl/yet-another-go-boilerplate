---
paths:
  - "**/*.go"
---

# Go Core Rules

Style and structure rules for this codebase. The vendored `go` skill
(`.claude/skills/go/SKILL.md`) is the authoritative reference for idiomatic Go;
these rules pin down how this template applies it.

## Package Layout: Domains, Not Layers

```
main.go       wiring only: config → pool → services → server (the only DI point)
config/       env-driven Config
api/          HTTP layer: server, routes, middleware, JSON, validation, handlers
auth/         authentication middleware + Identity-in-context
domains/      business domains, one package per domain (domains/notes is the example)
postgres/     connection pool + embedded migrations
testdb/       per-test isolated databases (cloned from a migrated template)
```

- A new domain = a new package under `domains/`. Copy `domains/notes/` and
  adapt; keep the `<domain>.go` (types + errors) / `service.go` / `repo.go`
  file split. Everything outside `domains/` is infrastructure and should stay
  as-is — the root gains no new packages as the app grows.
- Layers live as **files inside a domain package**, never as layer-named
  packages (`services/`, `repositories/`, `models/` are banned). `domains/` is
  the only grouping directory — it groups by what code *is* (a business
  domain), not by layer, and stays one level deep (no `domains/a/b/`).
- Never create `utils/`, `helpers/`, `common/`, `pkg/`, or `internal/`.
- Domain packages must not import `api/` or each other sideways. `api/` imports
  domains; `main.go` wires everything. Import cycles mean wrong boundaries.

## Dependency Injection

- Explicit constructor injection only: `NewRepo(pool)`, `NewService(repo)`,
  `api.NewServer(logger, cfg, pool, services...)`. Adding a dependency means
  adding a parameter — the compiler then finds every wiring point.
- No DI frameworks, no package-level singletons, no `init()`, no globals.
- Depend on concrete types until a second implementation actually exists;
  when you do need an interface, define it in the consuming package.

## Errors

- Wrap with action context: `fmt.Errorf("creating note: %w", err)`.
- Domain packages define sentinel errors (`notes.ErrNotFound`); repos translate
  driver errors (`pgx.ErrNoRows`) into them; handlers map them to HTTP status.
- Never log and return the same error — log once, at the HTTP boundary
  (`respondInternalError`).
- Never `panic` in request paths; the recover middleware is a last resort, not
  a control-flow mechanism.

## Logging

- `log/slog` only, passed as `*slog.Logger` constructor/function argument.
  `main.go` decides the handler (text in development, JSON otherwise).
- Levels: `Debug` = high-volume internals, `Info` = lifecycle + request log,
  `Warn` = recoverable oddities, `Error` = needs attention.

## Keep It Denormalized

This template optimizes for readability and agentic editing, not DRY:

- Prefer explicit, repeated code over clever abstraction. Two similar handlers
  are better than one generic one.
- No generic repositories, generic services, or base structs.
- Every SQL query is written out in the repo that uses it.

## Use Current Go (1.25)

- `any`, `for i := range n`, `min`/`max`, `cmp.Or` for defaults, `slices`/`maps`
  packages, `math/rand/v2`, `errors.Join`, `omitzero` JSON tags where useful.
- Loop variables are per-iteration — never write `x := x` captures.
- `gofmt -l -w .` before finishing any task; `go vet ./...` must be clean.
