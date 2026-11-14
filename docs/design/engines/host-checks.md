# Host checks

`pfm/internal/hostcheck` owns read-only host detectors. Each recognises one old or wrong host shape and returns a row with an operator fix. The package's `Env`, `Detector`, `Row` and severity types are engine-neutral; later Codex detectors can add environment fields and join `Detectors()` without a second gate.

## Contents

- [API](#api)
- [Install gate and doctor](#install-gate-and-doctor)
- [Paths and ordering](#paths-and-ordering)
- [Detectors](#detectors)
- [Detector details](#detector-details)

## API

`Severity` is a string; `Block` renders `BLOCK` and `Warn` renders `WARN`. `Row` holds `Severity`, stable `Check`, absolute `Path`, `Problem` and `Fix`. `Row.Line()` renders `{severity} {check} {path} — {problem}`.

`Env` holds `Home`, `Store`, `ConfigPath`, `StateDB`, `CacheDB`, `Accounts`, `AcceptedMCP`, `Now`, `Environ`, `TmuxDir`, `Tmux`, `ProcRoot`, `Retired` and `RetiredErr`. `EnvFor(runtime config.Runtime, now time.Time) Env` derives it from the loaded runtime: the store through `installer.ClaudeStore`, the retired lists through `retired.LoadLists`.

`Detector` holds a stable `Check` and `Detect func(Env) ([]Row, error)`. `Detectors() []Detector` returns the ordered registry; `RunAll(env Env) []Row` concatenates findings; `Count(rows []Row, severity Severity) int` counts one severity. The installer package does not import hostcheck; hostcheck can use installer readers.

Detectors read only: no filesystem write, process signal or network call. Missing paths are ordinary absence. An unreadable path produces a blocking row and detection continues on other paths. A returned non-absence error is converted to a blocking row too: `UNREADABLE {path}: {cause}`, with fix `make {path} readable to you, then rerun`. A `*fs.PathError` supplies its path; otherwise the detector path or check id is used.

## Install gate and doctor

All three install modes, preview, `--yes` and `--check`, run host checks before any install write. With any `BLOCK`, stderr contains, per blocking row, `row.Render("pfm install: ")` — its `pfm install: {row.Line()}` line, then `pfm install:   fix: {row.Fix}` — the same renderer `pfm doctor` prints with under its `host-check:` prefix, so a user rolled back to a binary without host checks still has the fixes; the rows are followed by `pfm install: {N} blocking — run pfm doctor for the fixes`; install exits 4. With no blocks, warnings print `pfm install: {M} warnings — run pfm doctor to see them` on stdout and install continues. Required-dependency and scheduler checks remain additional install preflight checks.

Doctor prints every finding as `host-check: {row.Line()}`, followed by `host-check:   fix: {row.Fix}`. A block counts a failure; a warning counts a warning. With no findings it prints `host-check: ok ({n} checks)`, where `n` is `len(Detectors())`.

The fix strings below are instructions for the operator. Detection and the install gate do not execute them. Install creates missing store entries and account links only after the gate; doctor also checks these targets ([claude-config-dir.md](claude-config-dir.md#pfm-doctor-checks)).

An operator or a model applies a fix exactly as printed, so each fix must be safe as written, in any order. A fix that deletes or moves aside one path while keeping another goes through `removeKeeping` (`pfm/internal/hostcheck/hostcheck.go`). These are the present-target fix of `store-identity`, plus `home-state-file`, `beside-backup` and every `account-entry-real` fix. The fix prints only when no deleted path is the same file as the kept one after following links (`os.SameFile`; a path with nothing at it is no file). One file under two names prints `{remove} and {keep} are one file through a link: delete neither; replace the link with a real copy, then rerun pfm doctor` instead. A failed stat, a dangling link included, prints the `UNREADABLE` row in place of the row, with no delete.

## Paths and ordering

`{home}` is the operator home, `{store}` is `~/.claude`, `{cfg}` is the runtime config path, `{acct}` is one roster account's `ConfigDir` and `{acct1}` is the lowest-ID account's directory.

Detectors run in the table's order. Accounts are visited by ID, shared/account entries in installer list order, and directory names and MCP names lexically. Shared physical paths are inspected once where the check calls for deduplication. A real entry is present and is not a symlink. Manual merge fixes for real shared entries are chosen per `StoreEntries` entry.

## Detectors

The table gives one row per detector; exact problems and fixes follow under each check id.

| Check | Severity | Looks at | Problem | Fix |
| --- | --- | --- | --- | --- |
| `pfm-settings` | BLOCK | physical user settings | duplicate pfm launch values | remove owned keys |
| `stray-dir` | WARN | non-account `.cc` dirs | stray directory | inspect/remove |
| `account-is-store` | BLOCK | physical account path | identity dir is store | unlink or configure |
| `store-identity` | BLOCK | account entries in store | identity inside store | move, or move aside |
| `home-state-file` | WARN | home Claude state | launch without account env | move aside |
| `account-entry-real` | BLOCK | real shared entries or links to existing outside data | data outside store | entry-specific merge |
| `retired-store-entry` | WARN | retired entries in accounts and store | leftover link or store copy | `pfm install` |
| `unclassified` | WARN | unknown top-level names | neither entry list | keep |
| `third-party-mcp` | WARN | user-level MCP names | server outside pfm config | edit `mcp.thirdParty` |
| `stale-state-tmp` | WARN | old state temp files | older than 24 hours | remove |
| `beside-backup` | WARN | backup-pattern names | adjacent backup | remove when unneeded |

## Detector details

### pfm-settings

**Severity:** BLOCK

Looks at each physical `settings.json` among `{store}` and every `{acct}`, once per physical file.

Problem: `carries pfm's {items} — pfm supplies its own hooks and status lines through --settings at launch, so these entries are leftovers of an earlier install` (items from `installer.PFMSettingsLeftovers`: distinct `hook "{command}"` entries sorted by command, then `statusLine`, then `subagentStatusLine`, joined with `, `).

Fix: `remove {items} from {path} (each hook entry running that command and each named key; keep every other entry)`.

### stray-dir

**Severity:** WARN

Looks at `{home}/.cc/.git`, `.codex`, `.agents`.

Problem: empty dir: `empty stray dir`; else `stray dir holds {n} entries` / `not a directory`.

Fix: empty: `rmdir {path}`; else `inspect, then rm -r {path}`.

### account-is-store

**Severity:** BLOCK

Looks at each account through `claudelaunch.InspectConfigDir`: its resolved real path is the store or inside it. An account link that does not resolve (dangling) is an `UNREADABLE` row naming the cause; a link resolving outside the store passes.

Problem: `account {id}'s config dir resolves to the store {store}`.

Fix:

- `{acct}` a symlink: `rm {acct} && mkdir -m 700 {acct}` (removes the link only).
- A link `{link}` above `{acct}` resolving into the store (`~/.cc -> ~/.claude`): `[ ! -L {link} ] || { rm {link} && mkdir -m 700 {link}; } && [ ! -e {acct} ] && mv {real} {acct}`. Here `{real}` is `{acct}`'s resolved dir in the store, and `mkdir -m 700 -p {dir(acct)}` precedes the test when `{dir(acct)}` is not `{link}`. Each account's row replaces the one link guarded, so the rows run in any order. When `{real}` is the store itself or a known store entry, the fix is `{link} links into the store {store}: replace it with a real dir by hand, moving out only what {acct} holds, then rerun pfm doctor` instead.
- Else `point accounts[{id}].configDir in {cfg} at {config.DefaultAccountDir(home, id)}`, or, when that dir also resolves into the store, `point accounts[{id}].configDir in {cfg} at a real dir outside the store {store}; {default} resolves into it`. The fix never points configDir at a path resolving into the store.

### store-identity

**Severity:** BLOCK

Looks at each `AccountEntries` entry in `{store}` that is not in the `storeEntries` list of `templates/global/retired.json` (those are `retired-store-entry`'s) and not `cache` (`storeRebuildableEntries`: a cache is rebuilt, and other tools' hooks write under the store's `cache/`, so a store holding only that raises no row; an account's own `cache` stays an account entry); for `state`, only `state/mcp-discover-verdicts.json` is checked. `{acct1}` is classified by `claudelaunch.InspectConfigDir`, as in `account-is-store`.

Problem: `{entry} is account identity inside the store, written by a Claude launched with CLAUDE_CONFIG_DIR set to the store (a loop over config dirs that still lists it)`. pfm launches always set `CLAUDE_CONFIG_DIR` to an account dir, so identity lands in the store only through a launch that points it at the store. For `backups` the problem adds `; a Claude launched without CLAUDE_CONFIG_DIR writes backups here too`: an unscoped Claude's config dir is the store. For `.claude.json`, `.claude.json.backup` and `backups` it ends with the file's birth, `({birth})` — see [Stray state birth](#stray-state-birth).

Fix: `{acct1}` resolving to the store makes `{path}` and `{acct1}/{entry}` one file, so the fix deletes neither:

- `{acct1}` a symlink: `[ ! -L {acct1} ] || { rm {acct1} && mkdir -m 700 {acct1}; } &&` followed by the move below. That prefix is `account-is-store`'s own fix, guarded so the line also runs after that fix has, and a later run of that fix refuses on the real dir without deleting anything.
- `{acct1}` a real dir in the store: `apply account-is-store's fix for {acct1} first; pfm doctor then names this entry's move`.

Otherwise, `{acct1}/{entry}` absent: `mkdir -m 700 -p {acct1} && mv {path} {acct1}/{entry}` (for `state`, `mkdir -m 700 -p {acct1} {acct1}/state`), runnable before account 1 exists; present: the [move-aside fix](#stray-state-birth), through `removeKeeping`.

### home-state-file

**Severity:** WARN

Looks at `{home}/.claude.json`.

Problem: `a Claude launched without CLAUDE_CONFIG_DIR wrote this state file ({birth})`.

Fix: the move-aside fix below, through `removeKeeping` with `{acct1}/.claude.json` kept.

#### Stray state birth

`{birth}` is `mtime {RFC 3339 UTC}, firstStartTime {value}`, the file's mtime and the `firstStartTime` Claude recorded in it; for a dir (`backups`) it is `newest {name}: …` for its newest regular file, or `holds no file`. A value that cannot be read says why: `firstStartTime absent`, `firstStartTime unreadable: not JSON ({cause})`, `firstStartTime unreadable: {cause}`, `firstStartTime not a string: {raw}`, `contents unreadable: {cause}`.

The move-aside fix deletes nothing: `mkdir -m 700 -p {dir(aside)} && [ ! -e {aside} ] && mv {path} {aside}  # moved aside, never deleted`, where `{aside}` is `{path}` below `{home}` mirrored under `{home}/.local/state/pfm/stray-claude-state/{run stamp YYYYMMDD-HHMMSS}/`, so one doctor run's rows share one directory and never land on an earlier file. Install never expires doctor-aside directories (`stray-claude-state` and `stale-backup`), regardless of age; the operator decides when their contents are no longer needed.

### account-entry-real

**Severity:** BLOCK

Looks at each account × shared entry: a real file or dir at `{acct}/{entry}`, or a link to existing data outside the store; skipped for an account `account-is-store` reports.

Problem: `{entry} is a real {dir|file}; it belongs in the store`, or `{entry} links to {target} outside the store; its data belongs in the store`.

An account whose config dir is a regular file gets one row for the account instead of its entries' rows, path `{acct}`, problem `account config dir is a file, not a directory`, fix `mv {acct} {acct}.bak  # pfm install then creates the account dir`.

Fix: the entry-specific rule below. For a link, the merge reads its target's data and the final `rm` removes only the link.

For `account-entry-real`, the fix is selected by entry. Each fix is one shell line whose delete is the last link of an `&&` chain, so it runs only after the merge into the store succeeded. The trailing `#` comment carries the prose:

- `projects`, `file-history`, `tasks`, `session-env`, `paste-cache`, `shell-snapshots`, `plans`, `uploads`, `downloads`, `teams`, `agents`, `commands`, `skills`, `rules`, `themes`: `mkdir -p {store}/{entry} && { cp -an {path}/. {store}/{entry}/ || :; } && ! diff -rq {path} {store}/{entry} 2>&1 | grep -v '^Only in {store}/{entry}' && rm -r {path}  # union into the store; stops while a file differs`. A differing file, or a diff error, stops the `rm`. BSD `cp -n` exits 1 for every file it skips, so `diff` alone judges the merge.
- `history.jsonl`: `jq -c -s 'sort_by(.timestamp)[]' {store}/history.jsonl {path} > {store}/history.jsonl.new && mv {store}/history.jsonl.new {store}/history.jsonl && rm {path}  # interleaved by timestamp`.
- `plugins`: `rm -r {path}  # the store keeps its copy (reinstallable)`.
- `settings.json`: `jq -e -s '.[0] as $s | .[1] | to_entries | all(.key as $k | ($s | has($k) | not) or $s[$k] == .value)' {store}/settings.json {path} > /dev/null && jq -s '.[0] * .[1]' {store}/settings.json {path} > {store}/settings.json.new && mv {store}/settings.json.new {store}/settings.json && rm {path}  # adds the keys the store lacks; stops while a key differs`.
- `CLAUDE.md`: `cat {path} >> {store}/CLAUDE.md && rm {path}  # appended whole; prune {store}/CLAUDE.md as you like`.
- `tool-visibility-controller.json`: `mkdir -p {store} && { [ -e {store}/tool-visibility-controller.json ] || [ -L {store}/tool-visibility-controller.json ] || printf '{}\n' > {store}/tool-visibility-controller.json; } &&` followed by the `settings.json` line naming `tool-visibility-controller.json`: it first seeds a missing store copy, since install, which this row blocks, is what would seed it.
- `.last-cleanup`, `gh-pr-status-cache.json`: `rm {path}  # a cache`.

### retired-store-entry

**Severity:** WARN

Looks at each entry of the `storeEntries` list of `templates/global/retired.json`: per account by ID, a link at `{acct}/{entry}` resolving to `{store}/{entry}` (skipped for an account `account-is-store` reports), then `{store}/{entry}`. A real `{acct}/{entry}` is the account's own file and gives no row.

Problem: `{entry} links into the store; it is a per-account file now` for a link, `{entry} is a per-account file now; the store copy is retired` for the store copy. While an account has a live chat, which writes the entry, `pfm install` holds the migration back by design, through the same door it asks (`installer.LiveChatDeferral`, read here without the account guard): that account's link row and the store copy's row end `; deferred: account {dir} has live chats; close them and rerun pfm install --yes` (`; deferred: live chats in {dir} unreadable: {cause}` when the chats cannot be read).

Fix: `run pfm install; it removes the link, and Claude writes this account's own file` for a link, `run pfm install; it archives the copy under {home}/.local/state/pfm/retired-store-entries` for the store copy. Never a BLOCK: the install a block would refuse is the step that migrates these.

### unclassified

**Severity:** WARN

Looks at top-level names in `{store}` and every `{acct}` on neither list, not ignored (Claude Code's runtime entries `debug`, `daemon.lock`, `daemon.status.json`, `.claude.json.lock` and `tmp` included), not matched by the two leftover checks, not a regular `*.md` file at `{store}`'s top level (the user's own instruction material, shared by construction; an `{acct}` one still reports), and not named after a plugin `{store}/plugins/installed_plugins.json` lists (the `{name}` of each `{name}@{marketplace}` key: a plugin's own dir, its logs). A registry that exists but cannot be read or parsed, in a readable store, adds `WARN unclassified {registry} — UNREADABLE plugin registry: {cause} — a dir named after a plugin reports unclassified` with fix `make {registry} readable JSON, then rerun pfm doctor`, and those dirs report unclassified.

Problem: `UNCLASSIFIED — on neither the shared nor the per-account list`.

Fix: `keep it; pfm doctor names it until a pfm release classifies it`.

### third-party-mcp

**Severity:** WARN

Looks at top-level `mcpServers` names in every existing `{acct}/.claude.json` and `{home}/.claude.json`. Every entry produces a row, except those `doctor.acceptedMCP` retains (exact absolute `path` + `server`): those print as an ACCEPTED row. Harvester's native plugin registration is outside these registries. Unreadable registries are surfaced as unreadable rather than absent.

Problem: `mcpServers.{name} is declared outside pfm`.

Fix: `move it to mcp.thirdParty.{name} in {cfg}, then remove mcpServers.{name} from {path}`.

### stale-state-tmp

**Severity:** WARN

Looks at `.claude.json.tmp.*` with mtime older than 24 h before `Env.Now`, in `{store}`, every `{acct}`, `{home}`.

Problem: `Claude's stale state temp file`.

Fix: `rm {path}`.

### beside-backup

**Severity:** WARN

Looks at top-level names matching `*.pre-professor-*`, `*.bak-*`, `*.before-*` in `{store}` and every `{acct}`.

Problem: `a backup beside the file`.

Fix: `rm -r {path} once you no longer need it`, through `removeKeeping` with the original, the name before the first backup marker, as the kept path.
