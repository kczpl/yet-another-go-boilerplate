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

// Service keeps the business rules. Every method takes the owner's userID.
// Ownership lives here and in the SQL, never in the handlers.
type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// Add creates a note for userID. It returns ValidationError for bad input.
func (s *Service) Add(ctx context.Context, userID, text string) (Note, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Note{}, ValidationError("text must not be empty")
	}
	if utf8.RuneCountInString(text) > maxTextLength {
		return Note{}, ValidationError(fmt.Sprintf("text must be at most %d characters", maxTextLength))
	}
	return s.repo.Insert(ctx, userID, text)
}

// List returns userID's notes, newest first, at most listLimit rows.
func (s *Service) List(ctx context.Context, userID string) ([]Note, error) {
	return s.repo.ListByUser(ctx, userID)
}

// Delete removes userID's note. It returns ErrNotFound for unknown ids and
// for notes that another user owns.
func (s *Service) Delete(ctx context.Context, userID, id string) error {
	return s.repo.Delete(ctx, id, userID)
}
