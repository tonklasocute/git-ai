package provider

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"syscall"
)

// Kind classifies failures so the CLI can say how to fix them.
type Kind int

const (
	KindUnknown Kind = iota
	KindAuth
	KindPermission
	KindNotFound
	KindRateLimit
	KindServer
	KindUnavailable
	KindTimeout
	KindBadRequest
	KindRefusal
	KindCanceled
)

func (k Kind) String() string {
	return [...]string{"error", "invalid API key", "permission denied", "model not found", "rate limited",
		"server error", "service unavailable", "timed out", "bad request", "refused", "canceled"}[k]
}

// Error is returned by every provider.
type Error struct {
	Provider string
	Kind     Kind
	Status   int
	Message  string
	Hint     string
	Err      error
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s", e.Provider, e.Kind)
	if e.Status != 0 {
		fmt.Fprintf(&b, " (HTTP %d)", e.Status)
	}
	if e.Message != "" {
		b.WriteString(": " + e.Message)
	}
	if e.Hint != "" {
		b.WriteString("\n  hint: " + e.Hint)
	}
	return b.String()
}

func (e *Error) Unwrap() error { return e.Err }

// Retryable reports whether a later attempt might succeed.
func (e *Error) Retryable() bool {
	return e.Kind == KindRateLimit || e.Kind == KindServer
}

// HintInfo is what hints need to be specific.
type HintInfo struct {
	Provider string
	Model    string
	KeyEnv   string
}

// FromStatus builds an Error from an HTTP status and the API's message.
func FromStatus(info HintInfo, status int, msg string) *Error {
	e := &Error{Provider: info.Provider, Status: status, Message: msg}
	switch {
	case status == 401:
		e.Kind = KindAuth
		e.Hint = keyHint(info)
	case status == 403:
		e.Kind = KindPermission
		e.Hint = "your key may lack access to this model or the account needs billing enabled; " + keyHint(info)
	case status == 404:
		e.Kind = KindNotFound
		e.Hint = fmt.Sprintf("check the model id %q for %s (`git quill config set model <id>`) and base_url", info.Model, info.Provider)
	case status == 429:
		e.Kind = KindRateLimit
		e.Hint = "rate limit or quota exceeded; wait a moment, or check your plan's limits and billing"
	case status == 408 || status == 504:
		e.Kind = KindTimeout
		e.Hint = "the request timed out; try again, or raise `timeout` in config"
	case status >= 500:
		e.Kind = KindServer
		e.Hint = "the provider is having trouble; try again shortly"
	case status == 400 || status == 413 || status == 422:
		e.Kind = KindBadRequest
		if strings.Contains(strings.ToLower(msg), "model") {
			e.Hint = fmt.Sprintf("the model %q may not exist or may not support this request", info.Model)
		} else if strings.Contains(strings.ToLower(msg), "token") || status == 413 {
			e.Hint = "the prompt may be too large; lower `max_input_tokens` in config"
		}
	default:
		e.Kind = KindUnknown
	}
	return e
}

func keyHint(info HintInfo) string {
	if info.KeyEnv == "" {
		return "check the API key for " + info.Provider
	}
	return fmt.Sprintf("set %s or run `git quill config set-key %s`", info.KeyEnv, info.Provider)
}

// FromTransport classifies a network-level error (no HTTP response).
func FromTransport(info HintInfo, err error) *Error {
	e := &Error{Provider: info.Provider, Err: err, Message: err.Error()}
	switch {
	case errors.Is(err, context.Canceled):
		e.Kind = KindCanceled
		e.Message = ""
	case errors.Is(err, context.DeadlineExceeded) || isTimeout(err):
		e.Kind = KindTimeout
		e.Message = ""
		e.Hint = "no response in time; try again, or raise `timeout` in config (local models can be slow on first load)"
	case IsConnRefused(err):
		e.Kind = KindUnavailable
		e.Hint = "nothing is listening at the configured base_url"
	default:
		e.Kind = KindUnavailable
		e.Hint = "check your network connection and base_url"
	}
	return e
}

// IsConnRefused reports whether err is a refused TCP connection.
func IsConnRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || strings.Contains(err.Error(), "connection refused")
}

func isTimeout(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	var ue *url.Error
	return errors.As(err, &ue) && ue.Timeout()
}
