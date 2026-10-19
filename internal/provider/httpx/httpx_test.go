package httpx

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tonklasocute/git-quill/internal/provider"
)

func testClient(retries int) (*Client, *[]time.Duration) {
	var waits []time.Duration
	c := New(provider.Options{MaxRetries: retries})
	c.Sleep = func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		return ctx.Err()
	}
	return c, &waits
}

func get(url string) func(ctx context.Context) (*http.Request, error) {
	return func(ctx context.Context) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	}
}

func TestRetryThenSuccess(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch n.Add(1) {
		case 1:
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(429)
		case 2:
			w.Header().Set("retry-after-ms", "150")
			w.WriteHeader(503)
		case 3:
			w.WriteHeader(529)
		default:
			_, _ = io.WriteString(w, "ok")
		}
	}))
	defer srv.Close()
	c, waits := testClient(3)
	var retries []string
	c.OnRetry = func(a int, _ time.Duration, reason string) { retries = append(retries, reason) }
	resp, err := c.Do(context.Background(), get(srv.URL))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("resp=%v err=%v", resp, err)
	}
	resp.Body.Close()
	if n.Load() != 4 {
		t.Errorf("attempts = %d", n.Load())
	}
	if (*waits)[0] != 2*time.Second || (*waits)[1] != 150*time.Millisecond {
		t.Errorf("waits = %v", *waits)
	}
	if (*waits)[2] < 1500*time.Millisecond || (*waits)[2] > 2500*time.Millisecond {
		t.Errorf("backoff for attempt 3 = %v (want ~2s ±25%%)", (*waits)[2])
	}
	if strings.Join(retries, ",") != "HTTP 429,HTTP 503,HTTP 529" {
		t.Errorf("OnRetry reasons = %v", retries)
	}
}

func TestRetryExhaustedReturnsLastResponse(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(500)
		_, _ = io.WriteString(w, `{"error":{"message":"boom"}}`)
	}))
	defer srv.Close()
	c, _ := testClient(2)
	resp, err := c.Do(context.Background(), get(srv.URL))
	if err != nil || resp.StatusCode != 500 || n.Load() != 3 {
		t.Fatalf("status=%v attempts=%d err=%v", resp.StatusCode, n.Load(), err)
	}
	e := ReadError(provider.HintInfo{Provider: "p"}, resp)
	if e.Kind != provider.KindServer || e.Message != "boom" {
		t.Errorf("ReadError = %+v", e)
	}
}

func TestNoRetryOn4xx(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(401)
	}))
	defer srv.Close()
	c, _ := testClient(3)
	resp, err := c.Do(context.Background(), get(srv.URL))
	if err != nil || resp.StatusCode != 401 || n.Load() != 1 {
		t.Fatalf("401 should not be retried: attempts=%d", n.Load())
	}
	resp.Body.Close()
}

func TestConnRefusedNotRetried(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	c, waits := testClient(3)
	_, err := c.Do(context.Background(), get(url))
	if err == nil || !provider.IsConnRefused(err) || len(*waits) != 0 {
		t.Fatalf("err=%v waits=%v", err, *waits)
	}
}

func TestNetworkErrorRetried(t *testing.T) {
	var n atomic.Int32
	c, _ := testClient(2)
	c.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if n.Add(1) < 3 {
			return nil, errors.New("connection reset by peer")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})}
	resp, err := c.Do(context.Background(), get("http://example.invalid"))
	if err != nil || resp.StatusCode != 200 || n.Load() != 3 {
		t.Fatalf("err=%v attempts=%d", err, n.Load())
	}
	// Exhausted: error returned.
	n.Store(-10)
	if _, err := c.Do(context.Background(), get("http://example.invalid")); err == nil {
		t.Error("want error after retries exhausted")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestContextCancelledDuringBackoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer srv.Close()
	c := New(provider.Options{MaxRetries: 5})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.Do(ctx, get(srv.URL))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	// Build errors are returned as-is.
	_, err = c.Do(context.Background(), func(context.Context) (*http.Request, error) { return nil, errors.New("bad") })
	if err == nil || err.Error() != "bad" {
		t.Errorf("err = %v", err)
	}
}

