// Package gittest creates throwaway git repositories for tests.
package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Env isolates git from the developer's global and system config so tests
// are reproducible (no signing, no hooksPath, no templates).
func Env(t testing.TB) []string {
	t.Helper()
	home := t.TempDir()
	return []string{
		"GIT_CONFIG_GLOBAL=" + filepath.Join(home, "gitconfig"),
		"GIT_CONFIG_NOSYSTEM=1",
		"HOME=" + home,
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_TERMINAL_PROMPT=0",
	}
}

// Repo is a temp repository.
type Repo struct {
	t   testing.TB
	Dir string
	Env []string
}

// New initialises an empty repo on branch main.
func New(t testing.TB) *Repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	r := &Repo{t: t, Dir: t.TempDir(), Env: Env(t)}
	r.Git("init", "-q", "-b", "main")
	r.Git("config", "commit.gpgsign", "false")
	r.Git("config", "tag.gpgsign", "false")
	r.Git("config", "core.autocrlf", "false")
	return r
}

// Git runs a git command and fails the test on error.
func (r *Repo) Git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Dir
	cmd.Env = append(os.Environ(), r.Env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// Write creates or overwrites a file relative to the repo root.
func (r *Repo) Write(path, content string) {
	r.t.Helper()
	full := filepath.Join(r.Dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// Stage writes files and `git add`s them.
func (r *Repo) Stage(files map[string]string) {
	r.t.Helper()
	for p, c := range files {
		r.Write(p, c)
		r.Git("add", "--", p)
	}
}

// Commit stages files and commits them with msg.
func (r *Repo) Commit(msg string, files map[string]string) {
	r.t.Helper()
	r.Stage(files)
	r.Git("commit", "-q", "--allow-empty", "-m", msg)
}

// Setenv applies the isolation env to the current process for code under
// test that shells out to git itself (e.g. the CLI end-to-end tests).
func (r *Repo) Setenv(t testing.TB) {
	t.Helper()
	for _, kv := range r.Env {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
}
