# pfm home

Where pfm keeps its own configuration and state. Nothing here belongs to one engine: Claude, Codex and OpenCode all read and write through these files, so none of them lives under an engine's config dir. How the Claude launch reads this config is in [claude-launch.md](claude-launch.md); how an older host is moved onto this layout is in [host-migration.md](host-migration.md).

## Contents

- [Decisions](#decisions)
- [pfm.config.json](#pfmconfigjson)
- [example.pfm.config.json](#examplepfmconfigjson)
- [State databases](#state-databases)
- [Other state](#other-state)
- [pfm doctor checks](#pfm-doctor-checks)

## Decisions

- **One config file, next to the framework.** The machine config is `{clone}/pfm.config.json` — `~/.professor/pfm.config.json` on a standard install — where anyone who opens the framework finds it. It is gitignored; it is never a template.
- **The config is the only source of launch defaults.** Every interactive harness launch takes its values from `pfm.config.json` (per-account entries override machine-wide ones). A per-launch choice — a picker toggle, a `pfm chat new` flag — changes that one launch and never becomes a default. `pfm headless exec` reads no launch value from it ([claude-headless.md](claude-headless.md#decisions)).
- **pfm state lives in pfm's state dir.** Both databases default to `~/.local/state/pfm/`, their paths are config keys, and no pfm file lives under `~/.cc` or any other engine dir.
- **Authoritative state and derived cache stay two files.** The cache can be deleted and rebuilt from transcripts at any time; the state cannot. Two files keep that line physical.

## pfm.config.json

- **Path:** `{clone}/pfm.config.json`, where `{clone}` is the recorded source repo (`~/.local/share/pfm/install/source-repo`). The `--config PATH` flag and the `PFM_CONFIG` environment variable override it; the fence sets `PFM_CONFIG` because its worktree mount is read-only.
- **Ignored by git:** `/pfm.config.json` and `/harvester.config.json` in the repo's `.gitignore`.
- **Written by pfm** only at the resolved path: `pfm config init`, `pfm mcp {server} enable|disable`, and the installer's log-default insertion.
- **`harvester.config.json`** lives beside it, `{clone}/harvester.config.json`, also gitignored.
- **Created by `pfm install`** from `example.pfm.config.json` when absent; an existing file is never overwritten. Keys a newer pfm adds are reported by `pfm doctor` as `missing key … (default …)`, not written silently.
- **Blocks:** `accounts` (the Claude account roster: `id`, `configDir`, per-account overrides), `claude` (every Claude launch setting — the full list is [claude-launch.md § Config keys](claude-launch.md#config-keys)), `codex`, `opencode`, `mcp`, `tmux`, `state`.

## example.pfm.config.json

Tracked at the repo root. It holds every key pfm reads, each at its default, with `~`-relative paths only — no machine-absolute path, no account id, no credential (the leak gate scans it). A new config key lands in this file in the same change that adds it; `dev.sh test pfm` fails when a key the loader knows is missing from the example.

## State databases

| File | Config key | Default | Holds |
| --- | --- | --- | --- |
| `pfm.db` | `state.db` | `~/.local/state/pfm/pfm.db` | the authoritative operator state: `chat`, `comms`, `issues`, `children`, `hidden`, `meta` (the primary account), `launch` (what pfm launched per session — [claude-launch.md](claude-launch.md#the-launch-record)) |
| `pfm-cache.db` | `state.cacheDb` | `~/.local/state/pfm/pfm-cache.db` | the derived cache, rebuildable from transcripts: `transcripts`, `chat_summaries`, `rollouts`, `cx_names`, `oc_sessions`, `epic_injections` |

- `paths.Values.StateDB` and `paths.Values.CacheDB` resolve them: the test-jail environment variable (`PFM_STATE_DB`, `PFM_CACHE_DB`) first, then the config key, then the default.
- `hidden` lives only in `pfm.db`.
- Both databases migrate by numbered, additive `migration_vN.sql` files (`pfm.db` gains the mechanism with `launch`). A migration backs the file up beside itself (`{file}.bak-before-v{n}`) before it runs.
- The move onto this layout drops the retired `swap_event` table, the cache's unread `hidden` copy, and the empty `shared.db`.

## Other state

All under `~/.local/state/pfm/`: `callmeter.db` (the tool-call recorder, `docs/design/hooks/callmeter.md`), `log/pfm.jsonl`, `migrations/` ([host-migration.md](host-migration.md#the-journal)), and flight directories under `flights/`.

## pfm doctor checks

| Check | Broken state reports |
| --- | --- |
| config file | `config: missing {path} — run pfm install`, `config: unreadable {path} error=…` |
| config keys | `config: missing key {key} (default {value})` per key the loader knows and the file lacks |
| state paths | `state: {key}={path} missing`, or `state: legacy {old path} still present — run pfm install` |
