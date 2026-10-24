The application vendors htmx 2.0.11 without changes.

- Source: https://github.com/bigskysoftware/htmx/tree/v2.0.11
- File: `internal/platform/web/static/htmx.min.js`
- License: [Zero-Clause BSD](https://github.com/bigskysoftware/htmx/blob/v2.0.11/LICENSE)
- SHA-256: `d6fdc75f204e6bdefa99b69bf1e6d4ac69b8a364f77929f45c13476b4000f717`

To update htmx, copy the distribution file from a release tag, record its
version and checksum here, and verify forms and CSP in a browser.

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
- The job queue (`internal/platform/jobs`) uses pgx. It needs no queue
  library and no cron library.

pgx adds 5 indirect modules: `jackc/pgpassfile`, `jackc/pgservicefile`,
`jackc/puddle/v2`, `golang.org/x/sync`, and `golang.org/x/text`.
`govulncheck` in `just ci` checks all of them.

An earlier version used `github.com/anthropics/anthropic-sdk-go`. The SDK
added 16 indirect modules, for example OpenTelemetry and
`invopop/jsonschema`, so the adapter now calls the Messages API with
`net/http`. Do not add an SDK of a vendor without a discussion first (see
`.agents/rules/core.md`, "Add a Provider").
