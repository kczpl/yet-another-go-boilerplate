// Package chatcompletions is the llm provider for the APIs that use the
// shape of the Chat Completions API: Mistral, xAI, and local servers such
// as Ollama, vLLM, and llama.cpp. The API is JSON over HTTP with a
// Server-Sent Events stream, so the package uses net/http and adds no
// module.
package chatcompletions

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

// maxLineBytes caps one line of the stream.
const maxLineBytes = 1 << 20

// Provider sends requests to the chat completions endpoint of one server.
// It does not retry. The feature sets the deadline of each call through
// ctx.
type Provider struct {
	endpoint string
	apiKey   string

	// StreamUsage asks the server for the token counts of the stream
	// (stream_options.include_usage). Set it for a server that sends the
	// counts only on request, for example xAI or Ollama. Mistral sends
	// them without the option.
	StreamUsage bool
}

var _ llm.Provider = (*Provider)(nil)

// New returns a Provider that sends requests to baseURL with apiKey.
// baseURL ends before /chat/completions, for example
// https://api.mistral.ai/v1. An empty apiKey sends no Authorization
// header, because a local server usually needs no key.
func New(baseURL, apiKey string) *Provider {
	return &Provider{
		endpoint: strings.TrimSuffix(baseURL, "/") + "/chat/completions",
		apiKey:   apiKey,
	}
}

// The wire types of the API.
type (
	apiRequest struct {
		Model         string            `json:"model"`
		Messages      []apiMessage      `json:"messages"`
		MaxTokens     int               `json:"max_tokens"`
		Stream        bool              `json:"stream"`
		StreamOptions *apiStreamOptions `json:"stream_options,omitempty"`
	}
	apiMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	apiStreamOptions struct {
		IncludeUsage bool `json:"include_usage"`
	}
	apiChunk struct {
		Choices []struct {
			Delta struct {
				Content json.RawMessage `json:"content"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
		Error json.RawMessage `json:"error"`
	}
	apiPart struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
)

// Stream implements llm.Provider. It sends only the text of the answer to
// onText. Thinking does not reach the user.
func (p *Provider) Stream(ctx context.Context, req llm.Request, onText func(string) error) (llm.Response, error) {
	body, err := p.newRequestBody(req)
	if err != nil {
		return llm.Response{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return llm.Response{}, fmt.Errorf("chatcompletions: %w", err)
	}
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return llm.Response{}, fmt.Errorf("chatcompletions: %w", err)
	}
	defer resp.Body.Close()
	// The error body can repeat the request text, so the error has only
	// the status.
	if resp.StatusCode != http.StatusOK {
		return llm.Response{}, fmt.Errorf("chatcompletions: status %d", resp.StatusCode)
	}
	return readStream(resp.Body, onText)
}

// newRequestBody maps req to the JSON body of the API. The system prompt
// becomes the first message.
func (p *Provider) newRequestBody(req llm.Request) ([]byte, error) {
	out := apiRequest{
		Model:     req.Model,
		Messages:  make([]apiMessage, 0, len(req.Messages)+1),
		MaxTokens: req.MaxTokens,
		Stream:    true,
	}
	if p.StreamUsage {
		out.StreamOptions = &apiStreamOptions{IncludeUsage: true}
	}
	if req.System != "" {
		out.Messages = append(out.Messages, apiMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case llm.RoleUser, llm.RoleAssistant:
			out.Messages = append(out.Messages, apiMessage{Role: string(m.Role), Content: m.Text})
		default:
			return nil, fmt.Errorf("chatcompletions: unknown role %q", m.Role)
		}
	}
	return json.Marshal(out)
}

// readStream reads the data lines of the stream until the [DONE] line or
// the end of the body. It ignores the other lines of the event stream.
func readStream(r io.Reader, onText func(string) error) (llm.Response, error) {
	var s stream
	scanner := bufio.NewScanner(r)
	scanner.Buffer(nil, maxLineBytes)
	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data:")
		if !ok {
			continue
		}
		data = strings.TrimPrefix(data, " ")
		if data == "[DONE]" {
			break
		}
		if err := s.add([]byte(data), onText); err != nil {
			return llm.Response{}, err
		}
	}
	if err := scanner.Err(); err != nil {
		return llm.Response{}, fmt.Errorf("chatcompletions: reading the stream: %w", err)
	}
	return s.response()
}

// stream collects the answer from the chunks of one stream.
type stream struct {
	text   strings.Builder
	finish string
	usage  llm.Usage
}

// add reads one chunk and sends its text to onText. The usage comes in
// the last chunk, which has no choices.
func (s *stream) add(data []byte, onText func(string) error) error {
	var chunk apiChunk
	if err := json.Unmarshal(data, &chunk); err != nil {
		return fmt.Errorf("chatcompletions: reading a chunk: %w", err)
	}
	// The message of an error event can repeat the request text, so the
	// error does not include it.
	if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
		return errors.New("chatcompletions: the server sent an error event")
	}
	if chunk.Usage != nil {
		s.usage = llm.Usage{InputTokens: chunk.Usage.PromptTokens, OutputTokens: chunk.Usage.CompletionTokens}
	}
	if len(chunk.Choices) == 0 {
		return nil
	}
	choice := chunk.Choices[0]
	if choice.FinishReason != "" {
		s.finish = choice.FinishReason
	}
	text, err := deltaText(choice.Delta.Content)
	if err != nil || text == "" {
		return err
	}
	s.text.WriteString(text)
	return onText(text)
}

// response returns the complete answer.
func (s *stream) response() (llm.Response, error) {
	stop, err := stopReason(s.finish)
	if err != nil {
		return llm.Response{}, err
	}
	return llm.Response{Text: s.text.String(), StopReason: stop, Usage: s.usage}, nil
}

// deltaText returns the text of one delta. The content is a string, or a
// list of parts when the model thinks (for example Mistral Magistral).
// Only the text parts count, so the thinking does not reach the user.
func deltaText(content json.RawMessage) (string, error) {
	if len(content) == 0 || string(content) == "null" {
		return "", nil
	}
	if content[0] == '"' {
		var text string
		if err := json.Unmarshal(content, &text); err != nil {
			return "", fmt.Errorf("chatcompletions: reading the content: %w", err)
		}
		return text, nil
	}
	var parts []apiPart
	if err := json.Unmarshal(content, &parts); err != nil {
		return "", fmt.Errorf("chatcompletions: reading the content: %w", err)
	}
	var text strings.Builder
	for _, part := range parts {
		if part.Type == "text" {
			text.WriteString(part.Text)
		}
	}
	return text.String(), nil
}

// stopReason maps the finish reason of the API to llm.StopReason. The
// request has no tools, so a tool call is an error.
func stopReason(reason string) (llm.StopReason, error) {
	switch reason {
	case "stop":
		return llm.StopEnd, nil
	case "length", "model_length":
		return llm.StopMaxTokens, nil
	case "content_filter":
		return llm.StopRefusal, nil
	case "":
		return "", errors.New("chatcompletions: the stream ended without a finish reason")
	default:
		return "", fmt.Errorf("chatcompletions: unexpected finish reason %q", reason)
	}
}
