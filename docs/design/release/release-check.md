# release-check

`scripts/release-check.mjs` is the release's deterministic core: whatever a release decides that can be computed, it computes, so no agent is asked to count, match or remember. Three modes — `scope`, `notes`, `ready` — one exit-code contract, and one tier table that nothing else restates. Family decisions live in [release.md](release.md).

## Contents

- [Exit codes and output](#exit-codes-and-output)
- [scope](#scope)
- [The tier table](#the-tier-table)
- [notes](#notes)
- [ready](#ready)
- [Where it runs](#where-it-runs)
- [Tests](#tests)

## Exit codes and output

| Exit | Means | Output |
| --- | --- | --- |
| 0 | the check passed | a last line `release-check {mode}: clean` |
| 1 | the check ran and failed | one `FAIL {rule}: {where} — {what}` line per failure; `ready` adds `RERUN {phase}` lines |
| 2 | the check could not run: bad usage, a missing input file, git failed | `ERROR {what}` — never a `FAIL`, never `clean` |

A missing input is an error, not an absence: a coverage file that does not exist is `ERROR`, while a coverage file that lacks a commit is `FAIL C1`. Every rule is named, so a failure line tells the fixer which rule and where.

## scope

`release-check scope --base {ref} --head {ref}`, run inside the worktree; `--base` must be an ancestor of `--head`, or it is an `ERROR`. It counts hunks with `git diff -U0` and prints:

```text
RANGE {base7}..{head7} COMMITS {n} FILES {n} HUNKS {n}
FILE {A|M|D|R} {hunks} {tier1|tier2} {area|-} {path}
AREA {area} {hunks} {files} {pathspec…}
TIER2 {hunks} {files}
REMOVED {path}
REMOVED-ROLE {agent|command|skill} {name}
```

- `COMMITS` counts non-merge commits. A rename is counted under its new path, and its old path is `REMOVED`; a rename out of a tier-1 path keeps the old path's tier and area, so moving a file out of the contract is still reviewed.
- `releases/**` is outside every area: the note is judged by `notes`.
- An area over 600 hunks splits by its next path segment, packing segments in path order into areas of at most 600 hunks, named `{area}-1`, `{area}-2`, … in that order; a single segment over 600 splits by file the same way, its parts numbered in the same sequence. An area name never holds a `/`, so `review/{area}.md` is one flat file. `AREA` lines carry the pathspecs the reviewer's diff uses.
- `REMOVED-ROLE` names each agent, command or skill that no root — `templates/global/`, `templates/project/`, `.claude/` — holds at head while one held it at base (a role moved between roots is not removed; an added role is one no root held at base): `agents/{x}.md` → `agent {x}`; `commands/{ns}/{x}.md` → `command /{ns}:{x}`; `commands/{x}.md` → `command /{x}`; `skills/{x}/…` → `skill {x}`.

## The tier table

Tier 1 is the adopter contract; its paths are the only ones a release reviews in full. The table lives in the script as data, and this is its one description.

| Area | Paths |
| --- | --- |
| `templates` | `templates/`, `workflows/`, `pfm/harness-prompts/` |
| `pfm-update` | `pfm/internal/{update,updatecheck,installer,doctor,config,professor,codexgen,opencodegen,picker,mcpserv,harvestmcp}/`; files under `pfm/cmd/pfm/` whose name starts with `install`, `uninstall`, `update`, `init`, `doctor`, `config` or `mcp_serve` |
| `public` | `README.md`, `INSTALL.md`, `CHANGELOG.md`, `docs/SETUP.md`, `docs/RELEASE.md`, `docs/BLUEPRINT.md`, `docs/PLACEHOLDERS.md` |
| `gates` | `.github/`, `.githooks/`, `scripts/`, `infra/fence/release-rehearsal.sh`, `infra/check-self-hosted-manifest.sh` |

Everything else is tier 2.

## notes

`release-check notes {file}` checks the grammar alone. Range, version and command rules join when their arguments are given: `--base`, `--head`, `--coverage` for coverage; `--version`, `--previous`, `--bump` for the version (`--version` and `--previous` each take `X.Y.Z` or `vX.Y.Z`; `--version` alone binds N1 only); `--pfm-help` for commands. `--record {path}` writes `NOTES PASS {head sha} {groups}` or `NOTES FAIL {head sha} {groups}` as the file's first line — `{groups}` the rule groups that ran, from `range version commands` — followed by the output; it judges the note and stamps as committed at HEAD, so a note or stamp that differs from HEAD is an `ERROR`, and an error before or during the run records `NOTES ERROR`. `notes --all {dir}` checks every `v*.md` in the directory at or above the grammar's first version, `0.78.0`, each against the version its file name carries, and prints `CHECKED {n}`.

Grammar (`docs/RELEASE.md` § Release notes is the public statement of the same rules):

| Rule | Fails when |
| --- | --- |
| N1 | line 1 is not `# v{X.Y.Z} — {YYYY-MM-DD}`, or its version differs from `--version` |
| N2 | no prose line stands between the title and the first `##` or `#### → Stop:` |
| N3 | a `#### → Stop: {reason}` line sits anywhere but between the lead and the first `##`, or has no reason |
| N4 | a `##` heading is outside `Breaking`, `Migration`, `Added`, `Changed`, `Fixed`, `Removed`, `Verification`; repeats; breaks that order; or heads no content |
| N5 | a non-blank line under a category heading is neither a bullet `- {Label}: {scope} — {change}` with `Label` in `Global`, `Project`, `pfm`, `Repo` and non-empty scope and change, nor an action line after a bullet |
| N6 | an action line does not read `#### → For: {audience} · {before update\|after update\|per project} · {surface} — {action}` with every part non-empty |
| N7 | `## Verification` holds a bullet or an action line |
| N8 | any other heading level or `####` line appears, a line opens with `#` but lacks the space after its hashes, or a code fence never closes |

Range, with `--base`, `--head`, `--coverage`:

| Rule | Fails when |
| --- | --- |
| C1 | a non-merge commit of the range is missing from the ledger, appears twice, or a row names a commit outside the range; a commit that touches only the release files (§ ready) is the release's own and needs no row |
| C2 | a row's `{section}` and `{Label}: {scope}` match no bullet in that section |
| C3 | a `none` row has no reason |
| C4 | a changed `templates/project/{rest}` path's `{rest}` appears, as a whole path, in no bullet or action line |
| C5 | an added or `REMOVED-ROLE` agent, command or skill name appears nowhere in the note |

Version, with `--version`, `--previous`, `--bump`:

| Rule | Fails when |
| --- | --- |
| V1 | `VERSION`, `.professor/VERSION` or `.professor/manifest.json` `installed_from.version` differs from `--version` |
| V2 | the first entry under `CHANGELOG.md` `## Releases` is not `- [v{V}](releases/v{V}.md) — {summary}` with a summary |
| V3 | `--version` is not `--previous` bumped by `--bump`, or `--bump` is below the floor of the note's sections ([release.md § Versioning](release.md#versioning)) |

Commands, with `--pfm-help`: P1 fails when an action line names a backticked `pfm {word}` that the help file's command list lacks. The help file is `pfm help`'s output; a command line is two spaces, the name, then whitespace.

## ready

`release-check ready {dir} --worktree {path} [--stamp]`. HEAD is the worktree's; the release files are `releases/`, `CHANGELOG.md`, `VERSION`, `.professor/VERSION`, `.professor/manifest.json`.

| Rule | Fails when | Rerun |
| --- | --- | --- |
| R1 | `scope.md` is absent; an `AREA` of `scope.md` has no `review/{area}.md`; a report is empty; a finding (`F{n}`, `S{n}`, `P{n}`) carries no `status:` line; or any `status:` value, markup stripped and case ignored, is neither `resolved @{sha}` nor `waived` | REVIEW |
| R2 | a tier-1 path outside the release files changed between `review/HEAD` and HEAD | REVIEW |
| R3 | a lane of `templates`, `pfm`, `e2e` lacks a `gate/candidate-{lane}.log` ending `EXIT 0 @{sha}`, where a red candidate lane counts only with a `gate/stable-{lane}.log` also red; and `{sha}` is HEAD or the diff since touches only the release files | GATE |
| R4 | `rehearsal.md` line one is not `REHEARSAL CLEAN {sha} round {n}`, either machine's `rehearsal/{machine}-{n}/result.json` verdict is not `CLEAN`, or `{sha}` is not HEAD and the diff since touches anything but the note's `## Verification` section; a recorded user ruling `REHEARSAL RULED {sha} round {n} — {ruling}` stands in for the `CLEAN` verdicts only with non-empty `{ruling}` text and a note `## Verification` line naming `REHEARSAL RULED` and round `{n}`, the `{sha}` rule unchanged | REHEARSE |
| R5 | `notes/check.txt` line one is not `NOTES PASS {HEAD} range version commands`; the worktree holds an uncommitted change to a tracked file; or HEAD's `VERSION` is not `X.Y.Z` with `releases/v{VERSION}.md` present at HEAD | NOTES |

With `--stamp` and every rule passing, it writes `READY` as `{sha} v{NEW} {YYYY-MM-DD}`. Without `--stamp` it also fails R6 when `READY` is absent or names a commit other than HEAD — the form `gitter` Phase RELEASE runs before it opens the PR.

## Where it runs

- `releaser`: `scope` at REVIEW, `notes` at NOTES and READY, `ready --stamp` at READY.
- `changelogger`: `notes` with the coverage arguments, before it returns.
- `gitter` Phase RELEASE: `ready` without `--stamp`, as its precondition.
- `.claude/scripts/dev.sh verify templates`: `notes --all releases`.
- `.github/workflows/release.yml`: `notes releases/{tag}.md --version {tag}` beside the authored-notes gate.

## Tests

Node's built-in test runner over fixture repositories built in a temporary directory, wired into `dev.sh verify templates` beside the other gate-script self-tests. Each rule has a case that passes and a case that fails with its named line; each mode has an `ERROR` case for a missing input, so the exit-2 path is proven distinct from exit 1.
