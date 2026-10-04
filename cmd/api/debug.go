package main

import (
	"expvar"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"time"
)

// startDebugServer serves debugHandler on addr. Config.Validate accepts
// only a loopback addr, because the profiles show internal data. The
// function opens the listener before it returns, so a bad addr stops the
// startup. A later serve error goes to errCh.
func startDebugServer(addr string, errCh chan<- error) (*http.Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listening on the debug address: %w", err)
	}
	// There is no WriteTimeout: a CPU profile or a trace streams for as
	// many seconds as the request asks.
	srv := &http.Server{Handler: debugHandler(), ReadHeaderTimeout: 5 * time.Second}
	go func() { errCh <- fmt.Errorf("debug server: %w", srv.Serve(ln)) }()
	return srv, nil
}

// debugHandler serves the runtime profiles (pprof) and the expvar
// variables, which include the memory statistics. The two imports also
// register these routes on http.DefaultServeMux. The app never serves that
// mux, so only the debug listener exposes them.
func debugHandler() http.Handler {
	mux := http.NewServeMux()
	// The patterns have no method, because Symbol also accepts POST.
	// Index also serves the named profiles, for example /debug/pprof/heap.
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.Handle("GET /debug/vars", expvar.Handler())
	return mux
}
