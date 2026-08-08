package mcpintent

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/tamcore/kadence/internal/provider"
)

const (
	testMalformedRequiredCategory = "malformed_required"
	testWeatherToolName           = "weather__get"
)

func TestAugmentToolPreservesSchemaAndRequiresIntent(t *testing.T) {
	in := provider.ToolDefinition{
		Name:        testWeatherToolName,
		Description: "Get weather",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"city":{"type":"string","minLength":3}},"required":["city"],"additionalProperties":false}`),
	}
	got, err := AugmentTool(in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != in.Name || got.Description != in.Description {
		t.Fatalf("tool metadata changed: %+v", got)
	}
	if string(in.Parameters) != `{"type":"object","properties":{"city":{"type":"string","minLength":3}},"required":["city"],"additionalProperties":false}` {
		t.Fatalf("input schema changed: %s", in.Parameters)
	}
	assertRequiredProperty(t, got.Parameters, ArgumentName)
	assertRequiredProperty(t, got.Parameters, "city")
	assertAdditionalPropertiesFalse(t, got.Parameters)
	assertPropertyJSON(t, got.Parameters, "city", `{"type":"string","minLength":3}`)
}

func TestAugmentToolAddsMissingPropertiesAndRequired(t *testing.T) {
	got, err := AugmentTool(provider.ToolDefinition{
		Name:       testWeatherToolName,
		Parameters: json.RawMessage(`{"type":"object","additionalProperties":false}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertRequiredProperty(t, got.Parameters, ArgumentName)
	assertOnlyRequired(t, got.Parameters, ArgumentName)
	assertAdditionalPropertiesFalse(t, got.Parameters)
}

func TestAugmentToolDoesNotDuplicateIntentRequirement(t *testing.T) {
	got, err := AugmentTool(provider.ToolDefinition{
		Name:       testWeatherToolName,
		Parameters: json.RawMessage(`{"type":"object","required":["city","_kadence_intent"]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertOnlyRequired(t, got.Parameters, "city", ArgumentName)
}

func TestAugmentToolRejectsNullProperties(t *testing.T) {
	assertSchemaCategory(t, `{"type":"object","properties":null}`, "malformed_properties")
}

func TestAugmentToolRejectsInvalidRequiredEntries(t *testing.T) {
	for _, raw := range []string{
		`{"type":"object","required":null}`,
		`{"type":"object","required":[null]}`,
		`{"type":"object","required":[1]}`,
	} {
		assertSchemaCategory(t, raw, testMalformedRequiredCategory)
	}
}

func TestAugmentToolRejectsDuplicateRequiredEntries(t *testing.T) {
	for _, raw := range []string{
		`{"type":"object","required":["_kadence_intent","_kadence_intent"]}`,
		`{"type":"object","required":["city","city"]}`,
	} {
		assertSchemaCategory(t, raw, testMalformedRequiredCategory)
	}
}

func TestAugmentToolRejectsUnsafeSchemas(t *testing.T) {
	for _, test := range []struct {
		raw      string
		category string
	}{
		{raw: `{"type":"object","properties":{"_kadence_intent":{"type":"number"}}}`, category: "reserved_collision"},
		{raw: `{"type":"array"}`, category: "non_object"},
		{raw: `{`, category: "malformed"},
		{raw: `{"type":"object","properties":[]}`, category: "malformed_properties"},
		{raw: `{"type":"object","required":{}}`, category: testMalformedRequiredCategory},
	} {
		t.Run(test.category, func(t *testing.T) {
			assertSchemaCategory(t, test.raw, test.category)
		})
	}
}

func assertSchemaCategory(t *testing.T, raw, want string) {
	t.Helper()
	_, err := AugmentTool(provider.ToolDefinition{Name: "bad__tool", Parameters: json.RawMessage(raw)})
	var schemaErr *SchemaError
	if !errors.As(err, &schemaErr) {
		t.Fatalf("err=%v is not a SchemaError", err)
	}
	if schemaErr.Category != want {
		t.Fatalf("category=%q want %q", schemaErr.Category, want)
	}
}

func assertRequiredProperty(t *testing.T, raw json.RawMessage, name string) {
	t.Helper()
	var schema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(schema.Required, name) {
		return
	}
	t.Fatalf("required=%v missing %q", schema.Required, name)
}

func assertOnlyRequired(t *testing.T, raw json.RawMessage, want ...string) {
	t.Helper()
	var schema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(schema.Required, want) {
		t.Fatalf("required=%v want %v", schema.Required, want)
	}
}

func assertAdditionalPropertiesFalse(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var schema map[string]json.RawMessage
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	value, ok := schema["additionalProperties"]
	if !ok {
		t.Fatal("additionalProperties missing")
	}
	var allowed bool
	if err := json.Unmarshal(value, &allowed); err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("additionalProperties=true")
	}
}

func assertPropertyJSON(t *testing.T, raw json.RawMessage, name, want string) {
	t.Helper()
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	got, ok := schema.Properties[name]
	if !ok {
		t.Fatalf("properties missing %q", name)
	}
	var gotValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatal(err)
	}
	var wantValue any
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("property %q changed: got %s want %s", name, got, want)
	}
}
