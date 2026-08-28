// Package web is the shared HTTP/HTML toolkit: the base layout, the render
// helpers, the static assets, and the request middleware. Features import
// web. web imports no feature.
package web

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
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

// RenderPage renders the full layout with the page's content. The handler
// returns the error, and web.E turns it into the unified 500.
func RenderPage(w http.ResponseWriter, status int, t *template.Template, data any) error {
	return render(w, status, t, "layout.html", data)
}

// RenderFragment renders one named template without the layout. htmx swaps
// this response into its hx-target.
func RenderFragment(w http.ResponseWriter, status int, t *template.Template, name string, data any) error {
	return render(w, status, t, name, data)
}

// render buffers the output, so a mid-render error returns before the first
// byte reaches the client and can still become a clean 500.
func render(w http.ResponseWriter, status int, t *template.Template, name string, data any) error {
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name, data); err != nil {
		return fmt.Errorf("rendering template %s: %w", name, err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
	return nil
}

// IsHTMX reports whether htmx issued the request. Handlers use it to choose
// a fragment or a full response, so every flow also works without
// JavaScript.
func IsHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// Static serves the embedded /static/* assets (htmx, stylesheet).
func Static() http.Handler {
	return http.FileServerFS(staticFS)
}
