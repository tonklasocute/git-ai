package git_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tonklasocute/git-quill/internal/git"
	"github.com/tonklasocute/git-quill/internal/git/gittest"
)

func open(t *testing.T, tr *gittest.Repo) *git.Repo {
	t.Helper()
	r, err := git.Open(context.Background(), tr.Dir)
	if err != nil {
		t.Fatal(err)
	}
	r.Env = tr.Env
	return r
}

func TestOpenNotRepo(t *testing.T) {
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(t.TempDir()))
	_, err := git.Open(context.Background(), t.TempDir())
	if !errors.Is(err, git.ErrNotRepo) {
		t.Fatalf("want ErrNotRepo, got %v", err)
	}
}

func TestStagedDiffAndFiles(t *testing.T) {
	tr := gittest.New(t)
	tr.Commit("init", map[string]string{"a.go": "package a\n\nfunc A() {}\n"})
	tr.Stage(map[string]string{
		"a.go":       "package a\n\nfunc A() int { return 1 }\n",
		"dir/new.md": "# hello\n",
	})
	r := open(t, tr)
	ctx := context.Background()

	d, err := r.StagedDiff(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"diff --git a/a.go b/a.go", "+func A() int { return 1 }", "+++ b/dir/new.md"} {
		if !strings.Contains(d, want) {
			t.Errorf("diff missing %q:\n%s", want, d)
		}
	}
	if strings.Contains(d, "\x1b[") {
		t.Error("diff contains colour codes")
	}

	d0, _ := r.StagedDiff(ctx, 0)
	if strings.Contains(d0, "\n package a") {
		t.Errorf("-U0 diff should not contain context lines:\n%s", d0)
	}

	files, err := r.StagedFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(files, ",") != "a.go,dir/new.md" {
		t.Errorf("StagedFiles = %v", files)
	}
}

func TestEmptyStage(t *testing.T) {
	tr := gittest.New(t)
	tr.Commit("init", map[string]string{"a": "1\n"})
	r := open(t, tr)
	files, err := r.StagedFiles(context.Background())
	if err != nil || len(files) != 0 {
		t.Fatalf("files=%v err=%v", files, err)
	}
	if r.HasUnstagedChanges(context.Background()) {
		t.Error("clean tree reported unstaged changes")
	}
	tr.Write("a", "2\n")
	if !r.HasUnstagedChanges(context.Background()) {
		t.Error("modified file not reported")
	}
}

func TestBranchBaseAndRangeDiff(t *testing.T) {
	tr := gittest.New(t)
	tr.Commit("chore: init", map[string]string{"README.md": "hi\n"})
	tr.Git("checkout", "-q", "-b", "feature/login")
	tr.Commit("feat(auth): add login", map[string]string{"auth/login.go": "package auth\n"})
	tr.Commit("fix(auth): handle empty password\n\nReturns an error now.", map[string]string{"auth/login.go": "package auth\n\n// fixed\n"})
	r := open(t, tr)
	ctx := context.Background()

	br, _ := r.CurrentBranch(ctx)
	if br != "feature/login" {
		t.Errorf("CurrentBranch = %q", br)
	}
	base, err := r.DefaultBase(ctx)
	if err != nil || base != "main" {
		t.Fatalf("DefaultBase = %q, %v", base, err)
	}
	d, err := r.RangeDiff(ctx, base, "HEAD", 3)
	if err != nil || !strings.Contains(d, "auth/login.go") || strings.Contains(d, "README.md") {
		t.Fatalf("RangeDiff err=%v\n%s", err, d)
	}
	log, err := r.Log(ctx, base, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != 2 || log[0].Subject != "feat(auth): add login" || log[1].Body != "Returns an error now." {
		t.Fatalf("Log = %+v", log)
	}
	all, _ := r.Log(ctx, "", "")
	if len(all) != 3 {
		t.Errorf("full log len = %d", len(all))
	}

	tr.Git("checkout", "-q", "--detach")
	if br, _ := r.CurrentBranch(ctx); br != "" {
		t.Errorf("detached CurrentBranch = %q", br)
	}
}

func TestDefaultBaseMasterAndNone(t *testing.T) {
	tr := gittest.New(t)
	tr.Commit("init", nil)
	tr.Git("branch", "-m", "trunk")
	r := open(t, tr)
	if _, err := r.DefaultBase(context.Background()); err == nil {
		t.Error("expected error without main/master")
	}
	tr.Git("branch", "master")
	if b, _ := r.DefaultBase(context.Background()); b != "master" {
		t.Errorf("got %q", b)
	}
}

func TestLastTag(t *testing.T) {
	tr := gittest.New(t)
	tr.Commit("init", nil)
	r := open(t, tr)
	if tag := r.LastTag(context.Background()); tag != "" {
		t.Errorf("no tags yet, got %q", tag)
	}
	tr.Git("tag", "v0.1.0")
	tr.Commit("feat: x", nil)
	tr.Git("tag", "-a", "v0.2.0", "-m", "v0.2.0")
	tr.Commit("fix: y", nil)
	if tag := r.LastTag(context.Background()); tag != "v0.2.0" {
		t.Errorf("LastTag = %q", tag)
	}
}

func TestCommitWithMessageAndHooksDir(t *testing.T) {
	tr := gittest.New(t)
	tr.Commit("init", nil)
	tr.Stage(map[string]string{"x.txt": "x\n"})
	r := open(t, tr)
	ctx := context.Background()
	msg := "feat: เพิ่มไฟล์ x\n\n# comment stripped\nBody line.\n"
	if err := r.CommitWithMessage(ctx, msg, nil, nil); err != nil {
		t.Fatal(err)
	}
	got := tr.Git("log", "-1", "--format=%B")
	if strings.TrimSpace(got) != "feat: เพิ่มไฟล์ x\n\nBody line." {
		t.Errorf("commit message = %q", got)
	}

	// Nothing staged → git error with stderr surfaced.
	err := r.CommitWithMessage(ctx, "x", nil, nil)
	var ge *git.Error
	if !errors.As(err, &ge) {
		t.Fatalf("want *git.Error, got %v", err)
	}
	if !strings.Contains(err.Error(), "git commit") {
		t.Errorf("error = %v", err)
	}

	hd, err := r.HooksDir(ctx)
	if err != nil || !strings.HasSuffix(filepath.ToSlash(hd), ".git/hooks") {
		t.Errorf("HooksDir = %q, %v", hd, err)
	}
	custom := filepath.Join(tr.Dir, "myhooks")
	_ = os.MkdirAll(custom, 0o755)
	tr.Git("config", "core.hooksPath", custom)
	hd, _ = r.HooksDir(ctx)
	if filepath.Clean(hd) != filepath.Clean(custom) {
		// macOS temp dirs may resolve through /private
		if ev, _ := filepath.EvalSymlinks(hd); ev != mustEval(t, custom) {
			t.Errorf("HooksDir with core.hooksPath = %q, want %q", hd, custom)
		}
	}
	if v := r.ConfigGet(ctx, "core.hooksPath"); v != custom {
		t.Errorf("ConfigGet = %q", v)
	}
	if v := r.ConfigGet(ctx, "nope.nothing"); v != "" {
		t.Errorf("ConfigGet unset = %q", v)
	}
	if root, err := r.Root(ctx); err != nil || mustEval(t, root) != mustEval(t, tr.Dir) {
		t.Errorf("Root = %q, %v", root, err)
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	ev, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func TestErrorMessage(t *testing.T) {
	e := &git.Error{Args: []string{"status"}, Err: errors.New("boom")}
	if e.Error() != "git status: boom" {
		t.Errorf("got %q", e.Error())
	}
}
