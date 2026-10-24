package llm

import (
	"context"
	"fmt"
	"maps"
	"strings"
)

// Router sends each request to a provider by its model reference. A
// reference has the form "provider/model", for example
// "anthropic/claude-opus-5-5". The model part can contain more slashes.
type Router struct {
	providers map[string]Provider
}

var _ Provider = (*Router)(nil)

// NewRouter returns a Router for providers. The map key is the provider
// name in a model reference.
func NewRouter(providers map[string]Provider) *Router {
	return &Router{providers: maps.Clone(providers)}
}

// Resolve returns the provider and the bare model name for ref. Call it at
// startup to check the configured model references.
func (r *Router) Resolve(ref string) (Provider, string, error) {
	name, model, ok := strings.Cut(ref, "/")
	if !ok || name == "" || model == "" {
		return nil, "", fmt.Errorf("model %q must have the form provider/model", ref)
	}
	p, ok := r.providers[name]
	if !ok {
		return nil, "", fmt.Errorf("%w %q in model %q", ErrUnknownProvider, name, ref)
	}
	return p, model, nil
}

// Stream sends req to the provider that req.Model names.
func (r *Router) Stream(ctx context.Context, req Request, onText func(string) error) (Response, error) {
	p, model, err := r.Resolve(req.Model)
	if err != nil {
		return Response{}, err
	}
	req.Model = model
	return p.Stream(ctx, req, onText)
}
