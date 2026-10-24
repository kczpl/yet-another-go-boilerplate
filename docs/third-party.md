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

The application has two direct Go modules:

- `github.com/jackc/pgx/v5` (PostgreSQL driver and pool).
- `github.com/anthropics/anthropic-sdk-go` v1.84.0 (MIT), the official
  Go SDK of Anthropic. Only `internal/platform/llm/anthropic` imports it.

The other code that talks to a network service adds no module:

- `internal/platform/llm/chatcompletions` (Mistral, xAI, local servers)
  and `internal/platform/systemone/typesafe` use `net/http`.
- Bedrock uses the Anthropic SDK with a Bedrock API key and the
  `/anthropic` path of the Bedrock endpoint. It does not use the AWS SDK.
- The job queue (`internal/platform/jobs`) uses pgx. It needs no queue
  library and no cron library.

The SDK adds 16 indirect modules, for example OpenTelemetry,
`tidwall/gjson`, `invopop/jsonschema`, and `go.yaml.in/yaml/v4`. The
compiler links only the packages that the binary uses. `govulncheck` in
`just ci` checks all of them.

`anthropic.New` sets `option.WithoutEnvironmentDefaults()`. Without this
option, the SDK reads `ANTHROPIC_API_KEY`, `ANTHROPIC_BASE_URL`, and other
`ANTHROPIC_*` variables itself. In this project, only `config.Load` reads
the environment.

To remove the SDK, delete `internal/platform/llm/anthropic`, remove its
registration in `internal/app/models.go`, and run `go mod tidy`. To update
it, run `go get github.com/anthropics/anthropic-sdk-go@<version>`, run
`go mod tidy`, record the version here, and run `just ci`.
