# pfm functionality landscape — Tier B lane map

This is the tracked copy of `tmp/inventory/landscape.md`, the flat id-numbered inventory the
Wave 4 mapping step reads to place every functionality item onto a Tier B live-fleet lane. Ids,
item text, `needs:`, `today:` and source columns are unchanged from the inventory pass that
derived them from code (`tmp/inventory/{cli,mcp,install,fleet,tests}.md`); the one addition is
`lane(s)`, filled by `infra/fence/lanes/map.tsv` (the reverse index lives there, keyed the other
way: `landscape_id · lane · beat`). A new item lands here with its beat in `infra/fence/lanes/
beats.md` in the same commit — this file and the map never drift apart.

# pfm functionality landscape

Flat, id-numbered inventory of every functionality item a user or adopter can exercise across
`pfm`, built from `tmp/inventory/{cli,mcp,install,fleet,tests}.md`. One item per line:
`<id> · <item> · needs:<...> · today:<U|A|B|NONE combo> · <source>`.

`today` = test-tier evidence per `tests.md`'s coverage matrix (§6) and the per-command `tests`
columns in `cli.md`/`mcp.md`: `U`=Tier A unit test exists, `A`=Tier A e2e (`pfm/e2e` Go test or
`.txtar` script) exists, `B`=Tier B live-fleet beat (`infra/demo/verify.sh`) exists, `NONE`=no
test found by any tier (always tagged `⚠ known-gap` when the source explicitly named the absence
as a gap rather than out-of-scope). Combos (`U+A`, `U+A+B`, `U+B`) mean more than one tier covers
the item.

---

## I — Install / host wiring (98)

### Host assets

I1 · Claude launcher shim staged + symlinked `~/.local/bin/claude` · needs:none · today:U · install.md:24 · lane(s):O
I2 · `pfm-statusline` overlay staged + symlinked · needs:none · today:U · install.md:25 · lane(s):O
I3 · `tmux-title-renudge` overlay staged + symlinked · needs:tmux · today:U · install.md:26 · lane(s):O
I4 · `handoff.skill.md` symlinked per Claude account · needs:seat:cc · today:NONE ⚠ known-gap (no doctor probe for skill-link health) · install.md:27 · lane(s):O
I5 · launchd name-sync plist wiring (macOS only) · needs:systemd/launchd · today:NONE ⚠ known-gap (no doctor row found; unverified on Linux) · install.md:28 · lane(s):O
I6 · launchd MCP plist wiring (macOS only, MCP-enabled gate) · needs:systemd/launchd,network · today:U · install.md:29 · lane(s):O
I7 · `codex-appendix.md` staged, consumed as Codex SessionStart hook file · needs:seat:cx · today:U · install.md:30 · lane(s):O
I8 · harness-prompt baseline files (opus + original) for drift doctor · needs:none · today:U · install.md:31 · lane(s):O
I9 · `professor-prompt.md` system-prompt file (`claude.systemPrompt="professor"`) · needs:seat:cc · today:NONE ⚠ known-gap (no dedicated doctor row) · install.md:32 · lane(s):O
I10 · `reload.command.md` installed command file (Claude + Codex mirror) · needs:seat:cc · today:NONE ⚠ known-gap (no doctor row for command-link health) · install.md:33 · lane(s):O
I11 · `pfm.zsh` shim sourced from `~/.zshrc` (defines `cc`/`cx`/`co`/`pfm` launchers) · needs:none · today:U (indirect via `pfmPathWarnings`) · install.md:34 · lane(s):O
I12 · systemd `pfm-mcp.service` unit (Linux, MCP-enabled gate) · needs:systemd/launchd,network · today:U · install.md:35 · lane(s):O
I13 · systemd `pfm-name-sync.{path,service,timer}` units (Linux) · needs:systemd/launchd · today:NONE ⚠ known-gap (no dedicated systemd-unit doctor row) · install.md:36 · lane(s):O
I14 · VS Code extension + `extensions.json` entry + `PFM` terminal profile wiring · needs:none · today:U · install.md:37 · lane(s):O
I15 · Claude Code themes bundle (3: professor-gold/silver/bronze) · needs:seat:cc · today:NONE ⚠ known-gap (no `printThemeDoctor` row found) · install.md:38 · lane(s):O
I16 · Harvestpy pinned Python conversion runtime provisioning (opt-in) · needs:network · today:U · install.md:39 · lane(s):O
I17 · Global commands wiring (Claude symlink + Codex mirror) · needs:seat:cc,seat:cx · today:NONE ⚠ known-gap (no dedicated doctor row for command-link health) · install.md:40 · lane(s):O
I18 · Global skills wiring (Claude only) · needs:seat:cc · today:NONE ⚠ known-gap (no dedicated doctor row) · install.md:41 · lane(s):O
I19 · Global agents wiring — Claude `.md` symlink side · needs:seat:cc · today:U (`ReportGlobalAgents`) · install.md:42 · lane(s):O
I20 · Global agents wiring — Codex `.toml` compile side (`wireCodexAgents`) · needs:seat:cx · today:U · install.md:42 · lane(s):O
I21 · `source-repo` marker anchoring every global link · needs:git · today:U (indirect) · install.md:43 · lane(s):O
I22 · `mcp-auth-token` credential file · needs:network · today:U (indirect via MCP reachability) · install.md:44 · lane(s):O
I23 · `settings-hook-ownership.json` ledger, cross-checked against live files · needs:seat:cc,seat:cx · today:U · install.md:45,79-84 · lane(s):O

### Hooks (installer-owned)

I24 · Claude `SessionStart` → `pfm internal launcher-repair` (repairs displaced claude symlink) · needs:seat:cc · today:U · install.md:64 · lane(s):O
I25 · Claude `UserPromptSubmit` → `pfm usage-hook` (spend tracking) · needs:seat:cc,network · today:U · install.md:65 · lane(s):O
I26 · Claude `SessionEnd` → `pfm internal clear-kill` · needs:seat:cc · today:U · install.md:66 · lane(s):O
I27 · Claude `SessionEnd` → `pfm internal exit-close` · needs:seat:cc,tmux · today:U · install.md:67 · lane(s):O
I28 · Claude `PreToolUse` (`Agent|Task`) → `pfm internal explore-deny` (guarded-file subagent deny) · needs:seat:cc · today:U · install.md:68 · lane(s):O
I29 · Claude `UserPromptSubmit` → `pfm internal epic-inject` · needs:seat:cc,tmux · today:U · install.md:69 · lane(s):O
I30 · Claude `UserPromptSubmit` → `pfm internal reload-intercept` · needs:seat:cc,tmux · today:U · install.md:70 · lane(s):O
I31 · Claude `UserPromptSubmit` → `pfm internal exit-intercept` · needs:seat:cc,tmux · today:U · install.md:71 · lane(s):O
I32 · Claude `UserPromptSubmit` → `pfm internal compact-nudge` · needs:seat:cc · today:U · install.md:72 · lane(s):O
I33 · Codex `SessionStart` (`startup|resume|clear`) → codex-appendix injection · needs:seat:cx · today:U · install.md:73 · lane(s):O

### Seats

I34 · Implicit account 1 discovery (`~/.cc/1` → `~/.claude` fallback) · needs:seat:cc · today:U · install.md:93-103 · lane(s):O
I35 · Explicit `~/.cc/N` additional accounts (N≥2) · needs:seat:cc · today:U · install.md:104-106 · lane(s):O
I36 · `installer.Options.ConfigDir`/`ConfigDirs` fanout · needs:seat:cc · today:U · install.md:107-110 · lane(s):O
I37 · `claudeConfigDirs()` physical-path dedup (symlink-aware) · needs:seat:cc · today:U · install.md:111-115 · lane(s):A,O
I38 · Dropped-seat hook-ownership reconciliation, refuses stranding on uninstall · needs:seat:cc · today:U · install.md:116-123 · lane(s):M,O
I39 · Codex homes configuration (default `~/.codex`, or `Options.CodexHomes`) · needs:seat:cx · today:U · install.md:124-126 · lane(s):O
I40 · OpenCode home — never written by `pfm install` ⚠ known-gap (confirmed absence) · needs:seat:oc · today:NONE · install.md:127-132 · lane(s):O

### Doctor row families

