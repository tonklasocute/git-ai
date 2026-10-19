package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tonklasocute/git-quill/internal/git/gittest"
)

func featureBranch(t *testing.T, tr *gittest.Repo) {
	t.Helper()
	tr.Git("checkout", "-q", "-b", "feature/login")
	tr.Commit("feat(auth): add login", map[string]string{"internal/auth/login.go": "package auth\n\nfunc Login() {}\n"})
	tr.Commit("fix(auth): reject empty password\n\nToken was "+"ghp_"+strings.Repeat("aB3dE5", 6), map[string]string{
		"internal/auth/login.go": "package auth\n\nfunc Login() {\n\t_ = err\n}\n",
	})
}

func TestPR(t *testing.T) {
	tr := setup(t, "")
	featureBranch(t, tr)
	out, errOut, code := runCLI(t, "", "pr", "-v")
	if code != 0 {
		t.Fatalf("code=%d %s", code, errOut)
	}
	for _, want := range []string{"feat(auth): add login\n\n## Summary", "- reject empty password", "## Breaking changes"} {
		if !strings.Contains(out, want) {
			t.Errorf("pr missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(errOut, "redacted 1 secret") {
		t.Errorf("commit body secret not redacted:\n%s", errOut)
	}

	out, _, _ = runCLI(t, "", "pr", "--lang", "th", "--base", "main", "--dry-run")
	if !strings.Contains(out, "## สรุป") {
		t.Errorf("thai pr:\n%s", out)
	}
	_, errOut, code = runCLI(t, "", "pr", "--dry-run", "--show-prompt")
	if code != 0 || !strings.Contains(errOut, "Commits (oldest first):") || !strings.Contains(errOut, "Branch: feature/login → main") {
		t.Errorf("pr prompt:\n%s", errOut)
	}

	// --create without gh on PATH falls back to printing.
	if runtime.GOOS == "windows" {
		return // relies on a symlinked git and a PATH without gh
	}
	t.Setenv("PATH", filepath.Dir(gitPath(t)))
	out, errOut, code = runCLI(t, "", "pr", "--create")
	if code != 0 || !strings.Contains(errOut, "gh) is not installed") || out == "" {
		t.Errorf("no gh: code=%d out=%s err=%s", code, out, errOut)
	}
}

func gitPath(t *testing.T) string {
	t.Helper()
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if _, err := os.Stat(filepath.Join(dir, "git")); err == nil {
			// Copy git alone into a temp dir so PATH has git but not gh.
			tmp := t.TempDir()
			src := filepath.Join(dir, "git")
			if err := os.Symlink(src, filepath.Join(tmp, "git")); err != nil {
				t.Skip(err)
			}
			return filepath.Join(tmp, "git")
		}
	}
	t.Skip("git not on PATH")
	return ""
}

func TestPRWithFakeGH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake gh is a shell script")
	}
	tr := setup(t, "")
	featureBranch(t, tr)
	bin := t.TempDir()
	log := filepath.Join(bin, "gh.log")
	script := "#!/bin/sh\necho \"$@\" > " + log + "\ncat \"$(echo \"$@\" | sed 's/.*--body-file \\([^ ]*\\).*/\\1/')\" >> " + log + "\necho https://github.com/x/y/pull/1\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, errOut, code := runCLI(t, "", "pr", "--create", "--draft")
	if code != 0 || !strings.Contains(out, "pull/1") {
		t.Fatalf("code=%d out=%s err=%s", code, out, errOut)
	}
	b, _ := os.ReadFile(log)
	got := string(b)
	for _, want := range []string{"pr create --base main --head feature/login --title feat(auth): add login --body-file", "--draft", "## Summary"} {
		if !strings.Contains(got, want) {
			t.Errorf("gh call missing %q:\n%s", want, got)
		}
	}

	// gh failure is reported with a push hint.
	_ = os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\nexit 1\n"), 0o755)
	_, errOut, code = runCLI(t, "", "pr", "--create")
	if code != 1 || !strings.Contains(errOut, "git push -u origin feature/login") {
		t.Errorf("gh failure: %d %s", code, errOut)
	}
}

