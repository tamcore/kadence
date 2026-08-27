package chat_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/tamcore/kadence/internal/chat"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
	"github.com/tamcore/kadence/internal/scheduled"
)

const (
	replacementReply        = "replacement"
	testHandoffOne          = "handoff-one"
	testStrengthWorkoutTool = "garmin__create_strength_workout"
	testToolStatusDone      = "done"
	testTimezoneBerlin      = "Europe/Berlin"
	testProviderMustNotRun  = "must not run"
)

type fakeProvider struct {
	reply string
	err   error
}

func (f fakeProvider) StreamChat(_ context.Context, _ provider.ChatRequest, onToken provider.TokenFunc) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	for _, part := range []string{f.reply[:2], f.reply[2:]} {
		if err := onToken(part); err != nil {
			return "", err
		}
	}
	return f.reply, nil
}

func (f fakeProvider) StreamChatWithTools(ctx context.Context, req provider.ChatRequest, onToken provider.TokenFunc) (provider.StreamResult, error) {
	content, err := f.StreamChat(ctx, req, onToken)
	return provider.StreamResult{Content: content}, err
}

// scriptedProvider returns a pre-scripted StreamResult per call, streaming
// each result's content through onToken, so tests can exercise multi-call
// flows like truncation-continuation.
type scriptedProvider struct {
	results  []provider.StreamResult
	calls    int
	requests []provider.ChatRequest
}

type scheduledFailingProvider struct {
	results []provider.StreamResult
	failAt  int
	calls   int
}

func (p *scheduledFailingProvider) StreamChat(ctx context.Context, req provider.ChatRequest, onToken provider.TokenFunc) (string, error) {
	result, err := p.StreamChatWithTools(ctx, req, onToken)
	return result.Content, err
}

func (p *scheduledFailingProvider) StreamChatWithTools(_ context.Context, _ provider.ChatRequest, onToken provider.TokenFunc) (provider.StreamResult, error) {
	if p.calls >= len(p.results) {
		return provider.StreamResult{}, nil
	}
	result := p.results[p.calls]
	p.calls++
	if result.Content != "" {
		if err := onToken(result.Content); err != nil {
			return provider.StreamResult{}, err
		}
	}
	if p.calls == p.failAt {
		return result, errors.New("provider connection interrupted")
	}
	return result, nil
}

func (p *scriptedProvider) StreamChat(ctx context.Context, req provider.ChatRequest, onToken provider.TokenFunc) (string, error) {
	r, err := p.StreamChatWithTools(ctx, req, onToken)
	return r.Content, err
}

func (p *scriptedProvider) StreamChatWithTools(_ context.Context, req provider.ChatRequest, onToken provider.TokenFunc) (provider.StreamResult, error) {
	p.requests = append(p.requests, req)
	if p.calls >= len(p.results) {
		return provider.StreamResult{FinishReason: "stop"}, nil
	}
	r := p.results[p.calls]
	p.calls++
	if r.Content != "" {
		if err := onToken(r.Content); err != nil {
			return provider.StreamResult{}, err
		}
	}
	return r, nil
}

type fakeConvs struct {
	created            *model.Conversation
	byID               map[string]model.Conversation
	titleUpdateCalls   []titleUpdateCall
	titleUpdateResult  model.Conversation
	titleUpdateSwapped bool
	titleUpdateErr     error
}

type titleUpdateCall struct {
	id           string
	userID       int64
	currentTitle string
	newTitle     string
}

func (f *fakeConvs) Create(_ context.Context, userID int64, title string) (model.Conversation, error) {
	c := model.Conversation{ID: testNewConvID, UserID: userID, Title: title}
	f.created = &c
	return c, nil
}
func (f *fakeConvs) GetByID(_ context.Context, id string, userID int64) (model.Conversation, error) {
	if c, ok := f.byID[id]; ok && c.UserID == userID {
		return c, nil
	}
	return model.Conversation{}, errFakeNotFound
}

func (f *fakeConvs) UpdateTitleIfCurrent(
	_ context.Context, id string, userID int64, currentTitle, newTitle string,
) (model.Conversation, bool, error) {
	f.titleUpdateCalls = append(f.titleUpdateCalls, titleUpdateCall{
		id: id, userID: userID, currentTitle: currentTitle, newTitle: newTitle,
	})
	return f.titleUpdateResult, f.titleUpdateSwapped, f.titleUpdateErr
}

var errFakeNotFound = &fakeErr{}

type fakeErr struct{}

func (*fakeErr) Error() string { return "not found" }

