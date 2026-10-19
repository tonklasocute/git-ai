// Package fake is a deterministic offline provider (GIT_QUILL_PROVIDER=fake).
// It reads the diff out of the prompt and writes plausible output so demos
// and end-to-end tests run without network access or API keys.
//
// Environment knobs:
//
//	GIT_QUILL_FAKE_RESPONSE  return this text verbatim
//	GIT_QUILL_FAKE_ERROR     fail with this kind: auth, not_found, rate_limit, server, unavailable
//	GIT_QUILL_FAKE_DELAY     milliseconds between streamed words (for demos)
package fake

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tonklasocute/git-quill/internal/diff"
	"github.com/tonklasocute/git-quill/internal/provider"
)

// Client implements provider.Provider.
type Client struct {
	model string
	mu    sync.Mutex
	calls int
	// Requests records every request (for tests).
	Requests []provider.Request
}

// New returns a fake client.
func New(opts provider.Options) *Client {
	m := opts.Model
	if m == "" {
		m = "fake-1"
	}
	return &Client{model: m}
}

func (c *Client) Name() string  { return "fake" }
func (c *Client) Model() string { return c.model }

// Calls returns how many requests were made.
func (c *Client) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// Stream implements provider.Provider.
func (c *Client) Stream(ctx context.Context, req provider.Request, onDelta func(string)) (*provider.Response, error) {
	c.mu.Lock()
	variant := c.calls
	c.calls++
	c.Requests = append(c.Requests, req)
	c.mu.Unlock()

	if kind := os.Getenv("GIT_QUILL_FAKE_ERROR"); kind != "" {
		return nil, fakeError(kind, c.model)
	}
	text := os.Getenv("GIT_QUILL_FAKE_RESPONSE")
	if text == "" {
		text = generate(req, variant)
	}
	delay, _ := strconv.Atoi(os.Getenv("GIT_QUILL_FAKE_DELAY"))
	if onDelta != nil {
		for _, w := range strings.SplitAfter(text, " ") {
			if err := ctx.Err(); err != nil {
				return nil, provider.FromTransport(provider.HintInfo{Provider: "fake"}, err)
			}
			onDelta(w)
			if delay > 0 {
				time.Sleep(time.Duration(delay) * time.Millisecond)
			}
		}
	}
	in := diff.EstimateTokens(req.System)
	for _, m := range req.Messages {
		in += diff.EstimateTokens(m.Content)
	}
	return &provider.Response{
		Text:       text,
		Model:      c.model,
		StopReason: "end_turn",
		Usage:      provider.Usage{InputTokens: in, OutputTokens: diff.EstimateTokens(text), Estimated: true},
	}, nil
}

func fakeError(kind, model string) error {
	status := map[string]int{"auth": 401, "not_found": 404, "rate_limit": 429, "server": 500}[kind]
	info := provider.HintInfo{Provider: "fake", Model: model, KeyEnv: "FAKE_API_KEY"}
	if status == 0 {
		return provider.FromTransport(info, fmt.Errorf("dial tcp 127.0.0.1:1: connect: connection refused"))
	}
	return provider.FromStatus(info, status, "simulated "+kind+" error")
}

func generate(req provider.Request, variant int) string {
	user := ""
	for _, m := range req.Messages {
		if m.Role == provider.User {
			user = m.Content
		}
	}
	th := strings.Contains(req.System, "Thai")
	switch req.Task {
	case provider.TaskSummarize:
		return summarize(user)
	case provider.TaskPR:
		return pr(user, th)
	case provider.TaskChangelog:
		return changelog(user, th)
	case provider.TaskReview:
		return review(user, th)
	default:
		return commits(req, user, th, variant)
	}
}

type fileInfo struct {
	path    string
	status  string // added, deleted, modified
	added   int
	deleted int
	symbols []string // declarations added in the diff
}

