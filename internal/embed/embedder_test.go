package embed

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Minimal OpenAI-compatible embeddings response: 2 vectors for 2 inputs.
const embBody = `{"object":"list","data":[` +
	`{"object":"embedding","index":0,"embedding":[0.1,0.2,0.3]},` +
	`{"object":"embedding","index":1,"embedding":[0.4,0.5,0.6]}],` +
	`"model":"test-embed","usage":{"prompt_tokens":2,"total_tokens":2}}`

func TestOpenAICompatEmbed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(embBody))
	}))
	defer srv.Close()

	vecs, err := NewOpenAICompat(srv.URL, "k", "test-embed", 0).Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 2 || len(vecs[0]) != 3 || vecs[1][2] != 0.6 {
		t.Fatalf("bad embeddings: %+v", vecs)
	}
}

// TestOpenAICompatEmbed_SendsDimensionsField verifies that when a positive
// dimensions value is configured, it is sent as the OpenAI-compat
// "dimensions" request field.
func TestOpenAICompatEmbed_SendsDimensionsField(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(embBody))
	}))
	defer srv.Close()

	if _, err := NewOpenAICompat(srv.URL, "k", "test-embed", 3).Embed(context.Background(), []string{"a", "b"}); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	dims, ok := gotBody["dimensions"]
	if !ok {
		t.Fatalf("request body missing dimensions field: %+v", gotBody)
	}
	if dims != float64(3) {
		t.Fatalf("dimensions = %v, want 3", dims)
	}
}

// TestOpenAICompatEmbed_TruncatesWhenProviderIgnoresDimensions covers the
// fallback path: the provider returns a longer vector than requested (it
// ignored/doesn't support "dimensions"), so the client truncates to N and
// L2-renormalizes. Valid for Matryoshka(MRL)-trained embedding models, where
// any length-N prefix of the full embedding is itself a well-formed
// embedding.
func TestOpenAICompatEmbed_TruncatesWhenProviderIgnoresDimensions(t *testing.T) {
	// 6-dim response even though we ask for 3.
	body := `{"object":"list","data":[` +
		`{"object":"embedding","index":0,"embedding":[3,4,0,1,2,2]}],` +
		`"model":"test-embed","usage":{"prompt_tokens":1,"total_tokens":1}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	vecs, err := NewOpenAICompat(srv.URL, "k", "test-embed", 3).Embed(context.Background(), []string{"a"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 1 || len(vecs[0]) != 3 {
		t.Fatalf("expected 1 vector truncated to 3 dims, got %+v", vecs)
	}
	// Prefix property: truncated vector should be proportional to the
	// original prefix [3,4,0] (renormalized to unit length).
	wantNorm := math.Sqrt(3*3 + 4*4)
	want := []float32{3 / float32(wantNorm), 4 / float32(wantNorm), 0}
	for i, w := range want {
		if math.Abs(float64(vecs[0][i]-w)) > 1e-6 {
			t.Fatalf("vecs[0][%d] = %v, want %v", i, vecs[0][i], w)
		}
	}
	var norm float64
	for _, f := range vecs[0] {
		norm += float64(f) * float64(f)
	}
	if math.Abs(math.Sqrt(norm)-1) > 1e-6 {
		t.Fatalf("‖v‖ = %v, want ≈1", math.Sqrt(norm))
	}
}

// TestOpenAICompatEmbed_ErrorsOnShortVector covers the fail-loud guard: if
// the provider returns fewer dimensions than configured, that vector cannot
// satisfy the pinned-dimension contract (padding would fabricate data), so
// Embed must return a clear error rather than silently accepting it.
func TestOpenAICompatEmbed_ErrorsOnShortVector(t *testing.T) {
	body := `{"object":"list","data":[` +
		`{"object":"embedding","index":0,"embedding":[0.1,0.2]}],` +
		`"model":"test-embed","usage":{"prompt_tokens":1,"total_tokens":1}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	_, err := NewOpenAICompat(srv.URL, "k", "test-embed", 3).Embed(context.Background(), []string{"a"})
	if err == nil {
		t.Fatal("Embed() = nil error, want error for short vector vs configured dimensions")
	}
}
