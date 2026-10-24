package chat

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/llm"
)

// maxQuestionLength caps a question in characters. It also caps the input
// cost of one model call.
const maxQuestionLength = 2000

// maxAnswerTokens caps the answer, and with it the output cost of one
// model call. Current Claude models think before they answer, and the
// thinking tokens count toward this cap.
const maxAnswerTokens = 4096

// answerTimeout caps the time of one model call. A provider that hangs
// must not hold the request and the connection without a limit.
const answerTimeout = 2 * time.Minute

// systemPrompt sets the behavior of the model for every question.
const systemPrompt = "You are a helpful assistant in a web app. Answer briefly, in plain text."

// Service keeps the rules of the feature: the input checks, the prompt,
// and the cost limits. It knows models only through llm.Provider.
type Service struct {
	models llm.Provider
	model  string
}

// NewService returns a Service that sends each question to model through
// models, for example to "fake/echo" through an llm.Router.
func NewService(models llm.Provider, model string) *Service {
	return &Service{models: models, model: model}
}

// Model returns the model reference of the service, for example
// "fake/echo".
func (s *Service) Model() string { return s.model }

// Stream asks question and calls onText with each part of the answer. It
// returns ValidationError for bad input.
func (s *Service) Stream(ctx context.Context, question string, onText func(string) error) (llm.Response, error) {
	req, err := s.request(question)
	if err != nil {
		return llm.Response{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, answerTimeout)
	defer cancel()
	return s.models.Stream(ctx, req, onText)
}

// Answer asks question and waits for the complete answer. It returns
// ValidationError for bad input.
func (s *Service) Answer(ctx context.Context, question string) (llm.Response, error) {
	req, err := s.request(question)
	if err != nil {
		return llm.Response{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, answerTimeout)
	defer cancel()
	return llm.Generate(ctx, s.models, req)
}

// request checks the question and builds the model call.
func (s *Service) request(question string) (llm.Request, error) {
	if !utf8.ValidString(question) || strings.ContainsRune(question, '\x00') {
		return llm.Request{}, ValidationError("question must contain valid text")
	}
	question = strings.TrimSpace(question)
	if question == "" {
		return llm.Request{}, ValidationError("question must not be empty")
	}
	if utf8.RuneCountInString(question) > maxQuestionLength {
		return llm.Request{}, ValidationError(fmt.Sprintf("question must be at most %d characters", maxQuestionLength))
	}
	return llm.Request{
		Model:     s.model,
		System:    systemPrompt,
		Messages:  []llm.Message{{Role: llm.RoleUser, Text: question}},
		MaxTokens: maxAnswerTokens,
	}, nil
}
