package chat

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

// chatTmpl holds the page template. The package parses it at startup and
// does not change it.
var chatTmpl = web.MustPage(templatesFS, "templates/chat.html")

// Routes registers every endpoint of the feature. Each question is a paid
// model call, so limiter wraps the question endpoint.
func Routes(mux *http.ServeMux, logger *slog.Logger, svc *Service, limiter *web.RateLimiter) {
	mux.Handle("GET /chat", auth.RequireIdentity(web.E(logger, handleChat(svc))))
	mux.Handle("POST /chat", auth.RequireIdentity(web.E(logger, limiter.Limit(handleAsk(svc)))))
}

type chatData struct {
	web.Page
	Model    string
	Question string
	Answer   string
	Error    string
}

// handleChat shows the chat section for htmx (the dashboard embeds it)
// and the full page for all other clients.
func handleChat(svc *Service) web.HandlerE {
	return func(w http.ResponseWriter, r *http.Request) error {
		return renderChat(w, r, http.StatusOK, chatData{
			Title:    "Chat",
			LoggedIn: true,
			Model:    svc.Model(),
		})
	}
}

// handleAsk answers a question. static/app.js asks for an event stream
// and gets the answer in parts. A plain form post gets the full page with
// the complete answer.
func handleAsk(svc *Service) web.HandlerE {
	return func(w http.ResponseWriter, r *http.Request) error {
		if err := r.ParseForm(); err != nil {
			return web.BadRequest("invalid form data")
		}
		question := r.PostFormValue("question")
		if web.WantsEventStream(r) {
			return streamAnswer(w, r, svc, question)
		}
		return renderAnswer(w, r, svc, question)
	}
}

// streamAnswer sends the answer as events. "delta" carries a part of the
// answer. "done" ends the answer and carries the stop reason. "error"
// ends the answer and carries a message that is safe to show. After the
// stream starts, the status is already 200, so every error must become an
// "error" event.
func streamAnswer(w http.ResponseWriter, r *http.Request, svc *Service, question string) error {
	stream, err := web.NewEventStream(w)
	if err != nil {
		return err
	}
	resp, err := svc.Stream(r.Context(), question, func(text string) error {
		return stream.Send("delta", text)
	})
	if vErr, ok := errors.AsType[ValidationError](err); ok {
		return stream.Send("error", vErr.Error())
	}
	if err != nil {
		// web.E logs err. The client gets only the generic message.
		_ = stream.Send("error", "the answer failed, try again")
		return err
	}
	return stream.Send("done", string(resp.StopReason))
}

// renderAnswer waits for the complete answer and renders the full page.
// The page is the result of the post, like a search result. There is no
// stored change, so there is no redirect.
func renderAnswer(w http.ResponseWriter, r *http.Request, svc *Service, question string) error {
	// The model can answer after http.Server.WriteTimeout. Without this
	// call, the server then drops the page of a paid answer.
	if err := web.RemoveWriteDeadline(w); err != nil {
		return err
	}
	data := chatData{
		Title:    "Chat",
		LoggedIn: true,
		Model:    svc.Model(),
		Question: question,
	}
	resp, err := svc.Answer(r.Context(), question)
	if vErr, ok := errors.AsType[ValidationError](err); ok {
		data.Error = vErr.Error()
		return renderChat(w, r, http.StatusUnprocessableEntity, data)
	}
	if err != nil {
		return err
	}
	data.Answer = resp.Text
	return renderChat(w, r, http.StatusOK, data)
}

// renderChat writes the chat section for htmx and the full page for all
// other clients.
func renderChat(w http.ResponseWriter, r *http.Request, status int, data chatData) error {
	if web.IsHTMX(r) {
		return web.RenderFragment(w, status, chatTmpl, "chat-section", data)
	}
	return web.RenderPage(w, status, chatTmpl, data)
}
