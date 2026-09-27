# Host migration

How `pfm install` moves a host from any older layout onto the one in [pfm-home.md](pfm-home.md) and [claude-config-dir.md](claude-config-dir.md) without losing a transcript, a checkpoint or a row of state. There are no one-off migration scripts: `pfm install` holds one declarative table of the target layout, classifies what it finds on disk against every row, and applies only the actions it can prove safe. `pfm doctor` runs the same classifier read-only.

## Contents

- [Decisions](#decisions)
- [The layout table](#the-layout-table)
- [Classification](#classification)
- [Order](#order)
- [Merging a session dir](#merging-a-session-dir)
- [Guards](#guards)
- [The journal](#the-journal)
- [Legacy prompt files](#legacy-prompt-files)
- [Rehearsal](#rehearsal)
- [Running it on a host](#running-it-on-a-host)

## Decisions

- **Desired state, not steps.** `HostLayout` (`pfm/internal/installer/layout.go`) lists every path pfm owns with the form it must take. Install reconciles toward it; a second run finds nothing to do. A future layout change is a row edit, never a new migration script.
- **One classifier, two callers.** `ClassifyLayout` is pure: it reads the disk and returns one verdict per row. Install applies the verdicts; doctor prints them. What install fixes, doctor reports, by construction.
- **Preview first.** `pfm install` without `--yes` prints every action and changes nothing.
- **Nothing is overwritten or deleted without a copy.** Every move, rewrite and removal is recorded in a journal with its backup; `pfm install --rollback {id}` reverses a run.
- **Refuse over guess.** A path in a shape the table does not recognise — a foreign symlink, two differing files with one name — is refused and named. `pfm install --yes` refuses the whole run before any change when the initial classification has a blocking refusal.
- **A shared settings file is one file.** An account `settings.json` that is a symlink to a regular file inside HOME (accounts sharing `~/.claude/settings.json` on purpose) is judged once, by its physical path: the first account resolving to it carries the verdict for that target, the others report `ok`, shared with the first; a strip writes through to the target, the link stays a link, the journal snapshots the target, and a live chat in any sharing account refuses it. A dangling link, a target outside HOME and a non-regular target are refused.

## The layout table

| Row | Target form | Legacy forms it recognises |
| --- | --- | --- |
| `pfm.config.json` | regular file `{clone}/pfm.config.json` | `~/.config/pfm/pfm.config.json`, or its older name `~/.config/pfm/config.json` |
| `harvester.config.json` | regular file `{clone}/harvester.config.json` | `~/.config/pfm/harvester.config.json` |
| `state.db` | `~/.local/state/pfm/pfm.db` (or the config path) | `~/.cc/fleet.db` (+ `-wal`, `-shm`) |
| `state.cacheDb` | `~/.local/state/pfm/pfm-cache.db` (or the config path) | `~/.local/state/pfm/fleet.db` (+ `-wal`, `-shm`) |
| session store, per account × `SessionPaths` entry | symlink `{config dir}/{entry} → ~/.claude/{entry}` | a real dir; a link to another account's dir; absent |
| managed cleanup | `/etc/claude-code/managed-settings.d/pfm.json` with `claude.cleanupPeriodDays` | absent; another value |
| memory helpers | `scripts/memory-wire.sh` / `scripts/memory-consolidate.sh`, named by the operator's own hooks | the fingerprint-matched `scripts/cc-memory-wire.sh` / `scripts/cc-memory-consolidate.sh` in any account dir: renamed once, and the operator hook paths that name them in `settings.json` / `settings.local.json` rewritten once (`migrateMemoryHelpers`) |
| account `settings.json`, or the regular file inside HOME it links to (judged once per physical file) | no pfm-owned `hooks`, `statusLine`, `subagentStatusLine` | entries recorded in `settings-hook-ownership.json`; any hook whose command is a `claudeHookTemplates` command or matches pfm's retired-hook table (`pfm/internal/installer/settings.go`) — pfm-owned by command shape, so installs older than the ledger are cleaned too; the overlay `statusLine`; the `--subagents` line |
| account `.claude.json` (row `account-mcp`) | no pfm-owned `mcpServers` entries | entries recorded in `mcp-ownership.json`; any `chat`, `harvester` or `professor` entry in an exact pfm shape — the legacy `chat` stdio (`{home}/.local/bin/pfm mcp chat serve` or `pfm mcp`) and loopback `http://127.0.0.1:{mcp.http.port}/mcp/{name}` entries, and `mcpServers.professor` as `{type: stdio, command: {home}/.local/bin/pfm, args: [mcp, serve, --stdio]}` — pfm-owned by shape, so installs older than the ledger are cleaned too; any other name only when the ledger records it |
| `~/.mcp.json` (row `home-mcp`) | no pfm-owned `mcpServers` entries, and no `clients` list in `mcp-ownership.json` | the names in the ledger's old `clients` list; any `chat`, `harvester` or `professor` entry in the same exact pfm shapes; a non-empty `clients` list, which install drops from the ledger |
| `~/.zshrc` source line | `{clone}/pfm/internal/installer/assets/shim/pfm.zsh` | the line naming `~/.local/share/pfm/install/shim/pfm.zsh` |
| staged prompts | absent | `~/.local/share/pfm/install/harness-prompts/` ([Legacy prompt files](#legacy-prompt-files)) |
| `shared.db` | absent | an empty `~/.local/state/pfm/shared.db` |
| stray dirs | absent | empty `~/.cc/.git`, `~/.cc/.codex`, `~/.cc/.agents` |

An account's `cleanupPeriodDays` key is never removed: it is a redundant copy of the managed value, and removing it gains nothing. The memory-helper row is the last write pfm makes into an account file; once it is `ok`, pfm writes no key in any account `settings.json`, `settings.local.json` or `.claude.json`.

## Classification

| Verdict | Meaning | Install does |
| --- | --- | --- |
| `ok` | already in target form | nothing |
| `create` | target absent, nothing in the way | create it |
| `repoint` | a pfm-made symlink to the wrong target | replace the link |
| `move` | a legacy file at an old path, target absent | move it, leave nothing at the old path (a memory helper's move also rewrites the operator hook paths naming it) |
| `merge` | a real session dir where a link belongs | [merge](#merging-a-session-dir), then link |
| `strip` | pfm-owned entries in an account file or `~/.mcp.json` | remove the ledger-owned entries, the hooks pfm owns by command shape and the MCP entries pfm owns by shape, keep every other key; `home-mcp` also drops the ledger's `clients` list |
| `remove` | a legacy pfm artefact with no remaining user | remove it |
| `refuse` | a shape the row does not recognise, or a [guard](#guards) holds | print why and the path; touch nothing |

## Order

Rows apply in this order, each only after the previous one verified:

1. **Managed cleanup** — attempted before any account file is touched. A declined `sudo -n` produces an advisory with a command to set the value by hand; later rows continue.
2. **Config** — `pfm.config.json` and `harvester.config.json` moved to the clone; every later row reads the new path.
3. **State databases** — moved with their `-wal`/`-shm` siblings after a checkpoint; row counts of every table are checked before and after the move. Install then opens the moved databases, migrates their schemas, and drops the retired `swap_event` table (`fleetdb/migration_v2.sql`) and the cache's `hidden` table (`store/migration_v9.sql`).
4. **Session store** — merged, then linked.
5. **Account files** — memory helpers renamed, then pfm entries stripped from `settings.json` and `.claude.json`, then from `~/.mcp.json` (`home-mcp`), whose change also retires the ledger's `clients` list.
6. **Shell** — the `~/.zshrc` line repointed.
7. **Leftovers** — staged prompts, `shared.db` and stray dirs removed once nothing uses them.

## Merging a session dir

For `{config dir}/{entry}` as a real directory:

1. Each child (a session-id dir, or a `projects/{slug}` dir) absent from `~/.claude/{entry}` is moved there with one `rename`.
2. A child present in both is merged file by file: a file absent in the store is moved; a byte-identical file is dropped; two differing files with one name keep the store's copy, and the account's copy is parked in the journal and listed as a conflict.
3. The emptied dir is removed and the symlink created.

Transcripts are append-only JSONL named by session id, so a differing same-named transcript means one session ran on two accounts before the store existed; both copies stay on disk and the conflict list names them for a human.

## Guards

- **Live chats.** Only a row that would write refuses for a live Claude process (`{config dir}/sessions/{pid}.json` with a running pid); an `ok` row stays `ok` while a chat is live. A refused row names the chats to close. A running pid is read from the process table (`/proc` on Linux, `sysctl` on macOS). `pfm install --yes` refuses the whole run before any change when any blocking row refuses, naming each refusal; preview still lists the rows without writing.
- **Database holders.** The state-database rows stop whichever of `pfm-mcp` and `pfm-name-sync` are running first and restart exactly those after — a unit the host never loaded, or one the operator stopped, is neither stopped nor started; a remaining holder (a picker, a live chat's MCP proxy) makes the row refuse, naming each pid. Linux compares symlink-resolved database and fd paths. Another user's process, whose `/proc/{pid}/fd` a normal user cannot read, is skipped. On macOS, `lsof -t` checks holders; a missing or failing `lsof` refuses the row and names the reason. A configured state or cache database path equal to its legacy path, including through symlinks, refuses.
- **Root.** The managed-settings row tries a direct write, then `sudo -n` if needed. Missing cached credentials produce an advisory warning with the command to run; they never refuse the whole install.
- **Free space.** Before the first change of `pfm install --yes`, a preflight sums what the run will write per filesystem: the bytes of every path the journal will copy (the layout rows it acts on, the installer's own planned writes, and the whole harvester root when a re-provision is planned) charged to the migrations directory's filesystem, plus the source of every cross-filesystem move charged to its destination's. It refuses the whole apply unless each has `free >= need + max(1 GiB, need/10)`: `pfm install: not enough free space on {dir}: need {need} bytes + margin {margin}, have {free} — nothing changed`, exit 1. A free-space probe that fails refuses too (`could not measure free space on {dir}: {err}`), never read as enough. Each cross-filesystem move checks again before it copies; `rename` within one filesystem needs none.
- **Live chats at rollback.** A rollback with a session-store record refuses while a chat is live on that account (`{home}/.claude/{entry}` checks every account), `--force` included.

## The journal

Each applying run keeps one journal for the whole `pfm install --yes`, in `~/.local/state/pfm/migrations/{UTC timestamp}/`: the layout rows, the config migration, and every write the installer itself makes that changes bytes (an identical rewrite records nothing, so a no-op install makes no journal). Its last stdout line names it: `install journal: {dir}`.

- `journal.json` — one record per affected path: row, verdict, source, destination, backup path, result, and `after`, the destination's fingerprint right after the change (sha256 over one line per entry at and under it, walked without following links: relative path, type, size, mtime, mode, link target). Each record is flushed before its path changes. The install refingerprints the state database after its own schema migration of the moved file.
- `scope.json` — the Claude accounts, Codex homes, and state and cache database paths used by that install. Rollback judges every record against this saved scope, even when the current config has moved or is absent.
- `backup/` — the prior bytes of every rewritten or removed path, each session tree before a merge, every parked conflict, and full copies of both moved databases and their WAL/SHM siblings before checkpointing. Rollback restores the database copies even if pfm opened the moved files after installation.

A journal with pending records blocks the next `pfm install --yes` before any change; `pfm install --rollback {id}` replays those records, including a half-finished database move. An unreadable journal is also a refusal. A released updater's staged candidate that predates the install journal refuses a layout migration with this line and prints the manual crossing commands:

```text
  refuse  updater — this install migrates the host layout, and the pfm update running it predates the install journal
```

`pfm doctor` prints `install journals: {count} in {root}, {bytes} bytes` and names pending or unreadable journals.

`pfm install --rollback {timestamp} [--force]` replays the journal backwards: links removed, moves reversed, backups restored. Scope, live-chat and drift checks run before replay; a database rollback stops the running fleet units before checking for remaining holders:

- a journal already rolled back refuses: `rollback {id} refused: already rolled back at {time}`;
- a journal without `scope.json` refuses: `rollback {id} refused: journal {dir} has no scope.json`;
- an unreadable or invalid scope refuses: `rollback {id} refused: journal scope {path}: {reason}`;
- a run that moved state databases stops the running fleet units before database replay and restarts those same units afterward; it refuses if any holder remains;
- a session-store record refuses while a chat is live on its account (§ Guards);
- a destination whose fingerprint no longer matches its last applied record — newer work since the install — refuses the whole rollback, naming every drifted path: `rollback {id} refused: drift at {path}[, {path}…] — rerun with --force to overwrite them`. A record without a fingerprint counts as drift. Pending records, the cache database and `-wal`/`-shm` siblings are not checked. `--force` overrides only this check.

A successful rollback keeps the journal and writes `{dir}/rolled-back` (the UTC time); a rollback that fails part-way leaves it unmarked, so it can be retried. `pfm update` rolls a failed candidate back by replaying that candidate install's journal the same way.

After a journal is sealed, pruning keeps the newest three sealed journals and every journal younger than 14 days; pending and unreadable journals are retained. Each pruned journal prints:

```text
  prune   install journal {id} ({bytes} bytes)
```

## Legacy prompt files

Chats born before the move were launched with `--system-prompt-file ~/.local/share/pfm/install/harness-prompts/claude.md`. Install stops writing that dir and leaves it in place; the row is `refuse` with `in use by {n} live chats` while any live Claude argv names a file there, and `remove` once none does. Chats migrate by being reborn — a reload or a new chat takes the repo path.

## Rehearsal

`TestHostLayoutMigratesLegacyHome` (`pfm/e2e/`) is a Go end-to-end jail test, run in the fence through `dev.sh iso` like every e2e test, over the existing `testjail` homes; it needs no provider account. It sets `PFM_CONFIG` for its own home and builds the full legacy shape — `~/.cc/1 → ~/.claude`, accounts 2 and 3 with linked `projects/` and real `file-history/`, `tasks/`, `session-env/` including a same-id conflict, `~/.cc/fleet.db` with a `swap_event` table, `~/.local/state/pfm/fleet.db`, an empty `shared.db`, pfm hooks (ledger-owned, pre-ledger and retired) and `statusLine` in each account `settings.json`, a fingerprint-matched `cc-memory-wire.sh` with the operator hook naming it, pfm MCP entries in each `.claude.json` (ledger-owned, plus a shape-only `professor` and a loopback `harvester` with no ledger record in account 2), a `~/.mcp.json` with a pfm-shape `harvester` beside a foreign `operator` and the ledger's old `clients: ["harvester"]`, the staged `~/.zshrc` line, and `~/.config/pfm/pfm.config.json`. It then asserts:

1. `pfm install` (preview) changes no byte.
2. `pfm install --yes` reaches every row `ok`, except the planted conflict, which is parked and listed.
3. The SHA-256 of every transcript, checkpoint and task file before equals the set after (store plus parked).
4. The row counts stay unchanged except for retired `swap_event` and cache `hidden`; cache hides move into shared-state `hidden` before their old table drops.
5. A second `pfm install --yes` records no action.
6. `pfm doctor` reports no layout finding.
7. `pfm install --rollback {id}` restores the legacy tree byte for byte.

## Running it on a host

1. Back up the live host with `infra/fence/host-backup.sh BACKUP live`, then run `infra/fence/host-rehearsal.sh BACKUP SCRATCH` against that backup. The rehearsal runs preview, apply, doctor, a second apply, the manifest check and rollback, and writes its verdict under `SCRATCH/rehearsal/`.
2. On v0.76–v0.78, cross before the install steps below: close every chat, including the one running an older `pfm update`; from a plain shell outside tmux run `git -C <clone> pull --ff-only`, `make -C <clone>/pfm host-install`, then `pfm install --yes`. The older updater rolls itself back first when its candidate refuses the migration.
3. Close chats on accounts whose rows need to write; any live-chat refusal blocks `pfm install --yes` before changes.
4. `pfm install` — read the plan.
5. `pfm install --yes`.
6. `pfm doctor` — every layout row `ok`; the conflict list, if any, names what to reconcile by hand.
