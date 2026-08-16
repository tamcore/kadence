package chat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
	"github.com/tamcore/kadence/internal/scheduled"
)

// Stream runs one chat turn: resolve/create the conversation, persist the user
// message, stream the assistant reply (persisting it), emitting SSE events.
func (s *Service) Stream(ctx context.Context, userID int64, uc UserContext, conversationID string, userText string, sink EventSink) error {
	return s.StreamTurn(ctx, userID, uc, conversationID, TurnInput{Text: userText}, sink)
}

// StreamTurn runs one chat turn with optional raw files and explicit document
// references. Guardrail classification always precedes document extraction.
func (s *Service) StreamTurn(
	ctx context.Context,
	userID int64,
	uc UserContext,
	conversationID string,
	input TurnInput,
	sink EventSink,
) error {
	streamCtx, cancel := s.turnContext(ctx)
	defer cancel()

	processor := s.attachments
	if processor == nil {
		processor = NewAttachmentProcessor(nil)
	}
	prepared, err := s.prepareTurnAttachments(processor, input.Files, sink)
	if err != nil {
		return err
	}
	var documents []model.Document
	if len(input.DocumentIDs) > 0 {
		if s.documents == nil {
			return s.fail(sink, "selected document is unavailable")
		}
		documents, err = s.documents.ListVisibleByIDs(ctx, userID, input.DocumentIDs)
		if err != nil {
			return s.fail(sink, "selected document is unavailable")
		}
	}

	newConversation := conversationID == ""
	var history []model.Message
	if !newConversation {
		if _, err := s.convs.GetByID(ctx, conversationID, userID); err != nil {
			return s.fail(sink, "conversation not found")
		}
		history, err = s.msgs.ListChatHistory(ctx, conversationID)
		if err != nil {
			return s.fail(sink, "could not load history")
		}
	}

	classifierText := input.Text
	if strings.TrimSpace(classifierText) == "" {
		classifierText = fileOnlyClassifierText
	}
	guardrailMsgs := guardrailMessages(history, classifierText)
	confirmationCandidate := !newConversation &&
		len(prepared) == 0 &&
		len(input.DocumentIDs) == 0 &&
		s.scheduled != nil &&
		isPlainAffirmation(input.Text)
	offTopic := false
	if !confirmationCandidate {
		offTopic = s.classifyGuardrail(streamCtx, conversationID, guardrailMsgs)
	}

	toPersist := prepared
	if !offTopic {
		toPersist = make([]model.MessageAttachment, 0, len(prepared))
		for ordinal, attachment := range prepared {
			extracted, extractErr := processor.ExtractDocuments(
				streamCtx, []model.MessageAttachment{attachment},
			)
			if extractErr != nil {
				_ = sendUploadStatus(
					sink, ordinal, input.Files[ordinal].Filename, UploadStatusError,
					"could not extract attachment",
				)
				return s.fail(sink, "could not extract attachment")
			}
			toPersist = append(toPersist, extracted[0])
		}
	}
	resolved := resolvedTurnContext{}
	resolved.mcpSnap, resolved.systemPrompt = s.resolveMCPAndSystemPrompt(streamCtx, uc)
	if estimateTokens(resolved.systemPrompt)+estimateTokens(input.Text)+
		estimateNativeImageTokens(toPersist) > s.contextBudget {
		return s.fail(sink, "current message and attachments exceed the configured context budget")
	}
	persistedInput := model.ChatUserInput{
		Content: input.Text, Attachments: toPersist, DocumentIDs: input.DocumentIDs,
	}
	fallbackTitle := ""
	var userMsg model.Message
	if newConversation {
		fallbackTitle = turnTitle(input.Text, prepared, documents)
		conversation, createdMessage, createErr :=
			s.msgs.CreateConversationWithChatUserInput(
				ctx, userID, fallbackTitle, persistedInput,
			)
		if createErr != nil {
			return s.fail(sink, "could not save message")
		}
		fallbackTitle = conversation.Title
		conversationID = conversation.ID
		userMsg = createdMessage
	} else {
		userMsg, err = s.msgs.AddChatUserInput(
			ctx, conversationID, userID, persistedInput,
		)
	}
	if err != nil {
		return s.fail(sink, "could not save message")
	}
	for ordinal, file := range input.Files {
		if err := sendUploadStatus(sink, ordinal, file.Filename, UploadStatusDone, ""); err != nil {
			return err
		}
	}
	if confirmationCandidate {
		if handled, confirmErr := s.tryConfirmScheduledDraft(
			ctx, userID, uc, conversationID, userMsg, input.Text, sink,
		); handled || confirmErr != nil {
			return confirmErr
		}
		offTopic = s.classifyGuardrail(streamCtx, conversationID, guardrailMsgs)
	}
	if offTopic {
		if err := s.sendTurnMeta(conversationID, userMsg, sink); err != nil {
			return err
		}
		return s.persistGuardrailRefusal(ctx, conversationID, userMsg, sink)
	}
	return s.streamPersistedTurn(
		ctx, streamCtx, userID, uc, conversationID,
		fallbackTitle, userMsg, history, documents, &resolved, nil, true, true, sink,
	)
}

