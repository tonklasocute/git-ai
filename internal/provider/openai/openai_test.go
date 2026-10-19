package openai

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
		fmt.Fprintf(w, "data: %s\n\n", c)
		w.(http.Flusher).Flush()
	}
}

var okChunks = []string{
	`{"id":"c1","model":"gpt-6.1-sol-2026-09-01","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
	`{"choices":[{"index":0,"delta":{"content":"fix(db): "},"finish_reason":null}]}`,
	`{"choices":[{"index":0,"delta":{"content":"close rows"},"finish_reason":"stop"}]}`,
	`{"choices":[],"usage":{"prompt_tokens":50,"completion_tokens":4}}`,
	`[DONE]`,
}

var req = provider.Request{System: "sys", Messages: []provider.Message{{Role: provider.User, Content: "diff"}}, MaxTokens: 512}

func TestOpenAIStream(t *testing.T) {
	var body map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		auth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		stream(w, okChunks...)
	}))
	defer srv.Close()
	c, err := New("openai", provider.Options{Model: "gpt-6.1-sol", APIKey: "sk-x", BaseURL: srv.URL + "/v1/"})
	if err != nil {
		t.Fatal(err)
	}
	var deltas []string
	resp, err := c.Stream(context.Background(), req, func(s string) { deltas = append(deltas, s) })
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "fix(db): close rows" || resp.StopReason != "stop" || resp.Model != "gpt-6.1-sol-2026-09-01" ||
		resp.Usage.InputTokens != 50 || resp.Usage.OutputTokens != 4 || resp.Usage.Estimated {
		t.Errorf("resp = %+v", resp)
	}
	if len(deltas) != 2 || auth != "Bearer sk-x" {
		t.Errorf("deltas=%q auth=%q", deltas, auth)
	}
	if body["max_completion_tokens"] != float64(16000) || body["max_tokens"] != nil || body["stream"] != true {
		t.Errorf("body = %v", body)
	}
	msgs := body["messages"].([]any)
	if len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "system" {
		t.Errorf("messages = %v", msgs)
	}
	if c.Name() != "openai" || c.Model() != "gpt-6.1-sol" {
		t.Error("Name/Model")
	}
}

func TestCompatibleProviders(t *testing.T) {
	var body map[string]any
	var hdr http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr = r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		// LM Studio-style: no usage chunk, no [DONE].
		stream(w, `{"choices":[{"delta":{"content":"<think>x</think>docs: update"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	c, _ := New("lmstudio", provider.Options{Model: "qwen", BaseURL: srv.URL})
	resp, err := c.Stream(context.Background(), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "docs: update" || !resp.Usage.Estimated {
		t.Errorf("resp = %+v", resp)
	}
	if hdr.Get("Authorization") != "" || body["max_tokens"] != float64(16000) || body["max_completion_tokens"] != nil {
		t.Errorf("hdr=%v body=%v", hdr, body)
	}

	if _, err := New("x", provider.Options{}); err == nil {
		t.Error("missing base_url should fail")
	}
}

func TestOpenRouterHeaders(t *testing.T) {
	var hdr http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr = r.Header.Clone()
		stream(w, okChunks...)
	}))
	defer srv.Close()
	c, _ := New("openrouter", provider.Options{Model: "x", APIKey: "k", BaseURL: srv.URL + "/openrouter.ai/api/v1"})
	if _, err := c.Stream(context.Background(), req, nil); err != nil {
		t.Fatal(err)
	}
	if hdr.Get("X-Title") != "git-quill" || hdr.Get("HTTP-Referer") == "" {
		t.Errorf("headers = %v", hdr)
	}
}

func TestErrorsAndRetry(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer bad":
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":{"message":"Incorrect API key provided","type":"invalid_request_error"}}`)
		case "Bearer nomodel":
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":{"message":"The model 'gpt-9' does not exist"}}`)
		case "Bearer flaky":
			if n.Add(1) == 1 {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			stream(w, okChunks...)
		case "Bearer midstream":
			stream(w, `{"choices":[{"delta":{"content":"a"}}]}`, `{"error":{"message":"upstream overloaded"}}`)
		case "Bearer filter":
			stream(w, `{"choices":[{"delta":{},"finish_reason":"content_filter"}]}`, "[DONE]")
		case "Bearer length":
			stream(w, `{"choices":[{"delta":{},"finish_reason":"length"}]}`, "[DONE]")
		case "Bearer garbage":
			stream(w, `{not json`)
		}
	}))
	defer srv.Close()
	mk := func(key string) *Client {
		c, _ := New("openai", provider.Options{Model: "gpt-9", APIKey: key, BaseURL: srv.URL, MaxRetries: 2, KeyEnv: "OPENAI_API_KEY"})
		return c
	}
	for key, kind := range map[string]provider.Kind{
		"bad": provider.KindAuth, "nomodel": provider.KindNotFound, "midstream": provider.KindServer,
		"filter": provider.KindRefusal, "length": provider.KindBadRequest, "garbage": provider.KindUnavailable,
	} {
		_, err := mk(key).Stream(context.Background(), req, nil)
		var pe *provider.Error
		if !errors.As(err, &pe) || pe.Kind != kind {
			t.Errorf("%s: err = %v", key, err)
		}
	}
	_, err := mk("bad").Stream(context.Background(), req, nil)
	if !strings.Contains(err.Error(), "Incorrect API key provided") || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Errorf("auth error text: %v", err)
	}
	resp, err := mk("flaky").Stream(context.Background(), req, nil)
	if err != nil || resp.Text != "fix(db): close rows" || n.Load() != 2 {
		t.Errorf("retry: %v %v %d", resp, err, n.Load())
	}
}

func TestServerDown(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	u := srv.URL
	srv.Close()
	c, _ := New("lmstudio", provider.Options{Model: "m", BaseURL: u + "/v1"})
	_, err := c.Stream(context.Background(), req, nil)
	var pe *provider.Error
	if !errors.As(err, &pe) || pe.Kind != provider.KindUnavailable || !strings.Contains(pe.Hint, "is the server running") {
		t.Fatalf("err = %v", err)
	}
}
