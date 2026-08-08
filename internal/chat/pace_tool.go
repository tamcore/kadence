package chat

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/tamcore/kadence/internal/pace"
	"github.com/tamcore/kadence/internal/provider"
)

const (
	convertPaceToolName = "kadence__convert_pace"
	metricUnitSystem    = "metric"
	imperialUnitSystem  = "imperial"
)

func paceToolDefinition() provider.ToolDefinition {
	return provider.ToolDefinition{
		Name: convertPaceToolName,
		Description: "Convert one running pace between minutes per kilometer, " +
			"minutes per mile, and meters per second. Use this tool instead of calculating.",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"unit":{"type":"string","enum":["metric","imperial"]},
				"targetpace":{"type":"string","pattern":"^(0|[1-9][0-9]*):[0-5][0-9]$"},
				"output":{"type":"string","enum":["metric","imperial","mps"]}
			},
			"required":["unit","targetpace","output"],
			"additionalProperties":false
		}`),
	}
}

func callPaceTool(argsJSON string) (string, error) {
	var args struct {
		Unit       string `json:"unit"`
		TargetPace string `json:"targetpace"`
		Output     string `json:"output"`
	}
	decoder := json.NewDecoder(strings.NewReader(argsJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return "", fmt.Errorf("invalid pace arguments: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("invalid pace arguments: trailing JSON")
	}

	result, err := pace.Convert(pace.Request{
		Unit:       args.Unit,
		TargetPace: args.TargetPace,
		Output:     args.Output,
	})
	if err != nil {
		return "", fmt.Errorf("invalid pace arguments: %w", err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encode pace result: %w", err)
	}
	return string(data), nil
}

func (s *Service) handlePaceConversion(tc provider.ToolCall, sink EventSink) provider.Message {
	_ = sink.Send(ChatEvent{
		Type: EventTool, Tool: tc.Name, Status: toolStatusRunning, Arguments: safeMCPArguments(tc.Arguments),
	})
	_ = sink.Flush()

	out, err := callPaceTool(tc.Arguments)
	status := toolStatusDone
	if err != nil {
		status = toolStatusError
		out = "error: " + err.Error()
	}

	_ = sink.Send(ChatEvent{Type: EventTool, Tool: tc.Name, Status: status})
	_ = sink.Flush()
	return provider.Message{
		Role: toolMsgRole, ToolCallID: tc.ID, Name: tc.Name, Content: out,
	}
}
