// Package confirm implements an in-memory broker that carries one yes/no
// question from a tool call to the user who triggered it, and carries their
// answer back to the goroutine that is blocked on it.
//
// It exists because a destructive MCP tool asks its caller to confirm, mid
// call, over the same connection. The request must reach a browser and the
// answer must return before the transport gives up on the call.
package confirm

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

// confirmTTL bounds how long a question waits for an answer.
//
// The ceiling is not a product choice. mcp-go's streamable-http transport
// wraps every incoming server request in a 30-second context before invoking
// the handler, and answers a lapsed one with REQUEST_INTERRUPTED. An answer
// arriving after that is dropped on the floor, so the wait must end first and
// leave room for the reply to travel.
const confirmTTL = 25 * time.Second

// idRandomBytes is the entropy per request id. The id travels to the browser
// and comes back as the only thing naming the pending question, so guessing
// one must be infeasible.
const idRandomBytes = 32

// Sentinel errors. Callers should use errors.Is.
var (
	// ErrDeclined means the user said no, or was logged out while being asked.
	// Both are refusals; neither is a fault.
	ErrDeclined = errors.New("confirm: the user declined")
	// ErrTimedOut means nobody answered before the TTL elapsed.
	ErrTimedOut = errors.New("confirm: timed out waiting for an answer")
	// ErrUnknownRequest covers an id that never existed, already expired, was
	// already answered, was abandoned, or belongs to another user. One error
	// for all of them, so a prober learns nothing from the difference.
	ErrUnknownRequest = errors.New("confirm: unknown request")
)

// Request is one pending question, as handed to whoever will show it.
type Request struct {
	ID     string
	UserID int64
	Tool   string
	Prompt string
}

// pending is the broker's own view of a question. The answer is recorded in
// the struct as well as delivered on ch, because Submit may land before Await
// is entered and a decision must not depend on that ordering. ch has capacity
// one and exactly one writer, so Submit never blocks; closing it instead of
// writing signals a purge.
type pending struct {
	userID    int64
	expiresAt time.Time
	answered  bool
	allowed   bool
	// purged marks a question abandoned because its owner logged out. The
	// entry outlives the purge so a waiter that arrives afterwards still
	// learns it was refused rather than that the id was never real.
	purged bool
	ch     chan bool
}

// Broker holds the questions currently awaiting an answer.
type Broker struct {
	mu       sync.Mutex
	requests map[string]*pending
	now      func() time.Time
}

// NewBroker returns a broker on the wall clock.
func NewBroker() *Broker { return NewBrokerWithClock(time.Now) }

// NewBrokerWithClock returns a broker reading time from now, for tests that
// need expiry without sleeping.
func NewBrokerWithClock(now func() time.Time) *Broker {
	return &Broker{requests: map[string]*pending{}, now: now}
}

// NewRequest registers a question for userID and returns it. The caller shows
// the Request to that user and then blocks in Await on its ID.
func (b *Broker) NewRequest(userID int64, tool, prompt string) (Request, error) {
	raw := make([]byte, idRandomBytes)
	if _, err := rand.Read(raw); err != nil {
		return Request{}, err
	}
	id := base64.RawURLEncoding.EncodeToString(raw)

	b.mu.Lock()
	defer b.mu.Unlock()
	b.sweepLocked()
	b.requests[id] = &pending{
		userID:    userID,
		expiresAt: b.now().Add(confirmTTL),
		ch:        make(chan bool, 1),
	}
	return Request{ID: id, UserID: userID, Tool: tool, Prompt: prompt}, nil
}

// Await blocks until the question is answered, the TTL elapses, the user is
// purged, or ctx ends. A true return is the only outcome that permits the
// call; every other one is a refusal carrying its reason.
func (b *Broker) Await(ctx context.Context, id string) (bool, error) {
	b.mu.Lock()
	// Expiry is read before the sweep: this caller owns the wait, so the
	// honest answer is that its question timed out, not that it never existed.
	if p, ok := b.requests[id]; ok && !p.expiresAt.After(b.now()) {
		delete(b.requests, id)
		b.mu.Unlock()
		return false, ErrTimedOut
	}
	b.sweepLocked()
	p, ok := b.requests[id]
	if ok && p.purged {
		delete(b.requests, id)
		b.mu.Unlock()
		return false, ErrDeclined
	}
	if ok && p.answered {
		// Answered before anyone waited. Consume it here.
		delete(b.requests, id)
		b.mu.Unlock()
		return resolve(p.allowed, true)
	}
	b.mu.Unlock()
	if !ok {
		return false, ErrUnknownRequest
	}

	timer := time.NewTimer(time.Until(p.expiresAt))
	defer timer.Stop()

	select {
	case allowed, live := <-p.ch:
		b.forget(id, p)
		return resolve(allowed, live)
	case <-timer.C:
		return b.giveUp(id, p, ErrTimedOut)
	case <-ctx.Done():
		return b.giveUp(id, p, ctx.Err())
	}
}

// resolve turns what came off the channel into an outcome. A closed channel
// is a purge, which reads as a decline: the user is gone, so the answer is no.
func resolve(allowed, live bool) (bool, error) {
	if !live || !allowed {
		return false, ErrDeclined
	}
	return true, nil
}

// giveUp abandons a wait. An answer that landed in the same instant still
// wins — it is a real decision, and discarding it in favour of the deadline
// would refuse a call the user allowed.
func (b *Broker) giveUp(id string, p *pending, cause error) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	select {
	case allowed, live := <-p.ch:
		b.forgetLocked(id, p)
		return resolve(allowed, live)
	default:
	}
	b.forgetLocked(id, p)
	return false, cause
}

// forget drops one entry. forgetLocked is the same under a held lock.
func (b *Broker) forget(id string, p *pending) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.forgetLocked(id, p)
}

// forgetLocked removes only our own entry: the id may already have been
// recycled by a later request.
func (b *Broker) forgetLocked(id string, p *pending) {
	if cur, ok := b.requests[id]; ok && cur == p {
		delete(b.requests, id)
	}
}

// Submit records one user's answer. Removing the entry and delivering the
// answer happen under the same lock, so an answer can never be accepted for a
// wait that has already been abandoned.
func (b *Broker) Submit(userID int64, id string, allowed bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sweepLocked()

	p, ok := b.requests[id]
	if !ok || p.userID != userID || p.answered || p.purged {
		return ErrUnknownRequest
	}
	p.answered, p.allowed = true, allowed
	p.ch <- allowed // capacity one, single writer: never blocks
	return nil
}

// PurgeUser abandons every question owned by userID, releasing their waiters
// at once. A logout must not leave a turn hanging for the rest of the TTL.
// The entries stay until they expire, so a waiter entering after the purge
// still reads a refusal.
func (b *Broker) PurgeUser(userID int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, p := range b.requests {
		if p.userID == userID && !p.purged {
			p.purged = true
			close(p.ch)
		}
	}
}

// sweepLocked drops questions nobody answered in time. Their waiters, if any,
// are released by their own timer.
func (b *Broker) sweepLocked() {
	now := b.now()
	for id, p := range b.requests {
		if !p.expiresAt.After(now) {
			delete(b.requests, id)
		}
	}
}