func TestRetryAfterDateAndCaps(t *testing.T) {
	c, _ := testClient(1)
	c.MaxDelay = 5 * time.Second
	h := http.Header{}
	h.Set("Retry-After", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat))
	if d := c.retryAfter(&http.Response{Header: h}, 0); d != 5*time.Second {
		t.Errorf("date beyond cap = %v", d)
	}
	h.Set("Retry-After", time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat))
	if d := c.retryAfter(&http.Response{Header: h}, 0); d != 0 {
		t.Errorf("past date = %v", d)
	}
	h.Set("Retry-After", "garbage")
	if d := c.retryAfter(&http.Response{Header: h}, 0); d <= 0 {
		t.Errorf("garbage → backoff, got %v", d)
	}
	c.BaseDelay = time.Hour
	if d := c.backoff(3); d > c.MaxDelay+c.MaxDelay/4 {
		t.Errorf("backoff not capped: %v", d)
	}
	if err := sleepCtx(context.Background(), time.Millisecond); err != nil {
		t.Error(err)
	}
}

func TestErrorMessage(t *testing.T) {
	tests := map[string]string{
		`{"error":{"message":"invalid x-api-key","type":"authentication_error"}}`: "invalid x-api-key",
		`{"error":"model 'llama9' not found"}`:                                    "model 'llama9' not found",
		`{"message":"Not Found"}`:                                                 "Not Found",
		`[{"error":{"code":400,"message":"API key not valid"}}]`:                  "API key not valid",
		"plain text body":        "plain text body",
		strings.Repeat("x", 400): strings.Repeat("x", 300) + "…",
	}
	for in, want := range tests {
		if got := ErrorMessage([]byte(in)); got != want {
			t.Errorf("ErrorMessage(%.40q) = %q, want %q", in, got, want)
		}
	}
}

func TestReadSSE(t *testing.T) {
	stream := ": keepalive\n\nevent: message_start\ndata: {\"a\":1}\n\r\ndata: line1\ndata: line2\n\nevent: ping\n\ndata: [DONE]\n\ndata: after-done"
	var got []string
	err := ReadSSE(strings.NewReader(stream), func(ev, data string) error {
		if data == "[DONE]" {
			return ErrStop
		}
		got = append(got, ev+"|"+data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ";") != `message_start|{"a":1};|line1`+"\n"+`line2` {
		t.Errorf("events = %q", got)
	}
	// Trailing event without blank line is dispatched; callback errors propagate.
	var last string
	_ = ReadSSE(strings.NewReader("data: tail"), func(_, d string) error { last = d; return nil })
	if last != "tail" {
		t.Errorf("tail = %q", last)
	}
	boom := errors.New("boom")
	if err := ReadSSE(strings.NewReader("data: x\n\n"), func(_, _ string) error { return boom }); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}

func TestReadLines(t *testing.T) {
	var got []string
	err := ReadLines(strings.NewReader("{\"a\":1}\n\n  {\"b\":2}  \nstop\nnever"), func(l []byte) error {
		if string(l) == "stop" {
			return ErrStop
		}
		got = append(got, string(l))
		return nil
	})
	if err != nil || strings.Join(got, ",") != `{"a":1},{"b":2}` {
		t.Errorf("got %v err %v", got, err)
	}
}

func TestJSONRequest(t *testing.T) {
	req, err := JSONRequest(context.Background(), "http://x/y", map[string]int{"a": 1}, map[string]string{"X-K": "v"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(req.Body)
	if string(b) != `{"a":1}` || req.Header.Get("X-K") != "v" || req.Header.Get("Content-Type") != "application/json" {
		t.Errorf("req = %s %v", b, req.Header)
	}
	if _, err := JSONRequest(context.Background(), "http://x", make(chan int), nil); err == nil {
		t.Error("unmarshalable body should fail")
	}
	if _, err := JSONRequest(context.Background(), "://bad", 1, nil); err == nil {
		t.Error("bad URL should fail")
	}
}
