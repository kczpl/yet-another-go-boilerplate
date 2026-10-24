// Package config loads application configuration from the environment.
package config

import (
	"cmp"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// Config holds the settings of one process. Load reads them from the
// environment. Validate rejects bad values before the service starts.
type Config struct {
	// Env is one of: development, staging, production. An unset
	// ENVIRONMENT means production, so a missing variable fails closed.
	Env         string
	LogLevel    slog.Level
	Host        string
	Port        string
	DatabaseURL string

	// ClientIPHeader names the header in which a trusted reverse proxy
	// sends the client address, for example X-Forwarded-For. The login
	// rate limit reads the last address in it. Leave it empty when no
	// proxy sets the header, because clients can forge it.
	ClientIPHeader string

	// DebugAddr is the loopback address of the optional pprof and expvar
	// listener, for example localhost:6060. Empty disables the listener.
	DebugAddr string

	// LLMModel is the default text model of the app as "provider/model",
	// for example "anthropic/claude-opus-5-5". The default "fake/echo"
	// needs no network and no credentials. internal/app/models.go registers
	// the providers.
	LLMModel string

	// SystemOneModel is the default System One model of the app as
	// "provider/model", for example "typesafe/jev-latest". The default
	// "fake/uniform" needs no network and no credentials.
	// internal/app/models.go registers the providers.
	SystemOneModel string

	// The API keys of the model providers. internal/app/models.go gives
	// each key only to the constructor of its provider and registers the
	// provider only when the key is set. Never log a key.
	AnthropicAPIKey string
	MistralAPIKey   string
	XAIAPIKey       string
	TypeSafeAPIKey  string

	// BedrockAPIKey and BedrockRegion select Claude on Amazon Bedrock. The
	// key is a Bedrock API key (AWS_BEARER_TOKEN_BEDROCK in the AWS
	// documentation). The region selects the endpoint, for example
	// us-east-1.
	BedrockAPIKey string
	BedrockRegion string

	// LocalLLMURL is the address of a local server with the Chat
	// Completions API, for example http://localhost:11434/v1 for Ollama.
	// A local server needs no key.
	LocalLLMURL string

	// logLevelInput keeps the raw LOG_LEVEL value for Validate. A typo
	// must stop startup, not silently mean info.
	logLevelInput string
}

// Load reads the configuration from getenv (usually os.Getenv). Tests pass
// their own getenv. Only development gets a default DATABASE_URL.
func Load(getenv func(string) string) Config {
	cfg := Config{
		Env:             cmp.Or(getenv("ENVIRONMENT"), "production"),
		LogLevel:        parseLogLevel(getenv("LOG_LEVEL")),
		Host:            cmp.Or(getenv("HOST"), "localhost"),
		Port:            cmp.Or(getenv("PORT"), "8080"),
		DatabaseURL:     getenv("DATABASE_URL"),
		ClientIPHeader:  getenv("CLIENT_IP_HEADER"),
		DebugAddr:       getenv("DEBUG_ADDR"),
		LLMModel:        cmp.Or(getenv("LLM_MODEL"), "fake/echo"),
		SystemOneModel:  cmp.Or(getenv("SYSTEMONE_MODEL"), "fake/uniform"),
		AnthropicAPIKey: getenv("ANTHROPIC_API_KEY"),
		MistralAPIKey:   getenv("MISTRAL_API_KEY"),
		XAIAPIKey:       getenv("XAI_API_KEY"),
		TypeSafeAPIKey:  getenv("TYPESAFE_API_KEY"),
		BedrockAPIKey:   getenv("BEDROCK_API_KEY"),
		BedrockRegion:   getenv("BEDROCK_REGION"),
		LocalLLMURL:     getenv("LOCAL_LLM_URL"),
		logLevelInput:   getenv("LOG_LEVEL"),
	}
	// A development convenience only. Validate keeps the other
	// environments explicit.
	if cfg.Development() {
		cfg.DatabaseURL = cmp.Or(cfg.DatabaseURL, "postgres://app:app@localhost:5432/app")
	}
	return cfg
}

// Validate collects hard misconfiguration. The service must refuse to
// start with a bad value. It never falls back to a guess.
func (c Config) Validate() error {
	var errs []error
	switch c.Env {
	case "development", "staging", "production":
	default:
		errs = append(errs, fmt.Errorf("ENVIRONMENT must be development, staging, or production, got %q", c.Env))
	}
	if !c.Development() && c.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required outside development (set ENVIRONMENT=development for local defaults)"))
	}
	switch strings.ToLower(c.logLevelInput) {
	case "", "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("LOG_LEVEL must be debug, info, warn, or error, got %q", c.logLevelInput))
	}
	port, err := strconv.Atoi(c.Port)
	if err != nil || port < 1 || port > 65535 {
		errs = append(errs, errors.New("PORT must be an integer from 1 to 65535"))
	}
	if c.DebugAddr != "" && !isLoopback(c.DebugAddr) {
		errs = append(errs, fmt.Errorf("DEBUG_ADDR must be a loopback host:port such as localhost:6060, got %q", c.DebugAddr))
	}
	errs = append(errs, c.validateModels()...)
	return errors.Join(errs...)
}

// validateModels checks the settings of the model providers.
func (c Config) validateModels() []error {
	var errs []error
	if !isModelRef(c.LLMModel) {
		errs = append(errs, fmt.Errorf("LLM_MODEL must have the form provider/model, got %q", c.LLMModel))
	}
	if !isModelRef(c.SystemOneModel) {
		errs = append(errs, fmt.Errorf("SYSTEMONE_MODEL must have the form provider/model, got %q", c.SystemOneModel))
	}
	if c.BedrockAPIKey != "" && !isRegion(c.BedrockRegion) {
		errs = append(errs, fmt.Errorf("BEDROCK_REGION must be an AWS region such as us-east-1 when BEDROCK_API_KEY is set, got %q", c.BedrockRegion))
	}
	// The URL can hold a password, so the error does not repeat it.
	if c.LocalLLMURL != "" && !isHTTPURL(c.LocalLLMURL) {
		errs = append(errs, errors.New("LOCAL_LLM_URL must be an http or https URL such as http://localhost:11434/v1"))
	}
	return errs
}

// Addr returns the host:port address of the HTTP server.
func (c Config) Addr() string { return net.JoinHostPort(c.Host, c.Port) }

// Development reports whether the process runs in the development
// environment. Development relaxes the cookie and transport security.
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

// isLoopback reports whether addr is a host:port on the loopback
// interface. The debug listener exposes profiles and memory statistics, so
// it must never listen on a public interface.
func isLoopback(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

// isRegion reports whether s has the form of an AWS region, for example
// us-east-1 or us-gov-west-1. The region becomes a part of a host name, so
// the check allows only lowercase letters, dashes, and a final number.
func isRegion(s string) bool {
	parts := strings.Split(s, "-")
	if len(parts) < 3 {
		return false
	}
	last := len(parts) - 1
	for _, part := range parts[:last] {
		if part == "" || strings.Trim(part, "abcdefghijklmnopqrstuvwxyz") != "" {
			return false
		}
	}
	return parts[last] != "" && strings.Trim(parts[last], "0123456789") == ""
}

// isHTTPURL reports whether s is an absolute http or https URL with a host.
func isHTTPURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// isModelRef reports whether ref has the form "provider/model" with two
// parts that are not empty.
func isModelRef(ref string) bool {
	provider, model, ok := strings.Cut(ref, "/")
	return ok && provider != "" && model != ""
}
