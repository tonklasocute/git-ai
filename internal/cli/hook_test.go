package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHookInstallUninstall(t *testing.T) {
	tr := setup(t, "")
	hook := filepath.Join(tr.Dir, ".git", "hooks", "prepare-commit-msg")

	_, errOut, code := runCLI(t, "", "hook", "install")
	if code != 0 || !strings.Contains(errOut, "installed") {
		t.Fatalf("install: %d %s", code, errOut)
	}
	b, _ := os.ReadFile(hook)
	if !strings.Contains(string(b), hookMarker) || !strings.Contains(string(b), `hook run "$1" "$2" "$3"`) {
		t.Errorf("script:\n%s", b)
	}
	if fi, _ := os.Stat(hook); runtime.GOOS != "windows" && fi.Mode()&0o111 == 0 {
		t.Error("hook not executable")
	}
	_, errOut, _ = runCLI(t, "", "hook", "install")
	if !strings.Contains(errOut, "updated") {
		t.Errorf("reinstall: %s", errOut)
	}
	_, errOut, code = runCLI(t, "", "hook", "uninstall")
	if code != 0 || !strings.Contains(errOut, "removed") {
		t.Errorf("uninstall: %s", errOut)
	}
	_, errOut, _ = runCLI(t, "", "hook", "uninstall")
	if !strings.Contains(errOut, "no prepare-commit-msg hook") {
		t.Errorf("uninstall twice: %s", errOut)
	}

	// A foreign hook is protected unless --force, and restored on uninstall.
	_ = os.WriteFile(hook, []byte("#!/bin/sh\necho mine\n"), 0o755)
	_, errOut, code = runCLI(t, "", "hook", "install")
	if code == 0 || !strings.Contains(errOut, "--force") {
		t.Errorf("foreign hook: %d %s", code, errOut)
	}
	_, errOut, code = runCLI(t, "", "hook", "uninstall")
	if code == 0 || !strings.Contains(errOut, "not installed by git-quill") {
		t.Errorf("uninstall foreign: %s", errOut)
	}
	if _, _, code = runCLI(t, "", "hook", "install", "--force"); code != 0 {
		t.Fatal("force install failed")
	}
	if b, _ := os.ReadFile(hook + ".bak"); string(b) != "#!/bin/sh\necho mine\n" {
		t.Errorf("backup = %q", b)
	}
	_, errOut, _ = runCLI(t, "", "hook", "uninstall")
	if b, _ := os.ReadFile(hook); string(b) != "#!/bin/sh\necho mine\n" || !strings.Contains(errOut, "restored") {
		t.Errorf("restore: %q %s", b, errOut)
	}

	// core.hooksPath is honoured.
	custom := filepath.Join(tr.Dir, "githooks")
	tr.Git("config", "core.hooksPath", custom)
	if _, errOut, code = runCLI(t, "", "hook", "install"); code != 0 {
		t.Fatal(errOut)
	}
	if _, err := os.Stat(filepath.Join(custom, "prepare-commit-msg")); err != nil {
		t.Errorf("hooksPath not used: %v", err)
	}
}

func TestHookRun(t *testing.T) {
	tr := setup(t, "")
	msg := filepath.Join(t.TempDir(), "COMMIT_EDITMSG")
	gitComments := "# Please enter the commit message for your changes.\n#\n# Changes to be committed:\n#\tnew file:   x.go\n"
	write := func(s string) { _ = os.WriteFile(msg, []byte(s), 0o644) }
	read := func() string { b, _ := os.ReadFile(msg); return string(b) }

	// Nothing staged → warning, message untouched, exit 0.
	write("\n" + gitComments)
	_, errOut, code := runCLI(t, "", "hook", "run", msg, "")
	if code != 0 || !strings.Contains(errOut, "nothing staged") || read() != "\n"+gitComments {
		t.Errorf("empty stage: %d %s %q", code, errOut, read())
	}

	tr.Stage(map[string]string{"cmd/tool/x.go": "package main\n"})
	_, _, code = runCLI(t, "", "hook", "run", msg)
	got := read()
	if code != 0 || !strings.HasPrefix(got, "feat(tool): add x") || !strings.Contains(got, "Suggested by git-quill") || !strings.HasSuffix(got, gitComments) {
		t.Errorf("hook run:\n%s", got)
	}

	for _, source := range []string{"message", "merge", "squash", "commit"} {
		write("user text\n")
		runCLI(t, "", "hook", "run", msg, source, "HEAD")
		if read() != "user text\n" {
			t.Errorf("source %s should be skipped", source)
		}
	}
	write("Ticket: \n# template comment\n")
	runCLI(t, "", "hook", "run", msg, "template")
	if read() != "Ticket: \n# template comment\n" {
		t.Error("template with content should be kept")
	}
	write("# only comments\n")
	runCLI(t, "", "hook", "run", msg, "template")
	if !strings.HasPrefix(read(), "feat(tool)") {
		t.Errorf("comment-only template should be filled: %q", read())
	}

	t.Setenv("GIT_QUILL_FAKE_ERROR", "auth")
	write("")
	_, errOut, code = runCLI(t, "", "hook", "run", msg)
	if code != 0 || !strings.Contains(errOut, "invalid API key") || !strings.Contains(errOut, "continuing without") {
		t.Errorf("provider error must not block: %d %s", code, errOut)
	}
	if _, _, code = runCLI(t, "", "hook", "run", filepath.Join(t.TempDir(), "missing")); code != 0 {
		t.Error("missing msg file must not fail")
	}
}

// TestHookEndToEnd builds the real binary and runs a plain `git commit`.
func TestHookEndToEnd(t *testing.T) {
	if testing.Short() || runtime.GOOS == "windows" {
		t.Skip("builds the binary")
	}
	// Build before setup() swaps HOME, so the real module cache is used.
	bin := filepath.Join(t.TempDir(), "git-quill")
	_, self, _, _ := runtime.Caller(0)
	build := exec.Command("go", "build", "-o", bin, "./cmd/git-quill")
	build.Dir = moduleRoot(t, filepath.Dir(self))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	tr := setup(t, "")
	install := exec.Command(bin, "hook", "install")
	install.Dir = tr.Dir
	if out, err := install.CombinedOutput(); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	tr.Stage(map[string]string{"internal/billing/invoice.go": "package billing\n"})
	t.Setenv("GIT_EDITOR", "true") // accept the pre-filled message as-is
	tr.Git("commit", "-q")
	if got := tr.Git("log", "-1", "--format=%s"); strings.TrimSpace(got) != "feat(billing): add invoice" {
		t.Errorf("hook-generated subject = %q", got)
	}
	tr.Stage(map[string]string{"b.txt": "b\n"})
	tr.Git("commit", "-q", "-m", "chore: manual message")
	if got := tr.Git("log", "-1", "--format=%s"); strings.TrimSpace(got) != "chore: manual message" {
		t.Errorf("-m must win: %q", got)
	}
	tr.Git("commit", "-q", "--amend", "--no-edit")
	if got := tr.Git("log", "-1", "--format=%s"); strings.TrimSpace(got) != "chore: manual message" {
		t.Errorf("amend must keep message: %q", got)
	}
}

func moduleRoot(t *testing.T, dir string) string {
	t.Helper()
	for d := dir; d != filepath.Dir(d); d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	t.Fatal("go.mod not found")
	return ""
}
