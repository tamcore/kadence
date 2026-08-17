package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

const (
	confirmToolName    = "delete_workout"
	confirmToolPrompt  = "Delete workout 12? This cannot be undone."
	confirmedResultTxt = `{"status":"deleted"}`
	confirmTestTool    = "garmin__" + confirmToolName
	schemaTypeString   = "string"
)

// confirmSchema is the shape upstream asks for: one required boolean.
func confirmSchema() map[string]any {
	return map[string]any{
		schemaKeyType:       schemaTypeObject,
		schemaKeyRequired:   []any{confirmField},
		schemaKeyProperties: map[string]any{confirmField: map[string]any{schemaKeyType: schemaTypeBoolean}},
	}
}

// stubConfirm records what it was asked and answers with a fixed decision.
type stubConfirm struct {
	mu     sync.Mutex
	calls  int
	userID int64
	tool   string
	prompt string
	allow  bool
	err    error
}

func (s *stubConfirm) Confirm(_ context.Context, userID int64, tool, prompt string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.userID, s.tool, s.prompt = userID, tool, prompt
	return s.allow, s.err
}

func (s *stubConfirm) seen() (int, int64, string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls, s.userID, s.tool, s.prompt
}

// elicitingClient stands in for a remote server that asks its caller to
// confirm before running. It answers the elicitation on the context of the
// call itself, which is where the official go-sdk server sends it: an
// outgoing request is written to the stream of the in-flight request it was
// made from, and mcp-go's client hands that stream's context to the handler.
type elicitingClient struct {
	params mcpgo.ElicitationParams
	closes atomic.Int32
}

func (c *elicitingClient) ListTools(context.Context) ([]ToolInfo, error) {
	return []ToolInfo{{Name: confirmToolName}}, nil
}

func (c *elicitingClient) CallTool(ctx context.Context, name, _ string) (string, error) {
	req := mcpgo.ElicitationRequest{}
	req.Method = string(mcpgo.MethodElicitationCreate)
	req.Params = c.params
	res, err := elicitHandler{}.Elicit(ctx, req)
	if err != nil {
		return "", err
	}
	if res.Action != mcpgo.ElicitationResponseActionAccept {
		return "", fmt.Errorf("%w: tool %s: policy: the user did not confirm", ErrToolRefused, name)
	}
	content, _ := res.Content.(map[string]any)
	if allowed, _ := content[confirmField].(bool); !allowed {
		return "", fmt.Errorf("%w: tool %s: policy: the user did not confirm", ErrToolRefused, name)
	}
	return confirmedResultTxt, nil
}

func (c *elicitingClient) Close() error {
	c.closes.Add(1)
	return nil
}

// serveEliciting installs a dial that returns one elicitingClient, and reports
// how many times a client was dialed.
func serveEliciting(t *testing.T, params mcpgo.ElicitationParams) *atomic.Int32 {
	t.Helper()
	var dials atomic.Int32
	restore := dialClient
	dialClient = func(context.Context, Server, *http.Client) (mcpClient, error) {
		dials.Add(1)
		return &elicitingClient{params: params}, nil
	}
	t.Cleanup(func() { dialClient = restore })
	return &dials
}

func defaultElicitParams() mcpgo.ElicitationParams {
	return mcpgo.ElicitationParams{Message: confirmToolPrompt, RequestedSchema: confirmSchema()}
}

// confirmingRegistry wires a per-principal server against src.
func confirmingRegistry(t *testing.T, src ConfirmSource) *Registry {
	t.Helper()
	s := oauthServerAt("https://garmin.example.invalid/mcp")
	s.Tools = []string{confirmToolName}
	reg := NewRegistry([]Server{s}, nil, nil)
	reg.SetPrincipalSource(stubPrincipals{testUsername: 42})
	reg.SetTokenSource(stubTokens{testPrincipalKey: testBearer42})
	if src != nil {
		reg.SetConfirmSource(src)
	}
	t.Cleanup(func() { _ = reg.Close() })
	return reg
}

