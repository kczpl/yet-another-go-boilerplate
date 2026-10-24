package chatcompletions_test

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/llm"
	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/llm/chatcompletions"
)

const testKey = "test-key"

// captured holds the parts of the last request that the tests check.
type captured struct {
	path, auth, contentType, accept string
	body                            string
}

// answer returns the chunks of one answer: "Hello world", then the finish
// reason with the usage, as Mistral sends them.
func answer(finish string) []string {
	return []string{
		`{"id":"1","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
		`{"id":"1","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
		`{"id":"1","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`,
		`{"id":"1","choices":[{"index":0,"delta":{"content":""},"finish_reason":"` + finish + `"}],"usage":{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17}}`,
	}
}

// newServer starts a fake API that sends chunks as an event stream and
// then [DONE]. It keeps the last request in got.
func newServer(t *testing.T, chunks []string, got *captured) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got != nil {
			body, _ := io.ReadAll(r.Body)
			*got = captured{
				path:        r.Method + " " + r.URL.Path,
				auth:        r.Header.Get("Authorization"),
				contentType: r.Header.Get("Content-Type"),
				accept:      r.Header.Get("Accept"),
				body:        string(body),
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		// A comment line keeps a connection open. The client ignores it.
		fmt.Fprint(w, ": ping\n\n")
		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	return server
}

func request() llm.Request {
	return llm.Request{
		Model:  "mistral-small-latest",
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
	var got captured
	server := newServer(t, answer("stop"), &got)

	var parts []string
	resp, err := chatcompletions.New(server.URL+"/v1/", testKey).Stream(t.Context(), request(), func(text string) error {
		parts = append(parts, text)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if !slices.Equal(parts, []string{"Hello", " world"}) {
		t.Errorf("parts = %q, want the two text deltas", parts)
	}
	want := llm.Response{Text: "Hello world", StopReason: llm.StopEnd, Usage: llm.Usage{InputTokens: 12, OutputTokens: 5}}
	if resp != want {
		t.Errorf("response = %+v, want %+v", resp, want)
	}

	wantRequest := captured{
		path:        "POST /v1/chat/completions",
		auth:        "Bearer " + testKey,
		contentType: "application/json",
		accept:      "text/event-stream",
		body: `{"model":"mistral-small-latest","messages":[` +
			`{"role":"system","content":"Be brief."},` +
			`{"role":"user","content":"Hi"},` +
			`{"role":"assistant","content":"Hello."},` +
			`{"role":"user","content":"Greet me again."}],` +
			`"max_tokens":100,"stream":true}`,
	}
	if got != wantRequest {
		t.Errorf("request = %+v\nwant %+v", got, wantRequest)
	}
}

// TestStreamUsageAsksForTheUsage checks the option for xAI and Ollama.
// These servers send the usage in one more chunk without choices.
func TestStreamUsageAsksForTheUsage(t *testing.T) {
	t.Parallel()
	var got captured
	server := newServer(t, []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"},"finish_reason":null}],"usage":null}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":null}`,
		`{"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":1,"total_tokens":8}}`,
	}, &got)
	provider := chatcompletions.New(server.URL, testKey)
	provider.StreamUsage = true

	resp, err := llm.Generate(t.Context(), provider, request())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	want := llm.Response{Text: "Hi", StopReason: llm.StopEnd, Usage: llm.Usage{InputTokens: 7, OutputTokens: 1}}
	if resp != want {
		t.Errorf("response = %+v, want %+v", resp, want)
	}
	if !strings.HasSuffix(got.body, `"stream":true,"stream_options":{"include_usage":true}}`) {
		t.Errorf("request body = %s, want stream_options with include_usage", got.body)
	}
}

func TestStreamWithoutKeySendsNoAuthorization(t *testing.T) {
	t.Parallel()
	var got captured
	server := newServer(t, answer("stop"), &got)

	if _, err := llm.Generate(t.Context(), chatcompletions.New(server.URL, ""), request()); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got.auth != "" {
		t.Errorf("Authorization = %q, want no header for a local server", got.auth)
	}
}

func TestStreamMapsFinishReasons(t *testing.T) {
	t.Parallel()
	tests := []struct {
		finish string
		want   llm.StopReason
	}{
		{"stop", llm.StopEnd},
		{"length", llm.StopMaxTokens},
		{"model_length", llm.StopMaxTokens},
		{"content_filter", llm.StopRefusal},
		{"tool_calls", ""},
		{"error", ""},
	}
	for _, tt := range tests {
		t.Run(tt.finish, func(t *testing.T) {
			t.Parallel()
			server := newServer(t, answer(tt.finish), nil)
			resp, err := llm.Generate(t.Context(), chatcompletions.New(server.URL, testKey), request())
			if tt.want == "" {
				if err == nil {
					t.Fatalf("Generate = %+v, want an error for finish reason %q", resp, tt.finish)
				}
				return
			}
			if err != nil || resp.StopReason != tt.want {
				t.Fatalf("StopReason = %q, err = %v, want %q", resp.StopReason, err, tt.want)
			}
		})
	}
}

func TestStreamWithoutFinishReasonFails(t *testing.T) {
	t.Parallel()
	// The connection ends after the first part of the answer.
	server := newServer(t, answer("stop")[:2], nil)
	if resp, err := llm.Generate(t.Context(), chatcompletions.New(server.URL, testKey), request()); err == nil {
		t.Fatalf("Generate = %+v, want an error for a cut stream", resp)
	}
}

// TestStreamWithoutDoneEndsWithTheBody checks a server that closes the
// stream without the [DONE] line. The finish reason came, so the answer is
// complete.
func TestStreamWithoutDoneEndsWithTheBody(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range answer("stop") {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
	}))
	t.Cleanup(server.Close)

	resp, err := llm.Generate(t.Context(), chatcompletions.New(server.URL, testKey), request())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Text != "Hello world" || resp.StopReason != llm.StopEnd {
		t.Errorf("response = %+v, want Hello world and the end", resp)
	}
}

// TestStreamRejectsALongLine checks the cap of one line (1 MiB). The
// client stops and does not keep the line in memory.
func TestStreamRejectsALongLine(t *testing.T) {
	t.Parallel()
	long := `{"choices":[{"delta":{"content":"` + strings.Repeat("a", 1<<20) + `"}}]}`
	server := newServer(t, []string{long}, nil)

	_, err := llm.Generate(t.Context(), chatcompletions.New(server.URL, testKey), request())
	if err == nil || !strings.Contains(err.Error(), "reading the stream") {
		t.Fatalf("err = %v, want an error for the long line", err)
	}
}

// TestStreamSkipsThinkingParts checks a model that thinks. Its content is
// a list of parts, and only the text parts reach the user.
func TestStreamSkipsThinkingParts(t *testing.T) {
	t.Parallel()
	server := newServer(t, []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":[{"type":"thinking","thinking":[{"type":"text","text":"The user greets me."}]}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"content":[{"type":"text","text":"Hello"}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"content":" world"},"finish_reason":"stop"}]}`,
	}, nil)

	var parts []string
	resp, err := chatcompletions.New(server.URL, testKey).Stream(t.Context(), request(), func(text string) error {
		parts = append(parts, text)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if !slices.Equal(parts, []string{"Hello", " world"}) || resp.Text != "Hello world" {
		t.Errorf("parts = %q, text = %q, want only the text parts", parts, resp.Text)
	}
}

func TestStreamStopsWhenOnTextFails(t *testing.T) {
	t.Parallel()
	server := newServer(t, answer("stop"), nil)
	stop := errors.New("client left")
	calls := 0
	_, err := chatcompletions.New(server.URL, testKey).Stream(t.Context(), request(), func(string) error {
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
	if _, err := llm.Generate(t.Context(), chatcompletions.New("http://127.0.0.1:1", testKey), req); err == nil {
		t.Fatal("Generate accepted an unknown role")
	}
}

// TestStreamErrorsHideKeyAndText checks the two error paths. The server
// repeats the question in both, and the error must not contain it.
func TestStreamErrorsHideKeyAndText(t *testing.T) {
	t.Parallel()
	const question = "Greet me again."
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{
			name: "status",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				fmt.Fprintf(w, `{"message":"invalid key secret-key for %q"}`, question)
			},
			want: "401",
		},
		{
			name: "error event",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: {\"error\":{\"message\":\"cannot answer %s\"}}\n\n", question)
			},
			want: "error event",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(tt.handler)
			t.Cleanup(server.Close)

			_, err := llm.Generate(t.Context(), chatcompletions.New(server.URL, "secret-key"), request())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want an error with %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "secret-key") || strings.Contains(err.Error(), question) {
				t.Fatalf("err = %v, it contains the key or the question", err)
			}
		})
	}
}
