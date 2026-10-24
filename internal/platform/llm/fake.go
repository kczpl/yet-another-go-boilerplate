package llm

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Fake is a provider for development and tests. It makes no network call
// and needs no credentials. Its model "echo" streams the last user message
// back, one word at a time. Each word counts as one token.
type Fake struct {
	// Delay is the pause before each word. It makes the stream visible in
	// a browser. Zero means no pause.
	Delay time.Duration
}

var _ Provider = Fake{}

// Stream implements Provider.
func (f Fake) Stream(ctx context.Context, req Request, onText func(string) error) (Response, error) {
	if req.Model != "echo" {
		return Response{}, fmt.Errorf("fake: unknown model %q", req.Model)
	}
	// A real provider needs a cap too. The check makes a feature that
	// forgets MaxTokens fail in its tests.
	if req.MaxTokens <= 0 {
		return Response{}, fmt.Errorf("fake: MaxTokens must be positive, got %d", req.MaxTokens)
	}
	question, err := lastUserText(req.Messages)
	if err != nil {
		return Response{}, err
	}
	// SplitAfter keeps the spaces, so the words join to the exact text.
	// A trailing space gives an empty last element.
	words := slices.DeleteFunc(strings.SplitAfter(question, " "), func(w string) bool { return w == "" })
	resp := Response{
		StopReason: StopEnd,
		Usage:      Usage{InputTokens: countWords(req)},
	}
	if len(words) > req.MaxTokens {
		words = words[:req.MaxTokens]
		resp.StopReason = StopMaxTokens
	}

	var answer strings.Builder
	for _, word := range words {
		if err := f.wait(ctx); err != nil {
			return Response{}, err
		}
		if err := onText(word); err != nil {
			return Response{}, err
		}
		answer.WriteString(word)
		resp.Usage.OutputTokens++
	}
	resp.Text = answer.String()
	return resp, nil
}

// wait pauses for f.Delay. It returns early with the context error when
// ctx ends.
func (f Fake) wait(ctx context.Context) error {
	if f.Delay <= 0 {
		return ctx.Err()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(f.Delay):
		return nil
	}
}

// lastUserText returns the text of the last user message.
func lastUserText(messages []Message) (string, error) {
	for _, message := range slices.Backward(messages) {
		if message.Role == RoleUser {
			return message.Text, nil
		}
	}
	return "", errors.New("fake: the request has no user message")
}

// countWords counts the words of the system prompt and of all messages.
func countWords(req Request) int {
	n := len(strings.Fields(req.System))
	for _, m := range req.Messages {
		n += len(strings.Fields(m.Text))
	}
	return n
}
