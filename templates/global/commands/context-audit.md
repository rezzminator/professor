---
# professor: SOURCE TEMPLATE — edit here for a framework change (routes through /pcm); project-scaffold customization belongs in its installed local source; engine mirrors are never hand-edited.
name: context-audit
description: 'USER-ONLY — /context-audit [pasted /context output]: finds what loads into this chat but never earns its place here — skills, agents, MCP and built-in tools, system-prompt sections — proves each at its source, ranks it, applies your per-row ruling. Prompt-file size → /context-meter.'
argument-hint: '[pasted /context output]'
disable-model-invocation: true
---

# Context Audit

Find what this session loads but never uses here, prove each finding at its source, let the user rule per row, then apply the ruling.

## 1. Meter

`$ARGUMENTS` is the user's `/context` output; absent, ask for `/context` (or `/context all` for the full lists) and stop until it arrives — the model cannot run it. Working room = auto-compact window − autocompact buffer − baseline, every number read off that output, never recalled.

## 2. Candidates

Walk every listed item against this project and the prompts loaded here. A candidate is:

- a global skill or agent this project never uses — it belongs project-scoped
- a tool a loaded prompt forbids or reroutes in the loop that lists it (another tool named the only route, a mechanism banned)
- an MCP tool a loaded prompt confines to nested agents
- an agent whose description says it is never delegated to directly, or `{CALLER}-ONLY`, listed to the main chat
- several routes to one capability (more than one browser-automation skill)
- a system-prompt section for an integration this session does not load
- a description far past its `/quality:description` cap

A framework prompt file over its size limit is `/context-meter`'s finding, not a row here.

## 3. Proof at the source

Every row rests on checks you watched succeed:

- Config: the active config is `$CLAUDE_CONFIG_DIR` (else `~/.claude`) — its `.claude.json` and `settings.json`; `~/.claude.json` may not exist, and `settings.json` may be one file symlinked into every account.
- Origin: `readlink` every skill and agent — a symlink into a clone (edit at the clone), a real directory (hand-made or a third-party installer: list its other files), a plugin, a claude.ai sync, a bundled item.
- Generator: grep the owning repo for the description text before editing a file a generator rewrites.
- A check prints its failure on its own line; an errored check is reported as failed and its claim stays unproven — never a row built on it.
- Deferred: a tool listed by name behind ToolSearch costs its name until loaded; `/context` may still price its full schema — say which you observed.

## 4. Controls

- `permissions.deny`: an entry still in this session's tool list or deferred-tool notice fails silently — find why; a feature-flag-gated tool can ignore deny (`EndConversation`, Claude Code 2.1.296).
- `pfm doctor`: tool-visibility-controller installed and enabled; without it every `visibility:` frontmatter and scope file is inert.
- `skillOverrides` already set in `settings.json`.

## 5. Ruling

One table ranked by tokens — `# · what · ~tokens · why junk here · fix` — then one line per broken control. The user rules each row. Push back once where a ruling breaks a caller: an agent the main chat spawns through a command stays visible to `main`.

## 6. Levers

Back up each file before its edit, in the scratch directory.

- Remove a skill: move its directory to the OS trash after listing its orphans (agents, hooks, scripts, settings keys).
- One project only: move the skill, or its symlink, into `{project}/.claude/skills/`.
- Bundled or claude.ai-synced skill: `"skillOverrides": {"{name}": "off"}` in `settings.json`; set the bare and the `plugin:`-prefixed name until `/context` shows which matched.
- A tool no loop needs: `permissions.deny`.
- Hide per loop: tool-visibility-controller — `visibility:` frontmatter on an owned agent, skill or command; for the main chat, built-in tools, plugin items and MCP, the scope file `tool-visibility-controller.json`, kept in `~/.claude/` and linked into every account's `$CLAUDE_CONFIG_DIR`.
- Trim a description: a `.claude/**`, `CLAUDE.md` or `templates/**` file → the `pcm` agent; a description compiled from source code → `flights-foreman`; any other file → edit in place.
- An integration's system-prompt section: the integration's own toggle.
- A plugin to install: `pfm install --yes` skips plugins while any chat is live — the user runs it from a plain terminal with every chat closed.

## 7. Report

What landed, with each live effect observed; what waits and on what; the backup paths; the re-measure — `/context` in a new session against step 1's baseline.
