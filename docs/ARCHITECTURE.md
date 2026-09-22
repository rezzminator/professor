# ARCHITECTURE — source, render, install, update

How Professor's files get from this repo onto a machine, who is allowed to write where, and how the same content serves two harnesses and any number of hosts without forking. `BLUEPRINT.md` is the philosophy; this is the mechanics. Sections marked **(designed)** describe the committed direction not yet fully built; everything else is shipping.

## Table of contents

- [The pipeline](#the-pipeline)
- [Ownership law — one writer per installed path](#ownership-law--one-writer-per-installed-path)
- [Stable addresses](#stable-addresses)
- [The harness axis — one source, per-harness renders](#the-harness-axis--one-source-per-harness-renders)
- [Values, not forks](#values-not-forks)
- [Tiers](#tiers)
- [Update flow](#update-flow)
- [Origin flow — the maintainer's repo is an adopter](#origin-flow--the-maintainers-repo-is-an-adopter)
- [Migration state](#migration-state)
- [Safety canon](#safety-canon)

## The pipeline

```
SOURCE   this repo — plain files where content is universal,
         templates where harness or project values genuinely differ;
         pfm/internal/installer/ owns the fleet installer and embedded assets
   │
RENDER   build step inside the repo; outputs are committed build
         artifacts, verified against their sources at release  (designed)
   │
INSTALL  pfm install — the ONLY writer of fleet-installed paths;
         stages embedded assets and links installed files to them
   │
UPDATE   pull, build, apply — every installed asset matches the binary
```

The clone supplies the binary source. A fresh box needs two steps: get the `pfm` binary, then run `pfm install --yes`. That stages the exact assets embedded in the binary and wires them into `~/.claude` by symlink. Updating means pulling, building one new candidate, and applying that candidate; the installed command surface cannot drift from the binary that installed it.

## Ownership law — one writer per installed path

**`pfm install` is the only program that writes a fleet-installed path.** Installed paths are `~/.claude/commands/**`, `~/.agents/skills/**`, user systemd unit links, managed settings entries, and the one fleet `source` line in `~/.zshrc`. Every generator, compiler, or sync script writes **only inside the repo or clone** — never into an install destination.

This law exists because its absence was measured, not imagined. Three writers once shared one command directory: the symlink installer, a template compiler emitting real files over the links, and a mirror builder re-rendering from whatever it found. Each was individually correct. Together they alternated ownership of the same paths, and one round of that restored stale files whose commands invoked scripts that no longer existed — a silent breakage, because a command file pointing at a dead path fails only when a user runs it.

Corollaries:

- A tool that wants to influence an installed file changes the **source in the repo**; the change reaches the install through build + `pfm install`, never by writing the destination.
- An installed file that is not a symlink into the managed asset tree is a finding. `pfm install --apply` reports and repairs it (backing up, never destroying).

## Stable addresses

The executable installs at **`~/.local/bin/pfm`**, and command cards reference that stable address. `pfm install` stages version-matched embedded assets under `~/.local/share/pfm/install/`; installed links point there, never at a transient checkout path.

## The harness axis — one source, per-harness renders

Professor targets two harnesses: Claude Code (`.md` commands, agents, skills) and Codex (prompts, skills, TOML). A pointer-stub mirror — "read the Claude file and execute it exactly" — is structurally dishonest: measured on a live monorepo, roughly half the mirrored command files contained Claude-only vocabulary (`Agent(...)` dispatch, tool names, model ids) that the second harness cannot execute, so it improvises. Drift by construction, not by discipline failure.

The design **(designed)**: harness divergence lives **in the template, at the exact line the harnesses differ** — `{{#if claude}} / {{#if codex}}` conditionals and shared partials — and the build renders one artifact per harness. Consequences:

- **Renders are committed.** The repo carries both the templates and their rendered outputs, so adopters install with git alone — no Node toolchain required at install time.
- **A release gate proves `rendered == build(source)`.** A hand-edit to a rendered file fails the gate; the fix is editing the template. (Same discipline as any bundled build artifact.)
- **Semantic divergence is authored, not translated.** Where the harnesses need genuinely different behavior — e.g. a close-this-chat command whose engine differs per harness — the template's two branches say so explicitly, instead of a generator naively translating one harness's file for the other.

## Values, not forks

Anything host- or project-specific — account rosters, project rosters, model names — is a **value**, supplied at install time from the setup interview and stored in a values file the adopter owns. Templates consume values; no file is ever copied and locally edited into a divergent fork **(designed — today the `pfm` launcher surface ships with a fixed two-account roster)**.

A render manifest records the hash of every rendered artifact. An artifact whose on-disk hash matches neither its manifest entry nor a fresh render was hand-edited: the build reports it and refuses to clobber, and the fix moves the edit into the template or the values file.

## Tiers

| Tier | Contents | Install mode | Values needed |
| --------------- | -------------------------------------------------------------- | ---------------------------------- | --------------------- |
| Host executable | `pfm` | built binary | runtime config only |
| Host assets | `pfm/internal/installer/assets/` | embedded, staged, then symlinked | none |
| Repo files | per-project testing manuals, project agents, Codex TOMLs, child CLAUDE.md | rendered from the adopter's values | roster, stack, models |

The host tiers are value-free by construction — proven by inspection: their templates contain only harness conditionals, no host variables. That is what makes committed renders possible for them. The repo tier genuinely depends on the adopter's roster, so it renders on the adopter's machine at setup and update. Which rows are shipping versus designed is the [Migration state](#migration-state) table's job — this one describes the target shape.

## Update flow

```bash
git -C ~/.professor pull        # the update
go -C ~/.professor/pfm build -o ~/.local/bin/pfm ./cmd/pfm
pfm install --yes             # idempotent; applies the exact binary assets
```

`pfm install` is idempotent and honest: dry-run by default, `--apply` to act, `--uninstall` to reverse. A real file at a destination is backed up beside the link (`.pre-professor-<timestamp>`), never destroyed. The `~/.zshrc` source line is rewritten in place when the clone moves — never appended beside an old one, so the shell never sources two copies. It targets `$HOME/.claude` regardless of `$CLAUDE_CONFIG_DIR`, because the bundle is host-level and an install launched from inside a running chat must not land in that chat's account dir (`--config-dir` is the deliberate override).

## Origin flow — the maintainer's repo is an adopter

The maintainer's own monorepo — where the pipeline runs daily and improvements are born — holds no special copy of anything **(designed; today it is the origin the blueprint is derived from)**. The terminal state:

- **Framework templates are authored in `templates/`; fleet assets are authored in `pfm/internal/installer/assets/`.** The binary embeds the latter; machines consume both through their owning install paths rather than a duplicate host bundle.
- **`refresh-map.json` retires file-by-file.** Its `curated` class (template IS the source, no live file to derive from) is the terminal state for every entry; the map is a migration ledger, and an empty map ends the live→template derivation era.
- **The release protocol shrinks** to: version, changelog, tag, push. The genericize/refresh pass exists only for files still in the derivation era.

Until then, both flows run side by side: derived templates refresh from the live source under the hash map; curated templates are edited here directly.

## Migration state

| Surface | Today | End state |
| ------------------------------------ | ----------------------------------------------- | --------------------------------------- |
| Host executable | shipped — self-installing Go binary | unchanged |
| Host assets (Claude and Codex) | embedded and symlinked by `pfm install` | unchanged |
| Compiler (`build.mjs` + contexts) | archived in the origin repo's history | `templates/compiler/`, release-gated |
| Repo tier (agents, TOMLs, CLAUDE.md) | setup-interview instantiation | rendered from the adopter's values file |
| `refresh-map.json` | mixed derived + curated entries | empty — every template source-authored |

## Safety canon

Rules earned by incident, enforced mechanically where possible:

- **Dry-run by default** for anything that rewrites files outside the repo; acting requires the explicit flag.
- **Backup, never destroy** — a real file in a link's way is moved aside with a timestamped suffix and reported.
- **One source line** — config-file edits rewrite the existing line in place; appending a second copy means two sourced fleets and the later one silently wins.
- **The leak gate matches case-insensitively** and is checked against **staged content** (or a committed range), not the worktree — a worktree scan over staged names can bless a leaking index.
- **No private URLs in public commit messages** — commit messages never pass through the leak gate, so trailers carrying session or machine URLs must be stripped at authoring time.
- **Tags are immutable** and annotated; a release that needs correcting gets a new version.
