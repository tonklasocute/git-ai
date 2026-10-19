// Package config loads git-quill settings from (lowest to highest
// precedence) built-in defaults, the global config file, the repo's
// .git-quill.toml, environment variables and finally CLI flags.
//
// API keys never live in these files: they come from environment variables
// or the OS keychain (see keys.go).
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// RepoFile is the per-repo config file name.
const RepoFile = ".git-quill.toml"

// IgnoreFile holds extra gitignore-style exclude patterns for a repo.
const IgnoreFile = ".git-quill-ignore"

// ProviderConfig holds per-provider settings.
type ProviderConfig struct {
	Model          string `toml:"model,omitempty"`
	BaseURL        string `toml:"base_url,omitempty"`
	MaxInputTokens int    `toml:"max_input_tokens,omitempty"`
	// Effort is Anthropic's output_config.effort ("" to omit).
	Effort *string `toml:"effort,omitempty"`
	// NumCtx is Ollama's context window.
	NumCtx int `toml:"num_ctx,omitempty"`
	// Think controls Ollama reasoning: "false" (default), "true" or "default".
	Think string `toml:"think,omitempty"`
}

// Config is the merged configuration.
type Config struct {
	Provider         string                    `toml:"provider"`
	Lang             string                    `toml:"lang"`
	Style            string                    `toml:"style"`
	MaxSubjectLength int                       `toml:"max_subject_length"`
	Emoji            bool                      `toml:"emoji"`
	Prompt           string                    `toml:"prompt,omitempty"`
	Ignore           []string                  `toml:"ignore,omitempty"`
	Timeout          Duration                  `toml:"timeout"`
	MaxRetries       int                       `toml:"max_retries"`
	Providers        map[string]ProviderConfig `toml:"providers,omitempty"`

	// Warnings collected while loading (unknown keys, refused repo keys).
	Warnings []string `toml:"-"`
	// Sources lists the files that were read, lowest precedence first.
	Sources []string `toml:"-"`
}

// Duration is a time.Duration that (un)marshals as "90s"/"2m".
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return fmt.Errorf("invalid duration %q (use e.g. \"90s\" or \"2m\")", b)
	}
	d.Duration = v
	return nil
}

func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// Default returns the built-in configuration.
func Default() *Config {
	return &Config{
		Provider:         "anthropic",
		Lang:             "en",
		Style:            "conventional",
		MaxSubjectLength: 72,
		Timeout:          Duration{2 * time.Minute},
		MaxRetries:       3,
		Providers:        map[string]ProviderConfig{},
	}
}

// GlobalPath is $GIT_QUILL_CONFIG, else $XDG_CONFIG_HOME/git-quill/config.toml,
// else ~/.config/git-quill/config.toml (on every OS, so docs stay simple).
func GlobalPath() (string, error) {
	if p := os.Getenv("GIT_QUILL_CONFIG"); p != "" {
		return p, nil
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "git-quill", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "git-quill", "config.toml"), nil
}