func (s *Service) prepareTurnAttachments(
	processor *AttachmentProcessor, files []FileInput, sink EventSink,
) ([]model.MessageAttachment, error) {
	for ordinal, file := range files {
		if err := sendUploadStatus(sink, ordinal, file.Filename, UploadStatusProcessing, ""); err != nil {
			return nil, err
		}
	}
	prepared := make([]model.MessageAttachment, 0, len(files))
	for ordinal, file := range files {
		attachments, err := processor.Prepare([]FileInput{file})
		if err != nil {
			_ = sendUploadStatus(sink, ordinal, file.Filename, UploadStatusError, "could not prepare attachment")
			return nil, s.fail(sink, "could not prepare attachment")
		}
		prepared = append(prepared, attachments[0])
	}
	return prepared, nil
}

const (
	multipleScheduledDraftsMessage  = "More than one scheduled task draft is waiting. Confirm each task separately using its card."
	incompleteScheduledDraftMessage = "Scheduled task draft still needs input. Complete it using its card."
	resolvedScheduledDraftMessage   = "That scheduled task was already handled."
)

func (s *Service) tryConfirmScheduledDraft(
	ctx context.Context, userID int64, uc UserContext, conversationID string,
	userMsg model.Message, userText string, sink EventSink,
) (bool, error) {
	if s.scheduled == nil || !isPlainAffirmation(userText) {
		return false, nil
	}
	result, err := s.scheduled.ConfirmSoleChatDraft(ctx, scheduled.Actor{
		ID: userID, Username: uc.Username, Timezone: uc.Timezone,
	}, conversationID)
	if err != nil {
		return true, s.fail(sink, "could not confirm scheduled task")
	}
	if result.Status == scheduled.ChatConfirmationNone {
		return false, nil
	}

	content := resolvedScheduledDraftMessage
	switch result.Status {
	case scheduled.ChatConfirmationMultiple:
		content = multipleScheduledDraftsMessage
	case scheduled.ChatConfirmationNeedsInput:
		content = incompleteScheduledDraftMessage
	case scheduled.ChatConfirmationConfirmed:
		content = "Scheduled task activated."
		if result.Artifact == nil {
			return true, s.fail(sink, "could not confirm scheduled task")
		}
		if result.Artifact.Proposal != nil && strings.TrimSpace(result.Artifact.Proposal.Name) != "" {
			content = "Scheduled task activated: " + strings.TrimSpace(result.Artifact.Proposal.Name) + "."
		}
	case scheduled.ChatConfirmationResolved:
	default:
		return true, s.fail(sink, "could not confirm scheduled task")
	}

	if err := s.sendTurnMeta(conversationID, userMsg, sink); err != nil {
		return true, err
	}
	if err := sink.Send(ChatEvent{Type: EventToken, Delta: content}); err != nil {
		return true, err
	}
	if err := sink.Flush(); err != nil {
		return true, err
	}
	if result.Artifact != nil {
		if err := sink.Send(ChatEvent{Type: EventScheduledArtifact, ScheduledArtifact: result.Artifact}); err != nil {
			return true, err
		}
		if err := sink.Flush(); err != nil {
			return true, err
		}
	}
	assistant, err := s.msgs.AddChatAssistantIfLatestUser(ctx, conversationID, userMsg, content, nil, nil)
	if err != nil {
		return true, s.fail(sink, "could not save response")
	}
	if err := sink.Send(ChatEvent{
		Type: EventDone, AssistantMessageID: assistant.ID, AssistantContent: &content,
	}); err != nil {
		return true, err
	}
	return true, sink.Flush()
}

