// Command git-quill writes commit messages, PR descriptions, changelogs and
// quick code reviews from your git diff. Installed on PATH it also works as
// `git quill <command>`.
package main

import (
	"os"

	"github.com/tonklasocute/git-quill/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
