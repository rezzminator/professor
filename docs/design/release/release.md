# Release

A release takes `develop`'s range past `main` to a published tag that an adopter can update onto without guessing. `/pfm:release` runs it in two verbs: `prepare` reviews, fixes, writes the notes, gates and rehearses the candidate until it is `READY`, with no publish authority; `publish`, on the user's explicit in-turn ask, lands the exact commit that was rehearsed and verifies what GitHub serves. This file holds the family's decisions; each member's own live in its file.

A change lands in this design doc first, then in the agent, command or script, then in every surface listed under [Surfaces that stay in sync](#surfaces-that-stay-in-sync).

## Contents

- [Why it exists](#why-it-exists)
- [The reader](#the-reader)
- [The family](#the-family)
- [The lifecycle](#the-lifecycle)
- [The phases](#the-phases)
- [The release directory](#the-release-directory)
- [Verdict tokens](#verdict-tokens)
- [The invariants](#the-invariants)
- [Versioning](#versioning)
- [Where each rule lives](#where-each-rule-lives)
- [Names](#names)
- [What the family replaced](#what-the-family-replaced)
- [Accepted risk](#accepted-risk)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)
- [Open items](#open-items)
- [Evidence](#evidence)

## Why it exists

Every adopter updates through the release notes, and the most common measured failure of release notes is omission, most of all of breaking changes (Evidence). The single-command flow this family replaces had reviewers at the mechanical tier write the notes as a side product of reviewing, one main chat carry a 14-step procedure end to end, four spellings of the bullet prefix and three of the action marker across published notes, a review report that no gate read, and a gate lane (`walker`) that no longer existed. Nothing proved that every change in the range reached the notes, and nothing proved that the commit rehearsed was the commit tagged.

The family splits the work by who is allowed to judge what: an agent that only writes the notes, an agent that only runs and judges phases, a script that decides everything that can be computed, a rehearsal that tests the notes by following them, and the user who reads the result before anything is published.

## The reader

The notes have one primary reader: the adopter's update chat, opened from `pfm ls`'s PROFESSOR UPDATE banner with the prompt `professorUpdatePrompt` (`pfm/internal/picker/update_row.go`; `pfm/cmd/pfm/update_notice_command.go` through v0.77.x). That chat runs `pfm version`, reads every `releases/vX.Y.Z.md` after the installed version through the target oldest first with `git show {target}:releases/…`, merges their `#### → For:` lines into one checklist, shows the user an overview, and only on approval runs `pfm update --to {target}`, the checklist, and `pfm doctor`. Inside each adopted project it then runs `pfm update check`, hand-applies each `UPDATED` template diff and pins it.

Three facts about that reader shape every rule of the family:

- The banner and the prompt come from the binary the adopter already runs. The update into vX runs the previous release's code; a change to the update path shows first on the update after it.
- The reader is a model following text literally. An action without a timing is guessed into before or after the update; an action without a named surface cannot be superseded by a later release's action on the same surface.
- `pfm update` prints the paths of the same notes (`pfm/internal/update/notes.go` `ReleaseNotes`), and `.github/workflows/release.yml` publishes the file verbatim as the GitHub Release body. A note is read three ways and must survive all three.

The note grammar that serves this reader is public, because adopters read it: `docs/RELEASE.md` § Release notes. [changelogger.md](changelogger.md) holds the decisions behind it.

## The family

| Member | Kind | Does | Runs at |
| --- | --- | --- | --- |
| `/pfm:release` | command | The human front and the loop: `prepare` and `publish`, one phase at a time, fixes by the work ladder, the run ledger | the main chat |
| `releaser` | agent | Runs one phase per spawn and judges it: scope and review, the notes gate, the fenced gate, the rehearsal, the ready stamp, the published-release check | smart (`opus`), effort `high` |
| `changelogger` | agent | Writes `releases/v{NEW}.md`, the `CHANGELOG.md` line and the coverage ledger for the updating adopter; revises on the gate's findings | smart (`opus`), effort `xhigh` |
| `reviewer` | agent (existing) | One per review area, pre-merge mode, its report at a path in the release directory | its own pin |
| `gitter` | agent (existing) | Every git write: worktrees, commits on `release/v{NEW}`, the merge, the push, Phase RELEASE | its own pin |
| `scripts/release-check.mjs` | script | Everything computable: the scope and its tiers, the note grammar and coverage, the ready verdict | — |
| `infra/fence/release-rehearsal.sh` | script (existing) | The fenced adopter machine the rehearsal drives | — |

`releaser` runs at smart because every phase ends in a judgment with liability: which finding blocks, whether a friction is the notes' or the code's, whether a gate red is new. `changelogger` runs at smart and `xhigh` because a document an adopter acts on is never mechanical work, and the range of a release is the largest body of reasoning any single agent in the fleet writes from.

## The lifecycle

1. `prepare` opens the release: pulls `develop`, fetches tags, sets up the two worktrees and the release directory, runs the refresh pass when `--from` names a live project.
2. Phases run in order; each is a fresh `releaser` spawn. A phase that finds something to fix returns it; the main chat fixes it on the candidate by the work ladder and re-runs the phase over the delta.
3. `READY`: `release-check ready --stamp` writes the commit that passed every verdict. The main chat reports the note, the waivers and the verdicts to the user and stops.
4. The user reads the note. Publication is a separate decision, made in the user's own turn.
5. `publish` lands exactly the stamped commit, pushes, runs `gitter` Phase RELEASE, spawns the `releaser` for VERIFY, and closes the development line.

`prepare` is safe to run days before a release: it writes only the candidate branch, the worktrees and the release directory. It resumes from `run.md` when run again for the same version.

## The phases

| # | Phase | Run by | Output | A red goes to |
| --- | --- | --- | --- | --- |
| 0 | OPEN | main chat | worktrees, release directory, `run.md`, refresh pass | stop |
| 1 | REVIEW | `releaser` → one `reviewer` per area, plus the seams sweep | `review/*.md` | the main chat fixes; REVIEW re-runs over the delta |
| 2 | NOTES | `releaser` → `changelogger`, then `release-check notes` | the note, the index line, `notes/coverage.tsv`, the version stamps, the release commit | `changelogger` revises, twice at most |
| 3 | GATE | `releaser` | `gate.md` | the main chat fixes; GATE re-runs |
| 4 | REHEARSE | `releaser` | `rehearsal.md`, `rehearsal/{machine}-{n}/` | notes frictions → NOTES revise; code and doc frictions → the main chat |
| 5 | READY | `releaser` | the `## Verification` section, `READY` | the phase whose verdict is not at HEAD |
| 6 | PUBLISH | main chat → `gitter` | the ruleset probe, the merge, the leak check, the standalone skill repos, the push, the PR, the tag | stop |
| 7 | VERIFY | `releaser` | `published.md` | stop and report |
| 8 | CLOSE | main chat → `gitter` | the next `-alpha` line, cleanup | stop |

The phase protocols live in [releaser.md](releaser.md); the loop around them lives in the command.

## The release directory

```text
$HOME/.local/state/pfm/releases/{project}/v{NEW}/
  run.md            the ledger: one line per phase return and per fix commit   written by /pfm:release
  scope.md          range, counts, the tier and area table                      written by releaser (REVIEW)
  review/{area}.md  one merge-gating report per area                            written by reviewer
  review/seams.md   deleted and renamed names still referenced                  written by releaser (REVIEW)
  review/reconcile.md  README and BLUEPRINT lists reconciled against templates/ written by releaser (REVIEW)
  review/sandbox-{area}/  a reviewer's scratch                                  written by reviewer
  review/scope-delta-{sha7}.md  the scope of one delta pass                     written by releaser (REVIEW)
  review/HEAD       the commit the last REVIEW covered                          written by releaser (REVIEW)
  notes/coverage.tsv  one row per commit in the range                           written by changelogger
  notes/pfm-help.txt  the candidate's `pfm help`, for rule P1                   written by releaser (NOTES)
  notes/check.txt   the last release-check notes output                         written by releaser (NOTES)
  gate/{candidate|stable}-{lane}.log  one fenced lane, closed `EXIT {code} @{sha}`  written by releaser (GATE)
  gate.md           the fenced gate's verdict lines                             written by releaser (GATE)
  rehearsal.md      the rehearsal verdict                                       written by releaser (REHEARSE)
  rehearsal/{machine}-{n}/  one Stage B driver run: brief, schema, result; Stage A in its stage-a/  written by releaser (REHEARSE)
  READY             the stamped commit, version and date                        written by release-check ready --stamp
  verify/           the downloaded binary, SHA256SUMS, the update-check cache   written by releaser (VERIFY)
  published.md      what GitHub serves, checked                                 written by releaser (VERIFY)
```

- `{project}` is the repo directory's basename with any leading dot stripped, as for flights. The directory is kept across reboots and outlives the release branch: `prepare` resumes from it, and an audit reads it after the tag.
- One writer per file; nobody edits another writer's file. The single exception is the reviewer contract's own: the main chat marks a finding `waived` in its report, with the reason, which is the orchestrator's mark by that contract.
- The note and the index line live in the candidate worktree, because they ship; everything else lives here, because it does not.

## Verdict tokens

`releaser` opens every return with one token on the first line; the main chat matches the token, never the prose.

| Token | Means | The main chat |
| --- | --- | --- |
| `DONE {phase}` | The phase passed at the commit it names | records it, runs the next phase |
| `FIX {phase}: {n}` | `n` items the main chat must fix, each with its path and its route (`code`, `docs`, `notes`) | fixes `code` and `docs` by the work ladder; sends `notes` items to NOTES in revise mode; re-runs the phase |
| `FAILED {phase}: {why}` | The phase could not reach a verdict: a tool that would not run, the cap, five rehearsal rounds without CLEAN | reads the cause; one re-run with a changed brief; a second is a stop |
| `BLOCKED {phase}: {question}` | A ruling only the user can make | asks the user; under full autonomy rules it and records the ruling in `run.md` |

`changelogger` returns `DONE notes` or `BLOCKED notes: {question}`. Every `run.md` line is `{time} {phase} {token} @{sha7} — {one line}`.

## The invariants

1. The rehearsed commit is the published commit. Each verdict file names the commit it ran at; `release-check ready` stamps `READY` only when every verdict holds at HEAD; `gitter` Phase RELEASE re-runs the check and refuses a HEAD that differs from the stamp.
2. Every commit in the range is accounted for. The coverage ledger names, for each commit, the bullet it reached or why it reaches no adopter; the check fails on a missing or extra commit.
3. The judge is never the judged. `changelogger` writes the note; the script, the `releaser` and the rehearsal judge it; the user reads it before publishing.
4. `prepare` never publishes. Push, tag, PR and release run only inside `publish`, on the user's explicit request in that turn (project contract § Publication).
5. A verdict holds for the commit it ran at. A later commit re-opens the phases it touches; `release-check ready` computes which, so no one has to remember.

A verdict carries over a later commit in exactly two cases, both computed by the script: the gate's, when the later commits touch only `releases/`, `CHANGELOG.md` and the version stamps; the rehearsal's, when they touch only the note's `## Verification` section.

## Versioning

`VERSION` is the source of truth; tags are `vX.Y.Z`, annotated, never moved. Between releases `develop` carries the next `X.Y.Z-alpha`.

The bump has a floor computed from the note's sections, following the left-most non-zero rule (Cargo, uv, Ruff): while the major is 0, the minor is the breaking component.

| Sections present | Floor at 0.x | Floor at ≥ 1.0 |
| --- | --- | --- |
| `Breaking`, `Migration` or `Removed` | minor | major |
| `Added` | minor | minor |
| only `Changed`, `Fixed` | patch | patch |

`release-check notes` fails a requested bump below its floor. 1.0 is a commitment, not a feature count (Tokio, Terraform, Go 1 in Evidence): the user declares it; no agent infers it from the diff.

## Where each rule lives

| Layer | Reader | Holds |
| --- | --- | --- |
| `docs/RELEASE.md` | adopters, `changelogger`, the rehearsal's adopter | Versioning, the note grammar, what the tag triggers, how to update |
| `.claude/commands/pfm/release.md` | the main chat | The verbs, the loop, the reactions to each token, the fixes, publication |
| `.claude/agents/releaser.md` | `releaser` | The phase protocols |
| `.claude/agents/changelogger.md` | `changelogger` | How to write for the updating adopter: grounding, routes, traps, coverage |
| `scripts/release-check.mjs` | everyone, by running it | The tier and area table, the grammar, coverage, the floors, the ready verdict |
| `docs/commands/pfm/references/release-rehearsal.md` | `releaser` at REHEARSE | The fenced machine, the driver, the briefs |
| `pfm/internal/picker/update_row.go` | the adopter's update chat | The update prompt |

A rule the script enforces is never restated as a prompt rule; the prompt names the check to run.

## Names

- The unit is a release; the family is `release`; a directory is `$HOME/.local/state/pfm/releases/{project}/v{NEW}/`.
- The agents are `releaser` and `changelogger`, role nouns like `reviewer` and `gitter`. `gitter` Phase RELEASE keeps its name: it is the git mechanics of publishing, one step inside `publish`.
- The candidate branch is `release/v{NEW}`; the worktrees are `.worktrees/release/develop` (the candidate) and `.worktrees/release/main` (detached on `origin/main`, the stable side).
- Both agents are project-local (`.claude/agents/`): they write for pfm's own update path, which no adopter has.

## What the family replaced

| Before | Now |
| --- | --- |
| One command of 14 steps carried end to end by one main chat | Two verbs; each phase a fresh `releaser` spawn under the 45-call law |
| Sonnet reviewers wrote the notes while reviewing | Reviewers review; `changelogger` writes; the script and the rehearsal judge |
| One review over the whole range, by area, with no coverage count | Reviewer runs sized to the reviewer's capacity over the adopter contract; the rest by the gate and the seams sweep (§ Accepted risk) |
| Four bullet-prefix schemes, three action-marker spellings | One grammar in `docs/RELEASE.md`, enforced by `release-check notes` |
| No check that every change reached the notes | The coverage ledger |
| The rehearsal drove the candidate's update prompt | The prompt of the version installed on the fenced machine, the one a real adopter sees |
| Nothing tied the tag to what was rehearsed | `READY` and `gitter`'s re-check |
| The tag reported as the end | VERIFY: the workflow, the assets, the body, the checksum, the update banner's source |

## Accepted risk

A range of hundreds of commits cannot be reviewed hunk by hunk at release time: review effectiveness collapses past about 300 lines an hour (Evidence), and one `reviewer` run holds about 600 hunks. The release review therefore covers in full only the adopter contract, the tier-1 paths `release-check scope` names: `templates/**`, `workflows/**`, the fleet prompt, pfm's update, install, init, doctor and config paths, the public docs and the release gates. Everything else relies on the review it had when it landed on `develop`, on the full fenced gate at the candidate, and on the seams sweep, which finds every deleted or renamed name still referenced anywhere in the tree. A defect inside an internal package that its own tests do not catch and its landing review missed can ship; the rehearsal catches the part of it an adopter's update touches.

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The command | `.claude/commands/pfm/release.md` | The loop and publication |
| The agents | `.claude/agents/releaser.md`, `.claude/agents/changelogger.md` | The executable wording |
| The script | `scripts/release-check.mjs` and its tests | The mechanisms |
| The public spec | `docs/RELEASE.md`, `CHANGELOG.md` (index and a pointer), `INSTALL.md` § Updating, `docs/SETUP.md` § Staying current | What adopters read |
| The reader's prompt | `pfm/internal/picker/update_row.go` `professorUpdatePrompt` and its test | Timing, surfaces, stops |
| The rehearsal | `docs/commands/pfm/references/release-rehearsal.md`, `infra/fence/release-rehearsal.sh` | The fenced adopter |
| The publish gate | `.claude/agents/gitter.md` Phase RELEASE | The ready re-check |
| The CI backstop | `.github/workflows/release.yml` | The grammar check on the tagged note |
| The mirrors and manifest | `.codex/`, `.opencode/`, `.professor/manifest.json` | The two agents registered |

## Open items

- Change-time adopter fragments. The strongest measured defense against omission is a note fragment written with each change and checked in CI (Go, Kubernetes, towncrier, changesets). This family keeps release-time derivation, grounded in commit messages and closed by the coverage ledger; a commit-message trailer carrying the adopter action (`Adopter: {when} · {surface} — {action}`) would move the knowledge to the moment the author holds it.
- A deterministic range merge. `pfm update plan --to vX` could parse the action grammar across the skipped range and print the ordered checklist, replacing the update chat's hand merge (Renovate, the Angular update guide). It helps only adopters whose installed binary already carries it.
- Provenance. `release.yml` could attest the binaries (`actions/attest-build-provenance`) and VERIFY check them with `gh attestation verify`; the build-from-source update path gains nothing from it, which is why it waits.

## Evidence

The research behind these rulings is `.professor/RR/release-notes-for-upgrading-adopters-2026-09-25.md`. The facts the design rests on:

- Omission is the measured failure of release notes, most of all for breaking changes (Wu et al., ICPC 2022); most notes list 6–26 % of the issues a release addressed (Abebe et al., EMSE 2016).
- LLM-written notes capture the what better than the why (arXiv 2404.14824); no study measures how often they invent entries. Grounding in authored commit messages, a coverage count, and a human read before publishing answer both.
- Mature projects tie each upgrade action to a surface and to before or after the upgrade (Kubernetes Urgent Upgrade Notes, OpenStack reno's upgrade section, Mattermost, Elastic) and declare required stops (GitLab).
- Release-time review is weighted by risk, never one pass over the whole range (Linux linux-next, Mozilla uplift rules, Chromium merge review); inspection past about 300 LOC an hour or 90 minutes loses its defect yield (SmartBear/Cisco).
- Verify the published artifact before announcing it (GoReleaser `verify`, PEP 101); the update banner reads `/releases/latest`, which is the newest non-draft, non-prerelease release.
- In 0.x the left-most non-zero component is the breaking one (Cargo, uv, Ruff); 1.0 is a support commitment (Tokio, Terraform, Go 1).