type fakeMsgs struct {
	added                         []model.Message
	createdConversation           *model.Conversation
	rejectAssistant               bool
	assistantSaveExhaustsDeadline bool
	lastInput                     model.ChatUserInput
	historyErr                    error
	payloadErr                    error
	payloadRequests               [][]int64
	editCalls                     int
	regenerateCalls               int
	assistantSaveContextErrors    []error
	assistantSaveHadDeadlines     []bool
	assistantSaveDeadlineIn       []time.Duration
	assistantHandoffIDs           []string
	assistantHandoffTraces        [][]string
}

func (f *fakeMsgs) Add(_ context.Context, convID string, role, content string) (model.Message, error) {
	m := model.Message{ID: int64(len(f.added) + 1), ConversationID: convID, Role: role, Content: content}
	f.added = append(f.added, m)
	return m, nil
}
func (f *fakeMsgs) AddWithToolCalls(_ context.Context, convID string, role, content string, toolCalls []model.MessageToolCall) (model.Message, error) {
	m := model.Message{ID: int64(len(f.added) + 1), ConversationID: convID, Role: role, Content: content, ToolCalls: toolCalls}
	f.added = append(f.added, m)
	return m, nil
}
func (f *fakeMsgs) AddChatUser(ctx context.Context, convID, content string) (model.Message, error) {
	return f.Add(ctx, convID, model.MsgRoleUser, content)
}
func (f *fakeMsgs) AddChatUserInput(
	_ context.Context, convID string, _ int64, input model.ChatUserInput,
) (model.Message, error) {
	f.lastInput = input
	attachments := slices.Clone(input.Attachments)
	for i := range attachments {
		attachments[i].ID = int64(i + 1)
		attachments[i].MessageID = int64(len(f.added) + 1)
		attachments[i].Ordinal = i
	}
	m := model.Message{
		ID: int64(len(f.added) + 1), ConversationID: convID,
		Role: model.MsgRoleUser, Content: input.Content,
		Attachments: attachments,
	}
	for ordinal, documentID := range input.DocumentIDs {
		id := documentID
		m.DocumentReferences = append(m.DocumentReferences, model.MessageDocumentReference{
			DocumentID: &id, Filename: testSelectedDocFilename, Scope: model.ScopePrivate,
			Ordinal: ordinal, Available: true,
		})
	}
	f.added = append(f.added, m)
	return m, nil
}
func (f *fakeMsgs) CreateConversationWithChatUserInput(
	ctx context.Context, userID int64, title string, input model.ChatUserInput,
) (model.Conversation, model.Message, error) {
	conversation := model.Conversation{
		ID: testNewConvID, UserID: userID, Title: title, Kind: model.ConversationKindChat,
	}
	f.createdConversation = &conversation
	message, err := f.AddChatUserInput(ctx, conversation.ID, userID, input)
	return conversation, message, err
}
func (f *fakeMsgs) UpdateChatAttachmentExtractions(
	_ context.Context, convID string, messageID, _ int64,
	attachments []model.MessageAttachment,
) (model.Message, error) {
	for i := range f.added {
		if f.added[i].ConversationID != convID || f.added[i].ID != messageID {
			continue
		}
		if len(f.added[i].Attachments) != len(attachments) {
			return model.Message{}, errFakeNotFound
		}
		for j := range attachments {
			if f.added[i].Attachments[j].ID != attachments[j].ID {
				return model.Message{}, errFakeNotFound
			}
			f.added[i].Attachments[j].ExtractedMarkdown =
				attachments[j].ExtractedMarkdown
			f.added[i].Attachments[j].ExtractionComplete = true
		}
		return f.added[i], nil
	}
	return model.Message{}, errFakeNotFound
}
func (f *fakeMsgs) AddChatAssistantIfLatestUser(
	ctx context.Context, convID string, expectedUser model.Message, content string, toolCalls []model.MessageToolCall, handoffIDs []string,
) (model.Message, error) {
	deadline, hadDeadline := ctx.Deadline()
	f.assistantSaveHadDeadlines = append(f.assistantSaveHadDeadlines, hadDeadline)
	remaining := time.Duration(0)
	if hadDeadline {
		remaining = time.Until(deadline)
	}
	f.assistantSaveDeadlineIn = append(f.assistantSaveDeadlineIn, remaining)
	f.assistantSaveContextErrors = append(f.assistantSaveContextErrors, ctx.Err())
	f.assistantHandoffIDs = slices.Clone(handoffIDs)
	f.assistantHandoffTraces = append(f.assistantHandoffTraces, slices.Clone(handoffIDs))
	if f.assistantSaveExhaustsDeadline {
		if deadline, ok := ctx.Deadline(); ok {
			<-time.After(time.Until(deadline))
		}
		return model.Message{}, errFakeNotFound
	}
	if f.rejectAssistant {
		return model.Message{}, errFakeNotFound
	}
	if len(f.added) == 0 {
		return model.Message{}, errFakeNotFound
	}
	latest := f.added[len(f.added)-1]
	if latest.Role != model.MsgRoleUser || latest.ID != expectedUser.ID || latest.Content != expectedUser.Content {
		return model.Message{}, errFakeNotFound
	}
	return f.AddWithToolCalls(ctx, convID, model.MsgRoleAssistant, content, toolCalls)
}

