# Contributing to git-quill

Thanks for helping! Bug reports, provider fixes, new redaction rules and better prompts are all welcome.

## Getting started

```sh
git clone https://github.com/tonklasocute/git-quill && cd git-quill
make build        # ./git-quill
make race         # go test -race ./...
make cover        # race + coverage across internal/..., prints the total
make lint         # golangci-lint (v2)
```

Requirements: Go 1.24+, git. Optional: [golangci-lint](https://golangci-lint.run) v2, [VHS](https://github.com/charmbracelet/vhs) for the demo GIFs, [GoReleaser](https://goreleaser.com) v2.

You never need an API key to develop: `GIT_QUILL_PROVIDER=fake` is a deterministic offline provider (`internal/provider/fake`) used by the tests and the demos. It also accepts `GIT_QUILL_FAKE_RESPONSE`, `GIT_QUILL_FAKE_ERROR` (`auth`, `not_found`, `rate_limit`, `server`, `unavailable`) and `GIT_QUILL_FAKE_DELAY`.

## Layout

```
cmd/git-quill          main package (thin)
internal/cli           cobra commands: commit, pr, changelog, review, hook, config
internal/app           the shared pipeline: diff → exclude → redact → budget → prompt → provider
internal/git           shells out to git (+ gittest helpers for temp repos)
internal/diff          unified-diff parser, gitignore-style matcher, token estimates, chunking
internal/redact        secret detection and redaction
internal/prompt        every prompt sent to a model (golden-tested)
internal/provider      Provider interface, errors, httpx (retries, SSE), one package per backend
internal/config        TOML config layers, provider registry, OS keychain
internal/ui            NO_COLOR-aware printer, spinner, interactive commit picker
docs/tapes             VHS tapes (+ setup.sh) that render docs/gifs
```

## Tests

- **Providers** are tested against `httptest` servers that mimic each API, including streaming, retries and error codes. Do not call real APIs from tests.
- **Git** behaviour is tested end to end in temporary repositories (`internal/git/gittest`), isolated from your global git config.
- **Prompts** are golden files in `internal/prompt/testdata`. If you change a prompt on purpose, run `go test ./internal/prompt -update` and review the diff: it is exactly what users will send.
- **Redaction** tests are tables of real-looking *fake* secrets plus false-positive cases. Build fake secrets at runtime (`fake("ghp_", ...)`) so the test file itself never trips secret scanners, and mark unavoidable generic literals with `// gitleaks:allow`.
- Keep `go test -race ./...` green on Linux, macOS and Windows; CI runs all three.

## Adding a provider

1. Add an entry to `config.Providers` (default model, key env vars, budget, `Kind`).
2. If it speaks Chat Completions, reuse `internal/provider/openai`. Otherwise add `internal/provider/<name>` implementing `provider.Provider`, using `httpx` for retries and `provider.FromStatus`/`FromTransport` for actionable errors.
3. Wire it in `internal/app/providers.go` and add an httptest suite (stream, 401, 404, 429 retry, mid-stream error).
4. Document setup in both READMEs.

## Adding a redaction rule

Add a `patternRule` in `internal/redact/redact.go` (specific formats go before generic ones), then add positive cases and at least one false-positive case to `redact_test.go`.

## Demo GIFs

```sh
make tapes        # builds ./git-quill and renders docs/tapes/*.tape with the fake provider
```

The tapes source `docs/tapes/setup.sh`, which creates an isolated throwaway repo (own git config and git-quill config).

## Commits and pull requests

- Use [Conventional Commits](https://www.conventionalcommits.org) (`feat(scope): …`, `fix: …`). Dogfooding welcome: `git quill commit`.
- Keep PRs focused; include tests; update docs when behaviour changes.
- Run `make lint race` before pushing.

## Releasing (maintainers)

Releases are built by GoReleaser from a `v*` tag (`.github/workflows/release.yml`). Update `CHANGELOG.md` first (`git quill changelog --version vX.Y.Z --write CHANGELOG.md`), then tag and push the tag.
