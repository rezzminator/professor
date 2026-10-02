# pfm home

Where pfm keeps its own configuration and state. Nothing here belongs to one engine: Claude, Codex and OpenCode all read and write through these files, so none of them lives under an engine's config dir. How the Claude launch reads this config is in [claude-launch.md](claude-launch.md); the read-only host detectors and their operator fixes are in [host-checks.md](host-checks.md).

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
- **Legacy config refusal:** without an explicit override, a missing clone config with a legacy config under `$XDG_CONFIG_HOME/pfm` (else `~/.config/pfm`) refuses normal runtime loading with `config not migrated: run pfm doctor for the fix ({legacy} present, {target} absent)` (`config.ErrNotMigrated`). Install loads through `config.LoadInstallRuntime` so its read-only host gate can name the problem; it does not relocate the file. Doctor uses a diagnostic runtime and prints the error and the operator fix.

### accounts

With no configured roster, `config.Defaults` supplies account 1 at `~/.cc/1`, whether or not it has credentials, plus discovered numeric `~/.cc/{n>1}` directories whose `.credentials.json` has a nonempty `claudeAiOauth.accessToken`. Discovery sorts by numeric id and records skipped candidates. The store is `~/.claude`, never an account directory. `config.DefaultAccountDir` owns the conventional path.

The jail's `PFM_CLAUDE_ROOTS` replaces discovery inputs. With a nonstandard root list, each root's parent becomes an account directory, with IDs assigned in list order and no extra `~/.cc` discovery. Empty roots or the single standard `~/.claude/projects` root use the default roster rule.

An explicit roster validates positive unique IDs, absolute or home-relative paths, unique cleaned directory strings, and refuses the cleaned store path. Errors are prefixed `config {path}: accounts:`:

- `entry {n} id must be positive` or `duplicate id {id}`.
- `entry {n} configDir: must not contain NUL`, or `must be absolute or start with ~/ or $HOME/, got {value}`.
- `entry {n} configDir {dir} duplicates entry {earlier}`.
- `entry {n} configDir {store} is the Claude store; an account needs its own dir (default {account dir})`.

Physical aliases to the store or into it are separately blocked by `account-is-store`. A symlinked account directory resolving outside the store is accepted. Launch refuses a missing directory with `run pfm install`, and a file or a directory resolving into the store with `run pfm doctor`. It also refuses a dangling link, naming its inspection error, and an unknown roster ID ([claude-launch.md](claude-launch.md#checkconfigdir)).

### mcp.thirdParty

A map from server name to a JSON object in Claude's `mcpServers` shape, default `{}`. pfm preserves each object's fields and passes every entry through `--mcp-config`. Definitions are operator-managed; host checks name entries still present in account or home state files. Validation refuses `mcp.thirdParty.professor: the name professor is pfm's own server` and `mcp.thirdParty.{name} must be a JSON object (a Claude mcpServers entry)`, prefixed `config {path}:`.

## example.pfm.config.json

Tracked at the repo root. It holds every key pfm reads, each at its default, with `~`-relative paths only — no machine-absolute path, no account id, no credential (the leak gate scans it). A new config key lands in this file in the same change that adds it; `dev.sh test pfm` fails when a key the loader knows is missing from the example.

## State databases

| File | Config key | Default | Holds |
| --- | --- | --- | --- |
| `pfm.db` | `state.db` | `~/.local/state/pfm/pfm.db` | the authoritative operator state: `chat`, `comms`, `issues`, `children`, `hidden`, `meta` (the primary account), `launch` (what pfm launched per session — [claude-launch.md](claude-launch.md#the-launch-record)) |
| `pfm-cache.db` | `state.cacheDb` | `~/.local/state/pfm/pfm-cache.db` | the derived cache, rebuildable from transcripts: `transcripts`, `chat_summaries`, `rollouts`, `cx_names`, `oc_sessions`, `epic_injections` |

- `paths.Values.StateDB` and `paths.Values.CacheDB` resolve them: the test-jail environment variable (`PFM_STATE_DB`, `PFM_CACHE_DB`) first, then the config key, then the default.
- `hidden` lives only in `pfm.db`.
- Both databases use numbered `migration_vN.sql` schema migrations. Before migrating an existing database, pfm preserves a backup beside it (`{file}.bak-before-v{n}`).
- The state schema migration drops `swap_event`; the cache schema migration drops its `hidden` copy. The `shared-db` host check reports an old `shared.db` for the operator to inspect and remove.
- **No fork while legacy data waits:** nothing creates the default state database while `~/.cc/fleet.db` waits, or the default cache while `~/.local/state/pfm/fleet.db` waits. `fleetdb.OpenSharedState` and `store.OpenContext` check before creating targets and return `paths.ErrLegacyPending`: `legacy database not migrated: {target} not created while legacy {legacy} still exists — run pfm doctor for the fix`. An existing target can open; an unreadable legacy path is an error. Install's host gate blocks on legacy database paths and doctor prints the operator's fix, including present WAL/SHM siblings.

## Other state

All under `~/.local/state/pfm/`: `log/pfm.jsonl`, generated assets under `generated/`, and flight directories under `flights/`.

## pfm doctor checks

| Check | Broken state reports |
| --- | --- |
| config file | `doctor: config: missing {path} — run pfm install`, `doctor: config: unreadable {path} error=…` |
| config keys | `doctor: config: missing key {key} (default {value})` per key the loader knows and the file lacks |
| legacy state/cache paths | `host-check: BLOCK legacy-state-db` / `legacy-cache-db`, each with an operator fix |
