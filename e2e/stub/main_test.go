package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatCompletionsStreamsSSEChunks(t *testing.T) {
	reqBody := `{"model":"stub","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()

	handler().ServeHTTP(rec, req)

	res := rec.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}

	contentType := res.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", contentType)
	}

	respBody := rec.Body.String()

	frames := extractDataFrames(t, respBody)
	if len(frames) < 2 {
		t.Fatalf("expected at least 2 data frames (content chunk + [DONE]), got %d: %v", len(frames), frames)
	}

	last := frames[len(frames)-1]
	if last != "[DONE]" {
		t.Fatalf("last frame = %q, want %q", last, "[DONE]")
	}

	var sawContent bool
	var full strings.Builder
	for _, f := range frames[:len(frames)-1] {
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(f), &chunk); err != nil {
			t.Fatalf("unmarshal chunk %q: %v", f, err)
		}
		if len(chunk.Choices) == 0 {
			t.Fatalf("chunk %q has no choices", f)
		}
		if chunk.Choices[0].Delta.Content != "" {
			sawContent = true
			full.WriteString(chunk.Choices[0].Delta.Content)
		}
	}

	if !sawContent {
		t.Fatalf("no chunk carried delta.content")
	}
	if full.Len() == 0 {
		t.Fatalf("accumulated content is empty")
	}
}

func TestEmbeddingsReturnsFixedVector(t *testing.T) {
	reqBody := `{"model":"stub","input":["hello world"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()

	handler().ServeHTTP(rec, req)

	res := rec.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}

	var body struct {
		Object string `json:"object"`
		Data   []struct {
			Object    string    `json:"object"`
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Model string `json:"model"`
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body.Object != "list" {
		t.Fatalf("object = %q, want %q", body.Object, "list")
	}
	if len(body.Data) != 1 {
		t.Fatalf("data length = %d, want 1", len(body.Data))
	}
	if body.Data[0].Object != "embedding" {
		t.Fatalf("data[0].object = %q, want %q", body.Data[0].Object, "embedding")
	}
	if body.Data[0].Index != 0 {
		t.Fatalf("data[0].index = %d, want 0", body.Data[0].Index)
	}
	if len(body.Data[0].Embedding) != defaultEmbeddingVectorLen {
		t.Fatalf("data[0].embedding length = %d, want %d", len(body.Data[0].Embedding), defaultEmbeddingVectorLen)
	}
}

// TestEmbeddingsHonorsRequestedDimensions ensures the stub sizes its vectors
// to whatever "dimensions" the caller requests (e.g. KADENCE_EMBED_DIMENSIONS
// set to something other than the default), rather than a hardcoded length —
// this is what makes the stub safe to use against fitDimensions validation.
func TestEmbeddingsHonorsRequestedDimensions(t *testing.T) {
	reqBody := `{"model":"stub","input":["hello"],"dimensions":256}`
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()

	handler().ServeHTTP(rec, req)

	var body struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Data) != 1 {
		t.Fatalf("data length = %d, want 1", len(body.Data))
	}
	if len(body.Data[0].Embedding) != 256 {
		t.Fatalf("embedding length = %d, want 256", len(body.Data[0].Embedding))
	}
}

func TestEmbeddingsReturnsOnePerInput(t *testing.T) {
	reqBody := `{"model":"stub","input":["a","b","c"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()

	handler().ServeHTTP(rec, req)

	var body struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(body.Data) != 3 {
		t.Fatalf("data length = %d, want 3", len(body.Data))
	}
	for i, d := range body.Data {
		if d.Index != i {
			t.Fatalf("data[%d].index = %d, want %d", i, d.Index, i)
		}
		if len(d.Embedding) != defaultEmbeddingVectorLen {
			t.Fatalf("data[%d].embedding length = %d, want %d", i, len(d.Embedding), defaultEmbeddingVectorLen)
		}
	}
}

// extractDataFrames splits an SSE body into the payload of each "data: " line,
// ignoring blank keep-alive lines.
func extractDataFrames(t *testing.T, body string) []string {
	t.Helper()
	var frames []string
	for line := range strings.SplitSeq(body, "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		frames = append(frames, strings.TrimPrefix(line, "data: "))
	}
	return frames
}
