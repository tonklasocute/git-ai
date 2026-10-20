# Security policy

## Reporting a vulnerability

Please report security issues privately through
[GitHub private vulnerability reporting](https://github.com/tonklasocute/git-quill/security/advisories/new),
not in a public issue. This includes secrets that git-quill failed to redact
before sending a diff to a model.

Include the git-quill version (`git quill --version`), your OS, and a minimal
reproduction. Use fake secrets in examples, never real ones.

## Supported versions

Fixes go into the latest release.

## Scope notes

Secret redaction is a heuristic safety net, not a guarantee; see
"What gets sent to the AI" in the README. A secret pattern that is missed is a
valid report.
