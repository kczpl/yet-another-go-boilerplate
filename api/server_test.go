package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kczpl/yet-another-go-boilerplate/api"
	"github.com/kczpl/yet-another-go-boilerplate/config"
	"github.com/kczpl/yet-another-go-boilerplate/domains/notes"
	"github.com/kczpl/yet-another-go-boilerplate/testdb"
)

const testAPIKey = "test-api-key"

// newTestServer builds the full handler — middleware, auth, routes — backed
// by a private test database, wired exactly as main wires it.
func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	pool := testdb.New(t)
	cfg := config.Config{Env: "test", APIKey: testAPIKey}
	logger := slog.New(slog.DiscardHandler)
	return api.NewServer(logger, cfg, pool, notes.NewService(notes.NewRepo(pool)))
}

// do executes one authenticated request against the handler and decodes the
// JSON response body (nil body for empty responses).
func do(t *testing.T, srv http.Handler, method, target string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encoding request body: %v", err)
		}
	}
	req := httptest.NewRequest(method, target, &buf)
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var decoded map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("decoding response %q: %v", rec.Body.String(), err)
		}
	}
	return rec.Code, decoded
}

func TestHealthz(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil) // no auth needed
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body)
	}
}

func TestNotesRequireAuth(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/notes", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestCreateNote(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)

	status, body := do(t, srv, http.MethodPost, "/api/v1/notes",
		map[string]string{"title": "Gadget", "content": "shiny"})

	if status != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %v", status, http.StatusCreated, body)
	}
	data := body["data"].(map[string]any)
	if data["title"] != "Gadget" {
		t.Errorf("title = %v, want Gadget", data["title"])
	}
	if data["content"] != "shiny" {
		t.Errorf("content = %v, want shiny", data["content"])
	}
	if data["id"] == "" || data["id"] == nil {
		t.Error("id is empty")
	}
}

func TestCreateNoteValidation(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)

	t.Run("empty title", func(t *testing.T) {
		status, body := do(t, srv, http.MethodPost, "/api/v1/notes", map[string]string{"title": "  "})
		if status != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want %d; body: %v", status, http.StatusUnprocessableEntity, body)
		}
		problems := body["problems"].(map[string]any)
		if problems["title"] == nil {
			t.Errorf("problems = %v, want a title problem", problems)
		}
	})

	t.Run("malformed json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/notes", bytes.NewBufferString("{not json"))
		req.Header.Set("Authorization", "Bearer "+testAPIKey)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
		}
	})
}

func TestGetNote(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)

	_, created := do(t, srv, http.MethodPost, "/api/v1/notes", map[string]string{"title": "keep me"})
	id := created["data"].(map[string]any)["id"].(string)

	status, body := do(t, srv, http.MethodGet, "/api/v1/notes/"+id, nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %v", status, http.StatusOK, body)
	}
	if got := body["data"].(map[string]any)["title"]; got != "keep me" {
		t.Errorf("title = %v, want keep me", got)
	}
}

func TestGetNoteNotFound(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)

	t.Run("missing", func(t *testing.T) {
		status, _ := do(t, srv, http.MethodGet, "/api/v1/notes/0197c2c2-0000-7000-8000-000000000000", nil)
		if status != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", status, http.StatusNotFound)
		}
	})

	t.Run("invalid uuid", func(t *testing.T) {
		status, body := do(t, srv, http.MethodGet, "/api/v1/notes/not-a-uuid", nil)
		if status != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want %d; body: %v", status, http.StatusUnprocessableEntity, body)
		}
	})
}

func TestListNotesPaginates(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)

	for i := range 3 {
		status, body := do(t, srv, http.MethodPost, "/api/v1/notes",
			map[string]string{"title": fmt.Sprintf("note %d", i)})
		if status != http.StatusCreated {
			t.Fatalf("seeding note %d: status = %d; body: %v", i, status, body)
		}
	}

	status, body := do(t, srv, http.MethodGet, "/api/v1/notes?page=1&page_size=2", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %v", status, http.StatusOK, body)
	}
	data := body["data"].([]any)
	if len(data) != 2 {
		t.Errorf("len(data) = %d, want 2", len(data))
	}
	pagination := body["pagination"].(map[string]any)
	if pagination["total_count"] != float64(3) {
		t.Errorf("total_count = %v, want 3", pagination["total_count"])
	}
	// Newest first.
	if got := data[0].(map[string]any)["title"]; got != "note 2" {
		t.Errorf("first title = %v, want note 2", got)
	}

	t.Run("invalid page", func(t *testing.T) {
		status, _ := do(t, srv, http.MethodGet, "/api/v1/notes?page=zero", nil)
		if status != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want %d", status, http.StatusUnprocessableEntity)
		}
	})
}

func TestDeleteNote(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)

	_, created := do(t, srv, http.MethodPost, "/api/v1/notes", map[string]string{"title": "delete me"})
	id := created["data"].(map[string]any)["id"].(string)

	status, _ := do(t, srv, http.MethodDelete, "/api/v1/notes/"+id, nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d", status, http.StatusNoContent)
	}

	status, _ = do(t, srv, http.MethodGet, "/api/v1/notes/"+id, nil)
	if status != http.StatusNotFound {
		t.Fatalf("get after delete status = %d, want %d", status, http.StatusNotFound)
	}
}

func TestUnknownRouteIsJSON404(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t)

	status, body := do(t, srv, http.MethodGet, "/nope", nil)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", status, http.StatusNotFound)
	}
	if body["error"] != "not found" {
		t.Errorf("error = %v, want not found", body["error"])
	}
}
