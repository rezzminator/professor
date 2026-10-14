---
name: pfm
description: Operates the fleet CLI — `pfm` verb map, the adopter update flow (`pfm doctor --project-updates`, `pfm update adopt|pin|ignore|drop|accept`), `pfm codex build|check`, `pfm install --yes`, `pfm doctor`, chat/harvest MCP-vs-shell routing, config location; load for any `pfm` verb or question. Not for editing framework files → the `pcm` agent.
argument-hint: [verb|question]
---

# pfm — the fleet CLI

$ARGUMENTS

`pfm` is the host binary Professor ships: it manages the chat fleet, the memory organs, the harvester, the host integration, and this project's template baseline. This file is the map; the flags of any verb are `pfm <verb> --help` — never invent one. Template store: `{BLUEPRINT_CLONE_PATH}` (default `~/.professor`; `.professor/manifest.json` can point elsewhere).

For creating, adopting, modifying or checking a workbench, load `/pfm:workbench`.

## Verb map

Operator verbs:

- ls: list or pick fleet chats (`--killed` for the graveyard)
- chat: operate on one chat — new, open, status, last, read, stream, call, ask, watch, capture, keys, recover, name, kill, unkill, end, reload, find, save, branch, history, resolve, schedule
- headless: run Claude or Codex through one isolated process interface (`pfm headless exec`)
- harvest: fetch and convert a URL, DOI, ISBN, PMID, PMCID, or local path to markdown
- index: refresh the transcript index
- whoami: this chat's own tmux session name
- issues: servicedesk complaints filed through `servicedesk`
- reap: classify the socket graveyard; `--apply` reclaims it
- archive: move killed chats and old subagent transcripts out of sight, reversibly
- heal: report or repair wedged Codex history projections
- install: wire the self-contained host integration (`--yes` non-interactive)
- uninstall: remove it
- update: bare form updates the binary from its source clone; `adopt|pin|ignore|drop|accept` manage this project's template baseline (`pfm doctor --project-updates` reports it)
- init: scaffold project templates once and pin their baselines (`pfm init [dir] [--force]`); `pfm init --render [dir]` fills install-time tokens once from `.professor/manifest.json`
- config: `init | show | validate` machine configuration
- doctor: fleet database and jail health — exit 0 machine checks clean (`project_updates` rows advisory), 1 warnings, 3 failures
- version: print the pfm version

Wiring verbs (hooks and services call these; you rarely type them): name-sync, statusline, usage-hook, mcp (`ls | serve | <server> enable|disable|serve`), codex.

## Adopter update flow

The blueprint never rewrites a project file after `pfm init`; every upstream change is hand-applied.

1. `pfm doctor --project-updates` — counts `current` pins and `ignored` templates, reports each `UPDATED`, `NEW`, `GONE-UPSTREAM`, `MOVED` or `LOCAL-DELETED` row, and prints under each `UPDATED` row the upstream commits and diff since its pin. `KEPT` (a pin kept apart on purpose) and `ACCEPTED` (retired-name hits accepted by a recorded rule) are decisions on record, never review. Its `RETIRED-NAME` section lists retired references in project files, including CURRENT pins; non-CURRENT pins are covered by their report rows, and the baseline ledger and generated engine mirrors are excluded; a name the project defines itself (its own command, agent or skill file) and a match inside a URL are not hits. `UNREADABLE` names scan failures. Every readable text file is scanned in full; a line over 4 MiB ends that file's scan with one `INCOMPLETE` coverage row, prefix hits retained; that gap alone never exits 3, and a report with only such gaps ends INCOMPLETE at exit 0, not clean. It writes nothing: exit 0 for clean or incomplete coverage, 1 for review, 3 for failure. `FAILED — .professor/baseline.json not found` means the install predates scaffolding: run `pfm update adopt [--at REF]` once, then re-run the report.
2. Read each printed diff and `RETIRED-NAME` hit, and decide per file what applies locally; install-time values are never read on update. A pin many releases back is ported commit by commit from the row's commit list (`git -C <blueprint> show <commit> -- templates/<template>`), never by applying the whole diff as a patch.
3. Port what applies through the `pcm` agent, keeping the project's own edits.
4. Close every row with a verb: `pfm update pin <local>...` (or `--all`) once ported; a version kept on purpose is `pfm update pin <local> --keep "<what you did not take, and why>"` (one `UPDATED` file, recorded, shown as `KEPT`; `pfm update pin <local> --unkeep` drops the record once the untaken change is ported); a `RETIRED-NAME` mention that must stay is `pfm update accept <path> --reason "<why>" [--name <name>]` (`--undo <path> [--name <name>]` reverses); a `NEW` template you adopt: `pfm update pin --template <template> <local>`; a `MOVED` file: keep it, move it to the row's new local path, then `pfm update pin --template <new-template> <new-local>` and `pfm update drop <old-local>`; one you will never take: `pfm update ignore <template>...` (`--undo` reverses); a `GONE-UPSTREAM` or `LOCAL-DELETED` file you keep or forget: `pfm update drop <local>...` (a dropped pin's template then reads `NEW`: adopt or `ignore` it).
5. Re-run `pfm doctor --project-updates` until it ends `clean`, exit 0; a session ends only there, and a deferral is one of the recorded verbs above, never an open row.

