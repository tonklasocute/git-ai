package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tonklasocute/git-quill/internal/git/gittest"
)

// setup creates a temp repo, makes it the working directory and points
// git-quill at an isolated config with the fake provider.
func setup(t *testing.T, cfg string) *gittest.Repo {
	t.Helper()
	tr := gittest.New(t)
	tr.Setenv(t)
	t.Chdir(tr.Dir)
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_QUILL_CONFIG", cfgPath)
	t.Setenv("GIT_QUILL_PROVIDER", "fake")
	for _, k := range []string{"GIT_QUILL_MODEL", "GIT_QUILL_LANG", "GIT_QUILL_FAKE_ERROR", "GIT_QUILL_FAKE_RESPONSE", "GIT_QUILL_FAKE_DELAY"} {
		t.Setenv(k, "")
	}
	t.Setenv("NO_COLOR", "1")
	tr.Commit("chore: init", map[string]string{"README.md": "# demo\n"})
	return tr
}

func runCLI(t *testing.T, stdin string, args ...string) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &errOut)
	return out.String(), errOut.String(), code
}

func TestCommitYes(t *testing.T) {
	tr := setup(t, "")
	tr.Stage(map[string]string{"internal/auth/login.go": "package auth\n\nfunc Login() {}\n"})
	out, errOut, code := runCLI(t, "", "commit", "--yes", "-v")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, errOut)
	}
	msg := tr.Git("log", "-1", "--format=%B")
	if !strings.HasPrefix(msg, "feat(auth): add login\n\n- Add internal/auth/login.go") {
		t.Errorf("commit message = %q", msg)
	}
	if !strings.Contains(out, "feat(auth): add login") { // git commit's summary line
		t.Errorf("stdout = %s", out)
	}
	if !strings.Contains(errOut, "tokens: ~") {
		t.Errorf("verbose usage missing: %s", errOut)
	}
}

func TestCommitDryRunThaiCandidates(t *testing.T) {
	tr := setup(t, "lang = \"en\"\n")
	tr.Stage(map[string]string{"docs/guide.md": "# Guide\n"})
	out, errOut, code := runCLI(t, "", "commit", "--dry-run", "--lang", "th", "-n", "2", "--emoji", "--no-body")
	if code != 0 {
		t.Fatalf("code=%d %s", code, errOut)
	}
	if out != "📝 docs(docs): อัปเดตเอกสาร guide\n===\n📝 docs(docs): เพิ่มคำอธิบาย guide\n" {
		t.Errorf("stdout = %q", out)
	}
	if n := tr.Git("rev-list", "--count", "HEAD"); strings.TrimSpace(n) != "1" {
		t.Error("dry run must not commit")
	}
}

func TestCommitShowPromptOffline(t *testing.T) {
	tr := setup(t, "")
	secret := "ghp_" + strings.Repeat("aB3dE5", 6)
	tr.Stage(map[string]string{"deploy.sh": "export GH_TOKEN=" + secret + "\n", "yarn.lock": "x\n"})
	t.Setenv("GIT_QUILL_FAKE_ERROR", "server") // proves nothing is sent
	out, errOut, code := runCLI(t, "", "commit", "--dry-run", "--show-prompt", "--context", "rotate token "+secret)
	if code != 0 {
		t.Fatalf("code=%d %s", code, errOut)
	}
	if out != "" {
		t.Errorf("stdout should be empty, got %q", out)
	}
	for _, want := range []string{"[system]", "[user]", "[REDACTED:github-token]", "Also changed, content not shown: yarn.lock (ignore pattern)", "dry run: nothing was sent", "redacted 1 secret(s)", "github-token (commit messages / context)"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr missing %q:\n%s", want, errOut)
		}
	}
	if strings.Contains(errOut, secret) {
		t.Error("secret leaked into the prompt")
	}
}

