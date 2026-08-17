package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// This file pins the one property everything else rests on: a server that asks
// for confirmation during a tool call gets its question routed to the user who
// made that call, and to nobody else.
//
// It speaks the wire rather than using an MCP server library, because the two
// implementations disagree about where the question goes. The official go-sdk —
// which the garmin server uses — writes a server-initiated request to the
// stream of the in-flight request it was made from, i.e. the tool call's own
// POST. mcp-go's server writes it only to the standalone GET stream. A test
// built on mcp-go's server would therefore pass while proving the opposite of
// what the real pairing does.

// wireServer is a minimal streamable-http MCP server. Its one tool answers the
// call's POST with an SSE stream that first asks elicitation/create, waits for
// the client's answer to arrive on a later POST, and only then sends the tool
// result on the original stream.
type wireServer struct {
	tool string
	// waiting maps an elicitation id to the stream waiting on its answer.
	mu      sync.Mutex
	waiting map[string]chan json.RawMessage
	// asked counts elicitation requests actually put on the wire, and also
	// numbers them: two clients each start their JSON-RPC ids at 1, so the
	// call id alone would collide between concurrent callers.
	asked atomic.Int32
}

func newWireServer(t *testing.T) (*httptest.Server, *wireServer) {
	t.Helper()
	ws := &wireServer{tool: confirmToolName, waiting: map[string]chan json.RawMessage{}}
	ts := httptest.NewServer(http.HandlerFunc(ws.serve))
	t.Cleanup(ts.Close)
	return ts, ws
}

type jsonrpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
}

func (s *wireServer) serve(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// The standalone listening stream. It is opened and then left silent:
		// nothing must ever be asked here, and a question that appeared here
		// would be unattributable.
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Bounded, so the handler cannot outlive the test and block the
		// server's own shutdown while a reader still holds the connection.
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
		return
	case http.MethodDelete:
		w.WriteHeader(http.StatusOK)
		return
	}

	body, _ := io.ReadAll(r.Body)
	var msg jsonrpcEnvelope
	if err := json.Unmarshal(body, &msg); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	switch msg.Method {
	case "initialize":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "wire-session")
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-11-25",`+
			`"capabilities":{"tools":{}},"serverInfo":{"name":"wire","version":"0.0.1"}}}`, msg.ID)
	case "tools/list":
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":%q,`+
			`"description":"d","inputSchema":{"type":"object"}}]}}`, msg.ID, s.tool)
	case "tools/call":
		s.serveCall(w, r, msg)
	case "":
		// A response to our elicitation request, posted by the client.
		s.deliverAnswer(msg)
		w.WriteHeader(http.StatusAccepted)
	default:
		// Notifications and anything else.
		w.WriteHeader(http.StatusAccepted)
	}
}

// serveCall answers a tools/call POST with an SSE stream, asking for
// confirmation on that same stream before producing a result.
func (s *wireServer) serveCall(w http.ResponseWriter, r *http.Request, call jsonrpcEnvelope) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	elicitID := fmt.Sprintf("elicit-%d", s.asked.Add(1))
	answers := make(chan json.RawMessage, 1)
	s.mu.Lock()
	s.waiting[elicitID] = answers
	s.mu.Unlock()

	_, _ = fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%q,\"method\":\"elicitation/create\","+
		"\"params\":{\"message\":%q,\"requestedSchema\":{\"type\":\"object\",\"required\":[\"confirm\"],"+
		"\"properties\":{\"confirm\":{\"type\":\"boolean\"}}}}}\n\n",
		elicitID, confirmToolPrompt)
	flusher.Flush()

	var answer json.RawMessage
	select {
	case answer = <-answers:
	case <-r.Context().Done():
		return
	case <-time.After(5 * time.Second):
		return
	}

	allowed := strings.Contains(string(answer), `"accept"`) && strings.Contains(string(answer), `"confirm":true`)
	text := "refused"
	if allowed {
		text = confirmedResultTxt
	}
	_, _ = fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"content\":"+
		"[{\"type\":\"text\",\"text\":%q}],\"isError\":%t}}\n\n", call.ID, text, !allowed)
	flusher.Flush()
}

func (s *wireServer) deliverAnswer(msg jsonrpcEnvelope) {
	var id string
	if err := json.Unmarshal(msg.ID, &id); err != nil {
		return
	}
	s.mu.Lock()
	ch := s.waiting[id]
	delete(s.waiting, id)
	s.mu.Unlock()
	if ch != nil {
		ch <- msg.Result
	}
}

