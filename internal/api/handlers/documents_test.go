package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
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

func (fakeDocExtractor) CanHandle(mime string) bool { return mime == testMimePDF }
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
		if d.Scope == model.ScopePrivate &&
			d.OwnerUserID != nil &&
			*d.OwnerUserID == ownerUserID {
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

type recordingDocIngester struct {
	mime string
}

func (f *recordingDocIngester) Ingest(
	_ context.Context,
	_ *int64,
	scope, filename, mime string,
	_ []byte,
) (model.Document, error) {
	f.mime = mime
	return model.Document{ID: 1, Scope: scope, Filename: filename, Mime: mime}, nil
}

func newDocumentsHandler(t *testing.T, maxBytes int) (*handlers.Documents, *fakeDocStore) {
	t.Helper()
	docs := newFakeDocStore()
	extractors := []ingest.Extractor{fakeDocExtractor{}}
	svc := ingest.NewService(extractors, fakeDocEmbedder{}, docs, fakeChunkStore{}, 20)
	capabilities := ingest.BuildUploadCapabilities(extractors, maxBytes)
	return handlers.NewDocuments(svc, docs, capabilities), docs
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

	req := withDocUser(multipartUploadRequest(t, "sample.pdf", testMimePDF, data))
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

func TestReferenceOptionsGroupsOwnAndPublicDocumentsWithoutContent(t *testing.T) {
	handler, documents := newDocumentsHandler(t, 10<<20)
	ownerID := sampleUserID
	otherOwnerID := int64(99)
	documents.docs = map[int64]model.Document{
		1: {
			ID: 1, OwnerUserID: &ownerID, Scope: model.ScopePrivate,
			Filename: "my-plan.md", Mime: testMimeMarkdown, SourceType: model.DocSourceText,
			ExtractedMarkdown: "private content must not leak",
		},
		2: {
			ID: 2, Scope: model.ScopePublic,
			Filename: "public-guide.pdf", Mime: testMimePDF, SourceType: model.DocSourcePDF,
			ExtractedMarkdown: "public content must not leak",
		},
		3: {
			ID: 3, OwnerUserID: &otherOwnerID, Scope: model.ScopePrivate,
			Filename: "other-user.md", Mime: testMimeMarkdown, SourceType: model.DocSourceText,
		},
		4: {
			ID: 4, OwnerUserID: &ownerID, Scope: model.ScopePublic,
			Filename: "owned-public.md", Mime: testMimeMarkdown, SourceType: model.DocSourceText,
		},
	}
	request := withDocUser(httptest.NewRequest(
		http.MethodGet, "/api/documents/references", nil,
	))
	response := httptest.NewRecorder()

	handler.ReferenceOptions(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "content must not leak") ||
		strings.Contains(response.Body.String(), "other-user.md") {
		t.Fatalf("reference options leaked content or invisible document: %s", response.Body.String())
	}
	var envelope struct {
		Data struct {
			Own []struct {
				ID         int64  `json:"id"`
				Filename   string `json:"filename"`
				MIME       string `json:"mime"`
				SourceType string `json:"source_type"`
				Scope      string `json:"scope"`
			} `json:"own"`
			Public []struct {
				ID       int64  `json:"id"`
				Filename string `json:"filename"`
				Scope    string `json:"scope"`
			} `json:"public"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(envelope.Data.Own) != 1 ||
		envelope.Data.Own[0].ID != 1 ||
		envelope.Data.Own[0].Filename != "my-plan.md" ||
		envelope.Data.Own[0].MIME != testMimeMarkdown ||
		envelope.Data.Own[0].SourceType != model.DocSourceText ||
		envelope.Data.Own[0].Scope != model.ScopePrivate {
		t.Fatalf("own options=%+v", envelope.Data.Own)
	}
	if len(envelope.Data.Public) != 2 {
		t.Fatalf("public options=%+v", envelope.Data.Public)
	}
	publicByID := make(map[int64]string, len(envelope.Data.Public))
	for _, document := range envelope.Data.Public {
		if document.Scope != model.ScopePublic {
			t.Fatalf("non-public document in public group: %+v", document)
		}
		publicByID[document.ID] = document.Filename
	}
	if publicByID[2] != "public-guide.pdf" || publicByID[4] != "owned-public.md" {
		t.Fatalf("public options=%+v", envelope.Data.Public)
	}
}

func TestUploadUnsupportedType(t *testing.T) {
	h, _ := newDocumentsHandler(t, 10<<20)
	req := withDocUser(multipartUploadRequest(t, "x.png", testMimePNG, []byte("not a real png but bytes")))
	rec := httptest.NewRecorder()
	h.Upload(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status=%d, want 415, body=%s", rec.Code, rec.Body.String())
	}
}

func TestUploadBodyTooLarge(t *testing.T) {
	h, _ := newDocumentsHandler(t, 16)
	data := bytes.Repeat([]byte("a"), 1024)
	req := withDocUser(multipartUploadRequest(t, "big.pdf", testMimePDF, data))
	rec := httptest.NewRecorder()
	h.Upload(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d, want 413, body=%s", rec.Code, rec.Body.String())
	}
}

func TestUploadAcceptsFileAtExactSizeLimit(t *testing.T) {
	const maxBytes = 1024
	h, _ := newDocumentsHandler(t, maxBytes)
	req := withDocUser(multipartUploadRequest(
		t,
		"exact.pdf",
		testMimePDF,
		bytes.Repeat([]byte("a"), maxBytes),
	))
	rec := httptest.NewRecorder()

	h.Upload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200 for exact-size file, body=%s", rec.Code, rec.Body.String())
	}
}

func TestUploadRejectsPreparsedFileOneByteAboveSizeLimit(t *testing.T) {
	const maxBytes = 1024
	h, _ := newDocumentsHandler(t, maxBytes)
	req := multipartUploadRequest(
		t,
		"too-big.pdf",
		testMimePDF,
		bytes.Repeat([]byte("a"), maxBytes+1),
	)
	if err := req.ParseMultipartForm(1); err != nil {
		t.Fatalf("preparse multipart form: %v", err)
	}
	req = withDocUser(req)
	rec := httptest.NewRecorder()

	h.Upload(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d, want 413 for max+1 file, body=%s", rec.Code, rec.Body.String())
	}
}

func TestUploadRemovesMultipartTemporaryFiles(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	h, _ := newDocumentsHandler(t, 8<<10)
	req := multipartUploadRequest(
		t,
		"temporary.pdf",
		testMimePDF,
		bytes.Repeat([]byte("a"), 4<<10),
	)
	if err := req.ParseMultipartForm(1); err != nil {
		t.Fatalf("preparse multipart form: %v", err)
	}
	before, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("read temp directory before upload: %v", err)
	}
	if len(before) == 0 {
		t.Fatal("preparsed multipart upload did not create a temporary file")
	}
	req = withDocUser(req)
	rec := httptest.NewRecorder()

	h.Upload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	after, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("read temp directory after upload: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("multipart temporary files remain after upload: %v", after)
	}
}

func TestUploadNormalizesGenericMIMEFromKnownFilenameExtension(t *testing.T) {
	tests := []struct {
		name         string
		filename     string
		declaredMIME string
		wantMIME     string
	}{
		{
			name:         "docx with absent content type",
			filename:     "plan.docx",
			declaredMIME: "",
			wantMIME:     "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		},
		{
			name:         "xlsx with octet stream",
			filename:     "training.xlsx",
			declaredMIME: "application/octet-stream",
			wantMIME:     "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		},
		{
			name:         "pptx sniffed as zip",
			filename:     "briefing.pptx",
			declaredMIME: "application/zip",
			wantMIME:     "application/vnd.openxmlformats-officedocument.presentationml.presentation",
		},
		{
			name:         "epub sniffed as zip",
			filename:     "guide.epub",
			declaredMIME: "application/zip",
			wantMIME:     "application/epub+zip",
		},
		{
			name:         "meaningful declared type is preserved",
			filename:     "renamed.docx",
			declaredMIME: testMimePDF,
			wantMIME:     testMimePDF,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ingester := &recordingDocIngester{}
			h := handlers.NewDocuments(ingester, nil, ingest.UploadCapabilities{MaxBytes: 1 << 20})
			req := withDocUser(multipartUploadRequest(
				t,
				tt.filename,
				tt.declaredMIME,
				[]byte("PK\x03\x04 archive bytes"),
			))
			rec := httptest.NewRecorder()

			h.Upload(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if ingester.mime != tt.wantMIME {
				t.Fatalf("Ingest mime=%q, want %q", ingester.mime, tt.wantMIME)
			}
		})
	}
}

func TestCapabilitiesReturnsEffectiveUploadProfile(t *testing.T) {
	h, _ := newDocumentsHandler(t, 12<<20)
	req := withDocUser(httptest.NewRequest(http.MethodGet, "/api/documents/capabilities", nil))
	rec := httptest.NewRecorder()

	h.Capabilities(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Data ingest.UploadCapabilities `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Data.MaxBytes != 12<<20 {
		t.Fatalf("max_bytes=%d, want %d", envelope.Data.MaxBytes, 12<<20)
	}
	if envelope.Data.RichExtraction {
		t.Fatal("rich_extraction=true, want false for the effective PDF-only extractor")
	}
	if envelope.Data.Accept != "application/pdf,.pdf" {
		t.Fatalf("accept=%q, want PDF-only accept string", envelope.Data.Accept)
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
