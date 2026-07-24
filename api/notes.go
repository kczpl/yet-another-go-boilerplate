package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/kczpl/yet-another-go-boilerplate/domains/notes"
)

// Handlers are maker functions that close over their dependencies and return
// an http.Handler. They only decode → validate → call the service → map the
// result to HTTP. Request/response types live here, next to the handlers, and
// are mapped to/from domain types explicitly.

type noteResponse struct {
	ID        uuid.UUID `json:"id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toNoteResponse(n notes.Note) noteResponse {
	return noteResponse{
		ID:        n.ID,
		Title:     n.Title,
		Content:   n.Content,
		CreatedAt: n.CreatedAt,
		UpdatedAt: n.UpdatedAt,
	}
}

type createNoteRequest struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

func (req createNoteRequest) Valid(ctx context.Context) map[string]string {
	problems := map[string]string{}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		problems["title"] = "must not be empty"
	} else if utf8.RuneCountInString(title) > 255 {
		problems["title"] = "must be at most 255 characters"
	}
	return problems
}

func handleNotesCreate(logger *slog.Logger, service *notes.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, problems, err := decodeValid[createNoteRequest](r)
		if err != nil {
			if problems != nil {
				respondError(w, http.StatusUnprocessableEntity, "validation failed", problems)
				return
			}
			respondBodyError(w, err)
			return
		}

		note, err := service.Create(r.Context(), notes.CreateParams{
			Title:   req.Title,
			Content: req.Content,
		})
		if err != nil {
			respondInternalError(w, logger, r, err)
			return
		}
		respondData(w, http.StatusCreated, toNoteResponse(note))
	})
}

func handleNotesList(logger *slog.Logger, service *notes.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, problems := parsePagination(r)
		if problems != nil {
			respondError(w, http.StatusUnprocessableEntity, "validation failed", problems)
			return
		}

		items, total, err := service.List(r.Context(), notes.ListParams{
			Limit:  page.limit(),
			Offset: page.offset(),
		})
		if err != nil {
			respondInternalError(w, logger, r, err)
			return
		}

		page.TotalCount = total
		data := make([]noteResponse, len(items))
		for i, n := range items {
			data[i] = toNoteResponse(n)
		}
		encode(w, http.StatusOK, listResponse[noteResponse]{Data: data, Pagination: page})
	})
}

func handleNotesGet(logger *slog.Logger, service *notes.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			respondError(w, http.StatusUnprocessableEntity, "validation failed",
				map[string]string{"id": "must be a valid UUID"})
			return
		}

		note, err := service.Get(r.Context(), id)
		if errors.Is(err, notes.ErrNotFound) {
			respondError(w, http.StatusNotFound, "note not found", nil)
			return
		}
		if err != nil {
			respondInternalError(w, logger, r, err)
			return
		}
		respondData(w, http.StatusOK, toNoteResponse(note))
	})
}

// updateNoteRequest is a partial update: absent (or null) fields are left
// unchanged, so pointers distinguish "not sent" from "set to empty".
type updateNoteRequest struct {
	Title   *string `json:"title"`
	Content *string `json:"content"`
}

func (req updateNoteRequest) Valid(ctx context.Context) map[string]string {
	problems := map[string]string{}
	if req.Title == nil && req.Content == nil {
		problems["body"] = "must set at least one of: title, content"
	}
	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if title == "" {
			problems["title"] = "must not be empty"
		} else if utf8.RuneCountInString(title) > 255 {
			problems["title"] = "must be at most 255 characters"
		}
	}
	return problems
}

func handleNotesUpdate(logger *slog.Logger, service *notes.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			respondError(w, http.StatusUnprocessableEntity, "validation failed",
				map[string]string{"id": "must be a valid UUID"})
			return
		}

		req, problems, err := decodeValid[updateNoteRequest](r)
		if err != nil {
			if problems != nil {
				respondError(w, http.StatusUnprocessableEntity, "validation failed", problems)
				return
			}
			respondBodyError(w, err)
			return
		}

		note, err := service.Update(r.Context(), id, notes.UpdateParams{
			Title:   req.Title,
			Content: req.Content,
		})
		if errors.Is(err, notes.ErrNotFound) {
			respondError(w, http.StatusNotFound, "note not found", nil)
			return
		}
		if err != nil {
			respondInternalError(w, logger, r, err)
			return
		}
		respondData(w, http.StatusOK, toNoteResponse(note))
	})
}

func handleNotesDelete(logger *slog.Logger, service *notes.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			respondError(w, http.StatusUnprocessableEntity, "validation failed",
				map[string]string{"id": "must be a valid UUID"})
			return
		}

		err = service.Delete(r.Context(), id)
		if errors.Is(err, notes.ErrNotFound) {
			respondError(w, http.StatusNotFound, "note not found", nil)
			return
		}
		if err != nil {
			respondInternalError(w, logger, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
