package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tonklasocute/git-quill/internal/provider"
)

func stream(w http.ResponseWriter, chunks ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, c := range chunks {
		fmt.Fprintf(w, "data: %s\r\n\r\n", c)
		w.(http.Flusher).Flush()
	}
}

var okChunks = []string{
	`{"candidates":[{"content":{"parts":[{"text":"Let me think","thought":true}],"role":"model"}}]}`,
	`{"candidates":[{"content":{"parts":[{"text":"docs(readme): "}],"role":"model"}}],"modelVersion":"gemini-3.8-flash-001"}`,
	`{"candidates":[{"content":{"parts":[{"text":"add install steps"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":30,"candidatesTokenCount":5,"thoughtsTokenCount":7}}`,
}

var req = provider.Request{
	System:   "sys",
	Messages: []provider.Message{{Role: provider.User, Content: "a"}, {Role: provider.Assistant, Content: "b"}},
}

func TestStream(t *testing.T) {
	var body map[string]any
	var key, path, query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, path, query = r.Header.Get("x-goog-api-key"), r.URL.Path, r.URL.RawQuery
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		stream(w, okChunks...)
	}))
	defer srv.Close()
	c, _ := New(provider.Options{Model: "gemini-3.8-flash", APIKey: "g-key", BaseURL: srv.URL + "/v1beta"})
	var deltas []string
	resp, err := c.Stream(context.Background(), req, func(s string) { deltas = append(deltas, s) })
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "docs(readme): add install steps" || resp.Model != "gemini-3.8-flash-001" || resp.StopReason != "STOP" {
		t.Errorf("resp = %+v", resp)
	}
	if resp.Usage.InputTokens != 30 || resp.Usage.OutputTokens != 12 {
		t.Errorf("usage = %+v", resp.Usage)
	}
	if len(deltas) != 2 {
		t.Errorf("thought parts must not stream: %q", deltas)
	}
	if key != "g-key" || path != "/v1beta/models/gemini-3.8-flash:streamGenerateContent" || query != "alt=sse" {
		t.Errorf("key=%q path=%q query=%q", key, path, query)
	}
	contents := body["contents"].([]any)
	if contents[1].(map[string]any)["role"] != "model" || body["systemInstruction"] == nil {
		t.Errorf("body = %v", body)
	}
	if gc := body["generationConfig"].(map[string]any); gc["maxOutputTokens"] != float64(16000) {
		t.Errorf("generationConfig = %v", gc)
	}
	if c.Name() != "gemini" || c.Model() != "gemini-3.8-flash" {
		t.Error("Name/Model")
	}
	if d, _ := New(provider.Options{}); d.base != DefaultBaseURL {
		t.Error("default base URL")
	}
}

func TestErrors(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("x-goog-api-key") {
		case "bad":
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT"}}`)
		case "nomodel":
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":{"code":404,"message":"models/gemini-9 is not found","status":"NOT_FOUND"}}`)
		case "quota":
			if n.Add(1) == 1 {
				w.WriteHeader(http.StatusTooManyRequests)
				fmt.Fprint(w, `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED"}}`)
				return
			}
			stream(w, okChunks...)
		case "blocked":
			stream(w, `{"promptFeedback":{"blockReason":"SAFETY"}}`)
		case "safety":
			stream(w, `{"candidates":[{"content":{"parts":[]},"finishReason":"SAFETY"}]}`)
		case "maxtok":
			stream(w, `{"candidates":[{"content":{"parts":[{"text":"x","thought":true}]},"finishReason":"MAX_TOKENS"}]}`)
		case "garbage":
			stream(w, `nope`)
		}
	}))
	defer srv.Close()
	for key, kind := range map[string]provider.Kind{
		"bad": provider.KindAuth, "nomodel": provider.KindNotFound, "blocked": provider.KindRefusal,
		"safety": provider.KindRefusal, "maxtok": provider.KindBadRequest, "garbage": provider.KindUnavailable,
	} {
		c, _ := New(provider.Options{Model: "gemini-9", APIKey: key, BaseURL: srv.URL, KeyEnv: "GEMINI_API_KEY"})
		_, err := c.Stream(context.Background(), req, nil)
		var pe *provider.Error
		if !errors.As(err, &pe) || pe.Kind != kind {
			t.Errorf("%s: err = %v", key, err)
		}
		if key == "bad" && (!strings.Contains(pe.Hint, "GEMINI_API_KEY") || pe.Status != 400) {
			t.Errorf("bad key hint/status: %+v", pe)
		}
	}
	c, _ := New(provider.Options{Model: "gemini-9", APIKey: "quota", BaseURL: srv.URL, MaxRetries: 1})
	resp, err := c.Stream(context.Background(), req, nil)
	if err != nil || resp.Text == "" || n.Load() != 2 {
		t.Errorf("retry: %v %v", resp, err)
	}

	srv2 := httptest.NewServer(http.NotFoundHandler())
	u := srv2.URL
	srv2.Close()
	c, _ = New(provider.Options{Model: "m", APIKey: "k", BaseURL: u})
	if _, err := c.Stream(context.Background(), req, nil); err == nil {
		t.Error("server down should fail")
	}
}
