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

	// logLevelInput keeps the raw LOG_LEVEL value for Validate. A typo
	// must stop startup, not silently mean info.
	logLevelInput string
}

// Load reads the configuration from getenv (usually os.Getenv) and applies
// the development defaults. Tests pass their own getenv.
func Load(getenv func(string) string) Config {
	cfg := Config{
		Env:           cmp.Or(getenv("ENVIRONMENT"), "development"),
		LogLevel:      parseLogLevel(getenv("LOG_LEVEL")),
		Host:          cmp.Or(getenv("HOST"), "localhost"),
		Port:          cmp.Or(getenv("PORT"), "8080"),
		DatabaseURL:   getenv("DATABASE_URL"),
		logLevelInput: getenv("LOG_LEVEL"),
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
	if c.logLevelInput != "" {
		var level slog.Level
		if err := level.UnmarshalText([]byte(c.logLevelInput)); err != nil {
			errs = append(errs, fmt.Errorf("LOG_LEVEL must be debug, info, warn, or error, got %q", c.logLevelInput))
		}
	}
	return errors.Join(errs...)
}

func (c Config) Addr() string { return net.JoinHostPort(c.Host, c.Port) }

func (c Config) Development() bool { return c.Env == "development" }

// parseLogLevel falls back to info on bad input; Validate reports the bad
// input as an error, so the fallback never hides a typo.
func parseLogLevel(s string) slog.Level {
	var level slog.Level
	if err := level.UnmarshalText([]byte(s)); err != nil {
		return slog.LevelInfo
	}
	return level
}
