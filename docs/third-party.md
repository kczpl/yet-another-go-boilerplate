The application vendors htmx 2.0.11 without changes.

- Source: https://github.com/bigskysoftware/htmx/tree/v2.0.11
- File: `internal/platform/web/static/htmx.min.js`
- License: [Zero-Clause BSD](https://github.com/bigskysoftware/htmx/blob/v2.0.11/LICENSE)
- SHA-256: `d6fdc75f204e6bdefa99b69bf1e6d4ac69b8a364f77929f45c13476b4000f717`

To update htmx, copy the distribution file from a release tag, record its
version and checksum here, and verify forms and CSP in a browser.

The application vendors PgQue 0.2.2 without changes. A migration installs
it; the binary embeds the file with the other migrations.

- Source: https://github.com/NikolayS/pgque/blob/v0.2.2/sql/pgque.sql
- File: `migrations/20261026215434_install_pgque.sql`
- License: Apache-2.0, with code from PgQ under the ISC license. Copies of
  the upstream `LICENSE` and `NOTICE` are in `docs/third-party/pgque/`.
- SHA-256: `1f96dde1e57d0e97e6418a4e02b76fe2bf2e7688c5fbdc812160c57baba84030`

Never edit the vendored migration: the migrator applies each file once.
To update PgQue, copy `sql/pgque.sql` of the new release tag into a new
migration (`just makemigration upgrade_pgque`, then replace the content).
The file upgrades an install in place. Record the version and the
checksum here, copy the `LICENSE` and `NOTICE` of the tag, and run
`just test`.

The agent skills under `.agents/skills/` come from spf13/go-skills. Their
source and update instructions are in `.agents/skills/README.md`. They are
development guidance and do not enter the application binary.

## Go modules

The application has one direct Go module:

- `github.com/jackc/pgx/v5` (PostgreSQL driver and pool).

The code that talks to a network service adds no module:

- `internal/platform/llm/anthropic` (Anthropic and Bedrock),
  `internal/platform/llm/chatcompletions` (Mistral, xAI, local servers),
  and `internal/platform/systemone/typesafe` use `net/http`. They use no
  SDK of a vendor.
- Bedrock uses the Anthropic adapter with a Bedrock API key and the
  `/anthropic` path of the Bedrock endpoint. It does not use the AWS SDK.
- The job queue (`internal/platform/queue`) calls the SQL API of PgQue
  with pgx. It adds no Go module: PgQue is a SQL migration (see below),
  and the template does not use the `pgque-go` client.

pgx adds 5 indirect modules: `jackc/pgpassfile`, `jackc/pgservicefile`,
`jackc/puddle/v2`, `golang.org/x/sync`, and `golang.org/x/text`.
`govulncheck` in `just ci` checks all of them.

An earlier version used `github.com/anthropics/anthropic-sdk-go`. The SDK
added 16 indirect modules, for example OpenTelemetry and
`invopop/jsonschema`, so the adapter now calls the Messages API with
`net/http`. Do not add an SDK of a vendor without a discussion first (see
`.agents/rules/core.md`, "Add a Provider").