// handlerWith runs the bare handler against one confirm source, bypassing the
// transport, so a protocol shape can be asserted without a live call.
func handlerWith(ctx context.Context, src ConfirmSource, params mcpgo.ElicitationParams) (*mcpgo.ElicitationResult, error) {
	req := mcpgo.ElicitationRequest{}
	req.Method = string(mcpgo.MethodElicitationCreate)
	req.Params = params
	if src != nil {
		ctx = withConfirmTarget(ctx, confirmTarget{userID: 42, tool: confirmTestTool, src: src})
	}
	return elicitHandler{}.Elicit(ctx, req)
}

func TestAConfirmedCallProceeds(t *testing.T) {
	serveEliciting(t, defaultElicitParams())
	src := &stubConfirm{allow: true}
	reg := confirmingRegistry(t, src)

	out, err := reg.Call(context.Background(), testUsername, confirmTestTool, "{}")
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out != confirmedResultTxt {
		t.Fatalf("result = %q, want %q", out, confirmedResultTxt)
	}
	if calls, _, _, _ := src.seen(); calls != 1 {
		t.Fatalf("confirm source called %d times, want 1", calls)
	}
}

func TestADeclinedCallIsRefusedAndNamesTheConfirmation(t *testing.T) {
	serveEliciting(t, defaultElicitParams())
	src := &stubConfirm{allow: false}
	reg := confirmingRegistry(t, src)

	_, err := reg.Call(context.Background(), testUsername, confirmTestTool, "{}")
	if err == nil {
		t.Fatal("a declined call succeeded")
	}
	if !strings.Contains(err.Error(), "confirm") {
		t.Fatalf("error = %q, want it to name the confirmation", err)
	}
}

func TestADeclinedCallDoesNotEvictTheClient(t *testing.T) {
	// A refusal is an answer, not a fault. Throwing the client away would
	// discard a working authenticated session on every "no".
	dials := serveEliciting(t, defaultElicitParams())
	src := &stubConfirm{allow: false}
	reg := confirmingRegistry(t, src)

	for range 2 {
		if _, err := reg.Call(context.Background(), testUsername, confirmTestTool, "{}"); err == nil {
			t.Fatal("a declined call succeeded")
		}
	}

	if n := dials.Load(); n != 1 {
		t.Fatalf("%d dials for two refusals, want the first client to be reused", n)
	}
}

func TestTheConfirmSourceIsAskedAboutTheDispatchingUserAndTool(t *testing.T) {
	serveEliciting(t, defaultElicitParams())
	src := &stubConfirm{allow: true}
	reg := confirmingRegistry(t, src)

	if _, err := reg.Call(context.Background(), testUsername, confirmTestTool, "{}"); err != nil {
		t.Fatalf("Call: %v", err)
	}
	_, userID, tool, prompt := src.seen()
	if userID != 42 {
		t.Fatalf("userID = %d, want the resolved 42", userID)
	}
	if tool != confirmTestTool {
		t.Fatalf("tool = %q, want the namespaced name", tool)
	}
	if prompt != confirmToolPrompt {
		t.Fatalf("prompt = %q, want the server's own message", prompt)
	}
}

func TestWithoutAConfirmSourceTheCallIsRefusedRatherThanHanging(t *testing.T) {
	serveEliciting(t, defaultElicitParams())
	reg := confirmingRegistry(t, nil)

	if _, err := reg.Call(context.Background(), testUsername, confirmTestTool, "{}"); err == nil {
		t.Fatal("a call with nowhere to ask still succeeded")
	}
}

