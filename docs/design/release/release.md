# Release

A release ships `develop`'s tip as it stands at the cut to a published tag that an adopter can update onto without guessing. `/pfm:release cut {patch|minor|major} "{summary}" [--dry]` runs it in one pass of about an hour: it gates the cut commit, writes the note, and publishes, with no code written inside the release. This file holds the family's decisions; each member's own live in its file.

A change lands in this design doc first, then in the command, agent or script, then in every surface listed under [Surfaces that stay in sync](#surfaces-that-stay-in-sync).

## Contents

- [Why it is one pass](#why-it-is-one-pass)
- [The reader](#the-reader)
- [The family](#the-family)
- [The cut](#the-cut)
- [The checks](#the-checks)
- [The release directory](#the-release-directory)
- [The invariants](#the-invariants)
- [Versioning](#versioning)
- [Where each rule lives](#where-each-rule-lives)
- [Names](#names)
- [Accepted risk](#accepted-risk)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)
- [Open items](#open-items)
- [Evidence](#evidence)

## Why it is one pass

Every commit reaches `develop` through a flight that already reviewed and gated it. A release adds only what no single flight can see: the whole range's seams, the README against what ships, the plugins pinned at published tags, the note, and the published assets. Everything a release checks is therefore a STOP, never a fix: a defect found at the cut is fixed on `develop` through a normal flight, and the user cuts again. The cut takes `develop`'s tip at that moment, with no soak window, so the release can never chase a candidate that moves under it.

Typing `/pfm:release cut` is the user's explicit publish request for that version (project contract § Publication). `--dry` runs every check and the release commit, then stops before any push.

## The reader

The notes have one primary reader: the adopter's update chat, opened from `pfm ls`'s PROFESSOR UPDATE banner with the prompt `professorUpdatePrompt` (`pfm/internal/picker/update_row.go`; `pfm/cmd/pfm/update_notice_command.go` through v0.77.x). That chat runs `pfm version`, reads every `releases/vX.Y.Z.md` after the installed version through the target oldest first with `git show {target}:releases/…`, merges their `#### → For:` lines into one checklist, shows the user an overview, and only on approval runs `pfm update --to {target}`, the checklist, and `pfm doctor`. Inside each adopted project it then runs `pfm doctor --project-updates`, takes the report to `clean`: each `UPDATED` row ported and pinned (or kept on record with `pin --keep`), each `NEW` row adopted or ignored, each `RETIRED-NAME` hit fixed or accepted with `pfm update accept`.

Three facts about that reader shape every rule of the family:

- The banner and the prompt come from the binary the adopter already runs. The update into vX runs the previous release's code; a change to the update path shows first on the update after it.
- The reader is a model following text literally. An action without a timing is guessed into before or after the update; an action without a named surface cannot be superseded by a later release's action on the same surface.
- `pfm update` prints the paths of the same notes (`pfm/internal/update/notes.go` `ReleaseNotes`), and `.github/workflows/release.yml` publishes the file verbatim as the GitHub Release body. A note is read three ways and must survive all three.

The note grammar that serves this reader is public, because adopters read it: `docs/RELEASE.md` § Release notes. [changelogger.md](changelogger.md) holds the decisions behind it.

## The family

| Member | Kind | Does | Runs at |
| --- | --- | --- | --- |
| `/pfm:release` | command | The one pass: pull, cut, checks, the release commit, publication, close; the run ledger | the main chat |
| `changelogger` | agent | Writes the draft note, the `CHANGELOG.md` line and the coverage ledger into the release directory; revises on the check's failure lines | smart (`opus`), effort `high` |
| `reviewer-pro` | agent (existing) | One round over the `pfm-update` area, only when the cut changes it | its own pin |
| `gitter` | agent (existing) | Every git write: the pull, the push, the release worktree, the release commit, Phase RELEASE, the close | its own pin |
| `scripts/release-check.mjs` | script | Everything computable: the scope and its tiers, the note grammar and coverage, the version floor, the preflight | — |
| `.github/workflows/ci.yml` | workflow | Tests every push to `develop`; its run on the cut commit is a check | GitHub Actions |
| `.github/workflows/release.yml` | workflow | Gates the release PR, publishes its merge, verifies what GitHub serves | GitHub Actions |

`changelogger` runs at smart because a document an adopter acts on is never mechanical work, and the range of a release is the largest body of reasoning any single agent in the fleet writes from.

## The cut

1. `gitter` Phase PULL fast-forwards `develop` and fetches tags. `origin/main` must be an ancestor of `develop`; the cut commit `C` is `develop`'s HEAD; `PREV` is the newest suffix-free `v*` tag and `NEW` is `PREV` bumped. An empty `origin/main..C` is a STOP.
2. `gitter` Phase PUSH sends `develop` when `origin/develop` lags `C`, so `ci.yml` runs on `C` (skipped under `--dry`).
3. `gitter` Phase SETUP makes `.worktrees/release/` on a new branch `release/v{NEW}` at `C`.
4. The checks of [§ The checks](#the-checks) run in parallel; each failure is a STOP.
5. The main chat copies the note into `releases/v{NEW}.md`, prepends the index line, writes `## Verification` from the checks' results, stamps `VERSION`, `.professor/VERSION` and the manifest to `NEW`, runs the full notes check, and `gitter` commits `release: v{NEW} — {summary}`. A release commit touching any file outside the five release files is a STOP.
6. `gitter` Phase RELEASE runs `make release-check`, pushes `release/v{NEW}`, opens the PR into `main`, waits for the required checks, merges with a merge commit, and watches `release.yml`'s `publish` job tag `v{NEW}` and its `verify` job check the assets.
7. One `gitter` close merges `origin/main` back into `develop`, opens `{NEXT}-alpha`, pushes `develop`, deletes `release/v{NEW}` and removes the worktree.

A STOP before step 6 removes the release worktree and its unpushed branch and keeps the release directory; a STOP at step 6 or later removes nothing and reports every ref that stands. A failure after the tag exists is reported as it is: the tag is public, and the fix is a patch release, never a moved tag.

## The checks

| Check | Runs | Passes when |
| --- | --- | --- |
| Gate | `.claude/scripts/test.sh --full` in the release worktree, over the clean tree at `C` | `gate.log` ends `EXIT 0 @{C}` |
| Leak | `scripts/leak-check.sh --range origin/main {C}` | exit 0 |
| Preflight | `release-check preflight --base origin/main --head {C}` | exit 0: no removed name still live (S1), every README command shipped (D1), every listed plugin at a published tag (T1) |
| CI | `gh run list --workflow ci.yml --commit {C}`, then `gh run watch` | conclusion `success`; under `--dry` with `C` unpushed it is a named gap |
| Notes | `changelogger`, then `release-check notes` with coverage, the title's version and `--pfm-help` | exit 0, within two `SendMessage` revision rounds |
| Review | one `reviewer-pro`, only when `release-check scope` prints a `pfm-update` area | no CRITICAL or HIGH finding on the update, install or doctor path; every other finding is listed in `## Verification` as not fixed in this release |

The review's premise is the reader's first fact: an adopter reaches this release by running the previous release's binary. It runs once, with no delta round, because nothing is fixed inside the release. No pre-release update run is made: the update path is proven by the gate's tests, `ci.yml`'s fresh installs and the review, never by a model following the note.

## The release directory

```text
$HOME/.local/state/pfm/releases/{project}/v{NEW}/
  run.md          one line per step, `{time} {step} {PASS|STOP|SKIP} @{C7} — {one line}`   written by /pfm:release
  gate.log        the full gate's output, closed `EXIT {code} @{sha}`                       written by /pfm:release
  leak.txt        the leak scan's output                                                    written by /pfm:release
  preflight.txt   release-check preflight's output                                          written by /pfm:release
  scope.txt       release-check scope's output                                              written by /pfm:release
  pfm-help.txt    the cut's `pfm help`, for rule P1                                         written by /pfm:release
  v{NEW}.md       the draft note                                                            written by changelogger
  changelog.line  the `CHANGELOG.md` index line                                             written by changelogger
  coverage.tsv    one row per commit in the range                                           written by changelogger
  review.md       the pfm-update review, when one ran                                       written by reviewer-pro
```

- `{project}` is the repo directory's basename with any leading dot stripped. The directory outlives the release branch, so an audit reads it after the tag; each cut opens `run.md` with a `CUT @{C7}` line.
- The changelogger writes here, never into the release worktree, because the full gate reads that tree while the note is being written. The note and the index line move into the worktree at step 5, because they ship.

## The invariants

1. The gated commit is the published commit. The gate, the leak scan, the preflight, `ci.yml` and the review all run at `C`; the release commit may touch only `releases/v{NEW}.md`, `CHANGELOG.md`, `VERSION`, `.professor/VERSION` and `.professor/manifest.json`, which the main chat checks after the commit and `gitter` Phase RELEASE checks again before the push.
2. Every commit in the range is accounted for. The coverage ledger names, for each commit, the bullet it reached or why it reaches no adopter; the check fails on a missing or extra commit.
3. The judge is never the judged. `changelogger` writes the note; the script judges it; `## Verification` is written from the checks' own output.
4. Nothing is fixed inside a release. A check that fails stops the cut; the fix lands on `develop` through a flight.
5. An empty check is never a pass. Each preflight rule prints what it swept, and a check that could not run exits 2, never clean.

## Versioning

`VERSION` is the source of truth; tags are `vX.Y.Z`, made by the `release.yml` `publish` job on the release PR's merge commit, never pushed by hand, never moved. Between releases `develop` carries the next `X.Y.Z-alpha`.

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
| `docs/RELEASE.md` | adopters, `changelogger` | Versioning, the note grammar, what the release PR triggers, how to update |
| `.claude/commands/pfm/release.md` | the main chat | The cut, the checks, the release commit, publication, the close |
| `.claude/agents/changelogger.md` | `changelogger` | How to write for the updating adopter: grounding, routes, traps, coverage |
| `.claude/agents/gitter.md` Phase RELEASE | `gitter` | The release PR, the release commit's file check, the merge back into `develop` |
| `scripts/release-check.mjs` | everyone, by running it | The tier and area table, the grammar, coverage, the floors, the preflight |
| `.github/workflows/release.yml` | GitHub Actions | The PR gate, the tag, the published-asset check |
| `pfm/internal/picker/update_row.go` | the adopter's update chat | The update prompt |

A rule the script enforces is never restated as a prompt rule; the prompt names the check to run.

## Names

- The unit is a release; the family is `release`; the verb is `cut`; a directory is `$HOME/.local/state/pfm/releases/{project}/v{NEW}/`.
- The release branch is `release/v{NEW}`; its worktree is `.worktrees/release/`. `gitter` Phase RELEASE keeps its name: it is the git mechanics of publishing, one step inside the cut.
- `changelogger` is project-local (`.claude/agents/`): it writes for pfm's own update path, which no adopter has.

## Accepted risk

The release reviews in full only the `pfm-update` area, and only when the cut changes it: the update, install, init, doctor and config paths an adopter's update runs through. Everything else relies on the review it had when it landed on `develop`, on the full fenced gate and `ci.yml` at the cut, and on the preflight's seams sweep, which finds every removed path or role still named anywhere outside history. A defect inside an internal package that its own tests do not catch and its landing review missed can ship; a MEDIUM or LOW finding on the update path ships named in `## Verification`. No model follows the note before it ships, so a note that misleads the update chat is found by an adopter, and its fix is the next patch release.

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The command | `.claude/commands/pfm/release.md` | The cut and publication |
| The agent | `.claude/agents/changelogger.md` | The executable wording |
| The publish gate | `.claude/agents/gitter.md` Phase RELEASE and its machine-global twin `templates/global/agents/gitter.md` | The release PR, the file check, the merge back |
| The script | `scripts/release-check.mjs` and its tests | The mechanisms |
| The public spec | `docs/RELEASE.md`, `CHANGELOG.md` (index and a pointer), `INSTALL.md` § Updating, `docs/SETUP.md` § Staying current | What adopters read |
| The reader's prompt | `pfm/internal/picker/update_row.go` `professorUpdatePrompt` and its test | Timing, surfaces, stops |
| The workflows | `.github/workflows/ci.yml`, `.github/workflows/release.yml` | The cut's CI run; the PR gate, `publish` and `verify` |
| The mirrors and manifest | `.codex/`, `.opencode/`, `.professor/manifest.json` | The agent registered |

## Open items

- Change-time adopter fragments. The strongest measured defense against omission is a note fragment written with each change and checked in CI (Go, Kubernetes, towncrier, changesets). This family keeps release-time derivation, grounded in commit messages and closed by the coverage ledger; a commit-message trailer carrying the adopter action (`Adopter: {when} · {surface} — {action}`) would move the knowledge to the moment the author holds it.
- A deterministic range merge. `pfm update plan --to vX` could parse the action grammar across the skipped range and print the ordered checklist, replacing the update chat's hand merge (Renovate, the Angular update guide). It helps only adopters whose installed binary already carries it.
- Provenance. `release.yml` could attest the binaries (`actions/attest-build-provenance`) and `verify` check them with `gh attestation verify`; the build-from-source update path gains nothing from it, which is why it waits.

## Evidence

The research behind these rulings is `.professor/RR/release-notes-for-upgrading-adopters-2026-09-25.md`. The facts the design rests on:

- Omission is the measured failure of release notes, most of all for breaking changes (Wu et al., ICPC 2022); most notes list 6–26 % of the issues a release addressed (Abebe et al., EMSE 2016).
- LLM-written notes capture the what better than the why (arXiv 2404.14824); no study measures how often they invent entries. Grounding in authored commit messages and a coverage count answer both.
- Mature projects tie each upgrade action to a surface and to before or after the upgrade (Kubernetes Urgent Upgrade Notes, OpenStack reno's upgrade section, Mattermost, Elastic) and declare required stops (GitLab).
- Release-time review is weighted by risk, never one pass over the whole range (Linux linux-next, Mozilla uplift rules, Chromium merge review); inspection past about 300 LOC an hour or 90 minutes loses its defect yield (SmartBear/Cisco).
- Verify the published artifact before announcing it (GoReleaser `verify`, PEP 101); the update banner reads `/releases/latest`, which is the newest non-draft, non-prerelease release.
- In 0.x the left-most non-zero component is the breaking one (Cargo, uv, Ruff); 1.0 is a support commitment (Tokio, Terraform, Go 1).
