package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tonklasocute/git-quill/internal/provider"
)

func TestStream(t *testing.T) {
	var got chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/x-ndjson")
		for _, part := range []string{"<think>x</think>", "feat(api): ", "add ", "health check"} {
			fmt.Fprintf(w, `{"message":{"role":"assistant","content":%q},"done":false}`+"\n", part)
			w.(http.Flusher).Flush()
		}
		fmt.Fprintln(w, `{"message":{"content":""},"done":true,"done_reason":"stop","prompt_eval_count":321,"eval_count":12}`)
	}))
	defer srv.Close()

	c, _ := New(provider.Options{Model: "qwen3.5:4b", BaseURL: srv.URL + "/", Extra: map[string]string{"num_ctx": "16384"}})
	var deltas []string
	resp, err := c.Stream(context.Background(), provider.Request{
		System:    "sys",
		Messages:  []provider.Message{{Role: provider.User, Content: "diff"}},
		MaxTokens: 500,
	}, func(s string) { deltas = append(deltas, s) })
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "feat(api): add health check" {
		t.Errorf("text = %q", resp.Text)
	}
	if resp.Usage.InputTokens != 321 || resp.Usage.OutputTokens != 12 || resp.Usage.Estimated || resp.StopReason != "stop" {
		t.Errorf("resp = %+v", resp)
	}
	if len(deltas) != 4 {
		t.Errorf("deltas = %q", deltas)
	}
	if got.Model != "qwen3.5:4b" || !got.Stream || len(got.Messages) != 2 || got.Messages[0].Role != "system" {
		t.Errorf("request = %+v", got)
	}
	if got.Options["num_ctx"] != float64(16384) || got.Options["num_predict"] != float64(500) {
		t.Errorf("options = %v", got.Options)
	}
	if got.Think == nil || *got.Think {
		t.Error("think should default to false")
	}
	if c.Name() != "ollama" || c.Model() != "qwen3.5:4b" {
		t.Error("Name/Model")
	}
}

func TestThinkDefaultOmitted(t *testing.T) {
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&raw)
		fmt.Fprintln(w, `{"message":{"content":"ok"},"done":true}`)
	}))
	defer srv.Close()
	c, _ := New(provider.Options{Model: "m", BaseURL: srv.URL, Extra: map[string]string{"think": "default"}})
	resp, err := c.Stream(context.Background(), provider.Request{Messages: []provider.Message{{Role: provider.User, Content: "hello world"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["think"]; ok {
		t.Error("think should be omitted when set to default")
	}
	if !resp.Usage.Estimated || resp.Usage.InputTokens == 0 {
		t.Errorf("usage should be estimated when server omits counts: %+v", resp.Usage)
	}
}

func TestModelNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		fmt.Fprint(w, `{"error":"model \"llama9\" not found, try pulling it first"}`)
	}))
	defer srv.Close()
	c, _ := New(provider.Options{Model: "llama9", BaseURL: srv.URL})
	_, err := c.Stream(context.Background(), provider.Request{}, nil)
	var pe *provider.Error
	if !errors.As(err, &pe) || pe.Kind != provider.KindNotFound || !strings.Contains(pe.Hint, "ollama pull llama9") {
		t.Fatalf("err = %v", err)
	}
}

func TestNotRunning(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	c, _ := New(provider.Options{Model: "m", BaseURL: url})
	_, err := c.Stream(context.Background(), provider.Request{}, nil)
	var pe *provider.Error
	if !errors.As(err, &pe) || pe.Kind != provider.KindUnavailable || !strings.Contains(pe.Hint, "ollama serve") ||
		!strings.Contains(pe.Message, "not running") {
		t.Fatalf("err = %v", err)
	}
}

func TestStreamErrors(t *testing.T) {
	for name, body := range map[string]string{
		"error chunk": `{"error":"out of memory"}`,
		"bad json":    `not json`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, body) }))
			defer srv.Close()
			c, _ := New(provider.Options{Model: "m", BaseURL: srv.URL})
			if _, err := c.Stream(context.Background(), provider.Request{}, nil); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestBaseURLFromEnv(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "0.0.0.0:9999")
	c, _ := New(provider.Options{})
	if c.base != "http://localhost:9999" {
		t.Errorf("base = %q", c.base)
	}
	t.Setenv("OLLAMA_HOST", "")
	c, _ = New(provider.Options{})
	if c.base != DefaultBaseURL {
		t.Errorf("default base = %q", c.base)
	}
	if normalizeHost("https://gpu.box:11434") != "https://gpu.box:11434" {
		t.Error("scheme kept")
	}
}