I41 · Doctor: config dump (info only) · needs:none · today:U · install.md:230 · lane(s):O
I42 · Doctor: harvester config warnings · needs:network · today:U · install.md:231 · lane(s):O
I43 · Doctor: engine roster warnings · needs:none · today:U · install.md:232 · lane(s):O
I44 · Doctor: OpenCode store warnings · needs:seat:oc · today:U · install.md:233 · lane(s):O
I45 · Doctor: engine capabilities warnings · needs:none · today:U · install.md:234 · lane(s):O
I46 · Doctor: MCP client cutover warnings · needs:network · today:U · install.md:235 · lane(s):O
I47 · Doctor: MCP daemon reachability + harvester-external + version-skew · needs:network · today:U · install.md:236 · lane(s):O
I48 · Doctor: database health (user_version/quick_check/shared-store/row-counts/WAL/busy), direct `return 3` · needs:none · today:U · install.md:237 · lane(s):O
I49 · Doctor: PATH canonical warnings · needs:none · today:U · install.md:238 · lane(s):O
I50 · Doctor: pre-push gate warnings · needs:git · today:U · install.md:239 · lane(s):O
I51 · Doctor: harness-prompt drift warnings · needs:seat:cc · today:U · install.md:240 · lane(s):O
I52 · Doctor: spawn audit warnings · needs:tmux · today:U · install.md:241 · lane(s):O
I53 · Doctor: tmux titles (info only — "both title owners are legitimate") · needs:tmux · today:U · install.md:242 · lane(s):O
I54 · Doctor: Claude launcher failures (missing/DISPLACED/unknown) · needs:seat:cc · today:U · install.md:243 · lane(s):O
I55 · Doctor: VS Code warnings · needs:none · today:U · install.md:244 · lane(s):O
I56 · Doctor: Claude versions/retention warnings+failures · needs:seat:cc · today:U · install.md:245 · lane(s):O
I57 · Doctor: dependency probe warnings+failures (gates `claudeAbsent` later rows) · needs:none · today:U+A (`doctor.txtar`) · install.md:246 · lane(s):O
I58 · Doctor: host overlays — every non-ok state is a FAILURE, never a warning · needs:none · today:U · install.md:247 · lane(s):O
I59 · Doctor: global agents warnings (Conflict/NoSources/Unresolved) + failures (Missing/Unreadable) · needs:seat:cc,seat:cx · today:U · install.md:248 · lane(s):O
I60 · Doctor: hooks warnings (drift) + failures (missing/broken/stale) · needs:seat:cc,seat:cx · today:U · install.md:249 · lane(s):O
I61 · Doctor: roots (Claude/Codex account roots) warnings · needs:seat:cc,seat:cx · today:U · install.md:250 · lane(s):O
I62 · Doctor: professor project row — `UNREADABLE` folded into warnings not failures ⚠ known-gap (severity mismatch) · needs:project · today:U · install.md:251 · lane(s):O
I63 · Doctor: Codex pane binding warnings (contested/retired `/clear` bindings) · needs:seat:cx,tmux · today:U · install.md:252 · lane(s):E2,O
I64 · Doctor: crumb health (SID dir) warnings · needs:seat:cc · today:U · install.md:253 · lane(s):O
I65 · Doctor: harvestpy environment warnings (skippable `--skip-harvest`) · needs:network · today:U · install.md:254 · lane(s):O
I66 · Doctor: harvest cache warnings · needs:none · today:U · install.md:255 · lane(s):O
I67 · Doctor: harvest search warnings · needs:network · today:U · install.md:256 · lane(s):O
I68 · Doctor: overall exit-code tally (0 clean / 1 warnings / 2 usage / 3 failures) · needs:none · today:U+A (`doctor.txtar`) · install.md:219-227 · lane(s):O

### Uninstall

I69 · Uninstall removes harvestpy managed env+cache · needs:none · today:U · install.md:265 · lane(s):O
I70 · Uninstall removes staged theme files · needs:none · today:U · install.md:265 · lane(s):O
I71 · Uninstall removes/restores Claude launcher symlink (displaced-native backup restore) · needs:seat:cc · today:U · install.md:266-267 · lane(s):O
I72 · Uninstall removes host overlay symlinks · needs:none · today:U · install.md:268 · lane(s):O
I73 · Uninstall removes `/reload` command link · needs:seat:cc · today:U · install.md:269 · lane(s):O
I74 · Uninstall removes `handoff` skill link · needs:seat:cc · today:U · install.md:269 · lane(s):O
I75 · Uninstall removes Codex command mirror (empty asset list = delete every wired command) · needs:seat:cx · today:U · install.md:270-271 · lane(s):O
I76 · Uninstall removes `/bb` + `/chat:*` command remnants · needs:seat:cc · today:U · install.md:271 · lane(s):O
I77 · Uninstall removes macOS launch agents / Linux systemd units+enablements · needs:systemd/launchd · today:U · install.md:272-274 · lane(s):O
I78 · Uninstall removes Claude `settings.json` + Codex `hooks.json` hook entries (ownership-scoped) · needs:seat:cc,seat:cx · today:U · install.md:274-276 · lane(s):O
I79 · Uninstall removes MCP client registrations · needs:network · today:U · install.md:276 · lane(s):O
I80 · Uninstall removes the `source .../pfm.zsh` line from `~/.zshrc` · needs:none · today:U · install.md:277 · lane(s):O
I81 · Uninstall removes VS Code extension link/index entry/terminal-profile settings · needs:none · today:U · install.md:278-279 · lane(s):O
I82 · Uninstall removes every staged file under the managed root, prunes empty subdirs · needs:none · today:U · install.md:279-280 · lane(s):O
I83 · Uninstall removes the update-metadata file (apply mode only) · needs:none · today:U · install.md:281-282 · lane(s):O
I84 · Uninstall guarantee: never strips a foreign hook coexisting in the same settings/hooks file · needs:seat:cc,seat:cx · today:U · install.md:286-293 · lane(s):O
I85 · Uninstall guarantee: never removes an operator's own agent/command/skill file (classified by link target) · needs:seat:cc,seat:cx · today:U · install.md:294-297 · lane(s):O
I86 · Uninstall guarantee: never touches project scaffold files (`.professor/`, `CLAUDE.md`, `.claude/**`) · needs:project · today:U · install.md:298-302 · lane(s):O
I87 · Uninstall guarantee: never force-removes a non-empty managed directory · needs:none · today:U · install.md:303-305 · lane(s):O
I88 · Backup guarantee: every destructive in-place rewrite gets a timestamped backup first · needs:none · today:U · install.md:307-311 · lane(s):O
I89 · Backup retention: no pruning routine found for old timestamped backups ⚠ known-gap (UNKNOWN — targeted not exhaustive search) · needs:none · today:NONE · install.md:312-315 · lane(s):O

### Doc vs code

I90 · §7b-i `settings-global.json` merge is a Phase-2 Claude-session hand-merge, not `pfm` Go code ⚠ known-gap · needs:seat:cc · today:NONE · install.md:324-331 · lane(s):O
I91 · §7d/§7e `notify.sh`/`format-md.sh` are project-scoped hooks shipped via `templates/project/settings.json`, not host hooks (clarification) · needs:project · today:U · install.md:332-337 · lane(s):O
I92 · §7g git-host bridge skill (`host-{gh,glab}`) is Phase-2 Claude-session generated, zero Go references ⚠ known-gap · needs:seat:cc,git · today:NONE · install.md:338-342 · lane(s):O
I93 · §7h themes — implemented, matches doc (confirmed correct) · needs:seat:cc · today:U · install.md:343-344 · lane(s):O
I94 · §7f-i MCP — implemented, matches doc (confirmed correct) · needs:network · today:U · install.md:345-348 · lane(s):O
I95 · `wireCodexAgents` naming trap — wires BOTH Claude+Codex sides under a Codex-sounding name ⚠ known-gap (reader confusion, not a functional bug) · needs:none · today:U · install.md:349-354 · lane(s):O

### Top-level CLI commands

I96 · `pfm install [--yes] [--vscode] [--skip-harvest] [--skip-engine codex] [--skip-themes] [--config-dir DIR]` · needs:systemd/launchd,network · today:U+A · cli.md:142 · lane(s):F,O
I97 · `pfm uninstall [--config-dir DIR]` · needs:systemd/launchd · today:U+A · cli.md:148 · lane(s):O
I98 · `pfm doctor [--verbose] [--skip-harvest]` command itself (exit 0/1/2/3 contract) · needs:tmux,network,git,project · today:U+A (`doctor.txtar`) · cli.md:92 · lane(s):O

---

## P — Project scaffold / update (37)

### Scaffold roster

P1 · `project/CLAUDE.md` → `CLAUDE.md` · needs:project · today:U+A (`install-init.txtar`) · install.md:145 · lane(s):A
P2 · `project/settings.json` → `.claude/settings.json` (ships `notify.sh`/`format-md.sh` hooks) · needs:project · today:U+A · install.md:146 · lane(s):A
P3 · `project/rumdl-policy.toml` → `.rumdl.toml` · needs:project · today:U · install.md:147 · lane(s):A
P4 · `project/commands` → `.claude/commands` · needs:project · today:U · install.md:148 · lane(s):A
P5 · `project/agents` (skip `per-project`) → `.claude/agents`, only `gitter.md` ships · needs:project · today:U · install.md:149 · lane(s):A
P6 · `project/scripts` → `.claude/scripts` · needs:project · today:U · install.md:150 · lane(s):A
P7 · `project/skills` → `.claude/skills` · needs:project · today:U · install.md:151 · lane(s):A
P8 · `project/epics` → `docs/epics` · needs:project · today:U · install.md:152 · lane(s):A
P9 · `project/codex` → `.codex` · needs:project · today:U · install.md:153 · lane(s):A
P10 · `project/docs-commands` → `docs/commands` · needs:project · today:U · install.md:154 · lane(s):A
P11 · `project/docs-agents` → `docs/agents` · needs:project · today:U · install.md:155 · lane(s):A

### Never deployed by `pfm` (Phase-2 Claude-session territory)

P12 · `project/per-project/CLAUDE.md` (roster child CLAUDE.md) never deployed ⚠ known-gap · needs:project · today:NONE · install.md:160 · lane(s):A
P13 · `project/agents/per-project/{developer,qa}.md` never deployed ⚠ known-gap · needs:project · today:NONE · install.md:161 · lane(s):A
P14 · `project/settings-global.json` never deployed — Phase-2 hand key-merge only ⚠ known-gap · needs:project,seat:cc · today:NONE · install.md:162-165 · lane(s):A
P15 · host git-bridge skill `.claude/skills/host-{gh,glab}/SKILL.md` never deployed — Phase-2 generated ⚠ known-gap · needs:project,git · today:NONE · install.md:166-168 · lane(s):A

### Baseline pin mechanics

P16 · `.professor/baseline.json` pin file (version/blueprint/files/ignored) · needs:project · today:U · install.md:173-178 · lane(s):A

### `pfm update check` status classes

