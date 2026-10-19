package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tonklasocute/git-quill/internal/app"
	"github.com/tonklasocute/git-quill/internal/git"
	"github.com/tonklasocute/git-quill/internal/provider"
)

// summarize runs map-reduce's map step behind a spinner.
func summarize(cmd *cobra.Command, a *app.App, p *app.Prepared) (string, error) {
	var summaries string
	err := spin(cmd, "summarizing large diff…", func(ctx context.Context, update func(string)) error {
		var err error
		summaries, err = a.Summarize(ctx, p, progressTitle("summarizing chunk")(update))
		return err
	})
	return summaries, err
}

// generate sends one request behind a spinner and returns the text.
func generate(cmd *cobra.Command, a *app.App, title string, req provider.Request) (string, error) {
	var text string
	err := spin(cmd, title, func(ctx context.Context, _ func(string)) error {
		resp, err := a.Call(ctx, req, nil)
		if err != nil {
			return err
		}
		text = resp.Text
		return nil
	})
	return text, err
}

// redactCommits redacts subjects and bodies before they are sent.
func redactCommits(a *app.App, commits []git.Commit) []git.Commit {
	out := make([]git.Commit, len(commits))
	for i, c := range commits {
		out[i] = git.Commit{Hash: c.Hash, Subject: c.Subject, Body: c.Body}
	}
	var b strings.Builder
	for _, c := range out {
		b.WriteString(c.Subject + "\x1f" + c.Body + "\x1e")
	}
	red := a.RedactText(b.String())
	recs := strings.Split(red, "\x1e")
	for i := range out {
		if i >= len(recs) {
			break
		}
		parts := strings.SplitN(recs[i], "\x1f", 2)
		out[i].Subject = parts[0]
		if len(parts) == 2 {
			out[i].Body = parts[1]
		}
	}
	return out
}

// confirm asks a yes/no question on stderr, reading a line from in.
func confirm(in io.Reader, out io.Writer, question string) bool {
	fmt.Fprintf(out, "%s [y/N] ", question)
	line, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}
