---
paths:
  - "**/repo.go"
  - "postgres/**/*.go"
  - "postgres/migrations/*.sql"
---

# Database Rules

PostgreSQL 18 via `jackc/pgx/v5` (`pgxpool`). No ORM — plain SQL, explicitly
scanned. Migrations via `pressly/goose` (embedded, applied on startup).

## Repositories (`<domain>/repo.go`)

- One `Repo` struct per domain holding `*pgxpool.Pool`; one method per query;
  the SQL is a `const` right inside the method. No query builders.
- Writes use `RETURNING` and hand back the full row — never re-select.
- Translate driver errors at the repo boundary: `pgx.ErrNoRows` →
  the domain sentinel (`ErrNotFound`); wrap everything else with action
  context (`fmt.Errorf("inserting note: %w", err)`).
- Repos never begin/commit transactions. A multi-statement flow belongs to the
  service: `pgx.BeginFunc(ctx, s.repo.db, func(tx pgx.Tx) error { ... })` with
  repo methods that accept the query interface. Single statements are already
  atomic — don't wrap them.

## Schema Conventions

- Primary keys: `uuid PRIMARY KEY DEFAULT uuidv7()` (native in PG 18) —
  time-ordered, so `id` works as a deterministic sort tie-breaker.
- Timestamps: `timestamptz NOT NULL DEFAULT now()` for `created_at` and
  `updated_at`. **Every `UPDATE` statement must set `updated_at = now()`.**
- No PostgreSQL ENUMs — use `text` + `CHECK` constraints
  (`CHECK (status IN ('active', 'archived'))`).
- Naming: snake_case; indexes `<table>_<cols>_idx`, e.g. `notes_created_at_idx`.
- Lists: `count(*) OVER () AS total_count` window for pagination in one query,
  and a fully deterministic `ORDER BY` (`created_at DESC, id DESC`).

## Migrations

- Live in `postgres/migrations/`, numbered `NNNNN_description.sql`, goose
  format (`-- +goose Up` / `-- +goose Down`). Create with
  `just makemigration description`.
- Append-only: never edit or renumber a migration that may have been applied
  anywhere. Fix mistakes with a new migration.
- Applied automatically on app startup and when `testdb` builds its template —
  the embedded FS in `postgres/migrate.go` is the single source of schema
  truth. There is no separate "create tables for tests" path.
- Keep migrations pure SQL and zero-downtime-safe: add columns as nullable or
  with defaults, create indexes `CONCURRENTLY` outside a transaction
  (`-- +goose NO TRANSACTION`) once tables are large.
