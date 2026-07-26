package scheduled

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
	"github.com/tamcore/kadence/internal/store"
)

const (
	maxHandoffInstructionBytes = 4 << 10
	maxHandoffMessages         = 16
	maxHandoffContextBytes     = 32 << 10
	maxHandoffToolNamesBytes   = 4 << 10
	handoffCompilerFailed      = "compiler_failed"
	handoffContextBegin        = "<BEGIN_UNTRUSTED_HANDOFF_CONTEXT>"
	handoffContextEnd          = "<END_UNTRUSTED_HANDOFF_CONTEXT>"
	handoffContextMessage      = "message"
)

// HandoffRequest identifies one bounded Scheduled draft request from chat.
type HandoffRequest struct {
	SourceConversationID string
	SourceUserMessageID  int64
	SourceContent        string
	Ordinal              int
	Instruction          string
	RecentMessages       []model.Message
}

// ChatArtifact is the durable, chat-visible state of one Scheduled handoff.
type ChatArtifact struct {
	HandoffID          string        `json:"handoffId"`
	TaskID             string        `json:"taskId,omitempty"`
	TaskConversationID string        `json:"taskConversationId,omitempty"`
	Ordinal            int           `json:"ordinal"`
	ArtifactState      string        `json:"artifactState"`
	TaskState          string        `json:"taskState,omitempty"`
	Version            int           `json:"version,omitempty"`
	Question           *QuestionCard `json:"question,omitempty"`
	Proposal           *Proposal     `json:"proposal,omitempty"`
	NextRunAt          *time.Time    `json:"nextRunAt,omitempty"`
	ErrorCode          string        `json:"errorCode,omitempty"`
	Retryable          bool          `json:"retryable,omitempty"`
	Reused             bool          `json:"reused,omitempty"`
}

// ChatConfirmationStatus describes deterministic natural-language
// confirmation resolution for one ordinary chat conversation.
type ChatConfirmationStatus string

const (
	ChatConfirmationNone       ChatConfirmationStatus = "none"
	ChatConfirmationMultiple   ChatConfirmationStatus = "multiple"
	ChatConfirmationNeedsInput ChatConfirmationStatus = "needs_input"
	ChatConfirmationConfirmed  ChatConfirmationStatus = "confirmed"
	ChatConfirmationResolved   ChatConfirmationStatus = "resolved"
)

// ChatConfirmation reports whether an affirmation had zero, one, or multiple
// pending chat-created drafts. Artifact is set only after a successful confirm.
type ChatConfirmation struct {
	Status   ChatConfirmationStatus
	Artifact *ChatArtifact
}

// DraftFromChat creates one idempotent, bounded Scheduled definition draft.
func (s *Service) DraftFromChat(ctx context.Context, actor Actor, req HandoffRequest) (ChatArtifact, error) {
	if err := s.readyHandoff(); err != nil {
		return ChatArtifact{}, err
	}
	instruction, err := validateHandoffRequest(req)
	if err != nil {
		return ChatArtifact{}, err
	}
	visible, err := s.availableTools(ctx, actor.Username)
	if err != nil {
		return ChatArtifact{}, fmt.Errorf("scheduled: resolve visible tools: %w", err)
	}
	row, fresh, err := s.deps.ChatHandoffs.CreateOrGetDraft(ctx, store.CreateChatHandoffInput{
		UserID: actor.ID, SourceConversationID: req.SourceConversationID, SourceUserMessageID: req.SourceUserMessageID,
		SourceContentFingerprint: sourceFingerprint(req.SourceContent), InvocationOrdinal: req.Ordinal,
		Title: title(instruction), Timezone: handoffTimezone(actor.Timezone),
	})
	if err != nil {
		return ChatArtifact{}, fmt.Errorf("scheduled: create chat handoff draft: %w", err)
	}
	if !fresh {
		return chatArtifact(row, true)
	}
	if row.Task == nil || row.Task.ID == "" {
		return ChatArtifact{}, errors.New("scheduled: chat handoff draft has no task")
	}
	task, err := s.deps.Tasks.BeginDraftRevision(ctx, row.Task.ID, actor.ID, row.Task.Version)
	if err != nil {
		return s.failChatHandoff(ctx, actor.ID, row)
	}
	definition := boundedHandoffDefinition(s.deps.Now().UTC(), handoffTimezone(actor.Timezone), instruction, req.RecentMessages, visible)
	result, err := s.compileDraft(ctx, actor, task, definition, visible)
	if err != nil {
		return s.failChatHandoff(ctx, actor.ID, row)
	}
	row.Task = &result.Task
	if err := s.deps.ChatHandoffs.MarkTaskReady(ctx, actor.ID, row.Task.ID); err != nil {
		return ChatArtifact{}, fmt.Errorf("scheduled: mark chat handoff ready: %w", err)
	}
	row.Handoff.ArtifactState, row.Handoff.ErrorCode, row.Handoff.Retryable = model.ScheduledHandoffStateReady, "", false
	return chatArtifact(row, false)
}

