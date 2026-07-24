package scheduled

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
	"github.com/tamcore/kadence/internal/store"
)

var (
	ErrInvalidTransition = errors.New("scheduled: illegal task state transition")
	ErrStaleProposal     = errors.New("scheduled: proposal version is stale or missing")
	ErrRunInProgress     = errors.New("scheduled: task has a pending or running occurrence")
)

// ConversationStore is the narrow conversation persistence dependency needed
// for Scheduled definition threads.
type ConversationStore interface {
	CreateWithKind(context.Context, int64, string, string) (model.Conversation, error)
}

// MessageStore is the narrow message persistence dependency for definition
// history. It deliberately does not expose tool-call persistence.
type MessageStore interface {
	Add(context.Context, string, string, string) (model.Message, error)
	ListByConversation(context.Context, string) ([]model.Message, error)
}

// TaskStore keeps every lifecycle operation owner-scoped.
type TaskStore interface {
	Create(context.Context, model.ScheduledTask) (model.ScheduledTask, error)
	GetByID(context.Context, string, int64) (model.ScheduledTask, error)
	ListByUser(context.Context, int64) ([]model.ScheduledTask, error)
	BeginDraftRevision(context.Context, string, int64) (model.ScheduledTask, error)
	Pause(context.Context, string, int64, int) (model.ScheduledTask, error)
	Resume(context.Context, string, int64, int, time.Time) (model.ScheduledTask, error)
	SaveProposal(context.Context, model.ScheduledTask, int64, int) (model.ScheduledTask, error)
	ConfirmProposal(context.Context, string, int64, int, time.Time) (model.ScheduledTask, error)
	SoftDelete(context.Context, string, int64) error
	RunNow(context.Context, int64, string, string, time.Time) (model.ScheduledTaskRun, error)
	ListRuns(context.Context, string, int64) ([]model.ScheduledTaskRun, error)
	MarkRead(context.Context, string, int64) error
	UnreadCount(context.Context, int64) (int, error)
}

// Refiner is implemented by Compiler. Keeping it small makes definition
// lifecycle tests independent from provider transports.
type Refiner interface {
	Refine(context.Context, []provider.Message, []provider.ToolDefinition, int) (Refinement, error)
}

// ToolResolver resolves exactly the current user's visible tools. The service
// never calls tools and Compiler excludes the interactive credentials tool.
type ToolResolver func(context.Context, string) ([]provider.ToolDefinition, error)

type ServiceDeps struct {
	Conversations ConversationStore
	Messages      MessageStore
	Tasks         TaskStore
	Compiler      Refiner
	ToolsForUser  ToolResolver
	Now           func() time.Time
}

// Service owns definition/refinement and lifecycle transitions. Execution is
// intentionally outside this service and is introduced by the worker phase.
type Service struct{ deps ServiceDeps }

func NewService(deps ServiceDeps) *Service {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Service{deps: deps}
}

type Actor struct {
	ID       int64
	Username string
	Timezone string
}

type DefinitionResult struct {
	Task       model.ScheduledTask
	Refinement Refinement
}

type Detail struct {
	Task model.ScheduledTask
	Runs []model.ScheduledTaskRun
}

type ListResult struct {
	Tasks  []model.ScheduledTask
	Unread int
}

// Create starts a separate Scheduled conversation and persists a valid draft
// before the definition model is called.
func (s *Service) Create(ctx context.Context, actor Actor, message string) (DefinitionResult, error) {
	if strings.TrimSpace(message) == "" {
		return DefinitionResult{}, errors.New("scheduled: message is required")
	}
	if err := s.ready(); err != nil {
		return DefinitionResult{}, err
	}
	conversation, err := s.deps.Conversations.CreateWithKind(ctx, actor.ID, title(message), model.ConversationKindScheduled)
	if err != nil {
		return DefinitionResult{}, fmt.Errorf("scheduled: create conversation: %w", err)
	}
	task, err := s.deps.Tasks.Create(ctx, draftTask(actor, conversation.ID))
	if err != nil {
		return DefinitionResult{}, fmt.Errorf("scheduled: create draft: %w", err)
	}
	return s.Refine(ctx, actor, task.ID, message)
}

// Refine adds one user message to an owner-scoped draft task's definition
// conversation and persists the compiler's text and any complete proposal.
func (s *Service) Refine(ctx context.Context, actor Actor, taskID, message string) (DefinitionResult, error) {
	if strings.TrimSpace(message) == "" {
		return DefinitionResult{}, errors.New("scheduled: message is required")
	}
	if err := s.ready(); err != nil {
		return DefinitionResult{}, err
	}
	task, err := s.deps.Tasks.BeginDraftRevision(ctx, taskID, actor.ID)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrInvalidScheduledTaskState):
			return DefinitionResult{}, ErrInvalidTransition
		case errors.Is(err, store.ErrScheduledRunInProgress):
			return DefinitionResult{}, ErrRunInProgress
		}
		return DefinitionResult{}, err
	}
	return s.refine(ctx, actor, task, message)
}

