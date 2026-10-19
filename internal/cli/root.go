// Package cli wires the cobra command tree for git-quill.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// version is set at build time via -ldflags.
var version = "dev"

// exitError lets a command choose the process exit code without printing
// anything extra (e.g. `review --strict` findings).
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

// Execute runs the root command and returns the process exit code.
func Execute() int {
	return run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	root := newRootCmd()
	root.SetArgs(args)
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)
	if err := root.Execute(); err != nil {
		var ee exitError
		if errors.As(err, &ee) {
			return ee.code
		}
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "git-quill",
		Short:         "AI commit messages, PR descriptions, changelogs and reviews from your git diff",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolP("verbose", "v", false, "show token usage, retries and diff budgeting details")
	root.AddCommand(newCommitCmd(), newPRCmd(), newChangelogCmd(), newReviewCmd(), newHookCmd(), newConfigCmd())
	return root
}
