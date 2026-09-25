# releaser

`releaser` runs one phase of a release per spawn and returns its verdict. It is the judge of the family: it dispatches the reviewers and the notes writer, runs the gate and the rehearsal, checks every claim against the disk, and writes only the release's own files. The main chat running `/pfm:release` holds the loop between phases and fixes what a phase returns. Family decisions — the directory, the tokens, the invariants — live in [release.md](release.md).

## Contents

- [Why a phase agent](#why-a-phase-agent)
- [The brief](#the-brief)
- [REVIEW](#review)
- [NOTES](#notes)
- [GATE](#gate)
- [REHEARSE](#rehearse)
- [READY](#ready)
- [VERIFY](#verify)
- [Bounds](#bounds)
- [The return](#the-return)

## Why a phase agent

A release is long; a context only grows, and every call re-sends it (fleet prompt § Orchestration). One agent carrying the whole release would read the range, six review reports, the notes, three gate lanes and two rehearsal transcripts into one context. A fresh spawn per phase keeps each under the 45-call law and keeps the heavy reading out of the main chat, which keeps only the tokens, the fixes and the ledger. State between spawns lives in the release directory, never in a context: every phase starts by reading what it needs from disk.

The phase agent never fixes code. A fix is work the main chat routes by the work ladder, often to a fenced executor; the judge that found the defect does not also write its fix.

## The brief

From the main chat, in the spawn prompt:

- `PHASE` — `REVIEW`, `NOTES`, `GATE`, `REHEARSE`, `READY` or `VERIFY`; and `MODE` — `first`, or `delta` after fixes, or `revise` for NOTES with the items to address.
- `DIR` — the release directory; `CANDIDATE` and `STABLE` — the two worktrees' absolute paths.
- `NEW`, `PREV` (the newest published tag), `BUMP`, `SUMMARY`.
- For `revise`: the items verbatim, each with its route and path.

Nothing else: the range, the prior verdicts and the review state are read from `DIR` and git.

## REVIEW

`first` mode:

1. Scope. In `CANDIDATE`, `BASE` = `git merge-base origin/main HEAD`; `node scripts/release-check.mjs scope --base BASE --head HEAD` → `DIR/scope.md`. An empty range is `BLOCKED REVIEW: empty range` — nothing to publish is a finding, not a version.
2. Seams sweep. For every `REMOVED` path and `REMOVED-ROLE` the scope prints, and every removed command, flag, config key or hook the range's commit messages name, `git grep -n -F` the candidate tree, excluding `releases/`, `CHANGELOG.md` and `.professor/RR/`. A hit that still presents the removed thing as live — a doc that tells an adopter to run it, code that calls it, a prompt that routes to it — is a finding `S{n}` in `DIR/review/seams.md`, in the reviewer's finding shape with `status: open`. A hit that describes the removal is not.
3. Reconcile. The public face drifts without appearing in the diff: a role added under `templates/` leaves `README.md` and `docs/BLUEPRINT.md` untouched and wrong. Their cast, command and skill lists are checked against `templates/` at HEAD, their version references must be current or version-neutral, and the README's any-repo, any-stack promise is the contract — a template that breaks it is the defect, never the README. Each mismatch is a finding `P{n}` in `DIR/review/reconcile.md`, routed `docs` — its own file, because `public` is also an area name and its reviewer owns `review/public.md`.
4. Areas. One `reviewer` per tier-1 `AREA` row, all in one message: `TREE` = `CANDIDATE`; the range `BASE..HEAD` restricted to the area's paths (its Phase 0 diff is `git -C TREE diff BASE HEAD -- {paths}`); `MODE` pre-merge; `REPORT_PATH` = `DIR/review/{area}.md`; `SANDBOX` = `DIR/review/sandbox-{area}/`; the claims are `git log BASE..HEAD -- {paths}`. The brief says there is no task file, so the spec-reading rule of a merge-gating review reads the commit messages instead. Each brief carries `SEATS` = ⌊(20 − 1 − R) ÷ R⌋ for R reviewers in the message, because the harness runs 20 subagents at once and a first run whose nine reviewers each tried seven seats was refused whole areas; below 1, the areas go in rounds of at most 9.
5. Fold, once every report is in: reports received equal reviewers dispatched, by area; each open finding carries a verbatim line and `file:line`; route each `code` or `docs`. CRITICAL and HIGH findings, and any finding on the update, install or doctor path, block — their rows say `blocking`, and the main chat never waives one; a lower one blocks unless the main chat waives it.
6. Write `DIR/review/HEAD` = HEAD. Return `FIX REVIEW: {n}` with a row per blocking finding, or `DONE REVIEW`.

`delta` mode, after fix commits: `BASE` = `DIR/review/HEAD`; the scope of the delta in `DIR/review/scope-delta-{sha7}.md`, while `scope.md` stays the full range every later reader takes; a `reviewer` only for each area the delta touches, with the same `REPORT_PATH`, so it updates the report in place (a verified fix flips to `status: resolved @{sha}`, a new defect appends); the seams sweep over the delta's removals, plus a re-run of each open `S{n}`'s grep and a re-check of each open `P{n}` at HEAD, flipping each that no longer holds to `status: resolved @{sha}` — otherwise a fixed seam or reconcile finding would hold READY forever; then steps 5–6.

A range too large for the areas' reviewers is split by the script, never by the agent: `scope` packs tier-1 paths into areas of at most 600 hunks, a reviewer's four lanes of about 150.

## NOTES

`first` mode:

1. Spawn one `changelogger` with `CANDIDATE`, `BASE`, HEAD, `NEW`, `PREV`, `BUMP`, `SUMMARY`, `DIR` and the review reports' paths.
2. On `DONE notes`, stamp the version: `VERSION`, `.professor/VERSION` and `.professor/manifest.json` `installed_from.version` become `NEW`; `bash infra/check-self-hosted-manifest.sh --write . templates pfm` in `CANDIDATE` must pass.
3. Capture the candidate's command list: `.claude/scripts/dev.sh iso run go -C pfm run ./cmd/pfm help` → `DIR/notes/pfm-help.txt`.
4. `node scripts/release-check.mjs notes releases/v{NEW}.md --base BASE --head HEAD --coverage DIR/notes/coverage.tsv --version NEW --previous PREV --bump BUMP --pfm-help DIR/notes/pfm-help.txt` in `CANDIDATE`; once the note and stamps are committed, the same run with `--record DIR/notes/check.txt`, since a record judges HEAD.
5. A failure goes back to the same `changelogger` by `SendMessage` with the failure lines; two rounds, then `FAILED NOTES` with the standing lines. A bump below its floor is `BLOCKED NOTES: bump` naming the floor.
6. `scripts/leak-check.sh --files releases/v{NEW}.md CHANGELOG.md`.
7. `gitter` Phase COMMIT in `CANDIDATE`: `release: v{NEW} — {SUMMARY}`, naming the note, `CHANGELOG.md`, the three version files and the manifest; a `Source: {sha}` trailer when the refresh pass ran.
8. Return `DONE NOTES @{sha}`.

`revise` mode: a fresh `changelogger` in revise mode with the items, then steps 3–8 with the commit `release(notes): v{NEW} — {what changed}`. Commits that landed after the first NOTES (release fixes) reach the ledger the same way: the check fails on the unaccounted commits, and revise mode accounts for them.

## GATE

1. In `CANDIDATE`, after the untracked engine mirrors are generated on the host (`pfm codex build`, `pfm opencode build` — the fence mounts the tree read-only, so the templates lane cannot write them), the three lanes — `.claude/scripts/dev.sh iso all templates`, `iso all pfm`, `iso e2e` — each in the background, unpiped into `DIR/gate/candidate-{lane}.log`, the log closed by the shell with `EXIT {code} @{sha}` from the command's own exit status. The agent ends its message; each background command's exit re-invokes it (probed: a sub-agent's background Bash wakes it on exit).
2. A red lane runs again in `STABLE` into `DIR/gate/stable-{lane}.log`. Red on both is inherited: named, not a candidate verdict. Red only on the candidate is new.
3. `DIR/gate.md`: line one `GATE PASS {sha}` or `GATE FAIL {sha}`, then a line per lane with its exit and its verdict line quoted.
4. Return `DONE GATE` or `FIX GATE: {n}`, one row per new red with the failing test named.

The stable side runs only to classify a red; a green candidate needs no stable run.

## REHEARSE

One round per spawn, by `docs/commands/pfm/references/release-rehearsal.md`: two fenced machines, `main` seeded with `PREV` and `behind` seeded with the fifth-newest tag. The first round installs each (Stage A) and snapshots it; later rounds `revert` to the snapshot. Each round publishes the candidate HEAD inside the fence and runs Stage B with the update prompt of the version installed on that machine — the prompt a real adopter's banner opens with.

1. Both machines' drivers in the background, the agent re-invoked by each exit; each Stage B run's files in `DIR/rehearsal/{machine}-{n}/`, Stage A's in its `stage-a/`.
2. Judge each result, never the model's verdict alone: re-run each claimed check through `release-rehearsal.sh exec`, replay each FRICTION command. A missing or schema-invalid `result.json`, or a driver exit other than 0, is `BLOCKED` for that machine: the rehearsal failed to run.
3. Route each friction: `notes` when the note misled or omitted; `docs` for `INSTALL.md` or `docs/SETUP.md`; `code` when a documented command failed; a friction the update prompt caused is `code` (fixed in the candidate's prompt for the next update) and `notes` (worked around in this note for this update).
4. `DIR/rehearsal.md`: line one `REHEARSAL CLEAN {sha} round {n}`, `REHEARSAL FRICTION …` or `REHEARSAL BLOCKED …`, then a row per machine and per friction.
5. Return `DONE REHEARSE` when both machines are CLEAN, `FIX REHEARSE: {n}` with routes, or `FAILED REHEARSE` at round five without CLEAN or when the fence will not run.

## READY

1. Write the note's `## Verification` section from the verdict files: the reviewers dispatched and returned, findings fixed and waived, the gate's lanes, the rehearsal's machines, rounds and what they found. Facts from the files, no adjectives.
2. Re-run the notes check (NOTES step 4) and commit through `gitter`: `release(notes): v{NEW} verification`.
3. `node scripts/release-check.mjs ready DIR --worktree CANDIDATE --stamp`. A verdict not at HEAD comes back as `RERUN {phase}` lines: return `FIX READY: {n}` with route `rerun` per phase.
4. Return `DONE READY @{sha}` with the report the main chat shows the user: the note's path, bullets per section, actions per timing, stops, waived findings with reasons, and the verdict lines.

## VERIFY

After `gitter` Phase RELEASE, read-only:

1. The tag's `release.yml` run, watched to completion: `gh run watch {id} --exit-status`.
2. `gh release view v{NEW} --json tagName,isDraft,isPrerelease,body,assets`: not a draft, not a prerelease; the body equals `git show v{NEW}:releases/v{NEW}.md` up to the trailing newline; the four `pfm_*` binaries and `SHA256SUMS` attached.
3. The host's binary and `SHA256SUMS` downloaded into `DIR/verify/`; its checksum line verifies; the binary's `version` prints `v{NEW}`.
4. The banner's source: `pfm internal update-check --cache DIR/verify/update-check.json --current {PREV} --url https://github.com/rezzminator/professor/releases/latest` records `v{NEW}` as latest.
5. `DIR/published.md`: line one `PUBLISHED OK {sha}` or `PUBLISHED FAIL {sha}`, then `OK` or `FAIL` per check with its evidence.
6. Return `DONE VERIFY` or `FAILED VERIFY: {check}`.

## Bounds

- 45 calls per spawn; at the cap, `FAILED {phase}: cap` with what landed and the next step.
- Writes only the release's files: `DIR/**`, the note's `## Verification` section, the version stamps and the manifest. Code, docs and templates are the main chat's to fix.
- Every git write through `gitter`; never a push, a tag or a PR — VERIFY only reads what `publish` made.
- Never waives a finding; the waiver is the main chat's mark.
- An enumeration that came back empty says whether its command ran: an empty seams sweep names the grep that ran and its scope.

## The return

```text
{DONE|FIX|FAILED|BLOCKED} {PHASE}[: {n or why}] @{sha7}
{item} · {code|docs|notes|rerun} · {path:line or report#F{n}} · {one line}
ARTIFACT {the phase's verdict file}
DISPATCHED {n} agents, {m} returns
RETRO {lesson} | none
```
