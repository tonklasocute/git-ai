package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tonklasocute/git-quill/internal/config"
	"github.com/tonklasocute/git-quill/internal/git/gittest"
	"github.com/tonklasocute/git-quill/internal/provider"
	"github.com/tonklasocute/git-quill/internal/provider/fake"
)

func newApp(t *testing.T, cfgTOML string) (*App, *gittest.Repo, *bytes.Buffer) {
	t.Helper()
	tr := gittest.New(t)
	tr.Setenv(t)
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfgPath, []byte(cfgTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_QUILL_CONFIG", cfgPath)
	t.Setenv("GIT_QUILL_PROVIDER", "fake")
	t.Setenv("GIT_QUILL_MODEL", "")
	t.Setenv("GIT_QUILL_LANG", "")
	tr.Commit("init", map[string]string{"README.md": "hi\n"})
	var out, errOut bytes.Buffer
	a, err := Open(context.Background(), tr.Dir, &out, &errOut)
	if err != nil {
		t.Fatal(err)
	}
	return a, tr, &errOut
}

func staged(a *App) Source {
	return func(ctx context.Context, n int) (string, error) { return a.Repo.StagedDiff(ctx, n) }
}

func TestPrepareSmallDiff(t *testing.T) {
	a, tr, errOut := newApp(t, "ignore = [\"*.snap\"]\n")
	if err := os.WriteFile(filepath.Join(tr.Dir, ".git-quill-ignore"), []byte("generated/\n!go.sum\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr.Stage(map[string]string{
		"main.go":           "package main\n",
		"go.sum":            "x v1 h1:abc\n",
		"ui/a.snap":         "snap\n",
		"generated/x.go":    "package x\n",
		"package-lock.json": "{}\n",
		"config/.env":       "API_TOKEN=" + "ghp_" + strings.Repeat("aB3dE5", 6) + "\n",
	})
	a.Verbose = true
	p, err := a.Prepare(context.Background(), staged(a), false)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range p.Files {
		paths = append(paths, f.Path())
	}
	if strings.Join(paths, ",") != "config/.env,go.sum,main.go" {
		t.Errorf("kept = %v", paths)
	}
	if len(p.Excluded) != 3 || p.OverBudget || p.ContextLines != 3 {
		t.Errorf("prepared = %+v", p)
	}
	if strings.Contains(p.Text, "aB3dE5") || !strings.Contains(p.Text, "[REDACTED:github-token]") {
		t.Errorf("secret not redacted:\n%s", p.Text)
	}
	if len(p.Findings) != 1 || !strings.Contains(errOut.String(), "redacted 1 secret") || !strings.Contains(errOut.String(), "github-token (config/.env)") {
		t.Errorf("warning output:\n%s", errOut.String())
	}
	if p.Note() != "" || !strings.Contains(strings.Join(p.Steps, ";"), "left out 3 file(s)") {
		t.Errorf("note = %q steps = %v", p.Note(), p.Steps)
	}
	a.Strategy(p, false)
	if !strings.Contains(errOut.String(), "note: left out 3 file(s)") {
		t.Errorf("verbose strategy note missing:\n%s", errOut.String())
	}
}

func bigFile(lines int) string {
	var b strings.Builder
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&b, "line %03d: some reasonably long content for budgeting purposes\n", i)
	}
	return b.String()
}

func TestPrepareReducesContextThenMapReduce(t *testing.T) {
	// Budget = 2600 - 2000 = 600 tokens.
	a, tr, errOut := newApp(t, "[providers.fake]\nmax_input_tokens = 2600\n")
	base := bigFile(60)
	tr.Commit("base", map[string]string{"a.txt": base})
	// One changed line in a big file: fits only once context is reduced.
	tr.Stage(map[string]string{"a.txt": strings.Replace(base, "line 030", "LINE 030", 1)})
	p, err := a.Prepare(context.Background(), staged(a), false)
	if err != nil {
		t.Fatal(err)
	}
	if p.OverBudget || p.ContextLines != 3 {
		t.Fatalf("small change should fit as-is: %+v", p.Steps)
	}

	// Many changed lines in several files: over budget even at -U0.
	tr.Stage(map[string]string{"b.txt": bigFile(40), "c.txt": bigFile(40), "d.txt": bigFile(40)})
	p, err = a.Prepare(context.Background(), staged(a), false)
	if err != nil {
		t.Fatal(err)
	}
	if !p.OverBudget || p.ContextLines != 0 || len(p.Chunks) < 3 {
		t.Fatalf("expected map-reduce: ctx=%d chunks=%d steps=%v", p.ContextLines, len(p.Chunks), p.Steps)
	}
	var progress []string
	sum, err := a.Summarize(context.Background(), p, func(d, n int) { progress = append(progress, fmt.Sprintf("%d/%d", d, n)) })
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"a.txt", "b.txt", "c.txt", "d.txt"} {
		if !strings.Contains(sum, "- "+f+":") {
			t.Errorf("summary missing %s:\n%s", f, sum)
		}
	}
	if len(progress) != len(p.Chunks) {
		t.Errorf("progress = %v", progress)
	}
	a.Strategy(p, true)
	if !strings.Contains(errOut.String(), "reduced diff context to 0 line(s)") || !strings.Contains(errOut.String(), "(map-reduce)") {
		t.Errorf("strategy output:\n%s", errOut.String())
	}
	fp, _ := a.Provider()
	if fp.(*fake.Client).Calls() != len(p.Chunks) {
		t.Error("one summarize call per chunk expected")
	}
}

