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

// answer returns the events of one answer: a thinking block, then the
// text "Hello world", then the stop reason.
func answer(stop string) []string {
	return []string{
		`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":12,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"The user greets me."}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"ping"}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Hello"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":" world"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_delta","delta":{"stop_reason":"` + stop + `","stop_sequence":null},"usage":{"output_tokens":5}}`,
		`{"type":"message_stop"}`,
	}
}

// newServer starts a fake Messages API that sends events. It keeps the
// body of the last request in got.
func newServer(t *testing.T, events []string, got *map[string]any) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" ||
			r.Header.Get("X-Api-Key") != testKey || r.Header.Get("Anthropic-Version") != "2023-06-01" {
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
		for _, data := range events {
			var event struct{ Type string }
			_ = json.Unmarshal([]byte(data), &event)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
		}
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
	server := newServer(t, answer("end_turn"), &body)

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
		`"system":"Be brief."`,
		`"messages":[{"content":"Hi","role":"user"},{"content":"Hello.","role":"assistant"},{"content":"Greet me again.","role":"user"}]`,
	} {
		if !strings.Contains(string(sent), want) {
			t.Errorf("request body %s does not contain %s", sent, want)
		}
	}
}

func TestStreamOmitsAnEmptySystemPrompt(t *testing.T) {
	t.Parallel()
	var body map[string]any
	server := newServer(t, answer("end_turn"), &body)
	req := request()
	req.System = ""

	if _, err := llm.Generate(t.Context(), anthropic.New(server.URL, testKey), req); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, ok := body["system"]; ok {
		t.Errorf("request body %v has a system field, want none", body)
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
			server := newServer(t, answer(tt.stop), nil)
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
	server := newServer(t, answer("end_turn"), nil)
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

// TestStreamErrorEventHidesRequestText checks an error in the middle of
// the stream, for example an overloaded server. The message of the event
// can repeat the request, so the error does not include it.
func TestStreamErrorEventHidesRequestText(t *testing.T) {
	t.Parallel()
	server := newServer(t, []string{
		answer("end_turn")[0],
		`{"type":"error","error":{"type":"overloaded_error","message":"Greet me again."}}`,
	}, nil)

	_, err := llm.Generate(t.Context(), anthropic.New(server.URL, testKey), request())
	if err == nil || !strings.Contains(err.Error(), "error event") {
		t.Fatalf("err = %v, want the error event", err)
	}
	if strings.Contains(err.Error(), "Greet me again") {
		t.Fatalf("err = %v, it contains the request text", err)
	}
}

func TestStreamWithoutStopReasonFails(t *testing.T) {
	t.Parallel()
	events := answer("end_turn")
	server := newServer(t, events[:len(events)-2], nil)

	_, err := llm.Generate(t.Context(), anthropic.New(server.URL, testKey), request())
	if err == nil || !strings.Contains(err.Error(), "without a stop reason") {
		t.Fatalf("err = %v, want an error for the missing stop reason", err)
	}
}

// TestStreamRejectsALongLine checks the cap of one line (1 MiB). The
// client stops and does not keep the line in memory.
func TestStreamRejectsALongLine(t *testing.T) {
	t.Parallel()
	long := `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"` + strings.Repeat("a", 1<<20) + `"}}`
	server := newServer(t, []string{long}, nil)

	_, err := llm.Generate(t.Context(), anthropic.New(server.URL, testKey), request())
	if err == nil || !strings.Contains(err.Error(), "reading the stream") {
		t.Fatalf("err = %v, want an error for the long line", err)
	}
}

// TestNewKeepsTheBasePath checks the route of Claude on Bedrock. Bedrock
// serves the Messages API under /anthropic/ and reads the key from
// x-api-key, as the API of Anthropic does.
func TestNewKeepsTheBasePath(t *testing.T) {
	t.Parallel()
	api := newServer(t, answer("end_turn"), nil)
	bedrock := httptest.NewServer(http.StripPrefix("/anthropic", api.Config.Handler))
	t.Cleanup(bedrock.Close)

	for _, base := range []string{bedrock.URL + "/anthropic/", bedrock.URL + "/anthropic"} {
		if _, err := llm.Generate(t.Context(), anthropic.New(base, testKey), request()); err != nil {
			t.Fatalf("Generate with %s: %v, want a request to /anthropic/v1/messages", base, err)
		}
	}
}
