package mcpintent

import (
	"testing"

	"github.com/tamcore/kadence/internal/provider"
)

const (
	testCheckWeather = "Check weather"
	testRunHere      = "I run here"
)

func TestTrustedContextCopiesValues(t *testing.T) {
	in := TrustedContext{
		Request:   testCheckWeather,
		History:   []provider.Message{{Role: classifierUserRole, Content: testRunHere}},
		Scheduled: &ScheduledContext{MonitoringState: []byte(`{"status":"active"}`)},
	}
	ctx := WithTrustedContext(t.Context(), in)
	in.History[0].Content = "mutated"
	in.Scheduled.MonitoringState[0] = 'x'

	got, ok := TrustedContextFrom(ctx)
	if !ok || got.History[0].Content != testRunHere || string(got.Scheduled.MonitoringState) != `{"status":"active"}` {
		t.Fatalf("got=%+v", got)
	}
	got.History[0].Content = "mutated result"
	got.Scheduled.MonitoringState[0] = 'x'

	again, ok := TrustedContextFrom(ctx)
	if !ok || again.History[0].Content != testRunHere || string(again.Scheduled.MonitoringState) != `{"status":"active"}` {
		t.Fatalf("again=%+v", again)
	}
}

func TestInheritedIntentDoesNotBecomeAuthority(t *testing.T) {
	ctx := WithInheritedIntent(t.Context(), "Analyze activity")
	if got, ok := InheritedIntentFrom(ctx); !ok || got != "Analyze activity" {
		t.Fatalf("got=%q", got)
	}
	if _, ok := TrustedContextFrom(ctx); ok {
		t.Fatal("intent became trusted context")
	}
}
