package handlers

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/tamcore/kadence/internal/auth"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
)

// AttachmentPayloadLoader returns one attachment only when every owner/path
// component matches.
type AttachmentPayloadLoader interface {
	GetAttachmentForUser(
		ctx context.Context,
		userID int64,
		conversationID string,
		messageID int64,
		attachmentID int64,
	) (model.MessageAttachment, error)
}

// DownloadAttachment handles
// GET /api/conversations/{id}/messages/{messageId}/attachments/{attachmentId}.
func (h *Chat) DownloadAttachment(w http.ResponseWriter, r *http.Request) {
	conversationID := chi.URLParam(r, "id")
	if _, err := uuid.Parse(conversationID); err != nil {
		RespondError(w, http.StatusBadRequest, "valid attachment path is required")
		return
	}
	messageID, messageErr := strconv.ParseInt(chi.URLParam(r, "messageId"), 10, 64)
	attachmentID, attachmentErr := strconv.ParseInt(
		chi.URLParam(r, "attachmentId"), 10, 64,
	)
	if messageErr != nil || attachmentErr != nil || messageID <= 0 || attachmentID <= 0 {
		RespondError(w, http.StatusBadRequest, "valid attachment path is required")
		return
	}
	loader, ok := h.msgs.(AttachmentPayloadLoader)
	if !ok {
		RespondError(w, http.StatusInternalServerError, "attachment delivery is unavailable")
		return
	}
	user := auth.UserFromContext(r.Context())
	attachment, err := loader.GetAttachmentForUser(
		r.Context(), user.ID, conversationID, messageID, attachmentID,
	)
	if errors.Is(err, store.ErrNotFound) {
		RespondError(w, http.StatusNotFound, "attachment not found")
		return
	}
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "could not load attachment")
		return
	}

	contentType, mediaType := safeAttachmentContentType(attachment.MIME)
	disposition := "attachment"
	if attachment.Kind == model.AttachmentKindImage && isInlineImageMIME(mediaType) {
		disposition = "inline"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(
		disposition,
		map[string]string{"filename": safeAttachmentFilename(attachment.Filename)},
	))
	w.Header().Set("Content-Length", strconv.Itoa(len(attachment.RawBytes)))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(attachment.RawBytes)
}

func safeAttachmentContentType(raw string) (string, string) {
	mediaType, params, err := mime.ParseMediaType(raw)
	if err != nil {
		return "application/octet-stream", "application/octet-stream"
	}
	formatted := mime.FormatMediaType(mediaType, params)
	if formatted == "" {
		return "application/octet-stream", "application/octet-stream"
	}
	return formatted, strings.ToLower(mediaType)
}

func isInlineImageMIME(mediaType string) bool {
	switch mediaType {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		return true
	default:
		return false
	}
}

func safeAttachmentFilename(filename string) string {
	filename = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsControl(r):
			return -1
		case r == '/' || r == '\\':
			return '_'
		default:
			return r
		}
	}, filename)
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return "attachment"
	}
	const maxFilenameRunes = 255
	runes := []rune(filename)
	if len(runes) > maxFilenameRunes {
		filename = string(runes[:maxFilenameRunes])
	}
	return filename
}
