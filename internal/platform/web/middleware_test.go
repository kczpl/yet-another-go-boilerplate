package web_test

import (
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/config"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/logging"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/web"
)

func TestRequestLogRecordsFirstFinalStatus(t *testing.T) {
	for _, implicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "explicit", true: "implicit"}[implicit], func(t *testing.T) {
			var logs strings.Builder
			logger := logging.New(&logs, config.Config{})
			h := web.LogRequests(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if implicit {
					_, _ = io.WriteString(w, "ok")
				} else {
					w.WriteHeader(http.StatusCreated)
				}
				w.WriteHeader(http.StatusInternalServerError)
			}))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
			if strings.Contains(logs.String(), `"status":500`) {
				t.Fatal("log recorded a status that the client did not receive")
			}
		})
	}
}

func TestPanicUsesUnifiedResponse(t *testing.T) {
	t.Parallel()
	logger := logging.New(io.Discard, config.Config{})
	h := web.RecoverPanics(logger, web.SecureHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("private cause") })))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), `"status":500`) {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "private cause") {
		t.Fatal("panic cause reached the client")
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("panic response lost security headers")
	}
}

func TestPanicAfterWriteAbortsResponse(t *testing.T) {
	t.Parallel()
	logger := logging.New(io.Discard, config.Config{})
	h := web.RecoverPanics(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "partial")
		panic("private cause")
	}))
	defer func() {
		if got := recover(); got != http.ErrAbortHandler {
			t.Errorf("panic = %v, want ErrAbortHandler", got)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	t.Fatal("partial response did not abort")
}

func TestTemplateFailureDoesNotCommitPartialHTML(t *testing.T) {
	t.Parallel()
	logger := logging.New(io.Discard, config.Config{})
	tmpl := template.Must(template.New("fragment").Parse(`private prefix {{.Missing}}`))
	h := web.E(logger, func(w http.ResponseWriter, _ *http.Request) error {
		return web.RenderFragment(w, 200, tmpl, "fragment", struct{}{})
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "private prefix") {
		t.Fatalf("response = %d %s", rec.Code, rec.Body.String())
	}
}
