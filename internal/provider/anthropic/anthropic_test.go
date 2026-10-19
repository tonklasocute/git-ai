package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tonklasocute/git-quill/internal/provider"
)

func sse(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, e := range events {
		var v struct{ Type string }
		_ = json.Unmarshal([]byte(e), &v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", v.Type, e)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
}

func okStream(stop string, texts ...string) []string {
	ev := []string{
		`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-haiku-5-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":120,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
	}
	for _, t := range texts {
		b, _ := json.Marshal(t)
		ev = append(ev, `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":`+string(b)+`}}`)
	}
	ev = append(ev,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_delta","delta":{"stop_reason":"`+stop+`","stop_sequence":null},"usage":{"output_tokens":9}}`,
		`{"type":"message_stop"}`)
	return ev
}

func client(t *testing.T, srv *httptest.Server, model string, extra map[string]string) *Client {
	t.Helper()
	c, err := New(provider.Options{Model: model, APIKey: "sk-test", BaseURL: srv.URL, MaxRetries: 2, KeyEnv: "ANTHROPIC_API_KEY", Extra: extra})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

var req = provider.Request{
	System:    "be brief",
	Messages:  []provider.Message{{Role: provider.User, Content: "diff here"}, {Role: provider.Assistant, Content: "ok"}, {Role: provider.User, Content: "again"}},
	MaxTokens: 1024,
}

func TestStream(t *testing.T) {
	var body map[string]any
	var hdr http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %s", r.URL.Path)
		}
		hdr = r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		sse(w, okStream("end_turn", "feat(api): ", "add health check")...)
	}))
	defer srv.Close()

	c := client(t, srv, "claude-haiku-5-5", map[string]string{"effort": "low"})
	var deltas []string
	resp, err := c.Stream(context.Background(), req, func(s string) { deltas = append(deltas, s) })
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "feat(api): add health check" || resp.StopReason != "end_turn" || resp.Model != "claude-haiku-5-5" {
		t.Errorf("resp = %+v", resp)
	}
	if resp.Usage.InputTokens != 120 || resp.Usage.OutputTokens != 9 || resp.Usage.Estimated {
		t.Errorf("usage = %+v", resp.Usage)
	}
	if strings.Join(deltas, "|") != "feat(api): |add health check" {
		t.Errorf("deltas = %q", deltas)
	}
	if hdr.Get("X-Api-Key") != "sk-test" || hdr.Get("Anthropic-Version") == "" {
		t.Errorf("headers = %v", hdr)
	}
	if hdr.Get("Anthropic-Beta") != "" || body["fallbacks"] != nil {
		t.Error("fallbacks must not be sent for haiku")
	}
	if body["model"] != "claude-haiku-5-5" || body["max_tokens"] != float64(16000) || body["stream"] != true {
		t.Errorf("body = %v", body)
	}
	if oc, _ := body["output_config"].(map[string]any); oc["effort"] != "low" {
		t.Errorf("output_config = %v", body["output_config"])
	}
	sys, _ := body["system"].([]any)
	msgs, _ := body["messages"].([]any)
	if len(sys) != 1 || len(msgs) != 3 || msgs[1].(map[string]any)["role"] != "assistant" {
		t.Errorf("system=%v messages=%v", sys, msgs)
	}
	if _, ok := body["thinking"]; ok {
		t.Error("thinking should be left to the model default")
	}
	if c.Name() != "anthropic" || c.Model() != "claude-haiku-5-5" {
		t.Error("Name/Model")
	}
}

// rewrite sends requests for api.anthropic.com to the test server, so we
// can check behaviour that only applies to the real endpoint.
type rewrite struct{ target *url.URL }

func (rw rewrite) RoundTrip(r *http.Request) (*http.Response, error) {
	r2 := r.Clone(r.Context())
	r2.URL.Scheme, r2.URL.Host = rw.target.Scheme, rw.target.Host
	return http.DefaultTransport.RoundTrip(r2)
}

