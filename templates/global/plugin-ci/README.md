# templates/global/plugin-ci

The two workflows every plugin repository carries under its own `.github/workflows/`, per `.claude/rules/ci-workflows.md`: `ci.yml`, the one check on the pull request into `main`, and `publish.yml`, the release on its merge. A plugin copies both and fills the tokens; it never calls them as a reusable workflow. The source is a Go plugin that publishes binaries, the fullest shape.

## Tokens

- `{PLUGIN_NAME}`: the installable plugin's name — `plugins/{PLUGIN_NAME}/`, its binaries, its release title.
- `{CLAUDE_CODE_VERSION}`: the Claude Code `claude plugin validate` runs on.
- `{NODE_VERSION}`: the Node major.

## Fixed in every plugin

- `ci.yml`: its `pull_request` trigger into `main` (`opened, synchronize, reopened`), the per-pull-request `concurrency`, `check` as a `fail-fast: false` matrix over `ubuntu-latest` and `macos-15` running `make check`, `release` running `make release` once, and the `ci` job verbatim.
- `publish.yml`: its `pull_request` `closed` trigger into `main`, `publish` gated on the merge, checking out the merge commit, failing when the tag exists or its lookup fails, and creating the release `--target` that commit with notes from the version's `CHANGELOG.md` section.

## Set by the plugin's stack

- Toolchain steps and the `env:` pins: only what `make check` and `make release` need (a Node plugin takes `NODE_VERSION` with `cache: npm`, a Go plugin `go-version-file: go.mod`); a pin another file owns stays there and a step reads it, its per-runner values in a matrix `include:` keeping both runners.
- The `LEAK_TERMS` lines of the `make check` step: only for a leak scan reading the `LEAK_TERMS` secret; the scan runs last inside `make check`.
- `release` as a runner matrix: only where each leg proves its native release asset, `make release` running on one leg.
- Version and tag: read from the plugin's manifest, `package.json` or `VERSION`; the tag keeps the plugin's scheme, `v{version}` or `{PLUGIN_NAME}--v{version}`.
- A plugin publishing no files drops the asset build, the asset arguments to `gh release create` and the `verify` job.
