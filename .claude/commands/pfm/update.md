---
name: pfm:update
description: 'MANDATORY — Installs Professor and plugins here — load before any `make host-install`, `pfm install` or plugin repoint; `/pfm:update` runs it end to end, `/pfm:update check` stops at the repoint plan, committing nothing. Returns the update report. Not for `pfm update {adopt|pin|…}` → /pfm.'
argument-hint: '[check]'
---

# PFM Update

You run this host's update. `pfm update {adopt|pin|ignore|drop|accept}` is another thing: the project-tier update an adopter's session runs (`/pfm`); this command never calls it.

## Constants

- `REPO`: this checkout, `~/.professor` on this host; every command names it by absolute path. `{project}` = its directory name minus the leading dot. Each step's output and exit codes are saved to `/tmp/{project}/update/{step}.txt`, each exit read before its output.
- `REV`: the commit the installed pfm was built from, `go version -m ~/.local/bin/pfm | sed -n 's/.*vcs.revision=//p'`; empty when the binary carries none.
- Build: `VERSION` reading `{X.Y.Z}-alpha` is an alpha build; anything else is stable (§ Stable).
- Plugins: `jq -r '.plugins[].name' REPO/.claude-plugin/marketplace.json`, each the submodule `plugins/{p}`. An empty list is the read failing: STOP. A `git -C REPO/plugins/{p}` read counts only once `git -C REPO/plugins/{p} rev-parse --show-toplevel` prints `REPO/plugins/{p}`: an uninitialised checkout is an empty directory whose git reads answer for REPO.
- Plugin delivery: `pfm install` on an alpha build copies each plugin's checkout working tree, `plugins/{p}/plugins/{p}`, uncommitted edits included, into its `-dev` copy `~/.local/share/{p}-dev/plugins/{p}`, then runs `claude plugin update {p}@{p}-dev --scope {scope} -y` for each `{p}@{p}-dev` entry in `~/.claude/plugins/installed_plugins.json` whose recorded version is behind the checkout's `plugin.json` and reads the version back. Chats run the `-dev` copy: marketplace `{p}-dev` is a `directory` source, so Claude Code reads the plugin from that folder (`CLAUDE_PLUGIN_ROOT` points there), never from the entry's `installPath` under `{config}/plugins/cache/` (`{config}` an account's config dir), and a checkout edit reaches chats through the copy refresh alone, no version bump. Every account's `plugins/` links to `~/.claude/plugins`, so marketplaces and record are shared.
- An alpha build installs every plugin from its checkout on disk, released or not: unpushed commits, uncommitted edits and a missing alpha tag install as they sit, and no plugin's local state STOPs the run. A plugin's gitlink moves to its latest alpha before the install, unless it already contains it, only as a clean fast-forward of a clean checkout; any other checkout installs as it is, its gitlink left where it is. Latest alpha of a plugin: its newest alpha tag (`git -C REPO/plugins/{p} tag -l --sort=-v:refname '*-alpha*' | head -1`), else its `origin/develop` head, whose version reads `-dev.N` or the next patch. Creating or pushing a tag is publication (`CLAUDE.md` § Publication): this command never tags; the report names each plugin without an alpha tag, its tagging the user's call.
- Git writes go to `gitter`. Every brief says "no Co-Authored-By or other attribution trailer", checked after: `git -C REPO log -1 --format=%B` exits 0 and `grep -ci co-authored` over its output prints 0. A push runs only on the user's explicit ask in this turn.

## Disturbing vs harmless

| With chats open | Change | Effect |
| --- | --- | --- |
| harmless | `-dev` copy refresh | what chats run: a new chat or an open chat's `/reload-plugins` loads it; a running chat's next hook call can already run the new files |
| harmless | callmeter binary swap | atomic rename, the old one kept as `.prev` |
| harmless | `claude plugin update`, `install`, `marketplace add` | a `-dev` plugin: the install record and an unread cache dir move, chats keep reading the folder; a released `{p}@{p}`: the old cache dir stays, marked `.orphaned_at`, a running chat keeps executing it, a new chat gets the new version, an open chat on `/reload-plugins` or restart |
| harmless | `enabledPlugins` edits | pfm re-reads `settings.json` just before its rename, so a chat's concurrent write survives |
| disturbing, gated | store link repoint, store migrations | shared session and project entries move under a live chat |
| disturbing, gated | fullscreen canary clear | not investigated: the gate stays |

