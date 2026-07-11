package notes

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repo is the data access layer for notes: plain SQL, one method per query.
// It never begins transactions — multi-statement flows are composed by the
// service. Driver errors are translated to domain errors here and nowhere
// else.
type Repo struct {
	db *pgxpool.Pool
}

func NewRepo(db *pgxpool.Pool) *Repo {
	return &Repo{db: db}
}

const noteColumns = `id, title, content, created_at, updated_at`

func (r *Repo) Insert(ctx context.Context, params CreateParams) (Note, error) {
	const q = `
		INSERT INTO notes (title, content)
		VALUES ($1, $2)
		RETURNING ` + noteColumns

	note, err := scanNote(r.db.QueryRow(ctx, q, params.Title, params.Content))
	if err != nil {
		return Note{}, fmt.Errorf("inserting note: %w", err)
	}
	return note, nil
}

func (r *Repo) GetByID(ctx context.Context, id uuid.UUID) (Note, error) {
	const q = `SELECT ` + noteColumns + ` FROM notes WHERE id = $1`

	note, err := scanNote(r.db.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Note{}, ErrNotFound
	}
	if err != nil {
		return Note{}, fmt.Errorf("getting note: %w", err)
	}
	return note, nil
}

func (r *Repo) List(ctx context.Context, params ListParams) ([]Note, int, error) {
	// count(*) OVER () returns the pre-LIMIT total on every row, so one query
	// serves both the page and the pagination metadata. Note: a page past the
	// end returns no rows, hence total 0 — acceptable for this endpoint.
	const q = `
		SELECT ` + noteColumns + `, count(*) OVER () AS total_count
		FROM notes
		ORDER BY created_at DESC, id DESC
		LIMIT $1 OFFSET $2`

	rows, err := r.db.Query(ctx, q, params.Limit, params.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("listing notes: %w", err)
	}
	defer rows.Close()

	notes := []Note{}
	total := 0
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.Title, &n.Content, &n.CreatedAt, &n.UpdatedAt, &total); err != nil {
			return nil, 0, fmt.Errorf("scanning note: %w", err)
		}
		notes = append(notes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("reading notes: %w", err)
	}
	return notes, total, nil
}

func (r *Repo) Delete(ctx context.Context, id uuid.UUID) error {
	const q = `DELETE FROM notes WHERE id = $1`

	tag, err := r.db.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("deleting note: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanNote(row pgx.Row) (Note, error) {
	var n Note
	if err := row.Scan(&n.ID, &n.Title, &n.Content, &n.CreatedAt, &n.UpdatedAt); err != nil {
		return Note{}, err
	}
	return n, nil
}
