// Package gemini calls the Gemini API (generativelanguage.googleapis.com)
// with server-sent-event streaming.
package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/tonklasocute/git-quill/internal/provider"
	"github.com/tonklasocute/git-quill/internal/provider/httpx"
)

// DefaultBaseURL is the public Gemini API.
const DefaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// minMaxTokens leaves room for thinking on Gemini 3.x models.
const minMaxTokens = 16000

// Client implements provider.Provider.
type Client struct {
	opts provider.Options
	http *httpx.Client
	base string
}

// New returns a Gemini client.
func New(opts provider.Options) (*Client, error) {
	base := opts.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	return &Client{opts: opts, http: httpx.New(opts), base: strings.TrimRight(base, "/")}, nil
}

func (c *Client) Name() string  { return "gemini" }
func (c *Client) Model() string { return c.opts.Model }

type part struct {
	Text    string `json:"text,omitempty"`
	Thought bool   `json:"thought,omitempty"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type chunk struct {
	Candidates []struct {
		Content      content `json:"content"`
		FinishReason string  `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	UsageMetadata *struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		ThoughtsTokenCount   int `json:"thoughtsTokenCount"`
	} `json:"usageMetadata"`
	ModelVersion string `json:"modelVersion"`
}

// Stream implements provider.Provider.
func (c *Client) Stream(ctx context.Context, req provider.Request, onDelta func(string)) (*provider.Response, error) {
	ctx, cancel := c.opts.WithTimeout(ctx)
	defer cancel()

	maxTokens := req.MaxTokens
	if maxTokens < minMaxTokens {
		maxTokens = minMaxTokens
	}
	body := map[string]any{"generationConfig": map[string]any{"maxOutputTokens": maxTokens}}
	if req.System != "" {
		body["systemInstruction"] = content{Parts: []part{{Text: req.System}}}
	}
	var contents []content
	for _, m := range req.Messages {
		role := "user"
		if m.Role == provider.Assistant {
			role = "model"
		}
		contents = append(contents, content{Role: role, Parts: []part{{Text: m.Content}}})
	}
	body["contents"] = contents

	endpoint := fmt.Sprintf("%s/models/%s:streamGenerateContent?alt=sse", c.base, url.PathEscape(c.opts.Model))
	headers := map[string]string{"x-goog-api-key": c.opts.APIKey}
	info := provider.HintInfo{Provider: "gemini", Model: c.opts.Model, KeyEnv: c.opts.KeyEnv}

	resp, err := c.http.Do(ctx, func(ctx context.Context) (*http.Request, error) {
		return httpx.JSONRequest(ctx, endpoint, body, headers)
	})
	if err != nil {
		return nil, provider.FromTransport(info, err)
	}
	if resp.StatusCode != http.StatusOK {
		e := httpx.ReadError(info, resp)
		// Gemini reports a bad key as 400 INVALID_ARGUMENT.
		if strings.Contains(e.Message, "API key not valid") || strings.Contains(e.Message, "API_KEY_INVALID") {
			auth := provider.FromStatus(info, http.StatusUnauthorized, e.Message)
			auth.Status = e.Status
			return nil, auth
		}
		return nil, e
	}
	defer resp.Body.Close()

	out := &provider.Response{Model: c.opts.Model}
	var text strings.Builder
	err = httpx.ReadSSE(resp.Body, func(_, data string) error {
		var ch chunk
		if err := json.Unmarshal([]byte(data), &ch); err != nil {
			return fmt.Errorf("bad stream chunk: %w", err)
		}
		if ch.PromptFeedback != nil && ch.PromptFeedback.BlockReason != "" {
			return &provider.Error{Provider: "gemini", Kind: provider.KindRefusal, Message: "prompt blocked: " + ch.PromptFeedback.BlockReason}
		}
		if ch.ModelVersion != "" {
			out.Model = ch.ModelVersion
		}
		for _, cand := range ch.Candidates {
			for _, p := range cand.Content.Parts {
				if p.Thought || p.Text == "" {
					continue
				}
				text.WriteString(p.Text)
				if onDelta != nil {
					onDelta(p.Text)
				}
			}
			if cand.FinishReason != "" {
				out.StopReason = cand.FinishReason
			}
		}
		if u := ch.UsageMetadata; u != nil {
			out.Usage = provider.Usage{InputTokens: u.PromptTokenCount, OutputTokens: u.CandidatesTokenCount + u.ThoughtsTokenCount}
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
	switch out.StopReason {
	case "SAFETY", "PROHIBITED_CONTENT", "BLOCKLIST", "SPII":
		return nil, &provider.Error{Provider: "gemini", Kind: provider.KindRefusal, Message: "response blocked (" + out.StopReason + ")"}
	case "MAX_TOKENS":
		if out.Text == "" {
			return nil, &provider.Error{Provider: "gemini", Kind: provider.KindBadRequest, Message: "ran out of output tokens before answering",
				Hint: "use a smaller diff or a Flash-Lite model"}
		}
	}
	return out, nil
}