func (s *Service) refine(ctx context.Context, actor Actor, task model.ScheduledTask, message string) (DefinitionResult, error) {
	revision := task.Version
	if _, err := s.deps.Messages.Add(ctx, task.ConversationID, model.MsgRoleUser, strings.TrimSpace(message)); err != nil {
		return DefinitionResult{}, fmt.Errorf("scheduled: save user message: %w", err)
	}
	history, err := s.deps.Messages.ListByConversation(ctx, task.ConversationID)
	if err != nil {
		return DefinitionResult{}, fmt.Errorf("scheduled: load definition history: %w", err)
	}
	tools, err := s.availableTools(ctx, actor.Username)
	if err != nil {
		return DefinitionResult{}, fmt.Errorf("scheduled: resolve visible tools: %w", err)
	}
	refinement, err := s.deps.Compiler.Refine(ctx, historyForCompiler(history), tools, task.Version)
	if err != nil {
		return DefinitionResult{}, err
	}
	if _, err := s.deps.Messages.Add(ctx, task.ConversationID, model.MsgRoleAssistant, assistantAudit(refinement)); err != nil {
		return DefinitionResult{}, fmt.Errorf("scheduled: save assistant message: %w", err)
	}
	if refinement.Proposal != nil {
		task = applyProposal(task, *refinement.Proposal)
		task.Version = revision
		updated, err := s.deps.Tasks.SaveProposal(ctx, task, actor.ID, revision)
		if err != nil {
			if errors.Is(err, store.ErrStaleScheduledProposal) {
				return DefinitionResult{}, ErrStaleProposal
			}
			return DefinitionResult{}, fmt.Errorf("scheduled: save proposal: %w", err)
		}
		task = updated
	}
	return DefinitionResult{Task: task, Refinement: refinement}, nil
}

func assistantAudit(refinement Refinement) string {
	if refinement.Proposal == nil {
		return refinement.Text
	}
	proposal, _ := json.Marshal(refinement.Proposal)
	return refinement.Text + "\n\nScheduled proposal audit: " + string(proposal)
}

func (s *Service) Confirm(ctx context.Context, actor Actor, taskID string, expectedVersion int) (model.ScheduledTask, error) {
	if err := s.ready(); err != nil {
		return model.ScheduledTask{}, err
	}
	task, err := s.deps.Tasks.GetByID(ctx, taskID, actor.ID)
	if err != nil {
		return model.ScheduledTask{}, err
	}
	if task.State != model.ScheduledTaskStateDraft || task.CompiledPrompt == "" || task.Version != expectedVersion {
		return model.ScheduledTask{}, ErrStaleProposal
	}
	now := s.deps.Now().UTC()
	next, err := taskNextRun(task, now)
	if err != nil {
		return model.ScheduledTask{}, err
	}
	confirmed, err := s.deps.Tasks.ConfirmProposal(ctx, taskID, actor.ID, expectedVersion, next)
	if errors.Is(err, store.ErrStaleScheduledProposal) {
		return model.ScheduledTask{}, ErrStaleProposal
	}
	return confirmed, err
}

func (s *Service) List(ctx context.Context, userID int64) (ListResult, error) {
	if err := s.ready(); err != nil {
		return ListResult{}, err
	}
	tasks, err := s.deps.Tasks.ListByUser(ctx, userID)
	if err != nil {
		return ListResult{}, err
	}
	unread, err := s.deps.Tasks.UnreadCount(ctx, userID)
	if err != nil {
		return ListResult{}, err
	}
	return ListResult{Tasks: tasks, Unread: unread}, nil
}

func (s *Service) Detail(ctx context.Context, userID int64, taskID string) (Detail, error) {
	if err := s.ready(); err != nil {
		return Detail{}, err
	}
	task, err := s.deps.Tasks.GetByID(ctx, taskID, userID)
	if err != nil {
		return Detail{}, err
	}
	runs, err := s.deps.Tasks.ListRuns(ctx, taskID, userID)
	if err != nil {
		return Detail{}, err
	}
	return Detail{Task: task, Runs: runs}, nil
}

func (s *Service) Pause(ctx context.Context, userID int64, taskID string) (model.ScheduledTask, error) {
	if err := s.ready(); err != nil {
		return model.ScheduledTask{}, err
	}
	task, err := s.deps.Tasks.GetByID(ctx, taskID, userID)
	if err != nil {
		return model.ScheduledTask{}, err
	}
	if task.State != model.ScheduledTaskStateActive {
		return model.ScheduledTask{}, ErrInvalidTransition
	}
	paused, err := s.deps.Tasks.Pause(ctx, taskID, userID, task.Version)
	if errors.Is(err, store.ErrInvalidScheduledTaskState) {
		return model.ScheduledTask{}, ErrInvalidTransition
	}
	return paused, err
}

