package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/tamcore/kadence/internal/ingest"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
)

// TurnInput is the current user turn: unchanged user-authored text, ordered
// raw files, and ordered explicit knowledge-document IDs.
type TurnInput struct {
	Text        string
	Files       []FileInput
	DocumentIDs []int64
}

type untrustedContextItem struct {
	ID       int64  `json:"id,omitempty"`
	Filename string `json:"filename"`
	Content  string `json:"content"`
}

type untrustedContextEnvelope struct {
	Attachments []untrustedContextItem `json:"attachments,omitempty"`
	Documents   []untrustedContextItem `json:"documents,omitempty"`
}

const untrustedContextOpen = "<untrusted_context>"
const untrustedContextClose = "</untrusted_context>"
const historicalPayloadOmittedMarker = "[historical attachment and document payload omitted to fit context budget]"

func currentTurnProviderMessage(
	userMessage model.Message, documents []model.Document,
) (provider.Message, error) {
	return currentTurnProviderMessageWithPageImages(userMessage, documents, nil)
}

// currentTurnProviderMessageWithPageImages builds the provider message for one
// user turn, appending page images already derived from its PDF attachments.
//
// pageImages is supplied by the caller rather than derived here on purpose:
// this function runs repeatedly inside fitCurrentTurnContext's trimming loop,
// and PDF page-image extraction costs seconds on a large document, so deriving
// here would multiply that cost by the number of fitting iterations. The
// trimming loop only measures len(message.Content), so images never affect
// fitting and passing nil there is correct.
func currentTurnProviderMessageWithPageImages(
	userMessage model.Message, documents []model.Document, pageImages []provider.ImageContent,
) (provider.Message, error) {
	message := provider.Message{Role: model.MsgRoleUser, Content: userMessage.Content}
	envelope := untrustedContextEnvelope{}
	for _, attachment := range userMessage.Attachments {
		switch attachment.Kind {
		case model.AttachmentKindImage:
			message.Images = append(message.Images, providerImageContent(attachment))
		case model.AttachmentKindDocument:
			envelope.Attachments = append(envelope.Attachments, untrustedContextItem{
				Filename: attachment.Filename, Content: attachment.ExtractedMarkdown,
			})
		}
	}
	// Derived page images go last so a vision-unsupported retry can drop them
	// as a suffix while keeping the images the user actually attached.
	message.Images = append(message.Images, pageImages...)
	for _, document := range documents {
		envelope.Documents = append(envelope.Documents, untrustedContextItem{
			ID: document.ID, Filename: document.Filename, Content: document.ExtractedMarkdown,
		})
	}
	if len(envelope.Attachments) == 0 && len(envelope.Documents) == 0 {
		return message, nil
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return provider.Message{}, fmt.Errorf("marshal untrusted turn context: %w", err)
	}
	if message.Content != "" {
		message.Content += "\n\n"
	}
	message.Content += untrustedContextOpen + "\n" + string(encoded) + "\n" + untrustedContextClose
	return message, nil
}

func hasHistoricalPayload(message model.Message) bool {
	return len(message.Attachments) > 0 || len(message.DocumentReferences) > 0
}

func historicalTextWithOmissionMarker(content string) string {
	if content == "" {
		return historicalPayloadOmittedMarker
	}
	return content + "\n\n" + historicalPayloadOmittedMarker
}

func historicalTextWithoutOmissionMarker(content string) string {
	if content == historicalPayloadOmittedMarker {
		return ""
	}
	return strings.TrimSuffix(content, "\n\n"+historicalPayloadOmittedMarker)
}

func estimateProviderMessageTokens(message provider.Message) int {
	tokens := estimateTokens(message.Content)
	for _, image := range message.Images {
		tokens += estimateImageTokens(image)
	}
	return tokens
}

const (
	imageTilePixels = 512
	imageBaseTokens = 256
	imageTileTokens = 256
)

