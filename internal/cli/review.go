package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tonklasocute/git-quill/internal/app"
	"github.com/tonklasocute/git-quill/internal/prompt"
	"github.com/tonklasocute/git-quill/internal/ui"
)

type reviewFlags struct {
	shared
	branch bool
	base   string
	strict bool
}

func newReviewCmd() *cobra.Command {
	var f reviewFlags
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Quick AI review of the staged diff (or the whole branch)",
		Long: `Asks the model for likely bugs, security issues and missing tests, grouped
by file with line numbers and severity. These are AI suggestions: verify
them before acting. Exits 0 unless --strict is set and a high or medium
severity finding is reported.`,
		Example: `  git quill review
  git quill review --branch --base main
  git quill review --strict   # use in a pre-push hook or CI`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runReview(cmd, &f) },
	}
	f.register(cmd)
	cmd.Flags().BoolVar(&f.branch, "branch", false, "review all changes on this branch instead of the staged diff")
	cmd.Flags().StringVar(&f.base, "base", "", "with --branch, the base branch (default: main/master)")
	cmd.Flags().BoolVar(&f.strict, "strict", false, "exit 1 when any high or medium severity finding is reported")
	return cmd
}

func runReview(cmd *cobra.Command, f *reviewFlags) error {
	a, err := openApp(cmd, &f.shared)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	src := func(ctx context.Context, n int) (string, error) { return a.Repo.StagedDiff(ctx, n) }
	what := "staged changes"
	if f.branch {
		base := f.base
		if base == "" {
			if base, err = a.Repo.DefaultBase(ctx); err != nil {
				return err
			}
		}
		src = func(ctx context.Context, n int) (string, error) { return a.Repo.RangeDiff(ctx, base, "HEAD", n) }
		what = "changes since " + base
	}
	p, err := a.Prepare(ctx, src, true)
	if err != nil {
		return err
	}
	if len(p.Files) == 0 {
		if len(p.Excluded) > 0 {
			return fmt.Errorf("nothing to review: every changed file is excluded (%s)", strings.Join(p.ExcludedList(), ", "))
		}
		if f.branch {
			return errors.New("nothing to review: no changes on this branch")
		}
		return errors.New("nothing to review: no staged changes (stage with `git add`, or use --branch)")
	}
	opts := prompt.Options{Lang: a.Cfg.Lang, Context: redactContext(a, &f.shared), Custom: a.Cfg.Prompt, Excluded: p.ExcludedList(), Note: p.Note()}
	chunks := []string{p.Text}
	if p.OverBudget {
		chunks = p.Chunks
		a.UI.Info("large diff: reviewing %d file(s) in %d chunk(s)", len(p.Files), len(chunks))
	}

	if f.showPrompt {
		for _, c := range chunks {
			a.ShowPrompt(prompt.Review(opts, c))
		}
		if f.dryRun {
			a.UI.Info("dry run: nothing was sent")
			return nil
		}
	}

	review := &app.Review{}
	var raw []string
	for i, c := range chunks {
		title := "reviewing " + what + "…"
		if len(chunks) > 1 {
			title = fmt.Sprintf("reviewing chunk %d/%d…", i+1, len(chunks))
		}
		text, err := generate(cmd, a, title, prompt.Review(opts, c))
		if err != nil {
			return err
		}
		r, perr := app.ParseReview(text)
		if perr != nil {
			raw = append(raw, strings.TrimSpace(text))
			continue
		}
		review.Merge(r)
	}

	printReview(a, review, raw)
	a.PrintUsageIf()
	if f.strict {
		if len(raw) > 0 {
			return fmt.Errorf("--strict: could not parse the model's review as JSON")
		}
		if review.Blocking() {
			return exitError{code: 1}
		}
	}
	return nil
}

func printReview(a *app.App, r *app.Review, raw []string) {
	st := a.UI.Styles()
	w := a.Out
	p, _ := a.Provider()
	model := ""
	if p != nil {
		model = " · " + p.Name() + "/" + p.Model()
	}
	fmt.Fprintln(w, st.Title.Render("AI review")+st.Dim.Render(" — suggestions only, verify before acting"+model))
	if r.Summary != "" {
		fmt.Fprintln(w, "\n"+r.Summary)
	}
	files, groups := r.ByFile()
	for _, file := range files {
		fmt.Fprintln(w, "\n"+st.Bold.Render(file))
		for _, f := range groups[file] {
			loc := "     "
			if f.Line > 0 {
				loc = fmt.Sprintf("L%-4d", f.Line)
			}
			cat := ""
			if f.Category != "" {
				cat = st.Dim.Render(" (" + f.Category + ")")
			}
			fmt.Fprintf(w, "  %s %s %s%s\n", severityBadge(st, f.Severity), loc, f.Message, cat)
			if f.Suggestion != "" {
				fmt.Fprintf(w, "  %s %s\n", strings.Repeat(" ", 14), st.Dim.Render("→ "+f.Suggestion))
			}
		}
	}
	for _, t := range raw {
		fmt.Fprintln(w, "\n"+st.Dim.Render("(unstructured response)")+"\n"+t)
	}
	c := r.Counts()
	fmt.Fprintf(w, "\n%d finding(s): %d high, %d medium, %d low, %d info\n", len(r.Findings), c["high"], c["medium"], c["low"], c["info"])
}

func severityBadge(st ui.Styles, sev string) string {
	label := fmt.Sprintf("%-8s", "["+strings.ToUpper(sev)+"]")
	switch sev {
	case "high":
		return st.High.Render(label)
	case "medium":
		return st.Medium.Render(label)
	case "low":
		return st.Low.Render(label)
	}
	return st.Note.Render(label)
}
