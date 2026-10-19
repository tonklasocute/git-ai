package prompt

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tonklasocute/git-quill/internal/git"
	"github.com/tonklasocute/git-quill/internal/provider"
	"github.com/tonklasocute/git-quill/internal/provider/fake"
)

var update = flag.Bool("update", false, "rewrite golden files")

const diffText = `diff --git a/internal/auth/login.go b/internal/auth/login.go
--- a/internal/auth/login.go
+++ b/internal/auth/login.go
@@ -10,3 +10,6 @@ func Login(user, pass string) error {
 	if user == "" {
 		return ErrEmptyUser
 	}
+	if pass == "" {
+		return ErrEmptyPassword
+	}
`

var commits = []git.Commit{
	{Hash: "0123456789abcdef", Subject: "feat(auth): add login"},
	{Hash: "fedcba9876543210", Subject: "fix(auth): reject empty password", Body: "Previously an empty password\nreached the database."},
}

func golden(t *testing.T, name string, r provider.Request) {
	t.Helper()
	got := Render(fake.New(provider.Options{}), r)
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run `go test ./internal/prompt -update`)", err)
	}
	if got != string(want) {
		t.Errorf("%s mismatch (run `go test ./internal/prompt -update` if intended)\n--- got ---\n%s", name, got)
	}
}

func TestGolden(t *testing.T) {
	for _, lang := range []string{"en", "th"} {
		base := Options{Lang: lang, Style: "conventional", MaxSubject: 72, Branch: "feature/login"}
		t.Run(lang, func(t *testing.T) {
			golden(t, "commit_"+lang, Commit(base, diffText))

			full := base
			full.Candidates, full.Emoji, full.Type, full.NoBody = 3, true, "fix", true
			full.Context = "closes the empty-password bug"
			full.Custom = "Mention the Jira key from the branch if present."
			full.Excluded = []string{"go.sum (ignore pattern)", "logo.png (binary)"}
			full.Note = "context lines were reduced to 0 to fit the token budget"
			golden(t, "commit_options_"+lang, Commit(full, diffText))

			simple := base
			simple.Style = "simple"
			golden(t, "commit_simple_"+lang, Commit(simple, diffText))

			golden(t, "commit_summaries_"+lang, CommitFromSummaries(base, "- internal/auth/login.go: +3/-0 lines; reject empty passwords"))

			pr := base
			pr.Base = "main"
			golden(t, "pr_"+lang, PR(pr, commits, diffText, false))
			golden(t, "pr_summaries_"+lang, PR(pr, commits, "- a.go: +1/-0 lines; x", true))
			golden(t, "changelog_"+lang, Changelog(base, "v1.2.0", commits))
			golden(t, "review_"+lang, Review(base, "### internal/auth/login.go (modified)\n    13 +\tif pass == \"\" {\n"))
		})
	}
	golden(t, "summarize", Summarize(diffText))
}

func TestPromptContracts(t *testing.T) {
	// The fake provider and the response parsers rely on these phrases.
	en := Commit(Options{Lang: "en", Style: "conventional", Candidates: 2, Type: "docs", NoBody: true, Emoji: true}, "x")
	for _, want := range []string{"Conventional Commits", "exactly 2 alternative", "type MUST be `docs`", "subject line only", "gitmoji", "line containing only ==="} {
		if !strings.Contains(en.System, want) {
			t.Errorf("system prompt lost contract phrase %q", want)
		}
	}
	if strings.Contains(en.System, "Thai") {
		t.Error("English prompt must not mention Thai")
	}
	if th := Commit(Options{Lang: "th"}, "x"); !strings.Contains(th.System, "Thai") {
		t.Error("Thai prompt must say Thai")
	}
	if s := Commit(Options{Style: "simple"}, "x"); strings.Contains(s.System, "Conventional Commits") || !strings.Contains(s.System, "at most 72") {
		t.Error("simple style")
	}
	if FormatCommits(nil) != "(none)\n" {
		t.Error("FormatCommits(nil)")
	}
	if !strings.Contains(Render(nil, Summarize("x")), "(none)") {
		t.Error("Render without provider")
	}
	if Summarize("x").Task != provider.TaskSummarize || Review(Options{}, "x").Task != provider.TaskReview {
		t.Error("tasks")
	}
}
