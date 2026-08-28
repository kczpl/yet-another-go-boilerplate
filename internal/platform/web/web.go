// Package web is the shared HTTP/HTML toolkit: the base layout, the render
// helpers, the static assets, and the request middleware. Features import
// web. web imports no feature.
package web

import (
	"bytes"
	"context"
	"embed"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
)

//go:embed layout.html
var layoutFS embed.FS

//go:embed static
var staticFS embed.FS

// Page carries the data that every page template needs. Feature page-data
// structs embed it, so all templates can use .Title and .LoggedIn.
type Page struct {
	Title    string
	LoggedIn bool
}

// MustPage parses the shared layout plus one page file into an isolated
// template set. Call it from a package-level var. It panics on a parse
// error, so a bad template fails at startup, not on the first request.
func MustPage(feature fs.FS, page string) *template.Template {
	t := template.Must(template.ParseFS(layoutFS, "layout.html"))
	return template.Must(t.ParseFS(feature, page))
}

// RenderPage renders the full layout with the page's content.
func RenderPage(ctx context.Context, logger *slog.Logger, w http.ResponseWriter, status int, t *template.Template, data any) {
	render(ctx, logger, w, status, t, "layout.html", data)
}

// RenderFragment renders one named template without the layout. htmx swaps
// this response into its hx-target.
func RenderFragment(ctx context.Context, logger *slog.Logger, w http.ResponseWriter, status int, t *template.Template, name string, data any) {
	render(ctx, logger, w, status, t, name, data)
}

// render buffers the output, so a mid-render error can still become a clean
// 500, not a half-written page.
func render(ctx context.Context, logger *slog.Logger, w http.ResponseWriter, status int, t *template.Template, name string, data any) {
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name, data); err != nil {
		logger.ErrorContext(ctx, "rendering template", "template", name, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

// IsHTMX reports whether htmx issued the request. Handlers use it to choose
// a fragment or a full response, so every flow also works without
// JavaScript.
func IsHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// InternalError logs an unexpected error and sends the unified opaque 500.
// Never leak err to the client.
func InternalError(logger *slog.Logger, w http.ResponseWriter, r *http.Request, err error) {
	RespondError(logger, w, r, err)
}

// Static serves the embedded /static/* assets (htmx, stylesheet).
func Static() http.Handler {
	return http.FileServerFS(staticFS)
}
