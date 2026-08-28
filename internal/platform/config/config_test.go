package config_test

import (
	"strings"
	"testing"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/config"
)

func getenvFrom(env map[string]string) func(string) string {
	return func(key string) string { return env[key] }
}

func TestLoadDevelopmentDefaults(t *testing.T) {
	t.Parallel()
	cfg := config.Load(getenvFrom(nil))

	if cfg.Env != "development" {
		t.Errorf("Env = %q, want development", cfg.Env)
	}
	if cfg.DatabaseURL == "" {
		t.Error("DatabaseURL default missing in development")
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate: %v, want nil — development runs on defaults", err)
	}
}

func TestValidateProductionRequiresExplicitConfig(t *testing.T) {
	t.Parallel()

	cfg := config.Load(getenvFrom(map[string]string{"ENVIRONMENT": "production"}))
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("Validate = %v, want a DATABASE_URL error for bare production config", err)
	}

	cfg = config.Load(getenvFrom(map[string]string{
		"ENVIRONMENT":  "production",
		"DATABASE_URL": "postgres://user:pass@db:5432/app",
	}))
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate with full config: %v, want nil", err)
	}
}

func TestValidateRejectsUnknownEnvironment(t *testing.T) {
	t.Parallel()

	cfg := config.Load(getenvFrom(map[string]string{"ENVIRONMENT": "prod"}))
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "ENVIRONMENT") {
		t.Fatalf("Validate = %v, want an ENVIRONMENT error for a typo'd env", err)
	}
}

func TestLogLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"empty means info", "", false},
		{"debug", "debug", false},
		{"uppercase warn", "WARN", false},
		{"typo fails validation", "debgu", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Load(getenvFrom(map[string]string{"LOG_LEVEL": tt.value}))
			err := cfg.Validate()
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "LOG_LEVEL") {
					t.Fatalf("Validate = %v, want a LOG_LEVEL error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate: %v, want nil", err)
			}
		})
	}
}
