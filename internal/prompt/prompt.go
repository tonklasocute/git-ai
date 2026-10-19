// Package prompt builds the exact requests sent to models. Everything that
// leaves the machine is assembled here, which keeps `--show-prompt` honest
// and makes the prompts reviewable as golden files.
package prompt

import (
	"fmt"
	"strings"

	"github.com/tonklasocute/git-quill/internal/git"
	"github.com/tonklasocute/git-quill/internal/provider"
)

// Options shared by all prompts.
type Options struct {
	Lang       string // "en" or "th"
	Style      string // "conventional" or "simple"
	MaxSubject int
	NoBody     bool
	Emoji      bool
	Type       string // forced commit type
	Context    string // --context hint (already redacted)
	Custom     string // config `prompt` additions
	Candidates int
	Branch     string
	Base       string
	// Excluded are "path (reason)" strings for files whose content was left out.
	Excluded []string
	// Note explains any reduction applied to the diff.
	Note string
}

func (o Options) maxSubject() int {
	if o.MaxSubject <= 0 {
		return 72
	}
	return o.MaxSubject
}

func language(lang string) string {
	if lang == "th" {
		return "Write in Thai (ภาษาไทย). Keep code identifiers, file paths, commit types and scopes, and technical terms in English."
	}
	return "Write in English."
}

func custom(o Options) string {
	if strings.TrimSpace(o.Custom) == "" {
		return ""
	}
	return "\nAdditional instructions from the user's configuration:\n" + strings.TrimSpace(o.Custom) + "\n"
}

const redactionRule = "- Text such as [REDACTED:<type>] marks secrets removed for privacy before sending. Never guess or mention their values."

// Separator between commit-message alternatives.
const Separator = "==="

// Commit builds the commit-message request from a (reduced) diff.
func Commit(o Options, diffText string) provider.Request {
	return provider.Request{
		Task:      provider.TaskCommit,
		System:    commitSystem(o),
		Messages:  []provider.Message{{Role: provider.User, Content: commitUser(o, "<diff>\n"+strings.TrimRight(diffText, "\n")+"\n</diff>")}},
		MaxTokens: 1024,
	}
}

// CommitFromSummaries builds the reduce step of map-reduce.
func CommitFromSummaries(o Options, summaries string) provider.Request {
	body := "The diff was too large to send in full. These are per-file summaries of the staged changes:\n\n<summaries>\n" +
		strings.TrimRight(summaries, "\n") + "\n</summaries>"
	return provider.Request{
		Task:      provider.TaskCommit,
		System:    commitSystem(o),
		Messages:  []provider.Message{{Role: provider.User, Content: commitUser(o, body)}},
		MaxTokens: 1024,
	}
}

func commitSystem(o Options) string {
	var b strings.Builder
	b.WriteString("You are an expert software engineer who writes clear, accurate git commit messages.\n\nRules:\n")
	if o.Style == "simple" {
		fmt.Fprintf(&b, "- Write a plain commit message: a capitalized subject line in the imperative mood (\"Add\", not \"Added\"), no trailing period, at most %d characters.\n", o.maxSubject())
	} else {
		b.WriteString("- Follow the Conventional Commits 1.0 specification: <type>(<scope>): <subject>\n")
		b.WriteString("- Allowed types: feat, fix, docs, style, refactor, perf, test, build, ci, chore, revert.\n")
		b.WriteString("- Infer <scope> from the changed paths (the main package, module or directory). Omit the scope when the change spans unrelated areas.\n")
		fmt.Fprintf(&b, "- Subject: imperative mood (\"add\", not \"added\"), lower-case start, no trailing period, at most %d characters for the whole first line.\n", o.maxSubject())
		b.WriteString("- If the change breaks backwards compatibility, put \"!\" after the type/scope and add a \"BREAKING CHANGE: <description>\" footer.\n")
	}
	if o.Type != "" {
		fmt.Fprintf(&b, "- The commit type MUST be `%s`.\n", o.Type)
	}
	if o.Emoji {
		b.WriteString("- Start the first line with one gitmoji matching the change (✨ feat, 🐛 fix, 📝 docs, ♻️ refactor, ✅ test, 🔧 chore, ⚡️ perf, 👷 ci, 📦 build).\n")
	}
	if o.NoBody {
		b.WriteString("- Write the subject line only, with no body.\n")
	} else {
		b.WriteString("- Body: after one blank line, explain what changed and why in short \"- \" bullet points wrapped at 72 columns. Skip the body for trivial changes. Do not repeat the subject.\n")
	}
	b.WriteString("- Describe only what the diff shows. Do not invent issue numbers, tickets or motivations.\n")
	b.WriteString(redactionRule + "\n")
	b.WriteString("- " + language(o.Lang) + "\n")
	if o.Candidates > 1 {
		fmt.Fprintf(&b, "- Return exactly %d alternative commit messages that differ meaningfully, separated by a line containing only %s.\n", o.Candidates, Separator)
	}
	b.WriteString("- Output only the commit message text: no preamble, no quotes, no Markdown code fences.\n")
	b.WriteString(custom(o))
	return b.String()
}

