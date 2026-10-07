# Contributing to Professor

Professor's files ship straight into other people's agent pipelines, so a few rules here are stricter than usual. By taking part you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).

## Where work lands

- Branch from `develop` and open the pull request against `develop`. `main` is release-only: it moves when a release pull request merges, and the repository ruleset refuses direct pushes to it.
- One change per pull request. The commit subject follows the history: `type(scope): what changed` — `fix(guard): …`, `feat(templates): …`, `docs(readme): …`.
- Write the commit message for the adopter who will update onto it: release notes are derived from `develop`'s diff and its commit messages, never written in a pull request.

## Build and test

Every build, test and gate runs through one script:

```bash
.claude/scripts/dev.sh status            # toolchains and projects
.claude/scripts/dev.sh iso verify templates  # leak gate + placeholder registry
.claude/scripts/dev.sh iso test pfm      # the Go fleet engine's suite
```

- `pfm/` is the Go fleet engine (version in `pfm/go.mod`); how it is tested is written down in [`.claude/commands/pfm-testing-manual.md`](.claude/commands/pfm-testing-manual.md).
- A bug fix carries a regression test you watched fail before the fix.
- Arm the pre-push leak gate once per clone: `git config core.hooksPath .githooks`.

## Rules specific to this repository

- `templates/**` is shipped source — an adopter's live agent prompts, one clone away. Treat every prompt line as production code.
- Nothing identifying ships: no private project or client name, no personal data, no machine-absolute path in any tracked file. `scripts/leak-check.sh` is the backstop, not the plan.
- Template example values are invented placeholders, registered in [`docs/PLACEHOLDERS.md`](docs/PLACEHOLDERS.md).
- `AGENTS.md`, `.codex/**` and `.opencode/**` are generated from the Claude sources (`pfm codex build`, `pfm opencode build`); edit the source, never the mirror.
- A `.claude/**` change any adopter could use lands in its `templates/project/**` twin in the same pull request.

## Reporting

- Bug or idea: [open an issue](https://github.com/rezzminator/professor/issues/new/choose).
- Question: [Discussions](https://github.com/rezzminator/professor/discussions).
- Vulnerability: [SECURITY.md](SECURITY.md) — privately, never in a public issue.