// wireRegistry points a per-principal server at url and wires src.
func wireRegistry(t *testing.T, url string, principals stubPrincipals, tokens stubTokens, src ConfirmSource) *Registry {
	t.Helper()
	s := oauthServerAt(url)
	s.URL, s.OAuthResource = url, url
	s.Tools = []string{confirmToolName}
	reg := NewRegistry([]Server{s}, nil, nil)
	reg.SetPrincipalSource(principals)
	reg.SetTokenSource(tokens)
	reg.SetConfirmSource(src)
	t.Cleanup(func() { _ = reg.Close() })
	return reg
}

func TestOverTheWireAConfirmationReachesTheCallingUser(t *testing.T) {
	ts, ws := newWireServer(t)
	src := &stubConfirm{allow: true}
	reg := wireRegistry(t, ts.URL, stubPrincipals{testUsername: 42}, stubTokens{testPrincipalKey: testBearer42}, src)

	out, err := reg.Call(context.Background(), testUsername, confirmTestTool, "{}")
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out != confirmedResultTxt {
		t.Fatalf("result = %q, want %q", out, confirmedResultTxt)
	}
	if n := ws.asked.Load(); n != 1 {
		t.Fatalf("the server asked %d times, want 1", n)
	}
	calls, userID, tool, _ := src.seen()
	if calls != 1 || userID != 42 || tool != confirmTestTool {
		t.Fatalf("the user was asked (%d, %d, %q), want (1, 42, %q)", calls, userID, tool, confirmTestTool)
	}
}

func TestOverTheWireADeclineRefusesTheCall(t *testing.T) {
	ts, _ := newWireServer(t)
	src := &stubConfirm{allow: false}
	reg := wireRegistry(t, ts.URL, stubPrincipals{testUsername: 42}, stubTokens{testPrincipalKey: testBearer42}, src)

	if _, err := reg.Call(context.Background(), testUsername, confirmTestTool, "{}"); err == nil {
		t.Fatal("a declined call succeeded")
	}
}

// routingConfirm answers yes and records every user it was asked about.
type routingConfirm struct {
	mu   sync.Mutex
	seen []int64
}

func (r *routingConfirm) Confirm(_ context.Context, userID int64, _, _ string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, userID)
	return true, nil
}

func (r *routingConfirm) asked() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.seen...)
}

func TestOverTheWireTwoUsersConfirmationsDoNotCross(t *testing.T) {
	// Two users call the same tool at the same time. Each question must name
	// the user who caused it — never one user twice. This is the case a
	// registry-level user->sink map would get wrong.
	ts, ws := newWireServer(t)
	src := &routingConfirm{}
	reg := wireRegistry(t, ts.URL,
		stubPrincipals{testUsername: 42, testOtherUsername: 43},
		stubTokens{testPrincipalKey: testBearer42, "43/garmin": "tok-43"},
		src)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, user := range []string{testUsername, testOtherUsername} {
		wg.Go(func() {
			if _, err := reg.Call(context.Background(), user, confirmTestTool, "{}"); err != nil {
				errs <- fmt.Errorf("Call(%s): %w", user, err)
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	if n := ws.asked.Load(); n != 2 {
		t.Fatalf("the server asked %d times, want 2", n)
	}
	got := src.asked()
	if len(got) != 2 {
		t.Fatalf("the confirm source was called %d times, want 2", len(got))
	}
	if got[0] == got[1] {
		t.Fatalf("both questions named user %d; one user answered for the other", got[0])
	}
}

// firstSSEData returns the first data line on a stream, or "" if none arrives.
func firstSSEData(body io.Reader) string {
	sc := bufio.NewScanner(body)
	for sc.Scan() {
		if line := sc.Text(); strings.HasPrefix(line, "data: ") {
			return strings.TrimPrefix(line, "data: ")
		}
	}
	return ""
}

func TestTheStandaloneStreamIsNeverAskedAnything(t *testing.T) {
	// A question arriving here would carry no caller, and the client would
	// have to refuse it. Asserting silence keeps the fake honest about the
	// property the real server guarantees.
	ts, _ := newWireServer(t)
	src := &stubConfirm{allow: true}
	reg := wireRegistry(t, ts.URL, stubPrincipals{testUsername: 42}, stubTokens{testPrincipalKey: testBearer42}, src)

	ctx := t.Context()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatalf("build GET: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open the standalone stream: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if _, err := reg.Call(context.Background(), testUsername, confirmTestTool, "{}"); err != nil {
		t.Fatalf("Call: %v", err)
	}

	got := make(chan string, 1)
	go func() { got <- firstSSEData(resp.Body) }()
	select {
	case line := <-got:
		if line != "" {
			t.Fatalf("the standalone stream received %q, want nothing", line)
		}
	case <-time.After(200 * time.Millisecond):
		// Silence is the pass.
	}
}
