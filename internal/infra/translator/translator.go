// Package translator implements domain.Translator. Translators pivot through
// the OpenAI chat format: every supported source->target pair is a function
// that rewrites a JSON request body and (separately) a response stream/body.
//
// Translation is intentionally implemented as pure functions over []byte /
// io.Reader rather than full schema types. Chat payloads are large and
// permissive; round-tripping through strict struct types would drop unknown
// fields and cost CPU we don't need to spend on the hot path.
package translator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/jhon/gorouter/internal/domain"
)

// New returns a Translator wired with all registered pair handlers.
func New() domain.Translator {
	return &registry{pairs: defaultPairs}
}

type pair struct {
	translateRequest  func(upstreamModel string, body []byte) ([]byte, error)
	translateResponseJSON func(body []byte) ([]byte, error)
	translateResponseStream func(ctx context.Context, r io.ReadCloser) (io.ReadCloser, error)
}

type registry struct {
	pairs map[[2]domain.Format]pair
}

func (r *registry) Supports(from, to domain.Format) bool {
	if from == to {
		return true
	}
	_, ok := r.pairs[[2]domain.Format{from, to}]
	return ok
}

func (r *registry) TranslateRequest(from, to domain.Format, upstreamModel string, body []byte) ([]byte, error) {
	if from == to {
		return rewriteModel(body, upstreamModel)
	}
	p, ok := r.pairs[[2]domain.Format{from, to}]
	if !ok {
		return nil, fmt.Errorf("translator: %s->%s not supported", from, to)
	}
	return p.translateRequest(upstreamModel, body)
}

func (r *registry) TranslateResponseJSON(from, to domain.Format, body []byte) ([]byte, error) {
	if from == to {
		return body, nil
	}
	p, ok := r.pairs[[2]domain.Format{from, to}]
	if !ok {
		return nil, fmt.Errorf("translator: %s->%s not supported", from, to)
	}
	return p.translateResponseJSON(body)
}

func (r *registry) TranslateResponseStream(ctx context.Context, from, to domain.Format, body io.ReadCloser) (io.ReadCloser, error) {
	if from == to {
		return body, nil
	}
	p, ok := r.pairs[[2]domain.Format{from, to}]
	if !ok {
		return nil, fmt.Errorf("translator: %s->%s not supported", from, to)
	}
	return p.translateResponseStream(ctx, body)
}

// rewriteModel substitutes the "model" field of an OpenAI-format request
// body with the upstream model id. Used by passthrough (from == to).
// If the body isn't JSON (e.g. multipart audio upload), it is returned
// unchanged — the upstream will parse the model from its own format.
func rewriteModel(body []byte, upstreamModel string) ([]byte, error) {
	if upstreamModel == "" {
		return body, nil
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return body, nil
	}
	m["model"] = upstreamModel
	return json.Marshal(m)
}