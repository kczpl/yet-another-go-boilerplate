// Package llm is the provider-neutral core for large language models.
// Features depend only on this package. A provider adapter (a hosted API,
// a cloud platform, or a local server) implements Provider in its own
// subpackage, for example llm/anthropic. A Router selects the provider by
// the prefix of the model name.
package llm

import (
	"context"
	"errors"
)

// ErrUnknownProvider means that a model reference names a provider that
// the Router does not have.
var ErrUnknownProvider = errors.New("unknown llm provider")

// Role tells who wrote a message.
type Role string

// The roles of a conversation. The system prompt is not a message; it is
// Request.System.
const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one turn of a conversation.
type Message struct {
	Role Role
	Text string
}

// Request is one call to a model.
type Request struct {
	// Model names the model. A Router expects "provider/model". A provider
	// gets only the part after the first slash.
	Model string
	// System is the system prompt. It is empty when the call has none.
	System string
	// Messages is the conversation. It ends with a user message.
	Messages []Message
	// MaxTokens caps the length of the answer, and with it the cost of
	// the call. It must be positive.
	MaxTokens int
}

// StopReason tells why the model stopped. A provider maps its own values
// to these constants.
type StopReason string

const (
	// StopEnd means that the model finished its answer.
	StopEnd StopReason = "end"
	// StopMaxTokens means that the answer reached Request.MaxTokens or
	// the context window of the model.
	StopMaxTokens StopReason = "max_tokens"
	// StopRefusal means that the model declined to answer.
	StopRefusal StopReason = "refusal"
)

// Usage counts the tokens of one call. Providers bill by these numbers.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Response is the complete answer to a Request.
type Response struct {
	Text       string
	StopReason StopReason
	Usage      Usage
}

// Provider is one source of models.
type Provider interface {
	// Stream sends req and calls onText with each part of the answer, in
	// order. It returns the complete answer when the model stops. When
	// onText returns an error, Stream stops the call and returns that
	// error. Stream also stops when ctx ends.
	Stream(ctx context.Context, req Request, onText func(text string) error) (Response, error)
}

// Generate sends req and waits for the complete answer.
func Generate(ctx context.Context, p Provider, req Request) (Response, error) {
	return p.Stream(ctx, req, func(string) error { return nil })
}