func TestPRErrors(t *testing.T) {
	tr := setup(t, "")
	_, errOut, _ := runCLI(t, "", "pr")
	if !strings.Contains(errOut, "you are on main itself") {
		t.Errorf("on main: %s", errOut)
	}
	tr.Git("checkout", "-q", "-b", "empty")
	_, errOut, _ = runCLI(t, "", "pr")
	if !strings.Contains(errOut, "no commits on empty") {
		t.Errorf("no commits: %s", errOut)
	}
	_, errOut, _ = runCLI(t, "", "pr", "--base", "nope")
	if !strings.Contains(errOut, `base branch "nope" not found`) {
		t.Errorf("bad base: %s", errOut)
	}
	tr.Git("checkout", "-q", "--detach")
	_, errOut, _ = runCLI(t, "", "pr")
	if !strings.Contains(errOut, "detached") {
		t.Errorf("detached: %s", errOut)
	}
}

func TestChangelog(t *testing.T) {
	tr := setup(t, "")
	tr.Git("tag", "v0.1.0")
	tr.Commit("feat(api): add health endpoint", map[string]string{"api.go": "package api\n"})
	tr.Commit("fix(api): handle timeouts", map[string]string{"api.go": "package api\n// x\n"})
	tr.Commit("chore: tidy", nil)

	out, errOut, code := runCLI(t, "", "changelog", "-v")
	if code != 0 {
		t.Fatalf("code=%d %s", code, errOut)
	}
	if !strings.HasPrefix(out, "## [Unreleased]\n\n### Added\n\n- **api:** Add health endpoint\n\n### Fixed\n\n- **api:** Handle timeouts\n") {
		t.Errorf("changelog:\n%s", out)
	}
	if !strings.Contains(errOut, "3 commit(s) from v0.1.0 to HEAD") {
		t.Errorf("verbose: %s", errOut)
	}

	_, errOut, code = runCLI(t, "", "changelog", "--version", "v0.2.0", "--write", "CHANGELOG.md")
	if code != 0 || !strings.Contains(errOut, "wrote ## [v0.2.0]") {
		t.Fatalf("write: %d %s", code, errOut)
	}
	b, _ := os.ReadFile(filepath.Join(tr.Dir, "CHANGELOG.md"))
	if !strings.HasPrefix(string(b), "# Changelog") || !strings.Contains(string(b), "## [v0.2.0] - ") {
		t.Errorf("CHANGELOG.md:\n%s", b)
	}
	_, errOut, code = runCLI(t, "", "changelog", "--version", "v0.2.0", "--write", "CHANGELOG.md")
	if code == 0 || !strings.Contains(errOut, "already has a section") {
		t.Errorf("duplicate write: %s", errOut)
	}

	out, _, _ = runCLI(t, "", "changelog", "--from", "v0.1.0", "--to", "HEAD~1", "--dry-run", "--version", "v0.1.1")
	if strings.Contains(out, "tidy") || !strings.Contains(out, "## [v0.1.1] - ") {
		t.Errorf("range:\n%s", out)
	}
	_, errOut, _ = runCLI(t, "", "changelog", "--dry-run", "--show-prompt")
	if !strings.Contains(errOut, "Keep a Changelog") || !strings.Contains(errOut, "Release: Unreleased") {
		t.Errorf("prompt: %s", errOut)
	}
	_, errOut, _ = runCLI(t, "", "changelog", "--from", "nope")
	if !strings.Contains(errOut, `unknown ref "nope"`) {
		t.Errorf("bad ref: %s", errOut)
	}
	tr.Git("tag", "v0.2.0")
	_, errOut, _ = runCLI(t, "", "changelog")
	if !strings.Contains(errOut, "no commits between v0.2.0 and HEAD") {
		t.Errorf("empty range: %s", errOut)
	}
}

func TestChangelogBudget(t *testing.T) {
	tr := setup(t, "[providers.fake]\nmax_input_tokens = 2600\n")
	for i := 0; i < 90; i++ {
		tr.Commit(fmt.Sprintf("feat(module%02d): add a feature with a fairly descriptive subject line", i)+"\n\n"+strings.Repeat("A long body line explaining things. ", 8), nil)
	}
	_, errOut, code := runCLI(t, "", "changelog", "--dry-run")
	if code != 0 || !strings.Contains(errOut, "dropped commit bodies and the") {
		t.Errorf("budget: %d %s", code, errOut)
	}
}

