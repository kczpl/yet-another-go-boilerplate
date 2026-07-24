package config_test

import (
	"strings"
	"testing"

	"github.com/kczpl/yet-another-go-boilerplate/config"
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
	if err == nil {
		t.Fatal("Validate = nil, want error for bare production config")
	}
	for _, want := range []string{"DATABASE_URL", "API_KEY"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}

	cfg = config.Load(getenvFrom(map[string]string{
		"ENVIRONMENT":  "production",
		"DATABASE_URL": "postgres://user:pass@db:5432/app",
		"API_KEY":      "secret",
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
