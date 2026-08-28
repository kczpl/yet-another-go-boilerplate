package note

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repo implements Repository against PostgreSQL. All SQL of the feature
// lives in this file.
type Repo struct {
	db *pgxpool.Pool
}

var _ Repository = (*Repo)(nil)

func NewRepo(db *pgxpool.Pool) *Repo {
	return &Repo{db: db}
}

const noteColumns = "id, user_id, text, created_at, updated_at"

// listLimit caps ListByUser. The page shows one screen of notes, and an
// unbounded query gets slower as an account ages. If users need more, add
// keyset pagination: filter on (created_at, id) < the last visible row.
const listLimit = 100

func (r *Repo) Insert(ctx context.Context, userID, text string) (Note, error) {
	rows, _ := r.db.Query(ctx, `
		INSERT INTO notes (user_id, text)
		VALUES (@user_id, @text)
		RETURNING `+noteColumns,
		pgx.NamedArgs{
			"user_id": userID,
			"text":    text,
		})
	n, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Note])
	if err != nil {
		return Note{}, fmt.Errorf("inserting note: %w", err)
	}
	return n, nil
}

func (r *Repo) ListByUser(ctx context.Context, userID string) ([]Note, error) {
	rows, _ := r.db.Query(ctx,
		"SELECT "+noteColumns+` FROM notes
		WHERE user_id = @user_id
		ORDER BY created_at DESC, id DESC
		LIMIT @limit`,
		pgx.NamedArgs{"user_id": userID, "limit": listLimit})
	notes, err := pgx.CollectRows(rows, pgx.RowToStructByName[Note])
	if err != nil {
		return nil, fmt.Errorf("listing notes: %w", err)
	}
	return notes, nil
}

func (r *Repo) Delete(ctx context.Context, id, userID string) error {
	// The owner scope makes a foreign id delete zero rows, the same as an
	// unknown id.
	tag, err := r.db.Exec(ctx,
		"DELETE FROM notes WHERE id = @id AND user_id = @user_id",
		pgx.NamedArgs{"id": id, "user_id": userID})
	if err != nil {
		return fmt.Errorf("deleting note: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
