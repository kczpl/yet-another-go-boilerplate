// Package config loads application configuration from the environment.
package config

import (
	"cmp"
	"errors"
	"fmt"
	"log/slog"
	"net"
)

type Config struct {
	// Env is one of: development, staging, production.
	Env         string
	LogLevel    slog.Level
	Host        string
	Port        string
	DatabaseURL string
	// APIKey is the static bearer token protecting /api/v1 routes.
	// See auth.RequireAPIKey.
	APIKey string
}

// Load reads configuration from getenv (usually os.Getenv), applying
// development defaults for anything unset. Tests pass their own getenv.
func Load(getenv func(string) string) Config {
	cfg := Config{
		Env:         cmp.Or(getenv("ENVIRONMENT"), "development"),
		LogLevel:    parseLogLevel(getenv("LOG_LEVEL")),
		Host:        cmp.Or(getenv("HOST"), "localhost"),
		Port:        cmp.Or(getenv("PORT"), "8080"),
		DatabaseURL: getenv("DATABASE_URL"),
		APIKey:      getenv("API_KEY"),
	}
	// Development convenience only — staging/production must be explicit,
	// enforced by Validate.
	if cfg.Development() {
		cfg.DatabaseURL = cmp.Or(cfg.DatabaseURL, "postgres://app:app@localhost:5432/app")
	}
	return cfg
}

// Validate reports hard misconfiguration, all problems at once. Development
// runs on defaults; anywhere else it is better to refuse to start than to
// come up half-working (an unset API_KEY rejects every request, an unset
// DATABASE_URL would silently point at localhost).
func (c Config) Validate() error {
	var errs []error
	switch c.Env {
	case "development", "staging", "production":
	default:
		errs = append(errs, fmt.Errorf("ENVIRONMENT must be development, staging, or production, got %q", c.Env))
	}
	if !c.Development() {
		if c.DatabaseURL == "" {
			errs = append(errs, errors.New("DATABASE_URL is required outside development"))
		}
		if c.APIKey == "" {
			errs = append(errs, errors.New("API_KEY is required outside development"))
		}
	}
	return errors.Join(errs...)
}

func (c Config) Addr() string { return net.JoinHostPort(c.Host, c.Port) }

func (c Config) Development() bool { return c.Env == "development" }

func parseLogLevel(s string) slog.Level {
	var level slog.Level
	if err := level.UnmarshalText([]byte(s)); err != nil {
		return slog.LevelInfo
	}
	return level
}
