package app

import (
	"errors"
	"fmt"
	"time"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/config"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/llm"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/llm/anthropic"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/systemone"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/systemone/typesafe"
)

// fakeDelay slows the fake model in development, so the stream is visible
// in a browser. Tests use the production config and get no delay.
const fakeDelay = 40 * time.Millisecond

// Models holds the two kinds of model that the app can call. They serve
// different purposes, so each kind has its own providers and its own
// default model:
//
//   - LLM writes text: an answer, a summary, a draft. LLM_MODEL selects
//     the default model.
//   - SystemOne judges a state: it picks an option, gives a score, or
//     checks a statement, with calibrated probabilities. SYSTEMONE_MODEL
//     selects the default model.
//
// A feature takes only the providers that it calls: llm.Provider,
// systemone.Provider, or both. It never takes Models.
type Models struct {
	LLM       llm.Provider
	SystemOne systemone.Provider
}

// NewModels builds the model routers from the configuration. It returns an
// error when no registered provider serves cfg.LLMModel or
// cfg.SystemOneModel, so a bad configuration stops startup.
func NewModels(cfg config.Config) (Models, error) {
	llms, llmErr := newLLM(cfg)
	systemOne, systemOneErr := newSystemOne(cfg)
	if err := errors.Join(llmErr, systemOneErr); err != nil {
		return Models{}, err
	}
	return Models{LLM: llms, SystemOne: systemOne}, nil
}

// newLLM builds the router of the text models. Register each LLM provider
// here, under the name that LLM_MODEL uses before the slash.
func newLLM(cfg config.Config) (*llm.Router, error) {
	fake := llm.Fake{}
	if cfg.Development() {
		fake.Delay = fakeDelay
	}
	providers := map[string]llm.Provider{"fake": fake}
	// Register a real provider only when its key is set. Without the key,
	// a model of the provider stops the startup below.
	if cfg.AnthropicAPIKey != "" {
		providers["anthropic"] = anthropic.New(anthropic.BaseURL, cfg.AnthropicAPIKey)
	}
	router := llm.NewRouter(providers)
	if _, _, err := router.Resolve(cfg.LLMModel); err != nil {
		return nil, fmt.Errorf("LLM_MODEL: %w (set the key of the provider in .env, for example ANTHROPIC_API_KEY, or register the provider in internal/app/models.go)", err)
	}
	return router, nil
}

// newSystemOne builds the router of the System One models. Register each
// System One provider here, under the name that SYSTEMONE_MODEL uses
// before the slash.
func newSystemOne(cfg config.Config) (*systemone.Router, error) {
	providers := map[string]systemone.Provider{"fake": systemone.Fake{}}
	// Register a real provider only when its key is set. Without the key,
	// a model of the provider stops the startup below.
	if cfg.TypeSafeAPIKey != "" {
		providers["typesafe"] = typesafe.New(typesafe.BaseURL, cfg.TypeSafeAPIKey)
	}
	router := systemone.NewRouter(providers)
	if _, _, err := router.Resolve(cfg.SystemOneModel); err != nil {
		return nil, fmt.Errorf("SYSTEMONE_MODEL: %w (set the key of the provider in .env, for example TYPESAFE_API_KEY, or register the provider in internal/app/models.go)", err)
	}
	return router, nil
}
