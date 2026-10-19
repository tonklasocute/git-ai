package provider

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestFromStatus(t *testing.T) {
	info := HintInfo{Provider: "openai", Model: "gpt-x", KeyEnv: "OPENAI_API_KEY"}
	tests := []struct {
		status int
		msg    string
		kind   Kind
		hint   string
	}{
		{401, "bad key", KindAuth, "set OPENAI_API_KEY or run `git quill config set-key openai`"},
		{403, "", KindPermission, "billing"},
		{404, "", KindNotFound, `check the model id "gpt-x"`},
		{429, "", KindRateLimit, "rate limit"},
		{500, "", KindServer, "try again"},
		{529, "", KindServer, "try again"},
		{504, "", KindTimeout, "timed out"},
		{400, "unknown model", KindBadRequest, "may not exist"},
		{400, "too many tokens", KindBadRequest, "max_input_tokens"},
		{413, "", KindBadRequest, "max_input_tokens"},
		{418, "", KindUnknown, ""},
	}
	for _, tt := range tests {
		e := FromStatus(info, tt.status, tt.msg)
		if e.Kind != tt.kind || !strings.Contains(e.Hint, tt.hint) {
			t.Errorf("%d: kind=%v hint=%q", tt.status, e.Kind, e.Hint)
		}
	}
	e := FromStatus(info, 401, "bad key")
	if got := e.Error(); got != "openai: invalid API key (HTTP 401): bad key\n  hint: set OPENAI_API_KEY or run `git quill config set-key openai`" {
		t.Errorf("Error() = %q", got)
	}
	if !FromStatus(info, 429, "").Retryable() || FromStatus(info, 401, "").Retryable() {
		t.Error("Retryable")
	}
	if h := FromStatus(HintInfo{Provider: "x"}, 401, "").Hint; h != "check the API key for x" {
		t.Errorf("no KeyEnv hint = %q", h)
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

var _ net.Error = timeoutErr{}

func TestFromTransport(t *testing.T) {
	info := HintInfo{Provider: "ollama"}
	tests := []struct {
		err  error
		kind Kind
	}{
		{context.Canceled, KindCanceled},
		{fmt.Errorf("wrap: %w", context.DeadlineExceeded), KindTimeout},
		{timeoutErr{}, KindTimeout},
		{&net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, KindUnavailable},
		{errors.New("no such host"), KindUnavailable},
	}
	for _, tt := range tests {
		e := FromTransport(info, tt.err)
		if e.Kind != tt.kind {
			t.Errorf("%v → %v, want %v", tt.err, e.Kind, tt.kind)
		}
		if !errors.Is(e, tt.err) {
			t.Errorf("Unwrap lost %v", tt.err)
		}
	}
	if !strings.Contains(FromTransport(info, &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}).Hint, "nothing is listening") {
		t.Error("conn refused hint")
	}
	if KindRefusal.String() != "refused" {
		t.Error("Kind.String")
	}
}

func TestUsageAndHelpers(t *testing.T) {
	u := Usage{InputTokens: 1, OutputTokens: 2}
	u.Add(Usage{InputTokens: 10, OutputTokens: 20, Estimated: true})
	if u.InputTokens != 11 || u.OutputTokens != 22 || !u.Estimated {
		t.Errorf("Add = %+v", u)
	}
	if got := CleanText("<think>\nhmm\n</think>\n  feat: x \n"); got != "feat: x" {
		t.Errorf("CleanText = %q", got)
	}
	ctx, cancel := Options{Timeout: time.Millisecond}.WithTimeout(context.Background())
	defer cancel()
	<-ctx.Done()
	ctx2, cancel2 := Options{}.WithTimeout(context.Background())
	cancel2()
	if ctx2.Err() == nil {
		t.Error("cancel func not wired")
	}
}

func TestIsConnRefusedWindowsText(t *testing.T) {
	err := errors.New("dial tcp 127.0.0.1:11434: connectex: No connection could be made because the target machine actively refused it.")
	if !IsConnRefused(err) {
		t.Error("Windows refusal text not recognised")
	}
}