func commitUser(o Options, body string) string {
	var b strings.Builder
	if o.Branch != "" {
		fmt.Fprintf(&b, "Branch: %s\n", o.Branch)
	}
	if o.Context != "" {
		fmt.Fprintf(&b, "Context from the author: %s\n", o.Context)
	}
	writeMeta(&b, o)
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	b.WriteString(body)
	return b.String()
}

func writeMeta(b *strings.Builder, o Options) {
	if len(o.Excluded) > 0 {
		fmt.Fprintf(b, "Also changed, content not shown: %s\n", strings.Join(o.Excluded, ", "))
	}
	if o.Note != "" {
		fmt.Fprintf(b, "Note: %s\n", o.Note)
	}
}

// Summarize builds a map-step request for one chunk of a large diff. It is
// always in English: it is an intermediate artifact, not user-facing.
func Summarize(chunk string) provider.Request {
	sys := "You summarize parts of a git diff so that someone else can write a commit message or pull request description from the summaries alone.\n\n" +
		"For each file in the diff output exactly one line:\n" +
		"- <path>: +<added>/-<removed> lines; <what changed and, if evident, why — at most 30 words>\n\n" +
		"Be factual and specific (name functions, types, config keys). No preamble.\n" + redactionRule + "\n"
	return provider.Request{
		Task:      provider.TaskSummarize,
		System:    sys,
		Messages:  []provider.Message{{Role: provider.User, Content: "<diff>\n" + strings.TrimRight(chunk, "\n") + "\n</diff>"}},
		MaxTokens: 1024,
	}
}

// PR builds the pull-request request.
func PR(o Options, commits []git.Commit, diffOrSummaries string, summarized bool) provider.Request {
	h := PRHeadings(o.Lang)
	var sys strings.Builder
	sys.WriteString("You write GitHub pull request descriptions that reviewers find genuinely useful.\n\nOutput format:\n")
	fmt.Fprintf(&sys, "- Line 1: the PR title, at most %d characters. Use Conventional Commits style if the commits do. No \"#\", no quotes.\n", o.maxSubject())
	sys.WriteString("- Line 2: blank.\n- Then the body in GitHub Markdown using exactly these four headings, in this order, each on its own line with nothing else on it:\n")
	for _, heading := range h {
		sys.WriteString("  " + heading + "\n")
	}
	sys.WriteString("\nWhat goes under each heading:\n")
	fmt.Fprintf(&sys, "- %s: 1-3 sentences on what this PR does and why.\n", h[0])
	fmt.Fprintf(&sys, "- %s: a bullet list of the notable changes, grouped logically.\n", h[1])
	fmt.Fprintf(&sys, "- %s: how it was or should be tested; use \"- [ ]\" checklist items when unknown.\n", h[2])
	fmt.Fprintf(&sys, "- %s: breaking changes and migration steps, or just \"%s\".\n", h[3], map[bool]string{false: "None.", true: "ไม่มี"}[o.Lang == "th"])
	sys.WriteString("\nRules:\n- Describe only what the commits and diff show. Do not invent issue numbers.\n")
	sys.WriteString(redactionRule + "\n- " + language(o.Lang) + "\n- Output only the title and body, no code fences around the whole answer.\n")
	sys.WriteString(custom(o))

	var u strings.Builder
	if o.Branch != "" {
		fmt.Fprintf(&u, "Branch: %s → %s\n", o.Branch, o.Base)
	}
	if o.Context != "" {
		fmt.Fprintf(&u, "Context from the author: %s\n", o.Context)
	}
	writeMeta(&u, o)
	u.WriteString("\nCommits (oldest first):\n")
	u.WriteString(FormatCommits(commits))
	if summarized {
		u.WriteString("\nThe diff was too large to send in full. Per-file summaries:\n\n<summaries>\n" + strings.TrimRight(diffOrSummaries, "\n") + "\n</summaries>")
	} else {
		u.WriteString("\n<diff>\n" + strings.TrimRight(diffOrSummaries, "\n") + "\n</diff>")
	}
	return provider.Request{
		Task:      provider.TaskPR,
		System:    sys.String(),
		Messages:  []provider.Message{{Role: provider.User, Content: u.String()}},
		MaxTokens: 2048,
	}
}

