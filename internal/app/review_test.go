package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseReview(t *testing.T) {
	text := "Here you go:\n```json\n" + `{"summary":"Two issues.","findings":[
	{"file":"b.go","line":9,"severity":"LOW","category":"Tests","message":"no test"},
	{"file":"a.go","line":20,"severity":"medium","category":"bug","message":"nil map"},
	{"file":"a.go","line":3,"severity":"high","category":"security","message":"sql injection","suggestion":"use args"},
	{"file":"a.go","line":1,"severity":"critical","message":"odd severity"}]}` + "\n```"
	r, err := ParseReview(text)
	if err != nil {
		t.Fatal(err)
	}
	files, groups := r.ByFile()
	if strings.Join(files, ",") != "a.go,b.go" {
		t.Errorf("files = %v", files)
	}
	var order []string
	for _, f := range groups["a.go"] {
		order = append(order, f.Severity)
	}
	if strings.Join(order, ",") != "high,medium,info" {
		t.Errorf("a.go order = %v", order)
	}
	if groups["b.go"][0].Severity != "low" || groups["b.go"][0].Category != "tests" {
		t.Errorf("normalisation: %+v", groups["b.go"][0])
	}
	c := r.Counts()
	if c["high"] != 1 || c["info"] != 1 || !r.Blocking() {
		t.Errorf("counts = %v", c)
	}
	r.Merge(&Review{Summary: "More.", Findings: []Finding{{File: "c.go", Severity: "low"}}})
	if r.Summary != "Two issues. More." || len(r.Findings) != 5 {
		t.Errorf("merge: %+v", r)
	}
	empty := &Review{}
	empty.Merge(&Review{Summary: "x"})
	if empty.Summary != "x" || empty.Blocking() {
		t.Error("merge into empty")
	}
	for _, bad := range []string{"no json here", "{broken", "} {"} {
		if _, err := ParseReview(bad); err == nil {
			t.Errorf("ParseReview(%q) should fail", bad)
		}
	}
}

func TestParsePRAndCleanSection(t *testing.T) {
	cases := map[string][2]string{
		"feat(auth): add login\n\n## Summary\n\nAdds login.":   {"feat(auth): add login", "## Summary\n\nAdds login."},
		"```markdown\n# Title: \"Fix the thing\"\n\nbody\n```": {"Fix the thing", "body"},
		"\n\n**Title:** Improve docs\nbody line":               {"Improve docs", "body line"},
		"":                                                     {"", ""},
	}
	for in, want := range cases {
		title, body := ParsePR(in)
		if title != want[0] || body != want[1] {
			t.Errorf("ParsePR(%q) = %q, %q", in, title, body)
		}
	}
	if got := CleanSection("Sure! Here are the notes:\n\n### Added\n\n- x"); got != "### Added\n\n- x" {
		t.Errorf("CleanSection = %q", got)
	}
	if got := CleanSection("```\n### Fixed\n- y\n```"); got != "### Fixed\n- y" {
		t.Errorf("CleanSection fenced = %q", got)
	}
}

func TestPrependSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CHANGELOG.md")
	read := func() string {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if SectionHeading("", "") != "## [Unreleased]" || SectionHeading("v1.0.0", "2026-10-19") != "## [v1.0.0] - 2026-10-19" {
		t.Error("SectionHeading")
	}

	// New file gets the standard header.
	if err := PrependSection(path, "## [v1.0.0] - 2026-01-01\n\n### Added\n\n- first"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(read(), ChangelogHeader+"\n## [v1.0.0]") {
		t.Errorf("new file:\n%s", read())
	}
	// Unreleased goes on top.
	if err := PrependSection(path, "## [Unreleased]\n\n### Fixed\n\n- wip"); err != nil {
		t.Fatal(err)
	}
	// A version goes below Unreleased.
	if err := PrependSection(path, "## [v1.1.0] - 2026-02-01\n\n### Added\n\n- second"); err != nil {
		t.Fatal(err)
	}
	// Regenerating Unreleased replaces it.
	if err := PrependSection(path, "## [Unreleased]\n\n### Fixed\n\n- wip v2"); err != nil {
		t.Fatal(err)
	}
	got := read()
	iU, i11, i10 := strings.Index(got, "## [Unreleased]"), strings.Index(got, "## [v1.1.0]"), strings.Index(got, "## [v1.0.0]")
	if iU > i11 || i11 > i10 || strings.Count(got, "## [Unreleased]") != 1 || !strings.Contains(got, "wip v2") || strings.Contains(got, "- wip\n") {
		t.Errorf("ordering/replacement wrong:\n%s", got)
	}
	// Duplicate version is refused.
	if err := PrependSection(path, "## [v1.1.0] - 2026-03-01\n\n- again"); err == nil || !strings.Contains(err.Error(), "already has a section") {
		t.Errorf("duplicate: %v", err)
	}

	// Existing file without any sections, and one with only Unreleased.
	p2 := filepath.Join(dir, "NOTES.md")
	_ = os.WriteFile(p2, []byte("# Notes\n"), 0o644)
	if err := PrependSection(p2, "## [v2] - d\n\n- x"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p2); string(b) != "# Notes\n\n## [v2] - d\n\n- x\n" {
		t.Errorf("no sections: %q", b)
	}
	p3 := filepath.Join(dir, "ONLYU.md")
	_ = os.WriteFile(p3, []byte("# C\n\n## [Unreleased]\n\n- u\n"), 0o644)
	if err := PrependSection(p3, "## [v3] - d\n\n- y"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p3); !strings.HasSuffix(string(b), "- u\n\n## [v3] - d\n\n- y\n") {
		t.Errorf("only unreleased: %q", b)
	}
	if err := PrependSection(dir, "## [x]"); err == nil {
		t.Error("directory path should fail")
	}
}
