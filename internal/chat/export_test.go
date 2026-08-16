package chat

import (
	"context"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
)

// Test-only accessors for the forced-answer notices, so the external test
// package can assert that each limit explains itself distinctly.

// ForcedByIterationCapNotice returns the notice shown when a turn spends its
// whole tool-iteration budget.
func ForcedByIterationCapNotice() string { return forcedByIterationCap.notice() }

// ForcedByContextBudgetNotice returns the notice shown when a turn's gathered
// context no longer fits the budget.
func ForcedByContextBudgetNotice() string { return forcedByContextBudget.notice() }

// dispatchTool routes one tool call the way a chat turn would, with the turn
// state a real turn carries defaulted away. Test-only: production always goes
// through dispatchToolWithTurn with the live turn's state.
func (s *Service) dispatchTool(
	ctx, streamCtx context.Context, conversationID string, userID int64, username string,
	mcpSnap MCPUserSnapshot, tc provider.ToolCall,
	gated map[string]bool, redactor *turnRedactor, sink EventSink,
) provider.Message {
	return s.dispatchToolWithTurn(
		ctx, streamCtx, conversationID, userID, UserContext{Username: username}, model.Message{}, nil,
		mcpSnap, tc, gated, &toolTurnState{}, redactor, sink,
	)
}

// fitRoutesForSnapshot resolves the configured FIT routes against one snapshot.
// Test-only: production resolves them inline where the tool set is assembled.
func (s *Service) fitRoutesForSnapshot(mcpSnap MCPUserSnapshot) []resolvedFITRoute {
	return resolveFITRoutes(mcpSnap, s.fitRoutes)
}
