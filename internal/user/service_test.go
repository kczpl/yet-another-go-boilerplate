package user_test

import (
	"errors"
	"testing"

	"github.com/kczpl/yet-another-go-boilerplate/internal/testdb"
	"github.com/kczpl/yet-another-go-boilerplate/internal/user"
)

func newService(t *testing.T) *user.Service {
	t.Helper()
	return user.NewService(user.NewRepo(testdb.New(t)))
}

func TestRegisterNormalizesInput(t *testing.T) {
	t.Parallel()
	svc := newService(t)

	u, err := svc.Register(t.Context(), "  Bob@Example.COM ", "  Bob  ", "s3cret-pass")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if u.Email != "bob@example.com" {
		t.Errorf("Email = %q, want lowercased and trimmed", u.Email)
	}
	if u.Name != "Bob" {
		t.Errorf("Name = %q, want trimmed", u.Name)
	}
	if u.ID == "" || u.PasswordHash == "s3cret-pass" {
		t.Errorf("suspicious user row: %+v", u)
	}
}

func TestRegisterValidation(t *testing.T) {
	t.Parallel()
	svc := newService(t)

	tests := []struct {
		name     string
		email    string
		userName string
		password string
	}{
		{"empty email", "", "Bob", "s3cret-pass"},
		{"not an email", "not-an-email", "Bob", "s3cret-pass"},
		{"display-name form", "Bob <bob@example.com>", "Bob", "s3cret-pass"},
		{"empty name", "bob@example.com", "   ", "s3cret-pass"},
		{"short password", "bob@example.com", "Bob", "1234567"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.Register(t.Context(), tt.email, tt.userName, tt.password)
			var vErr user.ValidationError
			if !errors.As(err, &vErr) {
				t.Errorf("Register = %v, want ValidationError", err)
			}
		})
	}
}

func TestRegisterRejectsDuplicateEmail(t *testing.T) {
	t.Parallel()
	svc := newService(t)

	if _, err := svc.Register(t.Context(), "bob@example.com", "Bob", "s3cret-pass"); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	// The email is the same, only the case differs. The lower(email) index
	// catches it.
	_, err := svc.Register(t.Context(), "BOB@example.com", "Other Bob", "s3cret-pass")
	if !errors.Is(err, user.ErrEmailTaken) {
		t.Errorf("second Register = %v, want ErrEmailTaken", err)
	}
}

func TestAuthenticate(t *testing.T) {
	t.Parallel()
	svc := newService(t)

	registered, err := svc.Register(t.Context(), "bob@example.com", "Bob", "s3cret-pass")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	u, err := svc.Authenticate(t.Context(), "Bob@Example.com", "s3cret-pass")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if u.ID != registered.ID {
		t.Errorf("ID = %q, want %q", u.ID, registered.ID)
	}

	// A wrong password and an unknown email return the same error.
	if _, err := svc.Authenticate(t.Context(), "bob@example.com", "wrong-pass"); !errors.Is(err, user.ErrInvalidCredentials) {
		t.Errorf("wrong password: %v, want ErrInvalidCredentials", err)
	}
	if _, err := svc.Authenticate(t.Context(), "nobody@example.com", "s3cret-pass"); !errors.Is(err, user.ErrInvalidCredentials) {
		t.Errorf("unknown email: %v, want ErrInvalidCredentials", err)
	}
	if _, err := svc.Authenticate(t.Context(), "not-an-email", "s3cret-pass"); !errors.Is(err, user.ErrInvalidCredentials) {
		t.Errorf("invalid email: %v, want ErrInvalidCredentials", err)
	}
}

func TestUpdateProfile(t *testing.T) {
	t.Parallel()
	svc := newService(t)

	u, err := svc.Register(t.Context(), "bob@example.com", "Bob", "s3cret-pass")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	updated, err := svc.UpdateProfile(t.Context(), u.ID, "robert@example.com", "Robert")
	if err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	if updated.Email != "robert@example.com" || updated.Name != "Robert" {
		t.Errorf("updated = %+v, want new email and name", updated)
	}
	if !updated.UpdatedAt.After(u.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want after %v", updated.UpdatedAt, u.UpdatedAt)
	}

	// The service rejects bad input before it touches the database.
	if _, err := svc.UpdateProfile(t.Context(), u.ID, "not-an-email", "Robert"); err == nil {
		t.Error("UpdateProfile accepted an invalid email")
	}

	// An update to an email that another user owns fails.
	if _, err := svc.Register(t.Context(), "alice@example.com", "Alice", "s3cret-pass"); err != nil {
		t.Fatalf("Register alice: %v", err)
	}
	if _, err := svc.UpdateProfile(t.Context(), u.ID, "alice@example.com", "Robert"); !errors.Is(err, user.ErrEmailTaken) {
		t.Errorf("UpdateProfile to taken email = %v, want ErrEmailTaken", err)
	}
}

func TestGetUnknownID(t *testing.T) {
	t.Parallel()
	svc := newService(t)

	// This uuid is well-formed, but it matches no row in the fresh test
	// database.
	_, err := svc.Get(t.Context(), "0198c0de-0000-7000-8000-000000000000")
	if !errors.Is(err, user.ErrNotFound) {
		t.Errorf("Get = %v, want ErrNotFound", err)
	}
}
