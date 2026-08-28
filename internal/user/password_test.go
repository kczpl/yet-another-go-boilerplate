package user

import (
	"strings"
	"testing"
)

// This is a white-box test (package user). hashPassword and verifyPassword
// are private on purpose. Nothing outside the package handles passwords.

func TestPasswordRoundTrip(t *testing.T) {
	t.Parallel()
	hash, err := hashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}

	if !strings.HasPrefix(hash, "pbkdf2-sha256$600000$") {
		t.Errorf("hash = %q, want pbkdf2-sha256$600000$ prefix", hash)
	}
	if !verifyPassword(hash, "correct horse battery staple") {
		t.Error("verifyPassword rejected the correct password")
	}
	if verifyPassword(hash, "wrong password") {
		t.Error("verifyPassword accepted a wrong password")
	}

	// The password is the same, but the salt is fresh. The hashes must
	// differ.
	hash2, err := hashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	if hash == hash2 {
		t.Error("two hashes of the same password are equal — salt not applied")
	}
}

func TestVerifyPasswordRejectsMalformedHashes(t *testing.T) {
	t.Parallel()
	valid, err := hashPassword("some password")
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}

	tests := []struct {
		name string
		hash string
	}{
		{"empty", ""},
		{"garbage", "not a hash at all"},
		{"wrong scheme", strings.Replace(valid, "pbkdf2-sha256", "bcrypt", 1)},
		{"missing part", strings.Join(strings.Split(valid, "$")[:3], "$")},
		{"bad iterations", strings.Replace(valid, "$600000$", "$abc$", 1)},
		{"zero iterations", strings.Replace(valid, "$600000$", "$0$", 1)},
		{"bad salt encoding", strings.Replace(valid, "$600000$", "$600000$!!!$", 1)},
		{"tampered key", valid[:len(valid)-4] + "AAAA"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if verifyPassword(tt.hash, "some password") {
				t.Errorf("verifyPassword accepted malformed hash %q", tt.hash)
			}
		})
	}
}
