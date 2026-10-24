package note_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kczpl/yet-another-go-boilerplate/internal/note"
	"github.com/kczpl/yet-another-go-boilerplate/internal/testdb"
)

// seedUser inserts a user row directly, because note tests must not depend
// on the user package.
func seedUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	// A random email lets one test seed many users.
	var id string
	err := pool.QueryRow(t.Context(), `
		INSERT INTO users (email, name, password_hash)
		VALUES (gen_random_uuid()::text || '@example.com', 'Note Test', 'x')
		RETURNING id`).Scan(&id)
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

	notes, next, err := svc.List(t.Context(), owner, note.Cursor{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !next.IsZero() {
		t.Errorf("next = %+v, want zero: one page holds all notes", next)
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
		{"nul text", "note\x00"},
		{"invalid UTF-8 text", "note\xff"},
		{"too long text", strings.Repeat("x", 10001)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.Add(t.Context(), owner, tt.text)
			if _, ok := errors.AsType[note.ValidationError](err); !ok {
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

	notes, _, err := svc.List(t.Context(), alice, note.Cursor{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(notes) != 0 {
		t.Errorf("List after delete returned %d notes, want 0", len(notes))
	}
}

// pageSize in service.go is 20.
const pageSize = 20

func TestListPagesByKeyset(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	svc := note.NewService(note.NewRepo(pool))
	owner := seedUser(t, pool)
	other := seedUser(t, pool)
	// All rows get the same created_at, so only the id orders them. This
	// is the hard case for a keyset cursor.
	_, err := pool.Exec(t.Context(), `
		INSERT INTO notes (user_id, text, created_at)
		SELECT $1, n::text, '2026-01-01T12:00:00Z' FROM generate_series(1, 45) n`, owner)
	if err != nil {
		t.Fatalf("seeding notes: %v", err)
	}
	if _, err := svc.Add(t.Context(), other, "private note"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	var all []note.Note
	var sizes []int
	after := note.Cursor{}
	for {
		page, next, err := svc.List(t.Context(), owner, after)
		if err != nil {
			t.Fatalf("List after %+v: %v", after, err)
		}
		all = append(all, page...)
		sizes = append(sizes, len(page))
		if next.IsZero() {
			break
		}
		after = next
	}

	if !slices.Equal(sizes, []int{pageSize, pageSize, 5}) {
		t.Fatalf("page sizes = %v, want [%d %d 5]", sizes, pageSize, pageSize)
	}
	for i, n := range all {
		if n.UserID != owner {
			t.Fatal("a page contains a note of another user")
		}
		if i > 0 && all[i-1].ID <= n.ID {
			t.Fatalf("note %d is not older than note %d: a row is missing, repeated, or out of order", i, i-1)
		}
	}
}

func TestListLastPageHasNoCursor(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	svc := note.NewService(note.NewRepo(pool))
	owner := seedUser(t, pool)
	// Exactly one full page: the extra row must not produce an empty
	// second page.
	_, err := pool.Exec(t.Context(), `INSERT INTO notes (user_id, text) SELECT $1, n::text FROM generate_series(1, $2::int) n`, owner, pageSize)
	if err != nil {
		t.Fatalf("seeding notes: %v", err)
	}
	notes, next, err := svc.List(t.Context(), owner, note.Cursor{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(notes) != pageSize || !next.IsZero() {
		t.Fatalf("List = %d notes, next %+v; want %d notes and no next page", len(notes), next, pageSize)
	}
}
