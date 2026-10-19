// Package ollama talks to a local Ollama server (POST /api/chat, NDJSON
// streaming). Nothing leaves the machine.
package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/tonklasocute/git-quill/internal/diff"
	"github.com/tonklasocute/git-quill/internal/provider"
	"github.com/tonklasocute/git-quill/internal/provider/httpx"
)

// DefaultBaseURL is where `ollama serve` listens by default.
const DefaultBaseURL = "http://localhost:11434"

// Client implements provider.Provider.
type Client struct {
	opts provider.Options
	http *httpx.Client
	base string
}

// New returns an Ollama client. Base URL precedence: opts.BaseURL, then
// $OLLAMA_HOST (Ollama's own convention), then DefaultBaseURL.
func New(opts provider.Options) (*Client, error) {
	base := opts.BaseURL
	if base == "" {
		base = normalizeHost(os.Getenv("OLLAMA_HOST"))
	}
	if base == "" {
		base = DefaultBaseURL
	}
	return &Client{opts: opts, http: httpx.New(opts), base: strings.TrimRight(base, "/")}, nil
}

func normalizeHost(h string) string {
	h = strings.TrimSpace(h)
	if h == "" {
		return ""
	}
	if !strings.Contains(h, "://") {
		h = "http://" + h
	}
	// 0.0.0.0 is a listen address, not something to connect to.
	return strings.Replace(h, "://0.0.0.0", "://localhost", 1)
}

func (c *Client) Name() string  { return "ollama" }
func (c *Client) Model() string { return c.opts.Model }

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string         `json:"model"`
	Messages []chatMessage  `json:"messages"`
	Stream   bool           `json:"stream"`
	Think    *bool          `json:"think,omitempty"`
	Options  map[string]any `json:"options,omitempty"`
}

type chatChunk struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	Done            bool   `json:"done"`
	DoneReason      string `json:"done_reason"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
	Error           string `json:"error"`
}

// Stream implements provider.Provider.
func (c *Client) Stream(ctx context.Context, req provider.Request, onDelta func(string)) (*provider.Response, error) {
	ctx, cancel := c.opts.WithTimeout(ctx)
	defer cancel()

	body := chatRequest{Model: c.opts.Model, Stream: true, Options: map[string]any{}}
	if req.System != "" {
		body.Messages = append(body.Messages, chatMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		body.Messages = append(body.Messages, chatMessage{Role: string(m.Role), Content: m.Content})
	}
	// Ollama defaults to a small context window and silently truncates the
	// prompt; size it to the request.
	numCtx := 8192
	if v, err := strconv.Atoi(c.opts.Extra["num_ctx"]); err == nil && v > 0 {
		numCtx = v
	}
	body.Options["num_ctx"] = numCtx
	if req.MaxTokens > 0 {
		body.Options["num_predict"] = req.MaxTokens
	}
	// Reasoning models are slow and add nothing for commit messages; turn
	// thinking off unless the user opts back in (think = "default").
	if t := c.opts.Extra["think"]; t != "default" {
		v := t == "true"
		body.Think = &v
	}

	info := provider.HintInfo{Provider: "ollama", Model: c.opts.Model}
	resp, err := c.http.Do(ctx, func(ctx context.Context) (*http.Request, error) {
		return httpx.JSONRequest(ctx, c.base+"/api/chat", body, nil)
	})
	if err != nil {
		e := provider.FromTransport(info, err)
		if e.Kind == provider.KindUnavailable {
			e.Message = "Ollama is not running at " + c.base
			e.Hint = "start it with `ollama serve` (or open the Ollama app); install from https://ollama.com/download"
		}
		return nil, e
	}
	if resp.StatusCode != http.StatusOK {
		e := httpx.ReadError(info, resp)
		if e.Kind == provider.KindNotFound || strings.Contains(e.Message, "not found") {
			e.Kind = provider.KindNotFound
			e.Hint = fmt.Sprintf("download it with `ollama pull %s`", c.opts.Model)
		}
		return nil, e
	}
	defer resp.Body.Close()

	var (
		text strings.Builder
		out  = &provider.Response{Model: c.opts.Model}
	)
	err = httpx.ReadLines(resp.Body, func(line []byte) error {
		var ch chatChunk
		if err := json.Unmarshal(line, &ch); err != nil {
			return fmt.Errorf("ollama: bad stream chunk: %w", err)
		}
		if ch.Error != "" {
			return &provider.Error{Provider: "ollama", Kind: provider.KindServer, Message: ch.Error}
		}
		if ch.Message.Content != "" {
			text.WriteString(ch.Message.Content)
			if onDelta != nil {
				onDelta(ch.Message.Content)
			}
		}
		if ch.Done {
			out.StopReason = ch.DoneReason
			out.Usage = provider.Usage{InputTokens: ch.PromptEvalCount, OutputTokens: ch.EvalCount}
			return httpx.ErrStop
		}
		return nil
	})
	if err != nil {
		if _, ok := err.(*provider.Error); ok { //nolint:errorlint // returned directly from our callback
			return nil, err
		}
		return nil, provider.FromTransport(info, err)
	}
	out.Text = provider.CleanText(text.String())
	if out.Usage.InputTokens == 0 && out.Usage.OutputTokens == 0 {
		out.Usage = estimate(req, out.Text)
	}
	return out, nil
}

func estimate(req provider.Request, text string) provider.Usage {
	in := diff.EstimateTokens(req.System)
	for _, m := range req.Messages {
		in += diff.EstimateTokens(m.Content)
	}
	return provider.Usage{InputTokens: in, OutputTokens: diff.EstimateTokens(text), Estimated: true}
}
