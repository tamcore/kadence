package fit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
)

type analysisFailure struct {
	stage string
	err   error
}

func (e *analysisFailure) Error() string {
	return "FIT analysis failed during " + e.stage
}

func (e *analysisFailure) Unwrap() error { return e.err }

func failAnalysis(stage string, err error) error {
	return &analysisFailure{stage: stage, err: err}
}

// FailureStage returns a bounded, path-free stage name suitable for
// operational logging. Unknown errors are classified as "unknown".
func FailureStage(err error) string {
	var failure *analysisFailure
	if errors.As(err, &failure) {
		return failure.stage
	}
	return "unknown"
}

// MCPCaller is the narrow MCP surface the analyzer needs.
type MCPCaller interface {
	Call(ctx context.Context, toolName, argsJSON string) (string, error)
}

// Analyzer orchestrates the configured MCP download and private file bridge.
type Analyzer struct {
	downloadTool string
	bridgeURL    string
	authUser     string
	authPass     string
	maxBytes     int64
	httpClient   *http.Client
}

// NewAnalyzer creates an analyzer for one configured Garmin download tool.
func NewAnalyzer(downloadTool, bridgeURL, authUser, authPass string, maxBytes int64) *Analyzer {
	return &Analyzer{downloadTool: downloadTool, bridgeURL: strings.TrimRight(bridgeURL, "/"), authUser: authUser, authPass: authPass, maxBytes: maxBytes, httpClient: http.DefaultClient}
}

// Analyze downloads one activity through MCP, fetches its temporary FIT file,
// and returns the bounded decoded activity.
func (a *Analyzer) Analyze(ctx context.Context, caller MCPCaller, activityID int64) (Activity, error) {
	args, _ := json.Marshal(map[string]int64{"activity_id": activityID})
	result, err := caller.Call(ctx, a.downloadTool, string(args))
	if err != nil {
		return Activity{}, failAnalysis("download", err)
	}
	name, err := fitFilename(result)
	if err != nil {
		return Activity{}, failAnalysis("download_result", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.bridgeURL+"/files/"+url.PathEscape(name), nil)
	if err != nil {
		return Activity{}, failAnalysis("bridge_request", err)
	}
	req.SetBasicAuth(a.authUser, a.authPass)
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return Activity{}, failAnalysis("bridge_fetch", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Activity{}, failAnalysis("bridge_status", fmt.Errorf("bridge returned %s", resp.Status))
	}
	activity, err := Decode(io.LimitReader(resp.Body, a.maxBytes+1))
	if err != nil {
		return Activity{}, failAnalysis("decode", err)
	}
	return activity, nil
}

func fitFilename(result string) (string, error) {
	path := strings.TrimSpace(result)
	var payload struct {
		Path     string `json:"path"`
		FilePath string `json:"file_path"`
	}
	if json.Unmarshal([]byte(path), &payload) == nil {
		if payload.Path != "" {
			path = payload.Path
		} else {
			path = payload.FilePath
		}
	}
	name := filepath.Base(path)
	if name == "." || name == path || !strings.HasSuffix(name, ".fit") {
		return "", fmt.Errorf("download FIT activity: invalid file reference")
	}
	return name, nil
}
