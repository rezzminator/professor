# hooks

Professor reaches a chat through hooks at two tiers. `pfm install` writes pfm's twelve machine-global Claude hooks — eighteen registrations, since `callmeter` is one hook registered on seven events — into the `settings.json` of every Claude config dir the machine config names; `pfm init` scaffolds a project's `.claude/settings.json` from the template, with six hooks over the project's own scripts. Codex and OpenCode carry no pfm hook. This file holds the inventory, the ownership rule, and the design of the `pfm doctor` check that proves each pfm hook is in place. The tool-call recorder itself is designed separately in [callmeter.md](callmeter.md); here it is only the last row of the inventory. The Bash guard over shared git state is designed under [git-guard](#git-guard).

A change lands in this design doc first, then in the code or template, then in every surface listed under [Surfaces that stay in sync](#surfaces-that-stay-in-sync).

Every claim cites the file that proves it. `{claude config dir}` is one account's `configDir` from the pfm machine config; `{codex home}` is one Codex account's home.

## Contents

- [Decisions](#decisions)
- [Count per engine](#count-per-engine)
- [Machine-global Claude hooks (pfm-owned)](#machine-global-claude-hooks-pfm-owned)
- [git-guard](#git-guard)
- [Project-tier Claude hooks (template)](#project-tier-claude-hooks-template)
- [Codex](#codex)
- [OpenCode](#opencode)
- [Agent-attached hooks (not machine-global)](#agent-attached-hooks-not-machine-global)
- [Retired hooks](#retired-hooks)
- [The ownership rule](#the-ownership-rule)
- [The pfm doctor check](#the-pfm-doctor-check)
- [This host](#this-host)
- [Discrepancies](#discrepancies)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)
- [Open items](#open-items)

## Decisions

- **One list is the truth for pfm's hooks.** `claudeHookTemplates` names all twelve hooks as eighteen registrations — eleven single-placement hooks plus `callmeter`'s seven (`pfm/internal/installer/expected_hooks.go:75-128`). The installer writes from it (`pfm/internal/installer/settings.go:27`) and doctor probes from it (`pfm/internal/installer/hook_probe.go:56-117`, over `pfm/internal/installer/expected_hooks.go:42-73`). A new pfm hook is a new row there, never a second list.
- **pfm writes only the account settings files.** It never writes a project's `.claude/settings.json` (`pfm/internal/installer/expected_hooks.go:39-41`). The project tier is scaffolded once by `pfm init` (`pfm/internal/professor/scaffold.go:29`) and is the adopter's file from then on.
- **Every pfm hook command is the installed binary.** Each command is `$HOME/.local/bin/pfm` plus a subcommand (`pfm/internal/installer/expected_hooks.go:76`). No pfm hook runs a shell script.
- **An ownership ledger records what pfm wrote.** It lives at `$HOME/.local/share/pfm/install/settings-hook-ownership.json` (`pfm/internal/installer/update_metadata.go:42-43`, `pfm/internal/installer/settings_ownership.go:13`), keyed by physical file, event, matcher and command (`pfm/internal/installer/settings_ownership.go:21-27`). Uninstall removes exactly what the ledger owns (`pfm/internal/installer/settings.go:34-36`).
- **A retired hook is removed by the installer and reported by doctor.** One table names the retired subcommands (`pfm/internal/installer/settings.go:319-343`); the installer strips them from every event (`pfm/internal/installer/settings.go:50`, function at `:528`), and doctor flags any left behind as stale (`pfm/internal/installer/hook_probe.go:367-395`).
- **pfm hooks fail open.** A pfm hook that cannot do its job writes one stderr line and lets the chat continue. Only the two intercepts exit 2, and only for the prompt they were built to stop; `git-guard` alone also denies a git command it cannot read ([git-guard](#git-guard)). `callmeter` is fail-open the same way: it always exits 0 and logs and counts a fault rather than blocking a call.

## Count per engine

| Engine | Tier | Hooks | Installed by |
| --- | --- | --- | --- |
| Claude | machine-global, per `{claude config dir}` | 12 hooks, 18 registrations (pfm-owned; `callmeter` alone is 7) | `pfm install` |
| Claude | project `.claude/settings.json` | 6 in the template | `pfm init`, then the adopter |
| Claude | operator's own, documented | 2 (memory backup, opt-in) | the adopter, by hand |
| Codex | `{codex home}/hooks.json` | 0 owned; 2 retired shapes removed | `pfm install` (removal only) |
| OpenCode | none | 0 | none |

## Machine-global Claude hooks (pfm-owned)

Installed into `{claude config dir}/settings.json` for every account in the machine config (`pfm/internal/installer/expected_hooks.go:47-53`), once per physical file (`pfm/internal/installer/expected_hooks.go:57-62`). Each command below is `$HOME/.local/bin/pfm …`. The installer appends a hook only when the same command under the same (event, matcher) pair is absent (`pfm/internal/installer/settings.go:132-136`).

| Name | Event | Matcher | Command | Defined | Body | Does | On failure |
| --- | --- | --- | --- | --- | --- | --- | --- |
| launcher-repair | `SessionStart` | `""` | `pfm internal launcher-repair` | `expected_hooks.go:78` | `pfm/internal/hookentry/launcher_repair.go:12-25` | Repairs the Claude launcher each session start | Exits 1 with one stderr line; the session continues |
| usage | `UserPromptSubmit` | `""` | `pfm usage-hook` | `expected_hooks.go:79` | `pfm/cmd/pfm/statusline_command.go:160-196` | Checks the account's usage and prints a warning into the prompt when one is due; a no-op under Codex (`statusline_command.go:176-179`) | Fail-open, exits 0 (`statusline_command.go:187-190`) |
| clear-kill | `SessionEnd` | `""` | `pfm internal clear-kill` | `expected_hooks.go:80` | `pfm/internal/hookentry/clear_kill.go:15` | Handles a `/clear` for the fleet session record | Fail-open, stderr line per cause (`clear_kill.go:27-66`) |
| exit-close | `SessionEnd` | `""` | `pfm internal exit-close` | `expected_hooks.go:81` | `pfm/internal/hookentry/exit_close.go:23` | Closes the terminal a chat was watched through after a human `/exit` | Fail-open, the terminal is left open (`exit_close.go:30-73`) |
| explore-deny | `PreToolUse` | `Agent\|Task` | `pfm internal explore-deny` | `expected_hooks.go:82-87` | `pfm/internal/hookentry/explore_deny.go:18` | Denies an `Explore` spawn and names `tracer` instead (`explore_deny.go:16`) | Fail-open on an unreadable payload (`explore_deny.go:22-30`) |
| git-guard | `PreToolUse` | `Bash` | `pfm internal git-guard` | `expected_hooks.go:88-95` | `pfm/internal/hookentry/git_guard.go:56` | Denies a shared git write (worktrees, history, branches, tags, remotes, the index, whole-tree destruction, repository settings) to every agent but `gitter`, per [git-guard](#git-guard) | Fail-open on an unreadable payload (`git_guard.go:57-69`); denies a git command it cannot parse |
| rr-dir | `SubagentStart` | `rr\|super-rr` | `pfm internal rr-dir` | `expected_hooks.go:96-101` | `pfm/internal/hookentry/rr_dir.go:27-31` | Hands the rr agents the directory their answer is saved into | Fail-open, exits 0 (`rr_dir.go:45-47`) |
| epic-inject | `UserPromptSubmit` | `""` | `pfm internal epic-inject` | `expected_hooks.go:102` | `pfm/internal/hookentry/epic_inject.go:44` | Injects an epic manifest once per session and epic name | Fail-open (`epic_inject.go:105`) |
| reload-intercept | `UserPromptSubmit` | `""` | `pfm internal reload-intercept` | `expected_hooks.go:103` | `pfm/internal/hookentry/reload_intercept.go:17` | Turns a `/reload` prompt into a scheduled reboot and blocks the prompt | Exits 2 with the reason when the reload cannot be scheduled (`reload_intercept.go:38-49`) |
| exit-intercept | `UserPromptSubmit` | `""` | `pfm internal exit-intercept` | `expected_hooks.go:104` | `pfm/internal/hookentry/exit_intercept.go:18` | Turns an exact `e` or `/e` prompt into a kill of this chat | Exits 2 when the kill fails (`exit_intercept.go:40`) |
| compact-nudge | `UserPromptSubmit` | `""` | `pfm internal compact-nudge` | `expected_hooks.go:105` | `pfm/internal/hookentry/compact_nudge.go:22` | Reminds the main chat to compact as context fills; Claude only | Exits 0 on every skip, 1 on one write failure (`compact_nudge.go:36-87`) |
| callmeter | `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PostToolBatch`, `SubagentStart`, `SubagentStop`, `Stop` (one command, seven registrations) | `Bash` on `PreToolUse`; `*` on the other tool events and the subagent events; none on `PostToolBatch` and `Stop` | `pfm internal callmeter`, async (`expected_hooks.go:106-127`) | `expected_hooks.go:99-119` | `pfm/internal/hookentry/callmeter.go` | Records calls, requests, agents and faults, per [callmeter.md](callmeter.md) | Always exits 0; logs and counts a fault |

A blocked prompt returns `decision: block` with the original prompt suppressed (`pfm/internal/hookentry/prompt_block.go:23-28`).

The installer also converges shape: it resets the `explore-deny` matcher to `Agent|Task` (`pfm/internal/installer/settings.go:109-125`), rewrites a legacy `explore-deny.sh` or `cc-fleet` command to the binary (`pfm/internal/installer/settings.go:39-48`), and holds one placement rule for every pfm-owned hook (`dropMisplacedTemplateHooks`, `pfm/internal/installer/settings_wiring.go:77-173`): a template command belongs exactly once under each (event, matcher) pair its templates name — a set per command, since `callmeter`'s one command names seven pairs. The installer removes a duplicate wherever it sits, keeping the first in sorted event order and then array order, even when the duplicate shares its entry with an operator hook. A copy under any other event or matcher is a moved hook and is removed too, unless it shares its entry with an operator hook, in which case the moved copy is left in place and the right copy is appended instead (`pfm/internal/installer/settings_wiring.go:60-75`). An entry emptied by a removal is dropped, and an event emptied by dropping its last entry is deleted. An entry whose `matcher` is not a string or whose `hooks` is not an array is never touched. Because this rule is per (event, matcher) pair rather than per command, a `usage`, `clear-kill`, `explore-deny`, `rr-dir` or any other single-placement hook found again under a non-empty matcher is now a moved hook, not a special case. `async: true` is converged separately, at the expected (event, matcher) pair only (`normalizeExpectedHookTypes`, `pfm/internal/installer/settings.go:634-664`), and is not part of the ownership ledger key.

With no Claude Code binary on the host, the installer wires no Claude hook and doctor collapses every `claude[N]` target's rows into one named skip line per account (`pfm/internal/installer/hook_probe.go:514-520`).

## git-guard

Only `gitter` writes shared git state. An agent that made its own worktree left it behind: nothing tore it down, and the stale worktrees and branches piled up in every repository. `git-guard` is the machine-global `PreToolUse` hook on `Bash` that makes the rule hold at the call instead of in the prompt: `pfm install` registers it in every account's `settings.json` (`pfm/internal/installer/expected_hooks.go:88-95`), and it runs in every repository, managed by pfm or not. Body: `pfm/internal/hookentry/git_guard.go`.

**Who is exempt.** A payload whose `agent_type` is `gitter` is always allowed — a project's own gitter (`.claude/agents/gitter.md`, scaffolded from `templates/project/agents/gitter.md`) or a machine-global one, since the hook reads only the name. Every other caller is checked, a main chat (no `agent_type`) included.

**How it reads a command.** A non-Bash tool, or a command containing neither `git` nor `worktree.sh`, returns at once. Otherwise the command goes through the callmeter shell parser (`pfm/internal/callmeter/cmdparse`), which unwraps wrappers (`timeout`, `env`), follows `&&`, `;`, pipes and subshells, and parses an inner `bash -c` string; a heredoc body and an `echo git …` argument are data, never a git call. For each part whose program is `git`, git's global options (`-C`, `-c`, `--git-dir`, `--work-tree`, `--no-pager` and the rest) are skipped, `-C` naming the repository; the subcommand and its arguments decide.

| Area | Blocked | Allowed |
| --- | --- | --- |
| worktrees | `worktree add`, `remove`, `prune`, `move`, `lock`, `unlock`, `repair`; `.claude/scripts/worktree.sh create`, `remove`, `prune` | `worktree list`; `worktree.sh list` |
| history | `commit`, `merge`, `rebase`, `cherry-pick`, `revert`, `am`, `pull`; every `reset` (a mode, a commit, paths, or bare) | `log`, `show`, `diff` and every other read |
| branches and tags | `switch`; `checkout -b`, `-B`, `--orphan`, `--track`, `--detach`, or one operand that is not an existing path (a branch switch); `branch` with a name or `-d`, `-D`, `-m`, `-M`, `-c`, `-f`, `-u`; `tag` with a name or `-d`, `-a`, `-s`, `-m`, `-f`; `update-ref`; `symbolic-ref` with two operands or `-d` | `branch` bare, `--list`, `-a`, `-r`, `-v`, `--show-current`, `--contains`, `--merged`, `--no-merged`; `tag` bare, `-l`, `--list`, `--contains`, `--points-at`; `symbolic-ref HEAD` |
| remotes | `push`, `fetch`; `remote add`, `remove`, `rename`, `set-url`, `set-head`, `set-branches`, `prune`, `update` | `remote`, `remote -v`, `ls-remote` |
| staging | `add`, `rm`, `mv`, `restore --staged` / `-S`, `apply --index` / `--cached` / `--3way`, `update-index`, `hash-object -w` | `add -n`, `rm -n` (`--dry-run`); `apply` to the working tree, `apply --check`; `hash-object` |
| whole-tree destruction | `stash` with no pathspec (bare, `push` or flags without paths, `save`), `stash drop`, `clear`, `store`, `branch`; `clean`; `checkout` or `restore` of `.`, `:/`, `*` or the repository root | `stash push -- <paths>`, `stash pop`, `apply`, `list`, `show`; `clean -n`; `checkout -- <file>`, `restore <file>` |
| repository settings | a `config` write (anything but `--get`, `--get-all`, `--get-regexp`, `--list`, `-l`, `--show-origin`, `get`, `list` or a single-key read); `gc`, `prune`, `filter-branch`, `filter-repo`, `replace`; `notes` except `show` and `list`; `submodule add`, `update`, `deinit`, `sync` | `config --get user.name`, `config user.name`; `notes show`; `submodule status` |

Anything not in the blocked column is allowed, `archive` included.

**The deny.** One message names every blocked part of the call as its words read, then `Only gitter writes this: spawn Agent(subagent_type: "gitter") with the repo path and the exact change.` A worktree block adds the right way for its repository: the `-C` directory, else the payload's `cwd`, walked up to the directory holding a `.git` entry without running git. When that repository has `.claude/scripts/worktree.sh`, the line reads `gitter creates and removes worktrees with {repo}/.claude/scripts/worktree.sh create|remove|prune — the only right way here.`; otherwise `gitter creates and removes worktrees (Phase SETUP).`

**Fail-open, and the one fail-closed case.** A payload that cannot be read or decoded writes one `pfm internal git-guard: … (fail-open)` stderr line and allows the call, like every pfm hook. A command that mentions `git ` and does not parse is the exception: it is denied with `could not read this command; split it so each git call is its own simple command.`, because allowing it would let any git write through by breaking the quoting.

**Named gap: Python snippets.** The parser hands `python -c` and heredoc Python to a runner the hook answers with no result, so the hook never spawns `python3`; a git call made from inside Python (`subprocess.run(["git", "push"])`) is not inspected. A `$VAR` or other word the parser cannot resolve is read as written, so `git $CMD` passes unless its literal words are a blocked form.

## Project-tier Claude hooks (template)

Source `templates/project/settings.json`, scaffolded to `.claude/settings.json` with the scripts in `templates/project/scripts/` copied to `.claude/scripts/` (`pfm/internal/professor/scaffold.go:29`, `pfm/internal/professor/scaffold.go:33`). pfm never rewrites them after init. Every command is `$CLAUDE_PROJECT_DIR/.claude/scripts/…`.

| Script | Event | Matcher | Line | Does | On failure |
| --- | --- | --- | --- | --- | --- |
| `pfm-guard.sh` | `PreToolUse` | `Edit\|Write` | `templates/project/settings.json:26` | Denies an edit to `.claude/**` or any `CLAUDE.md` unless this session's `/pcm` and quality markers are fresh (`templates/project/scripts/pfm-guard.sh:4-15`) | Blocks: deny JSON and exit 2 (`pfm-guard.sh:65-66`); exits 0 when the path is out of scope |
| `guard-stamp.sh` | `PostToolUse` | `Read` | `templates/project/settings.json:37` | Stamps the quality marker when `quality/prompt.md` is read (`templates/project/scripts/guard-stamp.sh:4-7`) | Silent |
| `format-md.sh` | `PostToolUse` | `Edit\|Write` | `templates/project/settings.json:46` | Formats the Professor-owned `.md` just written under `.rumdl.toml` (`templates/project/scripts/format-md.sh:4-6`) | Warns: one stderr line per cause, always exits 0 (`format-md.sh:8-11`) |
| `codex-sync.sh mark` | `PostToolUse` | `Edit\|Write` | `templates/project/settings.json:50` | Marks the Codex and OpenCode mirrors dirty after a Claude source edit (`templates/project/scripts/codex-sync.sh:5-8`) | Silent |
| `guard-stamp.sh stop` | `Stop` | `""` | `templates/project/settings.json:61` | Reaps abandoned guard markers (`templates/project/scripts/guard-stamp.sh:8-12`) | Silent |
| `codex-sync.sh sync` | `Stop` | `""`, timeout 60 | `templates/project/settings.json:65` | Builds and checks both mirrors when dirty (`templates/project/scripts/codex-sync.sh:9-10`) | Blocks the stop once with the reason, then lets it end with a warning (`codex-sync.sh:10-16`) |

This repo's own `.claude/settings.json` carries all six: `pfm-guard.sh` (`.claude/settings.json:28`), `guard-stamp.sh` (`:39`), `format-md.sh` (`:48`), `codex-sync.sh mark` (`:52`), `guard-stamp.sh stop` (`:63`) and `codex-sync.sh sync` (`:67`). A long-turn notification is the operator's own hook, never a template one.

The memory-backup hooks are documented for the adopter to install by hand into the global `settings.json`: `memory-wire.sh` on `SessionStart` and `memory-sync.sh` on `SessionEnd` (`docs/references/memory-backup.md:60-63`, `docs/SETUP.md:429-463`). They are the operator's own once installed. The installer only renames a helper whose content matches a known template fingerprint and rewrites the hook that names it (`pfm/internal/installer/memory_helpers.go:37-58`).

## Codex

pfm owns no Codex hook: the fleet prompt reaches Codex through `config.toml`'s `developer_instructions` (`pfm/internal/installer/expected_hooks.go:69-72`). `pfm install` still reads and rewrites `{codex home}/hooks.json` for every Codex account (`pfm/internal/installer/installer.go:2021-2055`), and only to remove pfm's retired hooks while keeping the operator's (`pfm/internal/installer/codex_hooks.go:16-18`). An unparsable file is skipped loudly unless owned hooks would be stranded (`pfm/internal/installer/installer.go:2060-2071`).

## OpenCode

pfm ships no OpenCode hook and no plugin. OpenCode has no appendix hook; the staged prompt reaches it through the `instructions` config array (`pfm/internal/installer/opencode_instructions.go:12-17`). The guard that a Claude hook gives is a permission deny in `.opencode/opencode.jsonc` instead (`.claude/codex-build.json:8`).

## Agent-attached hooks (not machine-global)

A hook can also be wired only into one agent's own frontmatter instead of every account's `settings.json` — it is never in `claudeHookTemplates`, never in the doctor's expected list, and pfm install/doctor never mention it. `orchestrator-wait` is the first: a `PreToolUse` hook on `Bash` that denies a call whose whole command only waits (`echo`/`printf` with plain literal arguments, `true`, `:`, or `sleep N` — matched as a whole string; `pfm/internal/hookentry/orchestrator_wait.go:56-73`), so a wait-looping orchestrator ends its message instead of spending a call proving nothing changed. It is fail-open the same way every pfm hook is: a read or decode failure logs to stderr and returns 0 (`orchestrator_wait.go:31-40`). It is registered as an internal verb exactly like `explore-deny` (`pfm/cmd/pfm/main.go`'s `internalSubcommands` list and its `runInternal` dispatch) so `pfm internal orchestrator-wait` runs and the subcommand is never mistaken for unknown residue, but it is deliberately absent from `expected_hooks.go` — it is attached only through the frontmatter of `flights-orchestrator` and `general-orchestrator`, the two agents that spend calls waiting on a spawned agent, and never runs for any other agent.

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

Each name matches from the `pfm` or `cc-fleet` binary, at any path (`retiredHookCommandName`, `pfm/internal/installer/settings.go:346-352`).

## The ownership rule

- **pfm owns** exactly the hooks in `claudeHookTemplates` and the retired shapes above. It may add, rewrite, deduplicate and remove them. Its record is the ownership ledger.
- **The operator owns** every other hook in a settings file: a notification script, a memory sync, anything hand-wired. pfm never rewrites, reorders or removes one, and doctor never reports one. The Codex writer's contract says the same (`pfm/internal/installer/codex_hooks.go:16`).
- **An operator's hook that calls pfm stays the operator's.** A hook naming a subcommand this binary implements, such as `pfm doctor`, is never treated as residue (`pfm/internal/installer/settings.go:423-436`). Only an unknown subcommand in pfm's own shape is.
- **Project-tier hooks are the adopter's.** pfm scaffolds them once; later changes flow through `pfm update check` and a hand-applied diff, never a rewrite.

## The pfm doctor check

The check lives in `hook_probe.go`: `ProbeExpectedHooks` (`pfm/internal/installer/hook_probe.go:60`) judges every configured file and returns one `HookProbeResult` per row; `ReportHooks` (`pfm/internal/installer/hook_probe.go:503`) prints each and tallies warnings and failures, called from `pfm doctor` (`pfm/internal/doctor/doctor.go:252`).

### What it proves per pfm-owned hook

1. Present in every Claude config dir the machine config names, once per physical file.
2. Under the right event and matcher; a copy found elsewhere is judged against the row whose pair is missing.
3. The command is the installer's exact string, and its first word resolves to an existing regular file with an execute bit, following a symlink (`pfm/internal/installer/hook_probe.go:285-317`).
4. Present exactly once.
5. Carries `async: true` when its template does (`pfm/internal/installer/hook_probe.go:223-224`).
6. No retired or unknown pfm hook remains in any probed Claude settings file or any configured Codex `hooks.json` — Codex homes are read too, not just Claude's.

### States

Each row keeps the prefix `doctor: hook {target} {file} {event} {name}` (`pfm/internal/installer/hook_probe.go:525`); the defined states are `ok`, `hook-drift`, `drift`, `stale`, `unreadable` and `no-claude-config` (`pfm/internal/installer/hook_probe.go:19-35`).

| State | When | Prints | Tally |
| --- | --- | --- | --- |
| OK | The hook is present once, event, matcher and command exact, `async` right, the executable resolves | `… ok` | none |
| MISSING | The settings file exists, parses and shape-checks, and no hook — typed or not, this event/matcher or another — carries the command; or the settings file does not exist | `… MISSING — run pfm install`; `… MISSING — run pfm install (settings file absent)` when the file itself is absent | failure |
| DRIFT (hook) | The command is present but wrong in one dimension: under another (event, matcher) pair (`what=event` or `what=matcher`), at another binary path (`what=binary`), not resolving as an executable (`what=executable`, `got` one of `absent`, `not-executable`, `not-regular-file`, `stat-failed(…)`), registered more than once (`what=count`), missing its expected `async: true` (`what=async`), or present but not of type `"command"` (`what=type`, `got=not-command`) | `… DRIFT {what} want={x} got={y} — run pfm install` | failure |
| DRIFT (ledger) | The file is right but the ownership ledger disagrees with it | `… DRIFT ledger ownership={n} file={m}`, where `m` can also be `absent` (the file has none where the ledger expects some) or `not-expected` (the ledger owns an entry the installer no longer expects) | warning |
| STALE | A retired or unknown pfm hook is registered in a probed Claude settings file or Codex `hooks.json`; for Codex, the Codex-only retired shapes (the old `clear-kill` `SessionStart` hook and the retired appendix hook) are flagged only under `SessionStart` | `… STALE {name} — run pfm install` | failure |
| UNREADABLE | The settings file, a Codex `hooks.json` or the ownership ledger exists but cannot be read or parsed, or its `hooks` value has the wrong shape | one line per file, no per-hook rows: `doctor: hook {target} {file} UNREADABLE error={cause}` | failure |
| no-claude-config | The machine config names no Claude account | `doctor: hook claude none — no Claude config dir is configured in the machine config` (or, with no Claude binary either, `doctor: hook claude skipped (no Claude config dir configured, no Claude Code binary installed)`, untallied) | warning (untallied when also Claude-absent) |
| (unknown) | An internal state this printer does not recognize | `… UNKNOWN-STATE state=…` | failure |

UNREADABLE is an error, never absence: it carries no MISSING rows for that file's hooks, because nothing was proven missing. A shape fault anywhere in a settings file's `hooks` value — a non-object `hooks`, a non-array event, a non-object entry, a `matcher` that is not a string, a non-array `hooks` list on an entry, a non-object hook, or a missing or empty `command` — makes the *whole file* UNREADABLE, reported as the first such fault in sorted event order (`pfm/internal/installer/hook_probe.go:415-484`). A non-async template hook carrying `async: true` anyway is not flagged; only a missing `async` on an `Async` template is DRIFT.

A moved hook is DRIFT, not MISSING: a stray copy of a multi-pair command (`callmeter`) sitting outside its own seven-pair set is reported on the row whose pair is actually missing, or — when nothing is missing — only on the command's first row (`pfm/internal/installer/hook_probe.go:212-233`, `placementDrift` at `:267-274`). The ledger comparison stays independent of the shape check: an unreadable ownership ledger prints its own UNREADABLE row for target `ownership`, and every other row still prints, without the ledger `DRIFT` comparison layered on it.

Operator hooks print nothing — no count line either. Codex homes print STALE and UNREADABLE rows only; with no expected Codex hook there is nothing to be MISSING there. Codex homes are drawn from the machine config's Codex accounts, named `codex[{id}]`, de-duplicated by physical path the same way Claude accounts are. A dangling-symlink settings file or `hooks.json` is UNREADABLE, never absent (`pfm/internal/installer/hook_probe.go:119-137`). OpenCode prints nothing.

### Exit

The check returns its warnings and failures to the doctor tally (`pfm/internal/doctor/doctor.go:252-254`). `pfm doctor` exits 3 on any failure, 1 on warnings only, 0 when clean (`pfm/internal/doctor/doctor.go:398-410`). A deterministic stub stays available through `HookProbeOverride` (`pfm/internal/installer/hook_probe.go:490`).

### What the check's own broken state reports

- An unreadable ownership ledger: one UNREADABLE row for target `ownership` (`pfm/internal/installer/hook_probe.go:65-73`), and every other file's rows still print without a ledger comparison.
- An empty machine config (no accounts): one line saying no Claude config dir is configured, never a silent pass.
- A probe that cannot resolve `$HOME`: doctor stops before the check, as before.

## This host

Read on 2026-09-23 with `pfm doctor` and a read-only parse of each file. This reading predates `callmeter`'s registration in `claudeHookTemplates`; the host is re-read after the next `pfm install` wires it.

- The machine config names three Claude config dirs, `$HOME/.claude`, `$HOME/.cc/2` and `$HOME/.cc/3`; the second and third are symlinks to `$HOME/.claude2` and `$HOME/.claude3`, and the ledger records the physical paths.
- All ten pfm hooks then defined were present in each of the three files: `pfm doctor` printed 30 `ok` hook rows and no `drift`, `stale` or `broken` row. Its `warnings=4` came from other checks.
- Each of the three files also holds four operator hooks: a notification script on `PreToolUse` and `Stop`, a memory-vault wire on `SessionStart` and a memory-vault sync on `SessionEnd`. They are the operator's own and are not reported.
- `$HOME/.codex/hooks.json` parses and holds no hook. Codex's own config enables its hook feature and records a vendor plugin's hook; neither is pfm's.
- No OpenCode plugin is configured, globally or in this repo.
- All five files read cleanly; none was UNREADABLE.

## Discrepancies

1. **Fixed: doctor never read a Codex `hooks.json`.** `probeCodexHooks` now opens every configured Codex home's `hooks.json`, shape-checks it and flags any retired or unknown pfm hook STALE, the same as a Claude settings file (`pfm/internal/installer/hook_probe.go:322-362`).
2. **Fixed: the executable was never checked.** `executableResult`/`executableVerdict` stat the command's first word, following a symlink, and report `absent`, `not-executable`, `not-regular-file` or `stat-failed(…)` as DRIFT (`pfm/internal/installer/hook_probe.go:285-317`).
3. **Fixed: a moved hook read as MISSING.** `absentHookResult` and `placementDrift` now find a stray copy under another (event, matcher) pair and report it as DRIFT `event` or `matcher`, only falling through to MISSING when no such copy exists anywhere (`pfm/internal/installer/hook_probe.go:244-274`).
4. **Fixed: read and parse failures printed `broken`,** the word used for a shape fault. Both now share one `unreadable` state and print `UNREADABLE`; a shape fault inside `hooks` (a non-object `hooks`, a non-array event, a non-string `matcher`, and so on) is treated the same way, making the whole file UNREADABLE (`pfm/internal/installer/hook_probe.go:415-484`).
5. **Fixed: duplicates surfaced only as a ledger `drift` warning, and the installer deduplicated five commands only.** `dropMisplacedTemplateHooks` now holds one placement rule for every pfm-owned hook, Claude-side, and the probe reports an in-file duplicate directly as DRIFT `count` (`pfm/internal/installer/settings_wiring.go:77-173`, `pfm/internal/installer/hook_probe.go:219-220`).
6. **Fixed: a stale count in a comment.** The "nine per-hook MISSING rows" comment is gone with the `expected_hooks.go` rewrite; `ReportHooks`'s doc comment in `hook_probe.go` names no hardcoded count.
7. **Fixed: the template shipped hooks this repo never ran.** `notify.sh` is the operator's own hand-written notifier and `filter-test-output.sh` is removed, so the template no longer ships either: their scripts, their `templates/project/settings.json` entries, their `templates/refresh-map.json` entries (which named `.claude/scripts/` sources that never existed), the `docs/SETUP.md` notification step, the `docs/BLUEPRINT.md` script lists, the `docs/dev/testing/landscape.md` rows and the `infra/fence/lanes/A.sh` assertion all went with them.
8. **Fixed: a dead script.** `.claude/scripts/explore-deny.sh` was wired nowhere, the binary having replaced it; it is deleted, with its mention in `docs/commands/pcm/references/audit-scopes.md`. The installer still rewrites a legacy `explore-deny.sh` command on an old host (`pfm/internal/installer/settings.go:41-42`).
9. **The Codex adapter says "Codex has NO hook layer"** (`.claude/codex-build.json:8`). Codex has one, and pfm reads and writes `{codex home}/hooks.json`. The conclusion (the guard is absolute in Codex) still holds, because pfm installs no Codex guard hook.
10. **Resolved: notifications are the operator's own.** This host runs `notify.sh` from `$HOME/.claude/scripts/` in its global settings; with the template's copy removed (item 7), nothing in pfm or the template claims it.
11. **Docs lag the inventory.** `docs/dev/pfm-surface.md:43` lists the internal hook bodies without `launcher-repair`; being fixed in the same batch, which adds its `internal callmeter` and `callmeter` rows. `docs/BLUEPRINT.md:209` lists "statusline" among the hooks (it is a `statusLine` key) and omits the guard and the mirror sync; that part is untouched here.
12. **Removed: the test filter.** It kept every passing line (its keep pattern matched `passed` case-insensitively: 300 `PASSED` lines and one failure reached the model as 200 lines, 199 of them passing). It was fixed and then removed from the template by the operator's ruling (item 7). The mechanism it used, `updatedToolOutput`, works on Claude Code 2.1.280.
13. **Twins differ.** `pfm-guard.sh`, `guard-stamp.sh` and `codex-sync.sh` differ between `.claude/scripts/` and `templates/project/scripts/`; `format-md.sh` is identical. The difference was not reviewed here.

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The expected list | `pfm/internal/installer/expected_hooks.go` | `claudeHookTemplates`, `ExpectedHooks` |
| The probe and printer | `pfm/internal/installer/hook_probe.go` | `ProbeExpectedHooks`, `ReportHooks`, the states |
| The Claude writer | `pfm/internal/installer/settings.go` | Convergence, dedupe, the retired table, the unknown-subcommand rule |
| The Codex writer | `pfm/internal/installer/codex_hooks.go` | Codex retirement |
| The ledger | `pfm/internal/installer/settings_ownership.go` | Ownership keys and counts |
| The dispatch | `pfm/cmd/pfm/main.go:54-68` | Every subcommand a hook may name |
| The hook bodies | `pfm/internal/hookentry/`, `pfm/cmd/pfm/statusline_command.go` | What each command does |
| The doctor | `pfm/internal/doctor/doctor.go:252` | Where the check runs and its tally |
| The project template | `templates/project/settings.json`, `templates/project/scripts/`, `templates/refresh-map.json` | The project-tier hooks and their sources |
| The scaffold | `pfm/internal/professor/scaffold.go:29-33` | Where `pfm init` puts them |
| This repo's install | `.claude/settings.json`, `.claude/scripts/` | The project tier this repo runs |
| The engine adapters | `.claude/codex-build.json` | What Codex and OpenCode are told about hooks |
| The CLI surface | `docs/dev/pfm-surface.md` | The `doctor`, `install` and `internal` rows |
| The setup docs | `docs/SETUP.md`, `docs/BLUEPRINT.md`, `docs/references/memory-backup.md` | What an adopter is told to install |
| The recorder | `pfm/internal/hookentry/callmeter.go`, `pfm/internal/callmeter/`, [callmeter.md](callmeter.md) | The tool-call hook and its design, once it ships |

## Open items

Decided:

1. **Ledger drift severity.** A ledger-only mismatch stays a DRIFT warning, not its own state.
2. **Project-tier coverage.** No project-tier doctor coverage in this batch; doctor still reports only the machine-global tier.
3. **Operator hooks.** No operator count line: doctor checks only pfm's own hooks and prints nothing about the operator's.
4. **This repo's missing template hooks.** Removed from the template: `notify.sh` is the operator's own, `filter-test-output.sh` is dropped (Discrepancies 7).
5. **The dead `explore-deny.sh`.** Deleted (Discrepancies 8).
6. **Duplicate removal.** Yes: the installer drops duplicates of every pfm hook, not five, through `dropMisplacedTemplateHooks`'s one placement rule per (event, matcher) pair.
7. **callmeter.** It is pfm-owned: it is a row in `claudeHookTemplates` carrying `Async`, and this check probes it like every other pfm-owned hook.
