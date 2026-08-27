package mcpaudit

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/model"
)

const (
	testActorUsername = "alice"
	testConversation  = "chat-id"
	testModel         = "model-a"
	testSecretResult  = `{"token":"secret"}`
	testRedacted      = "[REDACTED]"
)

type auditStoreFake struct {
	started          model.MCPAuditCall
	finished         model.MCPAuditCall
	startedCalls     []model.MCPAuditCall
	finishedCalls    []model.MCPAuditCall
	startErr         error
	finishErr        error
	finishContextErr error
	startDeadline    time.Time
}

func (f *auditStoreFake) Start(ctx context.Context, call model.MCPAuditCall) (int64, error) {
	f.started = call
	f.startedCalls = append(f.startedCalls, call)
	f.startDeadline, _ = ctx.Deadline()
	return 42, f.startErr
}

func (f *auditStoreFake) Finish(ctx context.Context, id int64, status, result, errorText string, finishedAt time.Time) error {
	f.finishContextErr = ctx.Err()
	f.finished = model.MCPAuditCall{
		ID: id, Status: status, Result: result, Error: errorText, FinishedAt: &finishedAt,
	}
	f.finishedCalls = append(f.finishedCalls, f.finished)
	return f.finishErr
}

func TestRecorderCallStoresAllowedDecisionOnce(t *testing.T) {
	store := &auditStoreFake{}
	recorder := NewRecorder(store, nil, fixedAuditNow)
	ctx := WithMetadata(t.Context(), Metadata{
		ActorUserID: 7, RequestedTool: "mcp__read", SafeArguments: `{"id":1}`,
		Intent: "Read activity", GuardVerdict: model.MCPAuditGuardAllowed,
		GuardReason: "Matches request",
	})

	_, err := recorder.Call(ctx, "mcp__read", `{"id":1}`, func(context.Context) (string, error) {
		return "ok", nil
	})
	if err != nil || len(store.startedCalls) != 1 || len(store.finishedCalls) != 1 {
		t.Fatalf("start=%d finish=%d err=%v", len(store.startedCalls), len(store.finishedCalls), err)
	}
	if got := store.startedCalls[0]; got.Intent != "Read activity" ||
		got.GuardVerdict != model.MCPAuditGuardAllowed || got.GuardReason != "Matches request" {
		t.Fatalf("started audit = %+v", got)
	}
}

func TestRecorderBlockStoresOneTerminalRow(t *testing.T) {
	store := &auditStoreFake{}
	recorder := NewRecorder(store, nil, fixedAuditNow)
	ctx := WithMetadata(t.Context(), Metadata{
		ActorUserID: 7,
		Sanitize: func(value string) string {
			if value == "Read secret" || value == "Tool mutates secret data" {
				return testRedacted
			}
			return value
		},
	})

	recorder.Block(ctx, "mcp__write", `{"id":1}`, "Read secret", model.MCPAuditGuardDenied, "Tool mutates secret data")
	if len(store.startedCalls) != 1 || len(store.finishedCalls) != 0 {
		t.Fatalf("start=%d finish=%d", len(store.startedCalls), len(store.finishedCalls))
	}
	got := store.startedCalls[0]
	if got.Status != model.MCPAuditStatusBlocked || got.GuardVerdict != model.MCPAuditGuardDenied ||
		got.FinishedAt == nil || got.Intent != testRedacted || got.GuardReason != testRedacted {
		t.Fatalf("blocked audit = %+v", got)
	}
}

func TestRecorderBlockIgnoresStartFailure(t *testing.T) {
	store := &auditStoreFake{startErr: errors.New("database unavailable")}
	recorder := NewRecorder(store, slog.New(slog.NewTextHandler(io.Discard, nil)), fixedAuditNow)
	ctx := WithMetadata(t.Context(), Metadata{ActorUserID: 7})

	recorder.Block(ctx, "mcp__write", `{"id":1}`, "Write activity", model.MCPAuditGuardDenied, "Tool mutates data")
	if len(store.startedCalls) != 1 || len(store.finishedCalls) != 0 {
		t.Fatalf("start=%d finish=%d", len(store.startedCalls), len(store.finishedCalls))
	}
}

func TestRecorderCallRunsOnceWhenPersistenceFails(t *testing.T) {
	for _, store := range []*auditStoreFake{
		{startErr: errors.New("start unavailable")},
		{finishErr: errors.New("finish unavailable")},
	} {
		t.Run("persistence failure", func(t *testing.T) {
			recorder := NewRecorder(store, slog.New(slog.NewTextHandler(io.Discard, nil)), fixedAuditNow)
			ctx := WithMetadata(t.Context(), Metadata{ActorUserID: 7})
			calls := 0
			out, err := recorder.Call(ctx, "mcp__read", `{}`, func(context.Context) (string, error) {
				calls++
				return "ok", nil
			})
			if out != "ok" || err != nil || calls != 1 {
				t.Fatalf("out=%q err=%v calls=%d", out, err, calls)
			}
		})
	}
}

func fixedAuditNow() time.Time {
	return time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
}