func isPlainAffirmation(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	normalized = strings.TrimSpace(strings.Trim(normalized, ".!?"))
	normalized = strings.Join(strings.Fields(normalized), " ")
	switch normalized {
	case "yes", "yes please", "yes, please", "confirm", "confirmed", "approve", "approved",
		"go ahead", "ok", "okay", "do it", "please do":
		return true
	default:
		return false
	}
}

// Edit rewrites one persisted user prompt, removes the later transcript, and
// streams a replacement assistant response.
func (s *Service) Edit(
	ctx context.Context, userID int64, uc UserContext, conversationID string,
	messageID int64, userText string, sink EventSink,
) error {
	streamCtx, cancel := s.turnContext(ctx)
	defer cancel()

	preflight, err := s.preflightRewind(
		ctx, userID, conversationID, messageID, false,
	)
	if err != nil {
		return s.failRewindPreflight(sink, err)
	}
	userMsg, err := s.msgs.EditAndRewind(
		ctx, conversationID, messageID, userID, userText,
	)
	if err != nil {
		return s.fail(sink, "could not edit message")
	}
	userMsg.Attachments = preflight.prompt.Attachments
	userMsg.DocumentReferences = preflight.prompt.DocumentReferences
	return s.streamPersistedTurn(
		ctx, streamCtx, userID, uc, conversationID,
		"", userMsg, preflight.history, preflight.documents,
		nil, preflight.historicalPayloads, true, false, sink,
	)
}

// Regenerate removes one persisted assistant response and the later
// transcript, then streams a replacement from its preceding user prompt.
func (s *Service) Regenerate(
	ctx context.Context, userID int64, uc UserContext, conversationID string,
	messageID int64, sink EventSink,
) error {
	streamCtx, cancel := s.turnContext(ctx)
	defer cancel()

	preflight, err := s.preflightRewind(
		ctx, userID, conversationID, messageID, true,
	)
	if err != nil {
		return s.failRewindPreflight(sink, err)
	}
	userMsg, err := s.msgs.RegenerateAndRewind(ctx, conversationID, messageID, userID)
	if err != nil {
		return s.fail(sink, "could not regenerate response")
	}
	userMsg.Attachments = preflight.prompt.Attachments
	userMsg.DocumentReferences = preflight.prompt.DocumentReferences
	return s.streamPersistedTurn(
		ctx, streamCtx, userID, uc, conversationID,
		"", userMsg, preflight.history, preflight.documents,
		nil, preflight.historicalPayloads, false, false, sink,
	)
}

type rewindPreflight struct {
	prompt             model.Message
	history            []model.Message
	documents          []model.Document
	historicalPayloads *historicalPayloadCache
}

func (s *Service) preflightRewind(
	ctx context.Context, userID int64, conversationID string,
	targetID int64, regenerate bool,
) (rewindPreflight, error) {
	if _, err := s.convs.GetByID(ctx, conversationID, userID); err != nil {
		return rewindPreflight{}, fmt.Errorf("load owned conversation: %w", err)
	}
	messages, err := s.msgs.ListChatHistory(ctx, conversationID)
	if err != nil {
		return rewindPreflight{}, fmt.Errorf("load history: %w", err)
	}
	targetIndex := -1
	for i := range messages {
		if messages[i].ID == targetID {
			targetIndex = i
			break
		}
	}
	if targetIndex < 0 {
		return rewindPreflight{}, fmt.Errorf("rewind target not found")
	}

	promptIndex := targetIndex
	if regenerate {
		promptIndex = -1
		for i := targetIndex - 1; i >= 0; i-- {
			if messages[i].Role == model.MsgRoleUser {
				promptIndex = i
				break
			}
		}
		if promptIndex < 0 {
			return rewindPreflight{}, fmt.Errorf("regenerate prompt not found")
		}
	}
	prompt := messages[promptIndex]
	if len(prompt.Attachments) > 0 {
		payloads, payloadErr := s.msgs.LoadChatAttachmentPayloads(
			ctx, conversationID, []int64{prompt.ID},
		)
		if payloadErr != nil {
			return rewindPreflight{}, fmt.Errorf(
				"%w: %w", errRewindAttachmentPayload, payloadErr,
			)
		}
		attachments, ok := payloads[prompt.ID]
		if !ok || len(attachments) != len(prompt.Attachments) {
			return rewindPreflight{}, fmt.Errorf(
				"%w: incomplete result", errRewindAttachmentPayload,
			)
		}
		prompt.Attachments = attachments
	}
	documents, err := s.loadReferencedDocuments(ctx, userID, prompt)
	if err != nil {
		return rewindPreflight{}, fmt.Errorf("%w: %w", errRewindReferences, err)
	}
	history, historicalPayloads, err := s.loadHistoricalPayloads(
		ctx, userID, conversationID, messages[:promptIndex], s.contextBudget,
	)
	if err != nil {
		return rewindPreflight{}, fmt.Errorf(
			"%w: %w", errRewindAttachmentPayload, err,
		)
	}
	return rewindPreflight{
		prompt: prompt, history: history, documents: documents,
		historicalPayloads: historicalPayloads,
	}, nil
}

