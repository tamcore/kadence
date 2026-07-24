package fit

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeMCPCaller struct{ result string }

var errUnexpectedCall = errors.New("unexpected MCP call")

func (f fakeMCPCaller) Call(_ context.Context, name, args string) (string, error) {
	if name != "garmin__download_activity_fit" || args != `{"activity_id":42}` {
		return "", errUnexpectedCall
	}
	return f.result, nil
}

func TestAnalyzerDownloadsBridgeFileAndReturnsSummary(t *testing.T) {
	data := testActivityFIT(t, 1)
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/files/activity.fit" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if user, pass, ok := r.BasicAuth(); !ok || user != "u" || pass != "p" {
			t.Fatal("missing bridge auth")
		}
		_, _ = w.Write(data)
	}))
	defer bridge.Close()

	a := NewAnalyzer("garmin__download_activity_fit", bridge.URL, "u", "p", 32<<20)
	got, err := a.Analyze(context.Background(), fakeMCPCaller{result: `{"path":"/data/fit/activity.fit"}`}, 42)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if got.Summary.Sport != "running" || len(got.Splits) != 1 {
		t.Fatalf("Analyze() = %+v", got)
	}
}

func TestAnalyzerClassifiesDecodeFailure(t *testing.T) {
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not a FIT file"))
	}))
	defer bridge.Close()

	a := NewAnalyzer("garmin__download_activity_fit", bridge.URL, "u", "p", 32<<20)
	_, err := a.Analyze(context.Background(), fakeMCPCaller{result: `{"file_path":"/data/fit/activity.fit"}`}, 42)
	if err == nil {
		t.Fatal("Analyze() error = nil, want decode error")
	}
	if got := FailureStage(err); got != "decode" {
		t.Fatalf("FailureStage() = %q, want decode", got)
	}
}