func TestReview(t *testing.T) {
	tr := setup(t, "")
	tr.Stage(map[string]string{
		"svc/db.go": "package svc\n\nfunc Q() {\n\t_ = err\n\t// TODO: retry\n}\n",
		"svc/.env":  "DB_PASSWORD=" + "hunter2hunter2\n",
		"README.md": "# demo\n\nmore docs\n",
	})
	out, errOut, code := runCLI(t, "", "review")
	if code != 0 {
		t.Fatalf("code=%d %s", code, errOut)
	}
	for _, want := range []string{"AI review — suggestions only", "svc/db.go", "[MEDIUM] L4    Error is discarded.", "[HIGH]", "svc/.env", "finding(s):"} {
		if !strings.Contains(out, want) {
			t.Errorf("review missing %q:\n%s", want, out)
		}
	}
	_, _, code = runCLI(t, "", "review", "--strict")
	if code != 1 {
		t.Errorf("--strict with medium/high findings should exit 1, got %d", code)
	}

	t.Setenv("GIT_QUILL_FAKE_RESPONSE", "Looks fine to me!")
	out, _, code = runCLI(t, "", "review")
	if code != 0 || !strings.Contains(out, "(unstructured response)") {
		t.Errorf("unstructured: %d %s", code, out)
	}
	_, errOut, code = runCLI(t, "", "review", "--strict")
	if code != 1 || !strings.Contains(errOut, "could not parse") {
		t.Errorf("strict unstructured: %d %s", code, errOut)
	}
	t.Setenv("GIT_QUILL_FAKE_RESPONSE", `{"summary":"ok","findings":[{"file":"x","line":1,"severity":"low","message":"nit"}]}`)
	if _, _, code = runCLI(t, "", "review", "--strict"); code != 0 {
		t.Errorf("only low findings should pass --strict, got %d", code)
	}
	t.Setenv("GIT_QUILL_FAKE_RESPONSE", "")

	_, errOut, _ = runCLI(t, "", "review", "--dry-run", "--show-prompt")
	if !strings.Contains(errOut, "     4 +\t_ = err") {
		t.Errorf("numbered prompt:\n%s", errOut)
	}
}

func TestReviewBranchAndChunks(t *testing.T) {
	tr := setup(t, "[providers.fake]\nmax_input_tokens = 2600\n")
	tr.Git("checkout", "-q", "-b", "big")
	body := strings.Repeat("\tx := compute()\n", 40)
	tr.Commit("feat: big", map[string]string{"a/a.go": "package a\nfunc A() {\n" + body + "}\n", "b/b.go": "package b\nfunc B() {\n" + body + "}\n", "c/c.go": "package c\nfunc C() {\n" + body + "}\n"})
	out, errOut, code := runCLI(t, "", "review", "--branch", "--base", "main")
	if code != 0 || !strings.Contains(errOut, "reviewing 3 file(s) in") || !strings.Contains(out, "a/a.go") || !strings.Contains(out, "c/c.go") {
		t.Errorf("chunked review: %d\n%s\n%s", code, errOut, out)
	}
}

func TestReviewNothing(t *testing.T) {
	tr := setup(t, "")
	_, errOut, _ := runCLI(t, "", "review")
	if !strings.Contains(errOut, "no staged changes") {
		t.Errorf("empty: %s", errOut)
	}
	tr.Stage(map[string]string{"go.sum": "x\n"})
	_, errOut, _ = runCLI(t, "", "review")
	if !strings.Contains(errOut, "every changed file is excluded") {
		t.Errorf("all excluded: %s", errOut)
	}
	tr.Git("checkout", "-q", "-b", "same")
	tr.Git("reset", "-q", "--hard")
	_, errOut, _ = runCLI(t, "", "review", "--branch")
	if !strings.Contains(errOut, "no changes on this branch") {
		t.Errorf("branch empty: %s", errOut)
	}
}

func TestConfirm(t *testing.T) {
	var out strings.Builder
	if !confirm(strings.NewReader("y\n"), &out, "Go?") || confirm(strings.NewReader("\n"), &out, "Go?") || !strings.Contains(out.String(), "Go? [y/N]") {
		t.Error("confirm")
	}
}