// Load reads global and repo config (repoRoot may be "") and applies env
// overrides. A missing file is not an error.
func Load(repoRoot string) (*Config, error) {
	merged := map[string]any{}
	cfg := Default()

	global, err := GlobalPath()
	if err != nil {
		return nil, err
	}
	gm, err := readFile(global)
	if err != nil {
		return nil, err
	}
	if gm != nil {
		cfg.Sources = append(cfg.Sources, global)
		mergeMaps(merged, gm)
	}

	if repoRoot != "" {
		rp := filepath.Join(repoRoot, RepoFile)
		rm, err := readFile(rp)
		if err != nil {
			return nil, err
		}
		if rm != nil {
			cfg.Sources = append(cfg.Sources, rp)
			for _, k := range dropForbidden(rm) {
				cfg.Warnings = append(cfg.Warnings,
					fmt.Sprintf("%s: ignoring %q (repo config may not change the provider or endpoint; set it in %s)", RepoFile, k, global))
			}
			mergeMaps(merged, rm)
		}
	}

	if err := decodeMap(merged, cfg); err != nil {
		return nil, err
	}
	cfg.applyEnv()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func readFile(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	if _, err := toml.Decode(string(b), &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// dropForbidden removes keys a repository may not set (provider and any
// providers.*.base_url): a cloned repo must not be able to redirect your diff
// and API key to a different endpoint. It returns the dropped keys.
func dropForbidden(m map[string]any) []string {
	var dropped []string
	if _, ok := m["provider"]; ok {
		delete(m, "provider")
		dropped = append(dropped, "provider")
	}
	if ps, ok := m["providers"].(map[string]any); ok {
		names := make([]string, 0, len(ps))
		for n := range ps {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			if pc, ok := ps[name].(map[string]any); ok {
				if _, ok := pc["base_url"]; ok {
					delete(pc, "base_url")
					dropped = append(dropped, "providers."+name+".base_url")
				}
			}
		}
	}
	return dropped
}

func mergeMaps(dst, src map[string]any) {
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				mergeMaps(dm, sm)
				continue
			}
			cp := map[string]any{}
			mergeMaps(cp, sm)
			dst[k] = cp
			continue
		}
		dst[k] = v
	}
}

func decodeMap(m map[string]any, cfg *Config) error {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(m); err != nil {
		return err
	}
	md, err := toml.Decode(buf.String(), cfg)
	if err != nil {
		return err
	}
	for _, k := range md.Undecoded() {
		key := k.String()
		if strings.Contains(strings.ToLower(key), "api_key") || strings.HasSuffix(strings.ToLower(key), "key") {
			cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("ignoring %q: API keys are never read from config files; use an env var or `git quill config set-key`", key))
			continue
		}
		cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("unknown config key %q", key))
	}
	if cfg.Providers == nil {
		cfg.Providers = map[string]ProviderConfig{}
	}
	return nil
}

func (c *Config) applyEnv() {
	if v := os.Getenv("GIT_QUILL_PROVIDER"); v != "" {
		c.Provider = v
	}
	if v := os.Getenv("GIT_QUILL_MODEL"); v != "" {
		pc := c.Providers[c.Provider]
		pc.Model = v
		c.Providers[c.Provider] = pc
	}
	if v := os.Getenv("GIT_QUILL_LANG"); v != "" {
		c.Lang = v
	}
}

// Validate checks enumerations and ranges.
func (c *Config) Validate() error {
	if _, ok := Lookup(c.Provider); !ok {
		return fmt.Errorf("unknown provider %q (choose one of: %s)", c.Provider, strings.Join(ProviderNames(), ", "))
	}
	switch c.Lang {
	case "en", "th":
	default:
		return fmt.Errorf("unsupported lang %q (use \"en\" or \"th\")", c.Lang)
	}
	switch c.Style {
	case "conventional", "simple":
	default:
		return fmt.Errorf("unsupported style %q (use \"conventional\" or \"simple\")", c.Style)
	}
	if c.MaxSubjectLength < 20 || c.MaxSubjectLength > 200 {
		return fmt.Errorf("max_subject_length %d out of range (20-200)", c.MaxSubjectLength)
	}
	if c.MaxRetries < 0 || c.MaxRetries > 10 {
		return fmt.Errorf("max_retries %d out of range (0-10)", c.MaxRetries)
	}
	if c.Timeout.Duration <= 0 {
		return errors.New("timeout must be positive")
	}
	return nil
}

// Active returns the settings for the selected provider with defaults
// filled in.
func (c *Config) Active() (ProviderInfo, ProviderConfig) {
	info, _ := Lookup(c.Provider)
	pc := c.Providers[c.Provider]
	if pc.Model == "" {
		pc.Model = info.DefaultModel
	}
	if pc.BaseURL == "" {
		pc.BaseURL = info.DefaultBaseURL
	}
	if pc.MaxInputTokens <= 0 {
		pc.MaxInputTokens = info.DefaultBudget
	}
	if pc.Effort == nil && info.Name == "anthropic" {
		low := "low"
		pc.Effort = &low
	}
	return info, pc
}

