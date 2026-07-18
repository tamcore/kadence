package chat_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tamcore/kadence/internal/chat"
	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
)

const (
	testClassifierModel = "classifier"
	testDomainName      = "TestCoach"
	testAllowedTopics   = "training"
	testRefusalMessage  = "off you go"
	testHistoryWindow   = 6
)

// verdictProvider returns a canned classifier verdict and records the request.
type verdictProvider struct {
	verdict string
	err     error
	gotReq  provider.ChatRequest
}

func (p *verdictProvider) StreamChat(_ context.Context, req provider.ChatRequest, _ provider.TokenFunc) (string, error) {
	p.gotReq = req
	return p.verdict, p.err
}

func newGuardrail(v *verdictProvider) *chat.Guardrail {
	return chat.NewGuardrail(v, chat.GuardrailConfig{
		Model: testClassifierModel, DomainName: testDomainName, AllowedTopics: testAllowedTopics,
		RefusalMessage: testRefusalMessage, HistoryWindow: testHistoryWindow,
	})
}

func TestGuardrailOffTopic(t *testing.T) {
	v := &verdictProvider{verdict: "OFF_TOPIC"}
	off, err := newGuardrail(v).Classify(context.Background(), []provider.Message{{Role: model.MsgRoleUser, Content: "stock tips?"}})
	if err != nil || !off {
		t.Fatalf("off=%v err=%v, want true/nil", off, err)
	}
	if v.gotReq.Messages[0].Role != model.MsgRoleSystem || !strings.Contains(v.gotReq.Messages[0].Content, "TestCoach") {
		t.Fatalf("classifier system prompt wrong: %+v", v.gotReq.Messages)
	}
}

func TestGuardrailOnTopic(t *testing.T) {
	off, err := newGuardrail(&verdictProvider{verdict: "ON_TOPIC"}).Classify(context.Background(),
		[]provider.Message{{Role: model.MsgRoleUser, Content: "how many rest days?"}})
	if err != nil || off {
		t.Fatalf("off=%v err=%v, want false/nil", off, err)
	}
}

func TestGuardrailErrorAndUnexpectedVerdict(t *testing.T) {
	_, err := newGuardrail(&verdictProvider{err: errors.New("boom")}).Classify(context.Background(),
		[]provider.Message{{Role: model.MsgRoleUser, Content: "x"}})
	if err == nil {
		t.Fatal("expected error from classifier failure")
	}
	_, err = newGuardrail(&verdictProvider{verdict: "maybe?"}).Classify(context.Background(),
		[]provider.Message{{Role: model.MsgRoleUser, Content: "x"}})
	if err == nil {
		t.Fatal("expected error on unexpected verdict")
	}
}

func TestRecentWindowLimit(t *testing.T) {
	msgs := make([]provider.Message, 0, 10)
	for i := range 10 {
		msgs = append(msgs, provider.Message{Role: model.MsgRoleUser, Content: string(rune('a' + i))})
	}
	v := &verdictProvider{verdict: "ON_TOPIC"}
	_, _ = newGuardrail(v).Classify(context.Background(), msgs)
	expectedMessages := 1 + testHistoryWindow // 1 system + testHistoryWindow recent
	if len(v.gotReq.Messages) != expectedMessages {
		t.Fatalf("classifier saw %d messages, want %d", len(v.gotReq.Messages), expectedMessages)
	}
}
