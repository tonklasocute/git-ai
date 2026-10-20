<h1 align="center">git-quill ✒️</h1>

<p align="center">
  <b>AI commit messages, PR descriptions, changelogs and quick reviews from your git diff.</b><br>
  One static binary · runs fully local with Ollama · redacts secrets before anything leaves your machine.
</p>

<p align="center">
  <a href="https://github.com/tonklasocute/git-quill/actions/workflows/ci.yml"><img src="https://github.com/tonklasocute/git-quill/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://pkg.go.dev/github.com/tonklasocute/git-quill"><img src="https://pkg.go.dev/badge/github.com/tonklasocute/git-quill.svg" alt="Go Reference"></a>
  <a href="https://goreportcard.com/report/github.com/tonklasocute/git-quill"><img src="https://goreportcard.com/badge/github.com/tonklasocute/git-quill" alt="Go Report Card"></a>
  <a href="https://github.com/tonklasocute/git-quill/releases"><img src="https://img.shields.io/github/v/release/tonklasocute/git-quill?include_prereleases" alt="Release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT License"></a>
</p>

<p align="center"><a href="README.th.md">อ่านภาษาไทย</a></p>

<p align="center"><img src="docs/gifs/commit.gif" alt="git quill commit: generate, regenerate, choose and accept a commit message" width="900"></p>

```console
$ git add -p
$ git quill commit          # suggest → [a]ccept [e]dit [r]egenerate [c]hoose [q]uit
$ git quill pr --create     # title + description, opened with gh
$ git quill changelog --version v1.4.0 --write CHANGELOG.md
$ git quill review          # likely bugs, security issues, missing tests
```

## Why git-quill?

- **One static binary.** No Node, no Python, no runtime. `go install` or download a release (Homebrew tap planned), and `git quill` just works (git runs any `git-<name>` on your `PATH`).
- **Private by default.** Secrets in your diff (cloud keys, tokens, private keys, passwords, high-entropy `.env` values) are replaced with `[REDACTED:<type>]` *before* the request is built, and you get a warning listing what was removed. Lockfiles, binaries, generated and vendored files never leave the machine. `--dry-run --show-prompt` prints exactly what would be sent and sends nothing.
- **Fully local if you want.** Ollama and LM Studio are first-class: free, offline, no account.
- **Big diffs handled.** Over the token budget? It drops excluded files, shrinks diff context, then summarizes per file (map-reduce), and tells you which strategy it used.
- **Thai and English.** `--lang th` writes natural Thai while keeping types, scopes and identifiers in English. Prompts for both languages are covered by golden tests.
- **Any provider.** Anthropic, OpenAI, Google Gemini, Ollama, OpenRouter, Groq, LM Studio, or any OpenAI-compatible endpoint. Any model id.
- **More than commits.** PR descriptions, Keep a Changelog release notes, line-referenced reviews, and a `prepare-commit-msg` hook for plain `git commit`.

## Contents

