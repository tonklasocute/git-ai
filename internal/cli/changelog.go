package cli

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tonklasocute/git-quill/internal/app"
	"github.com/tonklasocute/git-quill/internal/diff"
	"github.com/tonklasocute/git-quill/internal/git"
	"github.com/tonklasocute/git-quill/internal/prompt"
)

type changelogFlags struct {
	shared
	from, to, version, write string
}

func newChangelogCmd() *cobra.Command {
	var f changelogFlags
	cmd := &cobra.Command{
		Use:   "changelog",
		Short: "Write Keep a Changelog release notes from commits since the last tag",
		Example: `  git quill changelog
  git quill changelog --version v1.3.0 --write CHANGELOG.md
  git quill changelog --from v1.1.0 --to v1.2.0 --version v1.2.0`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runChangelog(cmd, &f) },
	}
	f.register(cmd)
	cmd.Flags().StringVar(&f.from, "from", "", "start ref, exclusive (default: last tag, or the first commit)")
	cmd.Flags().StringVar(&f.to, "to", "HEAD", "end ref, inclusive")
	cmd.Flags().StringVar(&f.version, "version", "", "release name for the heading (default: Unreleased)")
	cmd.Flags().StringVar(&f.write, "write", "", "prepend the section to this file (e.g. CHANGELOG.md)")
	return cmd
}

func runChangelog(cmd *cobra.Command, f *changelogFlags) error {
	a, err := openApp(cmd, &f.shared)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	from := f.from
	if from == "" {
		from = a.Repo.LastTag(ctx)
	}
	for _, ref := range []string{from, f.to} {
		if ref != "" && !a.Repo.RefExists(ctx, ref) {
			return fmt.Errorf("unknown ref %q", ref)
		}
	}
	commits, err := a.Repo.Log(ctx, from, f.to)
	if err != nil {
		return err
	}
	if len(commits) == 0 {
		where := "the beginning"
		if from != "" {
			where = from
		}
		return fmt.Errorf("no commits between %s and %s", where, f.to)
	}
	if a.Verbose {
		a.UI.Dim("changelog: %d commit(s) from %s to %s", len(commits), orDefault(from, "root"), f.to)
	}
	commits = fitCommits(a, redactCommits(a, commits))

	version := f.version
	if version == "" {
		version = "Unreleased"
	}
	req := prompt.Changelog(prompt.Options{Lang: a.Cfg.Lang, Custom: a.Cfg.Prompt}, version, commits)
	if f.showPrompt {
		a.ShowPrompt(req)
		if f.dryRun {
			a.UI.Info("dry run: nothing was sent")
			return nil
		}
	}
	text, err := generate(cmd, a, "writing changelog…", req)
	if err != nil {
		return err
	}
	section := app.SectionHeading(version, time.Now().Format("2006-01-02")) + "\n\n" + app.CleanSection(text) + "\n"
	a.PrintUsageIf()

	if f.write == "" || f.dryRun {
		fmt.Fprint(a.Out, section)
		return nil
	}
	path := f.write
	if !filepath.IsAbs(path) {
		wd, _ := filepath.Abs(".")
		path = filepath.Join(wd, path)
	}
	if err := app.PrependSection(path, section); err != nil {
		return err
	}
	a.UI.Success("wrote %s section to %s", strings.SplitN(section, "\n", 2)[0], f.write)
	return nil
}

// fitCommits drops bodies, then the oldest commits, until the list fits the
// token budget.
func fitCommits(a *app.App, commits []git.Commit) []git.Commit {
	budget := a.Budget()
	if diff.EstimateTokens(prompt.FormatCommits(commits)) <= budget {
		return commits
	}
	for i := range commits {
		commits[i].Body = ""
	}
	dropped := 0
	for len(commits) > 1 && diff.EstimateTokens(prompt.FormatCommits(commits)) > budget {
		commits = commits[1:]
		dropped++
	}
	msg := "large history: dropped commit bodies"
	if dropped > 0 {
		msg += fmt.Sprintf(" and the %d oldest commit(s)", dropped)
	}
	a.UI.Info("%s to fit the token budget", msg)
	return commits
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
