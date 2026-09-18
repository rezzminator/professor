---
name: pfm:release
description: Versions, tags and publishes this repo — `/pfm:release {patch|minor|major} "{summary}" [--from {live-root}]`, "blueprint release", "cut a release"; only on the user's explicit in-turn publish request. Reviews and fixes develop, rehearses the adopter update in the fence, lands develop → main by release PR.
argument-hint: '{patch|minor|major} "{summary}" [--from {live-project-root}]'
---

# PFM Release — Publish the Blueprint

## Constants

- Public repo: `rezzminator/professor` — **this repo IS the upstream.** The working copy you are in is the one that publishes.
- Blueprint tree: `templates/` · Public README: `README.md` · Release notes: `releases/vX.Y.Z.md` · Index: `CHANGELOG.md` · Version file: `VERSION`
- Release worktrees: `.worktrees/release/main` (detached, byte-identical to `origin/main` — the STABLE side) and `.worktrees/release/develop` (branch `release/v{NEW}` from `develop` — the CANDIDATE side). Every release edit, fix, and commit lands in the candidate worktree; the live checkout holds other sessions' WIP and is never swept.
- Rehearsal: `infra/fence/release-rehearsal.sh` (the fenced adopter machine) + `$CDOCS/pfm/$REFS/release-rehearsal.md` (the Codex driver and its briefs).
- The **live source project** — the private repo whose `.claude/` the templates are derived from — is NOT this repo. It is named with `--from {path}` and is optional; without it, the refresh pass is skipped (Step 4).

## Pre-flight

1. **Publication authority.** This command pushes. It runs only on an explicit in-turn request to release/publish. No authority → stop here and say so.
2. `gh auth status` — must be the repo owner. `gh api repos/rezzminator/professor/rules/branches/main` must list `pull_request`, `non_fast_forward`, `deletion`, and `required_status_checks`; a missing rule is a STOP — restore the `main-release-only` ruleset before anything publishes.
3. `git fetch --tags origin`; `git pull --ff-only origin develop` in the live checkout (STOP if it fails); `git merge-base --is-ancestor origin/main develop` — a commit on `main` that `develop` lacks means the last release never fast-forwarded `develop` back; STOP and report it. Report the newest tag.

## Steps

1. **Validate args** — bump type + summary required, bail if missing. `patch` = bug fixes / doc tweaks · `minor` = new archetype, command, or step · `major` = breaking change or migration. Compute `{NEW}` by bumping the newest tag from Pre-flight 3; it carries no suffix and must exceed every tag. `develop`'s `VERSION` names the development line (`{X.Y.Z}-alpha`, opened by Step 13), never the release number.

2. **Worktrees** — `gitter` Phase SETUP twice: `.worktrees/release/main` as `git worktree add --detach … origin/main`, then verify `HEAD == origin/main` and `status --porcelain` empty; `.worktrees/release/develop` on branch `release/v{NEW}` from `develop`'s SHA. A resumed release reuses both only after the same verification; `main` drifted from `origin/main` → recreate it.

3. **Scope the range — always runs.** In the candidate worktree, `git diff --stat origin/main...HEAD` and `git log --format='%h %s%n%b' origin/main..HEAD` are the release's whole scope: every shipped change is in that diff, every author's intent in those messages. Report the file count and commit count verbatim; an empty range STOPS the release — nothing to publish is a finding, not a version.

4. **Refresh pass — only when `--from {live-project-root}` is given.** Without it, say `refresh skipped — no live source named`; a SILENT skip is not legitimate. Run the pass per `$CDOCS/pcm/$REFS/refresh.md` § The pass inside the candidate worktree, fed every template whose live source the scan reports CHANGED. STOP if it stops; a FAILED scan is never an empty one.

5. **Review the candidate** — dispatch reviewers (`subagent_type: general-purpose`, `model: sonnet`, effort High) over `git -C .worktrees/release/develop diff origin/main...HEAD`, one per area in ONE message when the diff spans areas (`pfm/`; `templates/` + `.claude/` + prompts; `workflows/` + `infra/` + `scripts/` + docs). Each reads the diff AND the Step 3 commit messages for its area and returns (a) defects — file:line, the failing input, severity — and (b) the release notes for its area: one bullet per shipped change, `- {Tier}: {scope} — {semantic change}` (Tier = Global / Project / Engine / Minor / Fixed / Removed), plus the adopter instruction as a `#### → For:` line under every bullet that changes what an adopter must do (a command to run, a file to edit, a hook or setting to migrate) and `(cost)` on env/hook/permission/model deltas. The reviewers are the ONLY authors of the notes — nothing is written during development. Reports received must equal reviewers dispatched. Verify every defect against the code yourself; relay none on a reviewer's word.

6. **Fix on the candidate** — each verified defect is fixed in `.worktrees/release/develop` per root `CLAUDE.md` § Process (code through `dev.sh iso`, a regression test watched failing first; markdown directly), committed through `/git` on `release/v{NEW}`.