func TestAnElicitationOutsideAToolCallIsCancelled(t *testing.T) {
	// The standalone listening stream carries the dial's context, not a
	// caller's, so nothing identifies who is being asked. Fail closed.
	res, err := handlerWith(context.Background(), nil, defaultElicitParams())
	if err != nil {
		t.Fatalf("Elicit: %v", err)
	}
	if res.Action != mcpgo.ElicitationResponseActionCancel {
		t.Fatalf("action = %q, want cancel", res.Action)
	}
}

func TestAnUnexpectedElicitationShapeIsCancelledWithoutAskingTheUser(t *testing.T) {
	cases := map[string]mcpgo.ElicitationParams{
		"url mode": {
			Mode: mcpgo.ElicitationModeURL, Message: "open this",
			ElicitationID: "e1", URL: "https://example.invalid/",
		},
		"no schema": {Message: confirmToolPrompt},
		"wrong field": {Message: confirmToolPrompt, RequestedSchema: map[string]any{
			schemaKeyType: schemaTypeObject, schemaKeyRequired: []any{"password"},
			schemaKeyProperties: map[string]any{"password": map[string]any{schemaKeyType: schemaTypeString}},
		}},
		"extra field": {Message: confirmToolPrompt, RequestedSchema: map[string]any{
			schemaKeyType: schemaTypeObject, schemaKeyRequired: []any{confirmField, "reason"},
			schemaKeyProperties: map[string]any{
				confirmField: map[string]any{schemaKeyType: schemaTypeBoolean},
				"reason":     map[string]any{schemaKeyType: schemaTypeString},
			},
		}},
		"not boolean": {Message: confirmToolPrompt, RequestedSchema: map[string]any{
			schemaKeyType: schemaTypeObject, schemaKeyRequired: []any{"confirm"},
			schemaKeyProperties: map[string]any{confirmField: map[string]any{schemaKeyType: schemaTypeString}},
		}},
	}

	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			src := &stubConfirm{allow: true}
			res, err := handlerWith(context.Background(), src, params)
			if err != nil {
				t.Fatalf("Elicit: %v", err)
			}
			if res.Action != mcpgo.ElicitationResponseActionCancel {
				t.Fatalf("action = %q, want cancel", res.Action)
			}
			if calls, _, _, _ := src.seen(); calls != 0 {
				t.Fatalf("a malformed question still reached the user (%d calls)", calls)
			}
		})
	}
}

func TestAConfirmSourceFailureCancelsRatherThanErrors(t *testing.T) {
	// Returning an error would make the server report a transport fault. A
	// cancel is what "we could not ask" means in this protocol.
	src := &stubConfirm{err: errors.New("no live stream")}
	res, err := handlerWith(context.Background(), src, defaultElicitParams())
	if err != nil {
		t.Fatalf("Elicit: %v", err)
	}
	if res.Action != mcpgo.ElicitationResponseActionCancel {
		t.Fatalf("action = %q, want cancel", res.Action)
	}
}

func TestADeclineIsAnsweredWithDecline(t *testing.T) {
	src := &stubConfirm{allow: false}
	res, err := handlerWith(context.Background(), src, defaultElicitParams())
	if err != nil {
		t.Fatalf("Elicit: %v", err)
	}
	if res.Action != mcpgo.ElicitationResponseActionDecline {
		t.Fatalf("action = %q, want decline", res.Action)
	}
}

func TestAnAcceptCarriesTheBooleanUpstreamRequires(t *testing.T) {
	src := &stubConfirm{allow: true}
	res, err := handlerWith(context.Background(), src, defaultElicitParams())
	if err != nil {
		t.Fatalf("Elicit: %v", err)
	}
	if res.Action != mcpgo.ElicitationResponseActionAccept {
		t.Fatalf("action = %q, want accept", res.Action)
	}
	content, ok := res.Content.(map[string]any)
	if !ok {
		t.Fatalf("content = %#v, want an object", res.Content)
	}
	if allowed, _ := content["confirm"].(bool); !allowed {
		t.Fatalf("content = %#v, want confirm true", content)
	}
}
