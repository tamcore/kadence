package chat

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/tamcore/kadence/internal/chat/skill"
	"github.com/tamcore/kadence/internal/provider"
)

const (
	testConvertPaceToolName    = "kadence__convert_pace"
	testMetricPaceArgs         = `{"unit":"metric","targetpace":"4:52","output":"mps"}`
	testMetricPaceResult       = `{"value":3.4246575342465753,"unit":"m/s"}`
	testMetricUnit             = "metric"
	testImperialUnit           = "imperial"
	testSpoofedToolDescription = "spoofed"
	testPaceCallID             = "pace-1"
)

func TestPaceToolDefinitionExposesStrictContract(t *testing.T) {
	definition := paceToolDefinition()
	if definition.Name != testConvertPaceToolName {
		t.Fatalf("name = %q", definition.Name)
	}

	var schema struct {
		Type       string `json:"type"`
		Properties map[string]struct {
			Type    string   `json:"type"`
			Enum    []string `json:"enum"`
			Pattern string   `json:"pattern"`
		} `json:"properties"`
		Required             []string `json:"required"`
		AdditionalProperties *bool    `json:"additionalProperties"`
	}
	if err := json.Unmarshal(definition.Parameters, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Type != "object" ||
		schema.AdditionalProperties == nil ||
		*schema.AdditionalProperties ||
		!slices.Equal(schema.Required, []string{"unit", "targetpace", "output"}) ||
		!slices.Equal(schema.Properties["unit"].Enum, []string{testMetricUnit, testImperialUnit}) ||
		!slices.Equal(schema.Properties["output"].Enum, []string{testMetricUnit, testImperialUnit, "mps"}) ||
		schema.Properties["targetpace"].Pattern != `^(0|[1-9][0-9]*):[0-5][0-9]$` {
		t.Fatalf("schema = %+v", schema)
	}
}

func TestCallPaceToolConvertsAndRejectsInvalidJSON(t *testing.T) {
	got, err := callPaceTool(testMetricPaceArgs)
	if err != nil {
		t.Fatal(err)
	}
	if got != testMetricPaceResult {
		t.Fatalf("result = %s", got)
	}

	for _, input := range []string{
		`{"unit":"metric","targetpace":"4:52","output":"mps","extra":true}`,
		`{"unit":"metric","targetpace":"4:52","output":"mps"} {}`,
		`{"unit":"metric","targetpace":"4:60","output":"mps"}`,
	} {
		if _, err := callPaceTool(input); err == nil {
			t.Errorf("callPaceTool(%q) succeeded", input)
		}
	}
}

func TestHandlePaceConversionEmitsEvents(t *testing.T) {
	service := NewService(nil, ServiceConfig{}, Deps{})
	sink := &fitEventSink{}
	msg := service.handlePaceConversion(provider.ToolCall{
		ID:        testPaceCallID,
		Name:      testConvertPaceToolName,
		Arguments: `{"unit":"metric","targetpace":"5:35","output":"mps"}`,
	}, sink)
	if msg.Content != `{"value":2.985074626865672,"unit":"m/s"}` ||
		len(sink.events) != 2 ||
		sink.events[0].Status != toolStatusRunning ||
		sink.events[0].Arguments == "" ||
		sink.events[1].Status != toolStatusDone {
		t.Fatalf("message=%+v events=%+v", msg, sink.events)
	}
}

func TestAssembleToolsAlwaysOffersLocalPaceTool(t *testing.T) {
	service := NewService(nil, ServiceConfig{MCPMaxTools: 2}, Deps{})
	if tools := service.assembleTools(context.Background(), nil); !hasToolNamed(tools, testConvertPaceToolName) {
		t.Fatalf("tools = %+v", tools)
	}

	remote := fitToolSnapshot{tools: []provider.ToolDefinition{
		{Name: testConvertPaceToolName, Description: testSpoofedToolDescription},
		{Name: "remote__one"},
		{Name: "remote__two"},
	}}
	tools := service.assembleTools(context.Background(), remote)
	if countToolNamed(tools, testConvertPaceToolName) != 1 ||
		!hasToolNamed(tools, "remote__one") ||
		hasToolNamed(tools, "remote__two") {
		t.Fatalf("tools = %+v", tools)
	}
}

func TestHandlePaceConversionReturnsToolError(t *testing.T) {
	service := NewService(nil, ServiceConfig{}, Deps{})
	sink := &fitEventSink{}
	msg := service.handlePaceConversion(provider.ToolCall{
		ID:        testPaceCallID,
		Name:      testConvertPaceToolName,
		Arguments: `{"unit":"metric","targetpace":"0:00","output":"mps"}`,
	}, sink)
	if !strings.HasPrefix(msg.Content, "error: invalid pace arguments:") ||
		len(sink.events) != 2 ||
		sink.events[1].Status != toolStatusError {
		t.Fatalf("message=%+v events=%+v", msg, sink.events)
	}
}

func countToolNamed(tools []provider.ToolDefinition, name string) int {
	count := 0
	for _, tool := range tools {
		if tool.Name == name {
			count++
		}
	}
	return count
}

func TestPaceToolLoadsDedicatedSkillBeforeLocalRetry(t *testing.T) {
	reg, err := skill.Load()
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(nil, ServiceConfig{}, Deps{Skills: reg})
	call := provider.ToolCall{
		ID:        testPaceCallID,
		Name:      convertPaceToolName,
		Arguments: testMetricPaceArgs,
	}
	gated := map[string]bool{}
	sink := &fitEventSink{}

	first := service.dispatchTool(
		t.Context(), t.Context(), 1, nil, call, gated, &turnRedactor{}, sink,
	)
	if !strings.Contains(first.Content, "one tool call per pace") {
		t.Fatalf("first result = %q", first.Content)
	}

	second := service.dispatchTool(
		t.Context(), t.Context(), 1, nil, call, gated, &turnRedactor{}, sink,
	)
	if second.Content != testMetricPaceResult {
		t.Fatalf("second result = %q", second.Content)
	}
}

func TestWorkoutSkillRequiresPaceConverter(t *testing.T) {
	reg, err := skill.Load()
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(nil, ServiceConfig{}, Deps{Skills: reg})
	call := provider.ToolCall{
		ID:        "workout-1",
		Name:      "garmin__update_workout",
		Arguments: `{"workout_id":1,"workout_data":{}}`,
	}

	result := service.dispatchTool(
		t.Context(), t.Context(), 1, nil, call, map[string]bool{}, &turnRedactor{}, &fitEventSink{},
	)
	if !strings.Contains(result.Content, testConvertPaceToolName) {
		t.Fatalf("workout guidance did not require converter: %q", result.Content)
	}
}
