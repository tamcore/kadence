// Package embed abstracts text embedding behind a pluggable interface.
package embed

import (
	"context"
	"fmt"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// Embedder turns texts into embedding vectors.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// OpenAICompat is an Embedder backed by any OpenAI-compatible embeddings API.
type OpenAICompat struct {
	client openai.Client
	model  string
}

// NewOpenAICompat builds an embedder for the given base URL + key + model.
func NewOpenAICompat(baseURL, apiKey, model string) *OpenAICompat {
	return &OpenAICompat{
		client: openai.NewClient(option.WithBaseURL(baseURL), option.WithAPIKey(apiKey)),
		model:  model,
	}
}

// Embed returns one vector per input text, in order.
func (e *OpenAICompat) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	resp, err := e.client.Embeddings.New(ctx, openai.EmbeddingNewParams{
		Model: e.model,
		Input: openai.EmbeddingNewParamsInputUnion{OfArrayOfStrings: texts},
	})
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	out := make([][]float32, len(resp.Data))
	for i, d := range resp.Data {
		v := make([]float32, len(d.Embedding))
		for j, f := range d.Embedding {
			v[j] = float32(f)
		}
		out[i] = v
	}
	return out, nil
}
