package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/tonklasocute/git-quill/internal/config"
	"github.com/tonklasocute/git-quill/internal/git"
	"github.com/tonklasocute/git-quill/internal/provider/ollama"
	"github.com/tonklasocute/git-quill/internal/ui"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Interactive setup, or get/set/list settings and API keys",
		Long: `Without a subcommand, walks you through choosing a provider, model,
language and API key. Settings live in ~/.config/git-quill/config.toml
(global) and .git-quill.toml (per repo). API keys are read from environment
variables or the OS keychain, never from these files.`,
		Example: `  git quill config
  git quill config set provider ollama
  git quill config set model llama3.1:8b
  git quill config set lang th --repo
  git quill config set-key openai
  git quill config list`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runSetup(cmd) },
	}
	var repo bool
	set := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a value (provider-specific keys apply to the current provider)",
		Long:  "Keys: " + strings.Join(config.Keys, ", "),
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigSet(cmd, args[0], args[1], repo)
		},
	}
	set.Flags().BoolVar(&repo, "repo", false, "write to this repository's .git-quill.toml instead of the global file")
	get := &cobra.Command{
		Use:   "get <key>",
		Short: "Print the effective value of a key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			v, err := cfg.Get(args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), v)
			return nil
		},
	}
	list := &cobra.Command{
		Use:   "list",
		Short: "Show effective settings, config files and API key status",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runConfigList(cmd) },
	}
	setKey := &cobra.Command{
		Use:   "set-key <provider>",
		Short: "Store an API key in the OS keychain (reads from stdin)",
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return runSetKey(cmd, args[0]) },
	}
	deleteKey := &cobra.Command{
		Use:   "delete-key <provider>",
		Short: "Remove a stored API key from the OS keychain",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := config.DeleteKey(args[0]); err != nil {
				return err
			}
			ui.NewPrinter(cmd.ErrOrStderr()).Success("removed the %s key from the OS keychain", args[0])
			return nil
		},
	}
	path := &cobra.Command{
		Use:   "path",
		Short: "Print the global config file path",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := config.GlobalPath()
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), p)
			return nil
		},
	}
	cmd.AddCommand(set, get, list, setKey, deleteKey, path)
	return cmd
}

// loadConfig loads config with the repo layer when inside a repository.
func loadConfig(cmd *cobra.Command) (*config.Config, string, error) {
	root := ""
	if wd, err := os.Getwd(); err == nil {
		if r, err := git.Open(cmd.Context(), wd); err == nil {
			root, _ = r.Root(cmd.Context())
		}
	}
	cfg, err := config.Load(root)
	return cfg, root, err
}

func runConfigSet(cmd *cobra.Command, key, value string, repo bool) error {
	cfg, root, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	path, err := config.GlobalPath()
	if err != nil {
		return err
	}
	if repo {
		if root == "" {
			return errors.New("--repo needs to run inside a git repository")
		}
		if key == "provider" || key == "base_url" {
			return fmt.Errorf("%s cannot be set per repository (a cloned repo must not redirect your code to another endpoint); set it globally", key)
		}
		path = filepath.Join(root, config.RepoFile)
	}
	if strings.Contains(strings.ToLower(key), "key") {
		return errors.New("API keys are not stored in config files; use `git quill config set-key <provider>`")
	}
	if err := config.Set(path, cfg.Provider, key, value); err != nil {
		return err
	}
	ui.NewPrinter(cmd.ErrOrStderr()).Success("set %s = %s in %s", key, value, path)
	return nil
}

func runConfigList(cmd *cobra.Command) error {
	cfg, root, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	for _, l := range cfg.List() {
		fmt.Fprintln(w, l)
	}
	info, _ := config.Lookup(cfg.Provider)
	_, src, where := config.APIKey(info)
	status := "not needed"
	switch {
	case src == config.KeyFromEnv:
		status = "set via " + where
	case src == config.KeyFromKeyring:
		status = "stored in the OS keychain"
	case info.NeedsKey:
		status = "missing — run `git quill config set-key " + info.Name + "`"
	}
	fmt.Fprintf(w, "\napi key (%s): %s\n", info.Name, status)
	global, _ := config.GlobalPath()
	fmt.Fprintf(w, "global config: %s%s\n", global, existsNote(global))
	if root != "" {
		rp := filepath.Join(root, config.RepoFile)
		fmt.Fprintf(w, "repo config:   %s%s\n", rp, existsNote(rp))
	}
	pr := ui.NewPrinter(cmd.ErrOrStderr())
	for _, warn := range cfg.Warnings {
		pr.Warn("%s", warn)
	}
	return nil
}

func existsNote(p string) string {
	if _, err := os.Stat(p); err != nil {
		return " (not created)"
	}
	return ""
}

// readSecret reads one line, hiding input on a terminal.
func readSecret(cmd *cobra.Command, r *bufio.Reader, label string) (string, error) {
	fmt.Fprint(cmd.ErrOrStderr(), label)
	if f, ok := cmd.InOrStdin().(*os.File); ok && ui.IsTerminal(f) {
		b, err := term.ReadPassword(int(f.Fd())) //nolint:gosec // fd fits in int
		fmt.Fprintln(cmd.ErrOrStderr())
		return strings.TrimSpace(string(b)), err
	}
	line, err := r.ReadString('\n')
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return strings.TrimSpace(line), err
}

