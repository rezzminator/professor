# engine-sync

engine-sync carries a project's Claude Code configuration to Codex and OpenCode. It is one Go binary and one Claude Code plugin in their own repository, `rezzminator/engine-sync`, mounted here as the submodule `plugins/engine-sync/`. The binary is the single writer of every Codex and OpenCode artifact derived from Claude sources; pfm execs it and never imports its packages.

**Status:** built; the latest release is v0.5.0.

## Contents

- [Layout](#layout)
- [Doors](#doors)
- [Config](#config)
- [Ownership](#ownership)
- [Vocabulary](#vocabulary)

## Layout

Packages and their responsibilities: `plugins/engine-sync/docs/architecture.md` § Package responsibilities. The verbs, flags, config keys and JSON contract: `plugins/engine-sync/docs/interface.md`.

## Doors

- **The binary:** `~/.local/bin/engine-sync`, a link `pfm install` writes to `~/.local/share/engine-sync/bin/engine-sync`, the stable link the plugin's wrapper keeps pointed at the resolved release binary. An existing entry at `~/.local/bin/engine-sync` that is not pfm's link stays untouched and `pfm doctor` warns.
- **Verbs:** `engine-sync codex build|check|agents`, `engine-sync opencode build|check|doctor`, `engine-sync global`, `engine-sync hook claude mark|stop|start`, `engine-sync doctor --machine`.
- **Hooks:** the plugin's `hooks/hooks.json` is the only hook door. `hook claude mark` marks a project's mirrors dirty after a write, `hook claude stop` builds and checks them when dirty, `hook claude start` repairs stale projects and refreshes the machine roster. No project script runs the compiler.
- **pfm:** `pfm install` runs `engine-sync global`, which writes the Codex roles and command mirrors and carries every always-on global rule (`{store}/rules`, no `paths:`) to Codex, as an owned block in each Codex home's `AGENTS.md`, and to OpenCode, as `~/.local/share/engine-sync/rules/global-always-on.md` registered in the `instructions` array of `~/.config/opencode/opencode.jsonc` (design: `docs/design/rules/rules.md` § Global always-on rules). The `codex` and `opencode` subcommands of `pfm` are retired: each prints one stderr line naming `engine-sync codex ...` or `engine-sync opencode ...` and `pfm doctor --project-updates`, and exits 2. There are no forwarders.

## Config

Two files hold engine-sync's policy: `~/.claude/engine-sync.json` (the global file, machine keys included) and `{project}/.claude/engine-sync.json` (project keys only). Precedence: built-in defaults < global file < project file < environment < flags. No environment variable moves the global path, `CLAUDE_CONFIG_DIR` included. `pfm install` writes the global file from `templates/global/engine-sync.json` plus the host's `targets` roster. Keys: `plugins/engine-sync/docs/interface.md`.

## Ownership

- A registry link is Professor's when its target lies inside a Professor root: the managed root, the source clone or `~/.professor` (`installer.ScanRegistryOwnership`).
- A regular file engine-sync writes into a machine registry carries `# managed-by: professor` after its generated header; the global file sets `machine.managedBy = "professor"` to make engine-sync write it.
- `pfm doctor` prints the `registry-ownership` rows for the five registries (`{store}/agents`, `{store}/commands`, `{store}/rules`, `{store}/skills`, `~/.agents/skills`): `STORE` (a link into the retired home store) and `ORPHAN` (an owned link the install no longer writes) fail, `DEAD` (an owned link whose target is gone) and `RETIRED` (an unmarked entry named for a retired agent, command or skill) warn, and a scan error prints `registry-ownership check failed`.
- `engine-sync doctor --machine` judges the regular files engine-sync itself writes (`INSTALLED`, `MISSING`, `STALE`, `ORPHAN`, `SHADOWED`, `SKIPPED`, `STRAY`), plus the `global-rules` rows: Codex block `MISSING`, `STALE` or `SHADOWED`, OpenCode file or `instructions` entry `MISSING` or `STALE`, `PATH-SCOPED`, `RULE-FRONTMATTER`, `GLOBAL-BUDGET`. `pfm doctor` prints its rows.

## Vocabulary

- **declaration:** a machine agent source carrying `from:`.
- **shared file:** an `.md` below a subdirectory of the source agents directory, layered through `extends`; never an agent.
- **generated agent:** the rendered Claude file a declaration produces.
- **render:** the verb for producing a generated agent.

Printed bytes keep their older spelling: `VARIANT-INVALID`, the `variant %q` error texts and the `variants pin` verb.