// Keys lists the settable keys for `config get/set`.
var Keys = []string{
	"provider", "model", "lang", "style", "max_subject_length", "emoji", "prompt", "ignore",
	"timeout", "max_retries", "base_url", "max_input_tokens", "effort", "num_ctx", "think",
}

var providerScoped = map[string]bool{"model": true, "base_url": true, "max_input_tokens": true, "effort": true, "num_ctx": true, "think": true}

// Get returns the effective value of key as a string.
func (c *Config) Get(key string) (string, error) {
	_, pc := c.Active()
	switch key {
	case "provider":
		return c.Provider, nil
	case "lang":
		return c.Lang, nil
	case "style":
		return c.Style, nil
	case "max_subject_length":
		return strconv.Itoa(c.MaxSubjectLength), nil
	case "emoji":
		return strconv.FormatBool(c.Emoji), nil
	case "prompt":
		return c.Prompt, nil
	case "ignore":
		return strings.Join(c.Ignore, ","), nil
	case "timeout":
		return c.Timeout.String(), nil
	case "max_retries":
		return strconv.Itoa(c.MaxRetries), nil
	case "model":
		return pc.Model, nil
	case "base_url":
		return pc.BaseURL, nil
	case "max_input_tokens":
		return strconv.Itoa(pc.MaxInputTokens), nil
	case "effort":
		if pc.Effort == nil {
			return "", nil
		}
		return *pc.Effort, nil
	case "num_ctx":
		return strconv.Itoa(pc.NumCtx), nil
	case "think":
		return pc.Think, nil
	}
	return "", fmt.Errorf("unknown key %q (valid: %s)", key, strings.Join(Keys, ", "))
}

// Set writes key=value into the config file at path (creating it), scoping
// provider-specific keys to provider. It validates before writing.
func Set(path, provider, key, value string) error {
	m, err := readFile(path)
	if err != nil {
		return err
	}
	if m == nil {
		m = map[string]any{}
	}
	var v any = value
	switch key {
	case "max_subject_length", "max_retries", "max_input_tokens", "num_ctx":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("%s must be a number", key)
		}
		v = int64(n)
	case "emoji":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("%s must be true or false", key)
		}
		v = b
	case "ignore":
		var pats []any
		for _, p := range strings.Split(value, ",") {
			if p = strings.TrimSpace(p); p != "" {
				pats = append(pats, p)
			}
		}
		v = pats
	case "timeout":
		if _, err := time.ParseDuration(value); err != nil {
			return fmt.Errorf("timeout must be a duration like 90s or 2m")
		}
	case "provider", "lang", "style", "prompt", "model", "base_url", "effort", "think":
	default:
		return fmt.Errorf("unknown key %q (valid: %s)", key, strings.Join(Keys, ", "))
	}
	if providerScoped[key] {
		ps, _ := m["providers"].(map[string]any)
		if ps == nil {
			ps = map[string]any{}
			m["providers"] = ps
		}
		pc, _ := ps[provider].(map[string]any)
		if pc == nil {
			pc = map[string]any{}
			ps[provider] = pc
		}
		pc[key] = v
	} else {
		m[key] = v
	}

	// Validate the result before touching the file.
	check := Default()
	if err := decodeMap(m, check); err != nil {
		return err
	}
	if err := check.Validate(); err != nil {
		return err
	}

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(m); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// List returns "key = value" lines for every key.
func (c *Config) List() []string {
	out := make([]string, 0, len(Keys))
	for _, k := range Keys {
		v, _ := c.Get(k)
		out = append(out, fmt.Sprintf("%s = %s", k, v))
	}
	return out
}
