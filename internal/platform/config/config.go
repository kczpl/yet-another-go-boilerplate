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
}

// Load reads the configuration from getenv (usually os.Getenv) and applies
// the development defaults. Tests pass their own getenv.
func Load(getenv func(string) string) Config {
	cfg := Config{
		Env:         cmp.Or(getenv("ENVIRONMENT"), "development"),
		LogLevel:    parseLogLevel(getenv("LOG_LEVEL")),
		Host:        cmp.Or(getenv("HOST"), "localhost"),
		Port:        cmp.Or(getenv("PORT"), "8080"),
		DatabaseURL: getenv("DATABASE_URL"),
	}
	// A development convenience only. Validate keeps the other
	// environments explicit.
	if cfg.Development() {
		cfg.DatabaseURL = cmp.Or(cfg.DatabaseURL, "postgres://app:app@localhost:5432/app")
	}
	return cfg
}

// Validate collects hard misconfiguration. Outside development the service
// must refuse to start, because an unset DATABASE_URL silently points at
// localhost.
func (c Config) Validate() error {
	var errs []error
	switch c.Env {
	case "development", "staging", "production":
	default:
		errs = append(errs, fmt.Errorf("ENVIRONMENT must be development, staging, or production, got %q", c.Env))
	}
	if !c.Development() && c.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required outside development"))
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
