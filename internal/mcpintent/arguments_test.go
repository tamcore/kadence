package mcpintent

import (
	"strings"
	"testing"
)

func TestExtractArgumentsRemovesIntentAndPreservesPlaceholder(t *testing.T) {
	got, err := ExtractArguments(`{"city":"Bratislava","token":"KADENCE_SECRET_1","_kadence_intent":"  Check weather for today's run  "}`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Intent != "Check weather for today's run" {
		t.Fatalf("intent=%q", got.Intent)
	}
	if strings.Contains(got.SafeJSON, ArgumentName) || !strings.Contains(got.SafeJSON, "KADENCE_SECRET_1") {
		t.Fatalf("safe=%s", got.SafeJSON)
	}
}

func TestExtractArgumentsRejectsInvalidIntent(t *testing.T) {
	for _, raw := range []string{
		`{}`,
		`{"_kadence_intent":""}`,
		`{"_kadence_intent":12}`,
		`[]`,
		`{"_kadence_intent":"ok"} trailing`,
		`{"_kadence_intent":"` + strings.Repeat("x", MaxIntentBytes+1) + `"}`,
	} {
		if _, err := ExtractArguments(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}

func TestExtractArgumentsAcceptsIntentAtByteLimit(t *testing.T) {
	intent := strings.Repeat("x", MaxIntentBytes)
	got, err := ExtractArguments(`{"_kadence_intent":"` + intent + `"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Intent != intent {
		t.Fatalf("intent length=%d", len(got.Intent))
	}
}

func TestStripArgumentsRemovesOnlyIntent(t *testing.T) {
	got := StripArguments(`{"city":"Bratislava","token":"KADENCE_SECRET_1","_kadence_intent":"weather"}`)
	if strings.Contains(got, ArgumentName) || !strings.Contains(got, `"city":"Bratislava"`) || !strings.Contains(got, "KADENCE_SECRET_1") {
		t.Fatalf("stripped=%s", got)
	}
}

func TestStripArgumentsReturnsOriginalOnInvalidObject(t *testing.T) {
	for _, raw := range []string{`[]`, `{"_kadence_intent":"weather"} trailing`, `{not-json}`} {
		if got := StripArguments(raw); got != raw {
			t.Fatalf("got=%q want original=%q", got, raw)
		}
	}
}
