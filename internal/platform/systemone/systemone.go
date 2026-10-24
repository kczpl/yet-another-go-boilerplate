// Package systemone is the provider-neutral core for System One models.
// A System One model does not write text. It reads a state, answers typed
// questions about it, and returns calibrated probabilities. Use it to
// classify, to score, and to check statements. Use package llm when the
// app must write text.
//
// Features depend only on this package. A provider adapter, for example
// TypeSafe Jev, implements Provider in its own subpackage, for example
// systemone/typesafe. A Router selects the provider by the prefix of the
// model name.
package systemone

import (
	"context"
	"errors"
)

// ErrUnknownProvider means that a model reference names a provider that
// the Router does not have.
var ErrUnknownProvider = errors.New("unknown system one provider")

// QuestionType tells which kind of answer a question expects.
type QuestionType string

// The question types. A provider maps them to its own names.
const (
	// Choice picks one option from Question.Options.
	Choice QuestionType = "choice"
	// Score places the state on the scale of Question.Levels.
	Score QuestionType = "score"
	// Noul tells how probable it is that the statement in
	// Question.Instructions is true.
	Noul QuestionType = "noul"
)

// Question is one thing that the model must judge about the state.
type Question struct {
	Type QuestionType
	// Instructions tell the model what to judge. For Noul, it is the
	// statement to check.
	Instructions string
	// Options maps each label of a Choice to its description. Other
	// types ignore it.
	Options map[string]string
	// Levels describes the scale of a Score, level 0 first. Other types
	// ignore it.
	Levels []string
}

// Request is one call to a model.
type Request struct {
	// Model names the model. A Router expects "provider/model", for
	// example "typesafe/jev-latest". A provider gets only the part after
	// the first slash.
	Model string
	// State is the text to judge. Send structured data as JSON text.
	State string
	// Questions maps a question id to its question. The response uses the
	// same ids.
	Questions map[string]Question
}

// Answer is the answer to one question.
type Answer struct {
	Type QuestionType
	// Choice is the most probable option label of a Choice.
	Choice string
	// Score is the expected level of a Score, from 0 to len(Levels)-1.
	Score float64
	// Noul is the probability that the statement is true, from 0 to 1.
	Noul float64
	// Probabilities maps each option label of a Choice, or each level
	// index of a Score ("0", "1", ...), to its probability.
	Probabilities map[string]float64
	// Confidence is the confidence of the model in a Choice or a Score,
	// from 0 to 1.
	Confidence float64
}

// Usage counts the tokens of one call. Providers bill by these numbers.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Response holds the answers to one Request.
type Response struct {
	// Model is the exact model version that answered, for example
	// "jev-1.13.0".
	Model string
	// Answers maps each question id of the request to its answer.
	Answers map[string]Answer
	Usage   Usage
}

// Provider is one source of System One models.
type Provider interface {
	// Ask sends req and returns one answer for each question. It stops
	// when ctx ends.
	Ask(ctx context.Context, req Request) (Response, error)
}