var (
	diffHeaderRe = regexp.MustCompile(`(?m)^diff --git a/.+? b/(.+)$`)
	summaryRe    = regexp.MustCompile(`(?m)^- ([^\s:]+): \+(\d+)/-(\d+) lines(?:; (add|update|remove))?`)
	countRe      = regexp.MustCompile(`exactly (\d+) alternative`)
	typeRe       = regexp.MustCompile("type MUST be `(\\w+)`")
	commitLineRe = regexp.MustCompile(`(?m)^- (?:[0-9a-f]{7,40} )?(\w+)(?:\(([^)]*)\))?(!)?: (.+)$`)
)

func parseFiles(user string) []fileInfo {
	var files []fileInfo
	for _, f := range diff.Parse(user) {
		files = append(files, fileInfo{path: f.Path(), status: string(f.Status), added: f.Added, deleted: f.Deleted, symbols: symbols(&f)})
	}
	if len(files) == 0 { // map-reduce mode: the prompt has summaries, not a diff
		for _, m := range summaryRe.FindAllStringSubmatch(user, -1) {
			a, _ := strconv.Atoi(m[2])
			d, _ := strconv.Atoi(m[3])
			status := map[string]string{"add": "added", "remove": "deleted"}[m[4]]
			if status == "" {
				status = "modified"
			}
			files = append(files, fileInfo{path: m[1], status: status, added: a, deleted: d})
		}
	}
	if len(files) == 0 {
		for _, m := range diffHeaderRe.FindAllStringSubmatch(user, -1) {
			files = append(files, fileInfo{path: m[1], status: "modified"})
		}
	}
	return files
}

func commits(req provider.Request, user string, th bool, variant int) string {
	n := 1
	if m := countRe.FindStringSubmatch(req.System + user); m != nil {
		n, _ = strconv.Atoi(m[1])
	}
	files := parseFiles(user)
	var out []string
	for i := 0; i < n; i++ {
		out = append(out, commitMessage(req.System+"\n"+user, user, files, th, variant*n+i))
	}
	return strings.Join(out, "\n===\n")
}

var gitmoji = map[string]string{
	"feat": "✨", "fix": "🐛", "docs": "📝", "refactor": "♻️", "test": "✅",
	"ci": "👷", "build": "📦", "chore": "🔧", "perf": "⚡️", "style": "🎨",
}

// commitMessage follows directives found anywhere in prompt but infers the
// type only from the user's content (the system prompt lists every type).
func commitMessage(prompt, user string, files []fileInfo, th bool, variant int) string {
	typ := inferType(files, user)
	if m := typeRe.FindStringSubmatch(prompt); m != nil {
		typ = m[1]
	}
	scope := inferScope(files)
	thing := subjectNoun(files)
	tpl := subjects(typ, th)
	subject := fmt.Sprintf(tpl[variant%len(tpl)], thing)

	header := typ
	if scope != "" && strings.Contains(prompt, "Conventional Commits") {
		header += "(" + scope + ")"
	}
	header += ": " + subject
	if !strings.Contains(prompt, "Conventional Commits") {
		header = strings.ToUpper(subject[:1]) + subject[1:]
	}
	if strings.Contains(prompt, "gitmoji") {
		header = gitmoji[typ] + " " + header
	}
	if strings.Contains(prompt, "subject line only") || len(files) == 0 {
		return header
	}
	var body []string
	for i, f := range files {
		if i == 5 {
			if th {
				body = append(body, fmt.Sprintf("- และอีก %d ไฟล์", len(files)-5))
			} else {
				body = append(body, fmt.Sprintf("- and %d more files", len(files)-5))
			}
			break
		}
		line := "- " + verb(f.status, th) + " " + f.path
		if len(f.symbols) > 0 {
			line += " (" + strings.Join(f.symbols, ", ") + ")"
		}
		body = append(body, line)
	}
	return header + "\n\n" + strings.Join(body, "\n")
}