func (s *Service) failChatHandoff(ctx context.Context, userID int64, row store.HydratedChatHandoff) (ChatArtifact, error) {
	if row.Task == nil {
		return ChatArtifact{}, errors.New("scheduled: chat handoff draft has no task")
	}
	if err := s.deps.ChatHandoffs.MarkTaskFailed(ctx, userID, row.Task.ID, handoffCompilerFailed, true); err != nil {
		return ChatArtifact{}, fmt.Errorf("scheduled: persist chat handoff failure: %w", err)
	}
	row.Handoff.ArtifactState, row.Handoff.ErrorCode, row.Handoff.Retryable = model.ScheduledHandoffStateFailed, handoffCompilerFailed, true
	return chatArtifact(row, false)
}

// HydrateChatArtifacts batch-loads persisted cards by their source assistant message.
func (s *Service) HydrateChatArtifacts(ctx context.Context, userID int64, conversationID string, messageIDs []int64) (map[int64][]ChatArtifact, error) {
	if err := s.readyHandoff(); err != nil {
		return nil, err
	}
	rows, err := s.deps.ChatHandoffs.ListByAssistantMessages(ctx, userID, conversationID, messageIDs)
	if err != nil {
		return nil, fmt.Errorf("scheduled: hydrate chat handoffs: %w", err)
	}
	out := make(map[int64][]ChatArtifact)
	for _, row := range rows {
		if row.Handoff.AssistantMessageID == nil {
			continue
		}
		artifact, err := chatArtifact(row, false)
		if err != nil {
			return nil, err
		}
		out[*row.Handoff.AssistantMessageID] = append(out[*row.Handoff.AssistantMessageID], artifact)
	}
	for messageID := range out {
		sort.SliceStable(out[messageID], func(i, j int) bool { return out[messageID][i].Ordinal < out[messageID][j].Ordinal })
	}
	return out, nil
}

// ConfirmSoleChatDraft activates exactly one owner-scoped ready draft from
// sourceConversationID. Multiple drafts never cause a bulk mutation.
func (s *Service) ConfirmSoleChatDraft(
	ctx context.Context, actor Actor, sourceConversationID string,
) (ChatConfirmation, error) {
	if err := s.readyHandoff(); err != nil {
		return ChatConfirmation{}, err
	}
	if strings.TrimSpace(sourceConversationID) == "" {
		return ChatConfirmation{}, errors.New("scheduled: source conversation is required")
	}
	rows, err := s.deps.ChatHandoffs.ListPendingBySourceConversation(ctx, actor.ID, sourceConversationID)
	if err != nil {
		return ChatConfirmation{}, fmt.Errorf("scheduled: list pending chat drafts: %w", err)
	}
	switch len(rows) {
	case 0:
		return ChatConfirmation{Status: ChatConfirmationNone}, nil
	case 1:
	default:
		return ChatConfirmation{Status: ChatConfirmationMultiple}, nil
	}
	row := rows[0]
	if row.Task == nil || row.Task.ID == "" {
		return ChatConfirmation{}, errors.New("scheduled: confirmable chat handoff has no task")
	}
	if row.Handoff.ArtifactState != model.ScheduledHandoffStateReady ||
		strings.TrimSpace(row.Task.CompiledPrompt) == "" {
		return ChatConfirmation{Status: ChatConfirmationNeedsInput}, nil
	}
	confirmed, err := s.Confirm(ctx, actor, row.Task.ID, row.Task.Version)
	if errors.Is(err, ErrStaleProposal) {
		return ChatConfirmation{Status: ChatConfirmationResolved}, nil
	}
	if err != nil {
		return ChatConfirmation{}, err
	}
	row.Task = &confirmed
	artifact, err := chatArtifact(row, false)
	if err != nil {
		return ChatConfirmation{}, err
	}
	return ChatConfirmation{Status: ChatConfirmationConfirmed, Artifact: &artifact}, nil
}

