package embed

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

// rejectDimensionsServer simulates a provider that returns HTTP 400 for any
// request carrying the "dimensions" field, and otherwise returns one
// full-length (fullDims) embedding per input. It counts total requests
// received so tests can assert the sticky fallback avoids repeating the
// doomed with-dimensions attempt on subsequent calls.
func rejectDimensionsServer(t *testing.T, fullDims int) (*httptest.Server, *int32) {
	t.Helper()
	var requestCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, hasDimensions := body["dimensions"]; hasDimensions {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"upstream-service-error","type":"invalid_request_error"}}`))
			return
		}
		embedding := make([]float64, fullDims)
		for i := range embedding {
			embedding[i] = 1 // uniform vector; only used to check truncation/renorm plumbing
		}
		resp := map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"object": "embedding", "index": 0, "embedding": embedding},
			},
			"model": "test-embed",
			"usage": map[string]any{"prompt_tokens": 1, "total_tokens": 1},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	return srv, &requestCount
}

// TestOpenAICompatEmbed_FallsBackWhenProviderRejectsDimensions covers the
// live-verified defect: some OpenAI-compatible providers reject any request
// carrying an unsupported "dimensions" field with HTTP 400, instead of
// merely ignoring it. Embed must retry once without "dimensions" and
// truncate/renormalize the full-length response client-side.
func TestOpenAICompatEmbed_FallsBackWhenProviderRejectsDimensions(t *testing.T) {
	const fullDims = 4096
	const wantDims = 1024
	srv, requestCount := rejectDimensionsServer(t, fullDims)
	defer srv.Close()

	e := NewOpenAICompat(srv.URL, "k", "test-embed", wantDims)
	vecs, err := e.Embed(context.Background(), []string{"a"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 1 || len(vecs[0]) != wantDims {
		t.Fatalf("expected 1 vector of %d dims, got %+v", wantDims, vecs)
	}
	var norm float64
	for _, f := range vecs[0] {
		norm += float64(f) * float64(f)
	}
	if math.Abs(math.Sqrt(norm)-1) > 1e-6 {
		t.Fatalf("‖v‖ = %v, want ≈1", math.Sqrt(norm))
	}
	if got := atomic.LoadInt32(requestCount); got != 2 {
		t.Fatalf("first Embed call made %d requests, want 2 (rejected-with-dimensions + retry-without)", got)
	}

	// Second call: the sticky fallback should skip the doomed
	// with-dimensions attempt entirely, issuing exactly one request.
	if _, err := e.Embed(context.Background(), []string{"b"}); err != nil {
		t.Fatalf("second Embed: %v", err)
	}
	if got := atomic.LoadInt32(requestCount); got != 3 {
		t.Fatalf("after second Embed call, total requests = %d, want 3 (2 from first call + 1 sticky request)", got)
	}
}