func (s *Service) Resume(ctx context.Context, userID int64, taskID string) (model.ScheduledTask, error) {
	if err := s.ready(); err != nil {
		return model.ScheduledTask{}, err
	}
	task, err := s.deps.Tasks.GetByID(ctx, taskID, userID)
	if err != nil {
		return model.ScheduledTask{}, err
	}
	if task.State != model.ScheduledTaskStatePaused || task.CompiledPrompt == "" {
		return model.ScheduledTask{}, ErrInvalidTransition
	}
	next, err := scheduleFor(task).NextAfter(s.deps.Now().UTC())
	if err != nil {
		return model.ScheduledTask{}, err
	}
	resumed, err := s.deps.Tasks.Resume(ctx, taskID, userID, task.Version, next)
	if errors.Is(err, store.ErrInvalidScheduledTaskState) {
		return model.ScheduledTask{}, ErrInvalidTransition
	}
	return resumed, err
}

func (s *Service) Delete(ctx context.Context, userID int64, taskID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.deps.Tasks.SoftDelete(ctx, taskID, userID)
}

// RunNow records a distinct pending manual occurrence and marks the confirmed
// task due. The execution worker will claim this target in a later phase.
func (s *Service) RunNow(ctx context.Context, userID int64, taskID string) (model.ScheduledTaskRun, error) {
	if err := s.ready(); err != nil {
		return model.ScheduledTaskRun{}, err
	}
	now := s.deps.Now().UTC()
	run, err := s.deps.Tasks.RunNow(ctx, userID, taskID, "manual:"+uuid.NewString(), now)
	if errors.Is(err, store.ErrInvalidScheduledTaskState) {
		return model.ScheduledTaskRun{}, ErrInvalidTransition
	}
	return run, err
}

func (s *Service) MarkRead(ctx context.Context, userID int64, taskID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.deps.Tasks.MarkRead(ctx, taskID, userID)
}

func (s *Service) ready() error {
	if s == nil || s.deps.Conversations == nil || s.deps.Messages == nil || s.deps.Tasks == nil || s.deps.Compiler == nil {
		return errors.New("scheduled: lifecycle service dependencies are required")
	}
	return nil
}

func (s *Service) availableTools(ctx context.Context, username string) ([]provider.ToolDefinition, error) {
	if s.deps.ToolsForUser == nil {
		return nil, nil
	}
	return s.deps.ToolsForUser(ctx, username)
}

func draftTask(actor Actor, conversationID string) model.ScheduledTask {
	timezone := actor.Timezone
	if timezone == "" {
		timezone = "UTC"
	}
	return model.ScheduledTask{UserID: actor.ID, ConversationID: conversationID,
		Kind: string(TaskKindReminder), State: model.ScheduledTaskStateDraft, Timezone: timezone,
		ExecutionMode: string(ExecutionModeStatic), DeliveryPolicy: string(DeliveryPolicyAlways), InitialRun: string(InitialRunWait)}
}

func applyProposal(task model.ScheduledTask, proposal Proposal) model.ScheduledTask {
	task.Version, task.Name, task.Kind, task.CompiledPrompt = proposal.Version, proposal.Name, string(proposal.TaskKind), proposal.CompiledPrompt
	task.OneOffAt, task.DTStart, task.RRULE, task.Timezone = optionalTime(proposal.Schedule.At), optionalTime(proposal.Schedule.DTStart), proposal.Schedule.RRULE, proposal.Timezone
	task.ExecutionMode, task.AuthorizedTools = string(proposal.ExecutionMode), append([]string(nil), proposal.AuthorizedTools...)
	task.DeliveryPolicy, task.InitialRun, task.StopCondition, task.StaticMessage = string(proposal.DeliveryPolicy), string(proposal.InitialRun), proposal.StopCondition, proposal.StaticMessage
	return task
}

func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func taskNextRun(task model.ScheduledTask, now time.Time) (time.Time, error) {
	if task.InitialRun == string(InitialRunPreview) || task.InitialRun == string(InitialRunBaseline) {
		return now, nil
	}
	return scheduleFor(task).NextAfter(now)
}

func scheduleFor(task model.ScheduledTask) Schedule {
	s := Schedule{RRULE: task.RRULE, Timezone: task.Timezone}
	if task.OneOffAt != nil {
		s.At = *task.OneOffAt
	}
	if task.DTStart != nil {
		s.DTStart = *task.DTStart
	}
	return s
}

func historyForCompiler(history []model.Message) []provider.Message {
	out := make([]provider.Message, 0, len(history))
	for _, message := range history {
		if message.Role == model.MsgRoleUser || message.Role == model.MsgRoleAssistant {
			out = append(out, provider.Message{Role: message.Role, Content: message.Content})
		}
	}
	return out
}

func title(message string) string {
	runes := []rune(strings.TrimSpace(message))
	if len(runes) > 60 {
		return string(runes[:60])
	}
	return string(runes)
}
