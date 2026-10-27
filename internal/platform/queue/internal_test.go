package queue

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestBackoffGrowsToOneHour(t *testing.T) {
	t.Parallel()
	tests := []struct {
		retries int
		base    time.Duration
	}{
		{-1, 15 * time.Second},
		{0, 15 * time.Second},
		{1, 30 * time.Second},
		{2, time.Minute},
		{7, 32 * time.Minute},
		{8, time.Hour},
		{50, time.Hour},
	}
	for _, tt := range tests {
		for range 20 {
			got := backoff(tt.retries)
			if got < tt.base || got >= tt.base+tt.base/4 {
				t.Fatalf("backoff(%d) = %v, want [%v, %v)", tt.retries, got, tt.base, tt.base+tt.base/4)
			}
		}
	}
}

func TestValidName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		want bool
	}{
		{"auth.delete_expired_sessions", true},
		{"a", true},
		{strings.Repeat("a", 57), true},
		{"", false},
		{strings.Repeat("a", 58), false},
		{"Auth.delete", false},
		{"auth delete", false},
		{"auth-delete", false},
	}
	for _, tt := range tests {
		if got := validName(tt.name); got != tt.want {
			t.Errorf("validName(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestErrorTextIsSafeForPostgres(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "boom", "boom"},
		{"invalid UTF-8", "a\xffb", "a�b"},
		{"NUL", "a\x00b", "ab"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := errorText(errors.New(tt.in)); got != tt.want {
				t.Errorf("errorText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestErrorTextCutsOnARuneBoundary(t *testing.T) {
	t.Parallel()
	// "ż" has 2 bytes, so byte 2000 falls inside the last kept rune.
	got := errorText(errors.New("x" + strings.Repeat("ż", 2000)))
	if len(got) > maxErrorBytes {
		t.Errorf("len = %d, want at most %d", len(got), maxErrorBytes)
	}
	if !utf8.ValidString(got) {
		t.Errorf("errorText returned invalid UTF-8")
	}
	if len(got) < maxErrorBytes-utf8.UTFMax {
		t.Errorf("len = %d, want close to %d", len(got), maxErrorBytes)
	}
}
