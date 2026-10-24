package typesafe_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/systemone"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/systemone/typesafe"
)

const testKey = "test-key"

// docsResponse is the example response of the TypeSafe documentation.
const docsResponse = `{
  "model": "jev-1.13.0",
  "answers": {
    "department": {"type": "choice", "choice": "technical", "confidence": 1.0,
      "probabilities": {"technical": 1.0, "billing": 0.0, "sales": 0.0}},
    "frustration": {"type": "score", "score": 0.67, "confidence": 0.5,
      "legend": {"0": "Calm", "1": "Frustrated", "2": "Angry"},
      "probabilities": {"0": 0.33, "1": 0.67, "2": 0.0}},
    "is_urgent": {"type": "noul", "noul": 0.99}
  },
  "usage": {"input_tokens": 428, "output_tokens": 73}
}`

// newServer starts a fake API that checks the request and answers with
// status and body. It keeps the body of the last request in got.
func newServer(t *testing.T, status int, body string, got *string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/systemone" ||
			r.Header.Get("Authorization") != "Bearer "+testKey ||
			r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "wrong request", http.StatusNotFound)
			return
		}
		if got != nil {
			b, _ := io.ReadAll(r.Body)
			*got = string(b)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}

func request() systemone.Request {
	return systemone.Request{
		Model: "jev-latest",
		State: "Our API returns 500 errors.",
		Questions: map[string]systemone.Question{
			"department": {
				Type:         systemone.Choice,
				Instructions: "Which team should handle this",
				Options:      map[string]string{"billing": "Payments", "technical": "Bugs"},
			},
			"frustration": {
				Type:         systemone.Score,
				Instructions: "How frustrated the customer appears",
				Levels:       []string{"Calm", "Frustrated", "Angry"},
			},
			"is_urgent": {Type: systemone.Noul, Instructions: "The message is urgent"},
		},
	}
}

func TestAskSendsRequestAndMapsAnswers(t *testing.T) {
	t.Parallel()
	var body string
	server := newServer(t, http.StatusOK, docsResponse, &body)

	// A trailing slash in the base URL does not change the endpoint.
	resp, err := typesafe.New(server.URL+"/", testKey).Ask(t.Context(), request())
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}

	wantBody := `{"state":"Our API returns 500 errors.","model":"jev-latest","questions":{` +
		`"department":{"type":"choice","instructions":"Which team should handle this","criteria":{"billing":"Payments","technical":"Bugs"}},` +
		`"frustration":{"type":"score","instructions":"How frustrated the customer appears","criteria":["Calm","Frustrated","Angry"]},` +
		`"is_urgent":{"type":"noul","instructions":"The message is urgent"}}}`
	if body != wantBody {
		t.Errorf("request body:\n%s\nwant:\n%s", body, wantBody)
	}

	want := systemone.Response{
		Model: "jev-1.13.0",
		Answers: map[string]systemone.Answer{
			"department": {Type: systemone.Choice, Choice: "technical", Confidence: 1,
				Probabilities: map[string]float64{"technical": 1, "billing": 0, "sales": 0}},
			"frustration": {Type: systemone.Score, Score: 0.67, Confidence: 0.5,
				Probabilities: map[string]float64{"0": 0.33, "1": 0.67, "2": 0}},
			"is_urgent": {Type: systemone.Noul, Noul: 0.99},
		},
		Usage: systemone.Usage{InputTokens: 428, OutputTokens: 73},
	}
	if !reflect.DeepEqual(resp, want) {
		t.Errorf("response = %+v, want %+v", resp, want)
	}
}

func TestAskRejectsUnknownQuestionType(t *testing.T) {
	t.Parallel()
	req := request()
	req.Questions["rank"] = systemone.Question{Type: "rank", Instructions: "Rank it"}
	// The request fails before a network call, so the address is not used.
	if _, err := typesafe.New("http://127.0.0.1:1", testKey).Ask(t.Context(), req); err == nil {
		t.Fatal("Ask accepted an unknown question type")
	}
}

func TestAskErrorStatusHidesKeyAndState(t *testing.T) {
	t.Parallel()
	server := newServer(t, http.StatusUnauthorized, `{"error":"invalid key for state: Our API returns 500 errors."}`, nil)

	_, err := typesafe.New(server.URL, testKey).Ask(t.Context(), request())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v, want the 401 of the API", err)
	}
	if strings.Contains(err.Error(), testKey) || strings.Contains(err.Error(), "Our API") {
		t.Fatalf("err = %v, it contains the key or the state", err)
	}
}

func TestAskRejectsBadResponse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
	}{
		{"not json", `<html>gateway error</html>`},
		{"unknown answer type", `{"model":"jev-1","answers":{"x":{"type":"rank"}}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := newServer(t, http.StatusOK, tt.body, nil)
			if resp, err := typesafe.New(server.URL, testKey).Ask(t.Context(), request()); err == nil {
				t.Fatalf("Ask = %+v, want an error", resp)
			}
		})
	}
}

func TestAskStopsWhenContextEnds(t *testing.T) {
	t.Parallel()
	server := newServer(t, http.StatusOK, docsResponse, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := typesafe.New(server.URL, testKey).Ask(ctx, request()); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
