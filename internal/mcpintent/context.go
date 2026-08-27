package mcpintent

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"

	"github.com/tamcore/kadence/internal/provider"
)

type ScheduledContext struct {
	TaskKind        string          `json:"taskKind"`
	CanonicalPrompt string          `json:"canonicalPrompt"`
	StopCondition   string          `json:"stopCondition"`
	MonitoringState json.RawMessage `json:"monitoringState,omitempty"`
}

type TrustedContext struct {
	Request   string             `json:"request,omitempty"`
	History   []provider.Message `json:"history,omitempty"`
	Scheduled *ScheduledContext  `json:"scheduled,omitempty"`
}

type trustedContextKey struct{}
type inheritedIntentKey struct{}

func WithTrustedContext(ctx context.Context, trusted TrustedContext) context.Context {
	return context.WithValue(ctx, trustedContextKey{}, cloneTrustedContext(trusted))
}

func TrustedContextFrom(ctx context.Context) (TrustedContext, bool) {
	trusted, ok := ctx.Value(trustedContextKey{}).(TrustedContext)
	if !ok {
		return TrustedContext{}, false
	}
	return cloneTrustedContext(trusted), true
}

func WithInheritedIntent(ctx context.Context, intent string) context.Context {
	return context.WithValue(ctx, inheritedIntentKey{}, intent)
}

func InheritedIntentFrom(ctx context.Context) (string, bool) {
	intent, ok := ctx.Value(inheritedIntentKey{}).(string)
	return intent, ok
}

func cloneTrustedContext(in TrustedContext) TrustedContext {
	out := in
	out.History = make([]provider.Message, len(in.History))
	for i, message := range in.History {
		out.History[i] = cloneMessage(message)
	}
	if in.Scheduled != nil {
		scheduled := *in.Scheduled
		scheduled.MonitoringState = append(json.RawMessage(nil), in.Scheduled.MonitoringState...)
		out.Scheduled = &scheduled
	}
	return out
}

func cloneMessage(in provider.Message) provider.Message {
	out := in
	out.Images = make([]provider.ImageContent, len(in.Images))
	for i, image := range in.Images {
		out.Images[i] = image
		out.Images[i].Data = bytes.Clone(image.Data)
	}
	out.ToolCalls = slices.Clone(in.ToolCalls)
	return out
}