func TestReducedContextOnly(t *testing.T) {
	// 2 changed lines in a 200-line file, budget tight enough that -U3
	// context of many hunks overflows but -U0 fits.
	a, tr, errOut := newApp(t, "[providers.fake]\nmax_input_tokens = 3000\n")
	base := bigFile(200)
	tr.Commit("base", map[string]string{"big.txt": base})
	changed := base
	for i := 0; i < 200; i += 12 {
		k := fmt.Sprintf("line %03d", i)
		changed = strings.Replace(changed, k, strings.ToUpper(k), 1)
	}
	tr.Stage(map[string]string{"big.txt": changed})
	p, err := a.Prepare(context.Background(), staged(a), false)
	if err != nil {
		t.Fatal(err)
	}
	if p.OverBudget || p.ContextLines == 3 {
		t.Fatalf("want reduced context without map-reduce: ctx=%d over=%v tokens=%d", p.ContextLines, p.OverBudget, p.Tokens)
	}
	a.Strategy(p, false)
	if !strings.Contains(errOut.String(), "large diff: reduced diff context") {
		t.Errorf("strategy:\n%s", errOut.String())
	}
}

func TestPrepareNumbered(t *testing.T) {
	a, tr, _ := newApp(t, "")
	tr.Stage(map[string]string{"x.go": "package x\n\nfunc X() {}\n"})
	p, err := a.Prepare(context.Background(), staged(a), true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Text, "### x.go (added)") || !strings.Contains(p.Text, "     3 +func X() {}") {
		t.Errorf("numbered:\n%s", p.Text)
	}
}

func TestCallUsageAndErrors(t *testing.T) {
	a, _, errOut := newApp(t, "")
	a.Verbose = true
	resp, err := a.Call(context.Background(), provider.Request{Task: provider.TaskCommit, Messages: []provider.Message{{Role: provider.User, Content: "hello"}}}, nil)
	if err != nil || resp.Text == "" {
		t.Fatal(err)
	}
	a.PrintUsageIf()
	if !strings.Contains(errOut.String(), "commit call: ~") || !strings.Contains(errOut.String(), "1 call(s) · fake/fake-1") {
		t.Errorf("usage output:\n%s", errOut.String())
	}

	b, _, _ := newApp(t, "")
	b.NewProvider = func(*config.Config, func(int, time.Duration, string)) (provider.Provider, error) {
		return nil, errors.New("no key")
	}
	if _, err := b.Call(context.Background(), provider.Request{}, nil); err == nil || err.Error() != "no key" {
		t.Errorf("err = %v", err)
	}
	b.PrintUsage() // no calls: prints nothing, must not panic
	b.ShowPrompt(provider.Request{Task: provider.TaskCommit})

	t.Setenv("GIT_QUILL_FAKE_ERROR", "server")
	c, _, _ := newApp(t, "")
	if _, err := c.Call(context.Background(), provider.Request{}, nil); err == nil {
		t.Error("fake error not propagated")
	}
	p := &Prepared{Chunks: []string{"x"}}
	if _, err := c.Summarize(context.Background(), p, nil); err == nil {
		t.Error("summarize should propagate errors")
	}
}

func TestOpenErrors(t *testing.T) {
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(t.TempDir()))
	if _, err := Open(context.Background(), t.TempDir(), nil, nil); err == nil {
		t.Error("not a repo")
	}
	tr := gittest.New(t)
	tr.Setenv(t)
	cfg := filepath.Join(t.TempDir(), "c.toml")
	_ = os.WriteFile(cfg, []byte(`lang = "xx"`), 0o644)
	t.Setenv("GIT_QUILL_CONFIG", cfg)
	if _, err := Open(context.Background(), tr.Dir, nil, nil); err == nil {
		t.Error("bad config")
	}
	_ = os.WriteFile(cfg, []byte("colour = 1\n"), 0o644)
	var errOut bytes.Buffer
	if _, err := Open(context.Background(), tr.Dir, nil, &errOut); err != nil || !strings.Contains(errOut.String(), "unknown config key") {
		t.Errorf("warnings not printed: %v %s", err, errOut.String())
	}
}

