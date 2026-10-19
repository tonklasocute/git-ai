package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/atotto/clipboard"
	"github.com/spf13/cobra"

	"github.com/tonklasocute/git-quill/internal/app"
	"github.com/tonklasocute/git-quill/internal/prompt"
	"github.com/tonklasocute/git-quill/internal/provider"
)

type prFlags struct {
	shared
	base   string
	create bool
	draft  bool
	yes    bool
}

func newPRCmd() *cobra.Command {
	var f prFlags
	cmd := &cobra.Command{
		Use:   "pr",
		Short: "Write a pull request title and description for the current branch",
		Long: `Uses the commits and diff between the base branch (main/master, or --base)
and HEAD. Prints the PR and copies it to the clipboard; with --create it
opens the PR with the GitHub CLI (gh).`,
		Example: `  git quill pr
  git quill pr --base develop --lang th
  git quill pr --create --draft`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runPR(cmd, &f) },
	}
	f.register(cmd)
	cmd.Flags().StringVar(&f.base, "base", "", "base branch (default: origin's HEAD, main or master)")
	cmd.Flags().BoolVar(&f.create, "create", false, "open the PR with `gh pr create`")
	cmd.Flags().BoolVar(&f.draft, "draft", false, "with --create, open as a draft")
	cmd.Flags().BoolVarP(&f.yes, "yes", "y", false, "with --create, do not ask for confirmation")
	return cmd
}

func runPR(cmd *cobra.Command, f *prFlags) error {
	a, err := openApp(cmd, &f.shared)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	branch, _ := a.Repo.CurrentBranch(ctx)
	if branch == "" {
		return errors.New("HEAD is detached; check out the branch you want to open a PR for")
	}
	base := f.base
	if base == "" {
		if base, err = a.Repo.DefaultBase(ctx); err != nil {
			return err
		}
	} else if !a.Repo.RefExists(ctx, base) {
		return fmt.Errorf("base branch %q not found", base)
	}
	if branch == base {
		return fmt.Errorf("you are on %s itself; switch to a feature branch first", base)
	}
	commits, err := a.Repo.Log(ctx, base, "HEAD")
	if err != nil {
		return err
	}
	if len(commits) == 0 {
		return fmt.Errorf("no commits on %s that are not on %s", branch, base)
	}

	p, err := a.Prepare(ctx, func(ctx context.Context, n int) (string, error) { return a.Repo.RangeDiff(ctx, base, "HEAD", n) }, false)
	if err != nil {
		return err
	}
	opts := prompt.Options{
		Lang: a.Cfg.Lang, MaxSubject: a.Cfg.MaxSubjectLength, Context: redactContext(a, &f.shared),
		Custom: a.Cfg.Prompt, Branch: branch, Base: base, Excluded: p.ExcludedList(), Note: p.Note(),
	}
	safeCommits := redactCommits(a, commits)
	a.Strategy(p, p.OverBudget)

	var req provider.Request
	if p.OverBudget {
		if f.offline() {
			for _, c := range p.Chunks {
				a.ShowPrompt(prompt.Summarize(c))
			}
			a.UI.Info("the final request is built from the model's summaries of the %d chunk(s) above; nothing was sent", len(p.Chunks))
			return nil
		}
		sum, err := summarize(cmd, a, p)
		if err != nil {
			return err
		}
		req = prompt.PR(opts, safeCommits, sum, true)
	} else {
		req = prompt.PR(opts, safeCommits, p.Text, false)
	}
	if f.showPrompt {
		a.ShowPrompt(req)
		if f.dryRun {
			a.UI.Info("dry run: nothing was sent")
			return nil
		}
	}

	text, err := generate(cmd, a, "writing pull request…", req)
	if err != nil {
		return err
	}
	title, body := app.ParsePR(text)
	if title == "" {
		return errors.New("the model returned an empty pull request; try again")
	}
	if missing := missingHeadings(body, a.Cfg.Lang); len(missing) > 0 {
		a.UI.Warn("the description is missing %s; the model may have stopped early. Run again or try a larger model.", strings.Join(missing, ", "))
	}
	fmt.Fprintf(a.Out, "%s\n\n%s\n", title, body)
	a.PrintUsageIf()
	if f.dryRun {
		return nil
	}

	if f.create {
		gh, err := exec.LookPath("gh")
		if err != nil {
			a.UI.Warn("the GitHub CLI (gh) is not installed: https://cli.github.com")
			copyToClipboard(a, title+"\n\n"+body)
			return nil
		}
		if !f.yes && interactive(cmd) && !confirm(cmd.InOrStdin(), cmd.ErrOrStderr(), fmt.Sprintf("Open this PR from %s into %s with gh?", branch, base)) {
			a.UI.Println("Not created.")
			return nil
		}
		return ghCreate(cmd, gh, base, branch, title, body, f.draft)
	}
	if interactive(cmd) {
		copyToClipboard(a, title+"\n\n"+body)
	}
	return nil
}

func ghCreate(cmd *cobra.Command, gh, base, head, title, body string, draft bool) error {
	f, err := os.CreateTemp("", "git-quill-pr-*.md")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(body); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	args := []string{"pr", "create", "--base", base, "--head", head, "--title", title, "--body-file", f.Name()}
	if draft {
		args = append(args, "--draft")
	}
	c := exec.CommandContext(cmd.Context(), gh, args...)
	c.Stdin, c.Stdout, c.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
	if err := c.Run(); err != nil {
		return fmt.Errorf("gh pr create failed: %w (is the branch pushed? try `git push -u origin %s`)", err, head)
	}
	return nil
}

func copyToClipboard(a *app.App, text string) {
	if clipboard.Unsupported {
		return
	}
	if err := clipboard.WriteAll(text); err != nil {
		a.UI.Dim("(could not copy to the clipboard: %v)", err)
		return
	}
	a.UI.Success("copied to the clipboard")
}

// missingHeadings lists required PR sections absent from body.
func missingHeadings(body, lang string) []string {
	var missing []string
	for _, h := range prompt.PRHeadings(lang) {
		if !strings.Contains("\n"+body+"\n", "\n"+h+"\n") {
			missing = append(missing, `"`+h+`"`)
		}
	}
	return missing
}
