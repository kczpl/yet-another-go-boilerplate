package chat_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/kczpl/yet-another-go-boilerplate/internal/chat"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/llm"
)

// recorder keeps the last request and answers with a fixed text.
type recorder struct {
	got         llm.Request
	hasDeadline bool
}

func (r *recorder) Stream(ctx context.Context, req llm.Request, onText func(string) error) (llm.Response, error) {
	r.got = req
	_, r.hasDeadline = ctx.Deadline()
	if err := onText("ok"); err != nil {
		return llm.Response{}, err
	}
	return llm.Response{Text: "ok", StopReason: llm.StopEnd}, nil
}

func TestQuestionValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		question string
		want     string
	}{
		{"empty", "", "question must not be empty"},
		{"blank", " \n\t ", "question must not be empty"},
		{"too long", strings.Repeat("a", 2001), "question must be at most 2000 characters"},
		{"invalid utf-8", "a\xffb", "question must contain valid text"},
		{"nul byte", "a\x00b", "question must contain valid text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			model := &recorder{}
			svc := chat.NewService(model, "test/model")
			_, err := svc.Stream(t.Context(), tt.question, func(string) error { return nil })
			if vErr, ok := errors.AsType[chat.ValidationError](err); !ok || vErr.Error() != tt.want {
				t.Fatalf("err = %v, want ValidationError %q", err, tt.want)
			}
			if model.got.Model != "" {
				t.Fatal("the model ran for invalid input")
			}
		})
	}
}

func TestQuestionAtLengthLimit(t *testing.T) {
	t.Parallel()
	// The limit counts characters, not bytes: 2000 two-byte runes pass.
	svc := chat.NewService(&recorder{}, "test/model")
	if _, err := svc.Answer(t.Context(), strings.Repeat("é", 2000)); err != nil {
		t.Fatalf("Answer: %v", err)
	}
}

func TestStreamBuildsRequest(t *testing.T) {
	t.Parallel()
	model := &recorder{}
	svc := chat.NewService(model, "test/model")

	var parts []string
	resp, err := svc.Stream(t.Context(), "  What is SSE?  ", func(text string) error {
		parts = append(parts, text)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Text != "ok" || len(parts) != 1 {
		t.Fatalf("resp = %+v, parts = %q", resp, parts)
	}

	req := model.got
	if req.Model != "test/model" {
		t.Errorf("Model = %q, want test/model", req.Model)
	}
	if req.System == "" {
		t.Error("System is empty, want the system prompt")
	}
	if req.MaxTokens <= 0 {
		t.Errorf("MaxTokens = %d, want a positive cap", req.MaxTokens)
	}
	want := []llm.Message{{Role: llm.RoleUser, Text: "What is SSE?"}}
	if !slices.Equal(req.Messages, want) {
		t.Errorf("Messages = %+v, want %+v", req.Messages, want)
	}
}

func TestModelCallHasDeadline(t *testing.T) {
	t.Parallel()
	// t.Context has no deadline, so the deadline comes from the service.
	tests := []struct {
		name string
		ask  func(context.Context, *chat.Service) error
	}{
		{"stream", func(ctx context.Context, svc *chat.Service) error {
			_, err := svc.Stream(ctx, "hello", func(string) error { return nil })
			return err
		}},
		{"answer", func(ctx context.Context, svc *chat.Service) error {
			_, err := svc.Answer(ctx, "hello")
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			model := &recorder{}
			if err := tt.ask(t.Context(), chat.NewService(model, "test/model")); err != nil {
				t.Fatalf("ask: %v", err)
			}
			if !model.hasDeadline {
				t.Fatal("the model call has no deadline")
			}
		})
	}
}

func TestAnswerWithFakeModel(t *testing.T) {
	t.Parallel()
	models := llm.NewRouter(map[string]llm.Provider{"fake": llm.Fake{}})
	svc := chat.NewService(models, "fake/echo")

	resp, err := svc.Answer(t.Context(), "hello world")
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if resp.Text != "hello world" || resp.StopReason != llm.StopEnd {
		t.Fatalf("resp = %+v, want the echo with StopEnd", resp)
	}
}
