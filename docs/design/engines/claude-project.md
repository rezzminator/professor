# Claude project tier

The Claude Code settings a project carries in its own tree: this repo's `.claude/settings.json`, the template an adopter is scaffolded from, the shipped `settings-global.json` template, the gate that keeps them honest, and the settings an adopter is told to add by hand. pfm never writes a project settings file after `pfm init` scaffolds it; machine-global writes are in [claude-config-dir.md](claude-config-dir.md).

## Contents

- [Decisions](#decisions)
- [This repo](#this-repo)
- [The template](#the-template)
- [Differences](#differences)
- [settings-global.json](#settings-globaljson)
- [The outputStyle gate](#the-outputstyle-gate)
- [Manual adopter steps](#manual-adopter-steps)
- [Discrepancies](#discrepancies)

## Decisions

- **The adopter owns the file.** `pfm init` scaffolds `templates/project/settings.json` into `.claude/settings.json` once and pins it in `.professor/baseline.json`; `pfm doctor --project-updates` reports upstream changes for the adopter's session to hand-apply, then `pfm update {adopt|pin|ignore|drop}` manages the pins. The `"project/settings.json"` row in `templates/refresh-map.json` maps the template to its live source for `/pfm:release --from`.
- **Project settings carry permissions and project hooks only.** No MCP server ships with Professor; a personal server is the adopter's own registration. Output style, status line and machine hooks ride each launch's `--settings` ([claude-launch.md](claude-launch.md#how-claude-layers-its-settings)).
- **Hooks run project scripts.** Every project hook command is `$CLAUDE_PROJECT_DIR/.claude/scripts/…`; the six are inventoried in `docs/design/hooks/hooks.md`.

## This repo

`.claude/settings.json` — two keys:

- **`permissions.allow`** (no `deny`): the repo gates `Bash(.claude/scripts/dev.sh:*)`, `Bash(scripts/leak-check.sh:*)`, `Bash(scripts/refresh-scope.sh:*)`; `Bash(pfm codex:*)`; read-only git (`status`, `diff`, `log`, `show`, `rev-parse`); `go vet`, `go build`, `go test`; `Read(*)`, `Grep(*)`, `Glob(*)` (`.claude/settings.json:2-18`).
- **`hooks`**: `PreToolUse`, `PostToolUse`, `Stop` — four matcher groups, six commands (`:21`).

`.claude/settings.local.json` is untracked and holds `disabledMcpjsonServers: ["playwright"]`.

## The template

`templates/project/settings.json` — two keys, `permissions` and `hooks`. Agent teams are switched off by every pfm launch ([claude-launch.md](claude-launch.md#knobs)), so no project file carries the variable.

- **`permissions.allow`** (no `deny`): `Bash(*)`, `WebFetch(*)`, `WebSearch(*)`, `Workflow(*)`, `Read(*)`, `Write(*)` (`templates/project/settings.json:2-10`).
- **`hooks`**: identical to this repo's (`templates/project/settings.json:12`).

## Differences

| Key | This repo | Template |
| --- | --- | --- |
| `permissions.allow` | narrow: named scripts, read-only git, Go, read tools | wildcards: every Bash, web, Workflow, Write |
| `hooks` | 6 | the same 6 |

## settings-global.json

`templates/project/settings-global.json` ships `{"cleanupPeriodDays": 36500}`, but nothing installs that template. `cleanupPeriodDays` reaches Claude through `managed-settings.d/pfm.json`, which `pfm install` writes ([claude-config-dir.md](claude-config-dir.md#managed-settings)), and each launch's `--settings` ([claude-launch.md](claude-launch.md#how-claude-layers-its-settings)).

## The outputStyle gate

`infra/check-self-hosted-manifest.sh:247-265` (run by `.claude/scripts/check-templates.sh` and `.claude/scripts/dev.sh iso test templates`) fails when:

- `.claude/output-styles/` is tracked or exists on disk;
- the manifest advertises `installed.output_styles`;
- `.claude/settings.json`, `templates/project/settings.json` or `templates/project/settings-global.json` has an `outputStyle` key.

A style file would be inert under the per-launch `outputStyle: default` pin, so the gate is what surfaces one.

## Manual adopter steps

What `docs/SETUP.md` tells an adopter to add by hand — everything else is `pfm init` or `pfm install`:

- A `PostToolUse` `Edit|Write` hook running the project's `.claude/scripts/format-md.sh` (`docs/SETUP.md:256-278`).
- The memory scripts in `~/.claude/scripts/` with `SessionStart` `memory-wire.sh` and `SessionEnd` `memory-sync.sh` hooks in `~/.claude/settings.json` (`docs/SETUP.md:408-437`).

`INSTALL.md:29` states that pfm does not install the Claude or Codex CLI and their self-doctors are optional diagnostics; `pfm install --config-dir DIR` is the only supported override of the target dir (`INSTALL.md:222`).

## Discrepancies

- **The format hook is manual and templated at once.** `docs/SETUP.md:256-278` asks for the `format-md.sh` hook by hand, with an absolute path; the template's `PostToolUse` group already carries project hooks through `$CLAUDE_PROJECT_DIR`.
