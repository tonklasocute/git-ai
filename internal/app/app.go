// Package app holds the pipeline shared by every command:
//
//	git diff → drop excluded files → redact secrets → fit the token budget
//	(fewer context lines, then map-reduce) → prompt → provider
package app

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tonklasocute/git-quill/internal/config"
	"github.com/tonklasocute/git-quill/internal/diff"
	"github.com/tonklasocute/git-quill/internal/git"
	"github.com/tonklasocute/git-quill/internal/prompt"
	"github.com/tonklasocute/git-quill/internal/provider"
	"github.com/tonklasocute/git-quill/internal/redact"
	"github.com/tonklasocute/git-quill/internal/ui"
)

// promptOverhead is reserved for system prompt, metadata and the answer.
const promptOverhead = 2000

// App is per-invocation state.
type App struct {
	Repo    *git.Repo
	Root    string
	Cfg     *config.Config
	Out     io.Writer // generated text goes here
	UI      *ui.Printer
	Verbose bool

	// NewProvider builds the provider; replaceable in tests.
	NewProvider func(cfg *config.Config, onRetry func(int, time.Duration, string)) (provider.Provider, error)

	mu       sync.Mutex
	prov     provider.Provider
	usage    provider.Usage
	calls    int
	redactor *redact.Redactor
}

// Open loads the repo at dir and the merged config.
func Open(ctx context.Context, dir string, out, errOut io.Writer) (*App, error) {
	repo, err := git.Open(ctx, dir)
	if err != nil {
		return nil, err
	}
	root, err := repo.Root(ctx)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	a := &App{Repo: repo, Root: root, Cfg: cfg, Out: out, UI: ui.NewPrinter(errOut), NewProvider: NewProvider, redactor: redact.New()}
	for _, w := range cfg.Warnings {
		a.UI.Warn("%s", w)
	}
	return a, nil
}

// Provider returns the configured provider, building it on first use.
func (a *App) Provider() (provider.Provider, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.prov != nil {
		return a.prov, nil
	}
	onRetry := func(attempt int, wait time.Duration, reason string) {
		if wait > 0 {
			a.UI.Dim("  retrying (%d) in %s: %s", attempt, wait.Round(100*time.Millisecond), reason)
		} else {
			a.UI.Dim("  retrying (%d): %s", attempt, reason)
		}
	}
	p, err := a.NewProvider(a.Cfg, onRetry)
	if err != nil {
		return nil, err
	}
	a.prov = p
	return p, nil
}

// Call sends one request, tracking token usage.
func (a *App) Call(ctx context.Context, req provider.Request, onDelta func(string)) (*provider.Response, error) {
	p, err := a.Provider()
	if err != nil {
		return nil, err
	}
	start := time.Now()
	resp, err := p.Stream(ctx, req, onDelta)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.usage.Add(resp.Usage)
	a.calls++
	a.mu.Unlock()
	if a.Verbose {
		a.UI.Dim("  %s call: %s in %s", req.Task, usageString(resp.Usage), time.Since(start).Round(10*time.Millisecond))
	}
	return resp, nil
}

// PrintUsage prints accumulated token usage (shown with --verbose).
func (a *App) PrintUsage() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.calls == 0 || a.prov == nil {
		return
	}
	a.UI.Dim("tokens: %s · %d call(s) · %s/%s", usageString(a.usage), a.calls, a.prov.Name(), a.prov.Model())
}

func usageString(u provider.Usage) string {
	s := fmt.Sprintf("%s in / %s out", thousands(u.InputTokens), thousands(u.OutputTokens))
	if u.Estimated {
		s = "~" + s + " (estimated)"
	}
	return s
}

