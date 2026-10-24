package config_test

import (
	"maps"
	"strings"
	"testing"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/config"
)

func getenvFrom(env map[string]string) func(string) string {
	return func(key string) string { return env[key] }
}

// devEnv returns a development environment plus the extra variables.
func devEnv(extra map[string]string) func(string) string {
	env := map[string]string{"ENVIRONMENT": "development"}
	maps.Copy(env, extra)
	return getenvFrom(env)
}

func TestLoadDefaultsToProduction(t *testing.T) {
	t.Parallel()
	cfg := config.Load(getenvFrom(nil))

	if cfg.Env != "production" {
		t.Errorf("Env = %q, want production — a missing ENVIRONMENT must fail closed", cfg.Env)
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("Validate = %v, want a DATABASE_URL error for an empty environment", err)
	}
}

func TestLoadDevelopmentDefaults(t *testing.T) {
	t.Parallel()
	cfg := config.Load(devEnv(nil))

	if cfg.DatabaseURL == "" {
		t.Error("DatabaseURL default missing in development")
	}
	if cfg.Addr() != "localhost:8080" {
		t.Errorf("Addr = %q, want localhost:8080", cfg.Addr())
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
		{"offset level fails validation", "INFO+1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Load(devEnv(map[string]string{"LOG_LEVEL": tt.value}))
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

func TestValidatePort(t *testing.T) {
	t.Parallel()
	for _, port := range []string{"abc", "0", "-1", "65536"} {
		t.Run(port, func(t *testing.T) {
			cfg := config.Load(devEnv(map[string]string{"PORT": port}))
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "PORT") {
				t.Fatalf("Validate = %v, want a PORT error", err)
			}
		})
	}
}

func TestValidateDebugAddr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		addr    string
		wantErr bool
	}{
		{"", false},
		{"localhost:6060", false},
		{"127.0.0.1:6060", false},
		{"[::1]:6060", false},
		{"0.0.0.0:6060", true},
		{":6060", true},
		{"10.0.0.5:6060", true},
		{"localhost", true},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			cfg := config.Load(devEnv(map[string]string{"DEBUG_ADDR": tt.addr}))
			err := cfg.Validate()
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "DEBUG_ADDR") {
					t.Fatalf("Validate = %v, want a DEBUG_ADDR error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate: %v, want nil", err)
			}
		})
	}
}

func TestValidateModels(t *testing.T) {
	t.Parallel()

	cfg := config.Load(devEnv(nil))
	if cfg.LLMModel != "fake/echo" || cfg.SystemOneModel != "fake/uniform" {
		t.Errorf("defaults = %q, %q, want fake/echo, fake/uniform", cfg.LLMModel, cfg.SystemOneModel)
	}
	tests := []struct {
		model   string
		wantErr bool
	}{
		{"anthropic/claude-opus-5-5", false},
		{"local/meta-llama/Llama-3.3-70B", false},
		{"claude-opus-5-5", true},
		{"/echo", true},
		{"fake/", true},
	}
	for _, key := range []string{"LLM_MODEL", "SYSTEMONE_MODEL"} {
		for _, tt := range tests {
			t.Run(key+"="+tt.model, func(t *testing.T) {
				cfg := config.Load(devEnv(map[string]string{key: tt.model}))
				err := cfg.Validate()
				if tt.wantErr {
					if err == nil || !strings.Contains(err.Error(), key) {
						t.Fatalf("Validate = %v, want a %s error", err, key)
					}
					return
				}
				if err != nil {
					t.Fatalf("Validate: %v, want nil", err)
				}
			})
		}
	}
}
