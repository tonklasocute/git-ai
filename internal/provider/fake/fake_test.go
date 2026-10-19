package fake

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/tonklasocute/git-quill/internal/provider"
)

const loginDiff = `diff --git a/internal/auth/login.go b/internal/auth/login.go
new file mode 100644
--- /dev/null
+++ b/internal/auth/login.go
@@ -0,0 +1,3 @@
+package auth
+
+func Login() {}
`

func req(task provider.Task, system, user string) provider.Request {
	return provider.Request{Task: task, System: system, Messages: []provider.Message{{Role: provider.User, Content: user}}}
}

func TestCommitDeterministicAndVaried(t *testing.T) {
	c := New(provider.Options{})
	sys := "Write a Conventional Commits message."
	r1, err := c.Stream(context.Background(), req(provider.TaskCommit, sys, loginDiff), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(r1.Text, "feat(auth): add login\n\n- Add internal/auth/login.go") {
		t.Errorf("commit = %q", r1.Text)
	}
	r2, _ := c.Stream(context.Background(), req(provider.TaskCommit, sys, loginDiff), nil)
	if r1.Text == r2.Text {
		t.Error("regenerate should vary")
	}
	if c.Calls() != 2 || len(c.Requests) != 2 {
		t.Error("calls not recorded")
	}
	if !r1.Usage.Estimated || r1.Usage.InputTokens == 0 || c.Name() != "fake" || c.Model() != "fake-1" {
		t.Errorf("meta: %+v", r1)
	}
}

func TestCommitDirectives(t *testing.T) {
	c := New(provider.Options{Model: "m"})
	sys := "Write a Conventional Commits message in Thai. Use gitmoji. The commit type MUST be `fix`. Write the subject line only. Return exactly 3 alternatives"
	var streamed strings.Builder
	r, _ := c.Stream(context.Background(), req(provider.TaskCommit, sys, loginDiff), func(s string) { streamed.WriteString(s) })
	alts := strings.Split(r.Text, "\n===\n")
	if len(alts) != 3 {
		t.Fatalf("alternatives = %q", r.Text)
	}
	if !strings.HasPrefix(alts[0], "🐛 fix(auth): แก้ไขข้อผิดพลาดใน login") || strings.Contains(alts[0], "\n") {
		t.Errorf("alt0 = %q", alts[0])
	}
	if streamed.String() != r.Text {
		t.Error("streamed text differs from final text")
	}
	simple, _ := c.Stream(context.Background(), req(provider.TaskCommit, "plain style", loginDiff), nil)
	if !strings.HasPrefix(simple.Text, "Support login") && !strings.HasPrefix(simple.Text, "Add login") && !strings.HasPrefix(simple.Text, "Introduce login") {
		t.Errorf("simple style = %q", simple.Text)
	}
}

func TestInferType(t *testing.T) {
	tests := []struct {
		files []fileInfo
		want  string
	}{
		{nil, "chore"},
		{[]fileInfo{{path: "README.md"}, {path: "docs/x.txt"}}, "docs"},
		{[]fileInfo{{path: "a/b_test.go"}}, "test"},
		{[]fileInfo{{path: ".github/workflows/ci.yml"}}, "ci"},
		{[]fileInfo{{path: "Makefile"}, {path: "go.mod"}}, "build"},
		{[]fileInfo{{path: "a.go", status: "added"}}, "feat"},
		{[]fileInfo{{path: "a.go", status: "modified"}}, "refactor"},
	}
	for _, tt := range tests {
		if got := inferType(tt.files, ""); got != tt.want {
			t.Errorf("inferType(%v) = %q, want %q", tt.files, got, tt.want)
		}
	}
	if inferType([]fileInfo{{path: "a.go"}}, "+if err != nil {") != "fix" {
		t.Error("fix keyword")
	}
	if s := inferScope([]fileInfo{{path: "a/x"}, {path: "b/x"}, {path: "c/x"}}); s != "" {
		t.Errorf("too many scopes → empty, got %q", s)
	}
	if s := inferScope([]fileInfo{{path: "Makefile"}}); s != "makefile" {
		t.Errorf("root file scope = %q", s)
	}
}

func TestOtherTasks(t *testing.T) {
	c := New(provider.Options{})
	ctx := context.Background()

	sum, _ := c.Stream(ctx, req(provider.TaskSummarize, "", loginDiff), nil)
	if sum.Text != "- internal/auth/login.go: +3/-0 lines; add login" {
		t.Errorf("summary = %q", sum.Text)
	}
	fromSummaries, _ := c.Stream(ctx, req(provider.TaskCommit, "Conventional Commits", sum.Text), nil)
	if !strings.Contains(fromSummaries.Text, "(auth)") {
		t.Errorf("commit from summaries = %q", fromSummaries.Text)
	}

	log := "Commits:\n- abc1234 feat(auth): add login\n- def5678 fix(auth): handle empty password\n- 0001111 chore: tidy\n"
	pr, _ := c.Stream(ctx, req(provider.TaskPR, "", log+loginDiff), nil)
	for _, want := range []string{"feat(auth): add login\n\n## Summary", "- handle empty password", "## Testing", "None."} {
		if !strings.Contains(pr.Text, want) {
			t.Errorf("pr missing %q:\n%s", want, pr.Text)
		}
	}
	prTH, _ := c.Stream(ctx, req(provider.TaskPR, "Thai", log), nil)
	if !strings.Contains(prTH.Text, "## สรุป") {
		t.Errorf("thai pr:\n%s", prTH.Text)
	}

	cl, _ := c.Stream(ctx, req(provider.TaskChangelog, "", log), nil)
	if cl.Text != "### Added\n\n- **auth:** Add login\n\n### Fixed\n\n- **auth:** Handle empty password\n" {
		t.Errorf("changelog = %q", cl.Text)
	}
	empty, _ := c.Stream(ctx, req(provider.TaskChangelog, "Thai", "- chore: x"), nil)
	if !strings.Contains(empty.Text, "ปรับปรุงภายใน") {
		t.Errorf("empty thai changelog = %q", empty.Text)
	}

	numbered := "### app/db.go (modified)\n@@ -1 +1,3 @@\n     1 +password := \"[REDACTED:password]\"\n     2 +// TODO later\n     3 +_ = err\n"
	rv, _ := c.Stream(ctx, req(provider.TaskReview, "", numbered), nil)
	var out struct {
		Findings []struct {
			File, Severity, Category string
			Line                     int
		}
	}
	if err := json.Unmarshal([]byte(rv.Text), &out); err != nil {
		t.Fatalf("review not JSON: %v\n%s", err, rv.Text)
	}
	if len(out.Findings) != 4 || out.Findings[0].Severity != "high" || out.Findings[0].Line != 1 || out.Findings[0].File != "app/db.go" {
		t.Errorf("findings = %+v", out.Findings)
	}
	rvTH, _ := c.Stream(ctx, req(provider.TaskReview, "Thai", numbered), nil)
	if !strings.Contains(rvTH.Text, "ตรวจ 1 ไฟล์") {
		t.Errorf("thai review = %s", rvTH.Text)
	}
}

func TestEnvOverrides(t *testing.T) {
	c := New(provider.Options{})
	t.Setenv("GIT_QUILL_FAKE_RESPONSE", "fixed text")
	t.Setenv("GIT_QUILL_FAKE_DELAY", "1")
	r, _ := c.Stream(context.Background(), req(provider.TaskCommit, "", ""), func(string) {})
	if r.Text != "fixed text" {
		t.Errorf("text = %q", r.Text)
	}
	for kind, want := range map[string]provider.Kind{
		"auth": provider.KindAuth, "not_found": provider.KindNotFound, "rate_limit": provider.KindRateLimit,
		"server": provider.KindServer, "unavailable": provider.KindUnavailable,
	} {
		t.Setenv("GIT_QUILL_FAKE_ERROR", kind)
		_, err := c.Stream(context.Background(), req(provider.TaskCommit, "", ""), nil)
		var pe *provider.Error
		if !errors.As(err, &pe) || pe.Kind != want {
			t.Errorf("%s: err = %v", kind, err)
		}
	}
	t.Setenv("GIT_QUILL_FAKE_ERROR", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Stream(ctx, req(provider.TaskCommit, "", ""), func(string) {}); err == nil {
		t.Error("cancelled context should fail while streaming")
	}
}