`pfm update` (bare) advances the source clone to the latest release tag (or `--to vX.Y.Z`), rebuilds and installs the binary, runs `pfm doctor` (rolling back on failure), then prints the same report `pfm doctor --project-updates` prints for this project — start step 2 from there. Plain `pfm doctor` also warns, one `project_updates` row per adopted project a chat on this host ran in or the current directory sits in, about every row still open (`UPDATED`, `NEW`, `GONE-UPSTREAM`, `MOVED`, `LOCAL-DELETED`, `RETIRED-NAME`, `UNREADABLE`); those rows never change its exit code.

## Codex mirror

engine-sync, the `engine-sync` plugin's binary that `pfm codex build [repo-root]` forwards to, is the SINGLE writer of `AGENTS.md`, `.codex/` and `$HOME/.codex/`; `pfm codex check` gates drift (non-zero names each MISSING, STALE, ORPHANed, or CONFLICTing artifact). The `codex-sync.sh` hooks run both after any Edit/Write to a Claude source; a Bash-driven write bypasses the hook, so run `pfm codex build . && pfm codex check .` yourself. `pfm codex agents` compiles the machine-global agent `.toml` twins.

## Host integration

`pfm install --yes` stages the host assets and symlinks the machine-global agents, commands and skills from `{BLUEPRINT_CLONE_PATH}`; saving a global template IS the deploy. `pfm doctor [--verbose]` is the health check to run after an install or update, and the first thing to run when a fleet verb misbehaves.

## Chat: MCP first, shell for the rest

Inside a chat, the `mcp__chat-professor__*` MCP tools are preferred for `call`, `read`, `digest`, `last`, `find`, `ls`, `status`, `whoami`, `new`, and `save`. `ask`, `end`, `modal`, `watch`, `stream`, `recover`, and `history` are shell-only `pfm chat` commands. `pfm chat call` refuses `/compact`: pfm never types a compaction. Exit codes: 0 done · 2 usage · 3 chat dead · 4 no such chat · 5 answer timed out · 6 message not delivered.

## Harvest: MCP first, CLI for batches

Inside a chat, the `mcp__harvester-professor__*` tools (`mcp__harvester-professor__read` (its `files` field local server only), `mcp__harvester-professor__download_file`, `mcp__harvester-professor__search_literature`, `mcp__harvester-professor__search_web` (only when a search backend is configured)) answer per item; `pfm harvest [--refresh] [--include-content=false] [--ocr-language latin|zh|ja|ar|ru|he] [--json] [--header 'Name: value']... <url|path|identifier>...`, `pfm harvest download-file [--json] [--header 'Name: value']... <url>...` and `pfm harvest search [--type any|paper|book] [--limit N] [--json] <query>...` are the shell surface for batch or scripted fetches and for a chat without the MCP.

## Config

`pfm config show` prints the config path (`{clone}/pfm.config.json` by default; `pfm --config PATH` or `PFM_CONFIG` overrides) and every effective key with its source (file or default) — accounts, `claude.systemPrompt` (`production | lean | professor`), `claude.permissionMode`, `mcp.servers.*`, `harvester.enabled`, `ask.*`. `pfm config claude [--account N]` prints the resolved Claude launch settings. `pfm config validate` checks the file; `pfm config init [--force]` writes a fresh one.