func runSetKey(cmd *cobra.Command, name string) error {
	info, ok := config.Lookup(name)
	if !ok {
		return fmt.Errorf("unknown provider %q (choose one of: %s)", name, strings.Join(config.ProviderNames(), ", "))
	}
	key, err := readSecret(cmd, bufio.NewReader(cmd.InOrStdin()), fmt.Sprintf("Paste the API key for %s (input hidden): ", info.Title))
	if err != nil {
		return err
	}
	if err := config.SetKey(name, key); err != nil {
		return err
	}
	ui.NewPrinter(cmd.ErrOrStderr()).Success("stored the %s key in the OS keychain (service %q)", name, config.KeyringService)
	return nil
}

func runSetup(cmd *cobra.Command) error {
	cfg, _, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	path, err := config.GlobalPath()
	if err != nil {
		return err
	}
	in := bufio.NewReader(cmd.InOrStdin())
	out := cmd.ErrOrStderr()
	pr := ui.NewPrinter(out)
	st := pr.Styles()
	ask := func(label, def string) string {
		fmt.Fprintf(out, "%s %s ", label, st.Dim.Render("["+def+"]"))
		line, _ := in.ReadString('\n')
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
		return def
	}

	fmt.Fprintln(out, st.Title.Render("git-quill setup")+st.Dim.Render(" — writes "+path))
	fmt.Fprintln(out, "\nWhich provider?")
	def := 1
	for i, p := range config.Providers {
		if p.Name == "fake" {
			continue
		}
		mark := ""
		if _, src, where := config.APIKey(p); src == config.KeyFromEnv {
			mark = st.Success.Render(" ✓ " + where)
		} else if src == config.KeyFromKeyring {
			mark = st.Success.Render(" ✓ keychain")
		}
		if p.Name == cfg.Provider {
			def = i + 1
		}
		fmt.Fprintf(out, "  %d) %s%s\n", i+1, p.Title, mark)
	}
	choice := ask("Provider", strconv.Itoa(def))
	n, err := strconv.Atoi(choice)
	var info config.ProviderInfo
	if err == nil && n >= 1 && n <= len(config.Providers) {
		info = config.Providers[n-1]
	} else if p, ok := config.Lookup(choice); ok {
		info = p
	} else {
		return fmt.Errorf("invalid choice %q", choice)
	}

	cfg.Provider = info.Name
	_, pc := cfg.Active()
	model := ask("Model", orDefault(pc.Model, "(required)"))
	if model == "(required)" {
		return fmt.Errorf("%s needs a model id; see the provider's model list", info.Name)
	}
	baseURL := pc.BaseURL
	if info.Name == "compatible" || info.Local {
		baseURL = ask("Base URL", orDefault(baseURL, "http://localhost:8080/v1"))
	}
	lang := ask("Commit message language (en/th)", cfg.Lang)

	settings := [][2]string{{"provider", info.Name}, {"model", model}, {"lang", lang}}
	if baseURL != "" && baseURL != info.DefaultBaseURL {
		settings = append(settings, [2]string{"base_url", baseURL})
	}
	for _, kv := range settings {
		if err := config.Set(path, info.Name, kv[0], kv[1]); err != nil {
			return err
		}
	}

	if info.NeedsKey || info.Name == "compatible" {
		if _, src, where := config.APIKey(info); src != config.KeyMissing {
			pr.Success("API key found (%s)", orDefault(where, string(src)))
		} else {
			key, err := readSecret(cmd, in, fmt.Sprintf("API key for %s (hidden; stored in the OS keychain; empty to skip): ", info.Title))
			if err != nil {
				return err
			}
			if key != "" {
				if err := config.SetKey(info.Name, key); err != nil {
					pr.Warn("%v", err)
				} else {
					pr.Success("stored the key in the OS keychain")
				}
			} else if len(info.KeyEnv) > 0 {
				pr.Info("skipped; set %s later or run `git quill config set-key %s`", info.KeyEnv[0], info.Name)
			}
		}
	}
	if info.Kind == "ollama" {
		checkOllama(cmd.Context(), pr, orDefault(baseURL, ollama.DefaultBaseURL), model)
	}
	pr.Success("saved %s", path)
	fmt.Fprintln(out, st.Dim.Render("Try it: git add -p && git quill commit"))
	return nil
}

// checkOllama reports whether the server is up and the model is pulled.
func checkOllama(ctx context.Context, pr *ui.Printer, base, model string) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/api/tags", nil)
	if err != nil {
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		pr.Warn("Ollama is not reachable at %s. Install it from https://ollama.com/download and run `ollama serve`.", base)
		return
	}
	defer resp.Body.Close()
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&tags)
	for _, m := range tags.Models {
		if m.Name == model || strings.TrimSuffix(m.Name, ":latest") == model {
			pr.Success("Ollama is running and %s is available — everything stays on this machine", model)
			return
		}
	}
	pr.Warn("Ollama is running, but %s is not downloaded yet. Run: ollama pull %s", model, model)
}
