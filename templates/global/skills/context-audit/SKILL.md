---
name: context-audit
description: 'USER-ONLY — /context-audit [project dir] [--engine claude|codex|opencode|all] [--loop main|{agent}] [--days N]: every layer a loop receives before its first tool call, rebuilt from disk and reconciled with its last real session; cut lines, duplicates, never-used items. Returns a table ranked by tokens reclaimed, then applies your rulings.'
disable-model-invocation: true
---

# context-audit

Arguments: project dir defaults to the working directory, `--engine` to `claude` (`all` runs claude, codex and opencode in turn), `--loop` to `main` (an agent name audits that sub-agent), `--days` to 30. Every number in the report comes from this run's output; `/context` is a user-only command, so the audit never asks for it.

## 1 Assemble and measure

Run, per engine, the path written in full, after `mkdir -p /tmp/context-audit`:

`python3 -I $HOME/.claude/skills/context-audit/context-audit.py {project dir} --engine {engine} --loop {loop} --days {N} > /tmp/context-audit/{engine}-{loop}.tsv`

It prints tab-separated lines, each opened by its kind:

- `PROBE`: a file or setting looked for, `found` or `absent`.
- `LAYER`: kind, bytes, ~tokens, status, uses in the window, source, name. Status: `LOADED` (its text is in the recorded first request), `DIFFERS` (loaded, but the recorded text differs from disk: cut, or a copy cached before an edit), `CHANGED-SINCE` (file newer than that session), `NOT-LOADED`, `HIDDEN (visibility:)`, `SHADOWED`, `DUPLICATE NAME`, `UNVERIFIED` (the engine records no request text), `NOT-ON-WIRE` (a harness block absent from that request).
- `MEASURED`: the first-turn input tokens of the latest real session of this loop and the transcript it came from; bytes per token calibrated on that request where its text is recorded, else 4.0 assumed.
- `UNLISTED`: a recorded block over 500 bytes no listed layer accounts for. `UNRECORDED` (Codex): tokens the log never holds, the tool schemas.
- `GAP`: the untraced share of the measured tokens; over 10% the line says `FINDING`.
- `LIMIT`: where an engine stops reading: the file, the byte and line, the last text read, then every heading lost after it.
- `DUP`, `MIRROR`, `UNUSED`, `USAGE`: steps 3 and 4.
- `ERROR` or `WARN`: a probe that could not look; a nonzero exit or a traceback is an `ERROR` for the whole run.

What it rebuilds, in load order:

- Claude: managed `CLAUDE.md`, then `$CLAUDE_CONFIG_DIR` (default `~/.claude`) `CLAUDE.md` and its `rules/` without `paths:`, then each `CLAUDE.md`, `.claude/CLAUDE.md`, `CLAUDE.local.md` from `/` down to the project (`AGENTS.md` too when the `instructionFiles` plugin option says so), the project's `.claude/rules/` without `paths:`, auto memory `MEMORY.md` (first 200 lines or 25 KB); `@path` imports four hops deep, outside code spans; block HTML comments stripped. Then the skill, command and agent listings (plugins included; entries with no file on disk are named from the recorded listing), the deferred-tool and MCP blocks, the fleet prompt on the main loop, and for a sub-agent its own body and `skills:` preloads. Built-in `Explore` and `Plan`, and an agent with `omitClaudeMd: true`, get no `CLAUDE.md` chain. Sources: code.claude.com memory and sub-agents docs; the recorded first request (`api-request-shape`, `api-request-blob`) settles what loaded.
- Codex: `$CODEX_HOME/AGENTS.override.md` or `AGENTS.md`, outside the budget; then from the git root down to the working directory the first of `AGENTS.override.md`, `AGENTS.md` and `project_doc_fallback_filenames` per directory, within `project_doc_max_bytes` (default 32 KiB), the crossing file cut mid-file and later ones dropped; nothing from an untrusted project; then `developer_instructions` (the fleet prompt), the role file of a sub-agent, and the skill listing. Source: `codex-rs` `agents_md.rs`.
- OpenCode: the first of `~/.config/opencode/AGENTS.md` and `~/.claude/CLAUDE.md`, then the first of `AGENTS.md`, `CLAUDE.md`, `CONTEXT.md` found upward from the working directory to the worktree, then `opencode.json` `instructions`, then skills. Source: `instruction.ts` at the installed version (`opencode --version`). Unverified: OpenCode records no request text, so every layer reads `UNVERIFIED` and its gap holds the unrecorded base prompt and tool schemas.

## 2 Reconcile

A `GAP` line saying `FINDING` means a layer is missing from the list: open the `UNLISTED` block in the named transcript, say what it is and which file or setting puts it there. `DIFFERS` and `CHANGED-SINCE` rows mean the measured session predates the disk: name it.

## 3 Limits

Copy each `LIMIT` line verbatim. For every orientation file among the layers (`CLAUDE.md`, `AGENTS.md`), run the `/quality:claude-md` truth script and relay its prefixed lines verbatim, the per-chain budget included:

```bash
F=${CLAUDE_CONFIG_DIR:-$HOME/.claude}/commands/quality/claude-md.md
awk '/^## The truth script/{s=1} s&&/^```bash/{f=1;next} f&&/^```/{exit} f' "$F" > /tmp/context-audit/truth.sh
[ -s /tmp/context-audit/truth.sh ] || echo "ERROR: no truth script in $F"
bash /tmp/context-audit/truth.sh {orientation file}
```

## 4 Across layers

- `DUP`: one line stated in two layers; the more specific layer keeps it.
- Contradictions: read the `LOADED` instruction layers side by side and list every pair of directives that cannot both be followed, each quoted with its path.
- `MIRROR`: each generated mirror's bytes over its source.

## 5 Earned its place

`UNUSED` lists every loaded skill, command and agent with zero recorded uses in the window, ranked by tokens. `USAGE` names the store and the window: the callmeter store for Claude (`Skill` calls, typed `/command` expansions, `Agent` `subagent_type`), the rollouts for Codex, the OpenCode database for OpenCode. Instruction layers carry no use count; steps 3 and 4 judge them.

## 6 Report and rule

One table per engine and loop, ranked by tokens reclaimed: rank, item, ~tokens, evidence (status, uses in the window, the `LIMIT`, `DUP` or contradiction line), proposed lever. Levers:

- A skill or command never used: `skillOverrides` `"{name}": "off"` in settings, `disableBundledSkills` for the bundled set, or `disable-model-invocation: true` when the user still types it.
- An agent or skill one caller uses: `visibility:` scope to that caller.
- An item one project uses: move it from the user scope to that project's `.claude/`.
- An MCP or built-in tool never used: `permissions.deny`.
- A cut line, duplicate or contradiction: the prompt-file edit.

Ask the user one AskUserQuestion per table, every row an option carrying its evidence. Before applying any ruling, copy each file it touches to `~/.local/state/context-audit/backup/{timestamp}/` under its absolute path and print the restore command. Prompt-file rulings go to the `pcm` agent in one brief listing every ruled row; config levers are edited in place. Rerun step 1 and show the new numbers.

## Rules

- A step that prints `ERROR` or exits nonzero leaves its rows `not checked` in the report, the `ERROR` quoted; such a row is never filled from judgment or memory.
