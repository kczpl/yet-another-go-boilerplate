set dotenv-load
set positional-arguments

staticcheck_version := "v0.8.1"
gocognit_version := "v1.2.1"
govulncheck_version := "v1.8.0"

default:
  @just --list

# Start the API and PostgreSQL.
compose:
  docker compose up --build api postgres

# Run the API on the host. Start PostgreSQL first.
app:
  go run ./cmd/api

# Build the production binary.
build:
  CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/api ./cmd/api

# Format the code.
fmt:
  gofmt -l -w .

# Run the analyzers and check the format.
lint:
  go vet ./...
  go run honnef.co/go/tools/cmd/staticcheck@{{staticcheck_version}} ./...
  @unformatted="$(gofmt -l .)"; if [ -n "$unformatted" ]; then echo "gofmt needed:"; echo "$unformatted"; exit 1; fi

# Compile all packages and tests without a database.
types:
  go test -race -run '^$' ./...

# Limit cognitive complexity to 10 in application code and test support.
complexity:
  go run github.com/uudashr/gocognit/cmd/gocognit@{{gocognit_version}} -test=false -over 10 cmd internal migrations

# Run the tests against PostgreSQL. Always run them without the result cache.
test *flags:
  docker compose up -d --wait postgres-test
  go test -race -count=1 ./... "$@"

ci: lint types complexity test vulncheck

# Apply the migrations. App startup also applies them.
migrate:
  go run ./cmd/api migrate

# Create an account and print its generated password.
adduser email name:
  go run ./cmd/api adduser "$1" "$2"

# Create a migration file. Use a name such as create_toys_table.
makemigration name:
  #!/usr/bin/env bash
  set -euo pipefail
  if [[ ! "$1" =~ ^[a-z][a-z0-9_]*$ ]]; then
    echo "Use lowercase letters, digits, and underscores for the migration name." >&2
    exit 1
  fi
  file="migrations/$(date -u +%Y%m%d%H%M%S)_$1.sql"
  set -o noclobber
  printf -- '-- Write SQL that moves the schema forward.\n-- Never edit an applied file. Add a new file instead.\n\n' > "$file"
  echo "created $file"

# Check for known vulnerabilities with a pinned scanner and the live advisory database.
vulncheck:
  go run golang.org/x/vuln/cmd/govulncheck@{{govulncheck_version}} ./...