7. **Write the release notes** — NEW file `releases/v{NEW}.md` (title `# v{NEW} — {YYYY-MM-DD}`, bullets grouped under `## Added/Changed/Fixed/Removed/Breaking/Migration`): the Step 5 bullets, two reviewers describing one change merged into one bullet. A `#### → For:` line is the adopter's action for that version — it rides with its bullet, never paraphrased. Prepend `- [v{NEW}](releases/v{NEW}.md) — {summary}` to the `## Releases` index in `CHANGELOG.md`. Zero bullets from the reviewers → ask the user rather than inventing any.

7b. **Source-fetched skill release** — for each bullet naming a `sources.json` skill, ship the substance to the skill's OWN public repo first: clone/pull it → rebase-first against its current state (both-changed = keep the richer, never blast-overwrite) → genericize project identifiers → sync the live `.claude/skills/{name}/` byte-identical → bump its `version:` frontmatter + README refs → leak-grep the staged diff → commit + annotated tag + push. Then rewrite the professor bullet as a version pointer marked **`update`: skip — informational only** with a `#### → For:` re-pull note.

8. **Reconcile the candidate** — `README.md` + `docs/BLUEPRINT.md` cast / command / skill lists match `templates/`, version references stay current (prefer version-neutral phrasing); the README's universal "any repo / any stack" promise is the CONTRACT — fix drifted templates up to it, never downgrade the README. `echo "{NEW}" > VERSION` (dropping the `-alpha`). Stamp the self-hosted install ledger: `.professor/VERSION` and `manifest.json`'s `installed_from.version` to `{NEW}`, re-stamp `file_hashes` for the roster `infra/check-self-hosted-manifest.sh` enumerates, then `bash infra/check-self-hosted-manifest.sh . templates pfm` — STOP on failure. `gitter` Phase COMMIT: `release: v{NEW} — {summary}` (+ `Source: {sha}` trailer when Step 4 ran).

9. **Gate both sides in the fence** — from each worktree, `.claude/scripts/dev.sh iso all {templates|pfm|walker}` and `.claude/scripts/dev.sh iso e2e`. Stable red → report it as inherited, never a candidate verdict; candidate red → Step 6, then re-gate. Quote every verdict line.

10. **Rehearse the update** — per `$CDOCS/pfm/$REFS/release-rehearsal.md`: Codex installs the stable release on the fenced adopter machine and adopts it on a project, the machine is snapshotted, the candidate is published inside the fence, and Codex updates as an adopter would. Every FRICTION finding → Step 6 fix, commit, `revert`, re-publish, re-run the update; the loop ends at a CLEAN verdict. Five attempts without CLEAN → STOP and report the standing findings.

11. **Land on develop** — in the live checkout, `git merge --ff-only release/v{NEW}` through `gitter` Phase MERGE. `develop` advanced meanwhile → merge `develop` into `release/v{NEW}` in the candidate worktree and re-run Steps 9–10 before landing.

12. **Publish** — through `gitter` (or the active main Codex fallback only after explicit current-turn authorization):

    a. `scripts/leak-check.sh --files <every file changed since origin/main>` — brand current+former, user PII, machine home paths, zero secrets. Report its exit status. A single leftover is a refresh bug, not an exception.

    b. `gitter` Phase PUSH (`origin develop`), carrying the user's explicit publish request as its authority. Relay the pre-push hook's output verbatim; STOP if it fails; NEVER force-push. `main` is never a push target.

    c. `gitter` Phase RELEASE (`v{NEW}`): the `develop → main` PR, green required checks, merge, annotated tag, `develop` fast-forwarded onto `main`. STOP at the first failed step and report which.

13. **Close** — open the next development line on `develop` in the live checkout: `VERSION`, `.professor/VERSION` and `manifest.json`'s `installed_from.version` become `{NEXT}-alpha` (`{NEXT}` = `{NEW}` with minor + 1, patch 0), `infra/check-self-hosted-manifest.sh` passes, and `gitter` commits `chore(release): open v{NEXT}-alpha on develop` and pushes `develop` — a build from `develop` then never reports itself as the release it follows. `infra/release-rehearsal.sh down`; `gitter` removes both release worktrees and the merged `release/v{NEW}` branch.

14. **Report** the release PR URL, merge SHA, tag URL, source SHA (or "no refresh"), reviewer and rehearsal verdicts with the attempt count, and the release-note bullets, ending with: `Blueprint released: v{NEW}. URL: https://github.com/rezzminator/professor/releases/tag/v{NEW}`

## Hard rules

**NEVER:** push secrets; commit project-specific identifiers (a private project's brand name — current AND former — user PII, internal URLs, machine-absolute home paths such as `/home/…` and `/Users/…`); force-push; bypass the pre-push hook; ship Tier A characters with empty placeholders; strip an archetype's identity down to abstraction; auto-bump the README version without re-checking the templates; stage anything from `tmp/`; publish a candidate whose rehearsal did not end CLEAN. **The repo is PUBLIC — every push is world-visible, and a leak cannot be unpublished.**