pfm runs every harmless change with chats open and holds back only the gated ones. Before obeying a row that says close chats or rerun elsewhere, find what its gated action touches: one this table marks harmless is reported; any other is a user action.

## Steps

Each step ends on its check. A STOP ends the run with § Report so far; a step never skips silently. Each read is taken at run time: a result from a prior report or turn is never carried.

1. Sync from origin, every mode, `check` included. The user's word in this turn not to sync, pull or fetch skips the fetch and every move, named in § Report; the guards still run. The guards hold before any HEAD or working tree moves, and nothing is pushed.
   - Branch: `git -C REPO branch --show-current` prints `develop`, else STOP.
   - In-progress operation: `git -C REPO rev-parse -q --verify MERGE_HEAD` (likewise `REBASE_HEAD`, `CHERRY_PICK_HEAD`) prints nothing. One present belongs to another chat unless the user named it yours: STOP, naming `git -C REPO name-rev --name-only MERGE_HEAD`; never abort or continue it.
   - Foreign edits: `git -C REPO status --porcelain` lists each dirty path as another chat's work, left as it is: never stash, reset, `git submodule update` or check out over it.
   - Plugin checkouts, dirty: per initialised plugin, `git -C REPO/plugins/{p} status --porcelain`; a listed path, or the read failing, records the checkout dirty with its dirty-path count. A dirty checkout installs as it is, its edits included, and nothing moves, merges, checks out or resets over it.
   - Fetch: `gitter` freeform `git -C REPO fetch origin --tags`, then per initialised plugin `git -C REPO/plugins/{p} fetch origin --tags`; remote-tracking refs and tags only, the working trees untouched. A failed REPO fetch STOPs; a failed plugin fetch records that plugin unfetched: never moved, installed from disk, named in § Report.
   - Plugin checkouts, ahead: a checkout whose HEAD differs from its gitlink (`+` in `git -C REPO submodule status`) and is off `origin/develop` (`merge-base --is-ancestor HEAD origin/develop` fails) holds commits origin lacks, recorded with their count (`git -C REPO/plugins/{p} rev-list --count origin/develop..HEAD`): it installs from disk and never moves; sync never rewrites, parks or pushes them.
   - REPO: `gitter` `git -C REPO merge --ff-only origin/develop` on `develop`; foreign edits ride along, and a refused fast-forward STOPs naming REPO.
   - Submodules: a plugin with `-` in `git -C REPO submodule status` is uninitialised (engine-sync's fails `make host-install` with `no engine-sync checkout at …`); `gitter` runs `git -C REPO submodule update --init plugins/{p}` for exactly those, which have no working tree to overwrite. Unsynced, the report names them instead, their plan unread.
   - Plugin checkouts to the pulled gitlink: per plugin, `G` = `git -C REPO ls-tree HEAD plugins/{p}`. HEAD at or past `G` (`merge-base --is-ancestor G HEAD`) stays for step 3; HEAD behind it (`merge-base --is-ancestor HEAD G`) on a clean checkout moves, `gitter` running `git -C REPO/plugins/{p} merge --ff-only G` on its `develop` or `checkout --detach G` when detached; a dirty one behind, a diverged one or a refused move is recorded with `G` and HEAD, installed from disk and not moved.
   - Check: `git -C REPO merge-base --is-ancestor origin/develop HEAD` exits 0, no plugin shows `-` in `git -C REPO submodule status`, each plugin HEAD is at or past its `G` or recorded installed from disk (dirty, ahead, diverged, unfetched), nothing pushed.

2. Preflight, read-only.
   - Load: `uptime` and `nproc`; the 1-minute load and its per-core figure go in the report and on every red that follows. Near 50 the load fakes install-check refusals and gate TIMEOUTs on this host: wait for normal load, or go on.
   - Host check: `pfm install --check --plan`. Exit 0 is clean; 4 is standing BLOCK rows, reported verbatim, then STOP: a BLOCK is never overridden, and a move-aside it names runs only on the user's word, the data kept; any other exit, 1 included, is the check broken, not the host clean: STOP.
   - Stale binaries: `pgrep -x pfm` exits 0, or 1 when none runs, any other exit a failed read; then `for pid in $(pgrep -x pfm); do echo "$pid $(readlink /proc/$pid/exe) $(ps -o etimes=,args= -p $pid)"; done`: a `(deleted)` target is a process on a replaced binary, an empty target unreadable, never current.

3. Repoint, alpha only.
   1. Remote read, per plugin. Synced, step 1's fetch is the read, never a second fetch. Unsynced, every mode reads without a ref write: `git -C REPO/plugins/{p} ls-remote origin refs/heads/develop` gives the `origin/develop` head, `ls-remote --tags --sort=-v:refname origin '*-alpha*'` the alpha tags, its first line the newest tag's commit (an annotated tag's `^{}` line sorts first). A failure records that plugin installed from disk, pin unchanged, the failure named; an empty tag list at exit 0 is no alpha tag.
   2. Per plugin: `G` = the gitlink (`git -C REPO ls-tree HEAD plugins/{p}`), `T` = its latest alpha's commit (`git -C REPO/plugins/{p} rev-parse {tag|origin/develop}^{commit}`; unsynced, step 3.1's newest-tag commit, else its `origin/develop` head). Unchanged when the checkout HEAD is at `G` and `G` contains `T` (`git -C REPO/plugins/{p} merge-base --is-ancestor T G`, `T` = `G` included); a `G` past `T` is ahead by `git -C REPO/plugins/{p} rev-list --count T..G` commits, unpushed when `T` is the `origin/develop` head, and installs as it is. A checkout step 1 recorded (dirty, ahead, diverged, unfetched) or with HEAD past `T` (`merge-base --is-ancestor T HEAD`, HEAD ≠ `T`) is `installed from disk, pin unchanged`. Otherwise the plugin moves, and both checks must hold, else it is `installed from disk, pin unchanged`, naming `G`, `T` and the failed check: `git -C REPO/plugins/{p} merge-base --is-ancestor G T`, so a pin never moves backwards or sideways; `merge-base --is-ancestor T origin/develop`, so the target is on the alpha line. Unsynced, `origin/develop` is step 3.1's head, and a check needing a commit absent locally (`git -C REPO/plugins/{p} cat-file -e {sha}^{commit}` fails) reads `unverified: needs the fetch`, never a pass, step 3.3's diff included.
   3. Per moving plugin, its host-run shell: `git -C REPO/plugins/{p} diff --name-only G T | grep -E '\.(sh|bash)$|(^|/)hooks/'`, carried to § macOS; a `git diff` exiting non-zero, unless step 3.2 marked it `unverified`, drops that move (`installed from disk, pin unchanged`, the failure named) and never reads as no shell.
   4. `/pfm:update check` stops here and reports the plan, its `unverified` checks named. A full run goes on whatever any plugin's state; a move with an `unverified` check is skipped, that plugin `installed from disk, pin unchanged`.
   5. `gitter` COMMIT in REPO on `develop`, only for the moves step 3.2 cleared: per moving plugin, re-check its checkout clean, then `git -C REPO/plugins/{p} merge --ff-only T` on its `develop` branch or `checkout --detach T` when detached; a checkout found dirty or a refused fast-forward drops that move, `installed from disk, pin unchanged`. Stage exactly the gitlinks that moved to their `T` (`git add plugins/{p}`), nothing else, never a gitlink to a commit the plugin's origin lacks; message `chore(plugins): repoint {p} {G7}→{T7}[, …] to their latest alpha`; no move, no commit.
   - Check: per moved plugin `git -C REPO ls-tree HEAD plugins/{p}` and `git -C REPO/plugins/{p} rev-parse HEAD` both print `T`; every other plugin's gitlink still prints `G`; the commit stays local.

4. Gate, in the fence, from REPO.
   - `REPO/.claude/scripts/test.sh --full`: the full gate, pfm and templates in one run.
   - The gate covers every sibling's uncommitted edits in REPO: a red in a path this update did not touch is named as a sibling's, never fixed here.
   - A TIMEOUT whose PROFILE line attributes CONTENTION is load: rerun once at normal load. A skipped or filtered step is a named gap, never a pass.
   - A gate run on a flight worktree instead: `chmod 644 templates/generated/agents/*.md` first; the fence mounts read-only and a checkout writes 0664.
   - Check: the gate reads PASS, else STOP; a red `develop` is never installed. Only the user's explicit word in this turn skips the gate, a running one stopped: a named gap in § Report, never a pass.

5. Host build: `make -C REPO/pfm host-install`.
   - `install-guard: REFUSED`: the installed pfm carries patches HEAD lacks, typically another chat's worktree build. Report the refusal verbatim with `git -C REPO branch -a --contains REV` and what installing would drop; `install-guard: cannot verify` is an unverified downgrade check, never a pass. A refusing install check is reported verbatim. `FORCE=1` and `SKIP_INSTALL_CHECK=1` run only on the user's word.
   - `required dependency preflight failed` at high load: check `uptime`, retry once at normal load before chasing a dependency.
   - A refusal over a stray `~/.claude/.claude.json` comes from a `claude` run with `CLAUDE_CONFIG_DIR=$HOME/.claude`: a BLOCK under step 2's rule. Any `claude` command this update runs keeps the chat's own `CLAUDE_CONFIG_DIR`.
   - Check: `pfm version` prints `pfm {VERSION}`; rerun step 2's stale-binary loop and report the `(deleted)` processes by count, oldest age and pid. A `pfm ls` picker among them rewrites pre-fix state on every refresh, so no verification counts until it restarts: name its pid and age and ask the user to restart it; the others need no restart.

6. Install: `pfm install --yes`. Read every row:
   - The `summary changed=… ok=… skipped=…` line and its FAIL count; a non-zero exit is red.
   - BLOCK rows: verbatim, never overridden.
   - A store repoint, store migration or canary clear skipped for an account with live chats is deferred, not done. This update never ends a chat.
   - A plugin FAIL row is red.
   - Install record, read back, never repaired by hand: pfm's `run CLAUDE_CONFIG_DIR=… claude plugin update …` change rows are the report's record moves. Per plugin, every `{p}@{p}-dev` entry's `version` in `~/.claude/plugins/installed_plugins.json` equals `jq -r .version REPO/plugins/{p}/plugins/{p}/.claude-plugin/plugin.json`, else red naming both; either file unreadable or no entry is red, never a match.
   - callmeter: the install builds `~/.local/state/callmeter/bin/{version}/callmeter` from the checkout before its copy refresh and `claude plugin update`; a copy ahead of that file makes the wrapper download a published binary the stale `SHA256SUMS` rejects, and every hook drops its event silently.
   - The gateway and callmeter stay independent: an update never wires the gateway to callmeter, pfm or Professor; a step or row that would is refused and routed to the user.

7. Verify.
   - `pfm doctor`: exit 0 is clean; 1 is warnings only (`doctor: warnings=N`), each warning row verbatim; 3 is a FAIL, each FAIL row verbatim; any other exit is the doctor broken, never clean. On alpha a plugin off its `-dev` copy is a FAIL, and so is a record entry whose version differs from the checkout's. Its Codex role rows (installed, SYMLINK, MISMATCH, MISSING, UNREADABLE) show the roles rewritten: a role changes only at `pfm install`.
   - Herdr: a `herdr-integration: FAIL need=no bundled {engine} integration` row is Herdr's own integration holding the pane's agent state and locking pfm's reporter out. Run its printed `fix:` command verbatim, env prefix included, then `pfm doctor` again until the row clears. The uninstall edits `{config}/settings.json`: after it the file is still a symlink to `~/.claude/settings.json` (`readlink`) and `jq empty` reads it at exit 0, else red.
   - Plugin copies, what chats run: per plugin `diff -rq --exclude=node_modules --exclude=.DS_Store REPO/plugins/{p}/plugins/{p} ~/.local/share/{p}-dev/plugins/{p}` prints nothing; a missing directory is a FAIL, never a match.
   - Plugin source: `claude plugin list --json` exits 0, and per plugin `jq -r '.[] | select(.id == "{p}@{p}-dev") | .readFromFolder'` over its output prints `{HOME}/.local/share/{p}-dev/plugins/{p}` (`{HOME}` expanded) on every line: Claude's own word that chats read the copy, so the plugin-copies diff is what they run. No line, or a line naming another folder or `null`, is red: that plugin runs its cache or another copy, and the copies diff says nothing about it. `jq -r '(.enabledPlugins // {}) | to_entries[] | select(.value == true and (.key | startswith("{p}@")) and .key != "{p}@{p}-dev") | .key' {config}/settings.json` prints no other enabled copy (an `enabled twice` doctor FAIL is that twin). Open chats load the refreshed copy on `/reload-plugins` in each, which loads every newly installed and updated plugin, so no restart is needed; whether an already-loaded plugin's registered tools pick up changed definitions is unverified. The user action reads `run /reload-plugins in each open chat (or restart it)`, never a failure.
   - Machine-global links: `find ~/.claude/agents ~/.claude/commands ~/.claude/skills -maxdepth 2 -type l`, each `readlink -f` target existing; report the count by target root, the dangling ones as FAIL. Zero links found is the enumeration failing: FAIL.
   - Engine mirrors: `engine-sync codex build REPO && engine-sync codex check REPO`, then `engine-sync opencode build REPO && engine-sync opencode doctor REPO`, clean.

## Stable

`VERSION` without `-alpha`: step 3 is skipped (`repoint: stable build, none`), and `check` stops there; `pfm install` puts each plugin on its released `{p}@{p}` copy; a `-dev` copy still enabled is a doctor row to report.

## macOS

The fence is Linux with GNU coreutils: host-run plugin shell can pass every gate and break on BSD/macOS. A plugin change carrying host-run shell needs its Mac run (plugin-lab on the Mac, `docs/dev/testing/plugin-lab.md` § Run) before it lands on the plugin's `develop`, upstream of this update. This host cannot reach the Mac: the report lists each moving plugin's step 3.3 files under `macOS unverified` for the user to run there.

## Report

```text
Host update {date} — {alpha|stable} {VERSION} @ {develop sha7}{ — check only}
Sync: branch develop · in-progress none|{op} · foreign dirty {n} · {REPO {old7}→{new7}|unchanged · {p} {old7}→{new7}|unchanged|initialised|from disk ({dirty {n}|{N} ahead of origin/develop|diverged from {G7}|fetch failed})[, …] · nothing pushed | fetch and moves skipped on the user's word}
Preflight: load {1-min} on {nproc} cores ({per-core}/core) · host check exit {n} · stale pfm {n} ({pids}), oldest {age}{, picker {pid}} | none
Repoint: {p} {G7}→{T7} ({tag|origin/develop, no alpha tag}){, unverified: needs the fetch} | {p} unchanged{, {N} ahead of {tag|origin/develop, unpushed}} | {p} from disk at {HEAD7} ({N} commits origin lacks, dirty {n}{, {why}}), pin {G7} unchanged | {p} uninitialised, plan unread
Commit: {sha7} on develop, not pushed | none
Gate: templates {PASS|FAIL} · pfm {PASS|FAIL|not needed}{ — gaps} | skipped on the user's word, gap: develop installed ungated
Build: pfm {version} · stale pfm {n} ({pids}), oldest {age}{, picker {pid}} | none
Install: exit {n} · {summary line} · record {p} {old}→{new}[, …] | record unchanged · {BLOCK/FAIL/skip rows verbatim}
Verify: doctor exit {n} · plugin copies {clean|drift} · source {folder|off folder {p}[, …]} · links {n} by root, dangling {n} · mirrors {clean|verbatim}
macOS unverified: {p}: {files} | none
User actions: {each, with the command}
```

A red, a deferral or a STOP is never reported as an installed host, and a dirty plugin checkout is never checked out over.
