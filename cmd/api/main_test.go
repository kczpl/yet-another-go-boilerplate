package main

import (
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kczpl/yet-another-go-boilerplate/internal/testdb"
	"github.com/kczpl/yet-another-go-boilerplate/internal/user"
)

// passwordPattern matches the printed temporary password: 18 random bytes
// encode to 24 base64url characters.
var passwordPattern = regexp.MustCompile(`temporary password \(shown once\): ([A-Za-z0-9_-]{24})`)

// testEnv builds a getenv that points run at the database of this test and
// keeps every other setting on its development default.
func testEnv(t *testing.T) (func(string) string, *pgxpool.Pool) {
	t.Helper()
	pool := testdb.New(t)
	url := pool.Config().ConnString()
	getenv := func(key string) string {
		if key == "DATABASE_URL" {
			return url
		}
		return ""
	}
	return getenv, pool
}

func TestRunAddUser(t *testing.T) {
	t.Parallel()
	getenv, pool := testEnv(t)

	var out strings.Builder
	err := run(t.Context(), []string{"api", "adduser", "ada@example.com", "Ada Lovelace"}, getenv, &out)
	if err != nil {
		t.Fatalf("run adduser: %v", err)
	}

	match := passwordPattern.FindStringSubmatch(out.String())
	if match == nil {
		t.Fatalf("no temporary password in output:\n%s", out.String())
	}

	// The printed password must open the account through the real login
	// logic.
	users := user.NewService(user.NewRepo(pool))
	u, err := users.Authenticate(t.Context(), "ada@example.com", match[1])
	if err != nil {
		t.Fatalf("authenticating with the printed password: %v", err)
	}
	if u.Name != "Ada Lovelace" {
		t.Fatalf("name = %q, want %q", u.Name, "Ada Lovelace")
	}
}

func TestRunRejectsBadArgs(t *testing.T) {
	t.Parallel()
	getenv, _ := testEnv(t)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"unknown command", []string{"api", "frobnicate"}, "unknown command"},
		{"adduser without arguments", []string{"api", "adduser"}, "usage:"},
		{"adduser with a bad email", []string{"api", "adduser", "not-an-email", "Ada"}, "valid email"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(t.Context(), tt.args, getenv, io.Discard)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}
