package mcpintent

import (
	"encoding/json"
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

func TestJSONDecoderAcceptsUnpairedSurrogate(t *testing.T) {
	var decoded string
	if err := json.Unmarshal([]byte(`"\ud800"`), &decoded); err != nil {
		t.Fatalf("decoder rejected unpaired surrogate: %v", err)
	}
	if decoded != "\uFFFD" {
		t.Fatalf("decoded=%q", decoded)
	}
}

func TestExtractArgumentsRejectsInvalidUTF8(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
	}{
		{name: "source bytes", raw: `{"_kadence_intent":"` + string([]byte{0xff}) + `"}`},
		{name: "unpaired surrogate", raw: `{"_kadence_intent":"\ud800"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ExtractArguments(test.raw); err == nil {
				t.Fatalf("accepted invalid UTF-8 %q", test.raw)
			}
		})
	}
}

func TestExtractArgumentsAcceptsUnicodeSurrogatePair(t *testing.T) {
	got, err := ExtractArguments(`{"_kadence_intent":"Run \ud83d\ude80"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Intent != "Run 🚀" {
		t.Fatalf("intent=%q", got.Intent)
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
