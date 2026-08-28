package user

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"unicode/utf8"
)

const (
	minPasswordLength = 8
	// Cap the input length, so hashPassword does not receive megabytes.
	maxPasswordLength = 512
	maxNameLength     = 100
)

// Service keeps the account business rules.
type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// Register creates an account. It returns ValidationError for bad input and
// ErrEmailTaken for duplicate emails.
func (s *Service) Register(ctx context.Context, email, name, password string) (User, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return User{}, err
	}
	name, err = normalizeName(name)
	if err != nil {
		return User{}, err
	}
	if err := validatePassword(password); err != nil {
		return User{}, err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return User{}, fmt.Errorf("hashing password: %w", err)
	}
	return s.repo.Insert(ctx, email, name, hash)
}

// Authenticate checks the credentials and returns the user, or
// ErrInvalidCredentials. That error is the same for an unknown email and a
// wrong password.
func (s *Service) Authenticate(ctx context.Context, email, password string) (User, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return User{}, ErrInvalidCredentials
	}
	u, err := s.repo.GetByEmail(ctx, email)
	if errors.Is(err, ErrNotFound) {
		// Burn a hash, so the response time does not reveal whether the
		// email exists.
		if _, hashErr := hashPassword(password); hashErr != nil {
			return User{}, fmt.Errorf("hashing password: %w", hashErr)
		}
		return User{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, err
	}
	if !verifyPassword(u.PasswordHash, password) {
		return User{}, ErrInvalidCredentials
	}
	return u, nil
}

// Get returns the user by id, or ErrNotFound.
func (s *Service) Get(ctx context.Context, id string) (User, error) {
	return s.repo.GetByID(ctx, id)
}

// UpdateProfile changes the email and the name. It returns ValidationError,
// ErrEmailTaken, or ErrNotFound.
func (s *Service) UpdateProfile(ctx context.Context, id, email, name string) (User, error) {
	email, err := normalizeEmail(email)
	if err != nil {
		return User{}, err
	}
	name, err = normalizeName(name)
	if err != nil {
		return User{}, err
	}
	return s.repo.Update(ctx, id, email, name)
}

func normalizeEmail(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || len(email) > 254 {
		return "", ValidationError("enter a valid email address")
	}
	// ParseAddress also accepts "Bob <bob@example.com>". The equality check
	// pins it to a bare address.
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return "", ValidationError("enter a valid email address")
	}
	return email, nil
}

func normalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ValidationError("name must not be empty")
	}
	if utf8.RuneCountInString(name) > maxNameLength {
		return "", ValidationError(fmt.Sprintf("name must be at most %d characters", maxNameLength))
	}
	return name, nil
}

func validatePassword(password string) error {
	if len(password) < minPasswordLength {
		return ValidationError(fmt.Sprintf("password must be at least %d characters", minPasswordLength))
	}
	if len(password) > maxPasswordLength {
		return ValidationError(fmt.Sprintf("password must be at most %d characters", maxPasswordLength))
	}
	return nil
}
