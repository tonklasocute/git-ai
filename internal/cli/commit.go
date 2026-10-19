package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tonklasocute/git-quill/internal/app"
	"github.com/tonklasocute/git-quill/internal/prompt"
	"github.com/tonklasocute/git-quill/internal/provider"
	"github.com/tonklasocute/git-quill/internal/ui"
)

var commitTypes = []string{"feat", "fix", "docs", "style", "refactor", "perf", "test", "build", "ci", "chore", "revert"}

type commitFlags struct {
	shared
	n      int
	yes    bool
	typ    string
	noBody bool
	emoji  bool
}

func newCommitCmd() *cobra.Command {
	var f commitFlags
	cmd := &cobra.Command{
		Use:   "commit [flags] [-- <git commit args>]",
		Short: "Write a commit message for the staged changes, then commit",
		Long: `Reads the staged diff, redacts secrets, asks the model for a Conventional
Commit message and lets you accept, edit, regenerate or choose before running
git commit. Anything after "--" is passed to git commit (e.g. -- --no-verify).`,
		Example: `  git quill commit
  git quill commit -n 3 --lang th
  git quill commit --yes --type fix --context "closes the login timeout"
  git quill commit --dry-run --show-prompt   # see exactly what would be sent`,
		RunE: func(cmd *cobra.Command, args []string) error { return runCommit(cmd, &f, args) },
	}
	f.register(cmd)
	cmd.Flags().IntVarP(&f.n, "candidates", "n", 1, "number of alternative messages to generate (1-5)")
	cmd.Flags().BoolVarP(&f.yes, "yes", "y", false, "commit with the first suggestion without asking")
	cmd.Flags().StringVar(&f.typ, "type", "", "force the commit type ("+strings.Join(commitTypes, ", ")+")")
	cmd.Flags().BoolVar(&f.noBody, "no-body", false, "subject line only")
	cmd.Flags().BoolVar(&f.emoji, "emoji", false, "prefix the subject with a gitmoji")
	return cmd
}

func runCommit(cmd *cobra.Command, f *commitFlags, gitArgs []string) error {
	if f.n < 1 || f.n > 5 {
		return errors.New("--candidates must be between 1 and 5")
	}
	if f.typ != "" && !contains(commitTypes, f.typ) {
		return fmt.Errorf("unknown --type %q (use one of: %s)", f.typ, strings.Join(commitTypes, ", "))
	}
	a, err := openApp(cmd, &f.shared)
	if err != nil {
		return err
	}
	ctx := cmd.Context()

	staged, err := a.Repo.StagedFiles(ctx)
	if err != nil {
		return err
	}
	if len(staged) == 0 {
		msg := "nothing staged to commit.\n  Stage changes first, e.g. `git add -p` or `git add <file>`, then run `git quill commit` again."
		if a.Repo.HasUnstagedChanges(ctx) {
			msg += "\n  (You have unstaged changes; `git status` shows them.)"
		}
		return errors.New(msg)
	}
	if !f.yes && !f.dryRun && !interactive(cmd) {
		return errors.New("not running in a terminal: use --yes to commit without review, or --dry-run to print the message")
	}

	gen, err := commitGenerator(cmd, a, f)
	if err != nil || gen == nil {
		return err
	}

	var message string
	switch {
	case f.dryRun || f.yes:
		var cands []string
		err := spin(cmd, "generating commit message…", func(ctx context.Context, _ func(string)) error {
			var err error
			cands, err = gen(ctx, nil)
			return err
		})
		if err != nil {
			return err
		}
		if f.dryRun {
			fmt.Fprintln(a.Out, strings.Join(cands, "\n"+prompt.Separator+"\n"))
			a.PrintUsageIf()
			return nil
		}
		message = cands[0]
	default:
		picker := ui.NewCommitPicker(a.UI, gen, a.Repo.Editor(ctx))
		if err := picker.Run(ctx, cmd.InOrStdin(), cmd.ErrOrStderr()); err != nil {
			return err
		}
		if !picker.Accepted {
			a.UI.Println("Aborted — nothing was committed.")
			return exitError{code: 1}
		}
		message = picker.Message
	}

	if err := a.Repo.CommitWithMessage(ctx, message+"\n", cmd.OutOrStdout(), cmd.ErrOrStderr(), gitArgs...); err != nil {
		return err
	}
	a.PrintUsageIf()
	return nil
}

// commitGenerator prepares the diff (and summaries for huge diffs) and
// returns a function that produces candidates. It returns (nil, nil) when
// --dry-run --show-prompt has already printed everything.
func commitGenerator(cmd *cobra.Command, a *app.App, f *commitFlags) (ui.GenerateFunc, error) {
	ctx := cmd.Context()
	p, err := a.Prepare(ctx, func(ctx context.Context, n int) (string, error) { return a.Repo.StagedDiff(ctx, n) }, false)
	if err != nil {
		return nil, err
	}
	branch, _ := a.Repo.CurrentBranch(ctx)
	opts := prompt.Options{
		Lang:       a.Cfg.Lang,
		Style:      a.Cfg.Style,
		MaxSubject: a.Cfg.MaxSubjectLength,
		NoBody:     f.noBody,
		Emoji:      f.emoji || a.Cfg.Emoji,
		Type:       f.typ,
		Context:    redactContext(a, &f.shared),
		Custom:     a.Cfg.Prompt,
		Candidates: f.n,
		Branch:     branch,
		Excluded:   p.ExcludedList(),
		Note:       p.Note(),
	}
	a.Strategy(p, p.OverBudget)

	var req provider.Request
	if p.OverBudget {
		if f.offline() {
			for _, c := range p.Chunks {
				a.ShowPrompt(prompt.Summarize(c))
			}
			a.UI.Info("the final request is built from the model's summaries of the %d chunk(s) above; nothing was sent", len(p.Chunks))
			return nil, nil
		}
		summaries, err := summarize(cmd, a, p)
		if err != nil {
			return nil, err
		}
		req = prompt.CommitFromSummaries(opts, summaries)
	} else {
		req = prompt.Commit(opts, p.Text)
	}
	if f.showPrompt {
		a.ShowPrompt(req)
		if f.dryRun {
			a.UI.Info("dry run: nothing was sent")
			return nil, nil
		}
	}
	maxSubject := a.Cfg.MaxSubjectLength
	return func(ctx context.Context, onDelta func(string)) ([]string, error) {
		resp, err := a.Call(ctx, req, onDelta)
		if err != nil {
			return nil, err
		}
		cands := app.SplitCandidates(resp.Text, maxSubject)
		if len(cands) == 0 {
			return nil, errors.New("the model returned an empty message; try again or use a different model")
		}
		return cands, nil
	}, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
