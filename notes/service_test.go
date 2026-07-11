package notes_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"

	"github.com/kczpl/yet-another-go-boilerplate/notes"
	"github.com/kczpl/yet-another-go-boilerplate/testdb"
)

// Service tests run against a real, private database and exercise the repo
// underneath — no mocks.
func newService(t *testing.T) *notes.Service {
	t.Helper()
	pool := testdb.New(t)
	return notes.NewService(notes.NewRepo(pool))
}

func TestCreateAndGet(t *testing.T) {
	t.Parallel()
	service := newService(t)
	ctx := t.Context()

	created, err := service.Create(ctx, notes.CreateParams{Title: "  hello  ", Content: "world"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Title != "hello" {
		t.Errorf("Title = %q, want trimmed %q", created.Title, "hello")
	}
	if created.ID == uuid.Nil {
		t.Error("ID is zero")
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Error("timestamps are zero")
	}

	got, err := service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if diff := cmp.Diff(created, got); diff != "" {
		t.Errorf("Get mismatch (-created +got):\n%s", diff)
	}
}

func TestGetNotFound(t *testing.T) {
	t.Parallel()
	service := newService(t)

	_, err := service.Get(t.Context(), uuid.New())
	if !errors.Is(err, notes.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestListPaginatesNewestFirst(t *testing.T) {
	t.Parallel()
	service := newService(t)
	ctx := t.Context()

	for i := range 5 {
		if _, err := service.Create(ctx, notes.CreateParams{Title: fmt.Sprintf("note %d", i)}); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}

	page, total, err := service.List(ctx, notes.ListParams{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	if len(page) != 2 {
		t.Fatalf("len(page) = %d, want 2", len(page))
	}
	if page[0].Title != "note 4" || page[1].Title != "note 3" {
		t.Errorf("page = [%q, %q], want newest first [note 4, note 3]", page[0].Title, page[1].Title)
	}

	rest, _, err := service.List(ctx, notes.ListParams{Limit: 10, Offset: 4})
	if err != nil {
		t.Fatalf("List rest: %v", err)
	}
	if len(rest) != 1 || rest[0].Title != "note 0" {
		t.Errorf("last page = %v, want just note 0", rest)
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()
	service := newService(t)
	ctx := t.Context()

	created, err := service.Create(ctx, notes.CreateParams{Title: "doomed"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := service.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := service.Get(ctx, created.ID); !errors.Is(err, notes.ErrNotFound) {
		t.Fatalf("Get after Delete: err = %v, want ErrNotFound", err)
	}
	if err := service.Delete(ctx, created.ID); !errors.Is(err, notes.ErrNotFound) {
		t.Fatalf("second Delete: err = %v, want ErrNotFound", err)
	}
}