type fakeScheduledHandoff struct {
	artifacts            []scheduled.ChatArtifact
	requests             []scheduled.HandoffRequest
	actors               []scheduled.Actor
	cleanup              [][]string
	cleanupContextErrors []error
	confirmation         scheduled.ChatConfirmation
	confirmationErr      error
	confirmationCalls    int
	confirmationActor    scheduled.Actor
	confirmationChat     string
	err                  error
}

func (f *fakeScheduledHandoff) DraftFromChat(
	_ context.Context, actor scheduled.Actor, req scheduled.HandoffRequest,
) (scheduled.ChatArtifact, error) {
	f.actors = append(f.actors, actor)
	f.requests = append(f.requests, req)
	if f.err != nil {
		return scheduled.ChatArtifact{}, f.err
	}
	index := len(f.requests) - 1
	if index < len(f.artifacts) {
		return f.artifacts[index], nil
	}
	return scheduled.ChatArtifact{HandoffID: "handoff-" + strconv.Itoa(index+1), TaskID: "task-" + strconv.Itoa(index+1), Ordinal: index + 1, ArtifactState: testScheduledArtifactReady}, nil
}

func (f *fakeScheduledHandoff) CleanupChatDrafts(ctx context.Context, _ int64, ids []string) error {
	f.cleanup = append(f.cleanup, slices.Clone(ids))
	f.cleanupContextErrors = append(f.cleanupContextErrors, ctx.Err())
	return nil
}
func (f *fakeScheduledHandoff) ConfirmSoleChatDraft(
	_ context.Context, actor scheduled.Actor, conversationID string,
) (scheduled.ChatConfirmation, error) {
	f.confirmationCalls++
	f.confirmationActor, f.confirmationChat = actor, conversationID
	return f.confirmation, f.confirmationErr
}
func (f *fakeMsgs) ListByConversation(_ context.Context, _ string) ([]model.Message, error) {
	return f.added, nil
}
func (f *fakeMsgs) ListChatHistory(_ context.Context, _ string) ([]model.Message, error) {
	if f.historyErr != nil {
		return nil, f.historyErr
	}
	history := slices.Clone(f.added)
	for i := range history {
		history[i].Attachments = slices.Clone(history[i].Attachments)
		for j := range history[i].Attachments {
			history[i].Attachments[j].RawBytes = nil
			history[i].Attachments[j].ExtractedMarkdown = ""
		}
		history[i].DocumentReferences = slices.Clone(history[i].DocumentReferences)
	}
	return history, nil
}
func (f *fakeMsgs) LoadChatAttachmentPayloads(
	_ context.Context, _ string, messageIDs []int64,
) (map[int64][]model.MessageAttachment, error) {
	return f.loadChatAttachmentPayloads(messageIDs)
}
func (f *fakeMsgs) LoadChatAttachmentProviderPayloads(
	_ context.Context, _ string, messageIDs []int64,
) (map[int64][]model.MessageAttachment, error) {
	return f.loadChatAttachmentPayloads(messageIDs)
}
func (f *fakeMsgs) loadChatAttachmentPayloads(
	messageIDs []int64,
) (map[int64][]model.MessageAttachment, error) {
	f.payloadRequests = append(
		f.payloadRequests, slices.Clone(messageIDs),
	)
	if f.payloadErr != nil {
		return nil, f.payloadErr
	}
	requested := make(map[int64]bool, len(messageIDs))
	for _, messageID := range messageIDs {
		requested[messageID] = true
	}
	payloads := make(map[int64][]model.MessageAttachment, len(messageIDs))
	for _, message := range f.added {
		if !requested[message.ID] {
			continue
		}
		payloads[message.ID] = slices.Clone(message.Attachments)
	}
	return payloads, nil
}
func (f *fakeMsgs) EditAndRewind(_ context.Context, _ string, messageID, _ int64, content string) (model.Message, error) {
	f.editCalls++
	for i := range f.added {
		if f.added[i].ID != messageID {
			continue
		}
		if f.added[i].Role != model.MsgRoleUser {
			return model.Message{}, errFakeNotFound
		}
		f.added[i].Content = content
		f.added = f.added[:i+1]
		return f.added[i], nil
	}
	return model.Message{}, errFakeNotFound
}
func (f *fakeMsgs) RegenerateAndRewind(_ context.Context, _ string, messageID, _ int64) (model.Message, error) {
	f.regenerateCalls++
	for i := range f.added {
		if f.added[i].ID != messageID {
			continue
		}
		if f.added[i].Role != model.MsgRoleAssistant || i == 0 {
			return model.Message{}, errFakeNotFound
		}
		prompt := f.added[i-1]
		f.added = f.added[:i]
		return prompt, nil
	}
	return model.Message{}, errFakeNotFound
}

