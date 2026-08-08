package mcpintent

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

const ArgumentName = "_kadence_intent"
const MaxIntentBytes = 512

type Arguments struct {
	Intent   string
	SafeJSON string
}

func ExtractArguments(raw string) (Arguments, error) {
	values, err := parseObject(raw)
	if err != nil {
		return Arguments{}, errors.New("intent arguments must be one JSON object")
	}

	encoded, ok := values[ArgumentName]
	if !ok {
		return Arguments{}, errors.New("intent is required")
	}

	var intent string
	if err := json.Unmarshal(encoded, &intent); err != nil {
		return Arguments{}, errors.New("intent must be a string")
	}
	intent = strings.TrimSpace(intent)
	if intent == "" || len(intent) > MaxIntentBytes || !utf8.ValidString(intent) {
		return Arguments{}, errors.New("intent must be non-empty UTF-8 text of at most 512 bytes")
	}

	delete(values, ArgumentName)
	safe, err := json.Marshal(values)
	if err != nil {
		return Arguments{}, errors.New("could not clean intent arguments")
	}
	return Arguments{Intent: intent, SafeJSON: string(safe)}, nil
}

func StripArguments(raw string) string {
	values, err := parseObject(raw)
	if err != nil {
		return raw
	}
	delete(values, ArgumentName)
	safe, err := json.Marshal(values)
	if err != nil {
		return raw
	}
	return string(safe)
}

func parseObject(raw string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	var values map[string]json.RawMessage
	if err := decoder.Decode(&values); err != nil || values == nil {
		return nil, errors.New("not an object")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing JSON")
	}
	return values, nil
}
