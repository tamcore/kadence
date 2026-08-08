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
	if !validUnicodeEscapes(encoded) {
		return Arguments{}, errors.New("intent must be non-empty UTF-8 text of at most 512 bytes")
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
	safe, ok := SanitizeArguments(raw)
	if !ok {
		return raw
	}
	return safe
}

func SanitizeArguments(raw string) (string, bool) {
	values, err := parseObject(raw)
	if err != nil {
		return "{}", false
	}
	delete(values, ArgumentName)
	safe, err := json.Marshal(values)
	if err != nil {
		return "{}", false
	}
	return string(safe), true
}

func parseObject(raw string) (map[string]json.RawMessage, error) {
	if !utf8.ValidString(raw) {
		return nil, errors.New("invalid UTF-8")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return nil, errors.New("not an object")
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != '{' {
		return nil, errors.New("not an object")
	}
	values := make(map[string]json.RawMessage)
	for decoder.More() {
		keyToken, keyErr := decoder.Token()
		if keyErr != nil {
			return nil, errors.New("invalid object key")
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, errors.New("invalid object key")
		}
		if _, exists := values[key]; exists {
			return nil, errors.New("duplicate object key")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, errors.New("invalid object value")
		}
		values[key] = value
	}
	token, err = decoder.Token()
	if err != nil {
		return nil, errors.New("object is not closed")
	}
	delimiter, ok = token.(json.Delim)
	if !ok || delimiter != '}' {
		return nil, errors.New("object is not closed")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing JSON")
	}
	return values, nil
}

func validUnicodeEscapes(raw json.RawMessage) bool {
	for index := 0; index < len(raw); {
		if raw[index] != '\\' {
			index++
			continue
		}
		if index+1 >= len(raw) {
			return false
		}
		if raw[index+1] != 'u' {
			index += 2
			continue
		}
		code, ok := unicodeEscape(raw[index+2:])
		if !ok {
			return false
		}
		if code >= 0xD800 && code <= 0xDBFF {
			if index+12 > len(raw) || raw[index+6] != '\\' || raw[index+7] != 'u' {
				return false
			}
			low, ok := unicodeEscape(raw[index+8:])
			if !ok || low < 0xDC00 || low > 0xDFFF {
				return false
			}
			index += 12
			continue
		}
		if code >= 0xDC00 && code <= 0xDFFF {
			return false
		}
		index += 6
	}
	return true
}

func unicodeEscape(raw []byte) (uint16, bool) {
	if len(raw) < 4 {
		return 0, false
	}
	var code uint16
	for _, digit := range raw[:4] {
		code <<= 4
		switch {
		case digit >= '0' && digit <= '9':
			code += uint16(digit - '0')
		case digit >= 'a' && digit <= 'f':
			code += uint16(digit-'a') + 10
		case digit >= 'A' && digit <= 'F':
			code += uint16(digit-'A') + 10
		default:
			return 0, false
		}
	}
	return code, true
}
