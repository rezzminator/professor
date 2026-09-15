# RELEASE — How the blueprint ships and how adopters pull it

Two mechanisms ship on every tag: the portable blueprint tree (this repo, at `templates/`) and the compiled `pfm` CLI binaries (built from `pfm/`, attached to the GitHub Release). Both are versioned by the same git tag.

---

## Contents

- [Versioning](#versioning)
- [Release notes layout](#release-notes-layout)
- [Cutting a release](#cutting-a-release)
- [What the tag push triggers](#what-the-tag-push-triggers)
- [Pulling an update](#pulling-an-update-adopter)

---

## Versioning

[Semantic Versioning](https://semver.org/), one `VERSION` file at the repo root as the source of truth, one annotated git tag `v{MAJOR}.{MINOR}.{PATCH}` per release. Tags are immutable — never deleted or moved after push. Between releases `develop` carries the next development version, `{MAJOR}.{MINOR+1}.0-alpha`: the release commit drops the suffix, and the release's close opens the next `-alpha` line, so a build from `develop` never reports itself as the release it follows. Tags never carry a suffix.

| Bump | When | Adopter impact |
| --- | --- | --- |
| **PATCH** | Bug fixes, doc tweaks, non-interface mechanic changes | Review reported project-template diffs; machine-global links update with the clone |
| **MINOR** | New Tier B archetype, new mechanics command, new pipeline step | Review reported changes and adopt optional project files explicitly |
| **MAJOR** | Breaking rename, removed command, changed core convention | Full manual migration walkthrough; no silent project-file writes |

Magnitude for a multi-version update is the **largest single-release bump in the chain**, never the endpoint semver diff alone — one major release anywhere in the walked range makes the whole update major.

## Release notes layout

Per-version notes live in `releases/v{X.Y.Z}.md`, one file per version, each titled `# v{X.Y.Z} — {YYYY-MM-DD}` with bullets grouped under `## Added/Changed/Fixed/Removed/Breaking/Migration`. `CHANGELOG.md` is a **slim index only** — one line per release (`- [v{X.Y.Z}](releases/v{X.Y.Z}.md) — {summary}`), prepended on every release. Never write full notes into `CHANGELOG.md` itself.

Bullets carry a category prefix and optional trailing tags, both read at update time:

- Prefix → `Tier A:` / `Tier B:` / `Mechanics:` / `Docs:` / `Scripts:`
- Trailing tag → `(safe-auto)`, `(breaking)`, `(opt-in)`, `(cost)` (env var/hook/permission/model-config changes — always routed to manual review regardless of prefix)

## Cutting a release

Maintainer command: `/pfm:release {patch|minor|major} "{summary}" [--from {live-root}]`, run in this repo (the upstream itself) only on an explicit publish request. The command file (`.claude/commands/pfm/release.md`) is the procedure; its phases:

1. **Pre-flight** — owner auth, the `main-release-only` ruleset intact (pull request, green checks, no force-push, no deletion), `develop` fast-forwarded and containing `origin/main`.
2. **Two worktrees** — `.worktrees/release/main` detached at `origin/main` (stable) and `.worktrees/release/develop` on `release/v{X.Y.Z}` (candidate). All release work lands in the candidate; the live checkout is never swept.
3. **Scope** — the release's scope is `develop`'s diff and commit log against `main`; reviewers write the notes and adopter instructions from it; with `--from`, the refresh pass re-derives `templates/**` from the live source per `docs/commands/pcm/references/refresh.md`.
4. **Review** — reviewers read `origin/main...HEAD` per area and return defects plus bullets for un-ledgered changes; every defect is verified against the code, then fixed and committed on the candidate.
5. **Notes** — `releases/v{X.Y.Z}.md` from the ledger bullets (verbatim) and the reviewers' bullets, the `CHANGELOG.md` index line, `VERSION`, the self-hosted install ledger.
6. **Gates** — both worktrees run `dev.sh iso all` per project and `dev.sh iso e2e`; a stable red is inherited, a candidate red is fixed.
7. **Rehearsal** — a Codex model installs the stable release on a fenced adopter machine exactly as the stable docs say, then updates it to the candidate exactly as the candidate's docs say (`infra/release-rehearsal.sh`, `docs/commands/pfm/references/release-rehearsal.md`); every friction is fixed, the machine reverted to its stable snapshot, and the update re-run until CLEAN.
8. **Ship** — `develop` fast-forwards onto the candidate, `leak-check.sh` runs clean, `develop` is pushed, and the `develop → main` pull request merges on green checks; the annotated tag lands on the merge commit and `develop` fast-forwards back onto `main`. `develop` then opens the next `-alpha` line.

**Never:** push secrets or project identifiers (current/former brand, PII, internal URLs, machine-absolute home paths), force-push, push to `main` directly, ship a Tier A character with empty placeholders, publish a candidate whose rehearsal did not end CLEAN, or auto-bump the README version without re-checking the templates it describes.

## What the tag push triggers

The workflow is `.github/workflows/release.yml`.

A `v*` tag push (or manual `workflow_dispatch`) runs on GitHub Actions:

1. **Build** — `pfm` for `linux/amd64`, `linux/arm64`, `darwin/arm64`, `darwin/amd64` (Go, `-trimpath`, `CGO_ENABLED=0`); each platform binary uploads as its own workflow artifact.
2. **Assemble** — downloads every platform binary, writes `SHA256SUMS`.
3. **Require authored notes** — fails the run if `releases/{tag}.md` doesn't exist; a tag with no hand-written release file cannot publish.
4. **Publish the GitHub Release** — attaches the `pfm_*` binaries + `SHA256SUMS`, with `releases/{tag}.md` as the release body verbatim (`generate_release_notes: false` — no auto-summary, the authored file is the only source of truth for the release body).

The authored-notes gate is why phase 5 above (write `releases/v{X.Y.Z}.md` *before* tagging) is not optional — the tag push fails release assembly without it.

---

## Pulling an update (adopter)

State lives in `.professor/` inside the adopter's project: `VERSION` (installed version), `manifest.json` (user-owned interview record), `baseline.json` (pfm-owned local-to-template pins), and `drift.md` (optional local customization notes).

First read every `releases/vX.Y.Z.md` between the installed version and the target and merge their `#### → For:` actions (`INSTALL.md` § Updating). Then run `pfm update` to advance the tagged clone, rebuild the binary, refresh machine-global links, and append the current project's report. Run `pfm update check` when only the read-only project report is wanted. For every `UPDATED` item, inspect the printed template diff, hand-apply what belongs in the local source, then run `pfm update pin <local>`. `NEW`, `GONE-UPSTREAM`, and `LOCAL-DELETED` print their own `pin --template` or `drop` actions. An install that predates `pfm init` runs `pfm update adopt [--at <ref>]` once to create its baseline; `pfm update ignore <template>...` keeps an unwanted template out of `NEW`. Generated engine mirrors remain whole-file compiler outputs; symlinked machine-global files update through their blueprint original. Source-fetched skills (`templates/project/skills/sources.json`) update from their own repos — compare the installed `version:` frontmatter against the skill repo's latest tag; never downgrade. Project files are never regenerated or merged during update.
