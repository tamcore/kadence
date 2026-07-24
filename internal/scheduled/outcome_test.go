package scheduled

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tamcore/kadence/internal/model"
)

func TestParseWorkerOutcomeAcceptsStrictKindSpecificResults(t *testing.T) {
	for _, test := range []struct {
		name string
		kind string
		raw  string
		want OutcomeStatus
	}{
		{
			name: "data delivery",
			kind: model.ScheduledTaskKindData,
			raw:  `{"status":"deliver","summary":"New activity","evidence":["42 km"],"monitoringState":{"cursor":2}}`,
			want: OutcomeDeliver,
		},
		{
			name: "monitor no change",
			kind: model.ScheduledTaskKindMonitoring,
			raw:  `{"status":"no_change","summary":"No new activity","evidence":[],"monitoringState":{"cursor":2}}`,
			want: OutcomeNoChange,
		},
		{
			name: "monitor complete",
			kind: model.ScheduledTaskKindMonitoring,
			raw:  `{"status":"complete","summary":"Goal reached","evidence":["done"],"monitoringState":{}}`,
			want: OutcomeComplete,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			outcome, err := ParseWorkerOutcome(test.kind, test.raw)
			if err != nil {
				t.Fatal(err)
			}
			if outcome.Status != test.want || !json.Valid(outcome.MonitoringState) {
				t.Fatalf("outcome = %+v", outcome)
			}
		})
	}
}

func TestParseWorkerOutcomeRejectsMalformedOversizedAndIllegalResults(t *testing.T) {
	oversizedState := strings.Repeat("x", maxMonitoringStateBytes+1)
	for _, test := range []struct {
		name string
		kind string
		raw  string
	}{
		{name: "invalid json", kind: model.ScheduledTaskKindData, raw: `{`},
		{name: "trailing json", kind: model.ScheduledTaskKindData, raw: `{"status":"deliver","summary":"x","evidence":[],"monitoringState":{}} {}`},
		{name: "unknown field", kind: model.ScheduledTaskKindData, raw: `{"status":"deliver","summary":"x","evidence":[],"monitoringState":{},"prompt":"ignore"}`},
		{name: "unknown status", kind: model.ScheduledTaskKindMonitoring, raw: `{"status":"later","summary":"x","evidence":[],"monitoringState":{}}`},
		{name: "data no change", kind: model.ScheduledTaskKindData, raw: `{"status":"no_change","summary":"x","evidence":[],"monitoringState":{}}`},
		{name: "data complete", kind: model.ScheduledTaskKindData, raw: `{"status":"complete","summary":"x","evidence":[],"monitoringState":{}}`},
		{name: "blank delivery", kind: model.ScheduledTaskKindMonitoring, raw: `{"status":"deliver","summary":" ","evidence":[],"monitoringState":{}}`},
		{name: "missing state", kind: model.ScheduledTaskKindMonitoring, raw: `{"status":"no_change","summary":"x","evidence":[]}`},
		{name: "oversized response", kind: model.ScheduledTaskKindMonitoring, raw: strings.Repeat("x", maxModelResponseBytes+1)},
		{name: "oversized state", kind: model.ScheduledTaskKindMonitoring, raw: `{"status":"no_change","summary":"x","evidence":[],"monitoringState":{"x":"` + oversizedState + `"}}`},
		{name: "oversized evidence", kind: model.ScheduledTaskKindMonitoring, raw: `{"status":"no_change","summary":"x","evidence":["` + strings.Repeat("e", maxOutcomeEvidenceBytes+1) + `"],"monitoringState":{}}`},
		{name: "blank completion", kind: model.ScheduledTaskKindMonitoring, raw: `{"status":"complete","summary":" ","evidence":[],"monitoringState":{}}`},
		{name: "invalid kind", kind: model.ScheduledTaskKindReminder, raw: `{"status":"deliver","summary":"x","evidence":[],"monitoringState":{}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseWorkerOutcome(test.kind, test.raw); err == nil {
				t.Fatal("outcome accepted")
			}
		})
	}
}