func TestBudgetFloorAndHelpers(t *testing.T) {
	a, _, _ := newApp(t, "[providers.fake]\nmax_input_tokens = 100\n")
	if a.Budget() != 500 {
		t.Errorf("budget floor = %d", a.Budget())
	}
	if thousands(1234567) != "1,234,567" || thousands(12) != "12" {
		t.Error("thousands")
	}
	if usageString(provider.Usage{InputTokens: 1000, OutputTokens: 5}) != "1,000 in / 5 out" {
		t.Error("usageString")
	}
	if (&Prepared{ContextLines: 3}).Note() != "" || !strings.Contains((&Prepared{}).Note(), "reduced to 0") {
		t.Error("empty note")
	}
	a.RedactText("nothing secret here")
}

func TestNewProvider(t *testing.T) {
	cfg := config.Default()
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("HOME", t.TempDir()) // keychain lookups may fail; that's fine
	cfg.Provider = "openrouter"
	if _, err := NewProvider(cfg, nil); err == nil || !strings.Contains(err.Error(), "no model set") {
		t.Errorf("openrouter without model: %v", err)
	}
	cfg.Providers["compatible"] = config.ProviderConfig{Model: "m"}
	cfg.Provider = "compatible"
	if _, err := NewProvider(cfg, nil); err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Errorf("compatible without base_url: %v", err)
	}
	cfg.Provider = "ollama"
	p, err := NewProvider(cfg, nil)
	if err != nil || p.Name() != "ollama" || p.Model() != "qwen3.5:4b" {
		t.Errorf("ollama: %v %v", p, err)
	}
	cfg.Providers["ollama"] = config.ProviderConfig{NumCtx: 4096, Think: "true"}
	if _, err := NewProvider(cfg, nil); err != nil {
		t.Error(err)
	}
	cfg.Provider = "fake"
	if p, err := NewProvider(cfg, nil); err != nil || p.Name() != "fake" {
		t.Errorf("fake: %v", err)
	}
}

func TestSplitCandidatesAndClean(t *testing.T) {
	text := "<think>hmm</think>```\nfeat(api): add health check.\n\n- add /healthz\n```\n===\nCommit message: \"fix(api): handle timeouts\"\n=====\n\n===\n2) chore: tidy"
	got := SplitCandidates(text, 72)
	want := []string{"feat(api): add health check\n\n- add /healthz", "fix(api): handle timeouts", "chore: tidy"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("SplitCandidates = %q", got)
	}
	long := "feat(parser): support extremely long subjects that keep going well past the configured limit of characters"
	if s := CleanMessage(long, 72); len([]rune(s)) > 72 || strings.HasSuffix(s, " ") || !strings.HasPrefix(s, "feat(parser): support") {
		t.Errorf("shortened = %q (%d)", s, len([]rune(s)))
	}
	if s := ShortenSubject("feat: เพิ่มระบบเข้าสู่ระบบด้วยรหัสผ่านแบบใหม่ที่ยาวมากเกินกว่าที่กำหนดไว้แน่นอน", 30); len([]rune(s)) > 30 {
		t.Errorf("thai shorten = %q", s)
	}
	if CleanMessage("  ", 72) != "" || CleanMessage("''", 72) != "" {
		t.Error("empty")
	}
	if CleanMessage("fix: x\r\n\r\n\r\nbody  ", 0) != "fix: x\n\nbody" {
		t.Error("crlf/body normalisation")
	}
}

func TestNewProviderCloud(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, k := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_PROFILE", "OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY"} {
		t.Setenv(k, "")
	}
	cfg := config.Default()
	for _, name := range []string{"anthropic", "openai", "gemini"} {
		cfg.Provider = name
		if _, err := NewProvider(cfg, nil); err == nil || !strings.Contains(err.Error(), "no API key") {
			// The OS keychain may hold a real key on a developer machine.
			if err == nil {
				t.Logf("%s: key found in keychain, skipping missing-key check", name)
				continue
			}
			t.Errorf("%s without key: %v", name, err)
		}
	}
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "oauth-token")
	cfg.Provider = "anthropic"
	if p, err := NewProvider(cfg, nil); err != nil || p.Name() != "anthropic" || p.Model() != "claude-haiku-5-5" {
		t.Errorf("anthropic via ambient auth: %v", err)
	}
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	if err := os.MkdirAll(filepath.Join(home, ".config", "anthropic"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !anthropicAmbientAuth() {
		t.Error("ant profile dir should count as ambient auth")
	}
	t.Setenv("OPENAI_API_KEY", "sk-x")
	t.Setenv("GEMINI_API_KEY", "g")
	cfg.Provider = "openai"
	if p, err := NewProvider(cfg, nil); err != nil || p.Name() != "openai" || p.Model() != "gpt-6.1-sol" {
		t.Errorf("openai: %v", err)
	}
	cfg.Provider = "gemini"
	if p, err := NewProvider(cfg, nil); err != nil || p.Model() != "gemini-3.8-flash" {
		t.Errorf("gemini: %v", err)
	}
	cfg.Provider = "lmstudio"
	cfg.Providers["lmstudio"] = config.ProviderConfig{Model: "qwen"}
	if p, err := NewProvider(cfg, nil); err != nil || p.Name() != "lmstudio" {
		t.Errorf("lmstudio: %v", err)
	}
}
