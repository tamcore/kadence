package chat_test

import (
	"context"
	"testing"

	"github.com/tamcore/kadence/internal/chat"
	"github.com/tamcore/kadence/internal/model"
)

type fakeEmbedder struct{ calls int }

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	f.calls++
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{1, 0, 0}
	}
	return out, nil
}

type fakeChunks struct {
	inserted []model.Chunk
	search   []model.Chunk
}

func (f *fakeChunks) Insert(_ context.Context, c model.Chunk, _ []float32) error {
	f.inserted = append(f.inserted, c)
	return nil
}
func (f *fakeChunks) SearchTopK(_ context.Context, _ int64, _ []float32, _ int) ([]model.Chunk, error) {
	return f.search, nil
}

func TestRAGRetrieveReturnsContentsAndEmbedding(t *testing.T) {
	fc := &fakeChunks{search: []model.Chunk{{Content: "you ran 10k last week"}}}
	rag := chat.NewRAG(&fakeEmbedder{}, fc, 5)
	contents, emb, err := rag.Retrieve(context.Background(), 1, "how was my run?")
	if err != nil || len(contents) != 1 || contents[0] != "you ran 10k last week" || len(emb) != 3 {
		t.Fatalf("retrieve: %v %v %v", err, contents, emb)
	}
}

func TestRAGStorePrivateMessageChunk(t *testing.T) {
	fc := &fakeChunks{}
	rag := chat.NewRAG(&fakeEmbedder{}, fc, 5)
	if err := rag.Store(context.Background(), 7, 3, 9, "hello", []float32{1, 0, 0}); err != nil {
		t.Fatalf("store: %v", err)
	}
	c := fc.inserted[0]
	if c.UserID != 7 || *c.ConversationID != 3 || *c.SourceID != 9 || c.Scope != model.ScopePrivate || c.Content != "hello" {
		t.Fatalf("bad chunk: %+v", c)
	}
}
