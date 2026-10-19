package app

import (
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
)

// Finding is one review comment.
type Finding struct {
	File       string `json:"file"`
	Line       int    `json:"line"`
	Severity   string `json:"severity"`
	Category   string `json:"category"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
}

// Review is the parsed model output.
type Review struct {
	Summary  string    `json:"summary"`
	Findings []Finding `json:"findings"`
}

var severityRank = map[string]int{"high": 0, "medium": 1, "low": 2, "info": 3}

// ParseReview extracts the JSON object from a model response (tolerating
// code fences or chatter around it) and normalises severities.
func ParseReview(text string) (*Review, error) {
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, errors.New("no JSON object in the response")
	}
	var r Review
	if err := json.Unmarshal([]byte(text[start:end+1]), &r); err != nil {
		return nil, err
	}
	for i := range r.Findings {
		f := &r.Findings[i]
		f.Severity = strings.ToLower(strings.TrimSpace(f.Severity))
		if _, ok := severityRank[f.Severity]; !ok {
			f.Severity = "info"
		}
		f.Category = strings.ToLower(strings.TrimSpace(f.Category))
	}
	return &r, nil
}

// Merge combines chunked reviews.
func (r *Review) Merge(o *Review) {
	if o.Summary != "" {
		if r.Summary != "" {
			r.Summary += " "
		}
		r.Summary += o.Summary
	}
	r.Findings = append(r.Findings, o.Findings...)
}

// ByFile groups findings by file (sorted), each sorted by severity then line.
func (r *Review) ByFile() ([]string, map[string][]Finding) {
	groups := map[string][]Finding{}
	for _, f := range r.Findings {
		groups[f.File] = append(groups[f.File], f)
	}
	files := make([]string, 0, len(groups))
	for f, list := range groups {
		files = append(files, f)
		sort.SliceStable(list, func(i, j int) bool {
			if severityRank[list[i].Severity] != severityRank[list[j].Severity] {
				return severityRank[list[i].Severity] < severityRank[list[j].Severity]
			}
			return list[i].Line < list[j].Line
		})
	}
	sort.Strings(files)
	return files, groups
}

// Counts returns findings per severity.
func (r *Review) Counts() map[string]int {
	c := map[string]int{}
	for _, f := range r.Findings {
		c[f.Severity]++
	}
	return c
}

// Blocking reports whether --strict should fail (any high or medium).
func (r *Review) Blocking() bool {
	c := r.Counts()
	return c["high"]+c["medium"] > 0
}

var (
	fenceAnyRe = regexp.MustCompile("(?s)^```[a-zA-Z]*\\n(.*?)\\n?```$")
	titleRe    = regexp.MustCompile(`(?i)^(?:#+\s*)?(?:\*\*title:?\*\*:?|title:)?\s*`)
	// Small models sometimes echo the instructions onto the heading line.
	prHeadingRe = regexp.MustCompile(`(?m)^(## (?:Summary|Changes|Testing|Breaking changes|สรุป|การเปลี่ยนแปลง|การทดสอบ))[ \t]*(?:—|–|-|:)[^\n]*$`)
	starBullet  = regexp.MustCompile(`(?m)^([ \t]*)\* `)
)

// ParsePR splits a PR response into title and body.
func ParsePR(text string) (title, body string) {
	text = strings.TrimSpace(text)
	if m := fenceAnyRe.FindStringSubmatch(text); m != nil {
		text = strings.TrimSpace(m[1])
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		title = strings.Trim(strings.TrimSpace(titleRe.ReplaceAllString(strings.TrimSpace(l), "")), "\"'`")
		body = strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
		body = prHeadingRe.ReplaceAllString(body, "$1")
		return title, body
	}
	return "", ""
}

// CleanSection strips code fences and anything before the first "### ".
func CleanSection(text string) string {
	text = strings.TrimSpace(text)
	if m := fenceAnyRe.FindStringSubmatch(text); m != nil {
		text = strings.TrimSpace(m[1])
	}
	if i := strings.Index(text, "### "); i > 0 {
		text = text[i:]
	}
	// Keep a Changelog uses "- " bullets.
	return strings.TrimSpace(starBullet.ReplaceAllString(text, "${1}- "))
}
