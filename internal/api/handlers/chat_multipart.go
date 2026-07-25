package handlers

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/tamcore/kadence/internal/chat"
)

const (
	maxChatFiles              = 5
	maxChatDocumentReferences = 10
	defaultChatUploadMaxBytes = 10 << 20
	maxChatMessageBytes       = 1 << 20
	maxChatScalarFieldBytes   = 4 << 10
	multipartChatOverhead     = 64 << 10
)

var (
	errInvalidMultipartChat  = errors.New("invalid multipart chat request")
	errMultipartChatTooLarge = errors.New("multipart chat request is too large")
	errTooManyChatFiles      = errors.New("too many chat files")
	errTooManyChatReferences = errors.New("too many chat document references")
)

func parseMultipartChat(
	w http.ResponseWriter,
	request *http.Request,
	uploadMaxBytes int64,
) (string, chat.TurnInput, error) {
	request.Body = http.MaxBytesReader(
		w,
		request.Body,
		uploadMaxBytes+maxChatMessageBytes+multipartChatOverhead,
	)
	reader, err := request.MultipartReader()
	if err != nil {
		return "", chat.TurnInput{}, fmt.Errorf("%w: %w", errInvalidMultipartChat, err)
	}

	var (
		conversationID    string
		input             chat.TurnInput
		gotMessage        bool
		gotConversationID bool
		totalFileBytes    int64
		documentIDs       = make(map[int64]struct{})
	)
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return "", chat.TurnInput{}, classifyMultipartReadError(nextErr)
		}

		switch part.FormName() {
		case "message":
			if part.FileName() != "" || gotMessage {
				_ = part.Close()
				return "", chat.TurnInput{}, errInvalidMultipartChat
			}
			gotMessage = true
			value, readErr := readMultipartField(part, maxChatMessageBytes)
			if readErr != nil {
				return "", chat.TurnInput{}, readErr
			}
			input.Text = value

		case "conversationId":
			if part.FileName() != "" || gotConversationID {
				_ = part.Close()
				return "", chat.TurnInput{}, errInvalidMultipartChat
			}
			gotConversationID = true
			value, readErr := readMultipartField(part, maxChatScalarFieldBytes)
			if readErr != nil {
				return "", chat.TurnInput{}, readErr
			}
			conversationID = value

		case "documentIds":
			if part.FileName() != "" {
				_ = part.Close()
				return "", chat.TurnInput{}, errInvalidMultipartChat
			}
			value, readErr := readMultipartField(part, maxChatScalarFieldBytes)
			if readErr != nil {
				return "", chat.TurnInput{}, readErr
			}
			documentID, parseErr := strconv.ParseInt(value, 10, 64)
			if parseErr != nil || documentID <= 0 {
				return "", chat.TurnInput{}, errInvalidMultipartChat
			}
			if _, duplicate := documentIDs[documentID]; duplicate {
				return "", chat.TurnInput{}, errInvalidMultipartChat
			}
			if len(input.DocumentIDs) == maxChatDocumentReferences {
				return "", chat.TurnInput{}, errTooManyChatReferences
			}
			documentIDs[documentID] = struct{}{}
			input.DocumentIDs = append(input.DocumentIDs, documentID)

		case "files":
			if part.FileName() == "" {
				_ = part.Close()
				return "", chat.TurnInput{}, errInvalidMultipartChat
			}
			if len(input.Files) == maxChatFiles {
				_ = part.Close()
				return "", chat.TurnInput{}, errTooManyChatFiles
			}
			remaining := uploadMaxBytes - totalFileBytes
			data, readErr := io.ReadAll(io.LimitReader(part, remaining+1))
			closeErr := part.Close()
			if readErr != nil {
				return "", chat.TurnInput{}, classifyMultipartReadError(readErr)
			}
			if closeErr != nil {
				return "", chat.TurnInput{}, classifyMultipartReadError(closeErr)
			}
			if int64(len(data)) > remaining {
				return "", chat.TurnInput{}, errMultipartChatTooLarge
			}
			if len(data) == 0 {
				return "", chat.TurnInput{}, errInvalidMultipartChat
			}
			totalFileBytes += int64(len(data))
			input.Files = append(input.Files, chat.FileInput{
				Filename: part.FileName(),
				MIME:     part.Header.Get("Content-Type"),
				Data:     data,
			})

		default:
			_ = part.Close()
			return "", chat.TurnInput{}, errInvalidMultipartChat
		}
	}

	if strings.TrimSpace(input.Text) == "" &&
		len(input.Files) == 0 &&
		len(input.DocumentIDs) == 0 {
		return "", chat.TurnInput{}, errInvalidMultipartChat
	}
	return conversationID, input, nil
}

func readMultipartField(part *multipart.Part, maxBytes int64) (string, error) {
	data, err := io.ReadAll(io.LimitReader(part, maxBytes+1))
	closeErr := part.Close()
	if err != nil {
		return "", classifyMultipartReadError(err)
	}
	if closeErr != nil {
		return "", classifyMultipartReadError(closeErr)
	}
	if int64(len(data)) > maxBytes {
		return "", errMultipartChatTooLarge
	}
	return string(data), nil
}

func classifyMultipartReadError(err error) error {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return fmt.Errorf("%w: %w", errMultipartChatTooLarge, err)
	}
	return fmt.Errorf("%w: %w", errInvalidMultipartChat, err)
}

func respondMultipartChatError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errMultipartChatTooLarge):
		RespondError(w, http.StatusRequestEntityTooLarge, "attachments exceed maximum upload size")
	case errors.Is(err, errTooManyChatFiles):
		RespondError(w, http.StatusBadRequest, "a maximum of 5 files is allowed")
	case errors.Is(err, errTooManyChatReferences):
		RespondError(w, http.StatusBadRequest, "a maximum of 10 document references is allowed")
	default:
		RespondError(w, http.StatusBadRequest, "invalid multipart chat request")
	}
}
