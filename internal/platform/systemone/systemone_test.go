package systemone_test

import (
	"context"
	"errors"
	"maps"
	"testing"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/systemone"
)

// recorder is a provider that records the request it gets.
type recorder struct {
	got systemone.Request
}

func (r *recorder) Ask(_ context.Context, req systemone.Request) (systemone.Response, error) {
	r.got = req
	return systemone.Response{Model: "recorded"}, nil
}

// ticket asks the three question types about one support ticket.
func ticket() systemone.Request {
	return systemone.Request{
		Model: "uniform",
		State: "The API returns 500 on every request since 20 minutes.",
		Questions: map[string]systemone.Question{
			"department": {
				Type:         systemone.Choice,
				Instructions: "Which team must handle this ticket?",
				Options: map[string]string{
					"technical": "Bugs or integration problems",
					"billing":   "Payment or subscription problems",
					"sales":     "Price or account questions",
				},
			},
			"frustration": {
				Type:         systemone.Score,
				Instructions: "How frustrated is the customer?",
				Levels:       []string{"Calm", "Annoyed", "Angry", "Furious"},
			},
			"is_urgent": {
				Type:         systemone.Noul,
				Instructions: "The issue blocks the business of the customer.",
			},
		},
	}
}

func TestRouterSendsBareModelToProvider(t *testing.T) {
	t.Parallel()
	local := &recorder{}
	router := systemone.NewRouter(map[string]systemone.Provider{"local": local})

	req := ticket()
	req.Model = "local/org/judge-v2"
	if _, err := router.Ask(t.Context(), req); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if local.got.Model != "org/judge-v2" {
		t.Errorf("provider got model %q, want the part after the first slash", local.got.Model)
	}
}

func TestRouterRejectsBadReferences(t *testing.T) {
	t.Parallel()
	router := systemone.NewRouter(map[string]systemone.Provider{"fake": systemone.Fake{}})

	tests := []struct {
		ref         string
		wantUnknown bool
	}{
		{"uniform", false},
		{"/uniform", false},
		{"fake/", false},
		{"", false},
		{"typesafe/jev-latest", true},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			_, _, err := router.Resolve(tt.ref)
			if err == nil {
				t.Fatal("Resolve accepted a bad reference")
			}
			if got := errors.Is(err, systemone.ErrUnknownProvider); got != tt.wantUnknown {
				t.Errorf("errors.Is(err, ErrUnknownProvider) = %v, want %v (err: %v)", got, tt.wantUnknown, err)
			}
		})
	}
}

func TestFakeAnswersUniformly(t *testing.T) {
	t.Parallel()
	resp, err := systemone.Fake{}.Ask(t.Context(), ticket())
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if resp.Model != "uniform" || len(resp.Answers) != 3 {
		t.Fatalf("response = %+v, want 3 answers from model uniform", resp)
	}

	third := 1.0 / 3
	department := resp.Answers["department"]
	wantDepartment := map[string]float64{"billing": third, "sales": third, "technical": third}
	if department.Type != systemone.Choice || department.Choice != "billing" || !maps.Equal(department.Probabilities, wantDepartment) {
		t.Errorf("department = %+v, want the first label and %v", department, wantDepartment)
	}

	frustration := resp.Answers["frustration"]
	wantFrustration := map[string]float64{"0": 0.25, "1": 0.25, "2": 0.25, "3": 0.25}
	if frustration.Type != systemone.Score || frustration.Score != 1.5 || !maps.Equal(frustration.Probabilities, wantFrustration) {
		t.Errorf("frustration = %+v, want score 1.5 and %v", frustration, wantFrustration)
	}

	if urgent := resp.Answers["is_urgent"]; urgent.Type != systemone.Noul || urgent.Noul != 0.5 {
		t.Errorf("is_urgent = %+v, want noul 0.5", urgent)
	}
}

func TestFakeRejectsBadRequests(t *testing.T) {
	t.Parallel()
	tests := map[string]func(*systemone.Request){
		"unknown model": func(r *systemone.Request) { r.Model = "oracle" },
		"blank state":   func(r *systemone.Request) { r.State = "  " },
		"no questions":  func(r *systemone.Request) { r.Questions = nil },
		"no instructions": func(r *systemone.Request) {
			setQuestion(r, "is_urgent", func(q *systemone.Question) { q.Instructions = "" })
		},
		"one option": func(r *systemone.Request) {
			setQuestion(r, "department", func(q *systemone.Question) { q.Options = map[string]string{"a": "A"} })
		},
		"one level": func(r *systemone.Request) {
			setQuestion(r, "frustration", func(q *systemone.Question) { q.Levels = q.Levels[:1] })
		},
		"unknown type": func(r *systemone.Request) {
			setQuestion(r, "is_urgent", func(q *systemone.Question) { q.Type = "essay" })
		},
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			req := ticket()
			change(&req)
			if _, err := (systemone.Fake{}).Ask(t.Context(), req); err == nil {
				t.Error("Ask accepted the request")
			}
		})
	}
}

func TestFakeStopsWhenContextEnds(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := (systemone.Fake{}).Ask(ctx, ticket()); !errors.Is(err, context.Canceled) {
		t.Errorf("Ask = %v, want context.Canceled", err)
	}
}

// setQuestion changes the question id of r.
func setQuestion(r *systemone.Request, id string, change func(*systemone.Question)) {
	q := r.Questions[id]
	change(&q)
	r.Questions[id] = q
}
