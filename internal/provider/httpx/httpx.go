// Package httpx is the shared HTTP plumbing for providers that do not ship
// an official Go SDK: retries with exponential backoff on 429/5xx (honouring
// Retry-After), SSE and NDJSON stream readers, and error-body parsing.
package httpx

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tonklasocute/git-quill/internal/provider"
)

// Client wraps http.Client with retries.
type Client struct {
	HTTP       *http.Client
	MaxRetries int
	BaseDelay  time.Duration
	MaxDelay   time.Duration
	// Sleep waits for d or until ctx is done; replaceable in tests.
	Sleep   func(ctx context.Context, d time.Duration) error
	OnRetry func(attempt int, wait time.Duration, reason string)
}

// New returns a Client configured from provider options.
func New(opts provider.Options) *Client {
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{}
	}
	return &Client{
		HTTP:       hc,
		MaxRetries: opts.MaxRetries,
		BaseDelay:  500 * time.Millisecond,
		MaxDelay:   20 * time.Second,
		Sleep:      sleepCtx,
		OnRetry:    opts.OnRetry,
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// RetryableStatus reports whether status is worth retrying.
func RetryableStatus(status int) bool {
	switch status {
	case 408, 409, 429, 500, 502, 503, 504, 529:
		return true
	}
	return false
}

// Do sends the request built by newReq, retrying on retryable statuses and
// transient network errors. A non-2xx response that is not retried (or that
// is still failing after the last retry) is returned with a nil error; the
// caller owns resp.Body.
func (c *Client) Do(ctx context.Context, newReq func(ctx context.Context) (*http.Request, error)) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		req, err := newReq(ctx)
		if err != nil {
			return nil, err
		}
		resp, err := c.HTTP.Do(req)
		var (
			wait   time.Duration
			reason string
		)
		switch {
		case err != nil:
			if ctx.Err() != nil || provider.IsConnRefused(err) || attempt >= c.MaxRetries {
				return nil, err
			}
			reason = "network error: " + err.Error()
			wait = c.backoff(attempt)
		case RetryableStatus(resp.StatusCode) && attempt < c.MaxRetries:
			reason = fmt.Sprintf("HTTP %d", resp.StatusCode)
			wait = c.retryAfter(resp, attempt)
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
		default:
			return resp, nil
		}
		if c.OnRetry != nil {
			c.OnRetry(attempt+1, wait, reason)
		}
		if err := c.Sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
}

func (c *Client) backoff(attempt int) time.Duration {
	d := c.BaseDelay << attempt
	if d > c.MaxDelay || d <= 0 {
		d = c.MaxDelay
	}
	// ±25% jitter so many clients do not retry in lockstep.
	jitter := time.Duration(rand.Int64N(int64(d)/2+1)) - d/4
	return d + jitter
}

func (c *Client) retryAfter(resp *http.Response, attempt int) time.Duration {
	if ms := resp.Header.Get("retry-after-ms"); ms != "" {
		if n, err := strconv.ParseFloat(ms, 64); err == nil && n >= 0 {
			return c.capDelay(time.Duration(n * float64(time.Millisecond)))
		}
	}
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if n, err := strconv.ParseFloat(ra, 64); err == nil && n >= 0 {
			return c.capDelay(time.Duration(n * float64(time.Second)))
		}
		if t, err := http.ParseTime(ra); err == nil {
			return c.capDelay(time.Until(t))
		}
	}
	return c.backoff(attempt)
}

func (c *Client) capDelay(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	if d > c.MaxDelay {
		return c.MaxDelay
	}
	return d
}

// JSONRequest builds a POST with a JSON body.
func JSONRequest(ctx context.Context, url string, body any, headers map[string]string) (*http.Request, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req, nil
}

// ErrorMessage extracts a human message from an API error body. It
// understands the common shapes:
//
//	{"error":{"message":"..."}}  {"error":"..."}  {"message":"..."}  [{"error":{...}}]
func ErrorMessage(body []byte) string {
	body = bytes.TrimSpace(body)
	if bytes.HasPrefix(body, []byte("[")) {
		var arr []json.RawMessage
		if json.Unmarshal(body, &arr) == nil && len(arr) > 0 {
			return ErrorMessage(arr[0])
		}
	}
	var v struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(body, &v) == nil {
		if len(v.Error) > 0 {
			var s string
			if json.Unmarshal(v.Error, &s) == nil {
				return s
			}
			var inner struct {
				Message string `json:"message"`
			}
			if json.Unmarshal(v.Error, &inner) == nil && inner.Message != "" {
				return inner.Message
			}
		}
		if v.Message != "" {
			return v.Message
		}
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// ReadError consumes resp.Body and returns a classified provider error.
func ReadError(info provider.HintInfo, resp *http.Response) *provider.Error {
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return provider.FromStatus(info, resp.StatusCode, ErrorMessage(b))
}

// ReadSSE parses a text/event-stream, calling fn for each event. Multiple
// data lines are joined with "\n". fn returning ErrStop ends reading early.
func ReadSSE(r io.Reader, fn func(event, data string) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	var (
		event string
		data  []string
	)
	dispatch := func() error {
		if len(data) == 0 {
			event = ""
			return nil
		}
		err := fn(event, strings.Join(data, "\n"))
		event, data = "", nil
		return err
	}
	for sc.Scan() {
		line := strings.TrimSuffix(sc.Text(), "\r")
		switch {
		case line == "":
			if err := dispatch(); err != nil {
				return stopOK(err)
			}
		case strings.HasPrefix(line, ":"):
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return stopOK(dispatch())
}

// ReadLines calls fn for each non-empty line (NDJSON streams).
func ReadLines(r io.Reader, fn func(line []byte) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if err := fn(line); err != nil {
			return stopOK(err)
		}
	}
	return sc.Err()
}

// ErrStop ends a stream read without error.
var ErrStop = errors.New("stop")

func stopOK(err error) error {
	if errors.Is(err, ErrStop) {
		return nil
	}
	return err
}
