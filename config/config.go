// Package config loads application configuration from the environment.
package config

import (
	"cmp"
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
	return Config{
		Env:         cmp.Or(getenv("ENVIRONMENT"), "development"),
		LogLevel:    parseLogLevel(getenv("LOG_LEVEL")),
		Host:        cmp.Or(getenv("HOST"), "localhost"),
		Port:        cmp.Or(getenv("PORT"), "8080"),
		DatabaseURL: cmp.Or(getenv("DATABASE_URL"), "postgres://app:app@localhost:5432/app"),
		APIKey:      getenv("API_KEY"),
	}
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