P17 · status `current` (hash unchanged, present) · needs:project · today:U+A (`install-init.txtar`) · install.md:185 · lane(s):A
P18 · status `ignored` (never reviewed) · needs:project · today:U · install.md:186 · lane(s):A
P19 · status `UPDATED` (upstream hash changed) · needs:project · today:U · install.md:187 · lane(s):A
P20 · status `NEW` (unpinned, unignored template) · needs:project · today:U · install.md:188 · lane(s):A
P21 · status `GONE-UPSTREAM` (pinned template deleted upstream) · needs:project · today:U · install.md:189 · lane(s):A
P22 · status `LOCAL-DELETED` (pinned local file gone) · needs:project · today:U · install.md:190 · lane(s):A
P23 · `update check` exit-code contract (0/2/3/1) · needs:project · today:U+A · install.md:192-194 · lane(s):A

### `pfm update` verbs

P24 · `pfm update pin <local>...` · needs:project · today:U · install.md:197-199 · lane(s):A
P25 · `pfm update pin --all` · needs:project · today:U · install.md:197-199 · lane(s):A
P26 · `pfm update pin --template T <local>` (adopt a NEW file) · needs:project · today:U · install.md:197-199 · lane(s):A
P27 · `pfm update drop <local>...` (forget a pin; refuses if none) · needs:project · today:U · install.md:200-202 · lane(s):A
P28 · `pfm update ignore <template>...` · needs:project · today:U · install.md:203-206 · lane(s):A
P29 · `pfm update ignore --undo` · needs:project · today:U · install.md:203-206 · lane(s):A
P30 · `pfm update adopt [--root DIR]` (pin a pre-init install against store HEAD) · needs:project,git · today:U · install.md:207-213 · lane(s):A
P31 · `pfm update adopt --at REF` (pin against historical `git show REF` bytes) · needs:project,git · today:U · install.md:207-213 · lane(s):A

### Top-level CLI

P32 · `pfm init [dir] [--force]` · needs:project,git · today:U+A (`install-init.txtar`) · cli.md:165 · lane(s):A
P33 · `pfm update [--to vX.Y.Z] [--repo PATH] [--skip-harvest] [--root DIR] [--json]` (bare self-update: rebuild, doctor before/after, rollback on failure) · needs:git,network,project · today:U+A (`TestOlderPFMDiscoversUpdateThenPickerLaunchesGuidedEngine`) · cli.md:154 · lane(s):A

### Codex mirror

P34 · `pfm codex build [repo-root] [options]` (compiles `AGENTS.md`, `.codex/**`) · needs:project,git · today:U+B (`express-codex` sub-beat) · cli.md:194 · lane(s):A
P35 · `pfm codex check [repo-root] [options]` (verifies mirror without writing) · needs:project,git · today:U+B · cli.md:195 · lane(s):A
P36 · `pfm codex agents [--home PATH]` (global agent `.md`→`.toml`, symlinks both registries) · needs:seat:cc,seat:cx · today:U · cli.md:196 · lane(s):A

### OpenCode layer

P37 · `.opencode/**` layer — compiled by `build-opencode.mjs`, entirely outside the `pfm` module; no `pfm` command wires it ⚠ known-gap (confirmed absence) · needs:seat:oc · today:NONE · mcp.md:71-73 · lane(s):A

---

## C — Chat verbs (66)

C1 · `pfm ls` interactive fleet picker (cosmos TUI over `fleet.Scan`) · needs:tmux · today:U · cli.md:29 · lane(s):F
C2 · `pfm ls --plain` · needs:tmux · today:U · cli.md:29 · lane(s):F
C3 · `pfm ls --tsv` · needs:tmux · today:U · cli.md:29 · lane(s):F
C4 · `pfm ls -a/--all` · needs:tmux · today:U · cli.md:29 · lane(s):F
C5 · `pfm ls -K/--killed [--tsv]` · needs:none · today:U · cli.md:30 · lane(s):F
C6 · `pfm ls --no-sky` · needs:tmux · today:U · cli.md:29 · lane(s):F
C7 · `pfm ls --safe auto|on|off` · needs:tmux · today:U · cli.md:29 · lane(s):F
C8 · `pfm ls <id>` opens chat directly via `OpenID` · needs:tmux,seat:cc/cx/oc · today:U · cli.md:29 · lane(s):F
C9 · `pfm chat new` base launch (spawn ceremony, delivery proof, rescue retry) · needs:tmux · today:U+B (`fleet.sh`/`storm.sh` spawns) · cli.md:42 · lane(s):F
C10 · `pfm chat new --name` (mandatory) · needs:tmux · today:U · cli.md:42 · lane(s):F
C11 · `pfm chat new --engine cc|cx` · needs:tmux · today:U · cli.md:42 · lane(s):F
C12 · `pfm chat new --cwd DIR` · needs:tmux · today:U · cli.md:42 · lane(s):F
C13 · `pfm chat new --account N` · needs:tmux · today:U · cli.md:42 · lane(s):F
C14 · `pfm chat new --1h` · needs:tmux · today:U · cli.md:42 · lane(s):F
C15 · `pfm chat new --model M` · needs:tmux · today:U · cli.md:42 · lane(s):F
C16 · `pfm chat new --effort E` · needs:tmux · today:U · cli.md:42 · lane(s):F
C17 · `pfm chat new --prompt-file PATH` · needs:tmux · today:U · cli.md:42 · lane(s):F
C18 · `pfm chat new --role ROLE` · needs:tmux · today:U · cli.md:42 · lane(s):F
C19 · `pfm chat new --await [--timeout S] [--settle S] [--progress]` · needs:tmux · today:U · cli.md:42 · lane(s):F
C20 · `pfm chat new --attach` · needs:tmux · today:U · cli.md:42 · lane(s):F
C21 · `pfm chat open <target>` · needs:tmux · today:U · cli.md:43 · lane(s):F
C22 · `pfm chat status <target>` base · needs:tmux · today:U · cli.md:44 · lane(s):E1
C23 · `pfm chat status --json` · needs:tmux · today:U · cli.md:44 · lane(s):E1
C24 · `pfm chat status --summary` · needs:tmux · today:U · cli.md:44 · lane(s):E1
C25 · `pfm chat status --ask` · needs:tmux · today:U · cli.md:44 · lane(s):E1
C26 · `pfm chat status --engine claude|codex` (requires `--summary`/`--ask`) · needs:tmux · today:U · cli.md:44 · lane(s):E1
C27 · `pfm chat status --model MODEL` · needs:tmux · today:U · cli.md:44 · lane(s):E1
C28 · `pfm chat last <target>` · needs:none · today:U · cli.md:45 · lane(s):E1
C29 · `pfm chat read <target> [--tail N] [--condensed] [--json]` · needs:none · today:U · cli.md:46 · lane(s):E1
C30 · `pfm chat read` excerpt-file compatibility form · needs:none · today:U · cli.md:46 · lane(s):E1
C31 · `pfm chat stream <target> [--filter REGEX] [--margin N] [--from-start] [--raw] [--no-follow]` · needs:tmux · today:NONE (no dedicated test file found) · cli.md:47 · lane(s):E1
C32 · `pfm chat inject <target> <message>` base delivery · needs:tmux · today:U+B (`check_inject`) · cli.md:48 · lane(s):E1,E2
C33 · `pfm chat inject --force-now` · needs:tmux · today:U · cli.md:48 · lane(s):E1
C34 · `pfm chat inject --then STEER` (repeatable) · needs:tmux · today:U · cli.md:48 · lane(s):E1
C35 · `pfm chat inject --file PATH` · needs:tmux · today:U · cli.md:48 · lane(s):E1
C36 · `pfm chat inject --allow-unsigned` · needs:tmux · today:U+A (`chat-lifecycle.txtar`) · cli.md:48 · lane(s):E1
C37 · `pfm chat inject` refuses bare `/compact` (use self-compact) · needs:tmux · today:U · cli.md:48 · lane(s):E1
C38 · `pfm chat self-compact --then STEER <focus>` · needs:tmux · today:U+B (`check_compact`) · cli.md:49 · lane(s):E1
C39 · `pfm chat ask [--timeout S] [--settle S] [--now] [--json] [--progress]` · needs:tmux · today:U · cli.md:50 · lane(s):E1,E2
C40 · `pfm chat watch <target> [--idle-after S] [--on-idle CMD] [--on-exit CMD] [--once] [--poll S]` · needs:tmux · today:NONE (no dedicated test file found) · cli.md:51 · lane(s):E1,E2
C41 · `pfm chat capture <target>` · needs:tmux · today:NONE (no dedicated test file found) · cli.md:52 · lane(s):E1
C42 · `pfm chat keys [--delay ms] [--literal] [--capture] <target> <key>...` · needs:tmux · today:U · cli.md:53 · lane(s):E1
C43 · `pfm chat recover <thread-id|rollout-path>` · needs:none · today:U · cli.md:54 · lane(s):E2
C44 · `pfm chat name <target> <name>` · needs:tmux · today:U · cli.md:55 · lane(s):E1
C45 · `pfm chat name`'s raw delivery-failure exit code bypasses `writeInjectResult` (returns `deliver`'s unmapped `resultCode`) ⚠ known-gap · needs:tmux · today:U · cli.md:237 · lane(s):E1
C46 · `pfm chat kill <target> [--exit]` · needs:tmux · today:U · cli.md:56 · lane(s):E1
C47 · `pfm chat kill self/me` alias (incl. tmux-less Codex tool-shell via `CODEX_THREAD_ID`) · needs:tmux,seat:cx · today:U · cli.md:56 · lane(s):E2
C48 · `pfm chat unkill <target>` · needs:none · today:U · cli.md:57 · lane(s):E1
C49 · `pfm chat end <target>` (kills whole tmux server) · needs:tmux · today:U · cli.md:58 · lane(s):E1
C50 · `pfm chat reload --account N` · needs:tmux,seat:cc · today:U+B (`check_reload`) · cli.md:59 · lane(s):E1
C51 · `pfm chat reload --model M` · needs:tmux,seat:cc · today:U · cli.md:59 · lane(s):E1
C52 · `pfm chat reload --effort E` · needs:tmux,seat:cc · today:U · cli.md:59 · lane(s):E1
C53 · `pfm chat reload --1h on|off` · needs:tmux,seat:cc · today:U · cli.md:59 · lane(s):E1
C54 · `pfm chat reload --new` · needs:tmux,seat:cc · today:U · cli.md:59 · lane(s):E1
C55 · `pfm chat reload --new --hide` · needs:tmux,seat:cc · today:U · cli.md:59 · lane(s):E1
C56 · `pfm chat reload --then "prompt"` · needs:tmux,seat:cc · today:U+B · cli.md:59 · lane(s):E1
C57 · `pfm chat reload --sock socket` · needs:tmux,seat:cc · today:U · cli.md:59 · lane(s):E1
C58 · `pfm chat find <excerpt-file>` · needs:none · today:U · cli.md:60 · lane(s):F
C59 · `pfm chat save <target-file> [transcript-jsonl]` · needs:git · today:U · cli.md:61 · lane(s):F
C60 · `pfm chat ls [--all]` compatibility listing · needs:tmux · today:U · cli.md:62 · lane(s):F
C61 · `pfm chat branch [--engine claude|codex] [--session-id ID] [--cwd DIR] [--account N] [--name NAME]` · needs:tmux · today:U · cli.md:63 · lane(s):E1
C62 · `pfm chat history <sid-prefix|jsonl-path> [messages] [project-slug]` · needs:none · today:U · cli.md:64 · lane(s):F
C63 · `pfm chat modal <tmux-session> deny <down-count>` · needs:tmux · today:U · cli.md:65 · lane(s):F
C64 · `pfm chat resolve <target>` · needs:tmux · today:U · cli.md:66 · lane(s):F
C65 · `pfm chat whoami` (alias of `pfm whoami`) · needs:tmux · today:U · cli.md:67 · lane(s):O
C66 · Shared headless-verb exit contract (0/2/3/4/5/6/7 across ask/status/read/last/stream/inject/watch/self-compact) · needs:none · today:U · cli.md:34-38 · lane(s):E1

