package embed

import (
	"context"
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

	vecs, err := NewOpenAICompat(srv.URL, "k", "test-embed").Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 2 || len(vecs[0]) != 3 || vecs[1][2] != 0.6 {
		t.Fatalf("bad embeddings: %+v", vecs)
	}
}
