# release-check

`scripts/release-check.mjs` is the release's deterministic core: whatever a release decides that can be computed, it computes, so no agent is asked to count, match or remember. Three modes — `scope`, `notes`, `preflight` — one exit-code contract, and one tier table that nothing else restates. Family decisions live in [release.md](release.md).

## Contents

- [Exit codes and output](#exit-codes-and-output)
- [scope](#scope)
- [The tier table](#the-tier-table)
- [notes](#notes)
- [preflight](#preflight)
- [Where it runs](#where-it-runs)
- [Tests](#tests)

## Exit codes and output

| Exit | Means | Output |
| --- | --- | --- |
| 0 | the check passed | a last line `release-check {mode}: clean` |
| 1 | the check ran and failed | one `FAIL {rule}: {where} — {what}` line per failure |
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
- An area over 600 hunks splits by its next path segment, packing segments in path order into areas of at most 600 hunks, named `{area}-1`, `{area}-2`, … in that order; a single segment over 600 splits by file the same way, its parts numbered in the same sequence. An area name never holds a `/`. `AREA` lines carry the pathspecs the reviewer's diff uses; `/pfm:release` reviews the `pfm-update` areas alone.
- `REMOVED-ROLE` names each agent, command or skill that no root — `templates/global/`, `templates/project/`, `.claude/` — holds at head while one held it at base (a role moved between roots is not removed; an added role is one no root held at base): `agents/{x}.md` → `agent {x}`; `commands/{ns}/{x}.md` → `command /{ns}:{x}`; `commands/{x}.md` → `command /{x}`; `skills/{x}/…` → `skill {x}`.

## The tier table

Tier 1 is the adopter contract; its paths are the only ones a release reviews in full. The table lives in the script as data, and this is its one description.

| Area | Paths |
| --- | --- |
| `templates` | `templates/`, `pfm/harness-prompts/` |
| `pfm-update` | `pfm/internal/{update,updatecheck,installer,enginesync,globallink,codexappendix,claudelaunch,doctor,config,professor,codexprompt,picker,mcpserv,herdr,themesync,themetmux,agentlauncher}/`; files under `pfm/cmd/pfm/` whose name starts with `install`, `uninstall`, `update`, `init`, `doctor`, `config` or `mcp_serve` |
| `public` | `README.md`, `INSTALL.md`, `CHANGELOG.md`, `docs/SETUP.md`, `docs/RELEASE.md`, `docs/PLACEHOLDERS.md` |
| `gates` | `.github/`, `.githooks/`, `scripts/`, `infra/check-self-hosted-manifest.sh` |

Everything else is tier 2.

## notes

`release-check notes {file}` checks the grammar alone. Range, version and command rules join when their arguments are given: `--base`, `--head`, `--coverage` for coverage; `--version`, `--previous`, `--bump` for the version (`--version` and `--previous` each take `X.Y.Z` or `vX.Y.Z`; `--version` alone binds N1 only); `--pfm-help` for commands. The note file may sit outside the worktree (`/pfm:release` checks the draft in its release directory); the version and changelog stamps are read from the working directory. `notes --all {dir}` checks every `v*.md` in the directory at or above the grammar's first version, `0.78.0`, each against the version its file name carries, and prints `CHECKED {n}`.

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
| C1 | a non-merge commit of the range is missing from the ledger, appears twice, or a row names a commit outside the range; a commit that touches only the release files (`releases/`, `CHANGELOG.md`, `VERSION`, `.professor/VERSION`, `.professor/manifest.json`) is the release's own and needs no row |
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

## preflight

`release-check preflight --base {ref} --head {ref}`, run inside the worktree; `--base` must be an ancestor of `--head`. It judges the commit `--head` names, never the working tree, and prints one line per rule saying what it swept, so an empty sweep is provably a sweep:

```text
SEAMS {n} removed paths, {n} removed roles swept
README {n} commands checked
PLUGINS {n} checked, {m} unlistable
```

| Rule | Fails when |
| --- | --- |
| S1 | a file at `--head` names a path the range removed (`git grep -F`) or a role it removed (`git grep -F -w`, the `REMOVED-ROLE` names of `scope`), one `FAIL` per file line and name; a hyphen continues a role name (`flights-lander-sonnet` is not `flights-lander`), and a further segment or extension continues a path (`scripts/old.sh.bak` is not `scripts/old.sh`); the only exclusions are history and the retired-name registry: `releases/`, `CHANGELOG.md`, `.professor/RR/`, `pfm/internal/update/retired_names.go` and its test. A design doc's history section is no exclusion: it describes the removal on `develop` or drops the name |
| D1 | a code span in `README.md` opening with a command — `` `/{name}` `` or `` `/{ns}:{name}` `` — names nothing shipped: a command under `templates/global/commands/`, `templates/project/commands/` or `.claude/commands/`, a skill under `templates/global/skills/`, `templates/project/skills/` or `.claude/skills/`, an installer asset `pfm/internal/installer/assets/{name}.{command,skill}.md`, or one of the script's short list of Claude Code and plugin commands (`/reload-plugins`, `/buddy`, `/cache`, `/keepwarm`), which a new README command of that kind joins in the same change |
| T1 | a plugin `.claude-plugin/marketplace.json` lists has no `source.path` and `source.url`, no submodule gitlink at that path, or a gitlink no tag of `git ls-remote --tags {source.url}` points at; gpt-gateway is not listed, so it is never checked |

README agents are prose mentions, not a list: a removed agent the README still names is an S1 hit. A tag listing that fails — a network error, a repository gone — is an `ERROR`, never a `FAIL` and never clean; the other plugins are still checked, and the one `ERROR` line names every unlistable remote and what S1, D1 and the rest of T1 found; so is a `README.md` or marketplace absent at `--head`.

## Where it runs

- `/pfm:release`: `preflight` and `scope` at the cut; `notes` over the draft with coverage, `--version` and `--pfm-help`, then over `releases/v{NEW}.md` with every rule group once the version is stamped.
- `changelogger`: `notes` with the coverage arguments and `--version`, before it returns.
- `.claude/scripts/dev.sh verify templates`: `notes --all releases`.
- `.github/workflows/release.yml`: the release PR's `release` job runs `make release`, `notes releases/v{VERSION}.md --version v{VERSION}` beside the authored-notes gate; `make release-check` runs it locally.

## Tests

Node's built-in test runner over fixture repositories built in a temporary directory, wired into `dev.sh verify templates` beside the other gate-script self-tests. Each rule has a case that passes and a case that fails with its named line; each mode has an `ERROR` case for a missing input, so the exit-2 path is proven distinct from exit 1.
