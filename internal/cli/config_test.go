package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestConfigSetGetList(t *testing.T) {
	keyring.MockInit()
	tr := setup(t, "")
	t.Setenv("GIT_QUILL_PROVIDER", "") // use the file's provider
	t.Setenv("OPENAI_API_KEY", "")
	global := os.Getenv("GIT_QUILL_CONFIG")

	for _, kv := range [][]string{{"provider", "openai"}, {"model", "gpt-6-luna"}, {"lang", "th"}, {"emoji", "true"}} {
		if _, errOut, code := runCLI(t, "", append([]string{"config", "set"}, kv...)...); code != 0 {
			t.Fatalf("set %v: %s", kv, errOut)
		}
	}
	out, _, _ := runCLI(t, "", "config", "get", "model")
	if out != "gpt-6-luna\n" {
		t.Errorf("get model = %q", out)
	}
	b, _ := os.ReadFile(global)
	if !strings.Contains(string(b), "[providers.openai]") || !strings.Contains(string(b), `model = "gpt-6-luna"`) {
		t.Errorf("global file:\n%s", b)
	}

	// Per-repo values override; provider/base_url are refused per repo.
	if _, errOut, code := runCLI(t, "", "config", "set", "lang", "en", "--repo"); code != 0 {
		t.Fatal(errOut)
	}
	for _, k := range []string{"provider", "base_url"} {
		_, errOut, code := runCLI(t, "", "config", "set", k, "x", "--repo")
		if code == 0 || !strings.Contains(errOut, "cannot be set per repository") {
			t.Errorf("--repo %s: %s", k, errOut)
		}
	}
	out, _, _ = runCLI(t, "", "config", "list")
	for _, want := range []string{"provider = openai", "lang = en", "emoji = true", "api key (openai): missing", "repo config:   ", filepath.Join(filepath.Base(tr.Dir), ".git-quill.toml")} {
		if !strings.Contains(out, want) {
			t.Errorf("list missing %q:\n%s", want, out)
		}
	}

	_, errOut, code := runCLI(t, "", "config", "set", "api_key", "sk-123")
	if code == 0 || !strings.Contains(errOut, "set-key") {
		t.Errorf("api_key via set: %s", errOut)
	}
	if _, errOut, code = runCLI(t, "", "config", "set", "lang", "fr"); code == 0 || !strings.Contains(errOut, "unsupported lang") {
		t.Errorf("invalid value: %s", errOut)
	}
	if _, errOut, code = runCLI(t, "", "config", "get", "nope"); code == 0 || !strings.Contains(errOut, "unknown key") {
		t.Errorf("unknown get: %s", errOut)
	}
	out, _, _ = runCLI(t, "", "config", "path")
	if strings.TrimSpace(out) != global {
		t.Errorf("path = %q", out)
	}

	// Keys: stdin → keychain, never echoed.
	_, errOut, code = runCLI(t, "sk-secret-value\n", "config", "set-key", "openai")
	if code != 0 || strings.Contains(errOut, "sk-secret-value") {
		t.Fatalf("set-key: %s", errOut)
	}
	if v, _ := keyring.Get("git-quill", "openai"); v != "sk-secret-value" {
		t.Errorf("keychain = %q", v)
	}
	out, _, _ = runCLI(t, "", "config", "list")
	if !strings.Contains(out, "api key (openai): stored in the OS keychain") || strings.Contains(out, "sk-secret") {
		t.Errorf("list key status:\n%s", out)
	}
	t.Setenv("OPENAI_API_KEY", "sk-env")
	if out, _, _ = runCLI(t, "", "config", "list"); !strings.Contains(out, "set via OPENAI_API_KEY") {
		t.Errorf("env key status:\n%s", out)
	}
	if _, errOut, code = runCLI(t, "", "config", "delete-key", "openai"); code != 0 {
		t.Error(errOut)
	}
	if _, _, code = runCLI(t, "", "config", "delete-key", "openai"); code == 0 {
		t.Error("deleting a missing key should fail")
	}
	if _, errOut, code = runCLI(t, "k", "config", "set-key", "nope"); code == 0 || !strings.Contains(errOut, "unknown provider") {
		t.Errorf("set-key unknown: %s", errOut)
	}
	if _, _, code = runCLI(t, "", "config", "set-key", "openai"); code == 0 {
		t.Error("empty key should fail")
	}
}

