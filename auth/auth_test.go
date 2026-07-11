package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kczpl/yet-another-go-boilerplate/auth"
)

func TestRequireAPIKey(t *testing.T) {
	t.Parallel()

	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name       string
		apiKey     string
		authHeader string
		wantStatus int
	}{
		{"valid key", "secret", "Bearer secret", http.StatusOK},
		{"wrong key", "secret", "Bearer nope", http.StatusUnauthorized},
		{"missing header", "secret", "", http.StatusUnauthorized},
		{"not a bearer token", "secret", "Basic secret", http.StatusUnauthorized},
		{"empty bearer token", "secret", "Bearer ", http.StatusUnauthorized},
		// An unset API_KEY must fail closed, not open.
		{"empty configured key", "", "Bearer anything", http.StatusUnauthorized},
		{"empty key and empty token", "", "Bearer ", http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := auth.RequireAPIKey(tt.apiKey)(ok)
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestIdentityInContext(t *testing.T) {
	t.Parallel()

	var got auth.Identity
	var ok bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok = auth.FromContext(r.Context())
	})
	handler := auth.RequireAPIKey("secret")(next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if !ok {
		t.Fatal("identity missing from context")
	}
	if got.Subject == "" {
		t.Error("identity subject is empty")
	}
}

func TestFromContextWithoutMiddleware(t *testing.T) {
	t.Parallel()

	if _, ok := auth.FromContext(t.Context()); ok {
		t.Error("FromContext = ok on a bare context, want false")
	}
}
