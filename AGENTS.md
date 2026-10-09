# Repository Guidelines

These instructions apply to Codex, Claude Code, and other coding agents.

## Start with the project guides

Read [CONTRIBUTING.md](CONTRIBUTING.md), [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md),
and [SECURITY.md](SECURITY.md). Inspect the Git status before edits. Preserve
unrelated changes and existing security controls.

Use GitHub Issues for planned tasks. Record the goal, scope, and acceptance
criteria. Link the issue from the pull request. Keep durable project guidance
in repository Markdown files and review documentation changes through pull
requests. Do not commit private assistant histories.

## Architecture and style

`cmd/tuigram/` contains the CLI. `internal/core/` defines domain contracts.
`internal/telegram/` implements Telegram access. `internal/demo/` supplies the
account-free backend. `internal/tui/` contains the Bubble Tea interface.
Configuration, storage, and platform code have separate packages under
`internal/`.

Keep Telegram types inside the transport adapter. Keep blocking work outside
the UI update loop. Honor cancellation and reject stale results. Preserve
input bounds, private file permissions, and terminal text sanitization.

Use idiomatic Go and `gofmt`. Name test files `*_test.go`. Add regression tests
for behavior changes and trust boundaries. Preserve upstream notices in
`internal/tgcalls/`.

## Validation

- Run `make demo` to inspect the app without an account.
- Run `make check` for runtime changes. It checks formatting, vet, race tests,
  and CLI smoke tests.
- Run `go mod verify` and `go mod tidy -diff` for dependency changes.
- Run `make vuln` for the existing vulnerability check.
- Validate workflow changes with
  `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12`.
- Run `git diff --check` before publishing changes.

Report checks that failed or could not run. Automated tests do not establish
live Telegram interoperability or native BSD behavior. Use only authorized
test accounts for live checks. Never publish sessions, API credentials, QR
tokens, private conversations, or personal configuration.

## Commit and pull request text

Use strict Simplified Technical English before every commit and pull request
creation or update. Review changed prose, commit messages, and pull request
titles and descriptions. Use short sentences, active voice, clear actions,
and consistent terms. Preserve commands, identifiers, facts, and uncertainty.
These writing rules do not certify ASD-STE100 compliance.

Describe the behavior change and validation. Review AI findings and record
their resolution. Human review remains necessary. Do not commit, push, merge,
publish, or deploy without authorization in the task.