---

## K — Chat kinds / labels / engines (40)

K1 · Engine: Claude Code (`cc`) · needs:seat:cc · today:U · fleet.md:21 · lane(s):E1
K2 · Engine: Codex (`cx`) · needs:seat:cx · today:U · fleet.md:22 · lane(s):E2
K3 · Engine: OpenCode (`ox`) · needs:seat:oc · today:U · fleet.md:23 · lane(s):E3
K4 · Engine resolution: `--engine` on `chat new`/`branch` · needs:tmux · today:U · fleet.md:25-27 · lane(s):F
K5 · Engine resolution: fallback to caller's own engine then `machine.DefaultEngine()` · needs:tmux · today:U · fleet.md:27-28 · lane(s):F
K6 · Engine resolution: `engine.FromSocket` by prefix match, unknown prefix = named absence (never a silent fallback) · needs:tmux · today:U · fleet.md:29-31 · lane(s):F
K7 · Tmux socket naming law (`<prefix><epoch>-<pid>-<rand>`) · needs:tmux · today:U · fleet.md:35-42 · lane(s):F
K8 · Named chat: `--name` mandatory on `chat new` · needs:tmux · today:U · fleet.md:46 · lane(s):F
K9 · Unnamed sentinel `(unnamed)` display (never addressable; two unnamed ≠ one chat) · needs:tmux · today:U · fleet.md:47-49 · lane(s):F
K10 · Naming precedence, resumable row (custom title / AI title / first prompt) · needs:none · today:U · fleet.md:50-51 · lane(s):F
K11 · Naming precedence, live row (indexed / pane title / session name / last prompt / isCCSock) · needs:tmux · today:U · fleet.md:52-54 · lane(s):F
K12 · `{name}:{group}` label grammar (`nameGroupPrefix`, excludes stray prose colons) · needs:none · today:U (golden) · fleet.md:58-68 · lane(s):E1,F
K13 · Hidden/killed: name-based self-kill via `_KILL`/`_HIDE` prefix (case-insensitive, no store row) · needs:tmux · today:U · fleet.md:74-81 · lane(s):F
K14 · Hidden/killed: store-based kill via fleetdb `hidden` table (K2 prompt-ratchet auto-unkill) · needs:none · today:U · fleet.md:82-91 · lane(s):F
K15 · Headless-by-construction: every `chat new` launch has no attached terminal by default · needs:tmux · today:U · fleet.md:97-101 · lane(s):F
K16 · `--attach` opt-in interactive tmux-attach line · needs:tmux · today:U · fleet.md:100-101 · lane(s):F
K17 · `pfm headless exec`/`run` scripting front (`run`→`new`, `transcript`→`read` aliases) · needs:seat:cc/cx/oc,network · today:U · fleet.md:102-104 · lane(s):O
K18 · Storm chats — NOT a `pfm` CLI verb; `infra/demo/storm.sh` wraps ordinary `chat new`/`end`/`kill` · needs:tmux,network · today:B (`check_storm`) · fleet.md:106-124 · lane(s):F
K19 · `pfm storm`/`pfm idle` confirmed NOT to exist as subcommands (documented absence, not a failed search) · needs:none · today:NONE · fleet.md:118-124,252-255 · lane(s):F
K20 · Role/prompt-file: `--role ROLE` composes a registered agent constitution ahead of the prompt · needs:tmux · today:U · fleet.md:128-131 · lane(s):F
K21 · Role/prompt-file: `--prompt-file PATH` (mutually exclusive with inline prompt) · needs:tmux · today:U · fleet.md:131-134 · lane(s):F
K22 · Role re-arm crumb write so `reload`/`self-compact` can re-apply the role after a reset · needs:tmux · today:U · fleet.md:134-137 · lane(s):E1
K23 · Account/seat: `--account N` on `chat new`/`reload`/`branch`, hard error on unconfigured id · needs:tmux · today:U · fleet.md:141-146 · lane(s):E1,O
K24 · Account medal-emoji identity (🥇🥈🥉, retired 🍀) across label/statusline/TUI · needs:tmux · today:U · fleet.md:146-149 · lane(s):F
K25 · 1h cache mode: `--1h` on `chat new` · needs:tmux,seat:cc · today:U · fleet.md:153 · lane(s):F
K26 · 1h cache mode: `--1h on|off` on `chat reload` · needs:tmux,seat:cc · today:U · fleet.md:153-154 · lane(s):E1
K27 · 1h cache mode: TUI `ctrl+e` toggle · needs:tmux · today:U (golden) · fleet.md:154-157 · lane(s):F
K28 · Model/effort: `--model M --effort E` on `chat new`/`reload` (empty = inherit) · needs:tmux · today:U · fleet.md:161-164 · lane(s):F
K29 · Row Kind: `LiveClaude` (live seat, addressable) · needs:tmux · today:U (golden) · fleet.md:184 · lane(s):F
K30 · Row Kind: `LiveCodex` · needs:tmux · today:U · fleet.md:185 · lane(s):F
K31 · Row Kind: `LiveSplit` · needs:tmux · today:U · fleet.md:186 · lane(s):F
K32 · Row Kind: `Agent` (addressable, not a live seat) · needs:tmux · today:U · fleet.md:187 · lane(s):F
K33 · Row Kind: `ResumeClaude` · needs:none · today:U · fleet.md:188 · lane(s):F
K34 · Row Kind: `ResumeCodex` · needs:none · today:U · fleet.md:189 · lane(s):F
K35 · Row Kind: `NewClaude` · needs:none · today:U · fleet.md:190 · lane(s):F
K36 · Row Kind: `NewCodex` · needs:none · today:U · fleet.md:191 · lane(s):F
K37 · Row Kind: `Booting` (pane live, no SID crumb yet — only Enter legal) · needs:tmux · today:U · fleet.md:192,197-198 · lane(s):F
K38 · Row Kind: `ResumeOpenCode` · needs:none · today:U · fleet.md:193 · lane(s):F
K39 · Row Kind: `NewOpenCode` · needs:none · today:U · fleet.md:194 · lane(s):F
K40 · Row Kind: `ProfessorUpdate` (interactive-only, inserted by `cmd/pfm` ahead of the merged new-chat row) · needs:tmux · today:U · fleet.md:195,198-201 · lane(s):F

---

## T — TUI (38)