func estimateImageTokens(image provider.ImageContent) int {
	if image.Width <= 0 || image.Height <= 0 {
		return (len(image.Data) + 2) / 3
	}
	wide := (image.Width + imageTilePixels - 1) / imageTilePixels
	high := (image.Height + imageTilePixels - 1) / imageTilePixels
	return imageBaseTokens + wide*high*imageTileTokens
}

func providerImageContent(attachment model.MessageAttachment) provider.ImageContent {
	return provider.ImageContent{
		Data:     attachment.RawBytes,
		MIMEType: attachment.MIME,
		Width:    dereferenceInt(attachment.ImageWidth),
		Height:   dereferenceInt(attachment.ImageHeight),
	}
}

func dereferenceInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func providerMessagesContainImages(messages []provider.Message) bool {
	for _, message := range messages {
		if len(message.Images) > 0 {
			return true
		}
	}
	return false
}

func minimumHistoricalMessages(history []model.Message) []model.Message {
	minimum := append([]model.Message(nil), history...)
	for i := range minimum {
		if minimum[i].Role == model.MsgRoleUser && hasHistoricalPayload(minimum[i]) {
			minimum[i].Content = historicalTextWithOmissionMarker(minimum[i].Content)
		}
	}
	return minimum
}

type historicalPayloadCache struct {
	selected           map[int64]struct{}
	documents          map[int64][]model.Document
	documentsAvailable map[int64]bool
}

func (s *Service) loadHistoricalPayloads(
	ctx context.Context, userID int64, conversationID string,
	history []model.Message, availableTokens int,
) ([]model.Message, *historicalPayloadCache, error) {
	messageIDs := selectHistoricalAttachmentPayloadIDs(history, availableTokens)
	cache := &historicalPayloadCache{
		selected:           make(map[int64]struct{}, len(messageIDs)),
		documents:          make(map[int64][]model.Document, len(messageIDs)),
		documentsAvailable: make(map[int64]bool, len(messageIDs)),
	}
	for _, messageID := range messageIDs {
		cache.selected[messageID] = struct{}{}
	}
	if len(messageIDs) == 0 {
		return history, cache, nil
	}

	attachmentIDs := make([]int64, 0, len(messageIDs))
	for _, message := range history {
		if _, selected := cache.selected[message.ID]; selected &&
			len(message.Attachments) > 0 {
			attachmentIDs = append(attachmentIDs, message.ID)
		}
	}
	payloads := map[int64][]model.MessageAttachment{}
	if len(attachmentIDs) > 0 {
		var err error
		payloads, err = s.msgs.LoadChatAttachmentProviderPayloads(
			ctx, conversationID, attachmentIDs,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("load historical attachment payloads: %w", err)
		}
	}

	hydrated := append([]model.Message(nil), history...)
	for i := range hydrated {
		if _, selected := cache.selected[hydrated[i].ID]; !selected {
			continue
		}
		if len(hydrated[i].Attachments) > 0 {
			attachments, ok := payloads[hydrated[i].ID]
			if !ok || len(attachments) != len(hydrated[i].Attachments) {
				return nil, nil, fmt.Errorf(
					"load historical attachment payloads: message %d incomplete",
					hydrated[i].ID,
				)
			}
			hydrated[i].Attachments = attachments
		}
		documents, available := s.loadHistoricalDocuments(ctx, userID, hydrated[i])
		cache.documents[hydrated[i].ID] = documents
		cache.documentsAvailable[hydrated[i].ID] = available
	}
	return hydrated, cache, nil
}

func selectHistoricalAttachmentPayloadIDs(
	history []model.Message, availableTokens int,
) []int64 {
	return selectHistoricalPayloadIDsEligible(history, availableTokens, nil)
}

