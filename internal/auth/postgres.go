package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repo implements Repository against PostgreSQL. All session SQL lives in
// this file.
type Repo struct {
	db *pgxpool.Pool
}

var _ Repository = (*Repo)(nil)

func NewRepo(db *pgxpool.Pool) *Repo {
	return &Repo{db: db}
}

func (r *Repo) Insert(ctx context.Context, s Session) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at)
		VALUES (@token_hash, @user_id, @expires_at)`,
		pgx.NamedArgs{
			"token_hash": s.TokenHash,
			"user_id":    s.UserID,
			"expires_at": s.ExpiresAt,
		})
	if err != nil {
		return fmt.Errorf("inserting session: %w", err)
	}
	return nil
}

func (r *Repo) Get(ctx context.Context, tokenHash string) (Session, error) {
	// SQL enforces expiry, in one place.
	rows, _ := r.db.Query(ctx, `
		SELECT token_hash, user_id, created_at, expires_at
		FROM sessions
		WHERE token_hash = @token_hash AND expires_at > now()`,
		pgx.NamedArgs{"token_hash": tokenHash})
	session, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Session])
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNoSession
	}
	if err != nil {
		return Session{}, fmt.Errorf("getting session: %w", err)
	}
	return session, nil
}

func (r *Repo) Delete(ctx context.Context, tokenHash string) error {
	_, err := r.db.Exec(ctx,
		"DELETE FROM sessions WHERE token_hash = @token_hash",
		pgx.NamedArgs{"token_hash": tokenHash})
	if err != nil {
		return fmt.Errorf("deleting session: %w", err)
	}
	return nil
}

func (r *Repo) DeleteExpired(ctx context.Context) error {
	_, err := r.db.Exec(ctx, "DELETE FROM sessions WHERE expires_at <= now()")
	if err != nil {
		return fmt.Errorf("deleting expired sessions: %w", err)
	}
	return nil
}
