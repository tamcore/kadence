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
	inserted          []model.Chunk
	search            []model.Chunk
	documentSearch    map[int64][]model.Chunk
	searchCalls       int
	documentCalls     int
	documentEmbedding []float32
}

func (f *fakeChunks) Insert(_ context.Context, c model.Chunk, _ []float32) error {
	f.inserted = append(f.inserted, c)
	return nil
}
func (f *fakeChunks) SearchTopK(_ context.Context, _ int64, _ []float32, _ int) ([]model.Chunk, error) {
	f.searchCalls++
	return f.search, nil
}
func (f *fakeChunks) SearchTopKByVisibleDocuments(
	_ context.Context, _ int64, _ []int64, embedding []float32, _ int,
) (map[int64][]model.Chunk, error) {
	f.documentCalls++
	f.documentEmbedding = append([]float32(nil), embedding...)
	return f.documentSearch, nil
}

func TestRAGRetrieveTurnReturnsContentsAndEmbedding(t *testing.T) {
	fc := &fakeChunks{search: []model.Chunk{{Content: "you ran 10k last week"}}}
	rag := chat.NewRAG(&fakeEmbedder{}, fc, 5)
	retrieval, err := rag.RetrieveTurn(context.Background(), 1, "how was my run?", nil)
	if err != nil || len(retrieval.Broad) != 1 || retrieval.Broad[0] != "you ran 10k last week" ||
		len(retrieval.Embedding) != 3 {
		t.Fatalf("retrieve turn: %v %+v", err, retrieval)
	}
}

func TestRAGStorePrivateMessageChunk(t *testing.T) {
	fc := &fakeChunks{}
	rag := chat.NewRAG(&fakeEmbedder{}, fc, 5)
	const convID = "11111111-1111-1111-1111-111111111111"
	if err := rag.Store(context.Background(), 7, convID, 9, "hello", []float32{1, 0, 0}); err != nil {
		t.Fatalf("store: %v", err)
	}
	c := fc.inserted[0]
	if c.UserID == nil || *c.UserID != 7 || c.ConversationID == nil || *c.ConversationID != convID || *c.SourceID != 9 || c.Scope != model.ScopePrivate || c.Content != "hello" {
		t.Fatalf("bad chunk: %+v", c)
	}
}

func TestRAGRetrieveTurnReusesEmbeddingAndExcludesExplicitDocumentsFromBroadResults(t *testing.T) {
	explicitID := int64(44)
	otherID := int64(55)
	chunks := &fakeChunks{
		search: []model.Chunk{
			{DocumentID: &explicitID, Content: "duplicate selected chunk"},
			{DocumentID: &otherID, Content: "broad note"},
			{Content: "conversation memory"},
		},
		documentSearch: map[int64][]model.Chunk{
			explicitID: {{DocumentID: &explicitID, Content: "selected relevant section"}},
		},
	}
	embedder := &fakeEmbedder{}
	rag := chat.NewRAG(embedder, chunks, 5)

	got, err := rag.RetrieveTurn(context.Background(), 7, "marathon pacing", []int64{explicitID})
	if err != nil {
		t.Fatalf("RetrieveTurn: %v", err)
	}
	if embedder.calls != 1 || chunks.searchCalls != 1 || chunks.documentCalls != 1 {
		t.Fatalf(
			"calls: embed=%d broad=%d documents=%d, want one each",
			embedder.calls, chunks.searchCalls, chunks.documentCalls,
		)
	}
	if len(got.Embedding) != 3 ||
		len(chunks.documentEmbedding) != 3 ||
		chunks.documentEmbedding[0] != got.Embedding[0] {
		t.Fatalf("embedding was not reused: retrieval=%v document=%v", got.Embedding, chunks.documentEmbedding)
	}
	if len(got.Broad) != 2 ||
		got.Broad[0] != "broad note" ||
		got.Broad[1] != "conversation memory" {
		t.Fatalf("broad results = %v", got.Broad)
	}
	if len(got.ByDocument[explicitID]) != 1 ||
		got.ByDocument[explicitID][0] != "selected relevant section" {
		t.Fatalf("document results = %+v", got.ByDocument)
	}
}
