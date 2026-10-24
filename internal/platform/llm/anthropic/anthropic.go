// Package anthropic is the llm provider for the Claude models of
// Anthropic. It uses the official Go SDK. No other package imports the
// SDK.
package anthropic

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/llm"
)

// BaseURL is the address of the Anthropic API.
const BaseURL = "https://api.anthropic.com/"

// Provider sends requests to the Messages API. The SDK retries a rate
// limit and a server error two times before it returns the error.
type Provider struct {
	client anthropic.Client
}

var _ llm.Provider = (*Provider)(nil)

// New returns a Provider that sends requests to baseURL with apiKey. By
// default, the SDK reads ANTHROPIC_* environment variables. New turns that
// off, because only config.Load reads the environment.
func New(baseURL, apiKey string) *Provider {
	return &Provider{client: anthropic.NewClient(
		option.WithoutEnvironmentDefaults(),
		option.WithBaseURL(baseURL),
		option.WithAPIKey(apiKey),
	)}
}

// Stream implements llm.Provider. It sends only the text of the answer to
// onText. Thinking blocks do not reach the user.
func (p *Provider) Stream(ctx context.Context, req llm.Request, onText func(string) error) (llm.Response, error) {
	params, err := newParams(req)
	if err != nil {
		return llm.Response{}, err
	}
	stream := p.client.Messages.NewStreaming(ctx, params)
	defer stream.Close()

	var message anthropic.Message
	for stream.Next() {
		event := stream.Current()
		if err := message.Accumulate(event); err != nil {
			return llm.Response{}, fmt.Errorf("anthropic: reading the stream: %w", err)
		}
		if text, ok := textDelta(event); ok {
			if err := onText(text); err != nil {
				return llm.Response{}, err
			}
		}
	}
	if err := stream.Err(); err != nil {
		return llm.Response{}, fmt.Errorf("anthropic: %w", err)
	}
	return newResponse(message)
}

// newParams maps req to the parameters of the Messages API.
func newParams(req llm.Request) (anthropic.MessageNewParams, error) {
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: int64(req.MaxTokens),
		Messages:  make([]anthropic.MessageParam, 0, len(req.Messages)),
	}
	if req.System != "" {
		params.System = []anthropic.TextBlockParam{{Text: req.System}}
	}
	for _, m := range req.Messages {
		switch m.Role {
		case llm.RoleUser:
			params.Messages = append(params.Messages, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Text)))
		case llm.RoleAssistant:
			params.Messages = append(params.Messages, anthropic.NewAssistantMessage(anthropic.NewTextBlock(m.Text)))
		default:
			return anthropic.MessageNewParams{}, fmt.Errorf("anthropic: unknown role %q", m.Role)
		}
	}
	return params, nil
}

// textDelta returns the text of event when event adds text to the answer.
func textDelta(event anthropic.MessageStreamEventUnion) (string, bool) {
	block, ok := event.AsAny().(anthropic.ContentBlockDeltaEvent)
	if !ok {
		return "", false
	}
	delta, ok := block.Delta.AsAny().(anthropic.TextDelta)
	return delta.Text, ok
}

// newResponse maps the complete message to llm.Response.
func newResponse(message anthropic.Message) (llm.Response, error) {
	stop, err := stopReason(message.StopReason)
	if err != nil {
		return llm.Response{}, err
	}
	resp := llm.Response{
		StopReason: stop,
		Usage: llm.Usage{
			InputTokens:  int(message.Usage.InputTokens),
			OutputTokens: int(message.Usage.OutputTokens),
		},
	}
	var text strings.Builder
	for _, block := range message.Content {
		if b, ok := block.AsAny().(anthropic.TextBlock); ok {
			text.WriteString(b.Text)
		}
	}
	resp.Text = text.String()
	return resp, nil
}

// stopReason maps the stop reason of the API to llm.StopReason. The
// request has no tools, so a tool stop is an error.
func stopReason(reason anthropic.StopReason) (llm.StopReason, error) {
	switch reason {
	case anthropic.StopReasonEndTurn, anthropic.StopReasonStopSequence:
		return llm.StopEnd, nil
	case anthropic.StopReasonMaxTokens, anthropic.StopReasonModelContextWindowExceeded:
		return llm.StopMaxTokens, nil
	case anthropic.StopReasonRefusal:
		return llm.StopRefusal, nil
	default:
		return "", fmt.Errorf("anthropic: unexpected stop reason %q", reason)
	}
}
