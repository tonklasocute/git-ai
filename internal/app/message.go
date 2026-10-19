package app

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/tonklasocute/git-quill/internal/prompt"
	"github.com/tonklasocute/git-quill/internal/provider"
)

var (
	fenceRe = regexp.MustCompile("(?s)^```[a-zA-Z]*\\n(.*?)\\n?```$")
	labelRe = regexp.MustCompile(`(?i)^(?:commit message|message|option \d+|alternative \d+|\d+[.)])\s*:?\s*\n?`)
	sepRe   = regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(prompt.Separator) + `+\s*$`)
)

// SplitCandidates splits a response into cleaned commit messages.
func SplitCandidates(text string, maxSubject int) []string {
	var out []string
	for _, part := range sepRe.Split(provider.CleanText(text), -1) {
		if msg := CleanMessage(part, maxSubject); msg != "" {
			out = append(out, msg)
		}
	}
	return out
}

// CleanMessage removes wrapping the model may add (code fences, labels,
// quotes), normalises the subject/body blank line, and shortens an
// over-long subject at a word boundary.
func CleanMessage(s string, maxSubject int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if m := fenceRe.FindStringSubmatch(s); m != nil {
		s = strings.TrimSpace(m[1])
	}
	s = strings.TrimSpace(labelRe.ReplaceAllString(s, ""))
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'' || s[0] == '`' && s[len(s)-1] == '`') {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	subject := strings.TrimSuffix(strings.TrimSpace(lines[0]), ".")
	subject = ShortenSubject(subject, maxSubject)
	body := strings.TrimSpace(strings.Join(lines[1:], "\n"))
	if body == "" {
		return subject
	}
	return subject + "\n\n" + body
}

// ShortenSubject trims s to max runes at a word boundary.
func ShortenSubject(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)[:max]
	cut := string(r)
	if i := strings.LastIndex(cut, " "); i > max/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:-")
}
