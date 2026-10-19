// Package provider defines the interface every model backend implements,
// plus the shared error type that turns HTTP failures into actionable
// messages.
package provider

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Role of a chat message.
type Role string

const (
	User      Role = "user"
	Assistant Role = "assistant"
)

// Message is one chat turn.
type Message struct {
	Role    Role
	Content string
}

// Task tells the fake provider (and verbose logs) what a request is for. It
// is never sent to a real API.
type Task string

const (
	TaskCommit    Task = "commit"
	TaskPR        Task = "pr"
	TaskChangelog Task = "changelog"
	TaskReview    Task = "review"
	TaskSummarize Task = "summarize"
)

// Request is a provider-neutral chat request.
type Request struct {
	Task      Task
	System    string
	Messages  []Message
	MaxTokens int
}

// Usage is token accounting for one call.
type Usage struct {
	InputTokens  int
	OutputTokens int
	// Estimated is true when the provider did not report usage and the
	// numbers come from a local estimate.
	Estimated bool
}

// Add accumulates u2 into u.
func (u *Usage) Add(u2 Usage) {
	u.InputTokens += u2.InputTokens
	u.OutputTokens += u2.OutputTokens
	u.Estimated = u.Estimated || u2.Estimated
}

// Response is a completed generation.
type Response struct {
	Text       string
	Model      string
	StopReason string
	Usage      Usage
}

// Provider is a model backend. Stream calls onDelta (if non-nil) with text
// as it arrives and returns the full response at the end.
type Provider interface {
	Name() string
	Model() string
	Stream(ctx context.Context, req Request, onDelta func(string)) (*Response, error)
}

// Options configure a provider client.
type Options struct {
	Model      string
	APIKey     string
	BaseURL    string
	Timeout    time.Duration
	MaxRetries int
	HTTPClient *http.Client
	// KeyEnv is the env var users should set; used in error hints.
	KeyEnv string
	// Extra holds provider-specific settings (e.g. anthropic effort,
	// ollama num_ctx).
	Extra map[string]string
	// OnRetry is called before each retry (for --verbose).
	OnRetry func(attempt int, wait time.Duration, reason string)
}

// WithTimeout applies opts.Timeout to ctx when set.
func (o Options) WithTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if o.Timeout > 0 {
		return context.WithTimeout(ctx, o.Timeout)
	}
	return context.WithCancel(ctx)
}

var thinkRe = regexp.MustCompile(`(?s)<think>.*?</think>\s*`)

// CleanText strips reasoning tags some local models emit inline and trims
// surrounding whitespace.
func CleanText(s string) string {
	return strings.TrimSpace(thinkRe.ReplaceAllString(s, ""))
}
