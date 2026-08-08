package mcpintent

import (
	"encoding/json"
	"slices"

	"github.com/tamcore/kadence/internal/provider"
)

const schemaMarshalCategory = "marshal"

type SchemaError struct {
	Category string
}

func (e *SchemaError) Error() string {
	return "intent schema: " + e.Category
}

func AugmentTool(def provider.ToolDefinition) (provider.ToolDefinition, error) {
	var schema map[string]json.RawMessage
	if err := json.Unmarshal(def.Parameters, &schema); err != nil {
		return provider.ToolDefinition{}, &SchemaError{Category: "malformed"}
	}

	var typ string
	if err := json.Unmarshal(schema["type"], &typ); err != nil || typ != "object" {
		return provider.ToolDefinition{}, &SchemaError{Category: "non_object"}
	}

	properties := map[string]json.RawMessage{}
	if raw, ok := schema["properties"]; ok {
		if err := json.Unmarshal(raw, &properties); err != nil {
			return provider.ToolDefinition{}, &SchemaError{Category: "malformed_properties"}
		}
	}
	if _, exists := properties[ArgumentName]; exists {
		return provider.ToolDefinition{}, &SchemaError{Category: "reserved_collision"}
	}
	properties[ArgumentName] = json.RawMessage(`{"type":"string","description":"Concise reason this call directly serves the current user or Scheduled instruction."}`)
	propertiesJSON, err := json.Marshal(properties)
	if err != nil {
		return provider.ToolDefinition{}, &SchemaError{Category: schemaMarshalCategory}
	}
	schema["properties"] = propertiesJSON

	var required []string
	if raw, ok := schema["required"]; ok {
		if err := json.Unmarshal(raw, &required); err != nil {
			return provider.ToolDefinition{}, &SchemaError{Category: "malformed_required"}
		}
	}
	if !slices.Contains(required, ArgumentName) {
		required = append(required, ArgumentName)
	}
	requiredJSON, err := json.Marshal(required)
	if err != nil {
		return provider.ToolDefinition{}, &SchemaError{Category: schemaMarshalCategory}
	}
	schema["required"] = requiredJSON

	parameters, err := json.Marshal(schema)
	if err != nil {
		return provider.ToolDefinition{}, &SchemaError{Category: schemaMarshalCategory}
	}
	def.Parameters = parameters
	return def, nil
}