func verb(status string, th bool) string {
	switch {
	case status == "added" && th:
		return "เพิ่ม"
	case status == "added":
		return "Add"
	case status == "deleted" && th:
		return "ลบ"
	case status == "deleted":
		return "Remove"
	case th:
		return "ปรับปรุง"
	default:
		return "Update"
	}
}

func inferType(files []fileInfo, prompt string) string {
	if len(files) == 0 {
		return "chore"
	}
	all := func(pred func(string) bool) bool {
		for _, f := range files {
			if !pred(f.path) {
				return false
			}
		}
		return true
	}
	switch {
	case all(func(p string) bool { return strings.HasSuffix(p, ".md") || strings.HasPrefix(p, "docs/") }):
		return "docs"
	case all(func(p string) bool {
		return strings.Contains(p, "_test.") || strings.Contains(p, ".test.") || strings.Contains(p, ".spec.") || strings.HasPrefix(p, "test")
	}):
		return "test"
	case all(func(p string) bool { return strings.HasPrefix(p, ".github/") }):
		return "ci"
	case all(func(p string) bool {
		b := path.Base(p)
		return b == "Makefile" || b == "go.mod" || b == "package.json" || b == "Dockerfile" || strings.HasPrefix(b, ".goreleaser")
	}):
		return "build"
	}
	for _, f := range files {
		if f.status == "added" {
			return "feat"
		}
	}
	lower := strings.ToLower(prompt)
	for _, w := range []string{"fix", "bug", "err", "nil", "panic", "crash"} {
		if strings.Contains(lower, "+"+w) || strings.Contains(lower, " "+w) {
			return "fix"
		}
	}
	return "refactor"
}

func inferScope(files []fileInfo) string {
	if len(files) == 0 {
		return ""
	}
	scopes := map[string]int{}
	for _, f := range files {
		parts := strings.Split(f.path, "/")
		for len(parts) > 1 && (parts[0] == "internal" || parts[0] == "src" || parts[0] == "pkg" ||
			parts[0] == "cmd" || parts[0] == "lib" || parts[0] == "app" || parts[0] == ".github") {
			parts = parts[1:]
		}
		s := parts[0]
		if len(parts) == 1 {
			s = strings.TrimSuffix(s, path.Ext(s))
		}
		scopes[strings.ToLower(strings.TrimPrefix(s, "."))]++
	}
	if len(scopes) > 2 {
		return ""
	}
	keys := make([]string, 0, len(scopes))
	for k := range scopes {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return scopes[keys[i]] > scopes[keys[j]] || scopes[keys[i]] == scopes[keys[j]] && keys[i] < keys[j]
	})
	return keys[0]
}

func subjectNoun(files []fileInfo) string {
	if len(files) == 0 {
		return "project files"
	}
	best := files[0]
	for _, f := range files {
		if f.added+f.deleted > best.added+best.deleted {
			best = f
		}
	}
	b := path.Base(best.path)
	b = strings.TrimSuffix(b, path.Ext(b))
	b = strings.TrimSuffix(b, "_test")
	b = strings.NewReplacer("_", " ", "-", " ", ".", " ").Replace(b)
	if b == "" {
		b = best.path
	}
	return strings.ToLower(b)
}