func selectHistoricalPayloadIDsEligible(
	history []model.Message, availableTokens int, eligible map[int64]struct{},
) []int64 {
	if availableTokens <= 0 {
		return nil
	}
	remaining := int64(availableTokens)
	selected := make(map[int64]struct{}, maxHistoricalAttachmentPayloadTurns)
	for i := len(history) - 1; i >= 0; i-- {
		message := history[i]
		if message.Role != model.MsgRoleUser || !hasHistoricalPayload(message) {
			continue
		}
		if eligible != nil {
			if _, ok := eligible[message.ID]; !ok {
				continue
			}
		}
		cost := historicalPayloadTokenCost(message)
		if cost > remaining {
			continue
		}
		selected[message.ID] = struct{}{}
		remaining -= cost
		if len(selected) == maxHistoricalAttachmentPayloadTurns {
			break
		}
	}
	messageIDs := make([]int64, 0, len(selected))
	for _, message := range history {
		if _, ok := selected[message.ID]; ok {
			messageIDs = append(messageIDs, message.ID)
		}
	}
	return messageIDs
}

func historicalPayloadTokenCost(message model.Message) int64 {
	var cost int64
	for _, attachment := range message.Attachments {
		payloadBytes := attachment.PayloadBytes
		if payloadBytes <= 0 {
			payloadBytes = attachment.SizeBytes
		}
		switch attachment.Kind {
		case model.AttachmentKindImage:
			cost += (payloadBytes + 2) / 3
		case model.AttachmentKindDocument:
			cost += (payloadBytes + estBytesPerToken - 1) / estBytesPerToken
		}
	}
	for _, reference := range message.DocumentReferences {
		cost += (reference.PayloadBytes + estBytesPerToken - 1) /
			estBytesPerToken
	}
	return cost
}

func restrictHistoricalPayloadCache(
	history []model.Message, availableTokens int, cache *historicalPayloadCache,
) *historicalPayloadCache {
	if cache == nil {
		return nil
	}
	ids := selectHistoricalPayloadIDsEligible(
		history, availableTokens, cache.selected,
	)
	selected := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		selected[id] = struct{}{}
	}
	return &historicalPayloadCache{
		selected: selected, documents: cache.documents,
		documentsAvailable: cache.documentsAvailable,
	}
}

