package triage

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/systemone"
)

// maxMessageLength caps a message in characters. It also caps the input
// cost of one model call.
const maxMessageLength = 4000

// minConfidence is the lowest confidence in a queue that the service
// accepts. Below it, a person reviews the message.
const minConfidence = 0.6

// urgentThreshold is the lowest probability of urgency that marks a
// message as urgent.
const urgentThreshold = 0.7

// triageTimeout caps the time of one model call. It is shorter than
// http.Server.WriteTimeout, so the page always reaches the client.
const triageTimeout = 10 * time.Second

// The question ids. The response uses the same ids.
const (
	topicQuestion  = "topic"
	urgentQuestion = "urgent"
)

// Service keeps the rules of the feature: the input checks, the questions,
// and the thresholds of the decision. It knows models only through
// systemone.Provider.
type Service struct {
	models systemone.Provider
	model  string
}

// NewService returns a Service that sends each message to model through
// models, for example to "fake/uniform" through a systemone.Router.
func NewService(models systemone.Provider, model string) *Service {
	return &Service{models: models, model: model}
}

// Model returns the model reference of the service, for example
// "fake/uniform".
func (s *Service) Model() string { return s.model }

// Triage asks the model about message and returns the decision. It returns
// ValidationError for bad input and ErrInvalidAnswer when the answer of the
// model does not match the questions.
func (s *Service) Triage(ctx context.Context, message string) (Decision, error) {
	message, err := normalizeMessage(message)
	if err != nil {
		return Decision{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, triageTimeout)
	defer cancel()
	resp, err := s.models.Ask(ctx, systemone.Request{
		Model:     s.model,
		State:     message,
		Questions: questions(),
	})
	if err != nil {
		return Decision{}, fmt.Errorf("asking the model: %w", err)
	}
	return decide(resp)
}

// normalizeMessage checks message and removes the outer spaces.
func normalizeMessage(message string) (string, error) {
	if !utf8.ValidString(message) || strings.ContainsRune(message, '\x00') {
		return "", ValidationError("message must contain valid text")
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return "", ValidationError("message must not be empty")
	}
	if utf8.RuneCountInString(message) > maxMessageLength {
		return "", ValidationError(fmt.Sprintf("message must be at most %d characters", maxMessageLength))
	}
	return message, nil
}

// questions returns the questions of every triage. The feature owns their
// meaning, so a change here changes the decisions.
func questions() map[string]systemone.Question {
	return map[string]systemone.Question{
		topicQuestion: {
			Type:         systemone.Choice,
			Instructions: "Which team must handle this support message?",
			Options:      topicOptions(),
		},
		urgentQuestion: {
			Type:         systemone.Noul,
			Instructions: "The customer needs help within one hour.",
		},
	}
}

// topicOptions maps each queue that the model can pick to its description.
func topicOptions() map[string]string {
	return map[string]string{
		string(QueueBilling): "Payments, invoices, refunds, and prices.",
		string(QueueBug):     "The product does not work as expected.",
		string(QueueAccount): "Login, password, profile, and access to the account.",
	}
}

// decide turns the answers into a decision. The answers are external
// data, so decide checks every value that it uses.
func decide(resp systemone.Response) (Decision, error) {
	topic, ok := resp.Answers[topicQuestion]
	_, known := topicOptions()[topic.Choice]
	if !ok || !known || topic.Type != systemone.Choice || !isProbability(topic.Confidence) {
		return Decision{}, fmt.Errorf("%w: question %q", ErrInvalidAnswer, topicQuestion)
	}
	urgent, ok := resp.Answers[urgentQuestion]
	if !ok || urgent.Type != systemone.Noul || !isProbability(urgent.Noul) {
		return Decision{}, fmt.Errorf("%w: question %q", ErrInvalidAnswer, urgentQuestion)
	}

	decision := Decision{
		Queue:      Queue(topic.Choice),
		Urgent:     urgent.Noul >= urgentThreshold,
		Confidence: topic.Confidence,
		Urgency:    urgent.Noul,
		Model:      resp.Model,
	}
	// A wrong queue costs more than a review by a person.
	if topic.Confidence < minConfidence {
		decision.Queue = QueueReview
	}
	return decision, nil
}

// isProbability reports whether p is a probability. NaN is not.
func isProbability(p float64) bool {
	return p >= 0 && p <= 1
}
