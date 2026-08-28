package user

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repo implements Repository against PostgreSQL. All SQL of the feature
// lives in this file. Repo translates pgx errors into the package
// sentinels; nothing above this file sees pgx error types.
type Repo struct {
	db *pgxpool.Pool
}

var _ Repository = (*Repo)(nil)

func NewRepo(db *pgxpool.Pool) *Repo {
	return &Repo{db: db}
}

const userColumns = "id, email, name, password_hash, created_at, updated_at"

func (r *Repo) Insert(ctx context.Context, email, name, passwordHash string) (User, error) {
	rows, _ := r.db.Query(ctx, `
		INSERT INTO users (email, name, password_hash)
		VALUES (@email, @name, @password_hash)
		RETURNING `+userColumns,
		pgx.NamedArgs{
			"email":         email,
			"name":          name,
			"password_hash": passwordHash,
		})
	u, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[User])
	if isUniqueViolation(err) {
		return User{}, ErrEmailTaken
	}
	if err != nil {
		return User{}, fmt.Errorf("inserting user: %w", err)
	}
	return u, nil
}

func (r *Repo) GetByID(ctx context.Context, id string) (User, error) {
	rows, _ := r.db.Query(ctx,
		"SELECT "+userColumns+" FROM users WHERE id = @id",
		pgx.NamedArgs{"id": id})
	u, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[User])
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("getting user by id: %w", err)
	}
	return u, nil
}

func (r *Repo) GetByEmail(ctx context.Context, email string) (User, error) {
	rows, _ := r.db.Query(ctx,
		"SELECT "+userColumns+" FROM users WHERE lower(email) = lower(@email)",
		pgx.NamedArgs{"email": email})
	u, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[User])
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("getting user by email: %w", err)
	}
	return u, nil
}

func (r *Repo) Update(ctx context.Context, id, email, name string) (User, error) {
	rows, _ := r.db.Query(ctx, `
		UPDATE users
		SET email = @email, name = @name, updated_at = now()
		WHERE id = @id
		RETURNING `+userColumns,
		pgx.NamedArgs{
			"id":    id,
			"email": email,
			"name":  name,
		})
	u, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[User])
	if isUniqueViolation(err) {
		return User{}, ErrEmailTaken
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("updating user: %w", err)
	}
	return u, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