func TestCommitMapReduce(t *testing.T) {
	tr := setup(t, "[providers.fake]\nmax_input_tokens = 2600\n")
	big := strings.Repeat("some reasonably long line of content for the budget test\n", 60)
	tr.Stage(map[string]string{"store/a.go": big, "store/b.go": big, "store/c.go": big})
	out, errOut, code := runCLI(t, "", "commit", "--dry-run")
	if code != 0 {
		t.Fatalf("code=%d %s", code, errOut)
	}
	if !strings.Contains(errOut, "map-reduce") || !strings.Contains(errOut, "reduced diff context to 0") {
		t.Errorf("strategy not reported:\n%s", errOut)
	}
	if !strings.HasPrefix(out, "feat(store): ") {
		t.Errorf("message from summaries = %q", out)
	}

	_, errOut, code = runCLI(t, "", "commit", "--dry-run", "--show-prompt")
	if code != 0 || strings.Count(errOut, "request: summarize") < 3 || !strings.Contains(errOut, "nothing was sent") {
		t.Errorf("offline map-reduce prompts:\n%s", errOut)
	}
	_, errOut, code = runCLI(t, "", "commit", "--dry-run", "--show-prompt=true", "--yes")
	if code != 0 {
		t.Errorf("code=%d %s", code, errOut)
	}
}

func TestCommitShowPromptThenSend(t *testing.T) {
	tr := setup(t, "")
	tr.Stage(map[string]string{"a.go": "package a\n"})
	out, errOut, code := runCLI(t, "", "commit", "--show-prompt", "--yes", "--", "--no-verify")
	if code != 0 || !strings.Contains(errOut, "[system]") || !strings.Contains(out, "feat(a)") {
		t.Fatalf("code=%d out=%s err=%s", code, out, errOut)
	}
}

func TestCommitErrors(t *testing.T) {
	tr := setup(t, "")
	_, errOut, code := runCLI(t, "", "commit", "--yes")
	if code != 1 || !strings.Contains(errOut, "nothing staged") {
		t.Errorf("nothing staged: %d %s", code, errOut)
	}
	tr.Write("README.md", "changed\n")
	_, errOut, _ = runCLI(t, "", "commit", "--yes")
	if !strings.Contains(errOut, "unstaged changes") {
		t.Errorf("unstaged hint missing: %s", errOut)
	}
	tr.Stage(map[string]string{"x.go": "package x\n"})
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"commit"}, "not running in a terminal"},
		{[]string{"commit", "-n", "9", "--yes"}, "between 1 and 5"},
		{[]string{"commit", "--type", "feature", "--yes"}, "unknown --type"},
		{[]string{"commit", "--lang", "fr", "--yes"}, "unsupported lang"},
	} {
		_, errOut, code := runCLI(t, "", tc.args...)
		if code == 0 || !strings.Contains(errOut, tc.want) {
			t.Errorf("%v: code=%d stderr=%s", tc.args, code, errOut)
		}
	}
	t.Setenv("GIT_QUILL_FAKE_ERROR", "auth")
	_, errOut, code = runCLI(t, "", "commit", "--yes")
	if code != 1 || !strings.Contains(errOut, "invalid API key") || !strings.Contains(errOut, "hint:") {
		t.Errorf("provider error: %s", errOut)
	}
	t.Setenv("GIT_QUILL_FAKE_ERROR", "")
	t.Setenv("GIT_QUILL_FAKE_RESPONSE", "```\n\n```")
	_, errOut, _ = runCLI(t, "", "commit", "--yes")
	if !strings.Contains(errOut, "empty message") {
		t.Errorf("empty response: %s", errOut)
	}
}

func TestNotARepo(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	t.Chdir(dir)
	_, errOut, code := runCLI(t, "", "commit", "--yes")
	if code != 1 || !strings.Contains(errOut, "not a git repository") {
		t.Errorf("code=%d %s", code, errOut)
	}
}

func TestVersionAndHelp(t *testing.T) {
	out, _, code := runCLI(t, "", "--version")
	if code != 0 || !strings.Contains(out, "git-quill version") {
		t.Errorf("version: %s", out)
	}
	out, _, _ = runCLI(t, "", "commit", "--help")
	if !strings.Contains(out, "--dry-run") || !strings.Contains(out, "git quill commit -n 3 --lang th") {
		t.Errorf("help: %s", out)
	}
	if (exitError{code: 3}).Error() != "exit status 3" {
		t.Error("exitError")
	}
}