T1 · `pfm ls` interactive picker entry (`/dev/tty`, FPS 10) · needs:tmux · today:U (golden) · fleet.md:268-270 · lane(s):F
T2 · `PlainPicker` (`--plain`) noninteractive twin · needs:tmux · today:U · fleet.md:271-272 · lane(s):F
T3 · `TSVPicker` (`--tsv`) noninteractive twin · needs:tmux · today:U · fleet.md:271-272 · lane(s):F
T4 · Tab: Chats (default) · needs:tmux · today:U (golden) · fleet.md:276-284 · lane(s):F
T5 · Tab: Stats (StatsChats/StatsDocker, sort by `c`=CPU/`m`=RAM) · needs:tmux · today:U · fleet.md:285-291 · lane(s):F
T6 · Tab: Limits (rate-limit/usage cards, paginated) · needs:tmux,network · today:U · fleet.md:292-296 · lane(s):F
T7 · Tab: Cosmos (fleet activity graph, comms ledger) · needs:tmux · today:U · fleet.md:297-305 · lane(s):F
T8 · Tab cycling (`tab`/`shift+tab`) · needs:tmux · today:U · fleet.md:276-277,309 · lane(s):F
T9 · No literal "fullscreen" tab/view exists (confirmed absence) · needs:tmux · today:U · fleet.md:278-279 · lane(s):F
T10 · Global key: `esc`/`ctrl+c` cancel · needs:tmux · today:U · fleet.md:309 · lane(s):F
T11 · Global key: `left`/`right` navigate (carousel or horizontal) · needs:tmux · today:U · fleet.md:310-311 · lane(s):F
T12 · Chats-tab key: `ctrl+x` toggle killed view · needs:tmux · today:U · fleet.md:313 · lane(s):F
T13 · Chats-tab key: `ctrl+e` toggle 1h cache · needs:tmux · today:U · fleet.md:313-314 · lane(s):F
T14 · Chats-tab key: `ctrl+s` cycle selected account · needs:tmux · today:U · fleet.md:314 · lane(s):F
T15 · Chats-tab key: `ctrl+o` reboot/reload selected live seat · needs:tmux · today:U · fleet.md:314-316 · lane(s):F
T16 · Chats-tab key: `enter` select/act (carousel action index 0-4) · needs:tmux · today:U · fleet.md:316-317 · lane(s):F
T17 · Chats-tab key: `up`/`ctrl+p`, `down`/`ctrl+n`, `pgup`, `pgdown`, `home`, `end` cursor moves · needs:tmux · today:U · fleet.md:317-319 · lane(s):F
T18 · Chats-tab key: `backspace`/`ctrl+h` delete-char, `ctrl+w` delete-word, `ctrl+u` clear query · needs:tmux · today:U · fleet.md:319-320 · lane(s):F
T19 · Stats-tab key: `down`/`up` walk `StatsFocus`; `c`/`m` sort · needs:tmux · today:U · fleet.md:322-324 · lane(s):F
T20 · Limits-tab key: `down`/`up`/`pgdown`/`pgup`/`home`/`end` scroll cards · needs:tmux,network · today:U · fleet.md:326-328 · lane(s):F
T21 · Cosmos-tab key: `o` toggle classic-sky rendering · needs:tmux · today:U · fleet.md:330-331 · lane(s):F
T22 · Cosmos-tab key: `up`/`k`, `down`/`j` move star/seat selection · needs:tmux · today:U · fleet.md:331-332 · lane(s):F
T23 · Cosmos-tab key: `enter` open selected chat · needs:tmux · today:U · fleet.md:332 · lane(s):F
T24 · Cosmos-tab key: `s` cycle focus · needs:tmux · today:U · fleet.md:332 · lane(s):F
T25 · Cosmos-tab key: `[`/`]` scrub ±5min, `{`/`}` scrub ±1h · needs:tmux · today:U · fleet.md:332-334 · lane(s):F
T26 · Cosmos-tab key: `space` toggle replay playback (1 real sec = 1 replayed min) · needs:tmux · today:U · fleet.md:334-336 · lane(s):F
T27 · Cosmos-tab key: `n` return to now · needs:tmux · today:U · fleet.md:336 · lane(s):F
T28 · TUI golden-file regression testing (23 fixed-width ANSI frames, UTC-pinned clock) · needs:none · today:U · fleet.md:341-347 · lane(s):F
T29 · TUI stress test (5,000-row synthetic snapshot, `PFM_STRESS_STRICT=1`) · needs:none · today:U · fleet.md:349-351 · lane(s):F
T30 · TUI rendered/captured end-to-end in exactly one e2e test; nothing else exercises rendered TUI at any tier ⚠ known-gap · needs:tmux · today:A · tests.md:385 · lane(s):F
T31 · `pfm statusline [--refresh-gpt]` render (account badge→git→effort→rate-limit→Codex usage→cache-window segments, fail-open) · needs:none · today:U · cli.md:130 · lane(s):E1,E2,E3
T32 · `pfm statusline --refresh-gpt` (execs codex rate-limit read) · needs:network,seat:cx · today:U · cli.md:130 · lane(s):E2
T33 · Statusline side effect: `convergeWindowName` renames the tmux window on a single-pane Claude render (staleness-cached) · needs:tmux,seat:cc · today:U · fleet.md:367-375 · lane(s):E1
T34 · `pfm internal tmux-title-renudge` sweep re-emits OSC titles on every live socket · needs:tmux · today:U · fleet.md:377-381 · lane(s):O
T35 · Theme wiring: `custom:professor-gold/silver/bronze` selectable via `settings.json` or `/theme` · needs:seat:cc · today:U (`themes_test.go`) · fleet.md:383-397 · lane(s):E1
T36 · pfm TUI's own internal theme palettes (`default`/`tokyo-night`, distinct from Claude Code CLI themes) · needs:tmux · today:U · fleet.md:394-397 · lane(s):F
T37 · `/reload` installed slash-command file (body substituted from `reload.Usage`, never drifts) · needs:seat:cc · today:NONE ⚠ known-gap (no doctor row for link health) · fleet.md:404-412 · lane(s):O
T38 · `/handoff [--branch]` installed skill (writes handoff file, reboots pane or spawns detached sibling) · needs:seat:cc,tmux · today:U · fleet.md:413-419 · lane(s):E1

---

## M — MCP (55)

### Chat fleet server — 18 tools

M1 · `chat_ls` (all/killed/project/limit) · needs:tmux · today:U · mcp.md:15 · lane(s):M
M2 · `chat_resolve` (kind: label|session|cxwin, name) · needs:tmux · today:U · mcp.md:16 · lane(s):M
M3 · `chat_inject` (target/message/force_now/then) · needs:tmux · today:U · mcp.md:17 · lane(s):M
M4 · `chat_inject` self-target absence-as-result contract (unresolved self is not a Go error) ⚠ known-gap (asymmetric error contract) · needs:tmux · today:U · mcp.md:17 · lane(s):M
M5 · `chat_self_compact` (focus/then, always targets the requesting seat) · needs:tmux · today:U · mcp.md:18 · lane(s):M
M6 · `chat_keys` (target/keys/literal/delay_ms/capture) · needs:tmux · today:U · mcp.md:19 · lane(s):M
M7 · `chat_keys` mixed result+error contract on mid-sequence death (partial `KeysOutput` AND a Go error together) ⚠ known-gap · needs:tmux · today:U · mcp.md:19 · lane(s):M
M8 · `chat_capture` (target/tail_lines/max_bytes) · needs:tmux · today:U · mcp.md:20 · lane(s):M
M9 · `chat_whoami` (stdio-ambient or Codex `_meta.threadId`) · needs:tmux,seat:cx · today:U · mcp.md:21 · lane(s):M
M10 · `chat_find` (excerpt/limit/include_self) · needs:none · today:U · mcp.md:22 · lane(s):M
M11 · `chat_read` (source/last_n/max_bytes) · needs:none · today:U · mcp.md:23 · lane(s):M
M12 · `chat_last` (target) · needs:tmux · today:U · mcp.md:24 · lane(s):M
M13 · `chat_status` (target/summary/ask/engine/model) · needs:tmux,network · today:U · mcp.md:25 · lane(s):M
M14 · `chat_new` (name/engine/cwd/account/1h/model/effort/prompt/await/timeout/settle/progress/attach) · needs:tmux · today:U · mcp.md:26 · lane(s):M
M15 · `chat_open` (target) — no dedicated test file ⚠ known-gap · needs:tmux · today:NONE · mcp.md:27 · lane(s):M
M16 · `chat_name` (target/name) — no dedicated test file ⚠ known-gap · needs:tmux · today:NONE · mcp.md:28 · lane(s):M
M17 · `chat_kill` (target/exit) — no dedicated test file ⚠ known-gap · needs:tmux · today:NONE · mcp.md:29 · lane(s):M
M18 · `chat_unkill` (target) — no dedicated test file ⚠ known-gap · needs:none · today:NONE · mcp.md:30 · lane(s):M
M19 · `chat_save` (target/transcript, refuses a non-path-shaped target) · needs:none · today:U · mcp.md:31 · lane(s):M
M20 · `issue_servicedesk` (title/detail/severity/area, `UNIDENTIFIED` sentinel fallback) · needs:none · today:U · mcp.md:32 · lane(s):M

### Harvester server — 6 nominal tools + 1 prompt

M21 · `fetch` (sources 1-50/refresh/size_only) — no dedicated handler test ⚠ known-gap · needs:network · today:NONE · mcp.md:42 · lane(s):M
M22 · `findWorks` (query/limit) — no dedicated handler test ⚠ known-gap · needs:network · today:NONE · mcp.md:43 · lane(s):M
M23 · `search` (query/count/lang/engines), config-conditional — hidden, not erroring, when unconfigured · needs:network · today:U (`search_gate_test.go`) · mcp.md:44 · lane(s):M
M24 · `search` backend-failure-as-data contract (`IsError` flag, `nil` Go error) ⚠ known-gap (asymmetric vs. `findWorks`/`searchCache`) · needs:network · today:U · mcp.md:44,106 · lane(s):M
M25 · `fetchImage` (sources 1-50) — no dedicated handler test ⚠ known-gap · needs:network · today:NONE · mcp.md:45 · lane(s):M
M26 · `archive` (source/member, traversal/symlink/size guards) — no dedicated test for the fetch/error path ⚠ known-gap · needs:network · today:NONE · mcp.md:46 · lane(s):M
M27 · `searchCache` (pattern/max_results/ignore_case, local-only, no network) · needs:none · today:U (only the empty-match path is tested) · mcp.md:47 · lane(s):M
M28 · `searchCache`'s typed `CacheOutput`/`CacheHit` struct is defined but never returned over the wire ⚠ known-gap (dead type) · needs:none · today:NONE · mcp.md:47,106 · lane(s):M
M29 · `fetch` Prompt — a non-tool MCP primitive, not counted in the 6-tool surface · needs:network · today:NONE · mcp.md:38 · lane(s):M

