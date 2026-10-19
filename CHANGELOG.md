# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **git:** Add git shell-out wrapper with temp-repo integration tests
- **diff:** Parse unified diffs, gitignore-style excludes and token budgeting helpers
- **redact:** Redact secrets in diffs and text before they leave the machine
- **provider:** Add provider interface, retrying HTTP client, fake and Ollama providers
- **config:** Layered TOML config with repo safety rules and keychain-backed API keys
- **prompt:** Add commit, PR, changelog, review and summary prompts with th/en golden tests
- **ui:** Add NO_COLOR-aware printer, spinner and interactive commit picker
- **commit:** Generate, review and commit messages for staged changes
- **provider:** Add Anthropic (official SDK), OpenAI/compatible and Gemini streaming clients
- Add pr, changelog and review commands
- **hook:** Install a prepare-commit-msg hook that pre-fills plain git commit
- **config:** Add interactive setup, get/set/list and OS keychain key management