func TestConfigOutsideRepo(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	t.Chdir(dir)
	t.Setenv("GIT_QUILL_CONFIG", filepath.Join(dir, "c.toml"))
	t.Setenv("GIT_QUILL_PROVIDER", "")
	out, _, code := runCLI(t, "", "config", "list")
	if code != 0 || !strings.Contains(out, "provider = anthropic") || strings.Contains(out, "repo config") {
		t.Errorf("outside repo:\n%s", out)
	}
	if _, errOut, code := runCLI(t, "", "config", "set", "lang", "th", "--repo"); code == 0 || !strings.Contains(errOut, "inside a git repository") {
		t.Errorf("--repo outside: %s", errOut)
	}
}

func TestSetupOllama(t *testing.T) {
	keyring.MockInit()
	setup(t, "")
	t.Setenv("GIT_QUILL_PROVIDER", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"models":[{"name":"qwen3.5:4b"},{"name":"llama3.1:latest"}]}`)
	}))
	defer srv.Close()
	// provider 4 = ollama; accept default model; custom base URL; Thai.
	_, errOut, code := runCLI(t, "4\n\n"+srv.URL+"\nth\n", "config")
	if code != 0 {
		t.Fatalf("setup: %s", errOut)
	}
	if !strings.Contains(errOut, "qwen3.5:4b is available") || !strings.Contains(errOut, "saved") {
		t.Errorf("setup output:\n%s", errOut)
	}
	out, _, _ := runCLI(t, "", "config", "list")
	for _, want := range []string{"provider = ollama", "model = qwen3.5:4b", "lang = th", "base_url = " + srv.URL, "api key (ollama): not needed"} {
		if !strings.Contains(out, want) {
			t.Errorf("after setup missing %q:\n%s", want, out)
		}
	}
	// Model not pulled / server down.
	_, errOut, _ = runCLI(t, "ollama\nllama9\n"+srv.URL+"\n\n", "config")
	if !strings.Contains(errOut, "ollama pull llama9") {
		t.Errorf("missing model hint:\n%s", errOut)
	}
	srv.Close()
	_, errOut, _ = runCLI(t, "ollama\n\n"+srv.URL+"\n\n", "config")
	if !strings.Contains(errOut, "not reachable") {
		t.Errorf("down hint:\n%s", errOut)
	}
}

func TestSetupCloudProvider(t *testing.T) {
	keyring.MockInit()
	setup(t, "")
	t.Setenv("GIT_QUILL_PROVIDER", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	_, errOut, code := runCLI(t, "3\n\nen\ng-secret\n", "config")
	if code != 0 || !strings.Contains(errOut, "stored the key in the OS keychain") || strings.Contains(errOut, "g-secret") {
		t.Fatalf("gemini setup: %d %s", code, errOut)
	}
	if v, _ := keyring.Get("git-quill", "gemini"); v != "g-secret" {
		t.Errorf("keychain = %q", v)
	}
	// Key already present → not asked again.
	_, errOut, _ = runCLI(t, "gemini\n\n\n", "config")
	if !strings.Contains(errOut, "API key found (keychain)") {
		t.Errorf("existing key:\n%s", errOut)
	}
	// Skipping the key prints how to add it later.
	_, errOut, _ = runCLI(t, "openai\n\n\n\n", "config")
	if !strings.Contains(errOut, "set OPENAI_API_KEY later") {
		t.Errorf("skip key:\n%s", errOut)
	}
	// OpenRouter has no default model.
	if _, errOut, code = runCLI(t, "openrouter\n\n", "config"); code == 0 || !strings.Contains(errOut, "needs a model id") {
		t.Errorf("openrouter: %s", errOut)
	}
	if _, errOut, code = runCLI(t, "42\n", "config"); code == 0 || !strings.Contains(errOut, "invalid choice") {
		t.Errorf("bad choice: %s", errOut)
	}
}

// Seen live: accepting the default base URL for Ollama used to write
// http://localhost:8080/v1.
func TestSetupOllamaDefaultBaseURL(t *testing.T) {
	keyring.MockInit()
	setup(t, "")
	t.Setenv("GIT_QUILL_PROVIDER", "")
	t.Setenv("OLLAMA_HOST", "")
	_, errOut, code := runCLI(t, "ollama\n\n\n\n", "config")
	if code != 0 || !strings.Contains(errOut, "[http://localhost:11434]") {
		t.Fatalf("default shown: %s", errOut)
	}
	out, _, _ := runCLI(t, "", "config", "get", "base_url")
	if strings.TrimSpace(out) != "" {
		t.Errorf("base_url should stay unset so OLLAMA_HOST keeps working, got %q", out)
	}
}
