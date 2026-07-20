package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
)

const contextTestUserID = int64(42)

type fakeContextChunks struct {
	refs      []store.ChunkRef
	searchErr error
}

func (f *fakeContextChunks) ListContentForUser(_ context.Context, _ int64) ([]store.ChunkRef, error) {
	return f.refs, nil
}

func (f *fakeContextChunks) SearchContentForUser(_ context.Context, _ int64, term string, limit int) ([]store.ChunkRef, error) {
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	var out []store.ChunkRef
	for _, ref := range f.refs {
		if strings.Contains(ref.Content, term) {
			out = append(out, ref)
		}
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

type fakeContextDocs struct {
	own    []model.Document
	public []model.Document
}

func (f *fakeContextDocs) ListByOwner(_ context.Context, _ int64) ([]model.Document, error) {
	return f.own, nil
}

func (f *fakeContextDocs) ListPublic(_ context.Context) ([]model.Document, error) {
	return f.public, nil
}

func withContextUser(r *http.Request) *http.Request {
	return r.WithContext(auth.ContextWithUser(r.Context(), &model.User{ID: contextTestUserID, Username: "u", Role: model.RoleUser}))
}

func TestOverviewReturnsCountsAndTopTerms(t *testing.T) {
	docID := int64(1)
	chunks := &fakeContextChunks{refs: []store.ChunkRef{
		{Content: "widget alpha widget beta", SourceKind: model.ChunkSourceDocument, DocumentID: &docID},
		{Content: "conversation message here", SourceKind: model.ChunkSourceMessage},
	}}
	ownerID := contextTestUserID
	docs := &fakeContextDocs{
		own:    []model.Document{{ID: docID, OwnerUserID: &ownerID, Scope: model.ScopePrivate, Filename: "own.pdf"}},
		public: []model.Document{{ID: 2, Scope: model.ScopePublic, Filename: "shared.pdf"}},
	}
	h := handlers.NewContext(chunks, docs)

	req := withContextUser(httptest.NewRequest(http.MethodGet, "/api/context/overview", nil))
	rec := httptest.NewRecorder()
	h.Overview(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		Data struct {
			DocumentCount          int `json:"documentCount"`
			DocumentChunkCount     int `json:"documentChunkCount"`
			ConversationChunkCount int `json:"conversationChunkCount"`
			Documents              []struct {
				ID       int64  `json:"id"`
				Filename string `json:"filename"`
			} `json:"documents"`
			TopTerms []struct {
				Term string `json:"term"`
			} `json:"topTerms"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, rec.Body.String())
	}

	if body.Data.DocumentCount != 2 {
		t.Fatalf("expected documentCount=2, got %d", body.Data.DocumentCount)
	}
	if body.Data.DocumentChunkCount != 1 {
		t.Fatalf("expected documentChunkCount=1, got %d", body.Data.DocumentChunkCount)
	}
	if body.Data.ConversationChunkCount != 1 {
		t.Fatalf("expected conversationChunkCount=1, got %d", body.Data.ConversationChunkCount)
	}
	if len(body.Data.Documents) != 2 {
		t.Fatalf("expected 2 documents in overview, got %d", len(body.Data.Documents))
	}
	if len(body.Data.TopTerms) == 0 {
		t.Fatalf("expected top terms derived from seeded chunk content, got none")
	}
	found := false
	for _, term := range body.Data.TopTerms {
		if term.Term == "widget" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 'widget' among top terms: %+v", body.Data.TopTerms)
	}
}

func TestOverviewRequiresUser(t *testing.T) {
	h := handlers.NewContext(&fakeContextChunks{}, &fakeContextDocs{})
	req := httptest.NewRequest(http.MethodGet, "/api/context/overview", nil)
	rec := httptest.NewRecorder()
	h.Overview(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", rec.Code)
	}
}

func TestSearchEmptyTermReturns400(t *testing.T) {
	h := handlers.NewContext(&fakeContextChunks{}, &fakeContextDocs{})
	req := withContextUser(httptest.NewRequest(http.MethodGet, "/api/context/search?term=", nil))
	rec := httptest.NewRecorder()
	h.Search(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func TestSearchOversizedTermReturns400(t *testing.T) {
	h := handlers.NewContext(&fakeContextChunks{}, &fakeContextDocs{})
	longTerm := strings.Repeat("a", 65)
	req := withContextUser(httptest.NewRequest(http.MethodGet, "/api/context/search?term="+longTerm, nil))
	rec := httptest.NewRecorder()
	h.Search(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func TestSearchRequiresUser(t *testing.T) {
	h := handlers.NewContext(&fakeContextChunks{}, &fakeContextDocs{})
	req := httptest.NewRequest(http.MethodGet, "/api/context/search?term=foo", nil)
	rec := httptest.NewRecorder()
	h.Search(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", rec.Code)
	}
}

func TestSearchReturnsTruncatedSnippets(t *testing.T) {
	longContent := strings.Repeat("x", 500) + "needle" + strings.Repeat("y", 500)
	chunks := &fakeContextChunks{refs: []store.ChunkRef{
		{Content: longContent, SourceKind: model.ChunkSourceDocument},
	}}
	h := handlers.NewContext(chunks, &fakeContextDocs{})

	req := withContextUser(httptest.NewRequest(http.MethodGet, "/api/context/search?term=needle", nil))
	rec := httptest.NewRecorder()
	h.Search(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		Data struct {
			Term     string `json:"term"`
			Snippets []struct {
				Content    string `json:"content"`
				SourceKind string `json:"sourceKind"`
			} `json:"snippets"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v, body=%s", err, rec.Body.String())
	}
	if body.Data.Term != "needle" {
		t.Fatalf("expected term echoed back, got %q", body.Data.Term)
	}
	if len(body.Data.Snippets) != 1 {
		t.Fatalf("expected 1 snippet, got %d", len(body.Data.Snippets))
	}
	const snippetLen = 240
	if len([]rune(body.Data.Snippets[0].Content)) > snippetLen {
		t.Fatalf("expected content truncated to %d runes, got %d", snippetLen, len([]rune(body.Data.Snippets[0].Content)))
	}
}