func historicalAttachmentPayloadAvailable(message model.Message) bool {
	for _, attachment := range message.Attachments {
		switch attachment.Kind {
		case model.AttachmentKindImage:
			if len(attachment.RawBytes) == 0 {
				return false
			}
		case model.AttachmentKindDocument:
			if !attachment.ExtractionComplete {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (s *Service) loadHistoricalDocuments(
	ctx context.Context, userID int64, message model.Message,
) ([]model.Document, bool) {
	if len(message.DocumentReferences) == 0 {
		return nil, true
	}
	if s.documents == nil {
		return nil, false
	}
	ids := make([]int64, 0, len(message.DocumentReferences))
	for _, reference := range message.DocumentReferences {
		if !reference.Available || reference.DocumentID == nil {
			return nil, false
		}
		ids = append(ids, *reference.DocumentID)
	}
	documents, err := s.documents.ListVisibleByIDs(ctx, userID, ids)
	if err != nil || len(documents) != len(ids) {
		if err != nil {
			slog.Warn("historical document lookup failed; omitting payload", "err", err)
		}
		return nil, false
	}
	return documents, true
}

// buildHistoricalProviderMessages rehydrates selected historical turns. opts
// re-derives page images for rehydrated PDF attachments, so a follow-up turn
// can still read a table that has no text layer: the payload load above
// restores RawBytes, and derived images are never persisted.
func buildHistoricalProviderMessages(
	history []model.Message, availableTokens int, cache *historicalPayloadCache,
	opts ingest.PageImageOptions,
) ([]provider.Message, map[int]int) {
	derived := map[int]int{}
	out := make([]provider.Message, len(history))
	for i, message := range history {
		out[i] = provider.Message{Role: message.Role, Content: message.Content}
	}
	if availableTokens < 0 {
		availableTokens = 0
	}
	// Spend remaining history-payload budget from newest to oldest. Current
	// turn evidence and every kept message's text/omission marker are already
	// reserved before this function runs.
	for i := len(history) - 1; i >= 0; i-- {
		message := history[i]
		if message.Role != model.MsgRoleUser || !hasHistoricalPayload(message) {
			continue
		}
		if cache == nil {
			continue
		}
		if _, selected := cache.selected[message.ID]; !selected {
			continue
		}
		if !historicalAttachmentPayloadAvailable(message) {
			continue
		}
		if !cache.documentsAvailable[message.ID] {
			continue
		}
		message.Content = historicalTextWithoutOmissionMarker(message.Content)
		// Rehydrate the text first and only extract page images once the text
		// alone is known to fit. Extraction costs seconds on a large PDF, and
		// doing it before this check would burn that on messages that can
		// never be admitted.
		textOnly, err := currentTurnProviderMessage(message, cache.documents[message.ID])
		if err != nil {
			continue
		}
		baseline := estimateProviderMessageTokens(out[i])
		textExtra := estimateProviderMessageTokens(textOnly) - baseline
		if textExtra > availableTokens {
			continue
		}

		full, extra := textOnly, textExtra
		if pageImages := derivePageImagesForAttachments(message.Attachments, opts); len(pageImages) > 0 {
			withImages, imgErr := currentTurnProviderMessageWithPageImages(
				message, cache.documents[message.ID], pageImages,
			)
			// Images are a bonus: when they do not fit, keep the text-only
			// rehydration rather than dropping the turn entirely.
			if imgErr == nil {
				imageExtra := estimateProviderMessageTokens(withImages) - baseline
				if imageExtra <= availableTokens {
					full, extra = withImages, imageExtra
					derived[i] = len(pageImages)
				}
			}
		}
		out[i] = full
		availableTokens -= max(0, extra)
	}
	return out, derived
}

const contextTruncatedMarker = "[truncated to fit context budget]"

func fitCurrentTurnContext(
	userMessage model.Message,
	documents []model.Document,
	sections map[int64][]string,
	availableTokens int,
) (model.Message, []model.Document) {
	fittedMessage := userMessage
	fittedMessage.Attachments = append(
		[]model.MessageAttachment(nil), userMessage.Attachments...,
	)
	fittedDocuments := append([]model.Document(nil), documents...)

	type fitItem struct {
		attachment bool
		index      int
		full       string
		sections   []string
	}
	items := make([]fitItem, 0, len(fittedDocuments)+len(fittedMessage.Attachments))
	for i, attachment := range fittedMessage.Attachments {
		if attachment.Kind == model.AttachmentKindDocument {
			items = append(items, fitItem{
				attachment: true, index: i, full: attachment.ExtractedMarkdown,
			})
		}
	}
	for i, document := range fittedDocuments {
		items = append(items, fitItem{
			index: i, full: document.ExtractedMarkdown,
			sections: sections[document.ID],
		})
	}
	if len(items) == 0 {
		return fittedMessage, fittedDocuments
	}

	availableContextBytes := max(availableTokens*estBytesPerToken, 0)
	maxEncodedBytes := len(userMessage.Content) + availableContextBytes
	encodedBytes := func() int {
		current, err := currentTurnProviderMessage(fittedMessage, fittedDocuments)
		if err != nil {
			return maxEncodedBytes + 1
		}
		return len(current.Content)
	}
	if encodedBytes() <= maxEncodedBytes {
		return fittedMessage, fittedDocuments
	}

	setContent := func(index int, content string) {
		item := items[index]
		if item.attachment {
			fittedMessage.Attachments[item.index].ExtractedMarkdown = content
			return
		}
		fittedDocuments[item.index].ExtractedMarkdown = content
	}
	contentLengths := make([]int, len(items))
	for i, item := range items {
		contentLengths[i] = len(item.full)
		setContent(i, "")
	}

	type filenameItem struct {
		attachment bool
		index      int
		full       string
	}
	filenames := make([]filenameItem, 0, len(items))
	for _, item := range items {
		if item.attachment {
			filenames = append(filenames, filenameItem{
				attachment: true, index: item.index,
				full: fittedMessage.Attachments[item.index].Filename,
			})
			continue
		}
		filenames = append(filenames, filenameItem{
			index: item.index, full: fittedDocuments[item.index].Filename,
		})
	}
	setFilename := func(index int, filename string) {
		item := filenames[index]
		if item.attachment {
			fittedMessage.Attachments[item.index].Filename = filename
			return
		}
		fittedDocuments[item.index].Filename = filename
	}
	if encodedBytes() > maxEncodedBytes {
		filenameLengths := make([]int, len(filenames))
		for i, filename := range filenames {
			filenameLengths[i] = len(filename.full)
			setFilename(i, "")
		}
		minimumBytes := encodedBytes()
		if minimumBytes > maxEncodedBytes {
			return fittedMessage, fittedDocuments
		}
		filenameBudget := minimumBytes + (maxEncodedBytes-minimumBytes)/3
		distributeTurnContextBudget(
			filenameLengths,
			func(index, maxBytes int) string {
				return truncateUTF8(filenames[index].full, maxBytes)
			},
			setFilename, encodedBytes, filenameBudget,
		)
	}

	distributeTurnContextBudget(
		contentLengths,
		func(index, maxBytes int) string {
			return fitContextContent(
				items[index].full, items[index].sections, maxBytes,
			)
		},
		setContent, encodedBytes, maxEncodedBytes,
	)
	return fittedMessage, fittedDocuments
}

func distributeTurnContextBudget(
	fullLengths []int,
	render func(index, maxBytes int) string,
	apply func(index int, value string),
	encodedBytes func() int,
	maxEncodedBytes int,
) {
	caps := make([]int, len(fullLengths))
	for {
		maxIncrease := 0
		for i, fullLength := range fullLengths {
			if remaining := fullLength - caps[i]; remaining > maxIncrease {
				maxIncrease = remaining
			}
		}
		if maxIncrease == 0 {
			return
		}
		fitsIncrease := func(increase int) bool {
			for i, fullLength := range fullLengths {
				next := caps[i]
				if next < fullLength {
					next = min(fullLength, next+increase)
				}
				apply(i, render(i, next))
			}
			return encodedBytes() <= maxEncodedBytes
		}
		low, high := 0, maxIncrease
		for low < high {
			middle := low + (high-low+1)/2
			if fitsIncrease(middle) {
				low = middle
			} else {
				high = middle - 1
			}
		}
		if low == 0 {
			fitsIncrease(0)
			return
		}
		for i, fullLength := range fullLengths {
			if caps[i] < fullLength {
				caps[i] = min(fullLength, caps[i]+low)
			}
			apply(i, render(i, caps[i]))
		}
	}
}

func fitContextContent(full string, sections []string, maxChars int) string {
	if len(full) <= maxChars {
		return full
	}
	if maxChars <= 0 {
		return ""
	}
	if maxChars <= len(contextTruncatedMarker) {
		return truncateUTF8(contextTruncatedMarker, maxChars)
	}
	contentBudget := maxChars - len(contextTruncatedMarker) - 1
	var selected strings.Builder
	for _, section := range sections {
		if selected.Len() > 0 {
			if selected.Len()+2 > contentBudget {
				break
			}
			selected.WriteString("\n\n")
		}
		remaining := contentBudget - selected.Len()
		if remaining <= 0 {
			break
		}
		selected.WriteString(truncateUTF8(section, remaining))
	}
	if selected.Len() == 0 && contentBudget > 0 {
		selected.WriteString(truncateUTF8(full, contentBudget))
	}
	if selected.Len() > 0 {
		selected.WriteByte('\n')
	}
	selected.WriteString(contextTruncatedMarker)
	return selected.String()
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	for maxBytes > 0 && !utf8.ValidString(value[:maxBytes]) {
		maxBytes--
	}
	return value[:maxBytes]
}