func thousands(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// Matcher builds the exclude matcher: built-in defaults, then config
// `ignore`, then .git-quill-ignore (later rules win, so users can re-include
// a default with "!pattern").
func (a *App) Matcher() *diff.Matcher {
	m := diff.NewMatcher(diff.DefaultExcludes...)
	m.Add(a.Cfg.Ignore...)
	if err := m.AddFile(filepath.Join(a.Root, config.IgnoreFile)); err != nil {
		a.UI.Warn("reading %s: %v", config.IgnoreFile, err)
	}
	return m
}

// Budget is the token budget for diff content in one request.
func (a *App) Budget() int {
	_, pc := a.Cfg.Active()
	b := pc.MaxInputTokens - promptOverhead
	if b < 500 {
		b = 500
	}
	return b
}

// Source produces a diff with the given number of context lines.
type Source func(ctx context.Context, contextLines int) (string, error)

// Prepared is a diff ready for prompting.
type Prepared struct {
	Files    []diff.File
	Excluded []diff.Excluded
	Findings []redact.Finding
	// Text is the diff (or numbered diff) to embed in the prompt.
	Text         string
	ContextLines int
	Tokens       int
	// OverBudget means Text does not fit even with zero context lines;
	// Chunks then holds budget-sized pieces for map-reduce.
	OverBudget bool
	Chunks     []string
	// Steps describes what was done to fit the budget.
	Steps []string
}

// Note tells the model about reductions it cannot see (excluded files are
// listed separately in the prompt).
func (p *Prepared) Note() string {
	if p.ContextLines < 3 {
		return fmt.Sprintf("diff context was reduced to %d line(s) to fit the token budget", p.ContextLines)
	}
	return ""
}

// ExcludedList renders excluded files for the prompt.
func (p *Prepared) ExcludedList() []string {
	out := make([]string, len(p.Excluded))
	for i, e := range p.Excluded {
		out[i] = e.String()
	}
	return out
}

// Prepare loads, filters, redacts and budgets a diff. numbered renders
// files with line numbers (for review).
func (a *App) Prepare(ctx context.Context, src Source, numbered bool) (*Prepared, error) {
	budget := a.Budget()
	matcher := a.Matcher()
	render := (*diff.File).String
	if numbered {
		render = (*diff.File).Numbered
	}
	var (
		p   *Prepared
		err error
	)
	for _, ctxLines := range []int{3, 1, 0} {
		p, err = a.load(ctx, src, matcher, ctxLines, render)
		if err != nil {
			return nil, err
		}
		if p.Tokens <= budget || len(p.Files) == 0 {
			break
		}
	}
	if len(p.Excluded) > 0 {
		p.Steps = append([]string{fmt.Sprintf("left out %d file(s): %s", len(p.Excluded), strings.Join(p.ExcludedList(), ", "))}, p.Steps...)
	}
	if p.ContextLines < 3 {
		p.Steps = append(p.Steps, fmt.Sprintf("reduced diff context to %d line(s)", p.ContextLines))
	}
	if p.Tokens > budget {
		p.OverBudget = true
		p.Chunks = diff.ChunksFunc(p.Files, budget, render)
	}
	a.reportFindings(p.Findings)
	if a.Verbose {
		a.UI.Dim("diff: %d file(s), ~%s tokens (budget %s)", len(p.Files), thousands(p.Tokens), thousands(budget))
	}
	return p, nil
}

func (a *App) load(ctx context.Context, src Source, m *diff.Matcher, ctxLines int, render func(*diff.File) string) (*Prepared, error) {
	raw, err := src(ctx, ctxLines)
	if err != nil {
		return nil, err
	}
	kept, excluded := diff.Filter(diff.Parse(raw), m)
	p := &Prepared{Excluded: excluded, ContextLines: ctxLines}
	var b strings.Builder
	for i := range kept {
		red, fs := a.redactor.Diff(kept[i].Path(), kept[i].String())
		p.Findings = append(p.Findings, fs...)
		if parsed := diff.Parse(red); len(parsed) == 1 {
			kept[i] = parsed[0]
		}
		b.WriteString(render(&kept[i]))
	}
	p.Files = kept
	p.Text = b.String()
	p.Tokens = diff.EstimateTokens(p.Text)
	return p, nil
}

// RedactText redacts free text (commit messages, --context) and reports.
func (a *App) RedactText(s string) string {
	out, fs := a.redactor.Text(s)
	a.reportFindings(fs)
	return out
}

func (a *App) reportFindings(fs []redact.Finding) {
	if len(fs) == 0 {
		return
	}
	a.UI.Warn("redacted %d secret(s) before sending anything to the model:", len(fs))
	for _, s := range redact.Summary(fs) {
		a.UI.Println("    • " + s)
	}
	a.UI.Dim("  They were replaced with [REDACTED:<type>]. If they are real, remove them before committing.")
}

// Strategy reports to the user how a large diff was handled.
// Excluded files alone are only mentioned with --verbose.
func (a *App) Strategy(p *Prepared, mapReduce bool) {
	reduced := p.ContextLines < 3 || mapReduce
	if !reduced && (!a.Verbose || len(p.Excluded) == 0) {
		return
	}
	steps := p.Steps
	if mapReduce {
		steps = append(steps, fmt.Sprintf("summarized %d file(s) in %d chunk(s) (map-reduce)", len(p.Files), len(p.Chunks)))
	}
	label := "large diff"
	if !reduced {
		label = "note"
	}
	a.UI.Info("%s: %s", label, strings.Join(steps, "; "))
}

// Summarize runs the map step of map-reduce over p.Chunks (up to 4 in
// parallel) and returns the joined summaries, truncated to the budget.
func (a *App) Summarize(ctx context.Context, p *Prepared, progress func(done, total int)) (string, error) {
	out := make([]string, len(p.Chunks))
	errs := make([]error, len(p.Chunks))
	// Local servers (Ollama, LM Studio) queue requests and run them one at a
	// time; sending them in parallel only makes queued ones hit the timeout.
	parallel := 4
	if info, _ := a.Cfg.Active(); info.Local {
		parallel = 1
	}
	sem := make(chan struct{}, parallel)
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		done int
	)
	for i, chunk := range p.Chunks {
		wg.Add(1)
		go func(i int, chunk string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			resp, err := a.Call(ctx, prompt.Summarize(chunk), nil)
			if err != nil {
				errs[i] = err
				return
			}
			out[i] = strings.TrimSpace(resp.Text)
			mu.Lock()
			done++
			if progress != nil {
				progress(done, len(p.Chunks))
			}
			mu.Unlock()
		}(i, chunk)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return "", err
		}
	}
	joined, _ := diff.Truncate(strings.Join(out, "\n"), a.Budget())
	return joined, nil
}

// ShowPrompt writes a request exactly as it will be sent.
func (a *App) ShowPrompt(req provider.Request) {
	p, _ := a.Provider()
	fmt.Fprint(a.UI.Writer(), prompt.Render(p, req))
}

// PrintUsageIf prints usage when --verbose is on.
func (a *App) PrintUsageIf() {
	if a.Verbose {
		a.PrintUsage()
	}
}
