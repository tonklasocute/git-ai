package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tonklasocute/git-quill/internal/app"
	"github.com/tonklasocute/git-quill/internal/prompt"
)

const hookMarker = "git-quill-hook-v1"

// hookScript is POSIX sh so it also runs under Git for Windows.
func hookScript(bin string) string {
	return `#!/bin/sh
# ` + hookMarker + `
# Pre-fills "git commit" messages with an AI suggestion from git-quill.
# Remove with: git quill hook uninstall   (skip once: GIT_QUILL_SKIP_HOOK=1 git commit)
[ -n "$GIT_QUILL_SKIP_HOOK" ] && exit 0
GIT_QUILL_BIN="` + bin + `"
[ -x "$GIT_QUILL_BIN" ] || GIT_QUILL_BIN="$(command -v git-quill 2>/dev/null)"
[ -n "$GIT_QUILL_BIN" ] || exit 0
"$GIT_QUILL_BIN" hook run "$1" "$2" "$3" </dev/null || true
exit 0
`
}

func newHookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Install or remove the prepare-commit-msg hook",
		Long: `The hook pre-fills the message editor of a plain "git commit" with an AI
suggestion. It skips merges, squashes, amends and messages given with -m/-F,
and never blocks a commit: on any error the message is simply left empty.`,
	}
	var force bool
	install := &cobra.Command{
		Use:   "install",
		Short: "Install the hook in this repository",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return hookInstall(cmd, force)
		},
	}
	install.Flags().BoolVar(&force, "force", false, "replace an existing hook (it is backed up as prepare-commit-msg.bak)")
	uninstall := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the hook (restoring a backed-up hook if there is one)",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return hookUninstall(cmd) },
	}
	run := &cobra.Command{
		Use:    "run <msg-file> [source] [sha]",
		Short:  "Called by the hook; writes a suggestion into the message file",
		Hidden: true,
		Args:   cobra.RangeArgs(1, 3),
		RunE:   func(cmd *cobra.Command, args []string) error { return hookRun(cmd, args) },
	}
	cmd.AddCommand(install, uninstall, run)
	return cmd
}

func hookPath(cmd *cobra.Command) (*app.App, string, error) {
	a, err := openApp(cmd, nil)
	if err != nil {
		return nil, "", err
	}
	dir, err := a.Repo.HooksDir(cmd.Context())
	if err != nil {
		return nil, "", err
	}
	return a, filepath.Join(dir, "prepare-commit-msg"), nil
}

func hookInstall(cmd *cobra.Command, force bool) error {
	a, path, err := hookPath(cmd)
	if err != nil {
		return err
	}
	bin, err := os.Executable()
	if err != nil {
		bin = "git-quill"
	}
	verb := "installed"
	if old, err := os.ReadFile(path); err == nil {
		switch {
		case strings.Contains(string(old), hookMarker):
			verb = "updated"
		case !force:
			return fmt.Errorf("a prepare-commit-msg hook already exists at %s\n  Re-run with --force to replace it (it will be saved as prepare-commit-msg.bak)", path)
		default:
			if err := os.Rename(path, path+".bak"); err != nil {
				return err
			}
			a.UI.Info("backed up the existing hook to %s.bak", path)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(hookScript(filepath.ToSlash(bin))), 0o755); err != nil { //nolint:gosec // hooks must be executable
		return err
	}
	a.UI.Success("%s %s", verb, path)
	a.UI.Dim("  Plain `git commit` now opens your editor with an AI suggestion. Skip once with GIT_QUILL_SKIP_HOOK=1.")
	return nil
}

func hookUninstall(cmd *cobra.Command) error {
	a, path, err := hookPath(cmd)
	if err != nil {
		return err
	}
	old, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		a.UI.Info("no prepare-commit-msg hook installed")
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.Contains(string(old), hookMarker) {
		return fmt.Errorf("%s was not installed by git-quill; leaving it alone", path)
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	if _, err := os.Stat(path + ".bak"); err == nil {
		if err := os.Rename(path+".bak", path); err != nil {
			return err
		}
		a.UI.Success("removed the git-quill hook and restored your previous hook")
		return nil
	}
	a.UI.Success("removed %s", path)
	return nil
}

// hookRun implements the hook. It never returns an error to git: failures
// are printed and the commit proceeds with git's normal message.
func hookRun(cmd *cobra.Command, args []string) error {
	msgFile := args[0]
	source := ""
	if len(args) > 1 {
		source = args[1]
	}
	switch source {
	case "message", "merge", "squash", "commit":
		return nil // -m/-F, merges, squashes, amend/-c/-C: the user already has a message
	}
	existing, err := os.ReadFile(msgFile)
	if err != nil {
		return nil //nolint:nilerr // never block a commit
	}
	if source == "template" && hasContent(string(existing)) {
		return nil
	}
	if err := hookGenerate(cmd, msgFile, string(existing)); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "git-quill: %v\n  (continuing without a suggestion)\n", err)
	}
	return nil
}

func hookGenerate(cmd *cobra.Command, msgFile, existing string) error {
	a, err := openApp(cmd, nil)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	fmt.Fprintln(cmd.ErrOrStderr(), "git-quill: writing a commit message suggestion…")
	p, err := a.Prepare(ctx, func(ctx context.Context, n int) (string, error) { return a.Repo.StagedDiff(ctx, n) }, false)
	if err != nil {
		return err
	}
	if len(p.Files) == 0 && len(p.Excluded) == 0 {
		return errors.New("nothing staged")
	}
	branch, _ := a.Repo.CurrentBranch(ctx)
	opts := prompt.Options{
		Lang: a.Cfg.Lang, Style: a.Cfg.Style, MaxSubject: a.Cfg.MaxSubjectLength, Emoji: a.Cfg.Emoji,
		Custom: a.Cfg.Prompt, Branch: branch, Excluded: p.ExcludedList(), Note: p.Note(),
	}
	req := prompt.Commit(opts, p.Text)
	if p.OverBudget {
		sum, err := a.Summarize(ctx, p, nil)
		if err != nil {
			return err
		}
		req = prompt.CommitFromSummaries(opts, sum)
	}
	resp, err := a.Call(ctx, req, nil)
	if err != nil {
		return err
	}
	cands := app.SplitCandidates(resp.Text, a.Cfg.MaxSubjectLength)
	if len(cands) == 0 {
		return errors.New("the model returned an empty message")
	}
	msg := cands[0] + "\n\n# ↑ Suggested by git-quill. Edit freely; lines starting with # are ignored.\n"
	if existing != "" {
		msg += strings.TrimLeft(existing, "\n")
	}
	return os.WriteFile(msgFile, []byte(msg), 0o600)
}

func hasContent(s string) bool {
	for _, l := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "#") {
			return true
		}
	}
	return false
}
