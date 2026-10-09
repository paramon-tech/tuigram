# Contributing

Start with the [development guide](docs/DEVELOPMENT.md) for architecture, libraries, naming conventions, testing, and release packaging.

Use `make demo` to explore the app without an account. Before opening a PR, format your code, run the relevant tests, and run `make check` for changes that affect runtime behavior. Describe the user-visible change, how you validated it, and any remaining limits.

Please keep credentials, session files, QR codes, and private conversations out of issues, test fixtures, commits, and screenshots. See [SECURITY.md](SECURITY.md) for reporting sensitive issues.

## GitHub tasks and documentation

Use GitHub Issues for planned work. State the goal, scope, and acceptance
criteria. Link the task issue from its pull request. Keep durable guidance in
`README.md` or `docs/`, and submit documentation changes through pull requests.
Use the same validation and review process for changes from engineers and
coding agents. Agents must read [AGENTS.md](AGENTS.md).

Review AI findings before merge. Correct confirmed defects and explain findings
that do not apply. AI review supplements the existing checks and human review.

## Strict STE before publishing

Use strict Simplified Technical English before every commit and pull request
creation or update. Review changed prose, commit messages, and pull request
titles and descriptions. Use short sentences, active voice, clear actions,
and consistent terms. Preserve commands, identifiers, facts, and uncertainty.
These writing rules do not certify ASD-STE100 compliance.
