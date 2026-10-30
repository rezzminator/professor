---
name: pfm:release
description: 'Ships develop''s tip as cut — `/pfm:release cut {patch|minor|major} "{summary}" [--dry]` gates, notes and publishes it; typing it is the publish request, `--dry` stops before any push. /pfm:release → changelogger, reviewer-pro, gitter. Returns the release URL or the STOP that ended it.'
argument-hint: 'cut {patch|minor|major} "{summary}" [--dry]'
---

# PFM Release

One pass releases `develop`'s tip as it stands at the cut. Nothing is fixed inside a release: every check is a STOP, its fix lands on `develop` through a normal flight, and the user cuts again. The note grammar is `docs/RELEASE.md` § Release notes; the design is `docs/design/release/`.

## Constants

- Public repo `rezzminator/professor` — this repo IS the upstream.
- Typing `/pfm:release cut` is the user's explicit publish request for this version (project contract § Publication); every gitter brief that pushes quotes it. `--dry` runs steps 1, 3, 4 and 5, closes with § Dry close and pushes nothing.
- `BUMP` and `SUMMARY` from the arguments, both required. `PREV` = the newest suffix-free `v*` tag, written `vX.Y.Z`; `NEW` = `PREV` bumped by `BUMP`, written `X.Y.Z` — every tag, branch and path spells it `v{NEW}`; `NEXT` = `NEW` with minor + 1 and patch 0.
- `C` = the full sha of `develop`'s HEAD after step 1: the commit the release gates and ships.
- `WT` = `{repo}/.worktrees/release/`, on branch `release/v{NEW}`.
- `DIR` = `$HOME/.local/state/pfm/releases/{project}/v{NEW}/`, `{project}` = this repo's directory name with the leading dot stripped. `DIR/run.md` gets one line per step, `{time} {step} {PASS|STOP|SKIP} @{C7} — {one line}`; each cut opens with `{time} CUT @{C7}`.
- A STOP records its line in `run.md`, reports the step, its output and its file under `DIR`, and ends the cut. Before step 6, `gitter` then removes `WT` and deletes the local branch `release/v{NEW}` (never pushed); `DIR` stays. At step 6 or later nothing is removed: report every ref that stands.

## cut

1. `gh auth status` names the repo owner. `gitter` Phase PULL in the live checkout: `git pull --ff-only origin develop`, then `git fetch --tags origin`; STOP on failure. `git merge-base --is-ancestor origin/main develop` fails: STOP — `main` holds a commit `develop` lacks. Set `C`, `PREV` (`git tag --list 'v*' --sort=-v:refname | grep -xE 'v[0-9]+\.[0-9]+\.[0-9]+' | head -1`) and `NEW`; `git rev-list origin/main..{C}` printing nothing is a STOP (nothing to release), and an existing tag `v{NEW}` is a STOP. Create `DIR`.
2. Unless `--dry`: when `git rev-parse origin/develop` differs from `C`, `gitter` Phase PUSH `origin develop`, quoting the cut as the user's publish request; relay the pre-push hook's output verbatim; a failure is a STOP.
3. `gitter` Phase SETUP: `WT` on a new branch `release/v{NEW}` at `C`, submodules initialized; an existing `WT` or branch is a STOP naming it.
4. Launch every check below in one message — the background commands and the agents together — and wait for all of them. Each one that fails is a STOP; record each result in `run.md`. Commands run in `WT`.
   1. Gate: `./.claude/scripts/test.sh --full > DIR/gate.log 2>&1; echo "EXIT $? @$(git rev-parse HEAD)" >> DIR/gate.log` in the background, over the clean tree at `C`. It passes when its last line is `EXIT 0 @{C}`.
   2. Leak: `scripts/leak-check.sh --range origin/main {C} > DIR/leak.txt 2>&1` exits 0.
   3. Preflight: `node scripts/release-check.mjs preflight --base origin/main --head {C} > DIR/preflight.txt 2>&1` exits 0. Exit 1 lists `FAIL` lines, exit 2 an `ERROR`: both STOP.
   4. CI: `gh run list --workflow ci.yml --commit {C} --json databaseId,status,conclusion` names a run; while none exists yet after step 2's push, ask again every 30 seconds for up to 5 minutes, then STOP. A run in progress: `gh run watch {id} --exit-status`. It passes on conclusion `success`. Under `--dry` with `C` absent from `origin`, record `SKIP ci — C unpushed` and name it a gap in the report.
   5. Notes: `./.claude/scripts/dev.sh iso run go -C pfm run ./cmd/pfm help > DIR/pfm-help.txt`, then one `Agent(subagent_type: "changelogger")` with `WT`, `BASE` = `origin/main`'s sha, `C`, `NEW`, `PREV`, `BUMP`, `SUMMARY` and `DIR`. It writes `DIR/v{NEW}.md`, `DIR/changelog.line` and `DIR/coverage.tsv`, never `WT`, whose tree the gate is reading. Then `node scripts/release-check.mjs notes DIR/v{NEW}.md --base origin/main --head {C} --coverage DIR/coverage.tsv --version {NEW} --pfm-help DIR/pfm-help.txt` exits 0; each `FAIL` goes back to the same changelogger by `SendMessage` with the failure lines, two rounds at most, then STOP.
   6. Review, only when `node scripts/release-check.mjs scope --base origin/main --head {C} > DIR/scope.txt` prints an `AREA pfm-update` line (or a split `AREA pfm-update…` line): one `Agent(subagent_type: "reviewer-pro")`, one round and no delta round, `MODE` pre-merge, `TREE` = `WT`, `origin/main..{C}` restricted to the pathspecs of every such `AREA` line, `REPORT_PATH` = `DIR/review.md`, its premise that an adopter reaches this release by running the PREVIOUS release's binary: `pfm update` from `PREV` drives the update. A CRITICAL or HIGH finding on the update, install or doctor path is a STOP; every other finding is listed in the note's `## Verification` as not fixed in this release. Without such a line, record `SKIP review — no pfm-update hunks`.
