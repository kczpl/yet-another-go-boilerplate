package triage_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/systemone"
	"github.com/kczpl/yet-another-go-boilerplate/internal/triage"
)

// stubModel keeps the last request and returns a fixed response.
type stubModel struct {
	resp        systemone.Response
	err         error
	got         systemone.Request
	hasDeadline bool
}

func (m *stubModel) Ask(ctx context.Context, req systemone.Request) (systemone.Response, error) {
	m.got = req
	_, m.hasDeadline = ctx.Deadline()
	return m.resp, m.err
}

// answers builds a valid response: the model picks topic with confidence,
// and the message is urgent with the probability urgency.
func answers(topic string, confidence, urgency float64) systemone.Response {
	return systemone.Response{
		Model: "stub-1.0",
		Answers: map[string]systemone.Answer{
			"topic":  {Type: systemone.Choice, Choice: topic, Confidence: confidence},
			"urgent": {Type: systemone.Noul, Noul: urgency},
		},
	}
}

func TestMessageValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		message string
		want    string
	}{
		{"empty", "", "message must not be empty"},
		{"blank", " \n\t ", "message must not be empty"},
		{"too long", strings.Repeat("a", 4001), "message must be at most 4000 characters"},
		{"invalid utf-8", "a\xffb", "message must contain valid text"},
		{"nul byte", "a\x00b", "message must contain valid text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			model := &stubModel{resp: answers("bug", 0.9, 0.1)}
			_, err := triage.NewService(model, "test/model").Triage(t.Context(), tt.message)
			if vErr, ok := errors.AsType[triage.ValidationError](err); !ok || vErr.Error() != tt.want {
				t.Fatalf("err = %v, want ValidationError %q", err, tt.want)
			}
			if model.got.Model != "" {
				t.Fatal("the model ran for invalid input")
			}
		})
	}
}

func TestMessageAtLengthLimit(t *testing.T) {
	t.Parallel()
	// The limit counts characters, not bytes: 4000 two-byte runes pass.
	svc := triage.NewService(&stubModel{resp: answers("bug", 0.9, 0.1)}, "test/model")
	if _, err := svc.Triage(t.Context(), strings.Repeat("é", 4000)); err != nil {
		t.Fatalf("Triage: %v", err)
	}
}

func TestTriageBuildsRequest(t *testing.T) {
	t.Parallel()
	model := &stubModel{resp: answers("bug", 0.9, 0.1)}
	svc := triage.NewService(model, "test/model")
	if _, err := svc.Triage(t.Context(), "  The app crashes on login.  "); err != nil {
		t.Fatalf("Triage: %v", err)
	}
	req := model.got
	if req.Model != "test/model" || req.State != "The app crashes on login." {
		t.Errorf("Model = %q, State = %q, want test/model and the trimmed message", req.Model, req.State)
	}
	topic := req.Questions["topic"]
	if topic.Type != systemone.Choice || len(topic.Options) < 2 || topic.Options["review"] != "" {
		t.Errorf("topic = %+v, want a choice of the queues without review", topic)
	}
	if req.Questions["urgent"].Type != systemone.Noul {
		t.Errorf("urgent = %+v, want a noul", req.Questions["urgent"])
	}
	// t.Context has no deadline, so the deadline comes from the service.
	if !model.hasDeadline {
		t.Error("the model call has no deadline")
	}
}

func TestTriageDecision(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		topic      string
		confidence float64
		urgency    float64
		wantQueue  triage.Queue
		wantUrgent bool
	}{
		{"sure and calm", "bug", 0.9, 0.2, triage.QueueBug, false},
		{"sure and urgent", "billing", 0.8, 0.95, triage.QueueBilling, true},
		{"both thresholds", "account", 0.6, 0.7, triage.QueueAccount, true},
		{"not sure", "bug", 0.59, 0.69, triage.QueueReview, false},
		{"not sure but urgent", "billing", 0.1, 0.9, triage.QueueReview, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			model := &stubModel{resp: answers(tt.topic, tt.confidence, tt.urgency)}
			got, err := triage.NewService(model, "test/model").Triage(t.Context(), "help")
			if err != nil {
				t.Fatalf("Triage: %v", err)
			}
			want := triage.Decision{
				Queue:      tt.wantQueue,
				Urgent:     tt.wantUrgent,
				Confidence: tt.confidence,
				Urgency:    tt.urgency,
				Model:      "stub-1.0",
			}
			if got != want {
				t.Fatalf("decision = %+v, want %+v", got, want)
			}
		})
	}
}

func TestTriageRejectsInvalidAnswers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(systemone.Response)
	}{
		{"no topic", func(r systemone.Response) { delete(r.Answers, "topic") }},
		{"no urgency", func(r systemone.Response) { delete(r.Answers, "urgent") }},
		{"unknown queue", func(r systemone.Response) {
			r.Answers["topic"] = systemone.Answer{Type: systemone.Choice, Choice: "sales", Confidence: 0.9}
		}},
		{"review from the model", func(r systemone.Response) {
			r.Answers["topic"] = systemone.Answer{Type: systemone.Choice, Choice: "review", Confidence: 0.9}
		}},
		{"wrong type", func(r systemone.Response) {
			r.Answers["topic"] = systemone.Answer{Type: systemone.Score, Choice: "bug", Confidence: 0.9}
		}},
		{"confidence above 1", func(r systemone.Response) {
			r.Answers["topic"] = systemone.Answer{Type: systemone.Choice, Choice: "bug", Confidence: 1.5}
		}},
		{"urgency is NaN", func(r systemone.Response) {
			r.Answers["urgent"] = systemone.Answer{Type: systemone.Noul, Noul: math.NaN()}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp := answers("bug", 0.9, 0.1)
			tt.change(resp)
			_, err := triage.NewService(&stubModel{resp: resp}, "test/model").Triage(t.Context(), "help")
			if !errors.Is(err, triage.ErrInvalidAnswer) {
				t.Fatalf("err = %v, want ErrInvalidAnswer", err)
			}
		})
	}
}

func TestTriageModelError(t *testing.T) {
	t.Parallel()
	cause := errors.New("upstream 529")
	_, err := triage.NewService(&stubModel{err: cause}, "test/model").Triage(t.Context(), "help")
	if !errors.Is(err, cause) {
		t.Fatalf("err = %v, want the model error", err)
	}
}

func TestTriageWithFakeModel(t *testing.T) {
	t.Parallel()
	// The fake model rejects the questions that a real provider rejects.
	// It knows nothing, so its confidence is zero and a person reviews.
	models := systemone.NewRouter(map[string]systemone.Provider{"fake": systemone.Fake{}})
	got, err := triage.NewService(models, "fake/uniform").Triage(t.Context(), "I was charged twice.")
	if err != nil {
		t.Fatalf("Triage: %v", err)
	}
	want := triage.Decision{Queue: triage.QueueReview, Urgency: 0.5, Model: "uniform"}
	if got != want {
		t.Fatalf("decision = %+v, want %+v", got, want)
	}
}
