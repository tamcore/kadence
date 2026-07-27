package handlers_test

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/tamcore/kadence/internal/api/handlers"
	"github.com/tamcore/kadence/internal/chat"
)

// Multipart field-name literals reused across the fixtures below, to avoid
// goconst duplicate-literal warnings.
const (
	fieldMessage        = "message"
	fieldConversationID = "conversationId"
	fieldDocumentIDs    = "documentIds"
	fieldFiles          = "files"
)

type multipartStreamer struct {
	legacyCalls    int
	turnCalls      int
	conversationID string
	turn           chat.TurnInput
}

func (s *multipartStreamer) Stream(
	_ context.Context,
	_ int64,
	_ chat.UserContext,
	conversationID string,
	text string,
	sink chat.EventSink,
) error {
	s.legacyCalls++
	s.conversationID = conversationID
	s.turn = chat.TurnInput{Text: text}
	_ = sink.Send(chat.ChatEvent{Type: chat.EventDone})
	return sink.Flush()
}

func (s *multipartStreamer) StreamTurn(
	_ context.Context,
	_ int64,
	_ chat.UserContext,
	conversationID string,
	turn chat.TurnInput,
	sink chat.EventSink,
) error {
	s.turnCalls++
	s.conversationID = conversationID
	s.turn = chat.TurnInput{
		Text:        turn.Text,
		Files:       append([]chat.FileInput(nil), turn.Files...),
		DocumentIDs: append([]int64(nil), turn.DocumentIDs...),
	}
	_ = sink.Send(chat.ChatEvent{Type: chat.EventDone})
	return sink.Flush()
}

type multipartPart struct {
	field    string
	filename string
	mime     string
	data     []byte
}

