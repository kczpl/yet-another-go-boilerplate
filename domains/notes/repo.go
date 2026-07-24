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
// Arguments are passed by name (pgx.NamedArgs) and rows are collected by
// column name (pgx.RowToStructByName), so neither depends on ordering. It
// never begins transactions — multi-statement flows are composed by the
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
		VALUES (@title, @content)
		RETURNING ` + noteColumns

	rows, err := r.db.Query(ctx, q, pgx.NamedArgs{
		"title":   params.Title,
		"content": params.Content,
	})
	if err != nil {
		return Note{}, fmt.Errorf("inserting note: %w", err)
	}
	note, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Note])
	if err != nil {
		return Note{}, fmt.Errorf("inserting note: %w", err)
	}
	return note, nil
}

func (r *Repo) GetByID(ctx context.Context, id uuid.UUID) (Note, error) {
	const q = `SELECT ` + noteColumns + ` FROM notes WHERE id = @id`

	rows, err := r.db.Query(ctx, q, pgx.NamedArgs{"id": id})
	if err != nil {
		return Note{}, fmt.Errorf("getting note: %w", err)
	}
	note, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Note])
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
		LIMIT @limit OFFSET @offset`

	type listRow struct {
		Note
		TotalCount int `db:"total_count"`
	}

	rows, err := r.db.Query(ctx, q, pgx.NamedArgs{
		"limit":  params.Limit,
		"offset": params.Offset,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("listing notes: %w", err)
	}
	listRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[listRow])
	if err != nil {
		return nil, 0, fmt.Errorf("listing notes: %w", err)
	}

	notes := make([]Note, len(listRows))
	total := 0
	for i, row := range listRows {
		notes[i] = row.Note
		total = row.TotalCount
	}
	return notes, total, nil
}

func (r *Repo) Update(ctx context.Context, id uuid.UUID, params UpdateParams) (Note, error) {
	// coalesce(NULL, col) keeps the current value, so nil params are no-ops
	// and the whole partial update stays one statement.
	const q = `
		UPDATE notes
		SET title      = coalesce(@title, title),
		    content    = coalesce(@content, content),
		    updated_at = now()
		WHERE id = @id
		RETURNING ` + noteColumns

	rows, err := r.db.Query(ctx, q, pgx.NamedArgs{
		"id":      id,
		"title":   params.Title,
		"content": params.Content,
	})
	if err != nil {
		return Note{}, fmt.Errorf("updating note: %w", err)
	}
	note, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Note])
	if errors.Is(err, pgx.ErrNoRows) {
		return Note{}, ErrNotFound
	}
	if err != nil {
		return Note{}, fmt.Errorf("updating note: %w", err)
	}
	return note, nil
}

func (r *Repo) Delete(ctx context.Context, id uuid.UUID) error {
	const q = `DELETE FROM notes WHERE id = @id`

	tag, err := r.db.Exec(ctx, q, pgx.NamedArgs{"id": id})
	if err != nil {
		return fmt.Errorf("deleting note: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
