package note_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kczpl/yet-another-go-boilerplate/internal/note"
	"github.com/kczpl/yet-another-go-boilerplate/internal/testdb"
)

// seedUser inserts a user row directly, because note tests must not depend
// on the user package.
func seedUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	email := fmt.Sprintf("note-%d@example.com", time.Now().UnixNano())
	rows, _ := pool.Query(t.Context(),
		"INSERT INTO users (email, name, password_hash) VALUES ($1, 'Note Test', 'x') RETURNING id",
		email)
	id, err := pgx.CollectExactlyOneRow(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("seeding user: %v", err)
	}
	return id
}

func TestAddAndList(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	svc := note.NewService(note.NewRepo(pool))
	owner := seedUser(t, pool)

	first, err := svc.Add(t.Context(), owner, "  Buy milk  ")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if first.Text != "Buy milk" {
		t.Errorf("Text = %q, want trimmed %q", first.Text, "Buy milk")
	}
	if first.UserID != owner {
		t.Errorf("UserID = %q, want %q", first.UserID, owner)
	}

	if _, err := svc.Add(t.Context(), owner, "Call the vet"); err != nil {
		t.Fatalf("Add second note: %v", err)
	}

	notes, err := svc.List(t.Context(), owner)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("List returned %d notes, want 2", len(notes))
	}
	// The order is newest first.
	if notes[0].Text != "Call the vet" || notes[1].Text != "Buy milk" {
		t.Errorf("order = [%s, %s], want [Call the vet, Buy milk]", notes[0].Text, notes[1].Text)
	}
}

func TestAddValidation(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	svc := note.NewService(note.NewRepo(pool))
	owner := seedUser(t, pool)

	tests := []struct {
		name string
		text string
	}{
		{"empty text", ""},
		{"blank text", "   "},
		{"too long text", strings.Repeat("x", 10001)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.Add(t.Context(), owner, tt.text)
			var vErr note.ValidationError
			if !errors.As(err, &vErr) {
				t.Errorf("Add = %v, want ValidationError", err)
			}
		})
	}
}

func TestDeleteEnforcesOwnership(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	svc := note.NewService(note.NewRepo(pool))
	alice := seedUser(t, pool)
	bob := seedUser(t, pool)

	n, err := svc.Add(t.Context(), alice, "Alice's note")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Bob cannot delete Alice's note. Bob also cannot tell that the note
	// exists.
	if err := svc.Delete(t.Context(), bob, n.ID); !errors.Is(err, note.ErrNotFound) {
		t.Errorf("Delete by non-owner = %v, want ErrNotFound", err)
	}

	if err := svc.Delete(t.Context(), alice, n.ID); err != nil {
		t.Fatalf("Delete by owner: %v", err)
	}
	if err := svc.Delete(t.Context(), alice, n.ID); !errors.Is(err, note.ErrNotFound) {
		t.Errorf("second Delete = %v, want ErrNotFound", err)
	}

	notes, err := svc.List(t.Context(), alice)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(notes) != 0 {
		t.Errorf("List after delete returned %d notes, want 0", len(notes))
	}
}
