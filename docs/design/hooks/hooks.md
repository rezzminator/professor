# hooks

Professor reaches a chat through hooks at two tiers. pfm's twelve machine-global Claude hooks — eighteen registrations, since `callmeter` is one hook registered on seven events — ride every Claude launch: `claudelaunch.Render` renders them into the `hooks` key of the single `--settings` JSON the chat starts with. `pfm init` scaffolds a project's `.claude/settings.json` from the template, with six hooks over the project's own scripts. Claude merges hooks across its settings layers, so the project's hooks run beside the launch's. Codex and OpenCode carry no pfm hook. This file holds the inventory, the ownership rule, and the design of the `pfm doctor` checks that prove each pfm hook is in place. The tool-call recorder itself is designed separately in [callmeter.md](callmeter.md); here it is only the last row of the inventory. The Bash guard over shared git state is designed in [git-guard.md](git-guard.md).

A change lands in this design doc first, then in the code or template, then in every surface listed under [Surfaces that stay in sync](#surfaces-that-stay-in-sync).

Every claim cites the file that proves it. `{claude config dir}` is one account's `configDir` from the pfm machine config; `{codex home}` is one Codex account's home.

## Contents

- [Decisions](#decisions)
- [Count per engine](#count-per-engine)
- [Launch-time Claude hooks (pfm-owned)](#launch-time-claude-hooks-pfm-owned)
- [Project-tier Claude hooks (template)](#project-tier-claude-hooks-template)
- [Codex](#codex)
- [OpenCode](#opencode)
- [Agent-attached hooks (not machine-global)](#agent-attached-hooks-not-machine-global)
- [Retired hooks](#retired-hooks)
- [The ownership rule](#the-ownership-rule)
- [The pfm doctor checks](#the-pfm-doctor-checks)
- [Discrepancies](#discrepancies)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## Decisions

- **One list is the truth for pfm's hooks.** `claudelaunch.HookTemplates` (`pfm/internal/claudelaunch/hooks.go`) names all twelve hooks as eighteen registrations — eleven single-placement hooks plus `callmeter`'s seven. `claudelaunch.Render` renders it into every launch's `--settings` payload, and spawn-audit compares each live chat's decoded payload against it. A new pfm hook is a new row there, never a second list.
- **Hooks ride the launch, never a file.** `pfm install` writes no key into any account `settings.json`: no `hooks`, no `statusLine`, no `subagentStatusLine`. A chat carries the hook set it was launched with; a changed set reaches a running chat at its next reload (`pfm chat reload`) or relaunch.
- **pfm never writes a project's `.claude/settings.json`.** The project tier is scaffolded once by `pfm init` (`pfm/internal/professor/scaffold.go:29`) and is the adopter's file from then on. Its hooks merge with the launch's; neither layer replaces the other.
- **Every pfm hook command is the installed binary.** Each command is `$HOME/.local/bin/pfm` plus a subcommand (`pfm/internal/claudelaunch/hooks.go`). No pfm hook runs a shell script.
- **An ownership ledger records what pfm wrote into a file.** It lives at `$HOME/.local/share/pfm/install/settings-hook-ownership.json` (`pfm/internal/installer/update_metadata.go:42-43`, `pfm/internal/installer/settings_ownership.go:13`), keyed by physical file, event, matcher and command (`pfm/internal/installer/settings_ownership.go:21-27`). Two readers use it: the Codex `hooks.json` writer, and the host-migration reconciler (`HostLayout`, verdict `strip`), which removes from each account `settings.json` the pfm hooks, `statusLine` and `subagentStatusLine` an older install left there — the ledger's entries plus every hook pfm owns by command shape ([The ownership rule](#the-ownership-rule)) — and keeps every other key.
- **A retired hook is removed by the installer and reported by doctor.** One table names the retired subcommands (`pfm/internal/installer/settings.go:319-343`); the Codex writer strips them from every `hooks.json` event, and doctor flags any left behind as STALE.
- **pfm hooks fail open.** A pfm hook that cannot do its job writes one stderr line and lets the chat continue. Only the two intercepts exit 2, and only for the prompt they were built to stop; `git-guard` alone also denies a git command it cannot read ([git-guard.md](git-guard.md#how-it-fails)). `callmeter` is fail-open the same way: it always exits 0 and logs and counts a fault rather than blocking a call.

## Count per engine

| Engine | Tier | Hooks | Installed by |
| --- | --- | --- | --- |
| Claude | every interactive launch, `--settings` `hooks` | 12 hooks, 18 registrations (pfm-owned; `callmeter` alone is 7) | `claudelaunch.Render` |
| Claude | project `.claude/settings.json` | 6 in the template | `pfm init`, then the adopter |
| Claude | operator's own, documented | 2 (memory backup, opt-in) | the adopter, by hand |
| Codex | `{codex home}/hooks.json` | 0 owned; 2 retired shapes removed | `pfm install` (removal only) |
| OpenCode | none | 0 | none |

## Launch-time Claude hooks (pfm-owned)

`claudelaunch.Render` (`pfm/internal/claudelaunch/render.go`) turns `claudelaunch.HookTemplates` into the `hooks` object of the launch `--settings` JSON: one entry per (event, matcher) pair, each registration once, `async: true` on every `callmeter` entry. Every interactive door renders it — `pfm chat new`, the picker and `pfm chat open`, `pfm chat branch`, `pfm chat reload`, `pfm internal agent-open`, a `claude` typed at a shell through the managed launcher ([claude-launch.md](../engines/claude-launch.md#doors)). Three runs carry no pfm hook: a launcher passthrough (`-p`, `--version`, the Claude subcommands, `PFM_LAUNCH_PASSTHROUGH=1`), which execs the real binary untouched; the `claude agents --json` query; and `pfm headless exec`, which is config-free by design. Each command below is `$HOME/.local/bin/pfm …`.

Placement holds by construction: the renderer emits each registration once under its own pair, so a launch cannot carry a duplicate, a moved or a mis-typed pfm hook. The only place such a copy can still sit is an account `settings.json` an older install wrote, which the [legacy row](#account-files-the-legacy-row) names and `pfm install` strips.

| Name | Event | Matcher | Command | Defined | Body | Does | On failure |
| --- | --- | --- | --- | --- | --- | --- | --- |
| launcher-repair | `SessionStart` | `""` | `pfm internal launcher-repair` | `hooks.go` | `pfm/internal/hookentry/launcher_repair.go:12-25` | Repairs the Claude launcher each session start | Exits 1 with one stderr line; the session continues |
| usage | `UserPromptSubmit` | `""` | `pfm usage-hook` | `hooks.go` | `pfm/cmd/pfm/statusline_command.go:136-175` | Checks the account's usage through `usagehook.Fetch`, the one door to the usage endpoint (order below), and prints a warning into the prompt when one is due; a no-op under Codex (`statusline_command.go:152-153`) | Fail-open, exits 0 (`statusline_command.go:166-168`); a failed refresh is written to stderr, the hook's log |
| clear-kill | `SessionEnd` | `""` | `pfm internal clear-kill` | `hooks.go` | `pfm/internal/hookentry/clear_kill.go:15` | Handles a `/clear` for the fleet session record | Fail-open, stderr line per cause (`clear_kill.go:27-66`) |
| exit-close | `SessionEnd` | `""` | `pfm internal exit-close` | `hooks.go` | `pfm/internal/hookentry/exit_close.go:23` | Closes the terminal a chat was watched through after a human `/exit` | Fail-open, the terminal is left open (`exit_close.go:30-73`) |
| explore-deny | `PreToolUse` | `Agent\|Task` | `pfm internal explore-deny` | `hooks.go` | `pfm/internal/hookentry/explore_deny.go:18` | Denies an `Explore` spawn and names `tracer` instead (`explore_deny.go:16`) | Fail-open on an unreadable payload (`explore_deny.go:22-30`) |
| git-guard | `PreToolUse` | `Bash` | `pfm internal git-guard` | `hooks.go` | `pfm/internal/hookentry/git_guard.go:113` | Denies a shared git write (worktrees, history, branches, tags, remotes, the index, whole-tree destruction, repository settings) to every agent but `gitter`, per [git-guard.md](git-guard.md) | Fail-open on an unreadable payload or a parser error (`git_guard.go:114-142`); denies a git command one of whose parts does not parse |
| rr-dir | `SubagentStart` | `rr\|super-rr\|heavy-rr` | `pfm internal rr-dir` | `hooks.go` | `pfm/internal/hookentry/rr_dir.go:27-31` | Hands the rr agents the directory their answer is saved into | Fail-open, exits 0 (`rr_dir.go:45-47`) |
| epic-inject | `UserPromptSubmit` | `""` | `pfm internal epic-inject` | `hooks.go` | `pfm/internal/hookentry/epic_inject.go:44` | Injects an epic manifest once per session and epic name | Fail-open (`epic_inject.go:105`) |
| reload-intercept | `UserPromptSubmit` | `""` | `pfm internal reload-intercept` | `hooks.go` | `pfm/internal/hookentry/reload_intercept.go:17` | Turns a `/reload` prompt into a scheduled reboot and blocks the prompt | Exits 2 with the reason when the reload cannot be scheduled (`reload_intercept.go:38-49`) |
| exit-intercept | `UserPromptSubmit` | `""` | `pfm internal exit-intercept` | `hooks.go` | `pfm/internal/hookentry/exit_intercept.go:18` | Turns an exact `e` or `/e` prompt into a kill of this chat | Exits 2 when the kill fails (`exit_intercept.go:40`) |
| compact-nudge | `UserPromptSubmit` | `""` | `pfm internal compact-nudge` | `hooks.go` | `pfm/internal/hookentry/compact_nudge.go:22` | Reminds the main chat to compact as context fills; Claude only | Exits 0 on every skip, 1 on one write failure (`compact_nudge.go:36-87`) |
| callmeter | `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PostToolBatch`, `SubagentStart`, `SubagentStop`, `Stop` (one command, seven registrations) | `Bash` on `PreToolUse`; `*` on the other tool events and the subagent events; none on `PostToolBatch` and `Stop` | `pfm internal callmeter`, async | `hooks.go` | `pfm/internal/hookentry/callmeter.go` | Records calls, requests, agents and faults, per [callmeter.md](callmeter.md) | Always exits 0; logs and counts a fault |

The usage hook and the `pfm ls` Limits tab reach `GET https://api.anthropic.com/api/oauth/usage` only through `usagehook.Fetch` (`pfm/internal/usagehook/fetch.go`), which answers in this order:

1. A `cc-rate-limits` statusline snapshot of the seat (`ReadStatuslineSnapshot`): the same config dir, younger than the caller's TTL, its five-hour reset still ahead. No request.
2. The shared cache record `acct-<id>.json` fresh within the caller's TTL: the hook's 180 seconds (`CC_USAGE_TTL`), the Limits tab's 60 (`LiveLimitsTTL`).
3. An active shared backoff: the cached usage with the backoff's error. No request.
4. The `O_EXCL` refresh lock `acct-<id>.lock` beside the cache (`RefreshLockPath`). A lock older than 30 seconds (`lockStaleAfter`) is stale and taken over; a caller that cannot take the lock sends no request and answers from the cache; the holder re-reads the cache and answers if a peer refreshed or backed off meanwhile, and releases the lock on every path.
5. The request, sent with `User-Agent: pfm/{version}`. Success writes the record. A 429 writes the shared backoff (`BackoffFor`): `Retry-After` honored with a ten-minute floor, message `rate-limited — retry HH:MM (429 Too Many Requests)`. Any other failure backs off one minute.

The hook writes a failed refresh to stderr; its prompt output does not change. The statusline writes the snapshots from its stdin `rate_limits` (`harvestRateLimits`, `pfm/internal/statusline/render.go`): every window the harness sent, 0% included, under `windows`, beside the flat keys an older build's reader expects.

A blocked prompt returns `decision: block` with the original prompt suppressed (`pfm/internal/hookentry/prompt_block.go:23-28`).

## Project-tier Claude hooks (template)

Source `templates/project/settings.json`, scaffolded to `.claude/settings.json` with the scripts in `templates/project/scripts/` copied to `.claude/scripts/` (`pfm/internal/professor/scaffold.go:29`, `pfm/internal/professor/scaffold.go:33`). pfm never rewrites them after init. Every command is `$CLAUDE_PROJECT_DIR/.claude/scripts/…`. Claude runs them beside the launch's hooks, since hooks merge across settings layers.

| Script | Event | Matcher | Line | Does | On failure |
| --- | --- | --- | --- | --- | --- |
| `pfm-guard.sh` | `PreToolUse` | `Edit\|Write` | `templates/project/settings.json:26` | Denies an edit to `.claude/**` or any `CLAUDE.md` unless this session's `/pcm` and quality markers are fresh (`templates/project/scripts/pfm-guard.sh:4-15`) | Blocks: deny JSON and exit 2 (`pfm-guard.sh:78-79`); the quality marker is also read from the session's own project when the edit targets another repo (`pfm-guard.sh:40-52`); exits 0 when the path is out of scope |
| `guard-stamp.sh` | `PostToolUse` | `Read` | `templates/project/settings.json:37` | Stamps the quality marker when `quality/prompt.md` is read (`templates/project/scripts/guard-stamp.sh:4-7`) | Silent |
| `format-md.sh` | `PostToolUse` | `Edit\|Write` | `templates/project/settings.json:46` | Formats the Professor-owned `.md` just written under `.rumdl.toml` (`templates/project/scripts/format-md.sh:4-6`) | Warns: one stderr line per cause, always exits 0 (`format-md.sh:8-11`) |
| `codex-sync.sh mark` | `PostToolUse` | `Edit\|Write` | `templates/project/settings.json:50` | Marks the Codex and OpenCode mirrors dirty after a Claude source edit (`templates/project/scripts/codex-sync.sh:5-8`) | Silent |
| `guard-stamp.sh stop` | `Stop` | `""` | `templates/project/settings.json:61` | Reaps abandoned guard markers (`templates/project/scripts/guard-stamp.sh:8-12`) | Silent |
| `codex-sync.sh sync` | `Stop` | `""`, timeout 60 | `templates/project/settings.json:65` | Builds and checks both mirrors when dirty (`templates/project/scripts/codex-sync.sh:9-10`) | Blocks the stop once with the reason, then lets it end with a warning (`codex-sync.sh:10-16`) |

This repo's own `.claude/settings.json` carries all six: `pfm-guard.sh` (`.claude/settings.json:28`), `guard-stamp.sh` (`:39`), `format-md.sh` (`:48`), `codex-sync.sh mark` (`:52`), `guard-stamp.sh stop` (`:63`) and `codex-sync.sh sync` (`:67`). A long-turn notification is the operator's own hook, never a template one.

The memory-backup hooks are documented for the adopter to install by hand into the account `settings.json`: `memory-wire.sh` on `SessionStart` and `memory-sync.sh` on `SessionEnd` (`docs/references/memory-backup.md:60-63`, `docs/SETUP.md:429-463`). They are the operator's own once installed, and pfm writes no key of that file.

## Codex

pfm owns no Codex hook: the fleet prompt reaches Codex through `config.toml`'s `developer_instructions` (`pfm/internal/installer/expected_hooks.go`). `pfm install` still reads and rewrites `{codex home}/hooks.json` for every Codex account (`pfm/internal/installer/installer.go:2021-2055`), and only to remove pfm's retired hooks while keeping the operator's (`pfm/internal/installer/codex_hooks.go:16-18`), recording what it owns in the ownership ledger. An unparsable file is skipped loudly unless owned hooks would be stranded (`pfm/internal/installer/installer.go:2060-2071`).

## OpenCode

pfm ships no OpenCode hook and no plugin. OpenCode has no appendix hook; the staged prompt reaches it through the `instructions` config array (`pfm/internal/installer/opencode_instructions.go:12-17`). The guard that a Claude hook gives is a permission deny in `.opencode/opencode.jsonc` instead (`.claude/codex-build.json:8`).

## Agent-attached hooks (not machine-global)

A hook can also be wired only into one agent's own frontmatter instead of every launch's payload — it is never in `claudelaunch.HookTemplates`, never in spawn-audit's expected set, and pfm install/doctor never mention it. `orchestrator-wait` is the first: a `PreToolUse` hook on `Bash` that denies a call whose whole command only waits (`echo`/`printf` with plain literal arguments, `true`, `:`, or `sleep N` — matched as a whole string; `pfm/internal/hookentry/orchestrator_wait.go:52-70`), so a wait-looping orchestrator ends its message instead of spending a call proving nothing changed. It is fail-open the same way every pfm hook is: a read or decode failure logs to stderr and returns 0 (`orchestrator_wait.go:31-43`). It is registered as an internal verb exactly like `explore-deny` (`pfm/cmd/pfm/main.go`'s `internalSubcommands` list and its `runInternal` dispatch) so `pfm internal orchestrator-wait` runs and the subcommand is never mistaken for unknown residue, but it is deliberately absent from `claudelaunch.HookTemplates` — it is attached only through the frontmatter of `flights-orchestrator` and `general-orchestrator`, the two agents that spend calls waiting on a spawned agent, and never runs for any other agent. The frontmatter names bare `pfm`, never a machine path, since the agents ship; the Codex compile drops an agent's hooks, so the guard is Claude-only.

## Retired hooks

| Name | Where | Shape | Source |
| --- | --- | --- | --- |
| bb | Claude, Codex | `pfm bb`, `pfm chat bb`, `bb-hook.sh` | `pfm/internal/installer/settings.go:323-324`, `:339` |
| clear-hide | Claude, Codex | `pfm internal clear-hide` | `pfm/internal/installer/settings.go:325` |
| dream-agent-inject | Claude, Codex | `pfm dream hook agent-inject`, `dreamer-agent-inject.sh` | `pfm/internal/installer/settings.go:326`, `:340` |
| dream-nudge | Claude, Codex | `pfm dream hook nudge`, `dreamer-nudge.sh` | `pfm/internal/installer/settings.go:327`, `:341` |
| dream-codex-subagent-inject | Codex | `pfm dream hook codex-subagent-inject` | `pfm/internal/installer/settings.go:328` |
| group | Claude, Codex | `pfm chat group hook` | `pfm/internal/installer/settings.go:329` |
| Codex clear-kill | Codex `SessionStart`, matcher `startup\|resume\|clear` | `pfm internal clear-kill` with or without `--parent` | `pfm/internal/installer/codex_hooks.go:11-14`, `:34-39` |
| Codex appendix | Codex `SessionStart` | `codexappendix.Command(home)` | `pfm/internal/installer/codex_hooks.go:104-105` |
| unknown pfm subcommand | Claude, Codex | pfm's own shape naming a subcommand this binary does not implement | `unknownPFMHookCommand`, `pfm/internal/installer/settings.go:437-450` |

Each name matches from the `pfm` or `cc-fleet` binary, at any path (`retiredHookCommandName`, `pfm/internal/installer/settings.go:346-352`). In a Codex `hooks.json` the installer removes them and doctor reports them STALE. In a Claude account `settings.json` they exist only where an older install wrote them, and the `strip` verdict removes the ones the ownership ledger records.

## The ownership rule

- **pfm owns** exactly the hooks in `claudelaunch.HookTemplates`, which live only in the launch payload, and the retired shapes above. In a Codex `hooks.json` its record is the ownership ledger, and pfm removes exactly what the ledger records. In an account `settings.json` left by an older install, pfm owns what the ledger records plus every hook whose command is a `claudelaunch.HookTemplates` command or a retired shape — ownership by command shape, so an install older than the ledger is cleaned too.
- **The operator owns** every other hook in a settings file: a notification script, a memory sync, anything hand-wired. pfm never rewrites, reorders or removes one, and doctor never reports one. The Codex writer's contract says the same (`pfm/internal/installer/codex_hooks.go:16`).
- **An operator's hook that calls pfm stays the operator's.** A hook naming a subcommand this binary implements, such as `pfm doctor`, is never treated as residue (`pfm/internal/installer/settings.go:423-436`). Only an unknown subcommand in pfm's own shape is.
- **Project-tier hooks are the adopter's.** pfm scaffolds them once; later changes flow through `pfm doctor --project-updates` and an upstream diff the project ports by judgment, never a rewrite.

## The pfm doctor checks

Three checks prove pfm's hooks. The launch hooks are proven per live chat, because that is the only place they exist; account files are checked for leftovers that would double-fire beside the launch; Codex homes are checked for retired residue. Doctor checks only pfm's own hooks and prints nothing about the operator's — no count line either. The project tier has no doctor coverage.

### Launch hooks: spawn-audit

`pfm doctor` spawn-audit reads each live `cc-` chat's Claude argv from `/proc` and decodes it with `claudelaunch.Parse`, the inverse of `Render` ([claude-launch.md](../engines/claude-launch.md#pfm-doctor-spawn-audit)). A chat is `INJECTED` only when its `--settings` payload carries the registry's hook set: every `claudelaunch.HookTemplates` registration present exactly once, under its own event and matcher, with the exact command and `async: true` where the template has it.

| Verdict | Hook meaning | Reports |
| --- | --- | --- |
| `INJECTED` | the payload's `hooks` equals the registry's set | the chat's row, no finding |
| `VIOLATION` | a fresh launch with no registry payload — a spawn site bypassed `Render`, so the chat runs no pfm hook | failure, naming the chat and pid |
| `PREDATES-LAYER` | the payload's hook set differs from the current registry: the process started before it changed | warning, naming the chat; `pfm chat reload` carries the new set |

A probe that cannot read `/proc` or decode an argv reports `CHECK FAILED to run … — live chats unaudited`, never "no chats". Hook values are read from argv, never from `/proc/{pid}/environ`.

The same check stats the first word of every template command, following a symlink, and reports `DRIFT what=executable want=$HOME/.local/bin/pfm got={absent|not-executable|not-regular-file|stat-failed(…)} — run pfm install` (`executableVerdict`, `pfm/internal/installer/hook_probe.go:308`), since every rendered hook would fail to start.

### Account files: the legacy row

For every Claude config dir the machine config names, de-duplicated by physical path, doctor reads `{claude config dir}/settings.json` against the ownership ledger and pfm's command shapes with the same classifier the `strip` verdict uses (`ClassifyLayout`, [host-migration.md](../engines/host-migration.md#classification)).

| State | Reports | Tally |
| --- | --- | --- |
| clean | no pfm-owned `hooks`, `statusLine` or `subagentStatusLine`; the file is absent | nothing |
| leftover | `legacy: {file} still carries pfm hooks — run pfm install` (or `statusLine`, `subagentStatusLine`): each would double-fire beside the launch payload | failure |
| unreadable file | `legacy: {file} UNREADABLE error={cause}` — a read or parse failure, a dangling symlink, or a `hooks` value of the wrong shape; never judged clean | failure |
| unreadable ledger | `legacy: ownership ledger UNREADABLE error={cause} — ledger entries unjudged`; the command-shape rows still print, because they need no ledger | failure |
| no Claude account | `doctor: hook claude none — no Claude config dir is configured in the machine config` | warning |

### Codex hooks.json

The Codex probe (`probeCodexHooks`, `pfm/internal/installer/hook_probe.go:326-362`) opens every configured Codex home's `hooks.json`, drawn from the machine config's Codex accounts, named `codex[{id}]`, de-duplicated by physical path. Each row keeps the prefix `doctor: hook {target} {file} {event} {name}`. With no expected Codex hook there is nothing to be MISSING, so it prints only these states:

| State | When | Prints | Tally |
| --- | --- | --- | --- |
| STALE | A retired or unknown pfm hook is registered; the Codex-only retired shapes (the old `clear-kill` `SessionStart` hook and the retired appendix hook) are flagged only under `SessionStart` | `… STALE {name} — run pfm install` | failure |
| UNREADABLE | The file or the ownership ledger exists but cannot be read or parsed, is a dangling symlink, or its `hooks` value has the wrong shape (a non-object `hooks`, a non-array event, a non-object entry, a non-string `matcher`, a non-array `hooks` list, a non-object hook, a missing or empty `command`) | one line per file, no per-hook rows: `doctor: hook {target} {file} UNREADABLE error={cause}`; for the ledger, target `ownership` | failure |
| DRIFT (ledger) | The ledger owns an entry the file lacks, or one the installer no longer expects | `… DRIFT ledger ownership={n} file={m}`, `m` also `absent` or `not-expected` | warning |
| (unknown) | An internal state this printer does not recognize | `… UNKNOWN-STATE state=…` | failure |

UNREADABLE is an error, never absence: it carries no rows for that file's hooks, because nothing was proven. An unreadable ledger prints its own UNREADABLE row and every file's STALE and UNREADABLE rows still print, without the ledger comparison. OpenCode prints nothing.

### Exit

Each check returns its warnings and failures to the doctor tally (`pfm/internal/doctor/doctor.go:252-254`). `pfm doctor` exits 3 on any failure, 1 on warnings only, 0 when clean (`pfm/internal/doctor/doctor.go:398-410`). A deterministic stub stays available through `HookProbeOverride` (`pfm/internal/installer/hook_probe.go:490`).

### What the checks' own broken state reports

- Spawn-audit that cannot run: `CHECK FAILED to run … — live chats unaudited`, never "no chats".
- An unreadable ownership ledger: its own UNREADABLE row; ledger-recorded entries read as unjudged, never clean, while command-shape leftovers still report.
- An empty machine config (no accounts): one line saying no Claude config dir is configured, never a silent pass.
- A probe that cannot resolve `$HOME`: doctor stops before the checks.

## Discrepancies

1. **The Codex adapter says "Codex has NO hook layer"** (`.claude/codex-build.json:8`). Codex has one, and pfm reads and writes `{codex home}/hooks.json`. The conclusion (the guard is absolute in Codex) still holds, because pfm installs no Codex guard hook.
2. **`docs/BLUEPRINT.md` lists "statusline" among the hooks** in its tree diagram; it is a `statusLine` key in the launch payload, and the list omits the guard and the mirror sync.
3. **Twins differ.** `pfm-guard.sh`, `guard-stamp.sh` and `codex-sync.sh` differ between `.claude/scripts/` and `templates/project/scripts/`; `format-md.sh` is identical. The difference is not reviewed here.

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The hook list | `pfm/internal/claudelaunch/hooks.go` | `HookTemplates` |
| Installer adapter | `pfm/internal/installer/expected_hooks.go` | `claudeHookTemplates`, `ExpectedHooks` |
| The launch registry | `pfm/internal/claudelaunch/knobs.go`, `render.go` | the `hooks` knob, `Render` |
| Spawn-audit | `claudelaunch.Parse`, the doctor spawn-audit | the per-chat hook-set verdicts |
| The account-file reconciler | `pfm/internal/installer/layout.go` | `HostLayout`'s account `settings.json` row, `strip`, the `legacy` doctor row |
| The Codex probe and printer | `pfm/internal/installer/hook_probe.go` | `probeCodexHooks`, `ReportHooks`, `executableVerdict`, the states |
| The retired table | `pfm/internal/installer/settings.go` | the retired names, the unknown-subcommand rule |
| The Codex writer | `pfm/internal/installer/codex_hooks.go` | Codex retirement |
| The ledger | `pfm/internal/installer/settings_ownership.go` | Ownership keys and counts |
| The dispatch | `pfm/cmd/pfm/main.go:54-68` | Every subcommand a hook may name |
| The hook bodies | `pfm/internal/hookentry/`, `pfm/cmd/pfm/statusline_command.go` | What each command does |
| The doctor | `pfm/internal/doctor/doctor.go:252` | Where the checks run and their tally |
| The project template | `templates/project/settings.json`, `templates/project/scripts/`, `templates/refresh-map.json` | The project-tier hooks and their sources |
| The scaffold | `pfm/internal/professor/scaffold.go:29-33` | Where `pfm init` puts them |
| This repo's install | `.claude/settings.json`, `.claude/scripts/` | The project tier this repo runs |
| The engine adapters | `.claude/codex-build.json` | What Codex and OpenCode are told about hooks |
| The CLI surface | `docs/dev/pfm-surface.md` | The `doctor`, `install` and `internal` rows |
| The setup docs | `docs/SETUP.md`, `docs/BLUEPRINT.md`, `docs/references/memory-backup.md` | What an adopter is told to install |
| The recorder | `pfm/internal/hookentry/callmeter.go`, `pfm/internal/callmeter/`, [callmeter.md](callmeter.md) | The tool-call hook and its design |