func TestRecorderStoresSafeSuccessfulCall(t *testing.T) {
	store := &auditStoreFake{}
	started := time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC)
	finished := started.Add(time.Second)
	times := []time.Time{started, finished}
	recorder := NewRecorder(store, slog.New(slog.NewTextHandler(io.Discard, nil)), func() time.Time {
		next := times[0]
		times = times[1:]
		return next
	})
	ctx := WithMetadata(t.Context(), Metadata{
		ActorUserID: 5, ActorUsername: testActorUsername, ConversationID: testConversation,
		Source: model.MCPAuditSourceChat, Model: testModel, ToolCallID: "call-1",
		RequestedTool: "server__login", SafeArguments: `{"password":"TOKEN"}`,
		Sanitize: func(s string) string {
			if s == testSecretResult {
				return `{"token":"[REDACTED]"}`
			}
			return s
		},
	})

	out, err := recorder.Call(ctx, "server__login", `{"password":"raw"}`, func(context.Context) (string, error) {
		return testSecretResult, nil
	})
	if err != nil || out != testSecretResult {
		t.Fatalf("Call = %q, %v", out, err)
	}
	if store.started.Arguments != `{"password":"TOKEN"}` || store.started.ToolName != "server__login" ||
		store.started.Status != model.MCPAuditStatusRunning || !store.started.StartedAt.Equal(started) {
		t.Fatalf("started audit = %+v", store.started)
	}
	if store.finished.Status != model.MCPAuditStatusSucceeded ||
		store.finished.Result != `{"token":"[REDACTED]"}` || store.finished.Error != "" ||
		store.finished.FinishedAt == nil || !store.finished.FinishedAt.Equal(finished) {
		t.Fatalf("finished audit = %+v", store.finished)
	}
}

func TestRecorderStoresSafeFailureAndFailsOpen(t *testing.T) {
	callErr := errors.New("credential secret rejected")
	t.Run("tool failure", func(t *testing.T) {
		store := &auditStoreFake{}
		recorder := NewRecorder(store, slog.Default(), time.Now)
		ctx := WithMetadata(t.Context(), Metadata{
			ActorUserID: 5, ActorUsername: testActorUsername, ConversationID: testConversation,
			Source: model.MCPAuditSourceChat, Model: testModel,
			Sanitize: func(string) string { return "credential [REDACTED] rejected" },
		})
		_, err := recorder.Call(ctx, "server__login", `{}`, func(context.Context) (string, error) {
			return "", callErr
		})
		if !errors.Is(err, callErr) || store.finished.Status != model.MCPAuditStatusFailed ||
			store.finished.Error != "credential [REDACTED] rejected" {
			t.Fatalf("err=%v audit=%+v", err, store.finished)
		}
	})

	t.Run("cancelled call still records terminal status", func(t *testing.T) {
		store := &auditStoreFake{}
		recorder := NewRecorder(store, slog.Default(), time.Now)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		ctx = WithMetadata(ctx, Metadata{
			ActorUserID: 5, ActorUsername: testActorUsername, ConversationID: testConversation,
			Source: model.MCPAuditSourceChat, Model: testModel,
		})
		_, err := recorder.Call(ctx, "server__activities", `{}`, func(context.Context) (string, error) {
			return "", context.Canceled
		})
		if !errors.Is(err, context.Canceled) || store.finished.Status != model.MCPAuditStatusFailed ||
			store.finishContextErr != nil {
			t.Fatalf("err=%v audit=%+v finish context=%v", err, store.finished, store.finishContextErr)
		}
	})

	t.Run("start persistence failure", func(t *testing.T) {
		store := &auditStoreFake{startErr: errors.New("database unavailable")}
		recorder := NewRecorder(store, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now)
		ctx := WithMetadata(t.Context(), Metadata{
			ActorUserID: 5, ActorUsername: testActorUsername, ConversationID: testConversation,
			Source: model.MCPAuditSourceChat, Model: testModel,
		})
		called := false
		out, err := recorder.Call(ctx, "server__activities", `{}`, func(context.Context) (string, error) {
			called = true
			return "ok", nil
		})
		if err != nil || out != "ok" || !called {
			t.Fatalf("Call = %q, %v called=%v; want fail-open execution", out, err, called)
		}
		if store.startDeadline.IsZero() || time.Until(store.startDeadline) > 3*time.Second {
			t.Fatalf("audit start deadline = %v; want short bounded write", store.startDeadline)
		}
	})

	t.Run("finish persistence failure", func(t *testing.T) {
		store := &auditStoreFake{finishErr: errors.New("database unavailable")}
		recorder := NewRecorder(store, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now)
		ctx := WithMetadata(t.Context(), Metadata{
			ActorUserID: 5, ActorUsername: testActorUsername, ConversationID: testConversation,
			Source: model.MCPAuditSourceChat, Model: testModel,
		})
		out, err := recorder.Call(ctx, "server__activities", `{}`, func(context.Context) (string, error) {
			return "ok", nil
		})
		if err != nil || out != "ok" {
			t.Fatalf("Call = %q, %v; want fail-open result", out, err)
		}
	})
}
