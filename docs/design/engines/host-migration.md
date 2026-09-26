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
- **Refuse over guess.** A path in a shape the table does not recognise — a foreign symlink, two differing files with one name — is refused and named; install carries on with every other row.

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
| account `settings.json` | no pfm-owned `hooks`, `statusLine`, `subagentStatusLine` | entries recorded in `settings-hook-ownership.json`; any hook whose command is a `claudeHookTemplates` command or matches pfm's retired-hook table (`pfm/internal/installer/settings.go`) — pfm-owned by command shape, so installs older than the ledger are cleaned too; the overlay `statusLine`; the `--subagents` line |
| account `.claude.json` | no pfm-owned `mcpServers` entries | entries recorded in `mcp-ownership.json` |
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
| `strip` | pfm-owned entries in an account file | remove the ledger-owned entries and the hooks pfm owns by command shape, keep every other key |
| `remove` | a legacy pfm artefact with no remaining user | remove it |
| `refuse` | a shape the row does not recognise, or a [guard](#guards) holds | print why and the path; touch nothing |

## Order

Rows apply in this order, each only after the previous one verified:

1. **Managed cleanup** — written and read back before any account file is touched, so no later step can leave a launch without the retention value.
2. **Config** — `pfm.config.json` and `harvester.config.json` moved to the clone; every later row reads the new path.
3. **State databases** — moved with their `-wal`/`-shm` siblings after a checkpoint; row counts of every table are checked before and after the move. Install then opens the moved databases, migrates their schemas, and drops the retired `swap_event` table (`fleetdb/migration_v2.sql`) and the cache's `hidden` table (`store/migration_v9.sql`).
4. **Session store** — merged, then linked.
5. **Account files** — memory helpers renamed, then pfm entries stripped from `settings.json` and `.claude.json`.
6. **Shell** — the `~/.zshrc` line repointed.
7. **Leftovers** — staged prompts, `shared.db` and stray dirs removed once nothing uses them.

## Merging a session dir

For `{config dir}/{entry}` as a real directory:

1. Each child (a session-id dir, or a `projects/{slug}` dir) absent from `~/.claude/{entry}` is moved there with one `rename`.
2. A child present in both is merged file by file: a file absent in the store is moved; a byte-identical file is dropped; two differing files with one name keep the store's copy, and the account's copy is parked in the journal and listed as a conflict.
3. The emptied dir is removed and the symlink created.

Transcripts are append-only JSONL named by session id, so a differing same-named transcript means one session ran on two accounts before the store existed; both copies stay on disk and the conflict list names them for a human.

## Guards

- **Live chats.** A session row whose account has a live Claude process (`{config dir}/sessions/{pid}.json` with a running pid) is refused, naming the chats to close.
- **Database holders.** The state-database rows stop `pfm-mcp` and `pfm-name-sync` first and restart them after; a remaining holder (a picker, a live chat's MCP proxy) makes the row refuse, naming each pid.
- **Root.** The managed-settings row needs `sudo`; declined, it is refused and doctor keeps warning.
- **Free space.** A cross-filesystem move checks free space first; `rename` within one filesystem needs none.

## The journal

Each applying run writes `~/.local/state/pfm/migrations/{UTC timestamp}/`:

- `journal.json` — one record per affected path: row, verdict, source, destination, backup path, result. Each record is flushed before its path changes.
- `backup/` — the prior bytes of every rewritten or removed path, each session tree before a merge, every parked conflict, and full copies of both moved databases and their WAL/SHM siblings before checkpointing. Rollback restores the database copies even if pfm opened the moved files after installation.

`pfm install --rollback {timestamp}` replays the journal backwards: links removed, moves reversed, backups restored. A rollback of a run that moved state databases refuses while any pfm process holds them.

## Legacy prompt files

Chats born before the move were launched with `--system-prompt-file ~/.local/share/pfm/install/harness-prompts/claude.md`. Install stops writing that dir and leaves it in place; the row is `refuse` with `in use by {n} live chats` while any live Claude argv names a file there, and `remove` once none does. Chats migrate by being reborn — a reload or a new chat takes the repo path.

## Rehearsal

`TestHostLayoutMigratesLegacyHome` (`pfm/e2e/`) is a Go end-to-end jail test, run in the fence through `dev.sh iso` like every e2e test, over the existing `testjail` homes; it needs no provider account. It sets `PFM_CONFIG` for its own home and builds the full legacy shape — `~/.cc/1 → ~/.claude`, accounts 2 and 3 with linked `projects/` and real `file-history/`, `tasks/`, `session-env/` including a same-id conflict, `~/.cc/fleet.db` with a `swap_event` table, `~/.local/state/pfm/fleet.db`, an empty `shared.db`, pfm hooks (ledger-owned, pre-ledger and retired) and `statusLine` in each account `settings.json`, a fingerprint-matched `cc-memory-wire.sh` with the operator hook naming it, pfm MCP entries in each `.claude.json`, the staged `~/.zshrc` line, and `~/.config/pfm/pfm.config.json`. It then asserts:

1. `pfm install` (preview) changes no byte.
2. `pfm install --yes` reaches every row `ok`, except the planted conflict, which is parked and listed.
3. The SHA-256 of every transcript, checkpoint and task file before equals the set after (store plus parked).
4. The row counts stay unchanged except for retired `swap_event` and cache `hidden`; cache hides move into shared-state `hidden` before their old table drops.
5. A second `pfm install --yes` records no action.
6. `pfm doctor` reports no layout finding.
7. `pfm install --rollback {id}` restores the legacy tree byte for byte.

## Running it on a host

1. Close the chats on the accounts being merged, or accept that their rows refuse and rerun later.
2. `pfm install` — read the plan.
3. `pfm install --yes`.
4. `pfm doctor` — every layout row `ok`; the conflict list, if any, names what to reconcile by hand.