func subjects(typ string, th bool) []string {
	if th {
		switch typ {
		case "feat":
			return []string{"เพิ่ม %s", "รองรับ %s", "เพิ่มความสามารถด้าน %s", "เพิ่มระบบ %s"}
		case "fix":
			return []string{"แก้ไขข้อผิดพลาดใน %s", "ป้องกันค่าว่างใน %s", "แก้บั๊ก %s", "ตรวจสอบข้อมูลนำเข้าใน %s"}
		case "docs":
			return []string{"อัปเดตเอกสาร %s", "เพิ่มคำอธิบาย %s", "ปรับปรุงเอกสาร %s"}
		case "test":
			return []string{"เพิ่มเทสต์ %s", "ครอบคลุมเทสต์ %s", "ขยายเทสต์ %s"}
		default:
			return []string{"ปรับปรุง %s", "จัดโครงสร้าง %s ใหม่", "ทำให้ %s เรียบง่ายขึ้น"}
		}
	}
	switch typ {
	case "feat":
		return []string{"add %s", "support %s", "introduce %s", "implement %s flow"}
	case "fix":
		return []string{"handle empty input in %s", "fix error handling in %s", "guard against nil in %s", "validate %s input"}
	case "docs":
		return []string{"update %s", "document %s", "clarify %s"}
	case "test":
		return []string{"add tests for %s", "cover %s edge cases", "extend %s tests"}
	case "ci", "build", "chore":
		return []string{"update %s", "bump %s", "tidy %s"}
	default:
		return []string{"simplify %s", "restructure %s", "clean up %s"}
	}
}

func summarize(user string) string {
	files := parseFiles(user)
	var b strings.Builder
	for _, f := range files {
		fmt.Fprintf(&b, "- %s: +%d/-%d lines; %s %s\n", f.path, f.added, f.deleted, strings.ToLower(verb(f.status, false)), subjectNoun([]fileInfo{f}))
	}
	return strings.TrimSpace(b.String())
}

type commitInfo struct{ typ, scope, subject string }

func parseCommits(user string) []commitInfo {
	var cs []commitInfo
	for _, m := range commitLineRe.FindAllStringSubmatch(user, -1) {
		cs = append(cs, commitInfo{typ: m[1], scope: m[2], subject: m[4]})
	}
	return cs
}

func pr(user string, th bool) string {
	cs := parseCommits(user)
	files := parseFiles(user)
	title := "Update " + subjectNoun(files)
	if len(cs) > 0 {
		c := cs[len(cs)-1]
		if len(cs) > 1 {
			c = cs[0]
		}
		title = c.typ
		if c.scope != "" {
			title += "(" + c.scope + ")"
		}
		title += ": " + c.subject
	}
	h := map[bool][4]string{
		false: {"## Summary", "## Changes", "## Testing", "## Breaking changes"},
		true:  {"## สรุป", "## การเปลี่ยนแปลง", "## การทดสอบ", "## Breaking changes"},
	}[th]
	var b strings.Builder
	b.WriteString(title + "\n\n" + h[0] + "\n\n")
	if th {
		fmt.Fprintf(&b, "PR นี้มี %d commit ที่แก้ไข %d ไฟล์\n\n", len(cs), len(files))
	} else {
		fmt.Fprintf(&b, "This PR contains %d commit(s) touching %d file(s).\n\n", len(cs), len(files))
	}
	b.WriteString(h[1] + "\n\n")
	for _, c := range cs {
		fmt.Fprintf(&b, "- %s\n", c.subject)
	}
	for _, f := range files {
		fmt.Fprintf(&b, "- `%s` (+%d/-%d)\n", f.path, f.added, f.deleted)
	}
	b.WriteString("\n" + h[2] + "\n\n- [ ] `go test ./...`\n\n" + h[3] + "\n\n")
	if th {
		b.WriteString("ไม่มี\n")
	} else {
		b.WriteString("None.\n")
	}
	return b.String()
}

func changelog(user string, th bool) string {
	sections := map[string][]string{}
	order := []string{"Added", "Changed", "Deprecated", "Removed", "Fixed", "Security"}
	for _, c := range parseCommits(user) {
		sec := map[string]string{"feat": "Added", "fix": "Fixed", "perf": "Changed", "refactor": "Changed", "revert": "Removed", "security": "Security", "deprecate": "Deprecated"}[c.typ]
		if sec == "" {
			continue
		}
		entry := strings.ToUpper(c.subject[:1]) + c.subject[1:]
		if c.scope != "" {
			entry = "**" + c.scope + ":** " + entry
		}
		sections[sec] = append(sections[sec], entry)
	}
	var b strings.Builder
	for _, s := range order {
		if len(sections[s]) == 0 {
			continue
		}
		b.WriteString("### " + s + "\n\n")
		for _, e := range sections[s] {
			b.WriteString("- " + e + "\n")
		}
		b.WriteString("\n")
	}
	if b.Len() == 0 {
		if th {
			return "### Changed\n\n- ปรับปรุงภายในเล็กน้อย\n"
		}
		return "### Changed\n\n- Internal maintenance.\n"
	}
	return strings.TrimSpace(b.String()) + "\n"
}

