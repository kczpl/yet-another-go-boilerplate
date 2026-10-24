package triage

import (
	"embed"
	"errors"
	"log/slog"
	"net/http"

	"github.com/kczpl/yet-another-go-boilerplate/internal/auth"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/web"
)

//go:embed templates/*.html
var templatesFS embed.FS

// triageTmpl holds the page template. The package parses it at startup
// and does not change it.
var triageTmpl = web.MustPage(templatesFS, "templates/triage.html")

// Routes registers every endpoint of the feature. Each triage is a paid
// model call, so limiter wraps the triage endpoint.
func Routes(mux *http.ServeMux, logger *slog.Logger, svc *Service, limiter *web.RateLimiter) {
	mux.Handle("GET /triage", auth.RequireIdentity(web.E(logger, handleTriagePage(svc))))
	mux.Handle("POST /triage", auth.RequireIdentity(web.E(logger, limiter.Limit(handleTriage(svc)))))
}

type triageData struct {
	web.Page
	Model    string
	Message  string
	Decision *Decision
	Error    string
}

// handleTriagePage shows the triage section for htmx (the dashboard
// embeds it) and the full page for all other clients.
func handleTriagePage(svc *Service) web.HandlerE {
	return func(w http.ResponseWriter, r *http.Request) error {
		return renderTriage(w, r, http.StatusOK, triageData{
			Title:    "Triage",
			LoggedIn: true,
			Model:    svc.Model(),
		})
	}
}

// handleTriage shows the decision for one message. The decision is the
// result of the post, like a search result. There is no stored change, so
// there is no redirect.
func handleTriage(svc *Service) web.HandlerE {
	return func(w http.ResponseWriter, r *http.Request) error {
		if err := r.ParseForm(); err != nil {
			return web.BadRequest("invalid form data")
		}
		data := triageData{
			Title:    "Triage",
			LoggedIn: true,
			Model:    svc.Model(),
			Message:  r.PostFormValue("message"),
		}
		decision, err := svc.Triage(r.Context(), data.Message)
		if vErr, ok := errors.AsType[ValidationError](err); ok {
			data.Error = vErr.Error()
			return renderTriage(w, r, http.StatusUnprocessableEntity, data)
		}
		if err != nil {
			return err
		}
		data.Decision = &decision
		return renderTriage(w, r, http.StatusOK, data)
	}
}

// renderTriage writes the triage section for htmx and the full page for
// all other clients.
func renderTriage(w http.ResponseWriter, r *http.Request, status int, data triageData) error {
	if web.IsHTMX(r) {
		return web.RenderFragment(w, status, triageTmpl, "triage-section", data)
	}
	return web.RenderPage(w, status, triageTmpl, data)
}
