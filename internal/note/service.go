package note

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

// maxTextLength mirrors the CHECK constraint in
// migrations/0003_create_notes.sql.
const maxTextLength = 10000

// pageSize is the number of notes on one page.
const pageSize = 20

// Service keeps the business rules. Every method takes the owner's userID.
// Ownership lives here and in the SQL, never in the handlers.
type Service struct {
	repo Repository
}

// NewService returns a Service that keeps the notes in repo.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// Add creates a note for userID. It returns ValidationError for bad input.
func (s *Service) Add(ctx context.Context, userID, text string) (Note, error) {
	if !utf8.ValidString(text) || strings.ContainsRune(text, '\x00') {
		return Note{}, ValidationError("text must contain valid text")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return Note{}, ValidationError("text must not be empty")
	}
	if utf8.RuneCountInString(text) > maxTextLength {
		return Note{}, ValidationError(fmt.Sprintf("text must be at most %d characters", maxTextLength))
	}
	return s.repo.Insert(ctx, userID, text)
}

// List returns one page of userID's notes, newest first. A zero after
// starts at the newest note. next is the cursor of the following page. It
// is zero on the last page.
func (s *Service) List(ctx context.Context, userID string, after Cursor) (notes []Note, next Cursor, err error) {
	// One extra row shows if a following page exists.
	notes, err = s.repo.ListByUser(ctx, userID, after, pageSize+1)
	if err != nil {
		return nil, Cursor{}, err
	}
	if len(notes) > pageSize {
		notes = notes[:pageSize]
		last := notes[pageSize-1]
		next = Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return notes, next, nil
}

// Delete removes userID's note. It returns ErrNotFound for unknown ids and
// for notes that another user owns.
func (s *Service) Delete(ctx context.Context, userID, id string) error {
	return s.repo.Delete(ctx, userID, id)
}