func (s *Service) failRewindPreflight(sink EventSink, err error) error {
	switch {
	case errors.Is(err, errRewindAttachmentPayload):
		return s.fail(sink, "could not load attachment payload")
	case errors.Is(err, errRewindReferences):
		return s.fail(sink, "selected document is unavailable")
	default:
		return s.fail(sink, "could not load history")
	}
}

func (s *Service) loadReferencedDocuments(
	ctx context.Context, userID int64, userMessage model.Message,
) ([]model.Document, error) {
	if len(userMessage.DocumentReferences) == 0 {
		return nil, nil
	}
	if s.documents == nil {
		return nil, fmt.Errorf("document store unavailable")
	}
	ids := make([]int64, 0, len(userMessage.DocumentReferences))
	for _, reference := range userMessage.DocumentReferences {
		if !reference.Available || reference.DocumentID == nil {
			return nil, fmt.Errorf("document reference unavailable")
		}
		ids = append(ids, *reference.DocumentID)
	}
	return s.documents.ListVisibleByIDs(ctx, userID, ids)
}

func (s *Service) turnContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if s.cfg.Timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, s.cfg.Timeout)
}

func (s *Service) ensureAttachmentExtractions(
	ctx, streamCtx context.Context,
	conversationID string, userID int64, userMessage model.Message,
) (model.Message, error) {
	pending := false
	for _, attachment := range userMessage.Attachments {
		if attachment.Kind == model.AttachmentKindDocument &&
			!attachment.ExtractionComplete &&
			attachment.ExtractedMarkdown == "" {
			pending = true
			break
		}
	}
	if !pending {
		return userMessage, nil
	}
	processor := s.attachments
	if processor == nil {
		processor = NewAttachmentProcessor(nil)
	}
	extracted, err := processor.ExtractDocuments(streamCtx, userMessage.Attachments)
	if err != nil {
		return model.Message{}, err
	}
	return s.msgs.UpdateChatAttachmentExtractions(
		ctx, conversationID, userMessage.ID, userID, extracted,
	)
}

func (s *Service) sendTurnMeta(
	conversationID string, userMessage model.Message, sink EventSink,
) error {
	attachments := make([]EventAttachment, 0, len(userMessage.Attachments))
	for _, attachment := range userMessage.Attachments {
		attachments = append(attachments, EventAttachment{
			ID: attachment.ID, Filename: attachment.Filename, MIME: attachment.MIME,
			Kind: attachment.Kind, SizeBytes: attachment.SizeBytes,
			ImageWidth: attachment.ImageWidth, ImageHeight: attachment.ImageHeight,
			Ordinal: attachment.Ordinal,
		})
	}
	references := make(
		[]EventDocumentReference, 0, len(userMessage.DocumentReferences),
	)
	for _, reference := range userMessage.DocumentReferences {
		references = append(references, EventDocumentReference{
			ID: reference.ID, DocumentID: reference.DocumentID,
			Filename: reference.Filename, Scope: reference.Scope,
			Ordinal: reference.Ordinal, Available: reference.Available,
		})
	}
	if err := sink.Send(ChatEvent{
		Type: EventMeta, ConversationID: conversationID, UserMessageID: userMessage.ID,
		Attachments: &attachments, DocumentReferences: &references,
	}); err != nil {
		return err
	}
	return sink.Flush()
}