### Registration per engine

M30 · Both `chat`+`harvester` servers default-disabled at config layer, require `pfm mcp <server> enable` · needs:none · today:U · mcp.md:53 · lane(s):M
M31 · Claude registration: chat server wired stdio per account (`~/.claude.json`) · needs:seat:cc · today:U · mcp.md:55-62 · lane(s):M
M32 · Claude registration: harvester server wired HTTP per account · needs:seat:cc,network · today:U · mcp.md:55-61 · lane(s):M
M33 · Claude re-install maintains prior pfm registration shape (`isPFMStdioClient`/`isPFMHTTPClient`) · needs:seat:cc · today:U · mcp.md:63 · lane(s):M
M34 · Codex registration: both chat+harvester wired HTTP in `config.toml [mcp_servers]` fence · needs:seat:cx,network · today:U · mcp.md:65-69 · lane(s):E2,M
M35 · Codex fenced block preserves a pre-existing foreign `[mcp_servers]` entry of the same name · needs:seat:cx · today:U · mcp.md:68 · lane(s):M
M36 · OpenCode MCP wiring — confirmed NOT implemented anywhere in the pfm installer ⚠ known-gap · needs:seat:oc · today:NONE · mcp.md:71-73 · lane(s):E3,M
M37 · `pfm doctor` MCP registration-file classification (PFM/Absent/ForeignRegistration/LegacyStandalone/Unreadable) · needs:seat:cc,network · today:U · mcp.md:75-79 · lane(s):M
M38 · `pfm doctor` historical Codex + project-scope harvester cutover inspection · needs:seat:cx · today:U · mcp.md:80 · lane(s):M
M39 · `pfm doctor` live daemon reachability probe (`GET /status`, version-skew warning) · needs:network · today:U · mcp.md:81 · lane(s):M

### Daemon

M40 · `pfm mcp serve` entry point (port validation; refuses if all disabled or already running) · needs:network · today:U · mcp.md:87 · lane(s):M
M41 · Daemon single loopback port (default 18377), routes `/mcp/chat` + `/mcp/harvester` + `/status` · needs:network · today:U+B (`check_daemon`) · mcp.md:88 · lane(s):M
M42 · Daemon disabled route answers `503` with a named remedy (not a bare 404) · needs:network · today:U · mcp.md:88 · lane(s):M
M43 · Daemon refuses cross-origin/browser requests (any `Origin` header) · needs:network · today:U · mcp.md:88 · lane(s):M
M44 · Daemon chat is HTTP-only, never ambient (`AllowAmbientIdentity:false`) · needs:network · today:U · mcp.md:89 · lane(s):M
M45 · External harvester gateway (optional, OAuth/bearer-walled, default port 18378) · needs:network · today:U · mcp.md:90 · lane(s):M
M46 · Restart-on-replaced-binary via `binwatch` (5s poll, 30s drain, exit 75) · needs:none · today:U+B (`daemon.sh` stale-binary restart) · mcp.md:91 · lane(s):M
M47 · systemd unit `pfm-mcp.service` (`Restart=on-failure`) · needs:systemd/launchd · today:U · mcp.md:91 · lane(s):M
M48 · launchd plist `com.professor.pfm.mcp.plist` (`KeepAlive=true`) ⚠ known-gap (unverified on Linux host) · needs:systemd/launchd · today:NONE · mcp.md:91 · lane(s):M
M49 · `pfm mcp chat serve` stdio path (`AllowAmbientIdentity:true`, only transport resolving self via ambient tmux/process ancestry) · needs:tmux · today:U · mcp.md:93 · lane(s):M
M50 · `pfm mcp harvester serve [--transport stdio]` stdio path; retired flags each error by name · needs:network · today:U · mcp.md:94 · lane(s):M
M51 · Stdio pre-filter: malformed JSON-RPC frame answered with `-32700` parse error, connection not killed · needs:none · today:U · mcp.md:95 · lane(s):M
M52 · `pfm mcp` bare alias for `pfm mcp chat serve` · needs:tmux · today:U · cli.md:183 · lane(s):M
M53 · `pfm mcp ls` (list registered servers/enabled-state/source) · needs:none · today:U · cli.md:184 · lane(s):M
M54 · `pfm mcp <server> enable|disable` · needs:none · today:U · cli.md:185 · lane(s):M
M55 · `pfm mcp serve` top-level daemon dispatch form (exactly `pfm mcp serve`) · needs:network · today:U · cli.md:188 · lane(s):M

---

## L — Lifecycle mechanics (42)

