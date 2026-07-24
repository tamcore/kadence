package scheduled

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
)

const (
	maxModelResponseBytes   = 64 << 10
	maxOutcomeEvidenceBytes = 32 << 10
	maxToolResultBytes      = 64 << 10
	maxEvidenceContextBytes = 256 << 10
	maxMonitoringStateBytes = 32 << 10
	maxToolMetadataBytes    = 64 << 10
)

// OutcomeStatus is the only action an unattended gather worker may request.
type OutcomeStatus string

const (
	OutcomeNoChange OutcomeStatus = "no_change"
	OutcomeDeliver  OutcomeStatus = "deliver"
	OutcomeComplete OutcomeStatus = "complete"
)

// WorkerOutcome is the strict, bounded handoff from gather to persistence or
// tool-free primary synthesis.
type WorkerOutcome struct {
	Status          OutcomeStatus
	Summary         string
	Evidence        []string
	MonitoringState json.RawMessage
}

type workerOutcomeJSON struct {
	Status          OutcomeStatus   `json:"status"`
	Summary         string          `json:"summary"`
	Evidence        []string        `json:"evidence"`
	MonitoringState json.RawMessage `json:"monitoringState"`
}

// ParseWorkerOutcome validates the worker's final JSON without accepting
// prose, unknown fields, trailing values, or kind-inappropriate actions.
func ParseWorkerOutcome(kind, raw string) (WorkerOutcome, error) {
	if len(raw) > maxModelResponseBytes {
		return WorkerOutcome{}, errors.New("scheduled: worker outcome too large")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var decoded workerOutcomeJSON
	if err := decoder.Decode(&decoded); err != nil {
		return WorkerOutcome{}, errors.New("scheduled: invalid worker outcome")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return WorkerOutcome{}, errors.New("scheduled: trailing worker outcome")
	}
	if len(decoded.MonitoringState) == 0 || !json.Valid(decoded.MonitoringState) {
		return WorkerOutcome{}, errors.New("scheduled: monitoring state is required")
	}
	if len(decoded.MonitoringState) > maxMonitoringStateBytes {
		return WorkerOutcome{}, errors.New("scheduled: monitoring state too large")
	}
	if len(decoded.Summary) > maxOutcomeEvidenceBytes || evidenceBytes(decoded.Evidence) > maxOutcomeEvidenceBytes {
		return WorkerOutcome{}, errors.New("scheduled: worker evidence too large")
	}
	switch decoded.Status {
	case OutcomeNoChange:
		if kind != modelScheduledTaskKindMonitoring {
			return WorkerOutcome{}, errors.New("scheduled: no_change requires monitoring")
		}
	case OutcomeDeliver:
		if strings.TrimSpace(decoded.Summary) == "" {
			return WorkerOutcome{}, errors.New("scheduled: delivery summary is required")
		}
	case OutcomeComplete:
		if kind != modelScheduledTaskKindMonitoring {
			return WorkerOutcome{}, errors.New("scheduled: complete requires monitoring")
		}
		if strings.TrimSpace(decoded.Summary) == "" {
			return WorkerOutcome{}, errors.New("scheduled: completion summary is required")
		}
	default:
		return WorkerOutcome{}, errors.New("scheduled: invalid worker outcome status")
	}
	if kind != modelScheduledTaskKindData && kind != modelScheduledTaskKindMonitoring {
		return WorkerOutcome{}, errors.New("scheduled: worker task kind is invalid")
	}
	return WorkerOutcome{
		Status: decoded.Status, Summary: decoded.Summary,
		Evidence: decoded.Evidence, MonitoringState: append(json.RawMessage(nil), decoded.MonitoringState...),
	}, nil
}

const (
	modelScheduledTaskKindData       = "data"
	modelScheduledTaskKindMonitoring = "monitoring"
)

func evidenceBytes(evidence []string) int {
	total := 0
	for _, item := range evidence {
		total += len(item)
	}
	return total
}
