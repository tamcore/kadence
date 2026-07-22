package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/knowledge"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
)

const (
	// topTermCount bounds how many TF-IDF terms the overview surfaces.
	topTermCount = 40
	// searchLimit bounds how many matching chunks a search returns.
	searchLimit = 20
	// snippetLen bounds the length (in runes) of content shown per snippet.
	snippetLen = 240
	// maxTermLen bounds the accepted search term length.
	maxTermLen = 64
)

// contextChunks lists and searches a user's chunk content. Satisfied by
// *store.ChunkRepository.
type contextChunks interface {
	ListContentForUser(ctx context.Context, userID int64) ([]store.ChunkRef, error)
	SearchContentForUser(ctx context.Context, userID int64, term string, limit int) ([]store.ChunkRef, error)
	ReindexStatus(ctx context.Context) (stale, total int64, err error)
}

// contextDocs lists a user's own and public documents. Satisfied by
// *store.DocumentRepository.
type contextDocs interface {
	ListByOwner(ctx context.Context, ownerUserID int64) ([]model.Document, error)
	ListPublic(ctx context.Context) ([]model.Document, error)
}

// Context handles the context explorer HTTP endpoints (overview + search
// over a user's own and public knowledge base content).
type Context struct {
	chunks contextChunks
	docs   contextDocs
}

// NewContext constructs the Context handler.
func NewContext(chunks contextChunks, docs contextDocs) *Context {
	return &Context{chunks: chunks, docs: docs}
}

type contextDocumentDTO struct {
	ID        int64  `json:"id"`
	Filename  string `json:"filename"`
	Scope     string `json:"scope"`
	CreatedAt string `json:"createdAt"`
}

type overviewResponse struct {
	DocumentCount          int                  `json:"documentCount"`
	DocumentChunkCount     int                  `json:"documentChunkCount"`
	ConversationChunkCount int                  `json:"conversationChunkCount"`
	Documents              []contextDocumentDTO `json:"documents"`
	TopTerms               []knowledge.Term     `json:"topTerms"`
	Reindex                reindexStatusDTO     `json:"reindex"`
}

type reindexStatusDTO struct {
	Stale int64 `json:"stale"`
	Total int64 `json:"total"`
}

// Overview handles GET /api/context/overview: a summary of the caller's
// knowledge base (their own documents/chunks plus any public ones), with
// TF-IDF-ranked top terms across all visible chunk content.
func (c *Context) Overview(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())

	own, err := c.docs.ListByOwner(r.Context(), u.ID)
	if err != nil {
		slog.Error("list owned documents", "err", err, "user_id", u.ID)
	}
	pub, err := c.docs.ListPublic(r.Context())
	if err != nil {
		slog.Error("list public documents", "err", err)
	}

	documents := make([]contextDocumentDTO, 0, len(own)+len(pub))
	for _, d := range own {
		documents = append(documents, toContextDocumentDTO(d))
	}
	for _, d := range pub {
		documents = append(documents, toContextDocumentDTO(d))
	}

	refs, err := c.chunks.ListContentForUser(r.Context(), u.ID)
	if err != nil {
		slog.Error("list chunk content for user", "err", err, "user_id", u.ID)
	}

	var documentChunkCount, conversationChunkCount int
	contents := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.SourceKind == model.ChunkSourceDocument {
			documentChunkCount++
		} else {
			conversationChunkCount++
		}
		contents = append(contents, ref.Content)
	}

	stale, total, err := c.chunks.ReindexStatus(r.Context())
	if err != nil {
		slog.Error("reindex status", "err", err, "user_id", u.ID)
	}

	RespondJSON(w, http.StatusOK, overviewResponse{
		DocumentCount:          len(own) + len(pub),
		DocumentChunkCount:     documentChunkCount,
		ConversationChunkCount: conversationChunkCount,
		Documents:              documents,
		TopTerms:               knowledge.TopTerms(contents, topTermCount),
		Reindex:                reindexStatusDTO{Stale: stale, Total: total},
	})
}

func toContextDocumentDTO(d model.Document) contextDocumentDTO {
	return contextDocumentDTO{
		ID:        d.ID,
		Filename:  d.Filename,
		Scope:     d.Scope,
		CreatedAt: d.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

type snippetDTO struct {
	Content    string `json:"content"`
	SourceKind string `json:"sourceKind"`
	DocumentID *int64 `json:"documentId,omitempty"`
}

type searchResponse struct {
	Term     string       `json:"term"`
	Snippets []snippetDTO `json:"snippets"`
}

// Search handles GET /api/context/search?term=...: a substring search over
// the caller's own and public chunk content, returning truncated snippets.
func (c *Context) Search(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())

	term := strings.TrimSpace(r.URL.Query().Get("term"))
	if term == "" || len(term) > maxTermLen {
		RespondError(w, http.StatusBadRequest, "term is required")
		return
	}

	refs, err := c.chunks.SearchContentForUser(r.Context(), u.ID, term, searchLimit)
	if err != nil {
		slog.Error("search chunk content for user", "err", err, "user_id", u.ID)
		RespondError(w, http.StatusInternalServerError, "could not search content")
		return
	}

	snippets := make([]snippetDTO, 0, len(refs))
	for _, ref := range refs {
		snippets = append(snippets, snippetDTO{
			Content:    truncateRunes(ref.Content, snippetLen),
			SourceKind: ref.SourceKind,
			DocumentID: ref.DocumentID,
		})
	}

	RespondJSON(w, http.StatusOK, searchResponse{Term: term, Snippets: snippets})
}

// truncateRunes truncates s to at most n runes, rune-safe (never splits a
// multi-byte UTF-8 sequence).
func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