L1 · Idle detection: state derived from transcript+socket only, never a pane scrape (except `Ask`) · needs:none · today:U · fleet.md:426-429 · lane(s):F
L2 · Idle detection: no transcript + `Live` → `StateWorking` · needs:tmux · today:U · fleet.md:431-432 · lane(s):F
L3 · Idle detection: `ReadMeta` `fs.ErrNotExist` + `Live` → `StateWorking` (other read errors are real errors, not absence) · needs:tmux · today:U · fleet.md:433-435 · lane(s):F
L4 · Idle detection: `StateIdle` iff newest record role = assistant; `StateWorking` if tool-call/human-turn regardless of quiet duration · needs:none · today:U · fleet.md:436-439 · lane(s):F
L5 · Sidechain override: `newerClaudeSidechain` forces `StateWorking` when a subagent transcript is newer than its parent · needs:none · today:U · fleet.md:440-444 · lane(s):E1,F
L6 · `IdleSeconds` populated only when `State==StateIdle` · needs:none · today:U · fleet.md:445-447 · lane(s):F
L7 · `Missing(name)` explicit absence value, never silent · needs:none · today:U · fleet.md:448-449 · lane(s):F
L8 · Reap classification: `self` · needs:tmux · today:U · fleet.md:457 · lane(s):O
L9 · Reap classification: `keep` (attached) · needs:tmux · today:U · fleet.md:457-458 · lane(s):O
L10 · Reap classification: `mate` (`cc-new-*` detached teammate, reaped only by parent's close choreography) · needs:tmux · today:U · fleet.md:458-459 · lane(s):O
L11 · Reap classification: `busy` (engine self-reports working) · needs:tmux · today:U · fleet.md:459 · lane(s):O
L12 · Reap classification: `active` (transcript written moments ago) · needs:tmux · today:U · fleet.md:459-460 · lane(s):O
L13 · Reap classification: `hosts` (panes hosting non-chat processes, never reapable) · needs:tmux · today:U · fleet.md:460-461 · lane(s):O
L14 · Reap classification: `fork` (untouched detached `/chat:branch` seat, reapable) · needs:tmux · today:U · fleet.md:462-463 · lane(s):O
L15 · Reap classification: `orph` (unattached idle — THE reapable case) · needs:tmux · today:U · fleet.md:463 · lane(s):O
L16 · Reap classification: `IDLE` (attached but both transcript+window_activity stale) · needs:tmux · today:U · fleet.md:464-466 · lane(s):O
L17 · Reap classification: `KILL` (orphan actually killed this run) · needs:tmux · today:U+A (`reap-preview.txtar`) · fleet.md:466 · lane(s):O
L18 · Reap classification: `dead` (socket file, no server, old enough) · needs:tmux · today:U · fleet.md:467 · lane(s):O
L19 · Reap classification: `SKIP` (deliberately left alone, reason attached) · needs:tmux · today:U · fleet.md:467 · lane(s):O
L20 · Reap classification: `UNKN` (transcript unreadable — never reapable regardless of idle time) · needs:tmux · today:U · fleet.md:467-469 · lane(s):O
L21 · Reap actions: `None`/`KillServer`/`RemoveSocketFile`/`KillSession` · needs:tmux · today:U · fleet.md:470-471 · lane(s):O
L22 · `pfm reap` dry-run default (matches `--apply` plan exactly) · needs:tmux · today:U+A (`reap-preview.txtar`) · cli.md:106 · lane(s):O
L23 · `pfm reap --apply` · needs:tmux · today:U+A · cli.md:106 · lane(s):O
L24 · `pfm reap --horizon 48h` · needs:tmux · today:U · cli.md:106 · lane(s):O
L25 · `pfm reap --busy-recent SECONDS` · needs:tmux · today:U · cli.md:106 · lane(s):O
L26 · `pfm reap --json` · needs:tmux · today:U · cli.md:106 · lane(s):O
L27 · Inject busy/menu guards: `IsBusy` spinner-regex detection · needs:tmux · today:U · fleet.md:169-175,476-478 · lane(s):E1
L28 · Inject busy/menu guards: `SelectorLine`/menu detection (Claude `❯` vs Codex `›` regexes) · needs:tmux · today:U · fleet.md:171-172 · lane(s):E1
L29 · Inject busy/menu guards: draft detection (excludes spinner/idle-sparkle glyphs) · needs:tmux · today:U · fleet.md:172-174 · lane(s):E1
L30 · Inject busy/menu guards: compaction-receipt regex · needs:tmux · today:U · fleet.md:174-175 · lane(s):E1
L31 · Inject delivery proof requires visible-past-composer OR paste-placeholder OR queue-proof-text OR busy false→true flip (never "composer is empty" alone) · needs:tmux · today:U · fleet.md:479-484 · lane(s):E1
L32 · Reload worker: reboot-in-place under new account/cache/model/effort · needs:tmux,seat:cc · today:U+B (`check_reload`) · fleet.md:486-494 · lane(s):E1,O
L33 · Reload worker: `--new` fresh-session variant · needs:tmux,seat:cc · today:U · fleet.md:486-494 · lane(s):E1
L34 · Reload worker: detached scheduling via `pfm internal reload-run` · needs:tmux,seat:cc · today:U · cli.md:59,222 · lane(s):E1
L35 · Self-compact scheduling: `ScheduleSelfCompact` validates one control-char-free focus line · needs:tmux · today:U · fleet.md:498-502 · lane(s):E1
L36 · Self-compact scheduling composes `"/compact "+focus` (Codex: bare `/compact` — an unverified assumption, named "held, not disproved") ⚠ known-gap · needs:tmux,seat:cx · today:U · fleet.md:500-502 · lane(s):E2
L37 · Self-compact scheduling queued via `ScheduleAfterCurrentTurn` (never races a live `/compact`) · needs:tmux · today:U · fleet.md:502-503 · lane(s):E1
L38 · Self-compact: `rolePointer` re-attaches a `--role` seat's remembered constitution post-reset · needs:tmux · today:U · fleet.md:506-508 · lane(s):E1
L39 · Kill-storm — NOT a `pfm` mechanism; `infra/demo/kill-storm.sh` wraps ordinary `chat end`+`chat kill` on `STORM_[0-9]+` names · needs:tmux,network · today:B (`check_storm`) · fleet.md:510-517 · lane(s):F
L40 · `pfm name-sync [--apply] [--dry-run]` converges tmux window names (Codex thread name / Claude 🔖 label) · needs:tmux · today:U+A (`name-sync.txtar`) · cli.md:124 · lane(s):F
L41 · `pfm name-sync --apply` also converges tmux global title options, re-verifies every rename · needs:tmux · today:U+A · cli.md:124 · lane(s):F
L42 · name-sync never runs more than once concurrently by design (systemd path unit/timer/picker-refresh entry points) · needs:tmux,systemd/launchd · today:U · fleet.md:260-264 · lane(s):F

---

## H — Harvester: CLI + sidecar + cache + search (12)

H1 · `pfm harvest [--refresh] [--size-only] [--json] <sources>...` (1-50 sources, ordered results) · needs:network · today:U · cli.md:73 · lane(s):O
H2 · `pfm harvest ask -p <prompt> [--engine claude|codex] [--model M] [--effort E] [--refresh] <sources>...` · needs:network,seat:cc/cx · today:U+A (`TestHarvestAskE2E`) · cli.md:74 · lane(s):O
H3 · Harvest source kind: URL · needs:network · today:U · cli.md:73 · lane(s):O
H4 · Harvest source kind: DOI · needs:network · today:U · cli.md:73 · lane(s):O
H5 · Harvest source kind: ISBN · needs:network · today:U · cli.md:73 · lane(s):O
H6 · Harvest source kind: PMID · needs:network · today:U · cli.md:73 · lane(s):O
H7 · Harvest source kind: PMCID · needs:network · today:U · cli.md:73 · lane(s):O
H8 · Harvest source kind: local path · needs:none · today:U · cli.md:73 · lane(s):O
H9 · Harvestpy pinned Python conversion sidecar (non-HTML document conversion) · needs:network · today:U (`internal/harvestpy`, 37 tests) · mcp.md:42 · lane(s):O
H10 · Harvest local cache (backs `fetch`/`fetchImage` results, read by `searchCache`) · needs:none · today:U · mcp.md:42-47 · lane(s):M
H11 · Harvest search-backend config (SearXNG URL or Brave API key gates the `search` tool's visibility) · needs:network · today:U (`search_gate_test.go`) · mcp.md:44 · lane(s):M
H12 · Tier B: `setup.sh install` falls back to `--skip-harvest` silently on provisioning failure; no `verify.sh` beat asserts the harvester landed ⚠ known-gap · needs:network,docker · today:NONE · tests.md:391,437 · lane(s):O

---

## X — Misc CLI (41)

X1 · `pfm version` / `pfm --version` · needs:none · today:U · cli.md:23 · lane(s):O
X2 · `pfm config init [--force]` · needs:none · today:U · cli.md:98 · lane(s):O
X3 · `pfm config show` · needs:none · today:U · cli.md:99 · lane(s):O
X4 · `pfm config validate` · needs:none · today:U · cli.md:100 · lane(s):O
X5 · `pfm index [--full] [--progress]` · needs:none · today:U · cli.md:86 · lane(s):O
X6 · `pfm archive [--apply] [--subagents [--older-than DAYS]] [--restore id] [--prune-orphans [--yes]]` base · needs:none · today:U · cli.md:112 · lane(s):O
X7 · `pfm archive --apply` · needs:none · today:U · cli.md:112 · lane(s):O
X8 · `pfm archive --subagents [--older-than DAYS]` · needs:none · today:U · cli.md:112 · lane(s):O
X9 · `pfm archive --restore id` · needs:none · today:U · cli.md:112 · lane(s):O
X10 · `pfm archive --prune-orphans [--yes]` · needs:none · today:U · cli.md:112 · lane(s):O
X11 · `pfm heal [--apply | --thread id]` (report/rebuild wedged Codex thread-history) · needs:seat:cx · today:U · cli.md:118 · lane(s):O
X12 · `pfm issues [--all] [--json]` (servicedesk complaint listing) · needs:none · today:U · cli.md:177 · lane(s):O
X13 · `pfm whoami [--json | --label]` · needs:tmux · today:U · cli.md:171 · lane(s):O
X14 · `pfm usage-hook` fail-open UserPromptSubmit usage-limit warning (Codex no-op) · needs:network · today:U · cli.md:136 · lane(s):O
X15 · `pfm headless [exec] [options]` isolated non-interactive engine invocation (no tmux) · needs:seat:cc/cx/oc,network · today:U · cli.md:80 · lane(s):O
X16 · `pfm headless` `--schema`/`--json-schema` structured output · needs:seat:cc/cx/oc,network · today:U · cli.md:80 · lane(s):O
X17 · `pfm headless` `--tools`/`--setting-sources`/`--strict-mcp-config` controls · needs:seat:cc/cx/oc,network · today:U · cli.md:80 · lane(s):O
X18 · `pfm internal agent-open --id id --cwd path [--config path]` (picker's embedded per-window pane opener) — no dedicated file found ⚠ known-gap · needs:tmux · today:NONE · cli.md:204 · lane(s):F
X19 · `pfm internal chat-server <socket> <cwd> <run>` (shim's tmux-session creator) · needs:tmux · today:U · cli.md:205 · lane(s):F
X20 · `pfm internal claude-launch -- [claude args]` · needs:tmux · today:U · cli.md:206 · lane(s):E1
X21 · `pfm internal claude-version` · needs:none · today:U · cli.md:207 · lane(s):O
X22 · `pfm internal clear-kill < payload.json` (SessionEnd hook body) · needs:none · today:U · cli.md:208 · lane(s):O
X23 · `pfm internal codex-appendix` (stdin/stdout hook rewrite) — no test found by name ⚠ known-gap · needs:none · today:NONE · cli.md:209 · lane(s):E2
X24 · `pfm internal codex-launch BINARY [args...]` (process-replacing exec) · needs:tmux,seat:cx · today:U · cli.md:210 · lane(s):E2
X25 · `pfm internal compact-nudge` (UserPromptSubmit hook body) · needs:none · today:U · cli.md:211 · lane(s):E1
X26 · `pfm internal epic-inject` (UserPromptSubmit hook body, epic manifest via window name) · needs:tmux · today:U · cli.md:212 · lane(s):O
X27 · `pfm internal exit-close` (SessionEnd hook body, skips mid-reload) · needs:tmux · today:U · cli.md:213 · lane(s):E1
X28 · `pfm internal exit-intercept` (prompt hook body, `e`/`/e` → `kill --self --exit`) · needs:tmux,seat:cc · today:U · cli.md:214 · lane(s):E1
X29 · `pfm internal explore-deny` (PreToolUse hook body, denies non-haiku Explore subagent) · needs:none · today:U · cli.md:215 · lane(s):O
X30 · `pfm internal kill-exit --engine --id --path --socket --socket-name --pane` (tmux pane-death finisher) · needs:tmux · today:U · cli.md:216 · lane(s):O
X31 · `pfm internal launch --real PATH [--cwd DIR] -- [args]` (managed Claude launcher entry, process-replacing) · needs:tmux · today:U · cli.md:217 · lane(s):E1
X32 · `pfm internal launcher-repair` — no test found by name ⚠ known-gap · needs:seat:cc · today:NONE · cli.md:218 · lane(s):O
X33 · `pfm internal primary-get` — no test found by name ⚠ known-gap · needs:none · today:NONE · cli.md:219 · lane(s):O
X34 · `pfm internal primary-set <account>` — no test found by name ⚠ known-gap · needs:none · today:NONE · cli.md:220 · lane(s):O
X35 · `pfm internal reload-intercept` (prompt hook body, `/reload ...` → injected `chat reload`) · needs:tmux,seat:cc · today:U · cli.md:221 · lane(s):E1
X36 · `pfm internal reload-run [reload flags]` (detached worker performing the respawn) · needs:tmux,seat:cc · today:U · cli.md:222 · lane(s):E1
X37 · `pfm internal stale [--sweep] [--binary PATH]` (lists/TERM-KILLs stale-binary processes) · needs:none · today:U · cli.md:223 · lane(s):O
X38 · `pfm internal statusline` (alias of `pfm statusline`) · needs:none · today:U · cli.md:224 · lane(s):E1
X39 · `pfm internal then --socket --target [--self] --steer text...` (detached `--then` follow-up waiter) · needs:tmux · today:U · cli.md:225 · lane(s):E1
X40 · `pfm internal tmux-title-renudge` (OSC title repaint sweep) · needs:tmux · today:U · cli.md:226 · lane(s):O
X41 · `pfm internal update-check --cache PATH --current vX.Y.Z --url URL` (picker's cached release-notice refresh) — only its wiring is tested, the `internal/updatecheck` package itself not opened ⚠ known-gap · needs:network · today:NONE · cli.md:227 · lane(s):A

---

## Unclassified

None — every row read across the five inventories converted into an atomic item under I/P/C/K/T/M/L/H/X above. No inventory row was dropped.

---

## Counts

| Area | Items | today=NONE |
|---|---|---|
| I — Install/host wiring | 98 | 12 |
| P — Project scaffold/update | 37 | 5 |
| C — Chat verbs | 66 | 3 |
| K — Chat kinds/labels/engines | 40 | 1 |
| T — TUI | 38 | 1 |
| M — MCP | 55 | 11 |
| L — Lifecycle mechanics | 42 | 0 |
| H — Harvester | 12 | 1 |
| X — Misc CLI | 41 | 6 |
| **Total** | **429** | **40** |

`today=NONE` breakdown by id: I4,I5,I9,I10,I13,I15,I17,I18,I40,I89,I90,I92 (12) · P12,P13,P14,P15,P37 (5) · C31,C40,C41 (3) · K19 (1) · T37 (1) · M15,M16,M17,M18,M21,M22,M25,M26,M28,M29,M48 (11) · H12 (1) · X18,X23,X32,X33,X34,X41 (6).

---

## Inventory gaps

Every UNKNOWN the five inventories declared, verbatim ids/lines (not the "no dedicated test found"
residual-uncertainty notes already folded into `today=NONE` items above — those are named per-item,
not re-listed here).

### `cli.md`

- Coverage §, line 235: "UNKNOWN cells: **none marked**" — cli.md declares zero literal UNKNOWN
  cells in its own tables; the "no test file found" residuals it does name (line 236) are folded
  into items C31, C40, C41, C45(note), X18, X23, X32, X33, X34, X41 above, not re-listed as UNKNOWN.

### `mcp.md`

- Coverage §, line 105: "UNKNOWN cells: none marked `UNKNOWN` outright. One soft caveat:
  `chat_find`'s and `chat_read`'s 'needs' are inferred from `internal/chat/find.go`/`read.go`
  package headers and imports ... rather than a full read of their bodies — treat as
  high-confidence, not verbatim-quoted like the rest." (mcp.md:105, informs M10/M11's `needs` cell.)

### `install.md`

- install.md:27 — "UNKNOWN — no dedicated doctor row found for skill-link health" (`handoff.skill.md`).
- install.md:28 — "UNKNOWN — no dedicated launch-agent doctor row found (host under test is Linux;
  could not verify a macOS doctor path)" (name-sync launchd plist).
- install.md:32 — "UNKNOWN — no dedicated doctor row for this file's presence (selection is by
  config, not by wiring state)" (`professor-prompt.md`).
- install.md:33 — "UNKNOWN — not part of `ReportHooks`/`ReportGlobalAgents`; no dedicated
  command-link doctor row found" (`reload.command.md`).
- install.md:36 — "UNKNOWN — no dedicated systemd-unit doctor row found; `printTmuxTitlesDoctor`
  is INFO-only and doesn't probe unit health" (`pfm-mcp.service`, `pfm-name-sync.{path,service,timer}`).
- install.md:38 — "UNKNOWN — no `printThemeDoctor`/theme row found inside `doctor.go` Run's
  enumeration" (Claude Code themes).
- install.md:40 — "UNKNOWN — no dedicated doctor row for command-link health" (global commands).
- install.md:41 — "UNKNOWN — no dedicated doctor row for skill-link health" (global skills).
- install.md:384-390 (Coverage § UNKNOWN list, items 1-3): doctor probes for `handoff.skill.md`
  link health; the macOS launch-agent plist state; `professor-prompt.md` presence — restated
  verbatim as the file's own closing UNKNOWN roll-up.
- install.md:391 — "Doctor probe for `reload.command.md` link health (Claude or Codex side)."
- install.md:392-394 — "Doctor probe for Linux systemd unit health (`pfm-mcp.service`,
  `pfm-name-sync.{path,service,timer}`) beyond the MCP-daemon-reachability check and the
  info-only tmux-titles row."
- install.md:395-397 — "Doctor probe for global **commands**/**skills** link health specifically
  (only global **agents** has a named `ReportGlobalAgents`; no `ReportGlobalCommands`/
  `ReportGlobalSkills` equivalent was found)."
- install.md:398-399 — "Doctor probe for theme installation (`theme-ownership.json`) — no
  `printThemeDoctor`-shaped row found in `doctor.go`'s `Run`."
- install.md:400-402 — "Whether `pfmPathWarnings` (`doctor.go:1581`) reads `binary-ownership.json`
  directly, or only derives its PATH/hash checks independently — not confirmed by line-level reading."
- install.md:403-406 — "Whether `pfm uninstall` or any other command prunes old timestamped
  backups (`.bak-*`) — no retention routine was found, but the search ... was targeted rather than
  an exhaustive whole-file read of every backup call site."

### `fleet.md`

- fleet.md:223 — `open` verb: "per `pfmchat.OpenID` (UNKNOWN exact codes beyond target-not-found → 4)".
- fleet.md:227 — `stream` verb: "shell-only (no MCP tool); UNKNOWN dedicated test file (not found
  by name search within budget)".
- fleet.md:231 — `watch` verb: "shell-only; UNKNOWN dedicated fake/test file".
- fleet.md:232 — `capture` verb: "UNKNOWN dedicated test file within budget".
- fleet.md:237 — `unkill` verb: "covered via satellite/lineage jail tests; UNKNOWN a verb-specific file".
- fleet.md:239 — `reload` verb: "per `reload` package (multiple; UNKNOWN full enumeration beyond
  usage/flag errors=2)".
