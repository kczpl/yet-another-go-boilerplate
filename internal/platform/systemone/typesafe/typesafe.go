// Package typesafe is the systemone provider for the System One models of
// TypeSafe, for example Jev. TypeSafe has SDKs only for Python and
// JavaScript, so the package calls the HTTP API with net/http.
package typesafe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/kczpl/yet-another-go-boilerplate/internal/platform/systemone"
)

// BaseURL is the address of the TypeSafe API.
const BaseURL = "https://api.typesafe.ai"

// maxResponseBytes caps the response body that Ask reads.
const maxResponseBytes = 1 << 20

// Provider sends requests to the System One endpoint. It does not retry.
// The feature sets the deadline of each call through ctx.
type Provider struct {
	endpoint string
	apiKey   string
}

var _ systemone.Provider = (*Provider)(nil)

// New returns a Provider that sends requests to baseURL with apiKey.
func New(baseURL, apiKey string) *Provider {
	return &Provider{
		endpoint: strings.TrimSuffix(baseURL, "/") + "/v1/systemone",
		apiKey:   apiKey,
	}
}

// The wire types of the API. A choice sends its options as a map of
// criteria, a score sends its levels as a list, and a noul sends none.
type (
	apiRequest struct {
		State     string                 `json:"state"`
		Model     string                 `json:"model"`
		Questions map[string]apiQuestion `json:"questions"`
	}
	apiQuestion struct {
		Type         string `json:"type"`
		Instructions string `json:"instructions"`
		Criteria     any    `json:"criteria,omitempty"`
	}
	apiResponse struct {
		Model   string               `json:"model"`
		Answers map[string]apiAnswer `json:"answers"`
		Usage   struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	apiAnswer struct {
		Type          string             `json:"type"`
		Choice        string             `json:"choice"`
		Score         float64            `json:"score"`
		Noul          float64            `json:"noul"`
		Confidence    float64            `json:"confidence"`
		Probabilities map[string]float64 `json:"probabilities"`
	}
)

// Ask implements systemone.Provider.
func (p *Provider) Ask(ctx context.Context, req systemone.Request) (systemone.Response, error) {
	body, err := newRequestBody(req)
	if err != nil {
		return systemone.Response{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return systemone.Response{}, fmt.Errorf("typesafe: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return systemone.Response{}, fmt.Errorf("typesafe: %w", err)
	}
	defer resp.Body.Close()
	// The error body can repeat the state, so the error has only the
	// status.
	if resp.StatusCode != http.StatusOK {
		return systemone.Response{}, fmt.Errorf("typesafe: status %d", resp.StatusCode)
	}
	var out apiResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&out); err != nil {
		return systemone.Response{}, fmt.Errorf("typesafe: reading the response: %w", err)
	}
	return newResponse(out)
}

// newRequestBody maps req to the JSON body of the API.
func newRequestBody(req systemone.Request) ([]byte, error) {
	out := apiRequest{
		State:     req.State,
		Model:     req.Model,
		Questions: make(map[string]apiQuestion, len(req.Questions)),
	}
	for id, q := range req.Questions {
		question := apiQuestion{Type: string(q.Type), Instructions: q.Instructions}
		switch q.Type {
		case systemone.Choice:
			question.Criteria = q.Options
		case systemone.Score:
			question.Criteria = q.Levels
		case systemone.Noul:
		default:
			return nil, fmt.Errorf("typesafe: question %q: unknown type %q", id, q.Type)
		}
		out.Questions[id] = question
	}
	return json.Marshal(out)
}

// newResponse maps the response of the API to systemone.Response. The
// feature checks the values of the answers.
func newResponse(in apiResponse) (systemone.Response, error) {
	out := systemone.Response{
		Model:   in.Model,
		Answers: make(map[string]systemone.Answer, len(in.Answers)),
		Usage:   systemone.Usage{InputTokens: in.Usage.InputTokens, OutputTokens: in.Usage.OutputTokens},
	}
	for id, a := range in.Answers {
		answerType := systemone.QuestionType(a.Type)
		switch answerType {
		case systemone.Choice, systemone.Score, systemone.Noul:
		default:
			return systemone.Response{}, fmt.Errorf("typesafe: answer %q: unknown type %q", id, a.Type)
		}
		out.Answers[id] = systemone.Answer{
			Type:          answerType,
			Choice:        a.Choice,
			Score:         a.Score,
			Noul:          a.Noul,
			Probabilities: a.Probabilities,
			Confidence:    a.Confidence,
		}
	}
	return out, nil
}
