package handlers_test

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/ingest"
	"github.com/tamcore/kadence/internal/model"
)

const sampleUserID = int64(7)

type fakeDocExtractor struct{}

func (fakeDocExtractor) CanHandle(mime string) bool { return mime == "application/pdf" }
func (fakeDocExtractor) Extract(_ context.Context, _ []byte, _ string) (ingest.Result, error) {
	return ingest.Result{Markdown: "para one here.\n\npara two here.", SourceType: model.DocSourcePDF}, nil
}

type fakeDocEmbedder struct{}

func (fakeDocEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{1, 0, 0}
	}
	return out, nil
}

type fakeDocStore struct {
	nextID int64
	docs   map[int64]model.Document
}

func newFakeDocStore() *fakeDocStore {
	return &fakeDocStore{docs: map[int64]model.Document{}}
}

func (f *fakeDocStore) Create(_ context.Context, d model.Document) (model.Document, error) {
	f.nextID++
	d.ID = f.nextID
	f.docs[d.ID] = d
	return d, nil
}

func (f *fakeDocStore) ListByOwner(_ context.Context, ownerUserID int64) ([]model.Document, error) {
	var out []model.Document
	for _, d := range f.docs {
		if d.OwnerUserID != nil && *d.OwnerUserID == ownerUserID {
			out = append(out, d)
		}
	}
	return out, nil
}

func (f *fakeDocStore) ListPublic(_ context.Context) ([]model.Document, error) {
	var out []model.Document
	for _, d := range f.docs {
		if d.Scope == model.ScopePublic {
			out = append(out, d)
		}
	}
	return out, nil
}

func (f *fakeDocStore) Delete(_ context.Context, id, ownerUserID int64) error {
	if d, ok := f.docs[id]; ok && d.OwnerUserID != nil && *d.OwnerUserID == ownerUserID {
		delete(f.docs, id)
	}
	return nil
}

func (f *fakeDocStore) DeletePublic(_ context.Context, id int64) error {
	if d, ok := f.docs[id]; ok && d.Scope == model.ScopePublic {
		delete(f.docs, id)
	}
	return nil
}

type fakeChunkStore struct{}

func (fakeChunkStore) Insert(_ context.Context, _ model.Chunk, _ []float32) error { return nil }

func newDocumentsHandler(t *testing.T, maxBytes int) (*handlers.Documents, *fakeDocStore) {
	t.Helper()
	docs := newFakeDocStore()
	svc := ingest.NewService([]ingest.Extractor{fakeDocExtractor{}}, fakeDocEmbedder{}, docs, fakeChunkStore{}, 20)
	return handlers.NewDocuments(svc, docs, maxBytes), docs
}

func withDocUser(r *http.Request) *http.Request {
	return r.WithContext(auth.ContextWithUser(r.Context(), &model.User{ID: sampleUserID, Username: "u", Role: model.RoleUser}))
}

func multipartUploadRequest(t *testing.T, filename, contentType string, data []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreatePart(map[string][]string{
		"Content-Disposition": {`form-data; name="file"; filename="` + filename + `"`},
		"Content-Type":        {contentType},
	})
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/documents", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

func TestUploadSuccess(t *testing.T) {
	h, _ := newDocumentsHandler(t, 10<<20)
	data, err := os.ReadFile("../../ingest/testdata/sample.pdf")
	if err != nil {
		t.Fatalf("read sample.pdf: %v", err)
	}

	req := withDocUser(multipartUploadRequest(t, "sample.pdf", "application/pdf", data))
	rec := httptest.NewRecorder()
	h.Upload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "extracted_markdown") {
		t.Fatalf("response leaks extracted_markdown: %s", body)
	}
	if !strings.Contains(body, `"scope":"private"`) {
		t.Fatalf("expected private scope: %s", body)
	}
	if strings.Contains(body, `"id":0`) {
		t.Fatalf("expected non-zero id: %s", body)
	}
}

func TestUploadUnsupportedType(t *testing.T) {
	h, _ := newDocumentsHandler(t, 10<<20)
	req := withDocUser(multipartUploadRequest(t, "x.png", "image/png", []byte("not a real png but bytes")))
	rec := httptest.NewRecorder()
	h.Upload(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status=%d, want 415, body=%s", rec.Code, rec.Body.String())
	}
}

func TestUploadBodyTooLarge(t *testing.T) {
	h, _ := newDocumentsHandler(t, 16)
	data := bytes.Repeat([]byte("a"), 1024)
	req := withDocUser(multipartUploadRequest(t, "big.pdf", "application/pdf", data))
	rec := httptest.NewRecorder()
	h.Upload(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d, want 413, body=%s", rec.Code, rec.Body.String())
	}
}

func TestListReturnsUploadedDoc(t *testing.T) {
	h, docs := newDocumentsHandler(t, 10<<20)
	uid := sampleUserID
	if _, err := docs.Create(context.Background(), model.Document{OwnerUserID: &uid, Scope: model.ScopePrivate, Filename: "a.pdf"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := withDocUser(httptest.NewRequest(http.MethodGet, "/api/documents", nil))
	rec := httptest.NewRecorder()
	h.List(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"a.pdf"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeleteSuccess(t *testing.T) {
	h, docs := newDocumentsHandler(t, 10<<20)
	uid := sampleUserID
	created, err := docs.Create(context.Background(), model.Document{OwnerUserID: &uid, Scope: model.ScopePrivate, Filename: "a.pdf"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	req := withChiParam(withDocUser(httptest.NewRequest(http.MethodDelete, "/api/documents/"+strconv.FormatInt(created.ID, 10), nil)),
		"id", strconv.FormatInt(created.ID, 10))
	rec := httptest.NewRecorder()
	h.Delete(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := docs.docs[created.ID]; ok {
		t.Fatalf("document not deleted")
	}
}

func TestDeleteBadID(t *testing.T) {
	h, _ := newDocumentsHandler(t, 10<<20)
	req := withDocUser(httptest.NewRequest(http.MethodDelete, "/api/documents/notanid", nil))
	req = withChiParam(req, "id", "notanid")
	rec := httptest.NewRecorder()
	h.Delete(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
}
