package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/store"
)

const attachmentConversationID = "00000000-0000-0000-0000-000000000123"

type attachmentDeliveryLister struct {
	fakeMsgLister
	attachment     model.MessageAttachment
	err            error
	userID         int64
	conversationID string
	messageID      int64
	attachmentID   int64
}

func (l *attachmentDeliveryLister) GetAttachmentForUser(
	_ context.Context,
	userID int64,
	conversationID string,
	messageID int64,
	attachmentID int64,
) (model.MessageAttachment, error) {
	l.userID = userID
	l.conversationID = conversationID
	l.messageID = messageID
	l.attachmentID = attachmentID
	return l.attachment, l.err
}

func TestMessagesExposeSafeAttachmentAndReferenceMetadata(t *testing.T) {
	width, height := 640, 480
	documentID := int64(55)
	messages := fakeMsgLister{msgs: []model.Message{
		{
			ID: 12, Role: model.MsgRoleUser, Content: "compare",
			Attachments: []model.MessageAttachment{{
				ID: 31, MessageID: 12, Filename: "chart.png", MIME: "image/png",
				Kind: model.AttachmentKindImage, SizeBytes: 987,
				RawBytes: []byte("must-not-leak"), ExtractedMarkdown: "must-not-leak",
				ImageWidth: &width, ImageHeight: &height, Ordinal: 0,
			}},
			DocumentReferences: []model.MessageDocumentReference{{
				ID: 41, MessageID: 12, DocumentID: &documentID,
				Filename: "plan.md", Scope: model.ScopePrivate, Ordinal: 0, Available: true,
			}, {
				ID: 42, MessageID: 12, Filename: "deleted.pdf",
				Scope: model.ScopePublic, Ordinal: 1, Available: false,
			}},
		},
		{ID: 13, Role: model.MsgRoleAssistant, Content: "answer"},
	}}
	handler := handlers.NewChat(
		&fakeStreamer{}, &fakeConvLister{}, messages,
	)
	request := withChiParam(
		withUser(httptest.NewRequest(http.MethodGet, "/api/conversations/conv/messages", nil), 7),
		"id", "conv",
	)
	response := httptest.NewRecorder()

	handler.Messages(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "must-not-leak") {
		t.Fatalf("message response leaked attachment payload: %s", response.Body.String())
	}
	var envelope struct {
		Data []struct {
			ID          int64  `json:"id"`
			Role        string `json:"role"`
			Content     string `json:"content"`
			Attachments []struct {
				ID          int64  `json:"id"`
				Filename    string `json:"filename"`
				MIME        string `json:"mime"`
				Kind        string `json:"kind"`
				SizeBytes   int64  `json:"sizeBytes"`
				ImageWidth  *int   `json:"imageWidth"`
				ImageHeight *int   `json:"imageHeight"`
				Ordinal     int    `json:"ordinal"`
			} `json:"attachments"`
			DocumentReferences []struct {
				ID         int64  `json:"id"`
				DocumentID *int64 `json:"documentId"`
				Filename   string `json:"filename"`
				Scope      string `json:"scope"`
				Ordinal    int    `json:"ordinal"`
				Available  bool   `json:"available"`
			} `json:"documentReferences"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(envelope.Data) != 2 {
		t.Fatalf("messages=%+v", envelope.Data)
	}
	attachment := envelope.Data[0].Attachments
	if len(attachment) != 1 ||
		attachment[0].ID != 31 ||
		attachment[0].Filename != "chart.png" ||
		attachment[0].MIME != "image/png" ||
		attachment[0].Kind != model.AttachmentKindImage ||
		attachment[0].SizeBytes != 987 ||
		attachment[0].ImageWidth == nil || *attachment[0].ImageWidth != width ||
		attachment[0].ImageHeight == nil || *attachment[0].ImageHeight != height ||
		attachment[0].Ordinal != 0 {
		t.Fatalf("attachment metadata=%+v", attachment)
	}
	references := envelope.Data[0].DocumentReferences
	if len(references) != 2 ||
		references[0].DocumentID == nil || *references[0].DocumentID != documentID ||
		!references[0].Available ||
		references[1].DocumentID != nil || references[1].Available ||
		references[1].Filename != "deleted.pdf" ||
		references[1].Scope != model.ScopePublic {
		t.Fatalf("reference metadata=%+v", references)
	}
	if envelope.Data[1].Attachments == nil || envelope.Data[1].DocumentReferences == nil {
		t.Fatalf("empty metadata arrays encoded as null/omitted: %s", response.Body.String())
	}
}

func TestDownloadAttachmentUsesSafeDispositionAndPayload(t *testing.T) {
	tests := []struct {
		name            string
		attachment      model.MessageAttachment
		wantDisposition string
		wantContentType string
	}{
		{
			name: "validated image inline",
			attachment: model.MessageAttachment{
				ID: 31, MessageID: 12, Filename: "chart\r\nX-Evil: yes.png",
				MIME: "image/png", Kind: model.AttachmentKindImage, RawBytes: []byte("png-data"),
			},
			wantDisposition: "inline",
			wantContentType: "image/png",
		},
		{
			name: "document forced download",
			attachment: model.MessageAttachment{
				ID: 31, MessageID: 12, Filename: `<script>alert("x")</script>.html`,
				MIME: "text/html", Kind: model.AttachmentKindDocument, RawBytes: []byte("<script>"),
			},
			wantDisposition: "attachment",
			wantContentType: "text/html",
		},
		{
			name: "non-image mime cannot be inline despite image kind",
			attachment: model.MessageAttachment{
				ID: 31, MessageID: 12, Filename: "fake.html",
				MIME: "text/html", Kind: model.AttachmentKindImage, RawBytes: []byte("<script>"),
			},
			wantDisposition: "attachment",
			wantContentType: "text/html",
		},
		{
			name: "invalid mime defaults to opaque download",
			attachment: model.MessageAttachment{
				ID: 31, MessageID: 12, Filename: "unsafe.bin",
				MIME: "text/html\r\nX-Evil: yes", Kind: model.AttachmentKindDocument,
				RawBytes: []byte("bytes"),
			},
			wantDisposition: "attachment",
			wantContentType: "application/octet-stream",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &attachmentDeliveryLister{attachment: test.attachment}
			handler := handlers.NewChat(
				&fakeStreamer{}, &fakeConvLister{}, repository,
			)
			request := withChiParams(
				withUser(httptest.NewRequest(http.MethodGet, "/attachment", nil), 7),
				map[string]string{
					"id": attachmentConversationID, "messageId": "12", "attachmentId": "31",
				},
			)
			response := httptest.NewRecorder()

			handler.DownloadAttachment(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if repository.userID != 7 ||
				repository.conversationID != attachmentConversationID ||
				repository.messageID != 12 ||
				repository.attachmentID != 31 {
				t.Fatalf("ownership lookup=%+v", repository)
			}
			if got := response.Header().Get("Content-Type"); got != test.wantContentType {
				t.Fatalf("content-type=%q want=%q", got, test.wantContentType)
			}
			disposition := response.Header().Get("Content-Disposition")
			if !strings.HasPrefix(disposition, test.wantDisposition+";") {
				t.Fatalf("content-disposition=%q want prefix=%q", disposition, test.wantDisposition)
			}
			if strings.ContainsAny(disposition, "\r\n") ||
				response.Header().Get("X-Evil") != "" {
				t.Fatalf("unsafe content-disposition=%q headers=%v", disposition, response.Header())
			}
			if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Fatalf("X-Content-Type-Options=%q", got)
			}
			if got := response.Header().Get("Cache-Control"); got != "private, no-store" {
				t.Fatalf("Cache-Control=%q", got)
			}
			if got := response.Header().Get("Content-Length"); got != strconv.Itoa(len(test.attachment.RawBytes)) {
				t.Fatalf("Content-Length=%q", got)
			}
			if response.Body.String() != string(test.attachment.RawBytes) {
				t.Fatalf("body=%q want=%q", response.Body.String(), test.attachment.RawBytes)
			}
		})
	}
}

func TestDownloadAttachmentConcealsOwnershipMiss(t *testing.T) {
	repository := &attachmentDeliveryLister{err: store.ErrNotFound}
	handler := handlers.NewChat(
		&fakeStreamer{}, &fakeConvLister{}, repository,
	)
	request := withChiParams(
		withUser(httptest.NewRequest(http.MethodGet, "/attachment", nil), 7),
		map[string]string{
			"id": attachmentConversationID, "messageId": "12", "attachmentId": "31",
		},
	)
	response := httptest.NewRecorder()

	handler.DownloadAttachment(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d want=404 body=%s", response.Code, response.Body.String())
	}
}

func TestDownloadAttachmentMapsUnexpectedStoreFailure(t *testing.T) {
	repository := &attachmentDeliveryLister{err: errors.New("database unavailable")}
	handler := handlers.NewChat(
		&fakeStreamer{}, &fakeConvLister{}, repository,
	)
	request := withChiParams(
		withUser(httptest.NewRequest(http.MethodGet, "/attachment", nil), 7),
		map[string]string{
			"id": attachmentConversationID, "messageId": "12", "attachmentId": "31",
		},
	)
	response := httptest.NewRecorder()

	handler.DownloadAttachment(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want=500 body=%s", response.Code, response.Body.String())
	}
}
