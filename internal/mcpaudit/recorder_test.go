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
)

type auditStoreFake struct {
	started          model.MCPAuditCall
	finished         model.MCPAuditCall
	startErr         error
	finishErr        error
	finishContextErr error
	startDeadline    time.Time
}

func (f *auditStoreFake) Start(ctx context.Context, call model.MCPAuditCall) (int64, error) {
	f.started = call
	f.startDeadline, _ = ctx.Deadline()
	return 42, f.startErr
}

func (f *auditStoreFake) Finish(ctx context.Context, id int64, status, result, errorText string, finishedAt time.Time) error {
	f.finishContextErr = ctx.Err()
	f.finished = model.MCPAuditCall{
		ID: id, Status: status, Result: result, Error: errorText, FinishedAt: &finishedAt,
	}
	return f.finishErr
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
	ctx := WithMetadata(context.Background(), Metadata{
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
		ctx := WithMetadata(context.Background(), Metadata{
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
		ctx, cancel := context.WithCancel(context.Background())
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
		ctx := WithMetadata(context.Background(), Metadata{
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
		ctx := WithMetadata(context.Background(), Metadata{
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