- fleet.md:284 — Chats tab data source: "`Snapshot.Rows` ([]compose.Row, produced by
  `fleet.Scan`/`compose.Compose` upstream of the TUI — UNKNOWN exact call site within this pass's
  budget)".
- fleet.md:287-288 — Stats tab: "Reads via `StatsSampler.Sample`/`SampleResources` ... `internal/
  stats`, contents UNKNOWN — package not opened this pass beyond its `Snapshot` type reference".
- fleet.md:304-305 — Cosmos tab: "rendering/graph-build logic lives in `internal/ui/cosmos.go`
  (1593 lines) and `internal/compose/cosmos.go` ... — **not read in this pass**; UNKNOWN beyond
  the call graph traced through `internal/ui/chronoscope.go`".
- fleet.md:343-345 — golden testdata split: "23 files at `pfm/testdata/golden/`, shared with
  `cmd/pfm`'s K1 shell-emission goldens — UNKNOWN exact split".
- fleet.md:355-356 — `pfm internal statusline` wiring: "(`statusline_command.go`, UNKNOWN exact
  wiring beyond `main.go:138-139`)".
- fleet.md § Coverage, lines 578-614 (full "UNKNOWN (named gaps)" roll-up):
  - `internal/compose/{compose,cosmos,order}.go` (2092 lines): row composition and cosmos graph
    construction NOT read — "the single largest gap" (fleet.md:580-584).
  - `internal/ui/cosmos.go` (1593 lines) and `internal/ui/render.go` (1190 lines): only grepped
    for keybinding/group-panel anchors; full visual rendering UNKNOWN in detail (fleet.md:585-588).
  - `internal/stats` package contents UNKNOWN (fleet.md:589-592).
  - `internal/gather` package (`TmuxProbe`, `WindowNameFor`, `ProcFS`, `RenameWindow`) UNKNOWN
    beyond call-site signatures (fleet.md:593-594).
  - `internal/index`, `internal/store`, `internal/transcript`, `internal/action`, `internal/
    resolve`, `internal/recovery`, `internal/nudge`, `internal/usagehook` — NOT opened
    (fleet.md:595-598).
  - `internal/fleet/{codexpanes,reconcile}.go` (Codex pane reconcile mechanics) — NOT read
    (fleet.md:599-600).
  - Exact exit-code enumeration for `open`, `stream`, `watch`, `capture`, `reload` beyond
    "usage=2 / dead=3 / unknown=4" UNKNOWN (fleet.md:601-604).
  - Dedicated unit-level test files for `last`, `stream`, `watch`, `capture` verbs "not located
    within the tool-call budget ... reported as UNKNOWN rather than asserted 'NONE'"
    (fleet.md:605-609).
  - `pfm internal statusline` CLI wiring file listed but not opened (fleet.md:610-611).
  - `internal/ui` testdata/golden file-by-file breakdown "not disambiguated" (fleet.md:612-614).

### `tests.md`

- tests.md:417 — "**Coverage matrix UNKNOWN cells: 0.** Every cell was resolvable from the
  repository as checked out (either a concrete finding or a confirmed NONE); none required
  running code or guessing." tests.md declares **zero** UNKNOWN cells; its separate "Explicit
  gaps found (not UNKNOWNs — determined, not absent-because-unlooked)" list (tests.md:421-437,
  5 items) are determined facts, already folded into T30, C45, H12, and the `pfm/testdata/e2e.sh`
  observation — not re-listed here since the file itself explicitly distinguishes them from UNKNOWN.
