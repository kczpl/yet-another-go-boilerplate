set dotenv-load

default:
  @just --list

# Start the full stack (api + postgres)
compose:
  docker compose up

# Run the API on the host (needs: docker compose up postgres -d)
app:
  go run .

# Format code
fmt:
  gofmt -l -w .

# Vet + verify formatting
lint:
  go vet ./...
  @unformatted="$(gofmt -l .)"; if [ -n "$unformatted" ]; then echo "gofmt needed:"; echo "$unformatted"; exit 1; fi

# Run tests against the real test database
test *flags="":
  docker compose up -d --wait postgres-test
  go test ./... {{ flags }}

ci: lint test

# Apply migrations (they also run automatically on app startup)
migrate:
  go run . migrate

# Create a new migration: just makemigration create_users_table
makemigration name:
  #!/usr/bin/env bash
  set -euo pipefail
  next=$(printf "%05d" $(( $(ls postgres/migrations/*.sql 2>/dev/null | wc -l) + 1 )))
  file="postgres/migrations/${next}_{{ name }}.sql"
  printf -- '-- +goose Up\n\n-- +goose Down\n' > "$file"
  echo "created $file"

# Check dependencies for known vulnerabilities
vulncheck:
  go run golang.org/x/vuln/cmd/govulncheck@latest ./...