// PRHeadings are the section headings a PR body must contain.
func PRHeadings(lang string) [4]string {
	if lang == "th" {
		return [4]string{"## สรุป", "## การเปลี่ยนแปลง", "## การทดสอบ", "## Breaking changes"}
	}
	return [4]string{"## Summary", "## Changes", "## Testing", "## Breaking changes"}
}

// FormatCommits renders commits as "- <short hash> <subject>" with indented
// bodies.
func FormatCommits(commits []git.Commit) string {
	var b strings.Builder
	for _, c := range commits {
		h := c.Hash
		if len(h) > 7 {
			h = h[:7]
		}
		fmt.Fprintf(&b, "- %s %s\n", h, c.Subject)
		if c.Body != "" {
			for _, l := range strings.Split(c.Body, "\n") {
				b.WriteString("  " + l + "\n")
			}
		}
	}
	if b.Len() == 0 {
		return "(none)\n"
	}
	return b.String()
}

// Changelog builds the release-notes request.
func Changelog(o Options, version string, commits []git.Commit) provider.Request {
	sys := "You maintain a CHANGELOG.md that follows Keep a Changelog 1.1.0 (https://keepachangelog.com).\n\n" +
		"Write the entries for ONE release from the commits given.\n" +
		"- Use these headings in this order, omitting empty ones: ### Added, ### Changed, ### Deprecated, ### Removed, ### Fixed, ### Security.\n" +
		"- Map Conventional Commit types: feat → Added; fix → Fixed; perf and user-visible refactor → Changed; revert or removals → Removed; security fixes → Security.\n" +
		"- Skip chore, ci, test, style, build and docs commits unless users would notice them.\n" +
		"- One bullet per user-visible change, written for users rather than developers. Merge duplicates. Prefix with **<scope>:** when the commit has a scope.\n" +
		"- Mark breaking changes with **BREAKING:**.\n" +
		redactionRule + "\n- " + language(o.Lang) + " Keep the headings in English (they are part of the format).\n" +
		"- Output only the headings and bullets: no version header, no intro, no code fences.\n" + custom(o)
	user := fmt.Sprintf("Release: %s\n\nCommits (oldest first):\n%s", version, FormatCommits(commits))
	return provider.Request{
		Task:      provider.TaskChangelog,
		System:    sys,
		Messages:  []provider.Message{{Role: provider.User, Content: user}},
		MaxTokens: 2048,
	}
}

// Review builds a code-review request over a line-numbered diff.
func Review(o Options, numberedDiff string) provider.Request {
	sys := "You are a meticulous senior code reviewer. Review ONLY the changed lines (\"+\" and \"-\"); context lines are there to help you understand them.\n\n" +
		"Look for, in priority order: likely bugs, security problems, missing or inadequate tests for new behaviour, unsafe error handling, and performance traps.\n" +
		"Each diff line is prefixed with its line number in the new file; cite those numbers.\n\n" +
		"Respond with a single JSON object and nothing else:\n" +
		`{"summary": "<one sentence>", "findings": [{"file": "<path>", "line": <number>, "severity": "high|medium|low|info", "category": "bug|security|tests|performance|maintainability", "message": "<what is wrong and why>", "suggestion": "<concrete fix>"}]}` + "\n\n" +
		"Severity: high = very likely a bug or vulnerability; medium = probable problem; low = minor risk; info = worth knowing.\n" +
		"\nRules:\n- Return an empty findings array when nothing deserves attention. Do not report formatting or naming nits.\n" +
		redactionRule + " A redacted value in the diff is itself worth a security finding (a secret was about to be committed).\n" +
		"- " + language(o.Lang) + " Keep JSON keys and severity/category values in English.\n" + custom(o)
	var u strings.Builder
	if o.Context != "" {
		fmt.Fprintf(&u, "Context from the author: %s\n", o.Context)
	}
	writeMeta(&u, o)
	if u.Len() > 0 {
		u.WriteString("\n")
	}
	u.WriteString(strings.TrimRight(numberedDiff, "\n") + "\n")
	return provider.Request{
		Task:      provider.TaskReview,
		System:    sys,
		Messages:  []provider.Message{{Role: provider.User, Content: u.String()}},
		MaxTokens: 4096,
	}
}

// Render prints a request exactly as it will be sent, for --show-prompt.
func Render(p provider.Provider, r provider.Request) string {
	var b strings.Builder
	name, model := "(none)", "(none)"
	if p != nil {
		name, model = p.Name(), p.Model()
	}
	fmt.Fprintf(&b, "──── request: %s → %s (%s), max_tokens=%d ────\n", r.Task, name, model, r.MaxTokens)
	b.WriteString("[system]\n" + r.System + "\n")
	for _, m := range r.Messages {
		fmt.Fprintf(&b, "[%s]\n%s\n", m.Role, m.Content)
	}
	return b.String()
}