func sendUploadStatus(
	sink EventSink, ordinal int, filename, status, message string,
) error {
	if err := sink.Send(ChatEvent{
		Type: EventUpload, FileOrdinal: &ordinal, Filename: filename,
		Status: status, Message: message,
	}); err != nil {
		return err
	}
	return sink.Flush()
}

// applyGuardrailAndExtract runs the egress guardrail classifier (skipped
// when guardrailChecked is already true from an earlier pipeline stage) and,
// if the turn is allowed through, extracts any attachment text. When the
// classifier refuses the turn or extraction fails, the failure is already
// persisted and reported via sink; the caller must treat done==true as "stop
// and return guardErr immediately" without any further work.
func (s *Service) applyGuardrailAndExtract(
	ctx, streamCtx context.Context, conversationID string, userID int64,
	userMsg model.Message, history []model.Message, userText string,
	guardrailChecked bool, sink EventSink,
) (updated model.Message, done bool, guardErr error) {
	if guardrailChecked {
		return userMsg, false, nil
	}
	classifierText := userText
	if strings.TrimSpace(classifierText) == "" {
		classifierText = "The user submitted files or selected documents without accompanying text."
	}
	if s.classifyGuardrail(streamCtx, conversationID, guardrailMessages(history, classifierText)) {
		return userMsg, true, s.persistGuardrailRefusal(ctx, conversationID, userMsg, sink)
	}
	updated, err := s.ensureAttachmentExtractions(ctx, streamCtx, conversationID, userID, userMsg)
	if err != nil {
		return userMsg, true, s.fail(sink, "could not extract attachment")
	}
	return updated, false, nil
}

// turnContextAssembly holds the provider messages and RAG bookkeeping that
// assembleTurnContext produces once it has fit the current turn and prior
// history within the model's token budget.
type turnContextAssembly struct {
	historyMessages []provider.Message
	currentMessage  provider.Message
	ragInserts      []provider.Message
	ragTurnStorable bool
	// derivedImages counts the page images appended to each message, so a
	// vision-unsupported retry can drop exactly those and keep the images the
	// user actually attached. Keyed by index into historyMessages; the current
	// turn's count is currentDerivedImages.
	derivedImages        map[int]int
	currentDerivedImages int
}

// hasDerivedImages reports whether this turn sent any page images the user did
// not attach themselves.
func (a turnContextAssembly) hasDerivedImages() bool {
	if a.currentDerivedImages > 0 {
		return true
	}
	for _, count := range a.derivedImages {
		if count > 0 {
			return true
		}
	}
	return false
}

