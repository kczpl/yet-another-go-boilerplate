package notes

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// Service holds the business logic for notes. Handlers call it with validated
// input; it owns normalization, invariants, and transaction boundaries, and
// returns domain types and domain errors — nothing HTTP-shaped.
type Service struct {
	repo *Repo
}

func NewService(repo *Repo) *Service {
	return &Service{repo: repo}
}

func (s *Service) Create(ctx context.Context, params CreateParams) (Note, error) {
	params.Title = strings.TrimSpace(params.Title)
	return s.repo.Insert(ctx, params)
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (Note, error) {
	return s.repo.GetByID(ctx, id)
}

// List returns one page of notes plus the total count across all pages.
func (s *Service) List(ctx context.Context, params ListParams) ([]Note, int, error) {
	return s.repo.List(ctx, params)
}

// Update applies a partial update; nil fields keep their current value.
func (s *Service) Update(ctx context.Context, id uuid.UUID, params UpdateParams) (Note, error) {
	if params.Title != nil {
		title := strings.TrimSpace(*params.Title)
		params.Title = &title
	}
	return s.repo.Update(ctx, id, params)
}

func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	return s.repo.Delete(ctx, id)
}
