// Package anthropic calls Claude through the official Go SDK
// (github.com/anthropics/anthropic-sdk-go) with streaming.
package anthropic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/tonklasocute/git-quill/internal/provider"
	"github.com/tonklasocute/git-quill/internal/provider/httpx"
)

// minMaxTokens: current Claude models think adaptively and thinking counts
// toward max_tokens, so a tight cap can leave no room for the answer. You
// are only billed for tokens actually generated.
const minMaxTokens = 16000

// fallbackModels get Anthropic's server-side refusal fallback by default:
// if a safety classifier declines, the request is re-served by an
// appropriate model inside the same call.
var fallbackModels = map[string]bool{
	"claude-fable-5-1": true, "claude-opus-5-5": true, "claude-opus-5": true, "claude-sonnet-5-5": true,
}

// Client implements provider.Provider.
type Client struct {
	opts   provider.Options
	client sdk.Client
}

// New builds a client. With no API key the SDK falls back to its own
// credential chain (ANTHROPIC_AUTH_TOKEN, `ant auth login` profiles).
func New(opts provider.Options) (*Client, error) {
	ro := []option.RequestOption{option.WithMaxRetries(opts.MaxRetries)}
	if opts.APIKey != "" {
		ro = append(ro, option.WithAPIKey(opts.APIKey))
	}
	if opts.BaseURL != "" {
		ro = append(ro, option.WithBaseURL(opts.BaseURL))
	}
	if opts.HTTPClient != nil {
		ro = append(ro, option.WithHTTPClient(opts.HTTPClient))
	}
	return &Client{opts: opts, client: sdk.NewClient(ro...)}, nil
}

func (c *Client) Name() string  { return "anthropic" }
func (c *Client) Model() string { return c.opts.Model }

func (c *Client) info() provider.HintInfo {
	return provider.HintInfo{Provider: "anthropic", Model: c.opts.Model, KeyEnv: c.opts.KeyEnv}
}

// Stream implements provider.Provider.
func (c *Client) Stream(ctx context.Context, req provider.Request, onDelta func(string)) (*provider.Response, error) {
	ctx, cancel := c.opts.WithTimeout(ctx)
	defer cancel()

	maxTokens := req.MaxTokens
	if maxTokens < minMaxTokens {
		maxTokens = minMaxTokens
	}
	params := sdk.MessageNewParams{
		Model:     c.opts.Model,
		MaxTokens: int64(maxTokens),
	}
	if req.System != "" {
		params.System = []sdk.TextBlockParam{{Text: req.System}}
	}
	for _, m := range req.Messages {
		if m.Role == provider.Assistant {
			params.Messages = append(params.Messages, sdk.NewAssistantMessage(sdk.NewTextBlock(m.Content)))
		} else {
			params.Messages = append(params.Messages, sdk.NewUserMessage(sdk.NewTextBlock(m.Content)))
		}
	}
	if e := c.opts.Extra["effort"]; e != "" {
		params.OutputConfig = sdk.OutputConfigParam{Effort: sdk.OutputConfigEffort(e)}
	}

	var ro []option.RequestOption
	if fallbackModels[c.opts.Model] && c.opts.BaseURL == "" {
		ro = append(ro,
			option.WithHeaderAdd("anthropic-beta", "server-side-fallback-2026-07-01"),
			option.WithJSONSet("fallbacks", "default"))
	}
	if c.opts.OnRetry != nil {
		attempt, lastStatus := 0, 0
		ro = append(ro, option.WithMiddleware(func(r *http.Request, next option.MiddlewareNext) (*http.Response, error) {
			if attempt > 0 {
				c.opts.OnRetry(attempt, 0, fmt.Sprintf("HTTP %d", lastStatus))
			}
			attempt++
			resp, err := next(r)
			if resp != nil {
				lastStatus = resp.StatusCode
			}
			return resp, err
		}))
	}

	stream := c.client.Messages.NewStreaming(ctx, params, ro...)
	msg := sdk.Message{}
	var text strings.Builder
	for stream.Next() {
		ev := stream.Current()
		if err := msg.Accumulate(ev); err != nil {
			return nil, &provider.Error{Provider: "anthropic", Kind: provider.KindServer, Message: "bad stream event", Err: err}
		}
		if d, ok := ev.AsAny().(sdk.ContentBlockDeltaEvent); ok {
			if t, ok := d.Delta.AsAny().(sdk.TextDelta); ok && t.Text != "" {
				text.WriteString(t.Text)
				if onDelta != nil {
					onDelta(t.Text)
				}
			}
		}
	}
	if err := stream.Err(); err != nil {
		return nil, c.mapError(err)
	}

	out := &provider.Response{
		Text:       provider.CleanText(text.String()),
		Model:      msg.Model,
		StopReason: string(msg.StopReason),
		Usage:      provider.Usage{InputTokens: int(msg.Usage.InputTokens), OutputTokens: int(msg.Usage.OutputTokens)},
	}
	switch msg.StopReason {
	case sdk.StopReasonRefusal:
		detail := strings.TrimSpace(string(msg.StopDetails.Category))
		if msg.StopDetails.Explanation != "" {
			detail = strings.TrimSpace(detail + " " + msg.StopDetails.Explanation)
		}
		return nil, &provider.Error{Provider: "anthropic", Kind: provider.KindRefusal, Message: "the model declined this request " + detail,
			Hint: "try a different model, or exclude the files that triggered it (.git-quill-ignore)"}
	case sdk.StopReasonMaxTokens:
		if out.Text == "" {
			return nil, &provider.Error{Provider: "anthropic", Kind: provider.KindBadRequest, Message: "ran out of output tokens before answering",
				Hint: "lower `effort` (git quill config set effort low) or use a smaller diff"}
		}
	}
	return out, nil
}

func (c *Client) mapError(err error) error {
	var apiErr *sdk.Error
	if errors.As(err, &apiErr) {
		msg := httpx.ErrorMessage([]byte(apiErr.RawJSON()))
		e := provider.FromStatus(c.info(), apiErr.StatusCode, msg)
		e.Err = err
		if apiErr.StatusCode == http.StatusBadRequest && strings.Contains(msg, "effort") {
			e.Hint = "this model may not support `effort`; clear it with `git quill config set effort \"\"`"
		}
		return e
	}
	return provider.FromTransport(c.info(), err)
}
