---
name: pfm:release
description: 'Versions and publishes this repo — `/pfm:release prepare {patch|minor|major} "{summary}" [--from {live-root}]` reviews, writes the notes, gates and rehearses to READY without publishing; `/pfm:release publish v{X.Y.Z}` lands it, only on the user''s explicit in-turn ask. /pfm:release → releaser → changelogger. Returns READY or the release URL.'
argument-hint: 'prepare {patch|minor|major} "{summary}" [--from {live-project-root}] | publish v{X.Y.Z}'
---

# PFM Release

You hold the release's loop: one `releaser` phase at a time, every fix a phase returns, the ledger. The phases' protocols live in the `releaser` agent; the note grammar in `docs/RELEASE.md` § Release notes; the design in `docs/design/release/`.

## Constants

- Public repo `rezzminator/professor` — this repo IS the upstream.
- `DIR` = `$HOME/.local/state/pfm/releases/{project}/v{NEW}/`, `{project}` = this repo's directory name with the leading dot stripped. `DIR/run.md` is yours alone: one line per phase return and per fix commit, `{time} {phase} {token} @{sha7} — {one line}`.
- `CANDIDATE` = `.worktrees/release/develop`, branch `release/v{NEW}` from `develop`; `STABLE` = `.worktrees/release/main`, detached and byte-identical to `origin/main`. Every release edit, fix and commit lands in `CANDIDATE`; the live checkout holds other sessions' work and is never swept.
- `PREV` = the newest suffix-free `v*` tag, written `vX.Y.Z`; `NEW` = `PREV` bumped, written `X.Y.Z` — every tag, branch and path of this family spells it `v{NEW}`; `BUMP` and `SUMMARY` from the arguments.
- The live source project for the refresh pass is another repo, named only by `--from`.

## prepare

Runs without publish authority and never pushes, tags or opens a PR. Run again for the same version, it resumes from `DIR/run.md`.

1. `BUMP` and `SUMMARY` are required. `gitter` Phase PULL: `git pull --ff-only origin develop` and `git fetch --tags origin` in the live checkout; STOP on failure. `git merge-base --is-ancestor origin/main develop` — a commit on `main` that `develop` lacks means the last release never fast-forwarded `develop`: STOP and report it.
2. An existing `DIR/run.md`: verify both worktrees (`STABLE` at `origin/main`, clean; `CANDIDATE` on `release/v{NEW}`) and resume at the first phase not `DONE` at HEAD. Otherwise `gitter` Phase SETUP twice — `STABLE` as `git worktree add --detach … origin/main`, verified at `origin/main` and clean; `CANDIDATE` on `release/v{NEW}` from `develop`'s SHA — then create `DIR` and `run.md`.
3. Refresh pass, only with `--from`: `$CDOCS/pcm/$REFS/refresh.md` § The pass inside `CANDIDATE`, fed every template its scan reports CHANGED; STOP when it stops. When it changed files, `gitter` Phase COMMIT in `CANDIDATE` naming the refreshed `templates/**` paths and `templates/refresh-map.json`: `refresh: re-derive templates from the live source @{live sha7}` with a `Source: {live sha}` trailer, the source never named; record `{time} OPEN refresh @{sha7} — Source {live sha}` in `run.md`. Without `--from`, record `refresh skipped — no live source named` — a silent skip is not a skip.
4. The phases, one `Agent(subagent_type: "releaser")` at a time, in order: REVIEW, NOTES, GATE, REHEARSE, READY. Each brief carries `PHASE`, `MODE`, `DIR`, `CANDIDATE`, `STABLE`, `NEW`, `PREV`, `BUMP`, `SUMMARY` and, for `revise`, the items verbatim — nothing else; the agent reads the rest from `DIR`. React to each return (§ Reactions), record it in `run.md`, then dispatch the next.
5. On `DONE READY`: report to the user the releaser's READY report, `DIR/run.md`, and the next step — `/pfm:release publish v{NEW}`, on their word. Stop there.

## Reactions

Match the return's first-line token, never its prose.

| Return | Reaction |
| --- | --- |
| `DONE {phase}` | Record it; dispatch the next phase |
| `FIX {phase}` rows routed `code` or `docs` | Fix each in `CANDIDATE` by the work ladder (fleet prompt § Orchestration): code in the fence per `/pfm-testing-manual` with a regression test watched failing against the unfixed code first; markdown directly, prompt files through `/pcm`. `gitter` Phase COMMIT on `release/v{NEW}` per fix, `fix(release): {what}`. Then re-dispatch the phase: REVIEW in `delta`, GATE `first`, REHEARSE its next round |
| `FIX {phase}` rows routed `notes` | NOTES in `revise` with the rows verbatim, then re-dispatch the phase that raised them |
| `FIX READY` rows routed `rerun` | Re-run each named phase in phase order, then READY |
| `FAILED {phase}: {why}` | Read the cause; one re-dispatch with the cause in a changed brief; a second `FAILED` is a stop, reported with both causes |
| `BLOCKED {phase}: {question}` | Ask the user with the context inside the question; under full autonomy, rule it and record the ruling in `run.md` |

