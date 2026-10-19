// Package git is a thin wrapper that shells out to the git binary. Shelling
// out (instead of go-git) gives byte-for-byte the same diffs, hooks and
// config resolution the user sees in their terminal.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrNotRepo is returned when the working directory is not inside a git repo.
var ErrNotRepo = errors.New("not a git repository (run this inside a repo, or `git init` first)")

// Repo runs git commands in Dir.
type Repo struct {
	Dir string
	// Env is appended to os.Environ() for every git invocation.
	Env []string
}

// Open returns a Repo for dir after checking that dir is inside a work tree.
func Open(ctx context.Context, dir string) (*Repo, error) {
	r := &Repo{Dir: dir}
	out, err := r.Run(ctx, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(out) != "true" {
		return nil, ErrNotRepo
	}
	return r, nil
}

// Error carries git's stderr so callers can show something useful.
type Error struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), msg)
}

func (e *Error) Unwrap() error { return e.Err }

// Run executes git with args and returns stdout.
func (r *Repo) Run(ctx context.Context, args ...string) (string, error) {
	return r.RunInput(ctx, nil, args...)
}

// RunInput is Run with stdin.
func (r *Repo) RunInput(ctx context.Context, stdin []byte, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Dir
	cmd.Env = append(os.Environ(), r.Env...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), &Error{Args: args, Stderr: stderr.String(), Err: err}
	}
	return stdout.String(), nil
}

// Root returns the absolute path of the work tree root.
func (r *Repo) Root(ctx context.Context) (string, error) {
	out, err := r.Run(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return filepath.FromSlash(strings.TrimSpace(out)), nil
}

// diffArgs are shared by every diff we feed to a model: no colour, no
// external diff drivers or textconv (which could run arbitrary programs or
// leak decrypted content), rename detection on.
func diffArgs(contextLines int) []string {
	if contextLines < 0 {
		contextLines = 3
	}
	// core.quotePath=false keeps non-ASCII (e.g. Thai) file names readable.
	return []string{"-c", "core.quotePath=false", "diff", "--no-color", "--no-ext-diff", "--no-textconv",
		"--find-renames", "-U" + strconv.Itoa(contextLines)}
}

// StagedDiff returns `git diff --cached` with the given context lines.
func (r *Repo) StagedDiff(ctx context.Context, contextLines int) (string, error) {
	return r.Run(ctx, append(diffArgs(contextLines), "--cached")...)
}

// RangeDiff returns the diff between the merge base of base and head, and
// head (i.e. `git diff base...head`), which is what a PR shows.
func (r *Repo) RangeDiff(ctx context.Context, base, head string, contextLines int) (string, error) {
	return r.Run(ctx, append(diffArgs(contextLines), base+"..."+head)...)
}

// StagedFiles lists paths in the index that differ from HEAD.
func (r *Repo) StagedFiles(ctx context.Context) ([]string, error) {
	out, err := r.Run(ctx, "diff", "--cached", "--name-only", "-z")
	if err != nil {
		return nil, err
	}
	return splitNul(out), nil
}

// HasUnstagedChanges reports whether the work tree has changes not in the index.
func (r *Repo) HasUnstagedChanges(ctx context.Context) bool {
	out, err := r.Run(ctx, "status", "--porcelain")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if len(line) >= 2 && (line[1] != ' ' || line[0] == '?') {
			return true
		}
	}
	return false
}

// CurrentBranch returns the short name of HEAD, or "" when detached.
func (r *Repo) CurrentBranch(ctx context.Context) (string, error) {
	out, err := r.Run(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return "", nil //nolint:nilerr // detached HEAD is not an error for callers
	}
	return strings.TrimSpace(out), nil
}

// RefExists reports whether ref resolves to a commit.
func (r *Repo) RefExists(ctx context.Context, ref string) bool {
	_, err := r.Run(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

// DefaultBase guesses the branch a PR would target: origin's HEAD if known,
// then main, master, origin/main, origin/master.
func (r *Repo) DefaultBase(ctx context.Context) (string, error) {
	if out, err := r.Run(ctx, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if ref := strings.TrimSpace(out); ref != "" {
			// Prefer the local branch of the same name when it exists.
			if local := strings.TrimPrefix(ref, "origin/"); r.RefExists(ctx, local) {
				return local, nil
			}
			return ref, nil
		}
	}
	for _, c := range []string{"main", "master", "origin/main", "origin/master"} {
		if r.RefExists(ctx, c) {
			return c, nil
		}
	}
	return "", errors.New("could not detect a base branch (no main/master); pass --base <branch>")
}

// Commit is one entry from Log.
type Commit struct {
	Hash    string
	Subject string
	Body    string
}

// Log returns commits reachable from to but not from from (from..to), oldest
// first. An empty from means "from the root". Merge commits are skipped.
func (r *Repo) Log(ctx context.Context, from, to string) ([]Commit, error) {
	if to == "" {
		to = "HEAD"
	}
	rng := to
	if from != "" {
		rng = from + ".." + to
	}
	out, err := r.Run(ctx, "log", "--no-merges", "--reverse", "--format=%H%x1f%s%x1f%b%x1e", rng)
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for _, rec := range strings.Split(out, "\x1e") {
		rec = strings.TrimLeft(rec, "\n")
		if rec == "" {
			continue
		}
		parts := strings.SplitN(rec, "\x1f", 3)
		if len(parts) < 2 {
			continue
		}
		c := Commit{Hash: parts[0], Subject: parts[1]}
		if len(parts) == 3 {
			c.Body = strings.TrimSpace(parts[2])
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// LastTag returns the most recent tag reachable from HEAD, or "" if none.
func (r *Repo) LastTag(ctx context.Context) string {
	out, err := r.Run(ctx, "describe", "--tags", "--abbrev=0")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// CommitWithMessage runs `git commit -F <file>` with the message, passing
// extra args through. Output goes to the given writers so hooks and gpg
// prompts behave as usual.
func (r *Repo) CommitWithMessage(ctx context.Context, message string, stdout, stderr *os.File, extra ...string) error {
	f, err := os.CreateTemp("", "git-quill-msg-*.txt")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(message); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	args := append([]string{"commit", "--cleanup=strip", "-F", f.Name()}, extra...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Dir
	cmd.Env = append(os.Environ(), r.Env...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	var errBuf bytes.Buffer
	if stderr != nil {
		cmd.Stderr = stderr
	} else {
		cmd.Stderr = &errBuf
	}
	if err := cmd.Run(); err != nil {
		return &Error{Args: args, Stderr: errBuf.String(), Err: err}
	}
	return nil
}

// HooksDir returns the directory git runs hooks from, honouring core.hooksPath.
func (r *Repo) HooksDir(ctx context.Context) (string, error) {
	out, err := r.Run(ctx, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return "", err
	}
	p := filepath.FromSlash(strings.TrimSpace(out))
	if !filepath.IsAbs(p) {
		p = filepath.Join(r.Dir, p)
	}
	return p, nil
}

// ConfigGet returns a git config value or "" when unset.
func (r *Repo) ConfigGet(ctx context.Context, key string) string {
	out, err := r.Run(ctx, "config", "--get", key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func splitNul(s string) []string {
	var out []string
	for _, p := range strings.Split(s, "\x00") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
