package handlers

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/ingest"
	"github.com/tamcore/kadence/internal/model"
)

// mimeSniffLen is the number of leading bytes inspected by
// http.DetectContentType when a client omits (or misreports) Content-Type.
const mimeSniffLen = 512

// documentIngester runs the extract/chunk/embed/persist pipeline for an
// uploaded document. Satisfied by *ingest.Service.
type documentIngester interface {
	Ingest(ctx context.Context, ownerUserID *int64, scope, filename, mime string, data []byte) (model.Document, error)
}

// documentRepo lists and deletes documents for both the private (per-owner)
// and public (admin) scopes. Satisfied by *store.DocumentRepository.
type documentRepo interface {
	ListByOwner(ctx context.Context, ownerUserID int64) ([]model.Document, error)
	ListPublic(ctx context.Context) ([]model.Document, error)
	Delete(ctx context.Context, id, ownerUserID int64) error
	DeletePublic(ctx context.Context, id int64) error
}

// Documents handles the document upload/list/delete HTTP endpoints, for both
// user-private and admin-public scopes.
type Documents struct {
	svc      documentIngester
	repo     documentRepo
	maxBytes int
}

// NewDocuments constructs the Documents handler.
func NewDocuments(svc documentIngester, repo documentRepo, maxBytes int) *Documents {
	return &Documents{svc: svc, repo: repo, maxBytes: maxBytes}
}

type documentDTO struct {
	ID         int64  `json:"id"`
	Filename   string `json:"filename"`
	Mime       string `json:"mime"`
	SourceType string `json:"source_type"`
	Scope      string `json:"scope"`
	CreatedAt  string `json:"created_at"`
}

func toDocumentDTO(d model.Document) documentDTO {
	return documentDTO{
		ID:         d.ID,
		Filename:   d.Filename,
		Mime:       d.Mime,
		SourceType: d.SourceType,
		Scope:      d.Scope,
		CreatedAt:  d.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func toDocumentDTOs(list []model.Document) []documentDTO {
	out := make([]documentDTO, 0, len(list))
	for _, doc := range list {
		out = append(out, toDocumentDTO(doc))
	}
	return out
}

// errBodyTooLarge signals that the uploaded body exceeded maxBytes; the
// caller maps it to HTTP 413.
var errBodyTooLarge = errors.New("uploaded file exceeds maximum size")

// readUploadedFile reads the "file" multipart field from r, bounded by
// d.maxBytes, and determines its MIME type. Any read/parse failure caused by
// exceeding maxBytes is reported as errBodyTooLarge; other failures (e.g. no
// "file" field present) are returned as-is.
func (d *Documents) readUploadedFile(w http.ResponseWriter, r *http.Request) (data []byte, filename, mimeType string, err error) {
	r.Body = http.MaxBytesReader(w, r.Body, int64(d.maxBytes))
	if err := r.ParseMultipartForm(int64(d.maxBytes)); err != nil {
		return nil, "", "", errBodyTooLarge
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		return nil, "", "", err
	}
	defer func() { _ = file.Close() }()

	data, err = io.ReadAll(file)
	if err != nil {
		return nil, "", "", errBodyTooLarge
	}

	mimeType = header.Header.Get("Content-Type")
	if mimeType == "" {
		probe := data
		if len(probe) > mimeSniffLen {
			probe = probe[:mimeSniffLen]
		}
		mimeType = http.DetectContentType(probe)
	}
	return data, header.Filename, mimeType, nil
}

// Upload handles POST /api/documents (private, owned by the caller).
func (d *Documents) Upload(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		RespondError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	d.upload(w, r, &u.ID, model.ScopePrivate)
}

// UploadPublic handles POST /api/admin/documents (public, ownerless).
func (d *Documents) UploadPublic(w http.ResponseWriter, r *http.Request) {
	d.upload(w, r, nil, model.ScopePublic)
}

func (d *Documents) upload(w http.ResponseWriter, r *http.Request, ownerUserID *int64, scope string) {
	data, filename, mimeType, err := d.readUploadedFile(w, r)
	if err != nil {
		if errors.Is(err, errBodyTooLarge) {
			RespondError(w, http.StatusRequestEntityTooLarge, "file exceeds maximum upload size")
			return
		}
		RespondError(w, http.StatusBadRequest, "file field is required")
		return
	}

	doc, err := d.svc.Ingest(r.Context(), ownerUserID, scope, filename, mimeType, data)
	if err != nil {
		if errors.Is(err, ingest.ErrUnsupportedType) {
			RespondError(w, http.StatusUnsupportedMediaType, "unsupported document type")
			return
		}
		slog.Error("ingest document", "err", err, "filename", filename, "mime", mimeType)
		RespondError(w, http.StatusInternalServerError, "could not ingest document")
		return
	}
	RespondJSON(w, http.StatusOK, toDocumentDTO(doc))
}

// List handles GET /api/documents (the caller's own documents).
func (d *Documents) List(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		RespondError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	list, err := d.repo.ListByOwner(r.Context(), u.ID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "could not list documents")
		return
	}
	RespondJSON(w, http.StatusOK, toDocumentDTOs(list))
}

// ListPublic handles GET /api/admin/documents.
func (d *Documents) ListPublic(w http.ResponseWriter, r *http.Request) {
	list, err := d.repo.ListPublic(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "could not list documents")
		return
	}
	RespondJSON(w, http.StatusOK, toDocumentDTOs(list))
}

// Delete handles DELETE /api/documents/{id} (must be owned by the caller).
func (d *Documents) Delete(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		RespondError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid document id")
		return
	}
	if err := d.repo.Delete(r.Context(), id, u.ID); err != nil {
		RespondError(w, http.StatusInternalServerError, "could not delete document")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeletePublic handles DELETE /api/admin/documents/{id}.
func (d *Documents) DeletePublic(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid document id")
		return
	}
	if err := d.repo.DeletePublic(r.Context(), id); err != nil {
		RespondError(w, http.StatusInternalServerError, "could not delete document")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
