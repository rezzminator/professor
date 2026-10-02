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

`Env` holds `Home`, `Store`, `ConfigPath`, `LegacyConfigDir`, `StateDB`, `CacheDB`, `ManagedRoot`, `ConfigExplicit`, `HarvesterCacheDir`, `MCPPort`, `Accounts` and `Now`. `EnvFor(runtime config.Runtime, now time.Time) Env` derives it from the loaded runtime: store and managed root through installer path functions, legacy config through `config.LegacyConfigDir`.

`Detector` holds a stable `Check` and `Detect func(Env) ([]Row, error)`. `Detectors() []Detector` returns the ordered registry; `RunAll(env Env) []Row` concatenates findings; `Count(rows []Row, severity Severity) int` counts one severity. The installer package does not import hostcheck; hostcheck can use installer readers.

Detectors read only: no filesystem write, process signal or network call. Missing paths are ordinary absence. An unreadable path produces a blocking row and detection continues on other paths. A returned non-absence error is converted to a blocking row too: `UNREADABLE {path}: {cause}`, with fix `make {path} readable to you, then rerun`. A `*fs.PathError` supplies its path; otherwise the detector path or check id is used.

## Install gate and doctor

All three install modes, preview, `--yes` and `--check`, run host checks before any install write. With any `BLOCK`, stderr contains, per blocking row, `row.Render("pfm install: ")` — its `pfm install: {row.Line()}` line, then `pfm install:   fix: {row.Fix}` — the same renderer `pfm doctor` prints with under its `host-check: ` prefix, so a user rolled back to a binary without host checks still has the fixes; the rows are followed by `pfm install: {N} blocking — run pfm doctor for the fixes`; install exits 4. With no blocks, warnings print `pfm install: {M} warnings — run pfm doctor to see them` on stdout and install continues. Required-dependency and scheduler checks remain additional install preflight checks.

Doctor prints every finding as `host-check: {row.Line()}`, followed by `host-check:   fix: {row.Fix}`. A block counts a failure; a warning counts a warning. With no findings it prints `host-check: ok ({n} checks)`, where `n` is `len(Detectors())`.