var numberedAddRe = regexp.MustCompile(`(?m)^\s+(\d+) \+(.*)$`)

func review(user string, th bool) string {
	type finding struct {
		File       string `json:"file"`
		Line       int    `json:"line"`
		Severity   string `json:"severity"`
		Category   string `json:"category"`
		Message    string `json:"message"`
		Suggestion string `json:"suggestion,omitempty"`
	}
	var out struct {
		Summary  string    `json:"summary"`
		Findings []finding `json:"findings"`
	}
	sections := strings.Split(user, "### ")
	for _, sec := range sections[1:] {
		file := strings.TrimSpace(strings.SplitN(strings.SplitN(sec, "\n", 2)[0], " (", 2)[0])
		for _, m := range numberedAddRe.FindAllStringSubmatch(sec, -1) {
			line, _ := strconv.Atoi(m[1])
			code := strings.ToLower(m[2])
			switch {
			case strings.Contains(code, "[redacted:"):
				msg, sug := "Credential-like value committed to source.", "Load it from the environment or a secret manager."
				if th {
					msg, sug = "มีค่าที่ดูเหมือนรหัสผ่าน/คีย์อยู่ในโค้ด", "ย้ายไปอ่านจาก environment หรือ secret manager"
				}
				out.Findings = append(out.Findings, finding{file, line, "high", "security", msg, sug})
			case strings.Contains(code, "todo"):
				msg := "Unresolved TODO left in new code."
				if th {
					msg = "มี TODO ค้างอยู่ในโค้ดใหม่"
				}
				out.Findings = append(out.Findings, finding{file, line, "info", "maintainability", msg, ""})
			case strings.Contains(code, "err") && strings.Contains(code, "_ ="):
				msg := "Error is discarded."
				if th {
					msg = "error ถูกทิ้งโดยไม่ได้ตรวจสอบ"
				}
				out.Findings = append(out.Findings, finding{file, line, "medium", "bug", msg, "Handle or return the error."})
			}
		}
		if !strings.Contains(file, "_test") && !strings.HasSuffix(file, ".md") {
			if m := numberedAddRe.FindStringSubmatch(sec); m != nil {
				line, _ := strconv.Atoi(m[1])
				msg := "New behaviour has no accompanying test changes."
				if th {
					msg = "พฤติกรรมใหม่ยังไม่มีเทสต์รองรับ"
				}
				out.Findings = append(out.Findings, finding{file, line, "low", "tests", msg, "Add a table-driven test for the new branch."})
			}
		}
	}
	out.Summary = fmt.Sprintf("Reviewed %d file(s); %d suggestion(s).", len(sections)-1, len(out.Findings))
	if th {
		out.Summary = fmt.Sprintf("ตรวจ %d ไฟล์ พบ %d ข้อเสนอแนะ", len(sections)-1, len(out.Findings))
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b)
}

var declRe = regexp.MustCompile(`^\+\s*(?:func\s+(?:\([^)]*\)\s*)?|type\s+|var\s+)([A-Z]\w*)`)

// symbols lists exported declarations added in a file (max 3).
func symbols(f *diff.File) []string {
	var out []string
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if m := declRe.FindStringSubmatch(l); m != nil && len(out) < 3 {
				out = append(out, m[1])
			}
		}
	}
	return out
}