func newMultipartChatRequest(t *testing.T, parts []multipartPart) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, part := range parts {
		var (
			target ioWriter
			err    error
		)
		if part.filename == "" {
			target, err = writer.CreateFormField(part.field)
		} else {
			header := make(textproto.MIMEHeader)
			header.Set("Content-Disposition", fmt.Sprintf(
				`form-data; name=%q; filename=%q`, part.field, part.filename,
			))
			header.Set("Content-Type", part.mime)
			target, err = writer.CreatePart(header)
		}
		if err != nil {
			t.Fatalf("create multipart part %q: %v", part.field, err)
		}
		if _, err := target.Write(part.data); err != nil {
			t.Fatalf("write multipart part %q: %v", part.field, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/chat", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return withUser(request, 7)
}

type ioWriter interface {
	Write([]byte) (int, error)
}

func TestChatSendMultipartParsesOrderedTurn(t *testing.T) {
	streamer := &multipartStreamer{}
	handler := handlers.NewChatWithUploadLimit(
		streamer, &fakeConvLister{}, fakeMsgLister{}, 64, nil, nil,
	)
	request := newMultipartChatRequest(t, []multipartPart{
		{field: fieldMessage, data: []byte("compare these")},
		{field: fieldConversationID, data: []byte("conversation-1")},
		{field: fieldDocumentIDs, data: []byte("41")},
		{field: fieldFiles, filename: testChartPNGFilename, mime: testMimePNG, data: []byte("png")},
		{field: fieldDocumentIDs, data: []byte("7")},
		{field: fieldFiles, filename: "notes.md", mime: testMimeMarkdown, data: []byte("notes")},
	})
	response := httptest.NewRecorder()

	handler.Send(response, request)

	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status=%d content-type=%q body=%s",
			response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	if streamer.legacyCalls != 0 || streamer.turnCalls != 1 {
		t.Fatalf("legacy calls=%d turn calls=%d", streamer.legacyCalls, streamer.turnCalls)
	}
	if streamer.conversationID != "conversation-1" || streamer.turn.Text != "compare these" {
		t.Fatalf("conversation=%q turn=%+v", streamer.conversationID, streamer.turn)
	}
	if got := streamer.turn.DocumentIDs; !reflect.DeepEqual(got, []int64{41, 7}) {
		t.Fatalf("document ids=%v", got)
	}
	wantFiles := []chat.FileInput{
		{Filename: testChartPNGFilename, MIME: testMimePNG, Data: []byte("png")},
		{Filename: "notes.md", MIME: testMimeMarkdown, Data: []byte("notes")},
	}
	if !reflect.DeepEqual(streamer.turn.Files, wantFiles) {
		t.Fatalf("files=%+v want=%+v", streamer.turn.Files, wantFiles)
	}
}

func TestChatSendMultipartAllowsTextFileOrReferenceOnly(t *testing.T) {
	tests := []struct {
		name  string
		parts []multipartPart
	}{
		{
			name:  "text only",
			parts: []multipartPart{{field: fieldMessage, data: []byte("hello")}},
		},
		{
			name: "file only",
			parts: []multipartPart{{
				field: fieldFiles, filename: "screen.png", mime: testMimePNG, data: []byte("image"),
			}},
		},
		{
			name:  "reference only",
			parts: []multipartPart{{field: fieldDocumentIDs, data: []byte("91")}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			streamer := &multipartStreamer{}
			handler := handlers.NewChatWithUploadLimit(
				streamer, &fakeConvLister{}, fakeMsgLister{}, 64, nil, nil,
			)
			response := httptest.NewRecorder()

			handler.Send(response, newMultipartChatRequest(t, test.parts))

			if response.Code != http.StatusOK || streamer.turnCalls != 1 {
				t.Fatalf("status=%d turn calls=%d body=%s",
					response.Code, streamer.turnCalls, response.Body.String())
			}
		})
	}
}

func TestChatSendMultipartRejectsInvalidInputBeforeStreaming(t *testing.T) {
	tests := []struct {
		name       string
		parts      []multipartPart
		wantStatus int
	}{
		{name: "empty", wantStatus: http.StatusBadRequest},
		{
			name:       "whitespace text",
			parts:      []multipartPart{{field: fieldMessage, data: []byte("   ")}},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "unknown field after valid input",
			parts: []multipartPart{
				{field: fieldMessage, data: []byte("hello")},
				{field: "unexpected", data: []byte("value")},
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "duplicate message",
			parts: []multipartPart{
				{field: fieldMessage, data: []byte("one")},
				{field: fieldMessage, data: []byte("two")},
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "duplicate conversation id",
			parts: []multipartPart{
				{field: fieldMessage, data: []byte("hello")},
				{field: fieldConversationID, data: []byte("one")},
				{field: fieldConversationID, data: []byte("two")},
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid document id",
			parts:      []multipartPart{{field: fieldDocumentIDs, data: []byte("not-an-id")}},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "non-positive document id",
			parts:      []multipartPart{{field: fieldDocumentIDs, data: []byte("0")}},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "duplicate document id",
			parts: []multipartPart{
				{field: fieldDocumentIDs, data: []byte("9")},
				{field: fieldDocumentIDs, data: []byte("9")},
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "too many files",
			parts: []multipartPart{
				{field: fieldFiles, filename: "1.md", mime: testMimeMarkdown, data: []byte("1")},
				{field: fieldFiles, filename: "2.md", mime: testMimeMarkdown, data: []byte("2")},
				{field: fieldFiles, filename: "3.md", mime: testMimeMarkdown, data: []byte("3")},
				{field: fieldFiles, filename: "4.md", mime: testMimeMarkdown, data: []byte("4")},
				{field: fieldFiles, filename: "5.md", mime: testMimeMarkdown, data: []byte("5")},
				{field: fieldFiles, filename: "6.md", mime: testMimeMarkdown, data: []byte("6")},
			},
			wantStatus: http.StatusBadRequest,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			streamer := &multipartStreamer{}
			handler := handlers.NewChatWithUploadLimit(
				streamer, &fakeConvLister{}, fakeMsgLister{}, 64, nil, nil,
			)
			response := httptest.NewRecorder()

			handler.Send(response, newMultipartChatRequest(t, test.parts))

			if response.Code != test.wantStatus {
				t.Fatalf("status=%d want=%d body=%s",
					response.Code, test.wantStatus, response.Body.String())
			}
			if streamer.legacyCalls != 0 || streamer.turnCalls != 0 {
				t.Fatalf("service called before validation: legacy=%d turn=%d",
					streamer.legacyCalls, streamer.turnCalls)
			}
			if got := response.Header().Get("Content-Type"); strings.HasPrefix(got, "text/event-stream") {
				t.Fatalf("validation response already started SSE: content-type=%q", got)
			}
		})
	}
}

func TestChatSendMultipartEnforcesAggregateFileLimit(t *testing.T) {
	t.Run("exact limit accepted", func(t *testing.T) {
		streamer := &multipartStreamer{}
		handler := handlers.NewChatWithUploadLimit(
			streamer, &fakeConvLister{}, fakeMsgLister{}, 8, nil, nil,
		)
		response := httptest.NewRecorder()

		handler.Send(response, newMultipartChatRequest(t, []multipartPart{
			{field: fieldFiles, filename: "a.md", mime: testMimeMarkdown, data: []byte("1234")},
			{field: fieldFiles, filename: "b.md", mime: testMimeMarkdown, data: []byte("5678")},
		}))

		if response.Code != http.StatusOK || streamer.turnCalls != 1 {
			t.Fatalf("status=%d turn calls=%d body=%s",
				response.Code, streamer.turnCalls, response.Body.String())
		}
	})

	t.Run("one byte over rejected before service", func(t *testing.T) {
		streamer := &multipartStreamer{}
		handler := handlers.NewChatWithUploadLimit(
			streamer, &fakeConvLister{}, fakeMsgLister{}, 8, nil, nil,
		)
		response := httptest.NewRecorder()

		handler.Send(response, newMultipartChatRequest(t, []multipartPart{{
			field: fieldFiles, filename: "too-big.md", mime: testMimeMarkdown, data: []byte("123456789"),
		}}))

		if response.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status=%d want=%d body=%s",
				response.Code, http.StatusRequestEntityTooLarge, response.Body.String())
		}
		if streamer.legacyCalls != 0 || streamer.turnCalls != 0 {
			t.Fatalf("service called before size validation: legacy=%d turn=%d",
				streamer.legacyCalls, streamer.turnCalls)
		}
	})
}

func TestChatSendMultipartCapsExplicitDocumentReferences(t *testing.T) {
	t.Run("ten accepted", func(t *testing.T) {
		parts := make([]multipartPart, 0, 10)
		for id := 1; id <= 10; id++ {
			parts = append(parts, multipartPart{
				field: fieldDocumentIDs, data: []byte(strconv.Itoa(id)),
			})
		}
		streamer := &multipartStreamer{}
		handler := handlers.NewChatWithUploadLimit(
			streamer, &fakeConvLister{}, fakeMsgLister{}, 64, nil, nil,
		)
		response := httptest.NewRecorder()

		handler.Send(response, newMultipartChatRequest(t, parts))

		if response.Code != http.StatusOK || streamer.turnCalls != 1 {
			t.Fatalf("status=%d turn calls=%d body=%s",
				response.Code, streamer.turnCalls, response.Body.String())
		}
	})

	t.Run("eleven rejected before service", func(t *testing.T) {
		parts := make([]multipartPart, 0, 11)
		for id := 1; id <= 11; id++ {
			parts = append(parts, multipartPart{
				field: fieldDocumentIDs, data: []byte(strconv.Itoa(id)),
			})
		}
		streamer := &multipartStreamer{}
		handler := handlers.NewChatWithUploadLimit(
			streamer, &fakeConvLister{}, fakeMsgLister{}, 64, nil, nil,
		)
		response := httptest.NewRecorder()

		handler.Send(response, newMultipartChatRequest(t, parts))

		if response.Code != http.StatusBadRequest {
			t.Fatalf("status=%d want=400 body=%s", response.Code, response.Body.String())
		}
		if !strings.Contains(
			response.Body.String(), `"error":"a maximum of 10 document references is allowed"`,
		) {
			t.Fatalf("unstable error response: %s", response.Body.String())
		}
		if streamer.legacyCalls != 0 || streamer.turnCalls != 0 {
			t.Fatalf("service called before reference validation: legacy=%d turn=%d",
				streamer.legacyCalls, streamer.turnCalls)
		}
		if got := response.Header().Get("Content-Type"); strings.HasPrefix(got, "text/event-stream") {
			t.Fatalf("reference limit response already started SSE: content-type=%q", got)
		}
	})
}

func TestChatSendJSONUsesLegacyStreamPath(t *testing.T) {
	streamer := &multipartStreamer{}
	handler := handlers.NewChatWithUploadLimit(
		streamer, &fakeConvLister{}, fakeMsgLister{}, 8, nil, nil,
	)
	request := withUser(httptest.NewRequest(
		http.MethodPost, "/api/chat", strings.NewReader(`{"conversationId":"conversation-1","message":"hello"}`),
	), 7)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.Send(response, request)

	if response.Code != http.StatusOK || streamer.legacyCalls != 1 || streamer.turnCalls != 0 {
		t.Fatalf("status=%d legacy=%d turn=%d body=%s",
			response.Code, streamer.legacyCalls, streamer.turnCalls, response.Body.String())
	}
	if streamer.conversationID != "conversation-1" || streamer.turn.Text != "hello" {
		t.Fatalf("conversation=%q turn=%+v", streamer.conversationID, streamer.turn)
	}
}