[Install](#install) · [60-second setup](#60-second-setup) · [Commands](#commands) · [What gets sent to the AI](#what-gets-sent-to-the-ai) · [Large diffs](#large-diffs) · [Configuration](#configuration) · [Providers and models](#providers-and-models) · [FAQ](#faq) · [Comparison](#comparison-with-similar-tools) · [Contributing](#contributing)

## Install

**Go** (1.24+):

```sh
go install github.com/tonklasocute/git-quill/cmd/git-quill@latest
```

**Binaries:** download the archive for your OS (Linux, macOS, Windows; amd64 and arm64) from [Releases](https://github.com/tonklasocute/git-quill/releases), check it against `checksums.txt`, and put `git-quill` on your `PATH`.

**Homebrew:** a tap (`tonklasocute/homebrew-tap`) is planned; until then use `go install` or a release binary.

Check it works: `git quill --version`.

## 60-second setup

The quickest path is the interactive wizard; it picks a provider, model, language and stores your key in the OS keychain:

```sh
git quill config
```

Or set things up by hand:

<details open>
<summary><b>Ollama — free, everything stays on your machine (recommended for privacy)</b></summary>

```sh
# install from https://ollama.com/download, then:
ollama pull qwen3.5:4b
git quill config set provider ollama
git quill commit
```

Any model from `ollama list` works: `git quill config set model llama3.1:8b`. Point at another machine with `git quill config set base_url http://gpu-box:11434` (or `OLLAMA_HOST`).
</details>

<details>
<summary><b>Anthropic (Claude) — the default provider</b></summary>

```sh
export ANTHROPIC_API_KEY=sk-ant-...      # or: git quill config set-key anthropic
git quill commit
```

Defaults to `claude-haiku-5-5` (fast and inexpensive for commit messages). For harder reviews: `git quill config set model claude-sonnet-5-5`.
</details>

<details>
<summary><b>OpenAI</b></summary>

```sh
export OPENAI_API_KEY=sk-...             # or: git quill config set-key openai
git quill config set provider openai     # default model: gpt-6.1-sol
```
</details>

<details>
<summary><b>Google Gemini</b></summary>

```sh
export GEMINI_API_KEY=...                # GOOGLE_API_KEY also works
git quill config set provider gemini     # default model: gemini-3.8-flash
```
</details>

<details>
<summary><b>OpenRouter, Groq, LM Studio, or any OpenAI-compatible server</b></summary>

```sh
# OpenRouter
export OPENROUTER_API_KEY=...
git quill config set provider openrouter
git quill config set model <model-id>   # from openrouter.ai/models

# Groq
export GROQ_API_KEY=...
git quill config set provider groq
git quill config set model <model-id-from-console.groq.com>

# LM Studio (local): start the server in LM Studio, then
git quill config set provider lmstudio
git quill config set model <loaded-model-id>

# Anything else that speaks /v1/chat/completions (vLLM, llama.cpp server, LiteLLM, ...)
git quill config set provider compatible
git quill config set base_url http://localhost:8000/v1
git quill config set model my-model
export OPENAI_COMPATIBLE_API_KEY=...      # only if the server needs one
```
</details>

## Commands

Run any command with `-v/--verbose` to see token usage, retries and diff budgeting.

### `git quill commit`

Reads the **staged** diff and suggests a [Conventional Commit](https://www.conventionalcommits.org) (`type(scope): subject` + body, subject ≤ 72 characters, scope inferred from paths). In a terminal you get an interactive picker: **[a]ccept**, **[e]dit** in your git editor, **[r]egenerate**, **[c]hoose** between candidates, **[q]uit**. Then it runs `git commit`.

| Flag | |
|---|---|
| `-n, --candidates N` | generate N alternatives (1–5) to choose from |
| `-y, --yes` | commit with the first suggestion, no questions (CI, scripts) |
| `--dry-run` | print the message(s) instead of committing |
| `--show-prompt` | print the exact request; with `--dry-run`, **nothing is sent** |
| `--lang th\|en` | output language |
| `--type feat` | force the commit type |
| `--context "..."` | a hint for the model ("fixes the login timeout") |
| `--no-body` | subject line only |
| `--emoji` | gitmoji prefix (✨ feat, 🐛 fix, …), off by default |
| `-- <args>` | passed to `git commit`, e.g. `-- --no-verify --signoff` |

```sh
git quill commit -n 3 --lang th
git quill commit --yes --type fix --context "closes #42"
```

<img src="docs/gifs/thai.gif" alt="Thai commit message with gitmoji" width="760">

Nothing staged? You get told how to stage. Not a terminal? Use `--yes` or `--dry-run`.

### `git quill pr`

Writes a PR title and Markdown body (Summary, Changes, Testing, Breaking changes) from the commits and diff between the base branch and `HEAD`. The base is detected (origin's HEAD, `main`, `master`) or set with `--base`.

```sh
git quill pr                   # print + copy to clipboard
git quill pr --create --draft  # open it with the GitHub CLI (asks first)
```

Without [`gh`](https://cli.github.com), `--create` prints the PR and copies it to the clipboard.

<img src="docs/gifs/pr.gif" alt="git quill pr output" width="760">

### `git quill changelog`

Release notes in [Keep a Changelog](https://keepachangelog.com) format from the commits since the last tag (or `--from/--to`), grouped into Added / Changed / Deprecated / Removed / Fixed / Security.

```sh
git quill changelog                                     # [Unreleased] to stdout
git quill changelog --version v1.4.0 --write CHANGELOG.md
git quill changelog --from v1.2.0 --to v1.3.0 --version v1.3.0
```

`--write` prepends the section (creating the file with the standard header if needed), keeps an existing `[Unreleased]` section on top, refreshes `[Unreleased]` when you regenerate it, and refuses to duplicate a version.

### `git quill review`

A quick AI pass over the staged diff (or the whole branch with `--branch`): likely bugs, security issues, missing tests. Findings are grouped by file with line numbers and severity, and clearly labelled as AI suggestions.

```sh
git quill review
git quill review --branch --base main
git quill review --strict      # exit 1 on any high/medium finding (pre-push hook, CI)
```

<img src="docs/gifs/review.gif" alt="git quill review output" width="760">

Exit code is always 0 unless `--strict` is set.

### `git quill hook install | uninstall`

Installs a `prepare-commit-msg` hook so a plain `git commit` opens your editor with a suggestion already filled in. It skips merges, squashes, amends and messages given with `-m`/`-F`, honours `core.hooksPath`, and **never blocks a commit**: on any error you just get git's usual empty message. An existing foreign hook is left alone unless you pass `--force` (it is backed up and restored on uninstall). Skip once with `GIT_QUILL_SKIP_HOOK=1 git commit`.

### `git quill config`

```sh
git quill config                         # interactive setup
git quill config list                    # effective settings, files, key status
git quill config get model
git quill config set model gpt-6-luna    # provider-specific keys apply to the current provider
git quill config set lang th --repo      # write .git-quill.toml in this repo
git quill config set-key openai          # store a key in the OS keychain (input hidden)
git quill config delete-key openai
git quill config path
```

## What gets sent to the AI

<img src="docs/gifs/redaction.gif" alt="Secrets in .env.production are redacted before the prompt is built" width="760">

**Sent**, for each request:

1. Fixed instructions (the system prompt; see [`internal/prompt`](internal/prompt) and its [golden files](internal/prompt/testdata)).
2. Your branch name, and your `--context` hint (redacted).
3. The diff: staged changes for `commit`/`review`, `base...HEAD` for `pr`. Only changed lines plus up to 3 lines of context, **after redaction**.
4. The *names* of files left out (e.g. `go.sum (ignore pattern)`), never their content.
5. For `pr` and `changelog`: commit subjects and bodies (redacted).
6. For very large diffs: per-file summaries produced by the same model.

**Never sent:** content of excluded files, files you did not change, your git config, API keys for other providers, or anything outside the repository.

**Always excluded** (re-include with `!pattern` in `.git-quill-ignore`): lockfiles (`package-lock.json`, `yarn.lock`, `pnpm-lock.yaml`, `go.sum`, `Cargo.lock`, `poetry.lock`, `*.lock`, …), `vendor/`, `node_modules/`, minified and source-map files, generated code (`*.pb.go`, `Code generated … DO NOT EDIT`, `@generated`), images, media, fonts, archives, binaries and databases.

**Redacted** before the prompt is assembled, replaced with `[REDACTED:<type>]`:

| Detector | Examples |
|---|---|
| Cloud and SaaS tokens | AWS access key IDs and secret keys, GitHub (`ghp_`, `gho_`, `ghs_`, `github_pat_`), GitLab (`glpat-`, …), Slack tokens and webhooks, Anthropic, OpenAI, Google API keys, Stripe, npm |
| Private keys | any `-----BEGIN … PRIVATE KEY-----` block, including multi-line and escaped-in-JSON keys |
| Tokens in transit | JWTs, `Authorization: Bearer …` / `Basic …`, passwords in URLs (`postgres://user:pass@host`) |
| Assignments | `password=`, `secret:`, `api_key =`, `token: "…"` in code, JSON, YAML, TOML, INI, `.env` (with guards for `os.Getenv(...)`, `${VAR}`, placeholders, identifiers) |
| High entropy | random-looking values in `.env*` files |

You see a warning listing each type and file. Redaction is heuristic: it lowers the risk, it is not a guarantee, and it is not a substitute for a secret scanner such as [gitleaks](https://github.com/gitleaks/gitleaks) in a pre-commit hook.

**Check it yourself:** `git quill commit --dry-run --show-prompt` prints the exact request and sends nothing. The same flags work for `pr`, `changelog` and `review`.

**Where it goes:** to the provider you configured, under that provider's data policy. For zero data leaving your machine, use **Ollama** or **LM Studio**.

## Large diffs

Each provider has a token budget (`max_input_tokens`; defaults in the table below). When a diff does not fit, git-quill tries, in order:

1. **Leave out excluded files** (always done; listed in the prompt by name only).
2. **Reduce context lines** from 3 to 1 to 0.
3. **Map-reduce:** split the diff into budget-sized chunks, summarize each file (up to 4 requests in parallel for cloud providers, one at a time for local servers so queued requests do not time out), then write the commit message or PR from the summaries. `review` instead reviews each chunk and merges the findings.

It prints which strategy was used, e.g. `ℹ large diff: reduced diff context to 0 line(s); summarized 48 file(s) in 5 chunk(s) (map-reduce)`. Token counts are local estimates (about 4 characters per token for code, more conservative for Thai) and are only used for this decision; `--verbose` shows the usage the provider reported.

## Configuration

Settings are merged from, lowest to highest precedence:

1. built-in defaults
2. global file: `~/.config/git-quill/config.toml` (`$XDG_CONFIG_HOME` and `$GIT_QUILL_CONFIG` are honoured)
3. repository file: `.git-quill.toml` at the repo root
4. environment: `GIT_QUILL_PROVIDER`, `GIT_QUILL_MODEL`, `GIT_QUILL_LANG`
5. command-line flags

> **Repo safety rule:** `.git-quill.toml` may not set `provider` or any `base_url`. A repository you clone must not be able to send your code (and your API key) somewhere else. Such keys are ignored with a warning.

```toml
# ~/.config/git-quill/config.toml
provider = "anthropic"          # anthropic | openai | gemini | ollama | openrouter | groq | lmstudio | compatible
lang = "en"                     # en | th
style = "conventional"          # conventional | simple
max_subject_length = 72
emoji = false
prompt = "Mention the Jira key from the branch name if there is one."   # extra instructions
ignore = ["*.snap", "testdata/**"]                                       # extra exclude patterns
timeout = "2m"                  # default: 2m for cloud providers, 5m for local ones
max_retries = 3                 # retries on 429/5xx with exponential backoff (honours Retry-After)

[providers.anthropic]
model = "claude-haiku-5-5"
effort = "low"                  # output_config.effort; "" to omit (older models)
max_input_tokens = 60000

[providers.ollama]
model = "qwen3.5:4b"
base_url = "http://localhost:11434"
num_ctx = 8192                  # Ollama context window (default: budget + 2048)
think = "false"                 # "false" (default), "true", or "default" to leave it to the model
max_input_tokens = 6000
```

| Key | Default | |
|---|---|---|
| `provider` | `anthropic` | which backend to use (global only) |
| `model` | per provider | any model id the provider accepts |
| `lang` | `en` | `en` or `th` |
| `style` | `conventional` | `conventional` or `simple` (plain imperative subject) |
| `max_subject_length` | `72` | 20–200; longer subjects are shortened at a word boundary |
| `emoji` | `false` | gitmoji prefix |
| `prompt` | | extra instructions appended to every system prompt |
| `ignore` | | extra gitignore-style exclude patterns |
| `timeout` | auto | per request; auto = 2m for cloud providers, 5m for Ollama/LM Studio |
| `max_retries` | `3` | 0–10 |
| `base_url` | per provider | (global only) |
| `max_input_tokens` | per provider | token budget for diff content |
| `effort` | `low` | Anthropic only |
| `num_ctx`, `think` | | Ollama only |

**`.git-quill-ignore`** at the repo root takes gitignore syntax (`*`, `**`, `dir/`, `/anchored`, `!negate`).

**API keys** come from environment variables (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`/`GOOGLE_API_KEY`, `OPENROUTER_API_KEY`, `GROQ_API_KEY`, `OPENAI_COMPATIBLE_API_KEY`) or the OS keychain (`git quill config set-key <provider>`: macOS Keychain, Windows Credential Manager, Secret Service on Linux). They are never read from or written to config files. For Anthropic, an `ANTHROPIC_AUTH_TOKEN` or an `ant auth login` profile also works.

`NO_COLOR` and non-terminal output are honoured: plain text, no TUI.

## Providers and models

Defaults were checked against each provider's model documentation on **2026-10-19**. Every one can be changed with `git quill config set model <id>`.

| Provider | Default model | Cheaper / faster | Budget | Source |
|---|---|---|---|---|
| Anthropic | `claude-haiku-5-5` | — (larger: `claude-sonnet-5-5`, `claude-opus-5-5`) | 60k | [models overview](https://platform.claude.com/docs/en/about-claude/models/overview) |
| OpenAI | `gpt-6.1-sol` | `gpt-6-luna` (flagship: `gpt-6-astra`) | 60k | [models](https://developers.openai.com/api/docs/models) |
| Gemini | `gemini-3.8-flash` | `gemini-3.5-flash-lite` | 60k | [models](https://ai.google.dev/gemini-api/docs/models) |
| Ollama | `qwen3.5:4b` | `gemma4:e4b` | 6k | [library](https://ollama.com/library) |
| OpenRouter / Groq / LM Studio / compatible | *(you choose)* | | 30k / 20k / 6k / 16k | provider docs |

Notes: current Claude, GPT and Gemini models think before answering and count those tokens toward the output limit, so git-quill asks for a generous `max_tokens` (you are billed only for what is generated). For Claude models that support it (Opus 5.5, Sonnet 5.5, Fable 5.1, Opus 5), Anthropic's server-side refusal fallback is enabled. Errors are mapped to fixes: invalid key → how to set it; unknown model → how to change it; Ollama not running → `ollama serve`; model not pulled → `ollama pull <model>`.

**Tested live** (2026-10-19): Ollama 0.40.6 with `qwen3.5:4b`, CPU only (Docker on an Apple Silicon Mac). Every command worked end to end, including setup, the hook, Thai output, map-reduce and the "model not pulled" / "Ollama not running" errors. Typical timings: commit 10–25 s, Thai with `-n 3` ~20 s, review ~45 s, pr ~50 s, a map-reduce commit over 3 large files ~1 m 50 s. A GPU is much faster. The cloud providers (Anthropic, OpenAI, Gemini, OpenRouter, Groq, LM Studio) are covered by tests against mock servers that reproduce each API's streaming format and errors, but have not yet been exercised against the real services.

## FAQ

**Why "git-quill" and not "git-ai"?** The `git-ai` name is already used by [an active git extension](https://github.com/git-ai-project/git-ai) that installs a `git-ai` binary; two tools fighting over one name on your `PATH` helps nobody.

**Which provider should I start with?** Want it free and private: Ollama. Want the best messages with little setup: Anthropic or OpenAI with the default model. Use `--verbose` to see what each commit costs in tokens.

**Can I use it in CI or scripts?** Yes: `git quill commit --yes`, `--dry-run` to only print, `git quill review --strict` to fail a job on high or medium findings. Output is plain text when not attached to a terminal.

**Does it work on Windows?** Yes, including the hook (Git for Windows runs it with its bundled `sh`) and Windows Credential Manager for keys.

**macOS says the downloaded binary "cannot be opened".** Release binaries are not code-signed, so Gatekeeper quarantines files downloaded with a browser. Clear it with `xattr -d com.apple.quarantine ./git-quill`, or install with `go install` instead.

**How do I skip the hook once?** `GIT_QUILL_SKIP_HOOK=1 git commit`, or just use `-m`.

**How good is the Thai?** It depends on the model. Larger models write natural Thai; small local models are usable but plainer. Types, scopes, file names and identifiers stay in English so tools that parse Conventional Commits keep working.

**How well do small local models do?** With `qwen3.5:4b` commit messages were accurate and Thai read naturally. Expect a few quirks: they often pick `feat` for what is really a fix (add `--type fix`), may return fewer alternatives than `-n` asks for, rate review findings more severely than they deserve, and now and then stop a PR description early (git-quill warns when sections are missing). Larger models do not have these problems.

**Will my code be used for training?** That is up to the provider and your account settings. If that matters, run locally with Ollama or LM Studio.

**Can I see what demo GIFs would look like without a key?** `GIT_QUILL_PROVIDER=fake` is a deterministic offline provider used for the demos and tests. The GIFs in this README are rendered with it from [`docs/tapes`](docs/tapes) (`make tapes`).

## Comparison with similar tools

Fair-minded, based on each project's README on 2026-10-19 (— means not mentioned there; corrections welcome).

| | **git-quill** | [aicommits](https://github.com/Nutlope/aicommits) | [OpenCommit](https://github.com/di-sukharev/opencommit) | [CodeGPT](https://github.com/appleboy/CodeGPT) |
|---|---|---|---|---|
| Install | static Go binary | npm (Node.js) | npm (Node.js) | Go binary |
| Local models | Ollama, LM Studio | Ollama, LM Studio | Ollama, llama.cpp | Ollama |
| Providers | Anthropic, OpenAI, Gemini, OpenRouter, Groq, any OpenAI-compatible | OpenAI, Groq, xAI, OpenRouter, Together AI, … | OpenAI, Anthropic, Gemini, Azure, DeepSeek, OpenRouter, … | OpenAI, Azure, Gemini, Anthropic, Groq, OpenRouter |
| Redacts secrets inside the diff | ✅ with a report | — | — (file-level ignore) | — (file exclude list) |
| See the exact prompt, send nothing | ✅ `--dry-run --show-prompt` | — | — | — |
| Commit messages / hook | ✅ / ✅ | ✅ / ✅ | ✅ / ✅ | ✅ / ✅ |
| PR descriptions | ✅ (+ `gh pr create`) | — | — | — |
| Changelog | ✅ Keep a Changelog | — | — | — |
| Code review | ✅ line refs, severity, `--strict` | — | — | ✅ summary |
| Large-diff strategy | exclude → less context → map-reduce, reported | — | — | per-file summaries |
| Keys in OS keychain | ✅ | — | — | ✅ |
| Languages | English, Thai (tested prompts) | any locale | many | en, zh-tw, zh-cn |
| GitHub Action | — | — | ✅ | — |

[lumen](https://github.com/jnsahaj/lumen) is also worth a look: a fast Rust diff viewer and review TUI with optional AI helpers.

## Dogfooding

This repository uses its own tool. The docs and changelog commits were written with `git quill commit` and [`CHANGELOG.md`](CHANGELOG.md) with `git quill changelog --write` (then trimmed by hand: fixes to never-released code were dropped) using the offline `fake` provider, because no API key was configured. The later fix commits were written by a real local model (Ollama, `qwen3.5:4b`) through `git quill commit --yes`, sometimes with `--type fix`; where the model got a detail wrong, the message was corrected by hand, exactly what the `[e]dit` key is for. Those live runs also found and fixed five bugs (setup wizard base URL, PR headings, changelog bullets, local timeouts and parallelism, subject truncation).

## Contributing

Issues and PRs are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for the layout, how to run the tests (`make race`, `make cover`), how to update golden prompt files, and how to add a provider or a redaction rule.

## License

[MIT](LICENSE) © 2026 Khomkrit Daengnuan
