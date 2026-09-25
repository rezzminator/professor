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
2. Command symlinks — `~/.claude/commands/` (`/reload`); skill symlinks — `~/.claude/skills/` (`deep-rr`, `/handoff`)
3. The `pfm-name-sync` scheduler — three systemd user units (Linux) or one launchd agent (macOS)
4. Every Claude account settings file it finds (`~/.claude/settings.json` and each `~/.cc/N/settings.json`) — adds the usage, group, and `/clear` `SessionEnd` hooks; adopts the statusline only if none is already set
5. `~/.codex/prompts/`, `~/.codex/skills/`, and `~/.codex/agents/` — Codex mirrors generated from the installed global Claude commands and host-global agent sources; a role lands in `agents/` as a REGULAR FILE, because Codex opens a role with `O_NOFOLLOW` and rejects a symlink as "agent type is currently not available". Only marker-owned outputs are replaced or retired, while unmarked conflicts survive and stop the install by name
6. `~/.codex/hooks.json` — migrates surviving binary paths and removes retired clear-kill and Dream/STM hooks; it installs no automatic Codex hook
7. One source line appended to `~/.zshrc` — restart your shell (or `source ~/.zshrc`) for it to take effect
8. `~/.claude/themes/` — the themes declared by `templates/themes/sources.json`, source-fetched and bundled; a failed cosmetic fetch or an unreadable bundled file is reported and skipped without aborting the other surfaces
9. **Opt-in:** VS Code — links the Professor extension (Professor's assistant in VS Code) into `extensions/professor` of every VS Code product present (`~/.vscode`, `~/.vscode-insiders`, `~/.vscode-oss`, `~/.vscode-server`, `~/.vscode-server-insiders`, a portable install) and registers it in that product's own `extensions/extensions.json` — the file modern VS Code actually scans user extensions from, so a link alone is never loaded — and in the user or remote-machine `settings.json` adds a `PFM` terminal profile (icon `mortar-board`, colour magenta) and selects it as the platform default (the extension's own `Professor` profile stays in the + dropdown — a default an extension contributes would make every window reload drop the open terminals). After a reload, the extension is visible as **Professor** in the Extensions view and a **Professor** entry in the terminal `+` dropdown. Press Ctrl+Shift+Alt+T (macOS: Cmd+Shift+Alt+T), run **Professor: New Chat Terminal**, or pick **Professor** from the terminal `+` dropdown — all three give the next icon and colour; the default `+` terminal is `PFM`. A PFM terminal opens a login zsh, then the installed shim opens the PFM picker at the shell's first prompt; each tab carries its chat's live name. PFM edits JSONC surgically, so comments and unrelated profiles survive; later installs retain ownership, and uninstall removes only the links (and the index entries they registered) still pointing at PFM's copy, restoring the prior default unless the operator changed it after installation. Reload the VS Code window once to load a newly linked extension. `pfm doctor` reports one row per product (link, index registration, version) and per owned settings file.

Every rewritten file is backed up before it's touched.

Run `pfm` for the interactive picker. Its colors are enabled independently of inherited `NO_COLOR` or `CLICOLOR=0`; `pfm ls --plain` and `pfm ls --tsv` remain uncolored. The managed terminal profile uses `PFM_AUTO_OPEN=pfm` to open the picker once at the first prompt.

The Professor `cc*` shell commands are retired. Use `pfm`, `pfm chat open <target>`, and the picker's account selector. Installation removes the named legacy launch/account scripts, backing up regular files outside `PATH` under `~/.local/state/pfm/retired-commands/`; source the updated shim or start a new shell to unload old functions and aliases. Account credentials, transcripts, live chat socket names, and the system C compiler are preserved.

Optional `cc-memory-wire.sh` and `cc-memory-consolidate.sh` helpers become `memory-wire.sh` and `memory-consolidate.sh`. Installation migrates recognized historical copies and exact hook paths without executing either helper or changing memory data. Customized helpers, conflicting destinations, and unsupported hook commands stop migration with an error; hosts without these helpers remain opted out.

**Known gate — read before you run it.** A mutating install refuses with exit 97 only while PFM's name-sync job is actively running, so it cannot replace the job or binary mid-execution. On Linux, wait or run `systemctl --user stop pfm-name-sync.service`; on macOS, wait or run `launchctl bootout gui/$(id -u)/com.professor.pfm.name-sync`. The preview remains read-only.

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

Set `GOPROXY` to a Go module proxy reachable from your network. The source path needs Go **1.24.13 or newer**, the floor declared by `pfm/go.mod`, and access to the module host or proxy. The tag lookup and explicit checkout keep the source and the binary on the same latest release; if the source directory already exists, fetch and check out that tag there instead of cloning over it.

The source build has the same harvest cost and opt-outs as the [preview/apply block above](#preview-optional-components-and-harvest-footprint). Then run the same two commands as the binary path, from inside the clone — that is how `pfm install` records it as your source repository, which `pfm init` and `pfm update` both read:

```bash
cd "$HOME/.professor"
pfm install
pfm install --yes
```

Same eight base surfaces, the same optional VS Code surface, and the same rc-97 gate.

---

## 3. Full Professor adoption — the discipline layer

Everything above, plus `CLAUDE.md`, per-project agents, commands, docs scaffolding, and the whole pipeline. `pfm init` scaffolds the project layer once, with template tokens intact and per-file baseline pins; the Claude-guided interview then adapts those local files in place. Nothing here duplicates what paths 1/2 already do.

**Prerequisites:** Claude Code CLI, logged in. A git repository — if the project isn't one, Claude asks before `git init`. `jq` — required by the host installer and several hooks (`brew install jq` / `apt install jq`). `rumdl` — the markdown lint/format engine behind `/quality:md-forlint` and the format hook; `pfm install` provisions it, and `pfm doctor` carries its row. Optional, per opt-in: `tmux` (host fleet), `gh`/`glab` (git-host skill). Ten to fifteen minutes of your attention.

Initialize the target project, then follow the path printed by `pfm init`:

```bash
cd /path/to/your-project
pfm init .
claude
```

Tell Claude to read the printed `docs/SETUP.md` path and execute its **Install interview** section. Claude interviews you — structure, stack, optional roles, persona, and host extras — fills the scaffolded local files, deploys and pins per-project agents, shows the full write plan, waits for you to type **"go"**, then applies it. Ten to fifteen minutes, commits nothing.

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
| Host fleet wiring | `pfm install` — the only writer | `~/.local/share/pfm/install/`, `~/.claude/commands/`, `~/.claude/skills/`, the systemd/launchd scheduler units, every Claude account `settings.json`, `~/.codex/{prompts,skills,agents,hooks.json}`, one `~/.zshrc` line, and the opt-in VS Code user/remote `settings.json` |
| Project discipline layer | `pfm init` scaffolds and pins; the interview owns later local adaptation | `CLAUDE.md`, `.claude/`, `docs/`, `.professor/`, per-project `CLAUDE.md` + `.claude/` |
| Host-level opt-ins chosen during the interview | `pfm install`, invoked on your behalf | Lands inside the host-fleet surfaces above — the interview never writes them directly |
| Themes, source-fetched and bundled (default; `--skip-themes` opts out) | `pfm install` | `~/.claude/themes/tokyo-night.json`, `~/.claude/themes/professor-{gold,silver,bronze}.json`, and any other target declared by `templates/themes/sources.json`; exact ownership is recorded in the install ledger |
| MCP client registration (the one `professor` server) | `pfm install` — the only writer | registered while either family is enabled (`mcp.servers.chat.enabled`, `harvester.enabled`), removed when both are off; every engine registers the same stdio command `~/.local/bin/pfm mcp serve --stdio` (absolute path), which forwards to the daemon's `/mcp/professor`. Claude: key `mcpServers.professor` in every user-scope `.claude.json` a pfm-launched Claude can read — `~/.claude.json` for the account pfm spawns without `CLAUDE_CONFIG_DIR`, `<config dir>/.claude.json` for every explicit account, and `$CLAUDE_CONFIG_DIR/.claude.json` when the shell exports it. Codex: one installer-owned `[mcp_servers.professor]` fence (`command`, `args`) at the end of every Codex home's `config.toml`. OpenCode: key `mcp.professor` of type `local` in `opencode.jsonc` |

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
| Project files (`CLAUDE.md`, `.claude/**`, docs, scripts) | The local files | `pfm init` scaffolds them once (`pfm update adopt` pins an install that predates scaffolding). `pfm update check` reports template deltas; you review and hand-apply each wanted change, then pin it. |
| Engine mirrors (`AGENTS.md`, `.codex/**`, OpenCode outputs) | Generated from local project files | Never edit them by hand. Rebuild or verify them with their compiler, including `pfm codex build` and `pfm codex check`. |

A fresh clone of the blueprint itself carries none of these outputs — `AGENTS.md`, `.codex/**`, `.opencode/**` are generated, never tracked (see [`.gitignore`](.gitignore)). Opening it in Claude Code first generates them via the `Stop` hook; opening it in Codex or OpenCode before that first Claude turn needs `pfm codex build .` and `pfm opencode build .` run once by hand. `pfm install` compiles the machine-global `.toml` twins the same way, into pfm's own generated directory — never into the clone.

**Read every release you skipped before you update.** `pfm version` names the installed release; each later `releases/vX.Y.Z.md` up to the target is one release's changes, and its `#### → For:` lines are what that release asks of you. Read all of them first — five versions behind is five files — and merge their actions into one list, a later release's action superseding an earlier one on the same surface. Then run `pfm update` and work through the list; `pfm update` prints the release-notes files it moved past once the source has advanced.

The project flow is deliberately non-destructive:

1. Run `pfm update check` for a report only. Bare `pfm update` performs the machine update first and appends the same report when run inside a managed project.
2. For each `UPDATED` item, inspect the printed blueprint `git diff`, decide what belongs in the local file, and apply it by hand. `NEW`, `GONE-UPSTREAM`, and `LOCAL-DELETED` each print their own adoption or cleanup action.
3. Accept a reviewed file with `pfm update pin <local>`. Adopt a new template mapping with `pfm update pin --template <template> <local>`; forget an obsolete mapping with `pfm update drop <local>`; silence a template you will never take with `pfm update ignore <template>...`.
4. Rebuild opted-in engine mirrors from the resulting local source files.

No update regenerates scaffolded project files, replays the interview, or performs a three-way merge. See [`docs/SETUP.md`](docs/SETUP.md#staying-current) for the complete workflow.

---

## Uninstall

**`pfm`:** `pfm uninstall` — removes installer-owned links and theme files and restores the pre-install backups, per `pfm uninstall --help`. Locally modified theme files are preserved and reported rather than removed.

**The discipline layer:** no uninstall command exists anywhere in `templates/` or the shipped commands. Removing it is a manual `git` operation on your side — revert the install commit, or delete the written paths from the ownership table above.
