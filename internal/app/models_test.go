package app_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/kczpl/yet-another-go-boilerplate/internal/app"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/config"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/llm"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/systemone"
)

// TestNewModelsServesTheDefaults calls both kinds of model with their
// default model references, as a feature does.
func TestNewModelsServesTheDefaults(t *testing.T) {
	t.Parallel()
	cfg := config.Load(func(string) string { return "" })
	models, err := app.NewModels(cfg)
	if err != nil {
		t.Fatalf("NewModels: %v", err)
	}

	text, err := llm.Generate(t.Context(), models.LLM, llm.Request{
		Model:     cfg.LLMModel,
		Messages:  []llm.Message{{Role: llm.RoleUser, Text: "hello"}},
		MaxTokens: 10,
	})
	if err != nil || text.Text != "hello" {
		t.Errorf("LLM answer = %+v, %v, want the echo of hello", text, err)
	}

	judged, err := models.SystemOne.Ask(t.Context(), systemone.Request{
		Model: cfg.SystemOneModel,
		State: "The API returns 500 on every request.",
		Questions: map[string]systemone.Question{
			"is_urgent": {Type: systemone.Noul, Instructions: "The issue blocks the customer."},
		},
	})
	if err != nil || judged.Answers["is_urgent"].Type != systemone.Noul {
		t.Errorf("System One answer = %+v, %v, want a noul answer", judged, err)
	}
}

func TestNewModelsNeedsTheKeyOfAProvider(t *testing.T) {
	t.Parallel()
	// The format is valid, but without its key the provider is not
	// registered. The error names the key.
	tests := []struct {
		setting, model, key string
		want                error
	}{
		{"LLM_MODEL", "anthropic/claude-opus-5-5", "ANTHROPIC_API_KEY", llm.ErrUnknownProvider},
		{"SYSTEMONE_MODEL", "typesafe/jev-latest", "TYPESAFE_API_KEY", systemone.ErrUnknownProvider},
	}
	for _, tt := range tests {
		t.Run(tt.setting, func(t *testing.T) {
			t.Parallel()
			env := map[string]string{tt.setting: tt.model}
			_, err := app.NewModels(config.Load(func(k string) string { return env[k] }))
			if !errors.Is(err, tt.want) || !strings.Contains(err.Error(), tt.key) {
				t.Fatalf("NewModels = %v, want %v that names %s", err, tt.want, tt.key)
			}

			// With the key, the provider is registered. NewModels makes no
			// network call, so the test does not call the real API.
			env[tt.key] = "test-key"
			if _, err := app.NewModels(config.Load(func(k string) string { return env[k] })); err != nil {
				t.Fatalf("NewModels with %s: %v", tt.key, err)
			}
		})
	}
}
