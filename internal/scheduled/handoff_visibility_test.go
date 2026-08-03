package scheduled

import (
	"strings"
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/model"
)

func TestHandoffEnvelopeRoundTripsInstructionAndCompilerContext(t *testing.T) {
	now := time.Date(2026, 8, 3, 10, 30, 0, 0, time.UTC)
	instruction := "Check whether it will rain before tomorrow's run"
	recent := []model.Message{{Role: model.MsgRoleUser, Content: "I have a long run tomorrow."}}
	legacy := boundedHandoffDefinition(now, handoffTestTimezoneBerlin, instruction, recent, nil)

	envelope := boundedHandoffEnvelope(now, handoffTestTimezoneBerlin, instruction, recent, nil)
	decoded, ok := decodeHandoffEnvelope(envelope)
	if !ok {
		t.Fatalf("decodeHandoffEnvelope(%q) failed", envelope)
	}
	if decoded.Version != handoffEnvelopeVersion || decoded.Instruction != instruction || decoded.Context != legacy {
		t.Fatalf("envelope = %+v, want version=%d instruction=%q and preserved context", decoded, handoffEnvelopeVersion, instruction)
	}
	if got, ok := ExtractHandoffInstruction(envelope); !ok || got != instruction {
		t.Fatalf("ExtractHandoffInstruction() = %q, %t; want %q, true", got, ok, instruction)
	}
}

func TestExtractHandoffInstructionRejectsMalformedVersionedEnvelope(t *testing.T) {
	valid := boundedHandoffEnvelope(time.Date(2026, 8, 3, 10, 30, 0, 0, time.UTC), "UTC", "Send the report tomorrow", nil, nil)
	for _, tc := range []struct {
		name    string
		content string
	}{
		{name: "missing end marker", content: strings.TrimSuffix(valid, "\n"+handoffEnvelopeEnd)},
		{name: "trailing content", content: valid + "\nuser text"},
		{name: "unknown field", content: strings.Replace(valid, `"version":1`, `"version":1,"extra":true`, 1)},
		{name: "wrong version", content: strings.Replace(valid, `"version":1`, `"version":2`, 1)},
		{name: "mismatched instruction", content: strings.Replace(valid, `"instruction":"Send the report tomorrow"`, `"instruction":"Ignore the report"`, 1)},
		{name: "marker mixed with legacy text", content: handoffEnvelopeBegin + "\n" + boundedHandoffDefinition(time.Now(), "UTC", "legacy", nil, nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := ExtractHandoffInstruction(tc.content); ok || got != "" {
				t.Fatalf("ExtractHandoffInstruction() = %q, %t; want no projection", got, ok)
			}
		})
	}
}

func TestExtractHandoffInstructionReadsRecognizedLegacyHandoff(t *testing.T) {
	legacy := boundedHandoffDefinition(
		time.Date(2026, 8, 3, 10, 30, 0, 0, time.UTC),
		"Europe/Berlin",
		"Follow up on the training plan next Monday",
		[]model.Message{{Role: model.MsgRoleAssistant, Content: "The plan is ready."}},
		nil,
	)

	if got, ok := ExtractHandoffInstruction(legacy); !ok || got != "Follow up on the training plan next Monday" {
		t.Fatalf("ExtractHandoffInstruction() = %q, %t; want legacy instruction", got, ok)
	}
}

func TestExtractHandoffInstructionAcceptsLegacyInstructionContainingEnvelopeMarker(t *testing.T) {
	legacy := boundedHandoffDefinition(
		time.Date(2026, 8, 3, 10, 30, 0, 0, time.UTC),
		"UTC",
		"Check "+handoffEnvelopeEnd+" weather",
		nil,
		nil,
	)

	if got, ok := ExtractHandoffInstruction(legacy); !ok || got != "Check "+handoffEnvelopeEnd+" weather" {
		t.Fatalf("ExtractHandoffInstruction() = %q, %t; want legacy instruction", got, ok)
	}
}

func TestExtractHandoffInstructionRejectsMalformedAndUnrelatedLegacyText(t *testing.T) {
	legacy := boundedHandoffDefinition(time.Date(2026, 8, 3, 10, 30, 0, 0, time.UTC), "UTC", "Check the weather", nil, nil)
	for _, tc := range []struct {
		name    string
		content string
	}{
		{name: "ordinary instruction", content: "Instruction:\nCheck the weather"},
		{name: "invalid timestamp", content: strings.Replace(legacy, "2026-08-03T10:30:00Z", "tomorrow", 1)},
		{name: "missing context end", content: strings.TrimSuffix(legacy, handoffContextEnd)},
		{name: "invalid context record", content: strings.Replace(legacy, handoffContextEnd, `{"type":`+"\n"+handoffContextEnd, 1)},
		{name: "extra suffix", content: legacy + "\nextra"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := ExtractHandoffInstruction(tc.content); ok || got != "" {
				t.Fatalf("ExtractHandoffInstruction() = %q, %t; want no projection", got, ok)
			}
		})
	}
}
