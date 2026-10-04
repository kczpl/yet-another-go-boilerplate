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

// ListByUser pages by keyset, never by OFFSET. OFFSET reads and discards
// every skipped row. The keyset condition starts directly at the cursor in
// the (user_id, created_at DESC, id DESC) index. The first page has no
// cursor, so it uses a separate query.
func (r *Repo) ListByUser(ctx context.Context, userID string, after Cursor, limit int) ([]Note, error) {
	var rows pgx.Rows
	if after.IsZero() {
		rows, _ = r.db.Query(ctx,
			"SELECT "+noteColumns+` FROM notes
			WHERE user_id = @user_id
			ORDER BY created_at DESC, id DESC
			LIMIT @limit`,
			pgx.NamedArgs{"user_id": userID, "limit": limit})
	} else {
		rows, _ = r.db.Query(ctx,
			"SELECT "+noteColumns+` FROM notes
			WHERE user_id = @user_id
			  AND (created_at, id) < (@after_created_at, @after_id)
			ORDER BY created_at DESC, id DESC
			LIMIT @limit`,
			pgx.NamedArgs{
				"user_id":          userID,
				"after_created_at": after.CreatedAt,
				"after_id":         after.ID,
				"limit":            limit,
			})
	}
	notes, err := pgx.CollectRows(rows, pgx.RowToStructByName[Note])
	if err != nil {
		return nil, fmt.Errorf("listing notes: %w", err)
	}
	return notes, nil
}

func (r *Repo) Delete(ctx context.Context, userID, id string) error {
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
