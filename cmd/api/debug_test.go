package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDebugHandler(t *testing.T) {
	t.Parallel()
	h := debugHandler()
	for _, path := range []string{
		"/debug/pprof/",
		"/debug/pprof/goroutine?debug=1",
		"/debug/pprof/cmdline",
		"/debug/vars",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
		}
	}
}

func TestStartDebugServerFailsFast(t *testing.T) {
	t.Parallel()
	errCh := make(chan error, 1)
	srv, err := startDebugServer("127.0.0.1:0", errCh)
	if err != nil {
		t.Fatalf("start on a free port: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	// A bad address must fail at startup, not later in the background.
	if _, err := startDebugServer("256.0.0.1:0", errCh); err == nil {
		t.Fatal("started on a bad address")
	}
}
