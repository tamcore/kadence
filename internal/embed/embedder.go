// Package embed abstracts text embedding behind a pluggable interface.
package embed

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync/atomic"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
)

// Embedder turns texts into embedding vectors.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// OpenAICompat is an Embedder backed by any OpenAI-compatible embeddings API.
type OpenAICompat struct {
	client     openai.Client
	model      string
	dimensions int

	// skipDimensions is latched (for the process lifetime) once the
	// provider is observed to reject requests carrying the "dimensions"
	// field (rather than merely ignoring it). Once latched, subsequent
	// Embed calls skip the doomed with-dimensions attempt and go straight
	// to the no-dimensions request + client-side truncateAndRenormalize.
	// Embed may be called concurrently against the same *OpenAICompat, so
	// this needs atomic access rather than a plain bool.
	skipDimensions atomic.Bool
}

// NewOpenAICompat builds an embedder for the given base URL + key + model.
// dimensions pins the returned vector length (sent as the OpenAI-compat
// "dimensions" request field); 0 leaves the provider's default dimensionality
// unpinned and disables all length validation/truncation below.
func NewOpenAICompat(baseURL, apiKey, model string, dimensions int) *OpenAICompat {
	return &OpenAICompat{
		client:     openai.NewClient(option.WithBaseURL(baseURL), option.WithAPIKey(apiKey)),
		model:      model,
		dimensions: dimensions,
	}
}

// Embed returns one vector per input text, in order. When dimensions is
// pinned (> 0), every returned vector is validated/normalized to exactly that
// length:
//   - equal length: used as-is (provider honored the "dimensions" field).
//   - longer: client-side truncated to the first N dimensions and
//     L2-renormalized. This is only valid for Matryoshka(MRL)-trained
//     embedding models, where any length-N prefix of the full embedding is
//     itself a well-formed embedding (e.g. Qwen3 embedding family).
//   - shorter: fails loud with an error rather than silently padding, since a
//     padded vector would fabricate data and corrupt the pinned-dimension
//     column/index contract.
func (e *OpenAICompat) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	// Only attempt the "dimensions" field if it's configured and the
	// provider hasn't already been observed to reject it outright.
	sendDimensions := e.dimensions > 0 && !e.skipDimensions.Load()
	resp, err := e.embedRequest(ctx, texts, sendDimensions)
	if err != nil && sendDimensions {
		// Some OpenAI-compatible providers reject any request carrying an
		// unsupported "dimensions" field with a hard 4xx error, rather than
		// silently ignoring it (which the truncate/renormalize path below
		// already handles). Retry once without the field; if that succeeds,
		// latch skipDimensions so every later call goes straight to the
		// no-dimensions request instead of re-paying for a doomed attempt.
		var retryErr error
		resp, retryErr = e.embedRequest(ctx, texts, false)
		if retryErr != nil {
			return nil, fmt.Errorf("embed: %w", err)
		}
		e.skipDimensions.Store(true)
		slog.Info("provider rejected dimensions param; using client-side truncation", "model", e.model)
	} else if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	out := make([][]float32, len(resp.Data))
	for i, d := range resp.Data {
		v := make([]float32, len(d.Embedding))
		for j, f := range d.Embedding {
			v[j] = float32(f)
		}
		fitted, err := e.fitDimensions(v)
		if err != nil {
			return nil, fmt.Errorf("embed: input %d: %w", i, err)
		}
		out[i] = fitted
	}
	return out, nil
}

// embedRequest issues one embeddings call, including the OpenAI-compat
// "dimensions" field only when withDimensions is true.
func (e *OpenAICompat) embedRequest(ctx context.Context, texts []string, withDimensions bool) (*openai.CreateEmbeddingResponse, error) {
	params := openai.EmbeddingNewParams{
		Model: e.model,
		Input: openai.EmbeddingNewParamsInputUnion{OfArrayOfStrings: texts},
	}
	if withDimensions {
		params.Dimensions = param.NewOpt(int64(e.dimensions))
	}
	return e.client.Embeddings.New(ctx, params)
}

// fitDimensions enforces the pinned dimension contract on a single vector.
// The e.dimensions <= 0 short-circuit must stay first: it implements the
// client-side "0 disables" contract (no dimensions field sent, no truncation).
func (e *OpenAICompat) fitDimensions(v []float32) ([]float32, error) {
	if e.dimensions <= 0 || len(v) == e.dimensions {
		return v, nil
	}
	if len(v) < e.dimensions {
		return nil, fmt.Errorf("embed dimensions mismatch: got %d-dim vector, want %d (provider returned fewer dimensions than configured KADENCE_EMBED_DIMENSIONS)",
			len(v), e.dimensions)
	}
	return truncateAndRenormalize(v, e.dimensions), nil
}

// truncateAndRenormalize takes the first n dimensions of v and L2-renormalizes
// the result. Valid only for Matryoshka(MRL)-trained embeddings, whose
// prefixes are themselves valid embeddings.
func truncateAndRenormalize(v []float32, n int) []float32 {
	out := make([]float32, n)
	copy(out, v[:n])
	var sumSquares float64
	for _, f := range out {
		sumSquares += float64(f) * float64(f)
	}
	norm := math.Sqrt(sumSquares)
	if norm == 0 {
		return out
	}
	for i, f := range out {
		out[i] = float32(float64(f) / norm)
	}
	return out
}
