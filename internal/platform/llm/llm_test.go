package llm_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/llm"
)

// recorder is a provider that records the request it gets.
type recorder struct {
	got llm.Request
}

func (r *recorder) Stream(_ context.Context, req llm.Request, _ func(string) error) (llm.Response, error) {
	r.got = req
	return llm.Response{Text: "ok", StopReason: llm.StopEnd}, nil
}

func ask(text string) llm.Request {
	return llm.Request{
		Model:     "echo",
		Messages:  []llm.Message{{Role: llm.RoleUser, Text: text}},
		MaxTokens: 100,
	}
}

func TestRouterSendsBareModelToProvider(t *testing.T) {
	t.Parallel()
	local := &recorder{}
	router := llm.NewRouter(map[string]llm.Provider{"local": local})

	req := ask("hi")
	req.Model = "local/meta-llama/Llama-3.3-70B"
	if _, err := llm.Generate(t.Context(), router, req); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if local.got.Model != "meta-llama/Llama-3.3-70B" {
		t.Errorf("provider got model %q, want the part after the first slash", local.got.Model)
	}
}

func TestRouterRejectsBadReferences(t *testing.T) {
	t.Parallel()
	router := llm.NewRouter(map[string]llm.Provider{"fake": llm.Fake{}})

	tests := []struct {
		ref         string
		wantUnknown bool
	}{
		{"echo", false},
		{"/echo", false},
		{"fake/", false},
		{"", false},
		{"anthropic/claude-opus-5-5", true},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			_, _, err := router.Resolve(tt.ref)
			if err == nil {
				t.Fatal("Resolve accepted a bad reference")
			}
			if got := errors.Is(err, llm.ErrUnknownProvider); got != tt.wantUnknown {
				t.Errorf("errors.Is(err, ErrUnknownProvider) = %v, want %v (err: %v)", got, tt.wantUnknown, err)
			}
		})
	}
}

func TestFakeEchoStreamsWords(t *testing.T) {
	t.Parallel()
	var parts []string
	resp, err := llm.Fake{}.Stream(t.Context(), ask("hello  streaming world "), func(text string) error {
		parts = append(parts, text)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if want := []string{"hello ", " ", "streaming ", "world "}; !slices.Equal(parts, want) {
		t.Errorf("parts = %q, want %q", parts, want)
	}
	want := llm.Response{
		Text:       "hello  streaming world ",
		StopReason: llm.StopEnd,
		Usage:      llm.Usage{InputTokens: 3, OutputTokens: 4},
	}
	if resp != want {
		t.Errorf("response = %+v, want %+v", resp, want)
	}
}

func TestFakeStopsAtMaxTokens(t *testing.T) {
	t.Parallel()
	req := ask("one two three")
	req.MaxTokens = 2
	resp, err := llm.Generate(t.Context(), llm.Fake{}, req)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Text != "one two " || resp.StopReason != llm.StopMaxTokens {
		t.Errorf("response = %+v, want two words and StopMaxTokens", resp)
	}
}

func TestFakeStopsWhenCallbackFails(t *testing.T) {
	t.Parallel()
	errGone := errors.New("client gone")
	calls := 0
	_, err := llm.Fake{}.Stream(t.Context(), ask("a b c"), func(string) error {
		calls++
		return errGone
	})
	if !errors.Is(err, errGone) || calls != 1 {
		t.Errorf("err = %v after %d calls, want errGone after 1 call", err, calls)
	}
}

func TestFakeStopsWhenContextEnds(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	// The delay is long, so only the canceled context can end the call.
	fake := llm.Fake{Delay: time.Hour}
	calls := 0
	_, err := fake.Stream(ctx, ask("a b c"), func(string) error {
		calls++
		return nil
	})
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Errorf("err = %v after %d calls, want context.Canceled before the first word", err, calls)
	}
}

func TestFakeRejectsBadRequests(t *testing.T) {
	t.Parallel()
	unknown := ask("hi")
	unknown.Model = "parrot"
	noUser := ask("hi")
	noUser.Messages[0].Role = llm.RoleAssistant
	noCap := ask("hi")
	noCap.MaxTokens = 0

	for name, req := range map[string]llm.Request{"unknown model": unknown, "no user message": noUser, "no MaxTokens": noCap} {
		if _, err := llm.Generate(t.Context(), llm.Fake{}, req); err == nil {
			t.Errorf("%s: Generate accepted the request", name)
		}
	}
}
