package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tonklasocute/git-quill/internal/app"
	"github.com/tonklasocute/git-quill/internal/ui"
)

// shared holds flags common to the generating commands.
type shared struct {
	lang       string
	context    string
	dryRun     bool
	showPrompt bool
}

func (s *shared) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&s.lang, "lang", "", "output language: en or th (default from config)")
	cmd.Flags().StringVar(&s.context, "context", "", "extra hint for the model, e.g. \"fixes the login timeout\"")
	cmd.Flags().BoolVar(&s.dryRun, "dry-run", false, "print the result instead of acting on it")
	cmd.Flags().BoolVar(&s.showPrompt, "show-prompt", false, "print exactly what is sent to the model (with --dry-run, nothing is sent)")
}

// offline reports whether nothing should be sent at all.
func (s *shared) offline() bool { return s.dryRun && s.showPrompt }

func verbose(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("verbose")
	return v
}

// openApp opens the repo in the working directory and applies flags.
func openApp(cmd *cobra.Command, s *shared) (*app.App, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	a, err := app.Open(cmd.Context(), wd, cmd.OutOrStdout(), cmd.ErrOrStderr())
	if err != nil {
		return nil, err
	}
	a.Verbose = verbose(cmd)
	if s != nil && s.lang != "" {
		a.Cfg.Lang = s.lang
		if err := a.Cfg.Validate(); err != nil {
			return nil, err
		}
	}
	return a, nil
}

// interactive reports whether we can run a TUI: stdin and stderr are both
// terminals.
func interactive(cmd *cobra.Command) bool {
	return ui.IsTerminal(cmd.InOrStdin()) && ui.IsTerminal(cmd.ErrOrStderr())
}

// redactContext redacts the --context flag before it is sent.
func redactContext(a *app.App, s *shared) string {
	if strings.TrimSpace(s.context) == "" {
		return ""
	}
	return a.RedactText(s.context)
}

// spin runs fn behind a spinner on stderr.
func spin(cmd *cobra.Command, title string, fn func(ctx context.Context, update func(string)) error) error {
	return ui.Spin(cmd.Context(), cmd.ErrOrStderr(), title, fn)
}

func progressTitle(prefix string) func(update func(string)) func(done, total int) {
	return func(update func(string)) func(done, total int) {
		return func(done, total int) { update(fmt.Sprintf("%s %d/%d", prefix, done, total)) }
	}
}