5. In `WT`, once step 4 passed whole:
   1. Copy `DIR/v{NEW}.md` to `releases/v{NEW}.md` and prepend `DIR/changelog.line` as the first entry under `CHANGELOG.md` `## Releases`.
   2. Write the note's `## Verification` section, last in the file, from facts only: the gate's `EXIT` line and its `STEPS` line, the ci run's URL and conclusion, the leak scan's and the preflight's result lines, the reviews dispatched (0 or 1) with their findings by severity and each one not fixed in this release, and the line `No pre-release update run: the release ships develop's tip as cut.`
   3. `VERSION` and `.professor/VERSION` become `NEW`, `.professor/manifest.json` `installed_from.version` becomes `NEW`; `bash infra/check-self-hosted-manifest.sh --write . templates pfm` passes.
   4. `node scripts/release-check.mjs notes releases/v{NEW}.md --base origin/main --head {C} --coverage DIR/coverage.tsv --version {NEW} --previous {PREV} --bump {BUMP} --pfm-help DIR/pfm-help.txt` exits 0, and `scripts/leak-check.sh --files releases/v{NEW}.md CHANGELOG.md` is clean. A `V3` floor above `BUMP` is a STOP naming the floor.
   5. `gitter` Phase COMMIT in `WT` naming exactly `releases/v{NEW}.md`, `CHANGELOG.md`, `VERSION`, `.professor/VERSION` and `.professor/manifest.json`: `release: v{NEW} — {SUMMARY}`.
   6. `git -C WT diff --name-only {C} HEAD` printing any path outside those five is a STOP: the published commit is the gated commit plus the release files alone.
6. Unless `--dry`: `gh api repos/rezzminator/professor/rules/branches/main` lists `pull_request`, `non_fast_forward`, `deletion` and `required_status_checks`, and each `context` that last rule lists is a job's `name:` in `.github/workflows/*.yml` (`grep -hxF "    name: {context}" .github/workflows/*.yml`); a missing rule or an unmatched context is a STOP naming it. Then `gitter` Phase RELEASE with `WT`, `release/v{NEW}`, `C`, `NEW`, `SUMMARY` and the cut quoted as the publish request: it fetches `origin/main`, runs `make release-check` in `WT`, re-checks the release commit's files against `C`, pushes `release/v{NEW}`, opens the PR into `main`, waits for green required checks, merges with a merge commit, and watches `release.yml`'s `publish` job tag `v{NEW}` and its `verify` job check the published assets. A failed step is a STOP naming it; a failure after the tag exists is reported as it is — the tag is public, and the fix is a patch release, never a moved tag.
7. Close, one `gitter` dispatch in the live checkout: merge `origin/main` into `develop` with the merge commit `merge: v{NEW} back into develop`; `VERSION`, `.professor/VERSION` and `.professor/manifest.json` `installed_from.version` become `{NEXT}-alpha` and `bash infra/check-self-hosted-manifest.sh --write . templates pfm` restamps the manifest, committed as `chore(release): open v{NEXT}-alpha on develop`; push `develop`; delete `release/v{NEW}` locally and on `origin`; remove `WT`. A failed close names what stayed.
8. Report the PR URL, the merge SHA, the tag URL, the `verify` job's conclusion and `DIR/run.md`, ending with: `Blueprint released: v{NEW}. URL: https://github.com/rezzminator/professor/releases/tag/v{NEW}`

## Dry close

After step 5 under `--dry`: `make release-check` in `WT` (against `origin/main` as step 1 fetched it), recorded in `run.md`; then `gitter` removes `WT` and deletes the unpushed local branch `release/v{NEW}`. Report `DIR/run.md`, the note at `DIR/v{NEW}.md`, every check's result and every `SKIP` as a named gap.

## Hard rules

**NEVER:** push secrets; commit project-specific identifiers (a private project's brand name, current and former, user PII, internal URLs, machine-absolute home paths such as `/home/…` and `/Users/…`); force-push; bypass the pre-push hook; publish a commit other than `C` plus the release commit; move or delete a published tag; stage anything from a scratch directory. **The repo is PUBLIC — every push is world-visible, and a leak cannot be unpublished.**
