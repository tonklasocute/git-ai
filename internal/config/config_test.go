package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

func setup(t *testing.T, global, repo string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	gp := filepath.Join(dir, "global", "config.toml")
	t.Setenv("GIT_QUILL_CONFIG", gp)
	for _, k := range []string{"GIT_QUILL_PROVIDER", "GIT_QUILL_MODEL", "GIT_QUILL_LANG"} {
		t.Setenv(k, "")
	}
	if global != "" {
		_ = os.MkdirAll(filepath.Dir(gp), 0o755)
		if err := os.WriteFile(gp, []byte(global), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root := filepath.Join(dir, "repo")
	_ = os.MkdirAll(root, 0o755)
	if repo != "" {
		if err := os.WriteFile(filepath.Join(root, RepoFile), []byte(repo), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return gp, root
}

func TestDefaults(t *testing.T) {
	_, root := setup(t, "", "")
	c, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	info, pc := c.Active()
	if c.Provider != "anthropic" || info.Name != "anthropic" || pc.Model != "claude-haiku-5-5" ||
		pc.MaxInputTokens != 60000 || *pc.Effort != "low" || c.Lang != "en" || c.Style != "conventional" ||
		c.MaxSubjectLength != 72 || c.EffectiveTimeout() != 2*time.Minute || len(c.Sources) != 0 {
		t.Errorf("defaults = %+v %+v", c, pc)
	}
}

func TestPrecedenceAndRepoRestrictions(t *testing.T) {
	global := `
provider = "ollama"
lang = "th"
emoji = true
ignore = ["*.snap"]
timeout = "5m"

[providers.ollama]
model = "llama3.1:8b"
base_url = "http://gpu-box:11434"
num_ctx = 16384
`
	repo := `
provider = "openai"
style = "simple"
emoji = false
prompt = "Mention the ticket id."

[providers.ollama]
model = "qwen3.5:4b"
base_url = "https://evil.example.com"
`
	gp, root := setup(t, global, repo)
	c, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	_, pc := c.Active()
	if c.Provider != "ollama" {
		t.Errorf("repo must not change provider, got %q", c.Provider)
	}
	if pc.BaseURL != "http://gpu-box:11434" {
		t.Errorf("repo must not change base_url, got %q", pc.BaseURL)
	}
	if pc.Model != "qwen3.5:4b" || pc.NumCtx != 16384 {
		t.Errorf("repo model override / deep merge failed: %+v", pc)
	}
	if c.Style != "simple" || c.Emoji || c.Lang != "th" || c.Prompt != "Mention the ticket id." || c.Timeout.Duration != 5*time.Minute {
		t.Errorf("merge = %+v", c)
	}
	if len(c.Warnings) != 2 || !strings.Contains(c.Warnings[0], `"provider"`) || !strings.Contains(c.Warnings[1], "providers.ollama.base_url") {
		t.Errorf("warnings = %q", c.Warnings)
	}
	if len(c.Sources) != 2 || c.Sources[0] != gp {
		t.Errorf("sources = %v", c.Sources)
	}

	t.Setenv("GIT_QUILL_PROVIDER", "openai")
	t.Setenv("GIT_QUILL_MODEL", "gpt-6-luna")
	t.Setenv("GIT_QUILL_LANG", "en")
	c, err = Load(root)
	if err != nil {
		t.Fatal(err)
	}
	info, pc := c.Active()
	if c.Provider != "openai" || pc.Model != "gpt-6-luna" || c.Lang != "en" || pc.BaseURL != info.DefaultBaseURL || pc.Effort != nil {
		t.Errorf("env overrides: %+v %+v", c, pc)
	}
}

func TestWarningsForUnknownAndKeys(t *testing.T) {
	_, root := setup(t, "colour = true\napi_key = \"sk-x\"\n[providers.openai]\napi_key = \"sk-y\"\n", "")
	c, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(c.Warnings, "\n")
	if !strings.Contains(joined, `unknown config key "colour"`) || strings.Count(joined, "API keys are never read") != 2 {
		t.Errorf("warnings = %s", joined)
	}
}

func TestLoadErrors(t *testing.T) {
	for name, global := range map[string]string{
		"bad toml":         "provider = ",
		"bad provider":     `provider = "nope"`,
		"bad lang":         `lang = "fr"`,
		"bad style":        `style = "poetry"`,
		"bad subject":      `max_subject_length = 5`,
		"bad retries":      `max_retries = 99`,
		"bad timeout":      `timeout = "soon"`,
		"negative timeout": `timeout = "-1s"`,
	} {
		t.Run(name, func(t *testing.T) {
			_, root := setup(t, global, "")
			if _, err := Load(root); err == nil {
				t.Error("want error")
			}
		})
	}
	_, root := setup(t, "", "lang = [")
	if _, err := Load(root); err == nil {
		t.Error("bad repo toml should fail")
	}
}

func TestSetGetList(t *testing.T) {
	gp, root := setup(t, "", "")
	steps := [][2]string{
		{"provider", "ollama"}, {"model", "llama3.1:8b"}, {"num_ctx", "32768"}, {"think", "true"},
		{"lang", "th"}, {"emoji", "true"}, {"ignore", "*.snap, testdata/**"}, {"timeout", "90s"},
		{"max_subject_length", "60"}, {"prompt", "Be brief."}, {"max_retries", "2"}, {"max_input_tokens", "8000"},
	}
	provider := "anthropic"
	for _, kv := range steps {
		if err := Set(gp, provider, kv[0], kv[1]); err != nil {
			t.Fatalf("set %s: %v", kv[0], err)
		}
		if kv[0] == "provider" {
			provider = kv[1]
		}
	}
	if err := Set(gp, "anthropic", "effort", "medium"); err != nil {
		t.Fatal(err)
	}
	c, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"provider": "ollama", "model": "llama3.1:8b", "num_ctx": "32768", "think": "true", "lang": "th",
		"emoji": "true", "ignore": "*.snap,testdata/**", "timeout": "1m30s", "max_subject_length": "60",
		"prompt": "Be brief.", "max_retries": "2", "max_input_tokens": "8000", "base_url": "", "effort": "",
		"style": "conventional",
	}
	for k, v := range want {
		if got, err := c.Get(k); err != nil || got != v {
			t.Errorf("Get(%s) = %q, %v; want %q", k, got, err, v)
		}
	}
	if c.Providers["anthropic"].Effort == nil || *c.Providers["anthropic"].Effort != "medium" {
		t.Error("effort scoped to anthropic")
	}
	if _, err := c.Get("nope"); err == nil {
		t.Error("unknown key Get")
	}
	if l := c.List(); len(l) != len(Keys) || l[0] != "provider = ollama" {
		t.Errorf("List = %v", l)
	}

	for _, bad := range [][2]string{
		{"nope", "x"}, {"max_retries", "many"}, {"emoji", "maybe"}, {"timeout", "soon"},
		{"lang", "fr"}, {"provider", "nope"}, {"max_subject_length", "500"},
	} {
		if err := Set(gp, "ollama", bad[0], bad[1]); err == nil {
			t.Errorf("Set(%s=%s) should fail", bad[0], bad[1])
		}
	}
	// Failed sets must not have changed the file.
	c2, _ := Load(root)
	if c2.Lang != "th" || c2.Provider != "ollama" {
		t.Error("invalid set modified the file")
	}
}

func TestGlobalPath(t *testing.T) {
	t.Setenv("GIT_QUILL_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if p, _ := GlobalPath(); p != filepath.Join("/xdg", "git-quill", "config.toml") {
		t.Errorf("xdg path = %s", p)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/u")
	t.Setenv("USERPROFILE", "/home/u")
	if p, _ := GlobalPath(); p != filepath.Join("/home/u", ".config", "git-quill", "config.toml") {
		t.Errorf("home path = %s", p)
	}
	var d Duration
	if err := d.UnmarshalText([]byte("2m")); err != nil || d.Duration != 2*time.Minute {
		t.Error("duration")
	}
	if b, _ := d.MarshalText(); string(b) != "2m0s" {
		t.Errorf("marshal = %s", b)
	}
}

func TestProviders(t *testing.T) {
	if _, ok := Lookup("nope"); ok {
		t.Error("Lookup nope")
	}
	names := ProviderNames()
	if names[0] != "anthropic" || len(names) != len(Providers) {
		t.Errorf("names = %v", names)
	}
	for _, p := range Providers {
		if p.Kind == "" || p.DefaultBudget <= 0 {
			t.Errorf("provider %s incomplete", p.Name)
		}
		if p.NeedsKey && len(p.KeyEnv) == 0 {
			t.Errorf("provider %s needs a key but has no env var", p.Name)
		}
	}
}

func TestKeys(t *testing.T) {
	keyring.MockInit()
	info, _ := Lookup("openai")
	t.Setenv("OPENAI_API_KEY", "")
	if _, src, _ := APIKey(info); src != KeyMissing {
		t.Error("expected missing")
	}
	if err := SetKey("openai", "  sk-from-keychain \n"); err != nil {
		t.Fatal(err)
	}
	if k, src, _ := APIKey(info); k != "sk-from-keychain" || src != KeyFromKeyring {
		t.Errorf("keychain key = %q %s", k, src)
	}
	t.Setenv("OPENAI_API_KEY", "sk-from-env")
	if k, src, where := APIKey(info); k != "sk-from-env" || src != KeyFromEnv || where != "OPENAI_API_KEY" {
		t.Errorf("env key = %q %s %s", k, src, where)
	}
	if err := DeleteKey("openai"); err != nil {
		t.Fatal(err)
	}
	if err := DeleteKey("openai"); err == nil || !strings.Contains(err.Error(), "no key stored") {
		t.Errorf("double delete err = %v", err)
	}
	if err := SetKey("openai", " "); err == nil {
		t.Error("empty key accepted")
	}
	gem, _ := Lookup("gemini")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "g-key")
	if k, _, where := APIKey(gem); k != "g-key" || where != "GOOGLE_API_KEY" {
		t.Errorf("fallback env = %q %s", k, where)
	}
	msg := MissingKeyError(gem).Error()
	if !strings.Contains(msg, "GEMINI_API_KEY or GOOGLE_API_KEY") || !strings.Contains(msg, "config set-key gemini") || !strings.Contains(msg, "ollama") {
		t.Errorf("MissingKeyError = %s", msg)
	}

	keyring.MockInitWithError(keyring.ErrUnsupportedPlatform)
	if err := SetKey("openai", "x"); err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Errorf("keychain failure hint: %v", err)
	}
	if envFor("nope") == "" {
		t.Error("envFor fallback")
	}
}

func TestEffectiveTimeout(t *testing.T) {
	c := Default()
	if c.EffectiveTimeout() != 2*time.Minute {
		t.Errorf("cloud default = %v", c.EffectiveTimeout())
	}
	c.Provider = "ollama"
	if c.EffectiveTimeout() != 5*time.Minute {
		t.Errorf("local default = %v", c.EffectiveTimeout())
	}
	if v, _ := c.Get("timeout"); v != "5m0s" {
		t.Errorf("Get(timeout) = %q", v)
	}
	c.Timeout = Duration{90 * time.Second}
	if c.EffectiveTimeout() != 90*time.Second {
		t.Error("explicit timeout must win")
	}
}
