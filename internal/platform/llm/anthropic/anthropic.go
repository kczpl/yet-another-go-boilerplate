// Package anthropic is the llm provider for the Claude models of
// Anthropic. It calls the Messages API with net/http, so it adds no
// module. Claude on Amazon Bedrock serves the same API under another base
// URL, so the package serves Bedrock too.
package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/llm"
)

const (
	// BaseURL is the address of the Anthropic API.
	BaseURL = "https://api.anthropic.com/"

	// apiVersion is the version of the Messages API that the package
	// uses. The anthropic-version header keeps the format of the API
	// stable.
	apiVersion = "2023-06-01"

	// maxLineBytes caps one line of the stream.
	maxLineBytes = 1 << 20
)

// Provider sends requests to the Messages API. It does not retry. The
// feature sets the deadline of each call through ctx.
type Provider struct {
	endpoint string
	apiKey   string
}

var _ llm.Provider = (*Provider)(nil)

// New returns a Provider that sends requests to baseURL with apiKey.
// baseURL ends before /v1/messages: BaseURL for Anthropic, or
// https://bedrock-runtime.<region>.amazonaws.com/anthropic/ with a
// Bedrock API key for Bedrock.
func New(baseURL, apiKey string) *Provider {
	return &Provider{
		endpoint: strings.TrimSuffix(baseURL, "/") + "/v1/messages",
		apiKey:   apiKey,
	}
}

// The wire types of the API.
type (
	apiRequest struct {
		Model     string       `json:"model"`
		MaxTokens int          `json:"max_tokens"`
		System    string       `json:"system,omitempty"`
		Messages  []apiMessage `json:"messages"`
		Stream    bool         `json:"stream"`
	}
	apiMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	// apiEvent has the fields of all the stream events that the package
	// reads. Each event type fills only some of them.
	apiEvent struct {
		Type    string `json:"type"`
		Message struct {
			Usage apiUsage `json:"usage"`
		} `json:"message"`
		Delta struct {
			Type       string `json:"type"`
			Text       string `json:"text"`
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
		Usage apiUsage `json:"usage"`
	}
	apiUsage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	}
)

// Stream implements llm.Provider. It sends only the text of the answer to
// onText. Thinking blocks do not reach the user.
func (p *Provider) Stream(ctx context.Context, req llm.Request, onText func(string) error) (llm.Response, error) {
	body, err := newRequestBody(req)
	if err != nil {
		return llm.Response{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return llm.Response{}, fmt.Errorf("anthropic: %w", err)
	}
	httpReq.Header.Set("X-Api-Key", p.apiKey)
	httpReq.Header.Set("Anthropic-Version", apiVersion)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return llm.Response{}, fmt.Errorf("anthropic: %w", err)
	}
	defer resp.Body.Close()
	// The error body can repeat the request text, so the error has only
	// the status.
	if resp.StatusCode != http.StatusOK {
		return llm.Response{}, fmt.Errorf("anthropic: status %d", resp.StatusCode)
	}
	return readStream(resp.Body, onText)
}

// newRequestBody maps req to the JSON body of the API.
func newRequestBody(req llm.Request) ([]byte, error) {
	out := apiRequest{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		System:    req.System,
		Messages:  make([]apiMessage, 0, len(req.Messages)),
		Stream:    true,
	}
	for _, m := range req.Messages {
		switch m.Role {
		case llm.RoleUser, llm.RoleAssistant:
			out.Messages = append(out.Messages, apiMessage{Role: string(m.Role), Content: m.Text})
		default:
			return nil, fmt.Errorf("anthropic: unknown role %q", m.Role)
		}
	}
	return json.Marshal(out)
}

// readStream reads the data lines of the stream until the message_stop
// event or the end of the body. It ignores the other lines of the event
// stream, because each data line repeats the event type.
func readStream(r io.Reader, onText func(string) error) (llm.Response, error) {
	var s stream
	scanner := bufio.NewScanner(r)
	scanner.Buffer(nil, maxLineBytes)
	for !s.done && scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data:")
		if !ok {
			continue
		}
		if err := s.add([]byte(strings.TrimPrefix(data, " ")), onText); err != nil {
			return llm.Response{}, err
		}
	}
	if err := scanner.Err(); err != nil {
		return llm.Response{}, fmt.Errorf("anthropic: reading the stream: %w", err)
	}
	return s.response()
}

// stream collects the answer from the events of one stream.
type stream struct {
	text  strings.Builder
	stop  string
	usage llm.Usage
	done  bool
}

// add reads one event and sends its text to onText. It ignores the event
// types and the delta types that it does not know, for example ping and
// thinking_delta.
func (s *stream) add(data []byte, onText func(string) error) error {
	var event apiEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("anthropic: reading an event: %w", err)
	}
	switch event.Type {
	case "message_start":
		s.usage = llm.Usage{InputTokens: event.Message.Usage.InputTokens, OutputTokens: event.Message.Usage.OutputTokens}
	case "content_block_delta":
		if event.Delta.Type != "text_delta" || event.Delta.Text == "" {
			return nil
		}
		s.text.WriteString(event.Delta.Text)
		return onText(event.Delta.Text)
	case "message_delta":
		// The counts of message_delta are totals, not increments.
		s.stop = event.Delta.StopReason
		s.usage.OutputTokens = event.Usage.OutputTokens
		s.usage.InputTokens = max(s.usage.InputTokens, event.Usage.InputTokens)
	case "message_stop":
		s.done = true
	case "error":
		// The message of an error event can repeat the request text, so
		// the error does not include it.
		return errors.New("anthropic: the server sent an error event")
	}
	return nil
}

// response returns the complete answer.
func (s *stream) response() (llm.Response, error) {
	stop, err := stopReason(s.stop)
	if err != nil {
		return llm.Response{}, err
	}
	return llm.Response{Text: s.text.String(), StopReason: stop, Usage: s.usage}, nil
}

// stopReason maps the stop reason of the API to llm.StopReason. The
// request has no tools, so a tool stop is an error.
func stopReason(reason string) (llm.StopReason, error) {
	switch reason {
	case "end_turn", "stop_sequence":
		return llm.StopEnd, nil
	case "max_tokens", "model_context_window_exceeded":
		return llm.StopMaxTokens, nil
	case "refusal":
		return llm.StopRefusal, nil
	case "":
		return "", errors.New("anthropic: the stream ended without a stop reason")
	default:
		return "", fmt.Errorf("anthropic: unexpected stop reason %q", reason)
	}
}