A return mixing routes: every `code` and `docs` row first, then one NOTES `revise` with the `notes` rows, then one re-dispatch of the phase that raised them.

A finding you decide not to fix in this release is waived in its report — `status: waived — {reason}` — and recorded in `run.md`. A row the releaser returns as `blocking` is never waived.

## publish

Runs only on the user's explicit request in the current turn to publish this version (project contract § Publication). A finished `prepare`, a READY stamp or "finish it" is not that request: without it, stop and say `publish not run — explicit user publish request required`.

1. `gh auth status` names the repo owner. `gh api repos/rezzminator/professor/rules/branches/main` lists `pull_request`, `non_fast_forward`, `deletion` and `required_status_checks`; a missing rule is a STOP — restore the `main-release-only` ruleset first.
2. `node scripts/release-check.mjs ready DIR --worktree CANDIDATE` exits 0: `READY` names `CANDIDATE`'s HEAD. Otherwise back to `prepare`.
3. The live checkout is clean — `git status --porcelain` prints nothing: MERGE, RELEASE and Close write there, and `gitter` refuses a dirty `develop`. A dirty path is a STOP that names it; the user clears it. Then `gitter` Phase PULL (`git pull --ff-only origin develop`), and `develop` must fast-forward to `release/v{NEW}` (`git merge-base --is-ancestor develop release/v{NEW}`). When `develop` advanced, `gitter` merges `develop` into `release/v{NEW}` in `CANDIDATE` and `prepare` resumes: the new commits are reviewed, noted, gated and rehearsed like the rest.
4. `gitter` Phase MERGE: `git merge --ff-only release/v{NEW}` onto `develop` in the live checkout, naming `DIR/gate.md` (`GATE PASS {sha}`) and `DIR/READY` as the gate that passed.
5. `scripts/leak-check.sh --range origin/main develop` exits 0 — brand current and former, user PII, machine home paths, secrets. A hit is a content bug: fix, commit, back to `prepare`.
6. Source-fetched skills — for each note bullet naming a `templates/project/skills/sources.json` skill, ship the substance to the skill's own public repo: rebase-first against its state (both changed: keep the richer, never blast-overwrite), genericize project identifiers, sync the live `.claude/skills/{name}/` byte-identical, bump its `version:` and README references, leak-grep the staged diff. Its clone, commit, annotated tag and push go to `gitter` (freeform, the skill repo's path, quoting the user's publish request). The Professor bullet stays a version pointer with its re-pull action.
7. `gitter` Phase PUSH `origin develop`, carrying the user's publish request as its authority; relay the pre-push hook's output verbatim; STOP if it fails. Never force-push; `main` is never a push target.
8. `gitter` Phase RELEASE `v{NEW}` with `DIR`: it re-runs the ready check against `develop`'s HEAD, then the `develop → main` PR, green required checks, merge, annotated tag, `develop` fast-forwarded onto `main`. STOP at the first failed step and report which.
9. `Agent(subagent_type: "releaser")` with `PHASE` VERIFY, `DIR`, `CANDIDATE`, `NEW`, `PREV`. `FAILED VERIFY` is reported as it is: the tag is public, and the fix is a patch release, never a moved tag.
10. Close: `VERSION`, `.professor/VERSION` and `.professor/manifest.json` `installed_from.version` become `{NEXT}-alpha` (`{NEXT}` = `NEW` with minor + 1, patch 0); `bash infra/check-self-hosted-manifest.sh --write . templates pfm` passes; `gitter` commits `chore(release): open v{NEXT}-alpha on develop` and pushes `develop`. `infra/fence/release-rehearsal.sh down` for both machines (`PFM_REHEARSAL_NAME` of each); `gitter` removes both worktrees and the merged `release/v{NEW}` branch.
11. Report the PR URL, the merge SHA, the tag URL, the VERIFY lines and `DIR/run.md`, ending with: `Blueprint released: v{NEW}. URL: https://github.com/rezzminator/professor/releases/tag/v{NEW}`

## Hard rules

**NEVER:** push secrets; commit project-specific identifiers (a private project's brand name, current and former, user PII, internal URLs, machine-absolute home paths such as `/home/…` and `/Users/…`); force-push; bypass the pre-push hook; publish a commit other than the one `READY` names; move or delete a published tag; stage anything from a scratch directory. **The repo is PUBLIC — every push is world-visible, and a leak cannot be unpublished.**
