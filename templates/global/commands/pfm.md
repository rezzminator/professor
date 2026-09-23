---
name: pfm
description: Operates the fleet CLI — `pfm` verb map, the adopter update flow (`pfm update check|adopt|pin|ignore|drop`), `pfm codex build|check`, `pfm install --yes`, `pfm doctor`, chat/harvest MCP-vs-shell routing, config location; load for any `pfm` verb or question. Not for editing framework files → /pcm.
argument-hint: [verb|question]
---

# pfm — the fleet CLI

$ARGUMENTS

`pfm` is the host binary Professor ships: it manages the chat fleet, the memory organs, the harvester, the host integration, and this project's template baseline. This file is the map; the flags of any verb are `pfm <verb> --help` — never invent one. Template store: `{BLUEPRINT_CLONE_PATH}` (default `~/.professor`; `.professor/manifest.json` can point elsewhere).

## Verb map

Operator verbs:

- ls: list or pick fleet chats (`--killed` for the graveyard)
- chat: operate on one chat — new, open, status, last, read, stream, inject, self-compact, ask, watch, capture, keys, recover, name, kill, unkill, end, reload, find, save, branch, history, resolve
- headless: run Claude or Codex through one isolated process interface (`pfm headless exec`)
- harvest: fetch and convert a URL, DOI, ISBN, PMID, PMCID, or local path to markdown
- index: refresh the transcript index
- whoami: this chat's own tmux session name
- issues: servicedesk complaints filed through `issue_servicedesk`
- reap: classify the socket graveyard; `--apply` reclaims it
- archive: move killed chats and old subagent transcripts out of sight, reversibly
- heal: report or repair wedged Codex history projections
- install: wire the self-contained host integration (`--yes` non-interactive)
- uninstall: remove it
- update: bare form updates the binary from its source clone; `check|adopt|pin|ignore|drop` manage this project's template baseline
- init: scaffold project templates once and pin their baselines (`pfm init [dir] [--force]`)
- config: `init | show | validate` machine configuration
- doctor: fleet database and jail health — exit 0 clean, 1 warnings, 3 failures
- version: print the pfm version

Wiring verbs (hooks and services call these; you rarely type them): name-sync, statusline, usage-hook, mcp (`ls | serve | <server> enable|disable|serve`), codex.

## Adopter update flow

The blueprint never rewrites a project file after `pfm init`; every upstream change is hand-applied.

1. `pfm update check` — reports each pinned file as `current`, `ignored`, `UPDATED`, `NEW`, `GONE-UPSTREAM`, or `LOCAL-DELETED`, with the exact `git diff` command to read per item; it writes nothing. `FAILED — .professor/baseline.json not found` means the install predates scaffolding: run `pfm update adopt [--at REF]` once, then re-check.
2. Read each printed diff. Decide per file what belongs locally.
3. Hand-apply what belongs through `/pcm` (the guarded framework-edit flow).
4. Advance the pin: `pfm update pin <local>...` (or `--all`); a `NEW` template you adopt: `pfm update pin --template <template> <local>`; one you will never take: `pfm update ignore <template>...` (`--undo` reverses); a `GONE-UPSTREAM` or `LOCAL-DELETED` file you keep or forget: `pfm update drop <local>...`.

`pfm update` (bare) advances the source clone to the latest release tag (or `--to vX.Y.Z`), rebuilds and installs the binary, runs `pfm doctor` (rolling back on failure), then prints this project's `update check` report — start step 1 from there.

## Codex mirror

`pfm codex build [repo-root]` is the SINGLE writer of `AGENTS.md`, `.codex/` and `$HOME/.codex/`; `pfm codex check` gates drift (non-zero names each MISSING, STALE, ORPHANed, or CONFLICTing artifact). The `codex-sync.sh` hooks run both after any Edit/Write to a Claude source; a Bash-driven write bypasses the hook, so run `pfm codex build . && pfm codex check .` yourself. `pfm codex agents` compiles the machine-global agent `.toml` twins.

## Host integration

`pfm install --yes` stages the host assets and symlinks the machine-global agents, commands and skills from `{BLUEPRINT_CLONE_PATH}`; saving a global template IS the deploy. `pfm doctor [--verbose]` is the health check to run after an install or update, and the first thing to run when a fleet verb misbehaves.

## Chat: MCP first, shell for the rest

Inside a chat, the `chat_*` MCP verbs are the preferred surface for inject, ask, read, ls, status, new, and self-compact (`chat_inject`, `chat_ask`, `chat_read`, `chat_ls`, `chat_status`, `chat_new`, `chat_self_compact`). `end`, `modal`, `watch`, `stream`, `recover`, and `history` are shell-only `pfm chat` commands. `pfm chat inject` refuses `/compact` — compaction is `self-compact`. Exit codes: 0 done · 2 usage · 3 chat dead · 4 no such chat · 5 answer timed out · 6 message not delivered.

## Harvest: MCP first, CLI for batches

Inside a chat, the `harvester` MCP tools (`readPage`, `parseLocalDocuments` (local server only), `download`, `findWorks`, `readWork`, `webSearch` (only when a search backend is configured)) answer per item; `pfm harvest [--refresh] [--size-only] [--json] [--header 'Name: value'] <url|doi|path>...` and `pfm harvest download <url>...` are the shell surface for batch or scripted fetches and for a chat without the MCP.

## Config

`pfm config show` prints the config path (`~/.config/pfm/pfm.config.json` by default; `pfm --config PATH` overrides) and every effective key with its source (file or default) — accounts, `claude.systemPrompt` (`production | lean | professor`), `claude.permissionMode`, `mcp.servers.*`, `harvester.enabled`, `ask.*`. `pfm config validate` checks the file; `pfm config init [--force]` writes a fresh one.
