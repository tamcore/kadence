package chat

import (
	"context"
	"errors"

	"github.com/tamcore/kadence/internal/confirm"
)

// ErrNoLiveTurn is returned when a tool asks its caller to confirm but there is
// no stream to ask on — a scheduled or otherwise unattended turn. It is a
// refusal, and a deliberate one: the alternative is a background job waiting on
// a person who is not there, or a question surfacing in an unrelated turn.
var ErrNoLiveTurn = errors.New("chat: this run cannot ask you to confirm, so the action was refused")

// ConfirmBroker is the subset of confirm.Broker the bridge uses.
type ConfirmBroker interface {
	NewRequest(userID int64, tool, prompt string) (confirm.Request, error)
	Await(ctx context.Context, id string) (bool, error)
}

type confirmSinkKey struct{}

// WithConfirmSink marks a context as belonging to a turn a user is watching,
// so a mid-call question raised under it can be shown to them.
//
// The sink travels on the context rather than on the service because one
// service serves every turn at once: anything user-keyed could route one
// turn's question into another turn's stream.
func WithConfirmSink(ctx context.Context, sink EventSink) context.Context {
	return context.WithValue(ctx, confirmSinkKey{}, sink)
}

func confirmSinkFrom(ctx context.Context) (EventSink, bool) {
	sink, ok := ctx.Value(confirmSinkKey{}).(EventSink)
	return sink, ok && sink != nil
}

// ConfirmBridge turns a mid-call confirmation request into a question on the
// live chat stream and back into an answer. It satisfies mcp.ConfirmSource.
type ConfirmBridge struct {
	broker ConfirmBroker
}

// NewConfirmBridge returns a bridge over broker.
func NewConfirmBridge(broker ConfirmBroker) *ConfirmBridge {
	return &ConfirmBridge{broker: broker}
}

// Confirm asks the user watching this turn and blocks until they answer, the
// wait elapses, or the turn ends. Returning false with a nil error is a plain
// decline; a non-nil error means nobody could be asked.
func (b *ConfirmBridge) Confirm(ctx context.Context, userID int64, tool, prompt string) (bool, error) {
	sink, ok := confirmSinkFrom(ctx)
	if !ok {
		// Refuse before registering anything: an unasked question must leave
		// no trace for another turn to stumble into.
		return false, ErrNoLiveTurn
	}

	req, err := b.broker.NewRequest(userID, tool, prompt)
	if err != nil {
		return false, err
	}

	_ = sink.Send(ChatEvent{Type: EventConfirm, RequestID: req.ID, Tool: tool, Message: prompt})
	_ = sink.Flush()

	allowed, err := b.broker.Await(ctx, req.ID)
	if err != nil {
		// Declined, timed out, cancelled: to the caller these are one answer —
		// no — and none of them is a fault worth propagating.
		return false, nil
	}
	return allowed, nil
}
