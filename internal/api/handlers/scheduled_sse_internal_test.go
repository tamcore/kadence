package handlers

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"
)

type failingScheduledWriter struct {
	mu        sync.Mutex
	header    http.Header
	writes    int
	flushes   int
	failWrite bool
	failFlush bool
}

func (w *failingScheduledWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (*failingScheduledWriter) WriteHeader(int) {}

func (w *failingScheduledWriter) Write(payload []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes++
	if w.failWrite {
		return 0, errors.New("write disconnected")
	}
	return len(payload), nil
}

func (w *failingScheduledWriter) FlushError() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushes++
	if w.failFlush {
		return errors.New("flush disconnected")
	}
	return nil
}

func (w *failingScheduledWriter) counts() (int, int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writes, w.flushes
}

func TestScheduledSSEWriteAndFlushFailuresCancelAndStop(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failWrite bool
		failFlush bool
	}{
		{name: "write", failWrite: true},
		{name: "flush", failFlush: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			writer := &failingScheduledWriter{failWrite: tc.failWrite, failFlush: tc.failFlush}
			stream, stop := beginScheduledStream(writer, cancel)
			if err := stream.event(map[string]string{"type": "text"}); err == nil {
				t.Fatal("event error = nil")
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
				t.Fatal("stream failure did not cancel work")
			}
			stop()
			beforeWrites, beforeFlushes := writer.counts()
			time.Sleep(2 * time.Millisecond)
			afterWrites, afterFlushes := writer.counts()
			if beforeWrites != afterWrites || beforeFlushes != afterFlushes {
				t.Fatalf("post-return writes/flushes: before=%d/%d after=%d/%d", beforeWrites, beforeFlushes, afterWrites, afterFlushes)
			}
		})
	}
}

func TestScheduledSSEKeepaliveFailureCancelsAndStops(t *testing.T) {
	original := sseKeepaliveInterval
	sseKeepaliveInterval = time.Millisecond
	defer func() { sseKeepaliveInterval = original }()

	ctx, cancel := context.WithCancel(context.Background())
	writer := &failingScheduledWriter{failWrite: true}
	_, stop := beginScheduledStream(writer, cancel)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("keepalive failure did not cancel work")
	}
	stop()
	beforeWrites, beforeFlushes := writer.counts()
	time.Sleep(3 * time.Millisecond)
	afterWrites, afterFlushes := writer.counts()
	if beforeWrites != afterWrites || beforeFlushes != afterFlushes {
		t.Fatalf("keepalive continued after stop: before=%d/%d after=%d/%d", beforeWrites, beforeFlushes, afterWrites, afterFlushes)
	}
}
