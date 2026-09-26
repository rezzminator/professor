# RELEASE — How the blueprint ships and how adopters pull it

Two things ship on every tag: the blueprint tree (this repo) and the `pfm` binaries built from `pfm/` and attached to the GitHub Release. One git tag versions both.

---

## Contents

- [Versioning](#versioning)
- [Release notes](#release-notes)
- [Cutting a release](#cutting-a-release)
- [What the tag push triggers](#what-the-tag-push-triggers)
- [Pulling an update (adopter)](#pulling-an-update-adopter)

---

## Versioning

[Semantic Versioning](https://semver.org/), one `VERSION` file at the repo root as the source of truth, one annotated tag `v{MAJOR}.{MINOR}.{PATCH}` per release. Tags are immutable — never deleted or moved after push. Between releases `develop` carries the next development version, `{MAJOR}.{MINOR+1}.0-alpha`; the release commit drops the suffix, and the release's close opens the next `-alpha` line, so a build from `develop` never reports itself as the release it follows. Tags never carry a suffix.

The bump has a floor set by the sections the release's note holds. While the major is 0, the minor is the breaking component, as in Cargo:

| The note holds | Floor at 0.x | Floor at ≥ 1.0 |
| --- | --- | --- |
| `## Breaking`, `## Migration` or `## Removed` | minor | major |
| `## Added` | minor | minor |
| only `## Changed`, `## Fixed` | patch | patch |

`1.0.0` is a declared support commitment, never inferred from a diff. The magnitude of a multi-version update is the largest single-release bump in the chain, never the endpoint difference alone.

## Release notes

One file per version, `releases/v{X.Y.Z}.md`, written for the adopter who updates onto it — usually their update chat, which reads every note after its installed version oldest first and follows the actions literally. `CHANGELOG.md` is an index only: one line per release, `- [v{X.Y.Z}](releases/v{X.Y.Z}.md) — {summary}`, prepended under `## Releases`. From `v0.78.0` on, every note passes `node scripts/release-check.mjs notes releases/v{X.Y.Z}.md`, which enforces the grammar below line by line.

### The file, top to bottom

1. The title: `# v{X.Y.Z} — {YYYY-MM-DD}`.
2. The lead: two to five sentences on what this release means for an adopter, the breaking change first when there is one.
3. Required stops, when any: `#### → Stop: {reason}`. An adopter below this version updates to exactly this version first, finishes its actions, then continues.
4. The sections, each optional, none empty, in this order: `## Breaking`, `## Migration`, `## Added`, `## Changed`, `## Fixed`, `## Removed`, `## Verification`. No other heading appears.

### Bullets

Every line under a category section is a bullet or an action line. A bullet is one line:

```text
- {Label}: {scope} — {what changed, as the adopter sees it}
```

The label is the route that delivers the change, from a closed set:

| Label | Paths | Reaches the adopter |
| --- | --- | --- |
| `Global` | `templates/global/**`, `workflows/**` | when the source clone moves; machine-global files are linked live, Codex roles recompile at `pfm install` |
| `Project` | `templates/project/**` | only by hand, through `pfm update check` in each project; the bullet names the template path |
| `pfm` | `pfm/**` | with the binary `pfm update` rebuilds and installs |
| `Repo` | everything else | never at runtime; informational |

A bullet may end with `(opt-in)` for an optional addition or `(cost)` for an environment, hook, permission or model-configuration change that costs the adopter something.

### Actions

What a change asks of an adopter is one action line directly under its bullet, one line per step:

```text
#### → For: {audience} · {timing} · {surface} — {action}
```

- `{audience}`: who acts — `every adopter`, `Codex users`, `adopters who customized {template}`.
- `{timing}`: exactly one of `before update` (in the source clone, before `pfm update --to`), `after update` (on the machine, after `pfm update` and `pfm doctor`), `per project` (in each adopted project, alongside `pfm update check`).
- `{surface}`: the one thing the action touches — a path, a config key, a command, a template. A later release's action on the same surface supersedes an earlier one.
- `{action}`: imperative and safe to re-run, ending with the command whose output shows it done when one exists.

Example, with an invented key:

```text
- pfm: config — the `example.legacyKey` setting is retired; its behavior is now the default.
#### → For: every adopter · after update · pfm config file — delete the `example.legacyKey` line, then `pfm config validate` exits 0.
```

Notes before `v0.78.0` wrote actions as `#### → For:`, `#### → For adopters …:` or `#### For:` without a timing; the update prompt reads all three and has the adopter's chat judge the timing.

### Verification

`## Verification` closes the note: how the release was proven — reviews, gates, the rehearsal — as facts and numbers. It holds prose only, never a bullet or an action.

## Cutting a release

Maintainer command, run in this repo only:

- `/pfm:release prepare {patch|minor|major} "{summary}" [--from {live-root}]` — reviews the range past `main`, fixes what the review finds, writes the note, gates the candidate in the fence and rehearses the update on fenced adopter machines, until the candidate is stamped `READY`. It publishes nothing.
- `/pfm:release publish v{X.Y.Z}` — only on the maintainer's explicit ask: lands exactly the commit `READY` names on `develop`, pushes, merges the `develop → main` pull request on green checks, tags, and verifies what GitHub serves.

The procedure is `.claude/commands/pfm/release.md`; the design and the reasons are `docs/design/release/`.

## What the tag push triggers

The workflow is `.github/workflows/release.yml`. A `v*` tag push (or a manual `workflow_dispatch`) runs on GitHub Actions:

1. **Build** — `pfm` for `linux/amd64`, `linux/arm64`, `darwin/arm64`, `darwin/amd64` (Go, `-trimpath`, `CGO_ENABLED=0`); each platform binary uploads as its own workflow artifact.
2. **Assemble** — downloads every platform binary, writes `SHA256SUMS`.
3. **Require authored notes** — fails the run if `releases/{tag}.md` does not exist, then runs `release-check notes` on it.
4. **Publish the GitHub Release** — attaches the `pfm_*` binaries and `SHA256SUMS`, with `releases/{tag}.md` as the release body verbatim (`generate_release_notes: false`).

## Pulling an update (adopter)

State lives in `.professor/` inside the adopter's project: `VERSION` (installed version), `manifest.json` (the interview record), and `baseline.json` (pfm's local-to-template pins).

The update chat `pfm ls` opens reads every note between the installed version and the target, stops at each required stop, and merges the actions into one checklist by timing. Then `pfm update --to v{X.Y.Z}` advances the tagged clone, rebuilds the binary, refreshes machine-global links, and runs `pfm doctor`, rolling back when the doctor fails anew. The `after update` actions follow; then, in each project, `pfm update check` reports every template change — hand-apply each `UPDATED` diff and run `pfm update pin <local>` — and the `per project` actions finish the update. See `INSTALL.md` § Updating and `docs/SETUP.md` § Staying current.
