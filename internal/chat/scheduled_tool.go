package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
	"github.com/tamcore/kadence/internal/scheduled"
)

const (
	draftFutureUnattendedTaskToolName = "kadence__draft_future_unattended_task"
	legacyDraftScheduledTaskToolName  = "kadence__draft_scheduled_task"
	maxScheduledDraftCalls            = 5
)

var draftScheduledTaskParameters = json.RawMessage(`{
  "type":"object",
  "properties":{
    "instruction":{
      "type":"string",
      "minLength":1,
      "maxLength":4096,
      "description":"One independently confirmable unattended task."
    }
  },
  "required":["instruction"],
  "additionalProperties":false
}`)

type draftScheduledTaskArgs struct {
	Instruction string `json:"instruction"`
}

func draftFutureUnattendedTaskToolDefinition() provider.ToolDefinition {
	return provider.ToolDefinition{
		Name:        draftFutureUnattendedTaskToolName,
		Description: "Draft one independently confirmable future unattended task from an explicit request in the current user turn. Use it for a future retry or follow-up. Do not use it to execute or schedule work now, or to perform a direct calendar or domain operation. It only creates a draft: never claim the task is activated.",
		Parameters:  draftScheduledTaskParameters,
	}
}

func parseDraftScheduledTaskArgs(raw string) (draftScheduledTaskArgs, error) {
	var args draftScheduledTaskArgs
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return draftScheduledTaskArgs{}, fmt.Errorf("decode scheduled task arguments: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return draftScheduledTaskArgs{}, errors.New("decode scheduled task arguments: trailing JSON")
	}
	args.Instruction = strings.TrimSpace(args.Instruction)
	if args.Instruction == "" || len([]byte(args.Instruction)) > 4096 {
		return draftScheduledTaskArgs{}, errors.New("scheduled task instruction must be 1 to 4096 bytes")
	}
	return args, nil
}

func (s *Service) handleDraftScheduledTask(
	ctx context.Context, conversationID string, actor scheduled.Actor, sourceContent string, sourceUserMessageID int64,
	history []model.Message, state *toolTurnState, tc provider.ToolCall, sink EventSink,
) provider.Message {
	if state.ScheduledCalls >= maxScheduledDraftCalls {
		return provider.Message{Role: toolMsgRole, ToolCallID: tc.ID, Name: tc.Name,
			Content: "error: no more than 5 scheduled task drafts may be requested in one turn"}
	}
	state.ScheduledCalls++

	args, err := parseDraftScheduledTaskArgs(tc.Arguments)
	if err != nil {
		return provider.Message{Role: toolMsgRole, ToolCallID: tc.ID, Name: tc.Name,
			Content: "error: invalid scheduled task request"}
	}
	artifact, err := s.scheduled.DraftFromChat(ctx, actor, scheduled.HandoffRequest{
		SourceConversationID: conversationID,
		SourceUserMessageID:  sourceUserMessageID,
		SourceContent:        sourceContent,
		Ordinal:              state.ScheduledCalls,
		Instruction:          args.Instruction,
		RecentMessages:       history,
	})
	if err != nil {
		return provider.Message{Role: toolMsgRole, ToolCallID: tc.ID, Name: tc.Name,
			Content: "error: could not draft scheduled task"}
	}
	state.Handoffs = append(state.Handoffs, artifact)
	_ = sink.Send(ChatEvent{Type: EventScheduledArtifact, ScheduledArtifact: &artifact})
	_ = sink.Flush()
	result, marshalErr := json.Marshal(struct {
		TaskID            string `json:"taskId,omitempty"`
		Ordinal           int    `json:"ordinal"`
		ArtifactState     string `json:"artifactState"`
		AwaitConfirmation bool   `json:"awaitExplicitConfirmation"`
	}{
		TaskID: artifact.TaskID, Ordinal: artifact.Ordinal, ArtifactState: artifact.ArtifactState,
		AwaitConfirmation: true,
	})
	if marshalErr != nil {
		return provider.Message{Role: toolMsgRole, ToolCallID: tc.ID, Name: tc.Name,
			Content: "error: could not draft scheduled task"}
	}
	return provider.Message{Role: toolMsgRole, ToolCallID: tc.ID, Name: tc.Name, Content: string(result)}
}

func handoffIDs(artifacts []scheduled.ChatArtifact) []string {
	ids := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		if artifact.HandoffID != "" {
			ids = append(ids, artifact.HandoffID)
		}
	}
	return ids
}
