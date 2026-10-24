package systemone

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// Fake is a provider for development and tests. It makes no network call
// and needs no credentials. Its model "uniform" knows nothing: all options
// and all levels get the same probability, and the confidence is zero. It
// reports no usage.
type Fake struct{}

var _ Provider = Fake{}

// Ask implements Provider. It rejects the requests that a real provider
// rejects, so a feature with a bad question fails its tests.
func (Fake) Ask(ctx context.Context, req Request) (Response, error) {
	if req.Model != "uniform" {
		return Response{}, fmt.Errorf("fake: unknown model %q", req.Model)
	}
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	if strings.TrimSpace(req.State) == "" {
		return Response{}, errors.New("fake: the request has no state")
	}
	if len(req.Questions) == 0 {
		return Response{}, errors.New("fake: the request has no questions")
	}
	resp := Response{Model: req.Model, Answers: make(map[string]Answer, len(req.Questions))}
	for id, q := range req.Questions {
		answer, err := uniformAnswer(q)
		if err != nil {
			return Response{}, fmt.Errorf("fake: question %q: %w", id, err)
		}
		resp.Answers[id] = answer
	}
	return resp, nil
}

// uniformAnswer returns the answer of a model that knows nothing.
func uniformAnswer(q Question) (Answer, error) {
	if strings.TrimSpace(q.Instructions) == "" {
		return Answer{}, errors.New("the question has no instructions")
	}
	switch q.Type {
	case Choice:
		return uniformChoice(q.Options)
	case Score:
		return uniformScore(q.Levels)
	case Noul:
		return Answer{Type: Noul, Noul: 0.5}, nil
	default:
		return Answer{}, fmt.Errorf("unknown question type %q", q.Type)
	}
}

// uniformChoice gives each option the same probability. The first label
// in sorted order is the choice, so the answer is always the same.
func uniformChoice(options map[string]string) (Answer, error) {
	if len(options) < 2 {
		return Answer{}, errors.New("a choice needs at least 2 options")
	}
	labels := slices.Sorted(maps.Keys(options))
	answer := Answer{Type: Choice, Choice: labels[0], Probabilities: make(map[string]float64, len(labels))}
	for _, label := range labels {
		answer.Probabilities[label] = 1 / float64(len(labels))
	}
	return answer, nil
}

// uniformScore gives each level the same probability. The expected level
// is then the middle of the scale.
func uniformScore(levels []string) (Answer, error) {
	n := len(levels)
	if n < 2 {
		return Answer{}, errors.New("a score needs at least 2 levels")
	}
	answer := Answer{Type: Score, Score: float64(n-1) / 2, Probabilities: make(map[string]float64, n)}
	for i := range n {
		answer.Probabilities[strconv.Itoa(i)] = 1 / float64(n)
	}
	return answer, nil
}
