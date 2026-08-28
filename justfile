set dotenv-load

default:
  @just --list

# Start the full stack (api + postgres)
compose:
  docker compose up

# Run the API on the host (start postgres first: docker compose up postgres -d)
app:
  go run ./cmd/api

# Build the production binary into bin/api
build:
  CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/api ./cmd/api

# Format the code
fmt:
  gofmt -l -w .

# Run go vet and check the format
lint:
  go vet ./...
  @unformatted="$(gofmt -l .)"; if [ -n "$unformatted" ]; then echo "gofmt needed:"; echo "$unformatted"; exit 1; fi

# Run the tests against the real test database
test *flags="":
  docker compose up -d --wait postgres-test
  go test ./... {{ flags }}

ci: lint test

# Apply the migrations (they also run automatically on app startup)
migrate:
  go run ./cmd/api migrate

# Create an account: just adduser ada@example.com 'Ada Lovelace'
adduser email name:
  go run ./cmd/api adduser {{ email }} '{{ name }}'

# Create a new migration: just makemigration create_users_table
makemigration name:
  #!/usr/bin/env bash
  set -euo pipefail
  next=$(printf "%04d" $(( $(ls migrations/*.sql 2>/dev/null | wc -l) + 1 )))
  file="migrations/${next}_{{ name }}.sql"
  printf -- '-- Write forward-only SQL. Migrations are append-only.\n-- Never edit an applied file. Add a new file instead.\n\n' > "$file"
  echo "created $file"

# Check the dependencies for known vulnerabilities
vulncheck:
  go run golang.org/x/vuln/cmd/govulncheck@latest ./...
