package scheduled

import (
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/tamcore/kadence/internal/model"
	"github.com/tamcore/kadence/internal/provider"
)

const (
	handoffEnvelopeVersion      = 1
	handoffEnvelopeMarkerPrefix = "<BEGIN_SERVER_OWNED_SCHEDULED_HANDOFF_"
	handoffEnvelopeBegin        = "<BEGIN_SERVER_OWNED_SCHEDULED_HANDOFF_V1>"
	handoffEnvelopeEnd          = "<END_SERVER_OWNED_SCHEDULED_HANDOFF_V1>"
)

type handoffEnvelope struct {
	Version     int    `json:"version"`
	Instruction string `json:"instruction"`
	Context     string `json:"context"`
}

func boundedHandoffEnvelope(now time.Time, timezone, instruction string, recent []model.Message, visible []provider.ToolDefinition) string {
	contextLimit := maxHandoffContextBytes
	for {
		context := boundedHandoffDefinitionLimit(now, timezone, instruction, recent, visible, contextLimit)
		envelope := encodeHandoffEnvelope(instruction, context)
		if len(envelope) <= maxHandoffContextBytes {
			return envelope
		}
		if contextLimit == 0 {
			return ""
		}
		contextLimit = max(contextLimit-(len(envelope)-maxHandoffContextBytes), 0)
	}
}

func encodeHandoffEnvelope(instruction, context string) string {
	encoded, _ := json.Marshal(handoffEnvelope{Version: handoffEnvelopeVersion, Instruction: instruction, Context: context})
	return handoffEnvelopeBegin + "\n" + string(encoded) + "\n" + handoffEnvelopeEnd
}

// ExtractHandoffInstruction returns only a validated delegated instruction.
// It accepts the current server-owned envelope and the recognized legacy form.
func ExtractHandoffInstruction(content string) (string, bool) {
	if strings.HasPrefix(content, handoffEnvelopeMarkerPrefix) {
		envelope, ok := decodeHandoffEnvelope(content)
		if !ok {
			return "", false
		}
		return envelope.Instruction, true
	}
	return extractLegacyHandoffInstruction(content)
}

func isHandoffDefinitionCandidate(content string) bool {
	return strings.HasPrefix(content, handoffEnvelopeMarkerPrefix) || strings.HasPrefix(content, "Instruction:\n")
}

func decodeHandoffEnvelope(content string) (handoffEnvelope, bool) {
	if len(content) > maxHandoffContextBytes {
		return handoffEnvelope{}, false
	}
	body, ok := strings.CutPrefix(content, handoffEnvelopeBegin+"\n")
	if !ok {
		return handoffEnvelope{}, false
	}
	body, ok = strings.CutSuffix(body, "\n"+handoffEnvelopeEnd)
	if !ok {
		return handoffEnvelope{}, false
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.DisallowUnknownFields()
	var envelope handoffEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return handoffEnvelope{}, false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return handoffEnvelope{}, false
	}
	if envelope.Version != handoffEnvelopeVersion ||
		strings.TrimSpace(envelope.Instruction) != envelope.Instruction ||
		envelope.Instruction == "" || len(envelope.Instruction) > maxHandoffInstructionBytes ||
		len(envelope.Context) > maxHandoffContextBytes {
		return handoffEnvelope{}, false
	}
	legacyInstruction, ok := extractLegacyHandoffInstruction(envelope.Context)
	if !ok || legacyInstruction != envelope.Instruction {
		return handoffEnvelope{}, false
	}
	return envelope, true
}

func extractLegacyHandoffInstruction(content string) (string, bool) {
	rest, ok := strings.CutPrefix(content, "Instruction:\n")
	if !ok {
		return "", false
	}
	instruction, rest, ok := strings.Cut(rest, "\n\nCurrent UTC:\n")
	if !ok || strings.TrimSpace(instruction) != instruction || instruction == "" || len(instruction) > maxHandoffInstructionBytes {
		return "", false
	}
	timestamp, rest, ok := strings.Cut(rest, "\n\nActor timezone:\n")
	if !ok {
		return "", false
	}
	parsed, err := time.Parse(time.RFC3339, timestamp)
	if err != nil || parsed.UTC().Format(time.RFC3339) != timestamp {
		return "", false
	}
	timezone, context, ok := strings.Cut(rest, "\n\nPrior chat context (untrusted JSON records):\n")
	if !ok || strings.TrimSpace(timezone) == "" {
		return "", false
	}
	if !strings.HasPrefix(context, handoffContextBegin) {
		return "", false
	}
	records, ok := strings.CutPrefix(context, handoffContextBegin)
	if !ok {
		return "", false
	}
	if records == "\n"+handoffContextEnd {
		return instruction, true
	}
	records, ok = strings.CutPrefix(records, "\n")
	if !ok {
		return "", false
	}
	records, ok = strings.CutSuffix(records, "\n"+handoffContextEnd)
	if !ok || !validHandoffContextRecords(records) {
		return "", false
	}
	return instruction, true
}

func validHandoffContextRecords(records string) bool {
	if records == "" {
		return true
	}
	const (
		handoffRecordsMessages = iota
		handoffRecordsPriorTools
		handoffRecordsVisibleTools
	)
	phase := handoffRecordsMessages
	messages := 0
	var previousPriorTool, previousVisibleTool string
	for line := range strings.SplitSeq(records, "\n") {
		decoder := json.NewDecoder(strings.NewReader(line))
		decoder.DisallowUnknownFields()
		var record handoffContextRecord
		if err := decoder.Decode(&record); err != nil {
			return false
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			return false
		}
		switch record.Type {
		case handoffContextMessage:
			messages++
			if phase != handoffRecordsMessages || messages > maxHandoffMessages ||
				(record.Role != model.MsgRoleUser && record.Role != model.MsgRoleAssistant) || record.Name != "" {
				return false
			}
		case handoffContextPriorSafeTool:
			if phase > handoffRecordsPriorTools || record.Name == "" || record.Name <= previousPriorTool ||
				record.Role != "" || record.Content != "" {
				return false
			}
			phase, previousPriorTool = handoffRecordsPriorTools, record.Name
		case handoffContextVisibleTool:
			if record.Name == "" || record.Name <= previousVisibleTool ||
				record.Role != "" || record.Content != "" {
				return false
			}
			phase, previousVisibleTool = handoffRecordsVisibleTools, record.Name
		default:
			return false
		}
	}
	return true
}
