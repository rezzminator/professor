# INSTALL — Install Professor

Two independent things live in this repo. Install what you need.

- **`pfm`** — the host fleet CLI: statusline, `/reload`, the `professor` MCP server (chat and harvester families), multi-account tooling. Binary or source, touches only your `$HOME`, no project files.
- **Professor, the discipline layer** — `CLAUDE.md`, agents, commands, the pipeline. Installed into YOUR project through a Claude-guided interview.

Shortest path first.

---

## Contents

- [Runtime prerequisites](#runtime-prerequisites-for-the-pfm-install-paths)
- [Binary install](#1-binary-install--pfm-only-2-minutes)
- [Build from source](#2-build-from-source--pfm-only)
- [Full Professor adoption](#3-full-professor-adoption--the-discipline-layer)
- [What gets written where](#what-gets-written-where)
- [Updating](#updating)
- [Uninstall](#uninstall)

---

## Runtime prerequisites for the `pfm` install paths

Paths 1 and 2 use the same host runtime. Both require Linux or macOS on `amd64` or `arm64`, plus `tmux` ≥ 1.8, `git`, `jq`, a POSIX `sh`, `bash`, `zsh`, and `sleep`. Linux also requires `setsid`; macOS requires `ps`, `lsof`, and `launchctl`. `systemd` on Linux is optional when user units are unavailable, but the scheduler surface cannot be enabled without it.

The `claude` and `codex` executables are not installed by `pfm`. Their self-doctors are optional engine diagnostics even when accounts are configured: a broken engine capability stays visible, but it cannot block unrelated host installation. Use `--skip-engine codex` to skip the Codex probe and leave Codex mirror and hook surfaces unmanaged for this run.

## 1. Binary install — `pfm` only (2 minutes)

No clone, no Go toolchain. The installer's own assets (command cards, launcher shim, scheduler units) are embedded in the binary.

Prerequisites: the shared runtime listed above and `git` (to resolve the latest tag — or read it off the [Releases page](https://github.com/rezzminator/professor/releases) by hand). The binary path does not require a Go toolchain or access to a Go module proxy.

```bash
REPO=rezzminator/professor
TAG=$(git ls-remote --tags --sort=-v:refname "https://github.com/${REPO}.git" 'v*' \
  | grep -v '\^{}' | head -1 | sed 's#.*/##')
OS=linux      # or darwin
ARCH=amd64    # or arm64
BINARY="pfm_${TAG}_${OS}_${ARCH}"
BASE_URL="https://github.com/${REPO}/releases/download/${TAG}"

curl -fLO "${BASE_URL}/${BINARY}"
curl -fLO "${BASE_URL}/SHA256SUMS"
awk -v file="${BINARY}" '$2 == file' SHA256SUMS > "${BINARY}.sha256"
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum -c "${BINARY}.sha256"
else
  shasum -a 256 -c "${BINARY}.sha256"
fi

mkdir -p "$HOME/.local/bin"
install -m 0755 "${BINARY}" "$HOME/.local/bin/pfm"
```

The checksum only catches a corrupted/incomplete download — releases don't publish a separate signature.

For a filtered network that cannot reach a Go module host or proxy, use this binary path: it only needs access to the release assets and the Git tag lookup above, not the module downloads needed by a source build.

### Preview, optional components, and harvest footprint

Bare `pfm install` is a read-only preview. Review its planned writes and the harvest line before applying the identical flag set with `--yes`:

```bash
pfm install --skip-harvest --skip-engine codex --skip-themes
pfm install --yes --skip-harvest --skip-engine codex --skip-themes
```

- `--skip-harvest` leaves the pinned harvestpy runtime unmanaged; it avoids the harvest download and its disk footprint. It does not hide a failed provision.
- `--skip-engine codex` suppresses the Codex dependency probe and Codex mirror/hooks. It does not alter Claude or OpenCode surfaces.
- `--skip-themes` suppresses theme installation. Theme entries come from `templates/themes/sources.json`: the source-fetched Tokyo Night (`~/.claude/themes/tokyo-night.json`) and the bundled per-account overlays `professor-{gold,silver,bronze}.json` beside it (Tokyo Night with one input-bar colour each, merged at install).

The current embedded harvest plan is measured, not a promise for every host. On Linux `amd64`, the cold package closure is about **3.1 GB** (3,106,174,573 bytes) to download and about **5.8 GB** (5,786,939,761 bytes) installed. The uv and CPython bootstrap archives add roughly 57 MB, and temporary files or caches can require more free space. Other platforms and future lock revisions vary; the preview is the authoritative plan for the host.

Theme fetch failures are visible nonfatal skips, so the rest of the install can continue. A locally modified theme is preserved and reported as drift rather than overwritten. On uninstall, only theme files recorded as installer-owned in the ownership ledger are removed.

Add `$HOME/.local/bin` to `PATH` if it isn't already, then:

```bash
pfm install             # preview — the default mode, no writes
pfm install --yes       # apply the preview
pfm install --vscode    # opt-in preview: the Professor VS Code extension (Extensions view + terminal `+` dropdown entry) and the PFM default terminal; press Ctrl+Shift+Alt+T (macOS: Cmd+Shift+Alt+T), run **Professor: New Chat Terminal**, or pick **Professor** from the terminal `+` dropdown — all three give the next icon and colour; the default `+` terminal is `PFM`.
pfm install --yes --vscode
```

`pfm install --yes` manages eight surfaces, all under `$HOME`; `--vscode` adds a ninth:

1. Staged assets — `~/.local/share/pfm/install/`, including the `claude` launcher; since that launcher disables Claude Code's own version cleanup, pfm also owns retention under `~/.local/share/claude/versions/` — `pfm doctor` reports count, bytes, and prunable size, and `pfm install` previews and applies the prune (`pfm/TESTPLAN.md` § claude-versions)
2. Command symlinks — `~/.claude/commands/` (`/reload`); skill symlinks — `~/.claude/skills/` (`/handoff`)
3. The `pfm-name-sync` scheduler — three systemd user units (Linux) or one launchd agent (macOS)
4. Claude launch settings — hooks, status line, and MCP servers ride each launch; `pfm install` strips pfm-owned legacy entries from account `settings.json` and `.claude.json` files while preserving unrelated settings, and manages `cleanupPeriodDays` through Claude managed settings with sudo when required
5. `~/.codex/prompts/`, `~/.codex/skills/`, and `~/.codex/agents/` — Codex mirrors generated from the installed global Claude commands and host-global agent sources; a role lands in `agents/` as a REGULAR FILE, because Codex opens a role with `O_NOFOLLOW` and rejects a symlink as "agent type is currently not available". Only marker-owned outputs are replaced or retired, while unmarked conflicts survive and stop the install by name
6. `~/.codex/hooks.json` — migrates surviving binary paths and removes retired clear-kill and Dream/STM hooks; it installs no automatic Codex hook
7. One source line appended to `~/.zshrc` — restart your shell (or `source ~/.zshrc`) for it to take effect
8. `~/.claude/themes/` — the themes declared by `templates/themes/sources.json`, source-fetched and bundled; a failed cosmetic fetch or an unreadable bundled file is reported and skipped without aborting the other surfaces
9. **Opt-in:** VS Code — links the Professor extension (Professor's assistant in VS Code) into `extensions/professor` of every VS Code product present (`~/.vscode`, `~/.vscode-insiders`, `~/.vscode-oss`, `~/.vscode-server`, `~/.vscode-server-insiders`, a portable install) and registers it in that product's own `extensions/extensions.json` — the file modern VS Code actually scans user extensions from, so a link alone is never loaded — and in the user or remote-machine `settings.json` adds a `PFM` terminal profile (icon `mortar-board`, colour magenta) and selects it as the platform default (the extension's own `Professor` profile stays in the + dropdown — a default an extension contributes would make every window reload drop the open terminals). After a reload, the extension is visible as **Professor** in the Extensions view and a **Professor** entry in the terminal `+` dropdown. Press Ctrl+Shift+Alt+T (macOS: Cmd+Shift+Alt+T), run **Professor: New Chat Terminal**, or pick **Professor** from the terminal `+` dropdown — all three give the next icon and colour; the default `+` terminal is `PFM`. A PFM terminal opens a login zsh, then the installed shim opens the PFM picker at the shell's first prompt; each tab carries its chat's live name. PFM edits JSONC surgically, so comments and unrelated profiles survive; later installs retain ownership, and uninstall removes only the links (and the index entries they registered) still pointing at PFM's copy, restoring the prior default unless the operator changed it after installation. Reload the VS Code window once to load a newly linked extension. `pfm doctor` reports one row per product (link, index registration, version) and per owned settings file.

Every rewritten file is backed up before it is touched. `pfm install` classifies the HostLayout and migrates the clone config, both databases, the shared Claude session store, and legacy account files with recovery receipts. `pfm install --rollback ID` reverses the named journaled migration.

Run `pfm` for the interactive picker. Its colors are enabled independently of inherited `NO_COLOR` or `CLICOLOR=0`; `pfm ls --plain` and `pfm ls --tsv` remain uncolored. The managed terminal profile uses `PFM_AUTO_OPEN=pfm` to open the picker once at the first prompt.

The Professor `cc*` shell commands are retired. Use `pfm`, `pfm chat open <target>`, and the picker's account selector. Installation removes the named legacy launch/account scripts, backing up regular files outside `PATH` under `~/.local/state/pfm/retired-commands/`; source the updated shim or start a new shell to unload old functions and aliases. Account credentials, transcripts, live chat socket names, and the system C compiler are preserved.

Optional `cc-memory-wire.sh` and `cc-memory-consolidate.sh` helpers become `memory-wire.sh` and `memory-consolidate.sh`. Installation migrates recognized historical copies and exact hook paths without executing either helper or changing memory data. Customized helpers, conflicting destinations, and unsupported hook commands stop migration with an error; hosts without these helpers remain opted out.

**Known gate — read before you run it.** A mutating install refuses with exit 97 only while PFM's name-sync job is actively running, so it cannot replace the job or binary mid-execution. It asks before its first change and again just before the installer's own writes; the name-sync units the install stops start again only after those writes, so the install never fires the job itself. Only an install with layout changes to make stops the units, before its first change, and asks again once they are down, so a job already running refuses before any change, with exit 97 too; the managed-settings drop-in alone is no such change. From before that stop until the units are back, an interrupt (Ctrl-C, SIGTERM, SIGHUP, or SIGPIPE from a stdout or stderr whose reader died) finishes the current step, runs no later one, starts the units it stopped, then exits 128 + the signal's number; a signal ignored at launch (`nohup`) stays ignored. A Ctrl-C during the installer's own run also stops the installer step in flight: that run reports failure, and the stopped units still start. A migrated host's routine install stops no unit: a job its schedule starts between the two asks refuses at the second, before the installer's own writes; rerun once it finished. `pfm install --check` answers the first ask's refusal with exit 4. Two refusals come only after the stop, races the preview cannot answer: a name-sync job its schedule started between the two asks (exit 97), and on macOS a launch agent still tearing down 25 s after its bootout (exit 1). On Linux, wait or run `systemctl --user stop pfm-name-sync.service`; on macOS, wait or run `launchctl bootout gui/$(id -u)/com.professor.pfm.name-sync`. The preview remains read-only.

---

## 2. Build from source — `pfm` only

```bash
REPO=rezzminator/professor
SOURCE_DIR="$HOME/.professor"
TAG=$(git ls-remote --tags --sort=-v:refname "https://github.com/${REPO}.git" 'v*' \
  | grep -v '\^{}' | head -1 | sed 's#.*/##')
git clone "https://github.com/${REPO}.git" "$SOURCE_DIR"
git -C "$HOME/.professor" checkout "$TAG"
mkdir -p "$HOME/.local/bin"
GOPROXY=https://proxy.golang.org go -C "$HOME/.professor/pfm" build -trimpath -ldflags "-X main.version=$TAG" -o "$HOME/.local/bin/pfm" ./cmd/pfm
```

Set `GOPROXY` to a Go module proxy reachable from your network. The source path needs Go **1.27.1 or newer**, the floor declared by `pfm/go.mod`, and access to the module host or proxy. The tag lookup and explicit checkout keep the source and the binary on the same latest release; if the source directory already exists, fetch and check out that tag there instead of cloning over it.

The source build has the same harvest cost and opt-outs as the [preview/apply block above](#preview-optional-components-and-harvest-footprint). Then run the same two commands as the binary path, from inside the clone — that is how `pfm install` records it as your source repository, which `pfm init` and `pfm update` both read:

```bash
cd "$HOME/.professor"
pfm install
pfm install --yes
```

Same eight base surfaces, the same optional VS Code surface, and the same rc-97 gate.

---

## 3. Full Professor adoption — the discipline layer

Everything above, plus `CLAUDE.md`, per-project agents, commands, docs scaffolding, and the whole pipeline. `pfm init` scaffolds the project layer once, with template tokens intact and per-file baseline pins; the Claude-guided interview then records your answers in `.professor/manifest.json` `tokens`, runs `pfm init --render` to fill the install-time tokens once, and adapts the roster and structure of those local files in place. Updates never re-render. Nothing here duplicates what paths 1/2 already do.

**Prerequisites:** Claude Code CLI, logged in. A git repository — if the project isn't one, Claude asks before `git init`. `jq` — required by the host installer and several hooks (`brew install jq` / `apt install jq`). `rumdl` — the markdown lint/format engine behind `/quality:md-forlint` and the format hook; `pfm install` provisions it, and `pfm doctor` carries its row. Optional, per opt-in: `tmux` (host fleet), `gh`/`glab` (git-host skill). Ten to fifteen minutes of your attention.

Initialize the target project, then follow the path printed by `pfm init`:

```bash
cd /path/to/your-project
pfm init .
claude
```

Tell Claude to read the printed `docs/SETUP.md` path and execute its **Install interview** section. Claude interviews you — structure, stack, optional roles, persona, and host extras — shows the full write plan, waits for you to type **"go"**, then applies it: records your answers in `.professor/manifest.json`, fills the install-time tokens once through `pfm init --render`, adapts the scaffolded local files to your roster and structure, and deploys and pins per-project agents. Ten to fifteen minutes, commits nothing.

**Guarantees, stated by the installer up front:**

- Never commits, pushes, or runs `git add` — files only; you review and commit.
- Never overwrites an existing project file by default — `pfm init` reports `CONFLICT` and leaves that path unpinned; `--force` is the explicit overwrite choice.
- Never installs an opt-in piece — Tier B roles, Codex, statusline, hooks, host fleet, memory backup — without an explicit yes.
- Never touches a path outside the plan.

**Full protocol:** [`docs/SETUP.md`](./docs/SETUP.md) — the interview questions, pre-flight checks, existing-doc re-homing rules, generation steps, and verification. [`docs/PLACEHOLDERS.md`](./docs/PLACEHOLDERS.md) is the substitution law, [`docs/BLUEPRINT.md`](./docs/BLUEPRINT.md) the philosophy. Read all three before writing any file.

## What gets written where

One writer per surface — the law that keeps the two installers from fighting over the same file.

| Surface | Written by | Paths |
| --------------------------------------------------------- | ------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Host fleet wiring | `pfm install` — the only writer | `{clone}/pfm.config.json`, `~/.local/share/pfm/install/`, `~/.claude/commands/`, `~/.claude/skills/`, the systemd/launchd scheduler units, `~/.codex/{prompts,skills,agents,hooks.json}`, one `~/.zshrc` line, and the opt-in VS Code user/remote `settings.json`; Claude hooks, status line and MCP ride every launch |
| Claude managed cleanup | `pfm install` | `/etc/claude-code/managed-settings.d/pfm.json` on Linux, `/Library/Application Support/ClaudeCode/managed-settings.d/pfm.json` on macOS; uses `sudo -n` when direct write is unavailable. Without cached credentials, install warns and prints the command to run. |
| State and cache database moves | `pfm install` | `~/.cc/fleet.db` → `~/.local/state/pfm/pfm.db`; `~/.local/state/pfm/fleet.db` → `~/.local/state/pfm/pfm-cache.db`, including WAL/SHM siblings. |
| Install migration journal | `pfm install` | `~/.local/state/pfm/migrations/<id>/` records changed paths and their backups for rollback. |
| Claude plugin door | `pfm install` through `claude plugin install` | Each account's `{config dir}/plugins/**` and `enabledPlugins` in its `settings.json` are journaled; a plugin write is skipped while a chat is live on that settings file, including a sharing account. |
| Project discipline layer | `pfm init` scaffolds and pins; the interview owns later local adaptation | `CLAUDE.md`, `.claude/`, `docs/`, `.professor/`, per-project `CLAUDE.md` + `.claude/` |
| Host-level opt-ins chosen during the interview | `pfm install`, invoked on your behalf | Lands inside the host-fleet surfaces above — the interview never writes them directly |
| Themes, source-fetched and bundled (default; `--skip-themes` opts out) | `pfm install` | `~/.claude/themes/tokyo-night.json`, `~/.claude/themes/professor-{gold,silver,bronze}.json`, and any other target declared by `templates/themes/sources.json`; exact ownership is recorded in the install ledger |
| MCP client registration (the one `professor` server) | Claude: each managed launch's `--mcp-config`; Codex and OpenCode: `pfm install` — the only writer | registered while either family is enabled (`mcp.servers.chat.enabled`, `harvester.enabled`), removed when both are off; every engine runs the same stdio command `~/.local/bin/pfm mcp serve --stdio` (absolute path), which forwards to the daemon's `/mcp/professor`. Claude: rendered into every pfm-launched Claude's `--mcp-config`; `pfm install` writes no key into an account `.claude.json` and strips pfm-owned legacy entries from it, leaving manual entries. Codex: one installer-owned `[mcp_servers.professor]` fence (`command`, `args`) at the end of every Codex home's `config.toml`. OpenCode: key `mcp.professor` of type `local` in `opencode.jsonc` |

`pfm install --config-dir DIR` retargets the `~/.claude`-rooted writes to a different config directory — the only supported override.

### Codex homes are config-owned

An explicitly empty `codex.homes` array in the PFM machine config is authoritative: `"homes": []` means no Codex home even if `~/.codex` exists and contains credentials. Non-empty entries may use `~` or `$HOME/` and must name authenticated homes:

```json
{
  "version": 2,
  "codex": {
    "homes": [{ "id": 1, "home": "~/.codex" }]
  }
}
```

With an empty list, PFM does not fall back to the default home or write its Codex mirrors, configuration defaults, or hooks. If `ask.engine` is explicitly `codex`, select a configured engine there or remove that override; an explicit engine with no accounts is a configuration error. An omitted `ask.engine` is chosen from the available roster.

---

## Updating

Each tier has one source of truth and one update mechanism:

| Tier | Truth | Staying current |
| ----------------------------------------------------------- | ---------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Machine-global commands, agents, and skills | Blueprint originals | `pfm update` advances the tagged source clone, rebuilds the binary, runs `pfm install --yes`, and refreshes the registry symlinks. It rolls back only on a `pfm doctor` failure (a required dependency, launcher, hooks, host overlay, global agents, config, or database state); pre-existing warnings never block it, and it reports each warning the update newly introduced. |
| Project files (`CLAUDE.md`, `.claude/**`, docs, scripts) | The local files | `pfm init` scaffolds them once (`pfm update adopt` pins an install that predates scaffolding). `pfm doctor --project-updates` reports template deltas with the upstream diff under each `UPDATED` row; you carry what applies into the local file, keep your own edits, then pin it. |
| Engine mirrors (`AGENTS.md`, `.codex/**`, OpenCode outputs) | Generated from local project files | Never edit them by hand. Rebuild or verify them with their compiler, including `pfm codex build` and `pfm codex check`. |

A fresh clone of the blueprint itself carries none of these outputs — `AGENTS.md`, `.codex/**`, `.opencode/**` are generated, never tracked (see [`.gitignore`](.gitignore)). Opening it in Claude Code first generates them via the `Stop` hook; opening it in Codex or OpenCode before that first Claude turn needs `pfm codex build .` and `pfm opencode build .` run once by hand. `pfm install` compiles the machine-global `.toml` twins the same way, into pfm's own generated directory — never into the clone.

**Read every release you skipped before you update.** `pfm version` names the installed release; each later `releases/vX.Y.Z.md` up to the target is one release's changes, and its `#### → For:` lines are what that release asks of you, each marked `before update`, `after update` or `per project` (the grammar is `docs/RELEASE.md` § Release notes). A release whose note carries `#### → Stop:` is a required stop: update to it first, finish its actions, then continue. Read all of them first — five versions behind is five files — and merge their actions into one list, a later release's action superseding an earlier one on the same surface. Then run `pfm update` and work through the list; `pfm update` prints the release-notes files it moved past once the source has advanced.

### Crossing from v0.76–v0.78

Before the first migration, run `infra/fence/host-backup.sh <backup> live` from the clone, then rehearse with `infra/fence/host-rehearsal.sh <backup> <scratch>`. Close every chat before crossing. If an older `pfm update` reaches the layout migration, its candidate install prints this refusal:

```text
  refuse  updater — this install migrates the host layout, and the pfm update running it predates the install journal
cross by hand:
  1. close every chat, the one running this command included
  2. from a plain shell outside tmux, run:
     git -C <clone> pull --ff-only
     make -C <clone>/pfm host-install
     pfm install --yes
```

The older `pfm update` rolls itself back first, so nothing changed. Run the crossing commands from a plain shell outside tmux after it exits.

Between `make host-install` and `pfm install --yes`, the new binary sees a config it has not migrated yet. These refuse with `config not migrated: run pfm install` until the migration: `pfm-mcp.service` and the name-sync units (the launch agents on macOS); the Claude hooks (a non-blocking error in every chat); a `claude` launch through the shim; `pfm mcp serve --stdio`; `pfm update`. These still answer: `pfm --version`, `pfm doctor`, the statusline, `pfm config show|validate`, `make stale`, `make mcp-status` and `make sweep-stale`.

Before the swap, `make host-install` asks the new binary `pfm install --check`. It runs every refusal `pfm install --yes` makes before its first change, in the apply's order: a `--config` that does not exist, the space preflight, the install gate (which refuses a live chat, a legacy database held by any process other than pfm's own services, and session-store entries pfm cannot move as you), then the config migration's plan (which refuses a stray pre-split `config.json` beside the config), the paths the moved databases will resolve to, the required dependency preflight and a running name-sync job. It moves nothing and writes nothing but pfm's own activity log. It exits 0 when all would pass, 4 when the gate would refuse or the name-sync job is running now, and 1 when another refusal fires or the check cannot read what it needs. What no check can preview is a write that fails mid-flight (a checkpoint, a move, a service restart): the journal records each change as it lands, and `pfm install --rollback <id>` reverses what landed. When the new binary would refuse this host's config (a migration is pending), a non-zero answer keeps the installed `pfm`, so the window never opens on a host whose migration would itself refuse:

```text
install check: blocked — close what it names, then rerun make -C <clone>/pfm host-install
host-install: the new binary's install gate refuses this host — ~/.local/bin/pfm untouched; close or resolve what it names above, then rerun (a live chat includes Claude's daemon and bg-spare sessions: claude daemon stop --any)
```

The gate counts a chat as live from its `{account}/sessions/{pid}.json` file. Claude Code's background daemon and its `bg-spare` sessions write those files too, so they count; `claude daemon stop --any` ends them. On a host already migrated, the same answer prints as a `host-install: WARNING` and the swap goes ahead, because the new binary reads that config. `SKIP_INSTALL_CHECK=1 make host-install` skips the check with a SKIPPED line; `FORCE=1` passes only the downgrade guard.

Once the check passes, `make host-install` swaps the binary, names the window and exits 0:

```text
host-install: binary swapped; pfm refuses until `pfm install --yes` migrates this host — run it now
```

`make install` stops there, restarting and rolling back nothing:

```text
install: stopped after host-install; nothing restarted or rolled back — run pfm install --yes now, then make -C <clone>/pfm sweep-stale
```

Close every chat first, and run `pfm install --yes` at once.

For a journal listed under `~/.local/state/pfm/migrations/`, run `pfm install --rollback <id>` to reverse that install; `pfm install --rollback <id> --force` overrides destination drift when you intend to overwrite newer changes. A pending journal from a crashed or failed install blocks the next install until it is rolled back. After an install seals its journal, pruning keeps the newest three sealed journals and any younger than 14 days. Undo a crossing in this order:

1. `pfm install --rollback <id>` with the new binary first. When the restored config is a legacy one this pfm refuses, it leaves the fleet units stopped and prints its numbered `next` lines:

   ```text
     next    this pfm refuses the restored legacy config {legacy}; fleet units left stopped: {units}
     next    1. systemctl --user daemon-reload
     next    2. make -C <clone>/pfm rollback
     next    3. systemctl --user start {units}
   ```

   The `daemon-reload` line appears only when the rollback restored a systemd unit file, and comes first so that `make rollback`'s MCP restart runs the restored unit; on macOS each start line is a `launchctl bootstrap` of one launch agent.
2. `make -C <clone>/pfm rollback`, after the printed `daemon-reload` when there is one.
3. The printed unit restart.

`make rollback` refuses a `pfm.prev` installed before a layout migration that is not rolled back yet, printing this order (`rollback: REFUSED — …`); `FORCE=1 make rollback` overrides it. A failed `make mcp-restart` prints the unit's last log lines after its `MCP-RESTART-FAILED` line.

The project flow is deliberately non-destructive:

1. Run `pfm doctor --project-updates` for a report only (exit 0 clean, 1 review, 3 failure). Bare `pfm update` performs the machine update first and appends the same report when run inside a managed project.
2. For each `UPDATED` item, read the diff printed under the row — the intent of the upstream change — carry what applies into the local file, and keep the project's own edits. `NEW`, `GONE-UPSTREAM`, and `LOCAL-DELETED` each print their own adoption or cleanup action.
3. Accept a reviewed file with `pfm update pin <local>`. Adopt a new template mapping with `pfm update pin --template <template> <local>`; forget an obsolete mapping with `pfm update drop <local>`; silence a template you will never take with `pfm update ignore <template>...`.
4. Rebuild opted-in engine mirrors from the resulting local source files.

No update regenerates scaffolded project files, replays the interview, or performs a three-way merge. See [`docs/SETUP.md`](docs/SETUP.md#staying-current) for the complete workflow.

---

## Uninstall

**`pfm`:** `pfm uninstall` — removes installer-owned links and theme files and restores the pre-install backups, per `pfm uninstall --help`. Locally modified theme files are preserved and reported rather than removed.

**The discipline layer:** no uninstall command exists anywhere in `templates/` or the shipped commands. Removing it is a manual `git` operation on your side — revert the install commit, or delete the written paths from the ownership table above.