The fix strings below are instructions for the operator. Detection and the install gate do not execute them. Install creates missing store entries and account links only after the gate; doctor also checks these targets ([claude-config-dir.md](claude-config-dir.md#pfm-doctor-checks)).

An operator or a model applies a fix exactly as printed, so each fix must be safe as written, in any order. A fix that deletes one path while keeping another goes through `removeKeeping` (`pfm/internal/hostcheck/hostcheck.go`). These are the present-target fixes of `legacy-config`, `legacy-harvester-config`, `legacy-state-db`, `legacy-cache-db`, `legacy-harvester-cache` and `store-identity`, plus `pre-split-config` (b), `home-state-file`, `beside-backup` and every `account-entry-real` fix. The fix prints only when no deleted path is the same file as the kept one after following links (`os.SameFile`; a path with nothing at it is no file). One file under two names prints `{remove} and {keep} are one file through a link: delete neither; replace the link with a real copy, then rerun pfm doctor` instead. A failed stat, a dangling link included, prints the `UNREADABLE` row in place of the row, with no delete.

## Paths and ordering

`{home}` is the operator home, `{store}` is `~/.claude`, `{cfg}` is the runtime config path, `{legacy}` is `config.LegacyConfigDir`, `{acct}` is one roster account's `ConfigDir`, `{acct1}` is the lowest-ID account's directory, and `{managed}` is `~/.local/share/pfm/install`. Configured database targets are `{StateDB}` and `{CacheDB}`. `paths.LegacyStateDB` is `~/.cc/fleet.db`; `paths.LegacyCacheDB` is `~/.local/state/pfm/fleet.db`.

Detectors run in the table's order. Accounts are visited by ID, shared/account entries in installer list order, and directory names and MCP names lexically. Shared physical paths are inspected once where the check calls for deduplication. A real entry is present and is not a symlink. Manual merge fixes for real shared entries are chosen per `StoreEntries` entry.

## Detectors

The table gives one row per detector; exact problems and fixes follow under each check id.

| Check | Severity | Looks at | Problem | Fix |
| --- | --- | --- | --- | --- |
| `legacy-config` | BLOCK | legacy config files | config outside clone | move or compare/remove |
| `legacy-harvester-config` | BLOCK | legacy harvester config | config outside clone | move or compare/remove |
| `pre-split-config` | BLOCK | raw config and sibling | old name, key or port | rename, split or edit |
| `legacy-state-db` | BLOCK | legacy state and siblings | old database path | move or remove |
| `legacy-cache-db` | BLOCK | legacy cache and siblings | old database path | move or remove |
| `legacy-harvester-cache` | WARN | default legacy cache dir | old cache name | move or remove |
| `pfm-settings` | BLOCK | physical user settings | duplicate pfm launch values | remove owned keys |
| `pfm-mcp` | BLOCK | registries and ownership | pfm servers outside launch | remove owned entries |
| `memory-helpers` | BLOCK | fingerprinted scripts | retired helper name | rename and edit hooks |
| `staged-shim` | BLOCK | shell source lines | retired staged shim | delete old source line |
| `staged-prompts` | WARN | managed prompt dir | retired prompt staging | remove when unused |
| `shared-db` | WARN | old shared database | retired file | remove |
| `stray-dir` | WARN | non-account `.cc` dirs | stray directory | inspect/remove |
| `account-is-store` | BLOCK | physical account path | identity dir is store | unlink or configure |
| `store-identity` | BLOCK | account entries in store | identity inside store | move or inspect/remove |
| `home-state-file` | WARN | home Claude state | launch without account env | compare account/remove |
| `account-entry-real` | BLOCK | real shared account entries | data outside store | entry-specific merge |
| `unclassified` | WARN | unknown top-level names | neither entry list | keep |
| `third-party-mcp` | WARN | user-level MCP names | server outside pfm config | edit `mcp.thirdParty` |
| `stale-state-tmp` | WARN | old state temp files | older than 24 hours | remove |
| `beside-backup` | WARN | backup-pattern names | adjacent backup | remove when unneeded |

## Detector details

### legacy-config

**Severity:** BLOCK

Looks at `{legacy}/pfm.config.json`, `{legacy}/config.json`; skipped when ConfigExplicit.

Problem: `legacy pfm config outside the clone`.

Fix: target `{cfg}` absent: `mv {path} {cfg}`; present: `diff {path} {cfg} && rm {path}`.

### legacy-harvester-config

**Severity:** BLOCK

Looks at `{legacy}/harvester.config.json`.

Problem: `legacy harvester config outside the clone`.

Fix: target `dir({cfg})/harvester.config.json` absent: `mv {path} {target}`; present: `diff {path} {target} && rm {path}`.

### pre-split-config

**Severity:** BLOCK

Looks at raw JSON of `{cfg}` and `dir({cfg})/config.json`; one row per finding.

Problem: (a) `{cfg}` base name is `config.json`: `the config still has its pre-split name`; (b) a `config.json` beside a `pfm.config.json`: `a pre-split config.json beside the config`; (c) key `mcp.servers.harvester`: `mcp.servers.harvester belongs in harvester.config.json`; (d) `mcp.http.port` is 8377: `mcp.http.port is the pre-split default 8377`.

Fix: (a) `mv {cfg} {dir}/pfm.config.json`; (b) `rm {path}` once its content is in `{cfg}`; (c) `move mcp.servers.harvester.enabled ({value}) to "enabled" in {dir}/harvester.config.json, then delete mcp.servers.harvester from {cfg}`; (d) `set mcp.http.port to 18377 in {cfg}; pfm install re-wires every client`.

### legacy-state-db

**Severity:** BLOCK

Looks at `paths.LegacyStateDB(home)` and its `-wal`, `-shm`; skipped when it is physically `{StateDB}`.

Problem: `legacy state database at the pre-layout path`.

Fix: `{StateDB}` absent: `close every chat, stop pfm's services, then: mv {legacy} {StateDB}` plus `&& mv {legacy}-wal {StateDB}-wal` / `-shm` for each sibling present (pfm migrates the schema on its next open); present: `both exist — keep {StateDB}: rm {legacy} {legacy}-wal {legacy}-shm`.

### legacy-cache-db

**Severity:** BLOCK

Looks at `paths.LegacyCacheDB(home)` and siblings, same rules against `{CacheDB}`.

Problem: `legacy cache database at the pre-layout path`.

Fix: same shape with `{CacheDB}`.

### legacy-harvester-cache

**Severity:** WARN

Looks at `paths.LegacyHarvesterCacheDir(home)` when HarvesterCacheDir is "".

Problem: `pre-rename harvester cache dir`.

Fix: target absent: `mv {path} {paths.HarvesterCacheDir(home)}`; present: `rm -r {path}` (the new cache is kept).

### pfm-settings

**Severity:** BLOCK

Looks at each physical `settings.json` among `{store}` and every `{acct}`, once per physical file.

Problem: `carries pfm {keys} — they ride --settings at launch and would run twice` (keys from `installer.PFMSettingsLeftovers`).

Fix: `remove {keys} from {path} (pfm's hook commands only; keep every other key)`.

### pfm-mcp

**Severity:** BLOCK

Looks at every file from `installer.ClaudeUserRegistries(home, accounts, "")`, `{home}/.claude.json`, `{home}/.mcp.json`, once per physical file; plus `{managed}/mcp-ownership.json` `clients`.

Problem: `carries pfm mcpServers.{names}`; ledger: `mcp-ownership.json still records pfm clients`.

Fix: `remove mcpServers.{names} from {path}; pfm's server rides --mcp-config at launch`; ledger: `remove "clients" from {path}`.

### memory-helpers

**Severity:** BLOCK

Looks at `{dir}/scripts/cc-memory-wire.sh`, `cc-memory-consolidate.sh` for `{store}` and every `{acct}`; a regular file whose normalized fingerprint matches.

Problem: `pfm's memory helper under its retired name`.

Fix: `mv {path} {dir}/scripts/{new}, then replace {path} with {dir}/scripts/{new} in every hook command of {dir}/settings.json`.

### staged-shim

**Severity:** BLOCK

Looks at `{home}/.zshrc` lines where `installer.IsStagedShimLine(line)`.

Problem: `sources pfm's retired staged shim`.

Fix: `delete the line "{line}" from {home}/.zshrc; pfm install writes the clone's source line`.

### staged-prompts

**Severity:** WARN

Looks at `{managed}/harness-prompts`.

Problem: `retired staged prompt dir`.

Fix: `rm -r {path} once no chat started before the move is open`.

### shared-db

**Severity:** WARN

Looks at `{home}/.local/state/pfm/shared.db`.

Problem: empty: `retired empty shared.db`; else `retired shared.db holds {n} bytes`.

Fix: `rm {path}`.

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

Looks at each `AccountEntries` entry in `{store}`; for `state`, only `state/mcp-discover-verdicts.json` is checked. `{acct1}` is classified by `claudelaunch.InspectConfigDir`, as in `account-is-store`.

Problem: `{entry} is account identity inside the store`.

Fix: `{acct1}` resolving to the store makes `{path}` and `{acct1}/{entry}` one file, so the fix deletes neither:

- `{acct1}` a symlink: `[ ! -L {acct1} ] || { rm {acct1} && mkdir -m 700 {acct1}; } && ` followed by the move below. That prefix is `account-is-store`'s own fix, guarded so the line also runs after that fix has, and a later run of that fix refuses on the real dir without deleting anything.
- `{acct1}` a real dir in the store: `apply account-is-store's fix for {acct1} first; pfm doctor then names this entry's move`.

Otherwise, `{acct1}/{entry}` absent: `mkdir -m 700 -p {acct1} && mv {path} {acct1}/{entry}` (for `state`, `mkdir -m 700 -p {acct1} {acct1}/state`), runnable before account 1 exists; present: `keep {acct1}/{entry}; after checking, rm -r {path}`, through `removeKeeping`.

### home-state-file

**Severity:** WARN

Looks at `{home}/.claude.json`.

Problem: `a Claude launched without CLAUDE_CONFIG_DIR wrote this state file`.

Fix: `check it names the same oauthAccount as {acct1}/.claude.json, then rm {path}`.

### account-entry-real

**Severity:** BLOCK

Looks at each account × shared entry: a real file or dir at `{acct}/{entry}`; skipped for an account `account-is-store` reports.

Problem: `{entry} is a real {dir|file}; it belongs in the store`.

Fix: the entry-specific rule below.

For `account-entry-real`, the fix is selected by entry. Each fix is one shell line whose delete is the last link of an `&&` chain, so it runs only after the merge into the store succeeded. The trailing `#` comment carries the prose:

- `projects`, `file-history`, `tasks`, `session-env`, `paste-cache`, `shell-snapshots`, `plans`, `uploads`, `downloads`, `teams`, `agents`, `commands`, `skills`, `rules`, `themes`: `mkdir -p {store}/{entry} && cp -an {path}/. {store}/{entry}/ && ! diff -rq {path} {store}/{entry} 2>&1 | grep -v '^Only in {store}/{entry}' && rm -r {path}  # union into the store; stops while a file differs`. A differing file, or a diff error, stops the `rm`.
- `history.jsonl`: `jq -c -s 'sort_by(.timestamp)[]' {store}/history.jsonl {path} > {store}/history.jsonl.new && mv {store}/history.jsonl.new {store}/history.jsonl && rm {path}  # interleaved by timestamp`.
- `plugins`: `rm -r {path}  # the store keeps its copy (reinstallable)`.
- `settings.json`: `jq -e -s '.[0] as $s | .[1] | to_entries | all(.key as $k | ($s | has($k) | not) or $s[$k] == .value)' {store}/settings.json {path} > /dev/null && jq -s '.[0] * .[1]' {store}/settings.json {path} > {store}/settings.json.new && mv {store}/settings.json.new {store}/settings.json && rm {path}  # adds the keys the store lacks; stops while a key differs`.
- `CLAUDE.md`: `cat {path} >> {store}/CLAUDE.md && rm {path}  # appended whole; prune {store}/CLAUDE.md as you like`.
- `stats-cache.json`, `.last-cleanup`, `.last-update-result.json`, `gh-pr-status-cache.json`: `rm {path}  # a cache`.

### unclassified

**Severity:** WARN

Looks at top-level names in `{store}` and every `{acct}` on neither list, not ignored, not matched by the two leftover checks.

Problem: `UNCLASSIFIED — on neither the shared nor the per-account list`.

Fix: `keep it; pfm doctor names it until a pfm release classifies it`.

### third-party-mcp

**Severity:** WARN

Looks at top-level `mcpServers` names in every `{acct}/.claude.json` and `{home}/.claude.json` that `pfm-mcp` does not report.

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
