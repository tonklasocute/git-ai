// Package openai implements the Chat Completions streaming API used by
// OpenAI and by OpenAI-compatible servers (OpenRouter, Groq, LM Studio,
// vLLM, ...).
package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/tonklasocute/git-quill/internal/diff"
	"github.com/tonklasocute/git-quill/internal/provider"
	"github.com/tonklasocute/git-quill/internal/provider/httpx"
)

// minMaxTokens leaves room for reasoning tokens, which count toward the
// completion limit on reasoning models.
const minMaxTokens = 16000

// Client implements provider.Provider.
type Client struct {
	name string
	opts provider.Options
	http *httpx.Client
	base string
}

// New returns a client for the provider called name (used in messages).
func New(name string, opts provider.Options) (*Client, error) {
	if opts.BaseURL == "" {
		return nil, fmt.Errorf("%s: base_url is required", name)
	}
	return &Client{name: name, opts: opts, http: httpx.New(opts), base: strings.TrimRight(opts.BaseURL, "/")}, nil
}

func (c *Client) Name() string  { return c.name }
func (c *Client) Model() string { return c.opts.Model }

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Model string          `json:"model"`
	Error json.RawMessage `json:"error"`
}

// Stream implements provider.Provider.
func (c *Client) Stream(ctx context.Context, req provider.Request, onDelta func(string)) (*provider.Response, error) {
	ctx, cancel := c.opts.WithTimeout(ctx)
	defer cancel()

	body := map[string]any{
		"model":          c.opts.Model,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	var msgs []message
	if req.System != "" {
		msgs = append(msgs, message{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		msgs = append(msgs, message{Role: string(m.Role), Content: m.Content})
	}
	body["messages"] = msgs
	maxTokens := req.MaxTokens
	if maxTokens < minMaxTokens {
		maxTokens = minMaxTokens
	}
	// OpenAI's own API wants max_completion_tokens (max_tokens is rejected by
	// reasoning models); most compatible servers only know max_tokens.
	if c.name == "openai" {
		body["max_completion_tokens"] = maxTokens
	} else {
		body["max_tokens"] = maxTokens
	}

	headers := map[string]string{"Accept": "text/event-stream"}
	if c.opts.APIKey != "" {
		headers["Authorization"] = "Bearer " + c.opts.APIKey
	}
	if strings.Contains(c.base, "openrouter.ai") {
		headers["HTTP-Referer"] = "https://github.com/tonklasocute/git-quill"
		headers["X-Title"] = "git-quill"
	}

	info := provider.HintInfo{Provider: c.name, Model: c.opts.Model, KeyEnv: c.opts.KeyEnv}
	resp, err := c.http.Do(ctx, func(ctx context.Context) (*http.Request, error) {
		return httpx.JSONRequest(ctx, c.base+"/chat/completions", body, headers)
	})
	if err != nil {
		e := provider.FromTransport(info, err)
		if e.Kind == provider.KindUnavailable && provider.IsConnRefused(err) {
			e.Hint = fmt.Sprintf("nothing is listening at %s; is the server running? (LM Studio: start the local server)", c.base)
		}
		return nil, e
	}
	if resp.StatusCode != http.StatusOK {
		return nil, httpx.ReadError(info, resp)
	}
	defer resp.Body.Close()

	out := &provider.Response{Model: c.opts.Model}
	var text strings.Builder
	err = httpx.ReadSSE(resp.Body, func(_, data string) error {
		if data == "[DONE]" {
			return httpx.ErrStop
		}
		var ch chunk
		if err := json.Unmarshal([]byte(data), &ch); err != nil {
			return fmt.Errorf("bad stream chunk: %w", err)
		}
		if len(ch.Error) > 0 && string(ch.Error) != "null" {
			return &provider.Error{Provider: c.name, Kind: provider.KindServer, Message: httpx.ErrorMessage([]byte(`{"error":` + string(ch.Error) + `}`))}
		}
		if ch.Model != "" {
			out.Model = ch.Model
		}
		for _, choice := range ch.Choices {
			if s := choice.Delta.Content; s != "" {
				text.WriteString(s)
				if onDelta != nil {
					onDelta(s)
				}
			}
			if choice.FinishReason != "" {
				out.StopReason = choice.FinishReason
			}
		}
		if ch.Usage != nil {
			out.Usage = provider.Usage{InputTokens: ch.Usage.PromptTokens, OutputTokens: ch.Usage.CompletionTokens}
		}
		return nil
	})
	if err != nil {
		if pe, ok := err.(*provider.Error); ok { //nolint:errorlint // returned directly from our callback
			return nil, pe
		}
		return nil, provider.FromTransport(info, err)
	}
	out.Text = provider.CleanText(text.String())
	switch {
	case out.StopReason == "content_filter":
		return nil, &provider.Error{Provider: c.name, Kind: provider.KindRefusal, Message: "the response was blocked by the provider's content filter"}
	case out.StopReason == "length" && out.Text == "":
		return nil, &provider.Error{Provider: c.name, Kind: provider.KindBadRequest, Message: "ran out of output tokens before answering",
			Hint: "use a smaller diff or a non-reasoning model"}
	}
	if out.Usage.InputTokens == 0 && out.Usage.OutputTokens == 0 {
		in := diff.EstimateTokens(req.System)
		for _, m := range req.Messages {
			in += diff.EstimateTokens(m.Content)
		}
		out.Usage = provider.Usage{InputTokens: in, OutputTokens: diff.EstimateTokens(out.Text), Estimated: true}
	}
	return out, nil
}
