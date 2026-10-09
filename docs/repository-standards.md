# Application Repository Standards

## Work and documentation

Track work in GitHub Issues. Record scope, acceptance criteria, and a verification plan.
Link each implementation PR to its issue.
Keep setup instructions in `README.md` and design or operating guides in `docs/`.
Update documentation with the behavior it describes.
Use strict STE before every commit and PR creation or update.
Preserve identifiers, commands, quoted text, and uncertainty.

## Validation

Run the repository's formatter, static checks, tests, and relevant build checks.
Use existing test frameworks. Add regression tests for observable defects.
Check dependency integrity and known vulnerabilities with the project's supported tools.
Run package and platform checks when affected by a change.
Report skipped tests and unsupported platforms explicitly.

## Pipeline and review

Run normal CI and AI review for PRs, including external contributions.
Keep AI findings advisory. A missing key or incomplete diff is not a successful review.
Use pinned action revisions, minimal job permissions, timeouts, and checkout without saved credentials.
Keep fork code separate from secrets and write tokens.
Treat PR content as untrusted input to the AI reviewer.

Keep release credentials outside test and review jobs.
Preserve existing release and deployment approval rules.
Document compatibility changes, migrations, and rollback steps where applicable.

## Public contributors

Provide `CONTRIBUTING.md`, `SECURITY.md`, `AGENTS.md`, and `CLAUDE.md` in public repositories.
Keep agent instructions self-contained and consistent with documented commands.
Give contributors enough information to run checks without private services or workspace files.
Never require live credentials for ordinary contributor tests.

Review input handling, storage, network boundaries, and dependency changes for security risks.
Send sensitive findings through the repository's verified private reporting route.

Read [AI review setup](ai-review.md) for credentials, workflow activation, and review limits.