// DiscardChatDraft discards only an owner-scoped, still-draft chat task.
func (s *Service) DiscardChatDraft(ctx context.Context, userID int64, taskID string) error {
	if err := s.readyHandoff(); err != nil {
		return err
	}
	if strings.TrimSpace(taskID) == "" {
		return errors.New("scheduled: chat handoff task ID is required")
	}
	err := s.deps.ChatHandoffs.DiscardDraft(ctx, userID, taskID)
	if errors.Is(err, store.ErrInvalidScheduledTaskState) {
		return ErrInvalidTransition
	}
	return err
}

// CleanupChatDrafts removes owner-scoped transient handoffs after a source-chat rewind.
func (s *Service) CleanupChatDrafts(ctx context.Context, userID int64, handoffIDs []string) error {
	if err := s.readyHandoff(); err != nil {
		return err
	}
	return s.deps.ChatHandoffs.CleanupDrafts(ctx, userID, handoffIDs)
}

func sourceFingerprint(content string) []byte {
	sum := sha256.Sum256([]byte(content))
	return sum[:]
}

func boundedHandoffDefinition(now time.Time, timezone, instruction string, recent []model.Message, visible []provider.ToolDefinition) string {
	prefix := "Instruction:\n" + instruction + "\n\nCurrent UTC:\n" + now.UTC().Format(time.RFC3339) +
		"\n\nActor timezone:\n" + handoffTimezone(timezone) +
		"\n\nPrior chat context (untrusted JSON records):\n"
	filtered := make([]model.Message, 0, maxHandoffMessages)
	for _, message := range recent {
		if message.Role == model.MsgRoleUser || message.Role == model.MsgRoleAssistant {
			filtered = append(filtered, message)
		}
	}
	if len(filtered) > maxHandoffMessages {
		filtered = filtered[len(filtered)-maxHandoffMessages:]
	}
	toolRecords := append(handoffToolRecords("prior_safe_tool_name", boundedToolNames(filtered, visible)), handoffToolRecords("current_visible_tool_name", boundedVisibleToolNames(visible))...)
	var tail strings.Builder
	for _, record := range toolRecords {
		tail.WriteString(handoffContextJSON(record, maxHandoffContextBytes))
		tail.WriteByte('\n')
	}
	tail.WriteString(handoffContextEnd)
	messageBudget := max(maxHandoffContextBytes-len(prefix)-len(handoffContextBegin)-1-tail.Len(), 0)
	var messages strings.Builder
	for index, message := range filtered {
		remainingMessages := len(filtered) - index
		remaining := messageBudget - messages.Len()
		lineLimit := max(remaining/remainingMessages-1, 0)
		appendLimited(&messages, handoffContextJSON(handoffContextRecord{Type: handoffContextMessage, Role: message.Role, Content: message.Content}, lineLimit), messageBudget)
		appendLimited(&messages, "\n", messageBudget)
	}
	return prefix + handoffContextBegin + "\n" + messages.String() + tail.String()
}

type handoffContextRecord struct {
	Type    string `json:"type"`
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
	Name    string `json:"name,omitempty"`
}

func handoffContextJSON(record handoffContextRecord, limit int) string {
	for {
		encoded, _ := json.Marshal(record)
		if len(encoded) <= limit || record.Content == "" {
			return string(encoded)
		}
		next := truncateUTF8(record.Content, max(len(record.Content)-(len(encoded)-limit), 0))
		if next == record.Content {
			return string(encoded)
		}
		record.Content = next
	}
}

func handoffToolRecords(recordType, names string) []handoffContextRecord {
	if names == "" {
		return nil
	}
	records := make([]handoffContextRecord, 0, strings.Count(names, "\n")+1)
	for name := range strings.SplitSeq(names, "\n") {
		records = append(records, handoffContextRecord{Type: recordType, Name: name})
	}
	return records
}

