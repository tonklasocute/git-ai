package diff

import (
	"bufio"
	"os"
	"regexp"
	"strings"
)

// Matcher implements the useful subset of .gitignore semantics: globs with
// *, ?, ** and [classes], directory-only patterns ("dir/"), anchored
// patterns ("/x" or any pattern containing a slash) and negation ("!x").
// The last matching pattern wins, as in git.
type Matcher struct {
	rules []rule
}

type rule struct {
	re      *regexp.Regexp
	negate  bool
	dirOnly bool
}

// NewMatcher compiles patterns. Blank lines and "#" comments are skipped.
func NewMatcher(patterns ...string) *Matcher {
	m := &Matcher{}
	m.Add(patterns...)
	return m
}

// Add appends more patterns (later patterns take precedence).
func (m *Matcher) Add(patterns ...string) {
	for _, p := range patterns {
		if r, ok := compile(p); ok {
			m.rules = append(m.rules, r)
		}
	}
}

// AddFile appends patterns from a file; a missing file is not an error.
func (m *Matcher) AddFile(path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		m.Add(sc.Text())
	}
	return sc.Err()
}

// Match reports whether path (slash-separated, relative to the repo root)
// is excluded.
func (m *Matcher) Match(path string) bool {
	path = strings.TrimPrefix(path, "./")
	// Every parent directory is a candidate too: "vendor/" excludes
	// vendor/a/b.go.
	parts := strings.Split(path, "/")
	matched := false
	for _, r := range m.rules {
		hit := false
		for i := 1; i <= len(parts); i++ {
			cand := strings.Join(parts[:i], "/")
			isDir := i < len(parts)
			if r.dirOnly && !isDir {
				continue
			}
			if r.re.MatchString(cand) {
				hit = true
				break
			}
		}
		if hit {
			matched = !r.negate
		}
	}
	return matched
}

func compile(p string) (rule, bool) {
	p = strings.TrimRight(p, " \t\r")
	if p == "" || strings.HasPrefix(p, "#") {
		return rule{}, false
	}
	var r rule
	if strings.HasPrefix(p, "!") {
		r.negate = true
		p = p[1:]
	}
	p = strings.TrimPrefix(p, `\`) // escaped leading ! or #
	if strings.HasSuffix(p, "/") {
		r.dirOnly = true
		p = strings.TrimSuffix(p, "/")
	}
	anchored := strings.Contains(p, "/")
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return rule{}, false
	}
	body := globToRegexp(p)
	if anchored {
		body = "^" + body + "$"
	} else {
		body = "^(?:.*/)?" + body + "$"
	}
	re, err := regexp.Compile(body)
	if err != nil {
		return rule{}, false
	}
	r.re = re
	return r, true
}

func globToRegexp(g string) string {
	var b strings.Builder
	for i := 0; i < len(g); i++ {
		c := g[i]
		switch c {
		case '*':
			if i+1 < len(g) && g[i+1] == '*' {
				// "**/" matches zero or more directories; "/**" everything below.
				if i+2 < len(g) && g[i+2] == '/' {
					b.WriteString("(?:.*/)?")
					i += 2
				} else {
					b.WriteString(".*")
					i++
				}
				continue
			}
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		case '[':
			if j := strings.IndexByte(g[i:], ']'); j > 1 {
				class := g[i+1 : i+j]
				if strings.HasPrefix(class, "!") {
					class = "^" + class[1:]
				}
				b.WriteString("[" + class + "]")
				i += j
				continue
			}
			b.WriteString(`\[`)
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return b.String()
}
