package anthropic_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/llm"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/llm/anthropic"
)

const testKey = "test-key"

// streamBody returns the events of one answer: a thinking block, then the
// text "Hello world", then the stop reason.
func streamBody(stop string) string {
	events := []string{
		`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":12,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"The user greets me."}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Hello"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":" world"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_delta","delta":{"stop_reason":"` + stop + `","stop_sequence":null},"usage":{"output_tokens":5}}`,
		`{"type":"message_stop"}`,
	}
	var b strings.Builder
	for _, data := range events {
		var event struct{ Type string }
		_ = json.Unmarshal([]byte(data), &event)
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", event.Type, data)
	}
	return b.String()
}

// newServer starts a fake Messages API that answers with stop. It keeps
// the body of the last request in got.
func newServer(t *testing.T, stop string, got *map[string]any) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" || r.Header.Get("X-Api-Key") != testKey {
			http.Error(w, `{"type":"error","error":{"type":"not_found_error","message":"wrong request"}}`, http.StatusNotFound)
			return
		}
		if got != nil {
			if err := json.NewDecoder(r.Body).Decode(got); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, streamBody(stop))
	}))
	t.Cleanup(server.Close)
	return server
}

func request() llm.Request {
	return llm.Request{
		Model:  "claude-test",
		System: "Be brief.",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Text: "Hi"},
			{Role: llm.RoleAssistant, Text: "Hello."},
			{Role: llm.RoleUser, Text: "Greet me again."},
		},
		MaxTokens: 100,
	}
}

func TestStreamSendsRequestAndMapsAnswer(t *testing.T) {
	t.Parallel()
	var body map[string]any
	server := newServer(t, "end_turn", &body)

	var parts []string
	resp, err := anthropic.New(server.URL, testKey).Stream(t.Context(), request(), func(text string) error {
		parts = append(parts, text)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	// The thinking block does not reach onText or the answer.
	if !slices.Equal(parts, []string{"Hello", " world"}) {
		t.Errorf("parts = %q, want the two text deltas", parts)
	}
	want := llm.Response{Text: "Hello world", StopReason: llm.StopEnd, Usage: llm.Usage{InputTokens: 12, OutputTokens: 5}}
	if resp != want {
		t.Errorf("response = %+v, want %+v", resp, want)
	}

	sent, _ := json.Marshal(body)
	for _, want := range []string{
		`"model":"claude-test"`,
		`"max_tokens":100`,
		`"stream":true`,
		`"system":[{"text":"Be brief.","type":"text"}]`,
		`{"content":[{"text":"Hello.","type":"text"}],"role":"assistant"}`,
		`{"content":[{"text":"Greet me again.","type":"text"}],"role":"user"}`,
	} {
		if !strings.Contains(string(sent), want) {
			t.Errorf("request body %s does not contain %s", sent, want)
		}
	}
}

func TestStreamMapsStopReasons(t *testing.T) {
	t.Parallel()
	tests := []struct {
		stop string
		want llm.StopReason
	}{
		{"end_turn", llm.StopEnd},
		{"stop_sequence", llm.StopEnd},
		{"max_tokens", llm.StopMaxTokens},
		{"model_context_window_exceeded", llm.StopMaxTokens},
		{"refusal", llm.StopRefusal},
		{"tool_use", ""},
	}
	for _, tt := range tests {
		t.Run(tt.stop, func(t *testing.T) {
			t.Parallel()
			server := newServer(t, tt.stop, nil)
			resp, err := llm.Generate(t.Context(), anthropic.New(server.URL, testKey), request())
			if tt.want == "" {
				if err == nil {
					t.Fatalf("Generate = %+v, want an error for stop reason %q", resp, tt.stop)
				}
				return
			}
			if err != nil || resp.StopReason != tt.want {
				t.Fatalf("StopReason = %q, err = %v, want %q", resp.StopReason, err, tt.want)
			}
		})
	}
}

func TestStreamStopsWhenOnTextFails(t *testing.T) {
	t.Parallel()
	server := newServer(t, "end_turn", nil)
	stop := errors.New("client left")
	calls := 0
	_, err := anthropic.New(server.URL, testKey).Stream(t.Context(), request(), func(string) error {
		calls++
		return stop
	})
	if !errors.Is(err, stop) || calls != 1 {
		t.Fatalf("err = %v after %d calls, want the onText error after 1 call", err, calls)
	}
}

func TestStreamRejectsUnknownRole(t *testing.T) {
	t.Parallel()
	req := request()
	req.Messages[0].Role = "system"
	// The request fails before a network call, so the address is not used.
	if _, err := llm.Generate(t.Context(), anthropic.New("http://127.0.0.1:1", testKey), req); err == nil {
		t.Fatal("Generate accepted an unknown role")
	}
}

func TestStreamAPIErrorHidesKey(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	}))
	t.Cleanup(server.Close)

	_, err := llm.Generate(t.Context(), anthropic.New(server.URL, "secret-key"), request())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v, want the 401 of the API", err)
	}
	if strings.Contains(err.Error(), "secret-key") {
		t.Fatalf("err = %v, it contains the API key", err)
	}
}

// TestNewKeepsTheBasePath checks the route of Claude on Bedrock. Bedrock
// serves the Messages API under /anthropic/ and reads the key from
// x-api-key, as the API of Anthropic does.
func TestNewKeepsTheBasePath(t *testing.T) {
	t.Parallel()
	api := newServer(t, "end_turn", nil)
	bedrock := httptest.NewServer(http.StripPrefix("/anthropic", api.Config.Handler))
	t.Cleanup(bedrock.Close)

	if _, err := llm.Generate(t.Context(), anthropic.New(bedrock.URL+"/anthropic/", testKey), request()); err != nil {
		t.Fatalf("Generate: %v, want a request to /anthropic/v1/messages", err)
	}
}

// TestNewIgnoresEnvironment cannot run in parallel, because it sets
// environment variables.
func TestNewIgnoresEnvironment(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "http://127.0.0.1:1")
	t.Setenv("ANTHROPIC_API_KEY", "env-key")
	server := newServer(t, "end_turn", nil)

	// The server rejects every key except testKey and is not at the
	// address of the environment.
	if _, err := llm.Generate(t.Context(), anthropic.New(server.URL, testKey), request()); err != nil {
		t.Fatalf("Generate: %v, want New to use only its arguments", err)
	}
}