// assembleTurnContext retrieves RAG context (broad memory, selected-document
// sections, and the query embedding for later storage), fits the current
// turn's attachments/documents into the remaining token budget, bounds prior
// history to fit, and loads (or restricts an already-preloaded)
// historical-attachment payload cache. On failure, the failure is already
// persisted and reported via sink; the caller must return the returned error
// as-is without further work.
func (s *Service) assembleTurnContext(
	ctx, streamCtx context.Context,
	conversationID string, userID int64,
	userMsg model.Message, userText string, history []model.Message, documents []model.Document,
	systemPrompt string, storeUserChunk bool,
	preloadedHistoricalPayloads *historicalPayloadCache,
	sink EventSink,
) (assembly turnContextAssembly, retrieval TurnRetrieval, ragErr error, err error) {
	// Retrieve once after the guardrail so the same query embedding can serve
	// broad memory, selected-document sections, and message storage.
	documentIDs := make([]int64, 0, len(documents))
	for _, document := range documents {
		documentIDs = append(documentIDs, document.ID)
	}
	retrieval, ragErr = s.retrieveRAGContext(
		streamCtx, conversationID, userID, userText, documentIDs,
	)

	// Derived exactly once per turn: extraction is expensive, and
	// fitCurrentTurnContext below calls the message builder repeatedly.
	pageImages := derivePageImagesForAttachments(userMsg.Attachments, s.cfg.PageImages)
	currentImageTokens := estimateNativeImageTokens(userMsg.Attachments) +
		estimatePageImageTokens(pageImages)
	explicitBudget := s.contextBudget -
		estimateTokens(systemPrompt) -
		estimateTokens(userText) -
		currentImageTokens
	fittedUser, fittedDocuments := fitCurrentTurnContext(
		userMsg, documents, retrieval.ByDocument, explicitBudget,
	)
	currentMessage, msgErr := currentTurnProviderMessageWithPageImages(
		fittedUser, fittedDocuments, pageImages,
	)
	if msgErr != nil {
		return turnContextAssembly{}, retrieval, ragErr, s.fail(sink, "could not assemble attachment context")
	}
	currentMessageTokens := estimateProviderMessageTokens(currentMessage)
	// Page images are a bonus on top of the text layer. When they push the turn
	// past the budget, drop them and send the text rather than refusing the
	// turn outright — several large PDFs at once would otherwise fail entirely.
	if estimateTokens(systemPrompt)+currentMessageTokens > s.contextBudget &&
		len(pageImages) > 0 {
		slog.Info("dropping derived pdf page images to fit the context budget",
			"conversation", conversationID, "images", len(pageImages))
		dropped := len(pageImages)
		pageImages = nil
		currentMessage, msgErr = currentTurnProviderMessageWithPageImages(
			fittedUser, fittedDocuments, nil,
		)
		if msgErr != nil {
			return turnContextAssembly{}, retrieval, ragErr, s.fail(sink, "could not assemble attachment context")
		}
		// Say so explicitly. Dropping the images silently would leave the model
		// answering from prose as though the document were complete, which is
		// the confidently-wrong behavior this whole feature exists to prevent.
		currentMessage.Content += fmt.Sprintf("\n\n%s", droppedPageImagesNotice(dropped))
		currentMessageTokens = estimateProviderMessageTokens(currentMessage)
	}
	if estimateTokens(systemPrompt)+currentMessageTokens > s.contextBudget {
		return turnContextAssembly{}, retrieval, ragErr, s.fail(
			sink,
			"current message and attachments exceed the configured context budget",
		)
	}
	ragBudget := s.contextBudget -
		estimateTokens(systemPrompt) -
		currentMessageTokens
	ragInserts := s.buildRAGInserts(retrieval.Broad, ragBudget)
	ragTurnStorable := storeUserChunk && len(retrieval.Embedding) > 0 && userText != ""
	reservedTokens := 0
	for _, message := range ragInserts {
		reservedTokens += estimateTokens(message.Content)
	}
	minimumHistory := minimumHistoricalMessages(history)
	boundedHistory, droppedCount := s.boundHistory(
		minimumHistory, systemPrompt, currentMessage, reservedTokens,
	)
	if droppedCount > 0 {
		slog.Debug("chat history trimmed to fit token budget",
			"conversation", conversationID, "dropped_messages", droppedCount, "budget_tokens", s.contextBudget)
	}
	historyUsed := estimateTokens(systemPrompt) +
		currentMessageTokens + reservedTokens
	for _, message := range boundedHistory {
		historyUsed += estimateTokens(message.Content)
	}
	historyBudget := s.contextBudget - historyUsed
	var historicalPayloads *historicalPayloadCache
	if preloadedHistoricalPayloads == nil {
		var payloadErr error
		boundedHistory, historicalPayloads, payloadErr = s.loadHistoricalPayloads(
			ctx, userID, conversationID, boundedHistory, historyBudget,
		)
		if payloadErr != nil {
			slog.Error("historical attachment payload lookup failed", "err", payloadErr)
			return turnContextAssembly{}, retrieval, ragErr, s.fail(sink, "could not load historical attachment context")
		}
	} else {
		historicalPayloads = restrictHistoricalPayloadCache(
			boundedHistory, historyBudget, preloadedHistoricalPayloads,
		)
	}
	historyMessages, historyDerived := buildHistoricalProviderMessages(
		boundedHistory, historyBudget, historicalPayloads, s.cfg.PageImages,
	)

	return turnContextAssembly{
		historyMessages:      historyMessages,
		currentMessage:       currentMessage,
		ragInserts:           ragInserts,
		ragTurnStorable:      ragTurnStorable,
		derivedImages:        historyDerived,
		currentDerivedImages: len(pageImages),
	}, retrieval, ragErr, nil
}

func estimateNativeImageTokens(attachments []model.MessageAttachment) int {
	tokens := 0
	for _, attachment := range attachments {
		if attachment.Kind == model.AttachmentKindImage {
			tokens += estimateImageTokens(providerImageContent(attachment))
		}
	}
	return tokens
}
