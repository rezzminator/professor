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
- **Blocks:** `accounts` (the Claude account roster: `id`, `configDir`, per-account overrides), `chat` (the birth backend, below), `claude` (every Claude launch setting — the full list is [claude-launch.md § Config keys](claude-launch.md#config-keys)), `codex`, `opencode`, `mcp`, `tmux`, `state`.
- **Legacy config refusal:** without an explicit override, a missing clone config with a legacy config under `$XDG_CONFIG_HOME/pfm` (else `~/.config/pfm`) refuses normal runtime loading with `config not migrated: run pfm doctor for the fix ({legacy} present, {target} absent)` (`config.ErrNotMigrated`). Install loads through `config.LoadInstallRuntime` so its read-only host gate can name the problem; it does not relocate the file. Doctor uses a diagnostic runtime and prints the error and the operator fix.

### accounts

With no configured roster, `config.Defaults` supplies account 1 at `~/.cc/1`, whether or not it has credentials, plus discovered numeric `~/.cc/{n>1}` directories whose `.credentials.json` has a nonempty `claudeAiOauth.accessToken`. Discovery sorts by numeric id and records skipped candidates. The store is `~/.claude`, never an account directory. `config.DefaultAccountDir` owns the conventional path.

The jail's `PFM_CLAUDE_ROOTS` replaces discovery inputs. With a nonstandard root list, each root's parent becomes an account directory, with IDs assigned in list order and no extra `~/.cc` discovery. Empty roots or the single standard `~/.claude/projects` root use the default roster rule.

An explicit roster validates positive unique IDs, absolute or home-relative paths, and unique cleaned directory strings. Errors are prefixed `config {path}: accounts:`:

- `entry {n} id must be positive` or `duplicate id {id}`.
- `entry {n} configDir: must not contain NUL`, or `must be absolute or start with ~/ or $HOME/, got {value}`.
- `entry {n} configDir {dir} duplicates entry {earlier}`.

An entry naming the store loads; `account-is-store` blocks it and directs the operator to point its `configDir` at the account's default directory. Physical aliases to the store or into it are also blocked by `account-is-store`. A symlinked account directory resolving outside the store is accepted. Launch refuses a missing directory with `run pfm install`, and a file or a directory resolving into the store with `run pfm doctor`. It also refuses a dangling link, naming its inspection error, and an unknown roster ID ([claude-launch.md](claude-launch.md#checkconfigdir)).

### chat

`chat.backend` picks where a new Claude chat is born: `auto` (default), `tmux` or `herdr`. An explicit `--backend` on `pfm chat new` outranks it; the rules `auto` follows, and what a forced `herdr` does when its pane is unreachable, are in [claude-launch.md § The Herdr seat door](claude-launch.md#the-herdr-seat-door). Any other value, the empty string included, is refused with `config {path}: chat.backend must be auto, tmux or herdr`; a key set in the file reports source `file` in `pfm config show`, an absent one the default. It lives in `pfm/internal/config/chat.go` and the example file.

### chat.herdrTabStatus

`chat.herdrTabStatus` controls sole-chat Herdr tab labels: `emoji` (default), `text`, or `off`. Any other value is refused with `chat.herdrTabStatus must be emoji, text or off`. Emoji markers are working yellow circle, idle green circle, blocked red circle, error red square, dead black square, unknown white question mark. Blocked/error alternate with a white square every 500 ms; every marker occupies two display columns. Text fallback uses `[W]`, `[I]`, `[B]`, `[E]`, `[D]`, `[?]`, with `[ ]` for the attention off-phase. The name remains visible; focusing a blocked tab does not stop its pulse. These are glyph colors, not theme RGB or tab backgrounds: Herdr v0.9.5 exposes no per-status background API.

The existing `professor.indicators` plugin, linked by the Herdr theme-sync surface, runs the chat-aware `pfm internal herdr-indicators` painter. It monitors eligible chats even while idle so missed hooks, failed turns and process death are observed. `off` restores labels whose ownership still matches (a retained bridge tab by the same label rule) and stops tab monitoring; sidebar behavior is unchanged. The setting is reread at each poll. Status inspection failures show the unknown marker and a diagnostic under `HerdrLockDir`; they never become idle/dead. A tab whose ownership pfm cannot prove — at discovery or at the final validation before rename — is left untouched, and a diagnostic goes to the painter's warning writer (the Herdr plugin log and the persistent Herdr error log); every other tab keeps painting. A tmux chat shown in a Herdr pane with no native seat (a bridge pane) is proved by `call.ProveHerdrBridge` (`pfm/internal/call/herdr_bridge.go`): a process in the pane's Herdr foreground (`pane.process_info`) is a `tmux` client whose argv names a server inside pfm's tmux directory (`-L {name}`, or `-S {path}` directly under it), that server's `list-clients` lists the client pid attached to a session, the session holds pfm's `@pfm_herdr` record, and the pid's start identity is the same before and after those reads; `call.ProveHerdrPane` tries the durable-seat proof first and the bridge proof on its failure, and when neither proves it joins both causes (`ownership unproved`). The live tab validator (`tabViewReader.bridged`, `pfm/internal/chat/herdr_indicators.go`) additionally requires the proved client to be the client pid tmux listed for that chat, on the chat's own tmux socket and session. After a bridge tab's tmux client exits, the tab (`TabDecoration.Retained`, `reconcileTab` in `pfm/internal/herdr/tab_status.go`) is acted on only while pfm's own tab label record names that chat's identity with a rendered label and the tab still shows exactly the label pfm rendered; otherwise it is left untouched. A tab left untouched for an unproved owner or a foreign label is named once in the painter log (`... (left untouched)`) and never stops the other tabs; any other per-tab failure is logged, the remaining tabs are painted, and the joined error is returned. One chat whose tab views cannot be read is named in the warning writer and skipped; only a socket-wide pane listing failure fails the tick. Once ownership is proved, the tab reads dead when the supervisor is proved dead or the engine is no longer in the pane's foreground. The foreground read is `herdr.EngineForeground` (`pfm/internal/herdr/foreground.go`, called by `tabEngineForeground`): a missing, null or empty `foreground_processes` is unavailable evidence: the probe returns an error (`herdr foreground evidence unavailable … engine exit is unproved`) and the tab is never read dead from it; a validated nonempty list with no matching engine proves the engine is not in the foreground, and a malformed entry (no valid pid or identity) is an error. The metadata painter (`herdr.PaintIndicatorsWithTabs`) proves every pane-addressed report through `call.ProveHerdrPane` immediately before writing it (a bridge pane is proved as above): a pane whose ownership is unproved is logged (`pane {id} ownership unproved`) and skipped while the other targets keep painting. External Herdr changes can race the final validation and rename because the server has no conditional rename operation.

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
- Every pfm refuses a database whose `user_version` is newer than its own. `hidden.revision` (the kill write's rollback identity) remains an additive column applied at open. The shared state database is schema version 5; version 4 is reserved and skipped. The v5 migration upgrades existing version-3 and version-4 files directly, backing up before migration as `{file}.bak-before-v5`, under the existing migration lock and transaction. It rebuilds `comms` with a `kind` check for `call`, `group` and `spawn`, rewriting stored `inject` rows to `call`. Read-only opens require the exact current schema version; an older version-3 pfm rejects a version-5 database as newer.
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