type chatAuditStore struct {
	started                model.MCPAuditCall
	finished               model.MCPAuditCall
	startCount             int
	finishCount            int
	startDeadlineRemaining time.Duration
	startContextErr        error
	finishContextErr       error
}

func (s *chatAuditStore) Start(ctx context.Context, call model.MCPAuditCall) (int64, error) {
	s.started = call
	s.startCount++
	if deadline, ok := ctx.Deadline(); ok {
		s.startDeadlineRemaining = time.Until(deadline)
	}
	s.startContextErr = ctx.Err()
	return 9, nil
}

func (s *chatAuditStore) Finish(ctx context.Context, id int64, status, result, errorText string, finishedAt time.Time) error {
	s.finished = model.MCPAuditCall{ID: id, Status: status, Result: result, Error: errorText, FinishedAt: &finishedAt}
	s.finishCount++
	s.finishContextErr = ctx.Err()
	return nil
}

type capturingSink struct{ events []chat.ChatEvent }

func (s *capturingSink) Send(e chat.ChatEvent) error { s.events = append(s.events, e); return nil }
func (s *capturingSink) Flush() error                { return nil }

type titleDeliveryFailSink struct {
	capturingSink
	failSend  bool
	failFlush bool
	lastType  string
	doneSends int
	doneFlush int
}

func (s *titleDeliveryFailSink) Send(e chat.ChatEvent) error {
	s.lastType = e.Type
	if e.Type == chat.EventDone {
		s.doneSends++
	}
	if e.Type == chat.EventTitle && s.failSend {
		return errors.New("title delivery marker")
	}
	s.events = append(s.events, e)
	return nil
}

func (s *titleDeliveryFailSink) Flush() error {
	if s.lastType == chat.EventDone {
		s.doneFlush++
	}
	if s.lastType == chat.EventTitle && s.failFlush {
		return errors.New("title delivery marker")
	}
	return nil
}

type fakeTitleGenerator struct {
	title  string
	err    error
	inputs []chat.ConversationTitleInput
}

func (f *fakeTitleGenerator) Generate(
	_ context.Context, in chat.ConversationTitleInput,
) (string, error) {
	f.inputs = append(f.inputs, in)
	return f.title, f.err
}

// syncCapturingSink is a mutex-guarded capturingSink for tests where a
// goroutine polls sink.events concurrently with Stream still running (e.g.
// waiting for a credentials_request event to submit values for). Plain
// capturingSink is not safe for that concurrent read/write pattern.
type syncCapturingSink struct {
	mu     sync.Mutex
	events []chat.ChatEvent
}

func (s *syncCapturingSink) Send(e chat.ChatEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return nil
}
func (s *syncCapturingSink) Flush() error { return nil }

// snapshot returns a copy of the events recorded so far.
func (s *syncCapturingSink) snapshot() []chat.ChatEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]chat.ChatEvent, len(s.events))
	copy(out, s.events)
	return out
}

const (
	testReply                  = "Hello!"
	testSystemMsg              = "You are a coach."
	testModel                  = "m"
	testMaxTokens              = 64
	testTemp                   = 0.2
	testUserID                 = 7
	testUsername               = "alice"
	testConvID                 = "conv-uuid-1"
	testNewConvID              = "conv-uuid-new"
	testConvTitle              = "test"
	testScheduledToolName      = "kadence__draft_future_unattended_task"
	testScheduledArtifactReady = "ready"
	testScheduledCallID        = "call"
	testDirectDomainCallID     = "calendar-call"
	testDirectDomainToolName   = "calendar__schedule_event"
	testDirectDomainArguments  = `{"start":"2040-01-02T08:00:00Z"}`
	testScheduledArguments     = `{"instruction":"check recovery"}`
	testSelectedDocFilename    = "selected.md"
	testAssistantAnswer        = "answer"
	testUserLater              = "later"
	testGeneratedTitle         = "Marathon Pacing Review"
	testOperationEdit          = "edit"
	testFirstUserMessage       = "first"
	testOldAssistantResponse   = "old response"
	testScheduledTaskOne       = "task-one"
)
