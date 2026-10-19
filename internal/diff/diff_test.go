package diff

import (
	"strings"
	"testing"
)

const sample = `diff --git a/internal/auth/login.go b/internal/auth/login.go
index 1111111..2222222 100644
--- a/internal/auth/login.go
+++ b/internal/auth/login.go
@@ -10,6 +10,9 @@ func Login(user, pass string) error {
 	if user == "" {
 		return ErrEmptyUser
 	}
+	if pass == "" {
+		return ErrEmptyPassword
+	}
 	return check(user, pass)
-	// TODO
 }
diff --git a/docs/new file.md b/docs/new file.md
new file mode 100644
index 0000000..3333333
--- /dev/null
+++ b/docs/new file.md
@@ -0,0 +1,2 @@
+# Title
+text
\ No newline at end of file
diff --git a/old.txt b/old.txt
deleted file mode 100644
index 4444444..0000000
--- a/old.txt
+++ /dev/null
@@ -1 +0,0 @@
-bye
diff --git a/a/before.go b/b/after.go
similarity index 90%
rename from a/before.go
rename to b/after.go
index 5555555..6666666 100644
--- a/a/before.go
+++ b/b/after.go
@@ -1 +1 @@
-package a
+package b
diff --git a/logo.png b/logo.png
new file mode 100644
index 0000000..7777777
Binary files /dev/null and b/logo.png differ
diff --git "a/\340\270\227\340\270\224\340\270\252\340\270\255\340\270\232.txt" "b/\340\270\227\340\270\224\340\270\252\340\270\255\340\270\232.txt"
new file mode 100644
index 0000000..8888888
--- /dev/null
+++ "b/\340\270\227\340\270\224\340\270\252\340\270\255\340\270\232.txt"
@@ -0,0 +1 @@
+สวัสดี
`

func TestParse(t *testing.T) {
	files := Parse("leading noise\n" + sample)
	if len(files) != 6 {
		t.Fatalf("got %d files", len(files))
	}
	tests := []struct {
		path           string
		status         Status
		binary         bool
		added, deleted int
		hunks          int
	}{
		{"internal/auth/login.go", Modified, false, 3, 1, 1},
		{"docs/new file.md", Added, false, 2, 0, 1},
		{"old.txt", Deleted, false, 0, 1, 1},
		{"b/after.go", Renamed, false, 1, 1, 1},
		{"logo.png", Added, true, 0, 0, 0},
		{"ทดสอบ.txt", Added, false, 1, 0, 1},
	}
	for i, tt := range tests {
		f := files[i]
		if f.Path() != tt.path || f.Status != tt.status || f.Binary != tt.binary ||
			f.Added != tt.added || f.Deleted != tt.deleted || len(f.Hunks) != tt.hunks {
			t.Errorf("file %d = {%q %s bin=%v +%d -%d hunks=%d}, want %+v",
				i, f.Path(), f.Status, f.Binary, f.Added, f.Deleted, len(f.Hunks), tt)
		}
	}
	if files[3].OldPath != "a/before.go" {
		t.Errorf("rename old path = %q", files[3].OldPath)
	}
	if h := files[0].Hunks[0]; h.OldStart != 10 || h.NewStart != 10 {
		t.Errorf("hunk starts = %d,%d", h.OldStart, h.NewStart)
	}
}

func TestRenderRoundTrip(t *testing.T) {
	files := Parse(sample)
	if got := Render(files); got != sample {
		t.Errorf("round trip mismatch:\n%s", got)
	}
	if Parse("") != nil {
		t.Error("empty diff should parse to nil")
	}
}

func TestParseCRLFAndAmbiguousHeader(t *testing.T) {
	d := "diff --git a/x y b/x y\r\n--- a/x y\r\n+++ b/x y\r\n@@ -1 +1 @@\r\n-a\r\n+b\r\n"
	f := Parse(d)
	if len(f) != 1 || f[0].Path() != "x y" || f[0].Added != 1 {
		t.Fatalf("got %+v", f)
	}
	// Header only (mode change): split falls back to " b/".
	f = Parse("diff --git a/run.sh b/run.sh\nold mode 100644\nnew mode 100755\n")
	if len(f) != 1 || f[0].Path() != "run.sh" || len(f[0].Hunks) != 0 {
		t.Fatalf("mode change parse: %+v", f)
	}
	a, b := splitGitHeader("a/one b/two")
	if a != "one" || b != "two" {
		t.Errorf("split = %q %q", a, b)
	}
	if a, b := splitGitHeader("weird"); a != "weird" || b != "weird" {
		t.Errorf("fallback split = %q %q", a, b)
	}
}

func TestNumbered(t *testing.T) {
	files := Parse(sample)
	got := files[0].Numbered()
	for _, want := range []string{
		"### internal/auth/login.go (modified)",
		"    13 +\tif pass == \"\" {",
		"    16  \treturn check(user, pass)",
		"       -\t// TODO",
		"    17  }",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Numbered missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(files[1].Numbered(), `       \ No newline`) {
		t.Error("no-newline marker should have blank gutter")
	}
}

func TestStat(t *testing.T) {
	got := Stat(Parse(sample))
	for _, want := range []string{"M    3+    1-  internal/auth/login.go", "R    1+    1-  a/before.go → b/after.go", "D    0+    1-  old.txt"} {
		if !strings.Contains(got, want) {
			t.Errorf("Stat missing %q:\n%s", want, got)
		}
	}
}
