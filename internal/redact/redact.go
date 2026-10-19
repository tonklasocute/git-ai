// Package redact removes secrets from diffs and other text before anything
// is sent to a model. It favours catching real secrets over avoiding the
// occasional false positive, but has explicit guards for the common false
// positives in source code (env lookups, identifiers, placeholders).
package redact

import (
	"fmt"
	"math"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Finding records one redaction.
type Finding struct {
	Type string
	File string // "" for non-file text such as commit messages
	Line int    // 1-based line within the scanned text
}

// Marker returns the replacement text for a secret of type t.
func Marker(t string) string { return "[REDACTED:" + t + "]" }

type patternRule struct {
	typ   string
	re    *regexp.Regexp
	group int // submatch to replace; 0 = whole match
}

// Rules are ordered: specific token formats first, so their type names win
// over the generic assignment rule.
var patternRules = []patternRule{
	{"aws-access-key-id", regexp.MustCompile(`\b((?:AKIA|ASIA|ABIA|ACCA|AGPA|AIDA|AROA|ANPA|ANVA|AIPA)[A-Z0-9]{16})\b`), 1},
	{"aws-secret-access-key", regexp.MustCompile(`(?i)(?:aws_?secret_?access_?key|aws_?secret_?key|secret_?access_?key)["']?\s*(?::=|=>|=|:)\s*["']?([A-Za-z0-9/+=]{40})\b`), 1},
	{"github-token", regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{36,255})\b`), 1},
	{"github-token", regexp.MustCompile(`\b(github_pat_[A-Za-z0-9_]{22,255})\b`), 1},
	{"gitlab-token", regexp.MustCompile(`\b(gl(?:pat|dt|ptt|rt|soat|ffct|cbt|imt|agent)-[A-Za-z0-9_\-]{20,})`), 1},
	{"slack-token", regexp.MustCompile(`\b(xox[abposre]-[A-Za-z0-9-]{10,})`), 1},
	{"slack-token", regexp.MustCompile(`\b(xapp-\d-[A-Za-z0-9-]{10,})`), 1},
	{"slack-webhook", regexp.MustCompile(`(https://hooks\.slack\.com/(?:services|workflows|triggers)/[A-Za-z0-9+/_\-]{20,})`), 1},
	{"anthropic-api-key", regexp.MustCompile(`\b(sk-ant-[A-Za-z0-9_\-]{32,})`), 1},
	{"openai-api-key", regexp.MustCompile(`\b(sk-(?:proj-|svcacct-|admin-)?[A-Za-z0-9_\-]{32,})`), 1},
	{"google-api-key", regexp.MustCompile(`\b(AIza[0-9A-Za-z_\-]{35})`), 1},
	{"stripe-key", regexp.MustCompile(`\b((?:sk|rk)_(?:live|test)_[0-9A-Za-z]{24,})`), 1},
	{"npm-token", regexp.MustCompile(`\b(npm_[A-Za-z0-9]{36})\b`), 1},
	{"jwt", regexp.MustCompile(`\b(eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,})`), 1},
	{"bearer-token", regexp.MustCompile(`(?i)\bbearer\s+([A-Za-z0-9\-._~+/]{20,}=*)`), 1},
	{"basic-auth", regexp.MustCompile(`(?i)\bbasic\s+([A-Za-z0-9+/]{16,}={0,2})(?:[^A-Za-z0-9+/=]|$)`), 1},
	{"url-credentials", regexp.MustCompile(`\b[a-zA-Z][a-zA-Z0-9+.\-]*://[^\s:/@'"]+:([^\s@/'"]+)@`), 1},
}

var (
	privateKeyBegin = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----`)
	privateKeyEnd   = regexp.MustCompile(`-----END [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----`)

	// keyword = value. The value itself is parsed by hand (see parseValue)
	// because Go regexps cannot express "quoted string with escapes" plus
	// the exclusions we need.
	assignRe = regexp.MustCompile(`(?i)["']?\b([A-Za-z0-9_.\-]*(?:passwd|password|passphrase|pwd|secret|token|api[_\-]?key|apikey|access[_\-]?key|private[_\-]?key|auth[_\-]?key|credentials?)[A-Za-z0-9_.\-]*)["']?[ \t]*(:=|=>|==|!=|=|:)[ \t]*`)

	envLineRe  = regexp.MustCompile(`^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_.\-]*)\s*=\s*(.*)$`)
	identRe    = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*(?:\.[A-Za-z_$][A-Za-z0-9_$]*)*$`)
	numericRe  = regexp.MustCompile(`^[0-9][0-9_.]*$`)
	xPlaceRe   = regexp.MustCompile(`^(?i)(?:x+|\*+|\.+|-+|_+|0+)$`)
	digitRe    = regexp.MustCompile(`[0-9]`)
	letterRe   = regexp.MustCompile(`[A-Za-z]`)
	configExts = map[string]bool{
		".env": true, ".ini": true, ".cfg": true, ".conf": true, ".properties": true,
		".yaml": true, ".yml": true, ".toml": true, ".tfvars": true, ".npmrc": true,
		".pypirc": true, ".netrc": true,
	}
)

// Redactor scans text. The zero value is not usable; call New.
type Redactor struct {
	// EntropyThreshold (bits/char) above which a .env value counts as a secret.
	EntropyThreshold float64
	// MinEntropyLen is the minimum value length for the entropy check.
	MinEntropyLen int
}

// New returns a Redactor with default thresholds.
func New() *Redactor {
	return &Redactor{EntropyThreshold: 3.5, MinEntropyLen: 16}
}

// Diff redacts one file section of a unified diff. Lines are expected to
// carry the diff prefix (' ', '+', '-'); file is used for file-type rules
// (e.g. entropy scanning of .env files) and for reporting.
func (r *Redactor) Diff(file, text string) (string, []Finding) {
	return r.scan(file, text, true)
}

// Text redacts free text such as commit messages or user-supplied context.
func (r *Redactor) Text(text string) (string, []Finding) {
	return r.scan("", text, false)
}

func (r *Redactor) scan(file, text string, diffMode bool) (string, []Finding) {
	lines := strings.Split(text, "\n")
	var findings []Finding
	add := func(typ string, i int) { findings = append(findings, Finding{Type: typ, File: file, Line: i + 1}) }
	inKey := false
	env := isEnvFile(file)
	config := env || isConfigFile(file)

	for i, line := range lines {
		prefix, body := "", line
		if diffMode && len(line) > 0 && (line[0] == '+' || line[0] == '-' || line[0] == ' ') &&
			!strings.HasPrefix(line, "+++ ") && !strings.HasPrefix(line, "--- ") {
			prefix, body = line[:1], line[1:]
		}

		// Multi-line PEM private keys: blank every line between BEGIN and END.
		if inKey {
			if loc := privateKeyEnd.FindStringIndex(body); loc != nil {
				inKey = false
				body = Marker("private-key") + body[loc[1]:]
			} else {
				lead := len(body) - len(strings.TrimLeft(body, " \t\"'"))
				body = body[:lead] + Marker("private-key")
			}
			lines[i] = prefix + body
			continue
		}
		if loc := privateKeyBegin.FindStringIndex(body); loc != nil {
			add("private-key", i)
			if end := privateKeyEnd.FindStringIndex(body[loc[1]:]); end != nil {
				body = body[:loc[0]] + Marker("private-key") + body[loc[1]+end[1]:]
			} else {
				body = body[:loc[0]] + Marker("private-key")
				inKey = true
			}
		}

		for _, pr := range patternRules {
			body = replaceGroup(pr.re, body, pr.group, func(string) bool { return true }, func() string {
				add(pr.typ, i)
				return Marker(pr.typ)
			})
		}

		body = r.redactAssignments(body, config, func(typ string) { add(typ, i) })

		if env {
			body = r.redactEnvEntropy(body, func() { add("high-entropy-env", i) })
		}
		lines[i] = prefix + body
	}
	return strings.Join(lines, "\n"), findings
}

// replaceGroup replaces submatch `group` of every match of re in s with the
// result of repl, when keep(match) is true.
func replaceGroup(re *regexp.Regexp, s string, group int, keep func(string) bool, repl func() string) string {
	idx := re.FindAllStringSubmatchIndex(s, -1)
	if idx == nil {
		return s
	}
	var b strings.Builder
	last := 0
	for _, m := range idx {
		start, end := m[2*group], m[2*group+1]
		if start < 0 || start < last {
			continue
		}
		val := s[start:end]
		if strings.Contains(val, "[REDACTED:") || isPlaceholder(val) || !keep(val) {
			continue
		}
		b.WriteString(s[last:start])
		b.WriteString(repl())
		last = end
	}
	b.WriteString(s[last:])
	return b.String()
}

func (r *Redactor) redactAssignments(s string, config bool, found func(string)) string {
	var b strings.Builder
	last := 0
	for _, m := range assignRe.FindAllStringSubmatchIndex(s, -1) {
		if m[0] < last {
			continue
		}
		key := strings.ToLower(s[m[2]:m[3]])
		op := s[m[4]:m[5]]
		if op == "==" || op == "!=" {
			continue
		}
		vStart, vEnd, quoted := parseValue(s, m[1])
		if vStart < 0 {
			continue
		}
		val := s[vStart:vEnd]
		if !r.looksSecret(val, quoted, config) {
			continue
		}
		typ := "secret-assignment"
		if strings.Contains(key, "pass") || strings.Contains(key, "pwd") {
			typ = "password"
		}
		found(typ)
		b.WriteString(s[last:vStart])
		b.WriteString(Marker(typ))
		last = vEnd
	}
	b.WriteString(s[last:])
	return b.String()
}

// parseValue returns the bounds of the value starting at i: the contents of
// a quoted string, or an unquoted run up to whitespace or , ; ) } ].
func parseValue(s string, i int) (start, end int, quoted bool) {
	if i >= len(s) {
		return -1, -1, false
	}
	switch q := s[i]; q {
	case '"', '\'', '`':
		for j := i + 1; j < len(s); j++ {
			if s[j] == '\\' {
				j++
				continue
			}
			if s[j] == q {
				return i + 1, j, true
			}
		}
		return i + 1, len(s), true // unterminated: take the rest of the line
	}
	j := i
	for j < len(s) && !strings.ContainsRune(" \t,;)}]", rune(s[j])) {
		j++
	}
	if j == i {
		return -1, -1, false
	}
	return i, j, false
}

func (r *Redactor) looksSecret(v string, quoted, config bool) bool {
	if strings.Contains(v, "[REDACTED:") || isPlaceholder(v) {
		return false
	}
	// URLs (credentials inside them are handled by url-credentials) and
	// prose such as error messages ("invalid token") are not secrets.
	if strings.Contains(v, "://") || strings.ContainsAny(v, " \t") {
		return false
	}
	if quoted {
		return len(v) >= 4
	}
	// Unquoted values in code are usually references, not literals.
	if numericRe.MatchString(v) {
		return false
	}
	// An identifier-shaped value in code is a reference (cfg.Secret,
	// defaultKeyV2) unless it is digit-heavy like a random token.
	if !config && identRe.MatchString(v) && (strings.Contains(v, ".") || digitRatio(v) < 0.2) {
		return false
	}
	if strings.ContainsAny(v, "()[]{}") || strings.HasPrefix(v, "!") || strings.HasPrefix(v, "|") ||
		strings.HasPrefix(v, ">") || strings.HasPrefix(v, "&") || strings.HasPrefix(v, "*") {
		return false
	}
	if config {
		return len(v) >= 4
	}
	return len(v) >= 12 && digitRe.MatchString(v) && letterRe.MatchString(v) && entropy(v) >= 3.0
}

func (r *Redactor) redactEnvEntropy(s string, found func()) string {
	m := envLineRe.FindStringSubmatchIndex(s)
	if m == nil {
		return s
	}
	vStart := m[4]
	v := strings.TrimSpace(s[vStart:m[5]])
	// strip one layer of quotes, or a trailing comment
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		vStart += strings.Index(s[vStart:], v) + 1
		v = v[1 : len(v)-1]
	} else if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	if len(v) < r.MinEntropyLen || strings.Contains(v, "[REDACTED:") || strings.ContainsAny(v, " \t") ||
		strings.Contains(v, "://") || strings.HasPrefix(v, "/") || isPlaceholder(v) {
		return s
	}
	if !digitRe.MatchString(v) || !letterRe.MatchString(v) || entropy(v) < r.EntropyThreshold {
		return s
	}
	found()
	start := vStart + strings.Index(s[vStart:], v)
	return s[:start] + Marker("high-entropy-env") + s[start+len(v):]
}

// isPlaceholder recognises values that are obviously not real secrets.
func isPlaceholder(v string) bool {
	t := strings.TrimSpace(v)
	l := strings.ToLower(t)
	switch l {
	case "", "null", "nil", "none", "true", "false", "undefined", "changeme", "change_me",
		"replace_me", "redacted", "example", "secret", "password", "token", "todo", "fixme", "dummy", "test":
		return true
	}
	if xPlaceRe.MatchString(t) {
		return true
	}
	for _, p := range []string{"${", "$(", "{{", "<", "%(", "#{", "process.env", "os.getenv", "os.environ", "env("} {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	if strings.HasPrefix(t, "$") && identRe.MatchString(t[1:]) {
		return true
	}
	for _, p := range []string{"your-", "your_", "my-", "my_", "example", "placeholder", "dummy", "fake-", "fake_", "sample"} {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}

func isEnvFile(file string) bool {
	if file == "" {
		return false
	}
	base := path.Base(strings.ReplaceAll(file, `\`, "/"))
	return base == ".env" || strings.HasPrefix(base, ".env.") || strings.HasSuffix(base, ".env")
}

func isConfigFile(file string) bool {
	if file == "" {
		return false
	}
	base := path.Base(strings.ReplaceAll(file, `\`, "/"))
	if configExts[strings.ToLower(path.Ext(base))] || configExts[strings.ToLower(base)] {
		return true
	}
	return false
}

func digitRatio(s string) float64 {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			n++
		}
	}
	return float64(n) / float64(len(s))
}

// entropy is Shannon entropy in bits per byte.
func entropy(s string) float64 {
	if s == "" {
		return 0
	}
	var freq [256]int
	for i := 0; i < len(s); i++ {
		freq[s[i]]++
	}
	var h float64
	n := float64(len(s))
	for _, c := range freq {
		if c == 0 {
			continue
		}
		p := float64(c) / n
		h -= p * math.Log2(p)
	}
	return h
}

// Summary renders findings for a terminal warning, grouped by type and file:
//
//	aws-access-key-id ×2 (config/prod.env)
//	private-key (deploy/id_rsa.pem)
func Summary(findings []Finding) []string {
	type key struct{ typ, file string }
	counts := map[key]int{}
	var order []key
	for _, f := range findings {
		k := key{f.Type, f.File}
		if counts[k] == 0 {
			order = append(order, k)
		}
		counts[k]++
	}
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].file != order[j].file {
			return order[i].file < order[j].file
		}
		return order[i].typ < order[j].typ
	})
	var out []string
	for _, k := range order {
		s := k.typ
		if n := counts[k]; n > 1 {
			s += fmt.Sprintf(" ×%d", n)
		}
		where := k.file
		if where == "" {
			where = "commit messages / context"
		}
		out = append(out, fmt.Sprintf("%s (%s)", s, where))
	}
	return out
}