func TestFallbacksOnRealEndpoint(t *testing.T) {
	var body map[string]any
	var beta string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		beta = r.Header.Get("Anthropic-Beta")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		sse(w, okStream("end_turn", "ok")...)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	t.Setenv("ANTHROPIC_BASE_URL", "")
	c, _ := New(provider.Options{Model: "claude-opus-5-5", APIKey: "k", HTTPClient: &http.Client{Transport: rewrite{u}}})
	if _, err := c.Stream(context.Background(), req, nil); err != nil {
		t.Fatal(err)
	}
	if beta != "server-side-fallback-2026-07-01" || body["fallbacks"] != "default" {
		t.Errorf("beta=%q fallbacks=%v", beta, body["fallbacks"])
	}
	if _, ok := body["output_config"]; ok {
		t.Error("no effort configured → no output_config")
	}
}

func TestRetryThenSuccess(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			w.Header().Set("retry-after-ms", "1")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`)
			return
		}
		sse(w, okStream("end_turn", "fix: retry")...)
	}))
	defer srv.Close()
	var retries []string
	c, _ := New(provider.Options{Model: "m", APIKey: "k", BaseURL: srv.URL, MaxRetries: 2,
		OnRetry: func(_ int, _ time.Duration, r string) { retries = append(retries, r) }})
	resp, err := c.Stream(context.Background(), req, nil)
	if err != nil || resp.Text != "fix: retry" || n.Load() != 2 {
		t.Fatalf("resp=%v err=%v attempts=%d", resp, err, n.Load())
	}
	if len(retries) != 1 || retries[0] != "HTTP 429" {
		t.Errorf("retries = %v", retries)
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		status int
		body   string
		kind   provider.Kind
		hint   string
	}{
		{401, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`, provider.KindAuth, "ANTHROPIC_API_KEY"},
		{404, `{"type":"error","error":{"type":"not_found_error","message":"model: claude-nope"}}`, provider.KindNotFound, "model id"},
		{400, `{"type":"error","error":{"type":"invalid_request_error","message":"effort is not supported on this model"}}`, provider.KindBadRequest, "effort"},
		{529, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, provider.KindServer, "try again"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("retry-after-ms", "1")
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer srv.Close()
			c := client(t, srv, "claude-nope", nil)
			c.opts.MaxRetries = 0
			_, err := c.Stream(context.Background(), req, nil)
			var pe *provider.Error
			if !errors.As(err, &pe) || pe.Kind != tt.kind || !strings.Contains(pe.Hint, tt.hint) || pe.Status != tt.status {
				t.Fatalf("err = %#v", err)
			}
			if !strings.Contains(pe.Message, strings.Split(strings.Split(tt.body, `"message":"`)[1], `"`)[0]) {
				t.Errorf("message not extracted: %q", pe.Message)
			}
		})
	}
}

func TestRefusalAndMaxTokens(t *testing.T) {
	for _, tc := range []struct {
		stop  string
		texts []string
		kind  provider.Kind
	}{
		{"refusal", nil, provider.KindRefusal},
		{"max_tokens", nil, provider.KindBadRequest},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sse(w, okStream(tc.stop, tc.texts...)...)
		}))
		c := client(t, srv, "m", nil)
		_, err := c.Stream(context.Background(), req, nil)
		var pe *provider.Error
		if !errors.As(err, &pe) || pe.Kind != tc.kind {
			t.Errorf("%s: err = %v", tc.stop, err)
		}
		srv.Close()
	}
	// max_tokens with some text is still a usable answer.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sse(w, okStream("max_tokens", "feat: partial")...)
	}))
	defer srv.Close()
	if resp, err := client(t, srv, "m", nil).Stream(context.Background(), req, nil); err != nil || resp.Text != "feat: partial" {
		t.Errorf("partial: %v %v", resp, err)
	}
}

func TestTransportError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	u := srv.URL
	srv.Close()
	c, _ := New(provider.Options{Model: "m", APIKey: "k", BaseURL: u})
	_, err := c.Stream(context.Background(), req, nil)
	var pe *provider.Error
	if !errors.As(err, &pe) || pe.Kind != provider.KindUnavailable {
		t.Fatalf("err = %v", err)
	}
	// No key: the SDK's own credential chain is used (no panic, request still made).
	t.Setenv("ANTHROPIC_API_KEY", "")
	if _, err := New(provider.Options{Model: "m"}); err != nil {
		t.Error(err)
	}
}
