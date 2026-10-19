package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/tonklasocute/git-quill/internal/config"
	"github.com/tonklasocute/git-quill/internal/provider"
	"github.com/tonklasocute/git-quill/internal/provider/anthropic"
	"github.com/tonklasocute/git-quill/internal/provider/fake"
	"github.com/tonklasocute/git-quill/internal/provider/gemini"
	"github.com/tonklasocute/git-quill/internal/provider/ollama"
	"github.com/tonklasocute/git-quill/internal/provider/openai"
)

// NewProvider builds the client for the configured provider, resolving the
// API key from the environment or OS keychain.
func NewProvider(cfg *config.Config, onRetry func(int, time.Duration, string)) (provider.Provider, error) {
	info, pc := cfg.Active()
	if pc.Model == "" {
		return nil, fmt.Errorf("no model set for %s; pick one with `git quill config set model <id>` (see the provider's model list)", info.Name)
	}
	if pc.BaseURL == "" && info.Kind == "openai" {
		return nil, fmt.Errorf("%s needs a base_url; set it with `git quill config set base_url https://host/v1`", info.Name)
	}
	key, _, _ := config.APIKey(info)
	if info.NeedsKey && key == "" && (info.Kind != "anthropic" || !anthropicAmbientAuth()) {
		return nil, config.MissingKeyError(info)
	}
	opts := provider.Options{
		Model:      pc.Model,
		APIKey:     key,
		BaseURL:    pc.BaseURL,
		Timeout:    cfg.Timeout.Duration,
		MaxRetries: cfg.MaxRetries,
		OnRetry:    onRetry,
		Extra:      map[string]string{},
	}
	if len(info.KeyEnv) > 0 {
		opts.KeyEnv = info.KeyEnv[0]
	}
	if pc.Effort != nil {
		opts.Extra["effort"] = *pc.Effort
	}
	if pc.NumCtx > 0 {
		opts.Extra["num_ctx"] = strconv.Itoa(pc.NumCtx)
	} else if info.Kind == "ollama" {
		// Room for the input budget plus the answer.
		opts.Extra["num_ctx"] = strconv.Itoa(pc.MaxInputTokens + 2048)
	}
	if pc.Think != "" {
		opts.Extra["think"] = pc.Think
	}

	switch info.Kind {
	case "fake":
		return fake.New(opts), nil
	case "ollama":
		return ollama.New(opts)
	case "anthropic":
		return anthropic.New(opts)
	case "openai":
		return openai.New(info.Name, opts)
	case "gemini":
		return gemini.New(opts)
	}
	return nil, fmt.Errorf("provider %q is not supported in this build", info.Name)
}

// anthropicAmbientAuth reports whether the Anthropic SDK can find
// credentials without an API key (ANTHROPIC_AUTH_TOKEN or an `ant auth
// login` profile).
func anthropicAmbientAuth() bool {
	if os.Getenv("ANTHROPIC_AUTH_TOKEN") != "" || os.Getenv("ANTHROPIC_PROFILE") != "" {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, ".config", "anthropic"))
	return err == nil
}
