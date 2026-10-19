// Package diff parses unified diffs from git, filters out files that should
// never be sent to a model, and helps fit what remains into a token budget.
package diff

import (
	"fmt"
	"strconv"
	"strings"
)

// Status of a file in a diff.
type Status string

const (
	Modified Status = "modified"
	Added    Status = "added"
	Deleted  Status = "deleted"
	Renamed  Status = "renamed"
)

// Hunk is one @@ section.
type Hunk struct {
	Header   string // the full "@@ -a,b +c,d @@ ctx" line
	OldStart int
	NewStart int
	Lines    []string // each starts with ' ', '+', '-' or '\'
}

// File is one file section of a diff.
type File struct {
	OldPath string
	NewPath string
	Status  Status
	Binary  bool
	Header  []string // lines from "diff --git" up to the first hunk
	Hunks   []Hunk
	Added   int
	Deleted int
}

// Path is the path the file has after the change (or before, if deleted).
func (f *File) Path() string {
	if f.Status == Deleted || f.NewPath == "" {
		return f.OldPath
	}
	return f.NewPath
}

// String renders the file section back to unified-diff text.
func (f *File) String() string {
	var b strings.Builder
	for _, h := range f.Header {
		b.WriteString(h)
		b.WriteByte('\n')
	}
	for _, h := range f.Hunks {
		b.WriteString(h.Header)
		b.WriteByte('\n')
		for _, l := range h.Lines {
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// Numbered renders the file with new-side line numbers in a left gutter so
// a reviewer model can cite exact lines. Removed lines get a blank gutter.
func (f *File) Numbered() string {
	var b strings.Builder
	fmt.Fprintf(&b, "### %s (%s)\n", f.Path(), f.Status)
	for _, h := range f.Hunks {
		b.WriteString(h.Header)
		b.WriteByte('\n')
		n := h.NewStart
		for _, l := range h.Lines {
			switch {
			case strings.HasPrefix(l, "-"):
				fmt.Fprintf(&b, "%6s %s\n", "", l)
			case strings.HasPrefix(l, `\`):
				fmt.Fprintf(&b, "%6s %s\n", "", l)
			default:
				fmt.Fprintf(&b, "%6d %s\n", n, l)
				n++
			}
		}
	}
	return b.String()
}

// Render concatenates files back into one diff.
func Render(files []File) string {
	var b strings.Builder
	for i := range files {
		b.WriteString(files[i].String())
	}
	return b.String()
}

// Parse splits git's unified diff output into files. It is tolerant: text
// before the first "diff --git" line is ignored.
func Parse(s string) []File {
	var (
		files []File
		cur   *File
		hunk  *Hunk
	)
	flushHunk := func() {
		if cur != nil && hunk != nil {
			cur.Hunks = append(cur.Hunks, *hunk)
			hunk = nil
		}
	}
	flushFile := func() {
		flushHunk()
		if cur != nil {
			files = append(files, *cur)
			cur = nil
		}
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	for _, line := range strings.Split(strings.TrimSuffix(s, "\n"), "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			flushFile()
			a, b := splitGitHeader(strings.TrimPrefix(line, "diff --git "))
			cur = &File{OldPath: a, NewPath: b, Status: Modified, Header: []string{line}}
			continue
		}
		if cur == nil {
			continue
		}
		if hunk == nil || strings.HasPrefix(line, "@@ ") {
			if strings.HasPrefix(line, "@@ ") {
				flushHunk()
				hunk = parseHunkHeader(line)
				continue
			}
			parseHeaderLine(cur, line)
			cur.Header = append(cur.Header, line)
			continue
		}
		switch {
		case strings.HasPrefix(line, "+"):
			cur.Added++
		case strings.HasPrefix(line, "-"):
			cur.Deleted++
		}
		hunk.Lines = append(hunk.Lines, line)
	}
	flushFile()
	return files
}

func parseHeaderLine(f *File, line string) {
	switch {
	case strings.HasPrefix(line, "new file mode"):
		f.Status = Added
	case strings.HasPrefix(line, "deleted file mode"):
		f.Status = Deleted
	case strings.HasPrefix(line, "rename from "):
		f.Status = Renamed
		f.OldPath = unquote(strings.TrimPrefix(line, "rename from "))
	case strings.HasPrefix(line, "rename to "):
		f.Status = Renamed
		f.NewPath = unquote(strings.TrimPrefix(line, "rename to "))
	case strings.HasPrefix(line, "--- "):
		if p := stripPrefix(strings.TrimPrefix(line, "--- "), "a/"); p != "/dev/null" {
			f.OldPath = p
		}
	case strings.HasPrefix(line, "+++ "):
		if p := stripPrefix(strings.TrimPrefix(line, "+++ "), "b/"); p != "/dev/null" {
			f.NewPath = p
		}
	case strings.HasPrefix(line, "Binary files ") || line == "GIT binary patch":
		f.Binary = true
	}
}

// splitGitHeader extracts a/ and b/ paths from "a/x b/y". When paths contain
// spaces the header is ambiguous; we prefer the symmetric split (same path on
// both sides), which is correct for everything except renames, and renames
// carry explicit "rename from/to" lines that override this.
func splitGitHeader(rest string) (string, string) {
	if strings.HasPrefix(rest, `"`) {
		// Quoted paths: "a/..." "b/..."
		if i := strings.Index(rest, `" "`); i > 0 {
			return stripPrefix(rest[:i+1], "a/"), stripPrefix(rest[i+2:], "b/")
		}
	}
	if len(rest)%2 == 1 {
		half := len(rest) / 2
		a, b := rest[:half], rest[half+1:]
		if strings.HasPrefix(a, "a/") && strings.HasPrefix(b, "b/") && a[2:] == b[2:] {
			return a[2:], b[2:]
		}
	}
	if i := strings.LastIndex(rest, " b/"); i > 0 {
		return stripPrefix(rest[:i], "a/"), stripPrefix(rest[i+1:], "b/")
	}
	return rest, rest
}

func stripPrefix(p, prefix string) string {
	p = unquote(strings.TrimSpace(p))
	return strings.TrimPrefix(p, prefix)
}

// unquote handles git's C-style quoted paths (core.quotePath).
func unquote(p string) string {
	if len(p) >= 2 && p[0] == '"' && p[len(p)-1] == '"' {
		if u, err := strconv.Unquote(p); err == nil {
			return u
		}
	}
	return p
}

func parseHunkHeader(line string) *Hunk {
	h := &Hunk{Header: line, OldStart: 1, NewStart: 1}
	// @@ -12,3 +14,5 @@ optional context
	fields := strings.Fields(line)
	for _, f := range fields[1:] {
		if f == "@@" {
			break
		}
		num := strings.SplitN(f[1:], ",", 2)[0]
		n, err := strconv.Atoi(num)
		if err != nil {
			continue
		}
		switch f[0] {
		case '-':
			h.OldStart = n
		case '+':
			h.NewStart = n
		}
	}
	return h
}