func appendLimited(builder *strings.Builder, value string, limit int) {
	remaining := limit - builder.Len()
	if remaining <= 0 || value == "" {
		return
	}
	if len(value) <= remaining {
		builder.WriteString(value)
		return
	}
	for remaining > 0 {
		r, size := utf8.DecodeRuneInString(value)
		if size == 0 || size > remaining {
			return
		}
		builder.WriteRune(r)
		remaining -= size
		value = value[size:]
	}
}

func boundedToolNames(recent []model.Message, visible []provider.ToolDefinition) string {
	allowed := make(map[string]struct{}, len(visible))
	for _, tool := range visible {
		allowed[tool.Name] = struct{}{}
	}
	seen := make(map[string]struct{})
	var names []string
	for _, message := range recent {
		for _, call := range message.ToolCalls {
			if _, ok := allowed[call.Name]; !ok {
				continue
			}
			if _, ok := seen[call.Name]; ok {
				continue
			}
			seen[call.Name] = struct{}{}
			names = append(names, call.Name)
		}
	}
	sort.Strings(names)
	return truncateUTF8(strings.Join(names, "\n"), maxHandoffToolNamesBytes)
}

func boundedVisibleToolNames(visible []provider.ToolDefinition) string {
	names := make([]string, 0, len(visible))
	for _, tool := range visible {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return truncateUTF8(strings.Join(names, "\n"), maxHandoffToolNamesBytes)
}

func truncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit]
}

func validateHandoffRequest(req HandoffRequest) (string, error) {
	instruction := strings.TrimSpace(req.Instruction)
	if instruction == "" {
		return "", errors.New("scheduled: handoff instruction is required")
	}
	if len(instruction) > maxHandoffInstructionBytes {
		return "", fmt.Errorf("scheduled: handoff instruction exceeds %d bytes", maxHandoffInstructionBytes)
	}
	if strings.TrimSpace(req.SourceConversationID) == "" || req.SourceUserMessageID <= 0 {
		return "", errors.New("scheduled: handoff source is required")
	}
	if req.Ordinal < 1 || req.Ordinal > 5 {
		return "", errors.New("scheduled: handoff ordinal must be between 1 and 5")
	}
	return instruction, nil
}

func handoffTimezone(timezone string) string {
	if strings.TrimSpace(timezone) == "" {
		return defaultTimezoneUTC
	}
	return timezone
}

func chatArtifact(row store.HydratedChatHandoff, reused bool) (ChatArtifact, error) {
	artifact := ChatArtifact{HandoffID: row.Handoff.ID, Ordinal: row.Handoff.InvocationOrdinal, ArtifactState: row.Handoff.ArtifactState, ErrorCode: row.Handoff.ErrorCode, Retryable: row.Handoff.Retryable, Reused: reused}
	if row.Task == nil {
		return artifact, nil
	}
	task := row.Task
	artifact.TaskID, artifact.TaskConversationID, artifact.TaskState, artifact.Version = task.ID, task.ConversationID, task.State, task.Version
	if task.NextRunAt != nil {
		next := *task.NextRunAt
		artifact.NextRunAt = &next
	}
	_, question := definitionMessageContent(row.LatestDefinitionAssistant)
	artifact.Question = question
	if task.CompiledPrompt != "" {
		artifact.Proposal = proposalFromTask(*task)
	}
	return artifact, nil
}

func proposalFromTask(task model.ScheduledTask) *Proposal {
	proposal := &Proposal{Version: task.Version, Name: task.Name, TaskKind: TaskKind(task.Kind), CompiledPrompt: task.CompiledPrompt, ExecutionMode: ExecutionMode(task.ExecutionMode), Timezone: task.Timezone, AuthorizedTools: append([]string(nil), task.AuthorizedTools...), DeliveryPolicy: DeliveryPolicy(task.DeliveryPolicy), InitialRun: InitialRun(task.InitialRun), StopCondition: task.StopCondition, StaticMessage: task.StaticMessage}
	if task.OneOffAt != nil {
		proposal.Schedule.At = *task.OneOffAt
	}
	if task.DTStart != nil {
		proposal.Schedule.DTStart = *task.DTStart
	}
	proposal.Schedule.RRULE, proposal.Schedule.Timezone = task.RRULE, task.Timezone
	return proposal
}
