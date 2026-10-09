# AI Review

This repository uses Codex to review PR text patches.
The review covers correctness, security, regressions, and missing tests where the
patches provide enough evidence. It does not run code or replace project checks.

## Enable reviews

Add `OPENAI_API_KEY` as a repository Actions secret.
An organization secret also works when this repository has access.
Use GitHub's secret settings. Never commit the key or paste it into an issue.

Merge both AI review workflows into the default branch.
Then open or update a small PR. Check the `AI review` workflow and its comment.
Draft PRs wait until the author marks them ready for review.

Optional repository variables:

- `CODEX_REVIEW_MODEL`: a supported Codex model. Empty uses the action's default.
- `CODEX_REVIEW_VERSION`: a supported CLI release. Use version 0.138.0 or later.

The workflow reports a skipped review when the key is missing.
API use can incur charges. Use the provider's spending controls for the intended review budget.

## How review works

`AI review request` runs on PR events without secrets or a source checkout.
The trusted `AI review` workflow verifies the request through GitHub's API.
It fetches changed files as data and supplies bounded patches to Codex.
It never checks out contributor code or downloads contributor artifacts.

The Codex job uses read-only permissions and disabled command tools.
A separate publishing job has comment permission but no API key.
It validates the report and checks both commit SHAs before posting.
An old result does not replace a review for changed code.

Each review accepts at most 200 changed files and 120,000 bytes of patch data.
The comment reports omitted, missing, or partial patches.
Large changes, binary files, and missing context still need human review.
The workflow also reports failed or invalid model output.

## Review requirements

Treat AI findings as advice. Verify each finding before changing code.
Resolve confirmed defects or explain why a finding does not apply.
Run the normal project checks. Use strict STE for commit and PR text.
Keep release approval and required checks independent of this review.

Send sensitive findings through the repository's private reporting route.
Do not copy private data or credentials into public comments.

Implementation references:
[Codex action](https://github.com/openai/codex-action/blob/bdf19a4a223ec2549a3e2274a0cf61556bc07675/README.md)
and [GitHub workflow events](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#workflow_run).
