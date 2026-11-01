# rules

A rule is a Markdown instruction file under `.claude/rules/`. Claude Code reads rules natively; Codex and OpenCode have no equivalent. engine-sync (`docs/design/engine-sync/engine-sync.md`) carries every rule to both engines by compiling it into an instruction file the engine reads at session start: an always-on rule (no `paths:`) is inlined, and a path-scoped rule becomes a read-on-match pointer, one line telling the loop to read the rule when it works on a matching file. Global rules compile into the Codex home's `AGENTS.md` block and an OpenCode `instructions` file; a project's own rules compile into that project's `AGENTS.md`, which OpenCode reads too. A designed injector would add a path-scoped rule's body mid-session, from one engine-sync matcher run by a Codex hook entry and an OpenCode shim.

**Status:** built except the injector. Built: [Global rule source](#global-rule-source), [Global always-on rules](#global-always-on-rules) (the Codex block, the OpenCode file and `instructions` entry), [Always-on project rules](#always-on-project-rules), the [read-on-match pointer](#read-on-match-pointer) for global and project path-scoped rules, [Agent targeting](#agent-targeting) on Claude Code (the rule-control plugin), the [freshness](#freshness) triggers at `pfm install`, `engine-sync doctor` and the Claude `SessionStart` hook, and the [Checks](#checks) rows marked built. Designed, not built: the [`engine-sync hook {codex|opencode}`](#engine-sync-hook-codexopencode) injector, its Codex hook entries and OpenCode shim, and the launch-time [freshness](#freshness) trigger — **Build path for the rest:** engine-sync phase P3, `flights-foreman` with this file as the spec — **Consumers:** `rm-rf-absolute-path`, `git-writes-through-gitter`, `flights-life-cycle`, `coding` and `testing`, shipped always-on; `professor` and `integration-tests`, shipped path-scoped; `no-hardcoded-locations`, this repo's own project rule, path-scoped to `pfm/**/*.go`, served by the pointer in `AGENTS.md` ([First consumer](#first-consumer)); `pfm-testing-manual`, not yet a rule; the rule-control plugin, which reads a rule's `visible-to:` key to deliver it to chosen loops on Claude Code ([Agent targeting](#agent-targeting))

`{codex home}` is one Codex account's home; `{claude config dir}` is one Claude account's `configDir`; `{engine-sync home}` is `~/.local/share/engine-sync`; `{engine-sync state}` is `~/.local/state/engine-sync`.

## Contents

- [Decisions](#decisions)
- [Engine facts](#engine-facts)
- [Claude Code, native](#claude-code-native)
- [Delivery matrix](#delivery-matrix)
- [Always-on project rules](#always-on-project-rules)
- [Path-scoped rules](#path-scoped-rules)
- [Global rule source](#global-rule-source)
- [Global always-on rules](#global-always-on-rules)
- [Checks](#checks)
- [Rejected alternatives](#rejected-alternatives)
- [The canary probe](#the-canary-probe)
- [Unverified](#unverified)
- [Open questions](#open-questions)
- [First consumer](#first-consumer)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)
- [Sources](#sources)

## Decisions

- **`.claude/rules/` is the one source.** Project rules live in `{project}/.claude/rules/**/*.md`, global rules in `~/.claude/rules/**/*.md`. `rules` is a shared store entry (`pfm/internal/installer/claude_store.go`), so every `{claude config dir}/rules` links to `~/.claude/rules`. Professor's own global rules ship as `templates/global/rules/*.md`, which `pfm install` links into `~/.claude/rules/` ([Global rule source](#global-rule-source)). engine-sync reads rules and never writes into either directory.
- **Two classes, one scope law.** A rule without `paths:` is always-on; a rule with `paths:` is path-scoped. A rule's scope is its owner directory intersected with its `paths:`: a rule in a nested `{dir}/.claude/rules/` applies only under `{dir}`, so a nested always-on rule is path-scoped to `{dir}/**`.
- **Always-on compiles inline; path-scoped compiles to a pointer.** Compiled text survives compaction and costs nothing per tool call. A pointer is one line in the compiled text naming the rule file and its globs: the loop reads the rule when it works on a matching file, so the body arrives only if the loop obeys. Only an injector can make the body appear when a matching file is touched ([designed](#path-scoped-rules)).
- **One matcher for both non-Claude engines (designed).** `engine-sync hook {codex|opencode}` (Go, in the engine-sync binary) holds path extraction, glob matching, the once-per-agent ledger and the output shape. The Codex hook entry and the OpenCode shim are thin callers of `{engine-sync home}/bin/engine-sync`. A Go binary exec costs about 10 ms on the host (measured on `pfm --version`, three runs).
- **Claude stays native.** engine-sync adds nothing to Claude's delivery; it only lints rule frontmatter, because Claude silently loads an unparseable `paths:` rule as always-on.
- **Rules reach every agent, the fleet prompt does not.** Rules never ride pfm's fleet-prompt channels (`developer_instructions` on Codex, the composed prompt's `instructions` entry on OpenCode). Those carry the main chat's system layer, and a Codex agent role replaces `developer_instructions`.
- **Global always-on on Codex:** an engine-sync-owned fenced block in every `{codex home}/AGENTS.md`.
- **Global always-on on OpenCode:** an engine-sync-owned entry in the global `instructions` array pointing at a compiled file, never `~/.config/opencode/AGENTS.md`.
- **Freshness.** `engine-sync global` compiles global rules, writing only when bytes differ. Built: it runs at `pfm install` and at every Claude `SessionStart` through the plugin, and `engine-sync doctor` reports `STALE` for any drift. Designed, not built: a run at every pfm launch of a Codex or OpenCode chat. No filesystem watcher. A project's rules compile with its `AGENTS.md`, rebuilt by the plugin's `Stop` hook and repaired at `SessionStart`.
- **Codex nested `AGENTS.md` rides the injector (designed).** A Codex session never reads an `AGENTS.md` below its working directory; the injector treats each one as a rule scoped to its own directory.
- **Rules carry protocol, engines differ in mechanics.** A compiled or injected rule body gets the same transform `CLAUDE.md` gets for `AGENTS.md` (`/command` and model-name rewrites, `CLAUDE.md` read as `AGENTS.md`), and nothing else.

## Engine facts

Verified against Claude Code 2.1.295 and 2.1.312 (probes), Codex 0.161.0 (source at commit `979011409de0`), OpenCode 1.18.31 (source at commit `014614d3`).

| Fact | Claude Code | Codex | OpenCode |
| --- | --- | --- | --- |
| Native glob-scoped rules | yes, `.claude/rules` with `paths:` | no | no |
| Instruction discovery | launch, plus nested `CLAUDE.md` and rules on file access | once at start, git root down to the working directory | upward from the project dir at start; `read` attaches nested `AGENTS.md`/`CLAUDE.md`/`CONTEXT.md` |
| Reads `.claude/rules` | yes | no | no |
| Rule targeting by agent | not natively (`paths` only); the rule-control plugin's `visible-to:` key | no | no |
| Mid-session injection point | built in (`nested_memory`) | `PreToolUse`/`PostToolUse` `additionalContext` | plugin `tool.execute.after` mutating `output.output` |
| Injected text lands as | a system-reminder after the tool result | a `developer`-role message | part of the tool result |
| File read tool | Read (also `cat`/`head` on one file) | none: `exec_command`, matched as `Bash` | `read` (`filePath`) |
| File edit tools | Edit, Write | `apply_patch`, matched as `apply_patch`, `Edit`, `Write` | `edit`, `write` (`filePath`), `apply_patch` (`patchText`) |
| Hooks in sub-agents | built in, fires (probed) | yes, child inherits hook config | yes, `task` runs the same prompt loop |
| Unknown `paths:` in a skill | n/a | dropped silently | dropped silently |
| Project instruction cap | none (warns past 200 lines per file) | `project_doc_max_bytes`, 32 KiB, project docs only | none found |

## Claude Code, native

**Mechanism:** a path-scoped rule is not a user hook. The session transcript records a `nested_memory` attachment, the one that also loads a subdirectory's `CLAUDE.md`, rendered as `<system-reminder>Contents of {path}:` plus the body, after the tool result that touched the file. It loads once per agent per session and loads again on demand after compaction.

**Triggers:** Read, Write or Edit on a matching file, and a Bash command that counts as a read (`cat`, `head` on one file). Glob and Grep do not trigger, nor does a Bash write: in a live test, a chat in a plugin directory edited `CLAUDE.md` with `sed -i` and a user rule globbing `**/CLAUDE.md` never loaded. A law that must bind every write to a file is therefore always-on.

**Sub-agents:** a sub-agent's own Read loads the rule into the sub-agent only. Probe: the `InstructionsLoaded` hook logged `agent_type: general-purpose`, `load_reason: path_glob_match`, `trigger_file_path: …/x_test.go`, and the parent reported no canary.

**Frontmatter:** `paths` is the only key read (a YAML list or a comma-separated string); other keys are ignored, and the frontmatter is stripped before loading. Unparseable YAML loads the rule as always-on. A rule may be a symlink; one pointing outside the project needs the external-import approval, and then only its always-on rules load.

**Native agent targeting:** Claude Code itself cannot target a rule at an agent or the main chat; its frontmatter has no such key ([Agent targeting](#agent-targeting) is the plugin that adds one). Probed on Claude Code 2.1.312 (2026-10-29, `/tmp/professor/rules-scope/probe/`, one run per agent, evidence the agents' quoted context lines):

- A user rule (`~/.claude/rules/`), a project rule and `CLAUDE.md` reach the main chat, a custom sub-agent and `general-purpose` alike.
- An agent with frontmatter `omitClaudeMd: true` receives none of the three: it drops user and project `CLAUDE.md` and every rule together. Managed policy is kept, and the key has no effect on the main session.
- A `SessionStart` hook's `additionalContext` reaches the main chat only, never a sub-agent.
- A `SubagentStart` hook's `additionalContext` reaches the sub-agent whose `agent_type` its matcher names: it reached the two named custom agents (`omitClaudeMd` included) and not `general-purpose`, which the matcher did not name.
- `InstructionsLoaded` fires only at the main chat's session start (`load_reason: session_start`). A sub-agent's always-on instructions are not logged separately, so the engine log cannot prove per-agent delivery.

**User-rule globs:** a user rule's `paths:` globs match the touched file's path relative to the session's project root. Probed on Claude Code 2.1.314 (2026-10-30, a scratch project under `~/.cache/`, since the `~/` globs needed home paths, two runs plus one in this repo, evidence the transcript's `nested_memory` attachments, each after the Read that triggered it):

- `**/NAME.md` matched a file in a subdirectory and at the project root; `**/.dir/**` and `**/dir/global/**` matched through dot directories (`.wt/flight/…`). The stored glob drops a trailing `/**` (`**/.pcmdot`).
- A `~/`-prefixed glob matched nothing, for a file inside the project or outside it.
- No user rule matched a file outside the project, `**/dir/**` included.
- A parent directory's `.claude/rules/` rule did not fire for a file under that parent when the session ran in a child directory.

So a user rule guards files only by project-relative globs, and only in sessions whose project holds the file.

**Debugging:** the `InstructionsLoaded` hook reports `file_path`, `memory_type`, `load_reason` (`session_start`, `path_glob_match`) and `trigger_file_path`; `/context` lists what loaded at launch.

### Agent targeting

**Built** by the rule-control plugin (`plugins/rule-control/`, its own repository, listed in this repo's marketplace), on the probe results above. A top-level `visible-to: {include: [...], exclude: [...]}` key in a rule's frontmatter names the loops that receive the rule: `main` (the main chat) or an agent type, exact or a `*` glob. A targeted rule leaves the instruction context every loop shares (`prompt.context`) and is given to each loop it names as hook context: the main chat at `SessionStart`, a sub-agent at `SubagentStart`, a compacted loop again at the `SessionStart` its `session.compact` pairs with; a path-scoped rule with `visible-to` is withheld (`prompt.attachment`) from a loop it leaves out. It fails open: a malformed key, an unreadable rule or a failing hook is logged and the rule reaches every loop. Live evidence: the plugin repository's README (tested on Claude Code 2.1.316) and the run records in `/tmp/rule-control/evidence/`.

**The engine-sync gap:** Codex and OpenCode have no per-loop delivery. engine-sync reads only `paths:`, so a rule with `visible-to:` and no `paths:` stays always-on: it compiles into the Codex block and the OpenCode file like any other, frontmatter stripped, and reaches every loop there. `engine-sync doctor --machine` names each one in a `global-rules VISIBLE-TO {rule} delivered to every Codex and OpenCode loop` row, level `ok`: a known gap with no repair, never a failure.

## Delivery matrix

| Scope | Class | Claude Code | Codex | OpenCode |
| --- | --- | --- | --- | --- |
| project | always-on | native | inlined in the project's `AGENTS.md` | the same `AGENTS.md` |
| project | path-scoped | native | read-on-match pointer in the project's `AGENTS.md` | the same `AGENTS.md` |
| global | always-on | native | inlined in the owned block in `{codex home}/AGENTS.md` | inlined in the owned `instructions` entry |
| global | path-scoped | native | read-on-match pointer in the owned block | read-on-match pointer in the owned `instructions` entry |
| nested `{dir}/.claude/rules` of a child project | either | native, on demand | compiled into `{dir}/AGENTS.md`, which Codex reads only for a session started at or below `{dir}` | compiled into `{dir}/AGENTS.md`, attached on a `read` below `{dir}` |
| nested `{dir}/AGENTS.md` | n/a | native (`CLAUDE.md`) | the injector, scoped to `{dir}/**` (designed) | native on `read` |

Every cell is built except the Codex nested `AGENTS.md` one. A pointer asks the loop to read the rule; no engine hook enforces it, so the rule body reaches the context only when the loop follows the line. The injector (designed) would add the body on top of the pointer.

## Always-on project rules

**Built.** A project's own `.claude/rules/**/*.md` compile into that project's `AGENTS.md`, for the root and for every child project with its own `AGENTS.md`.

**Loading:** `source.Rules` (`plugins/engine-sync/internal/source/rules.go`) reads a rules directory, the one door global rules use too: every `.md` below it, links followed, in sorted relative-path order; an absent directory holds no rules. `LoadProject` (`internal/source/project.go`) calls it for `{project}/.claude/rules` of each project.

**Compile point:** `orientations` in `plugins/engine-sync/internal/target/codex/orientation.go` appends `codex.CompileRules` output after the compiled `CLAUDE.md` body and before `codex.rootAdapter`. Rules sit in sorted path order, each under `<!-- rule: {project-relative}/.claude/rules/{rel} -->` (`.claude/rules/…` at the root, `{child}/.claude/rules/…` in a child), frontmatter stripped. An always-on rule's body gets the same transform the `CLAUDE.md` body gets, so `CLAUDE.md` inside a rule reads `AGENTS.md`. A rule with `visible-to:` and no `paths:` stays always-on. A path-scoped rule becomes its [pointer](#read-on-match-pointer). A project with no rules compiles unchanged.

**Failure:** an unparseable rule header is `RULE-FRONTMATTER {path}: {error}`, a rule body holding the engine-sync rules fence line `RULE-FENCE {path}: …`, an unreadable rule or directory `read rule {path}: …` or `read rules {dir}: …`. Each is a problem that fails the run: that project's `AGENTS.md` is not compiled, and a failure never reads as no rules.

**Check:** `engine-sync codex check` compares the compiled `AGENTS.md` byte for byte (`internal/reconcile`), so a rule edit not rebuilt reports `STALE`. OpenCode reads the same `AGENTS.md`, gets no file or `instructions` entry of its own, and `engine-sync opencode check` and `doctor` report a differing engine-sync-owned `AGENTS.md` as `STALE {path}` with the remedy to run `engine-sync codex build`.

**Budget:** Codex stops adding project docs past `project_doc_max_bytes`, 32768 by default. `AGENTS-BUDGET {path}` (`internal/target/codex/budget.go`) measures the Codex chain, that `AGENTS.md` plus the `AGENTS.md` of every project above it, against the limit from the project's `.codex/config.toml`; a project that sets none gets the strictest Codex home's limit, and a home without the key counts as 32768. `0` disables the check and a negative limit is an error. A build warns and writes the file whole, `check` fails, and nothing is ever cut.

**Adopters:** the engine-sync plugin's `Stop` hook already builds both mirrors, so adopter rules compile with no template change.

## Path-scoped rules

### Read-on-match pointer

**Built.** `codex.CompileRules` (`plugins/engine-sync/internal/target/codex/rules.go`) compiles a rule with `paths:` into two lines in its sorted place, one pointer per rule, body not compiled:

```text
<!-- rule: {path} (path-scoped) -->
Before working on a file matching `{glob}`, read {path} and follow it.
```

Several globs read `` `{glob}`, `{glob}` ``. `{path}` is `{project-relative}/.claude/rules/{rel}` for a project rule, in that project's `AGENTS.md`, and `~/.claude/rules/{rel}` for a global rule, in the owned Codex block and the OpenCode `instructions` file (`internal/machine/rules.go`). A global pointer counts toward `GLOBAL-BUDGET`. `engine-sync doctor --machine` names each global one in a `global-rules PATH-SCOPED {rule} delivered to Codex and OpenCode as a read-on-match pointer` row, level `ok`.

The pointer is the delivery on Codex and OpenCode today: both engines have no path trigger, so the line is an instruction the loop follows when it touches a matching file.

### `engine-sync hook {codex|opencode}`

**Designed, not built.** The injector would add the rule body when a matching file is touched, on top of the pointer. Today `engine-sync hook codex` and `hook opencode` (`internal/cli/hook.go`, `runHook`) exit 0 with no output, and no Codex hook entry or OpenCode shim calls them.

**Input:** the engine's hook JSON on stdin. Codex: `session_id`, `agent_id` (sub-agents only), `cwd`, `tool_name`, `tool_input`. OpenCode: the shim forwards `sessionID`, `tool`, `args` and the instance directory.

**Path extraction:**

| Engine | Tool | Paths taken from |
| --- | --- | --- |
| Codex | `apply_patch` | `*** Add File:`, `*** Update File:` and `*** Move to:` headers of the raw patch in `tool_input.command` |
| Codex | `Bash` | every shell word of the command that resolves, against the command's working directory, to an existing regular file |
| OpenCode | `read`, `edit`, `write` | `args.filePath` |
| OpenCode | `apply_patch` | the file headers of `args.patchText` |
| OpenCode | `bash` | every shell word of `args.command` resolving against `args.workdir` to an existing regular file |

Codex reads through `sed`, `nl`, `rg`, `cat` and others, so the Bash rule takes any existing-file argument, not a verb list; an extra match costs one rule, once. Glob, grep and directory arguments never trigger, matching Claude.

**Rule set:** global rules from `~/.claude/rules/**/*.md`, plus the rules of every `.claude/rules/` from the project root down to the touched file's directory, plus (Codex only) every `AGENTS.md` strictly below the session's working directory on that path. The project root is the git root above the working directory. Rules are read live from source on each call; there is no compiled index to go stale.

**Matching:** `paths:` globs resolve against the project root, with `**` and brace expansion as Claude documents them; a rule outside the root project matches only under its owner directory. A rule with unparseable frontmatter is skipped and reported, never treated as always-on.

**Ledger:** `{engine-sync state}/rules/{engine}/{session}-{agent}.json` records each rule path already injected for that agent, so a rule lands once per agent per session and a sub-agent gets its own copy. A compaction clears the agent's ledger.

**Output:** the body under the line `Contents of {project-relative path}:`, the shape Claude renders. Codex receives `{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"…"}}` and nothing else, because every Codex output struct denies unknown fields. OpenCode receives the text, which the shim appends to `output.output` inside `<system-reminder>` tags.

**Failure:** the injector never blocks a tool. On any error it exits 0 with no output, writes the error with its full context to `{engine-sync state}/rules/errors.log`, and `engine-sync doctor` reports errors logged since its last run.

### Codex wiring

**Designed, not built.** `engine-sync global` would add two engine-sync-owned entries to every target `{codex home}/hooks.json`, beside pfm's `resume-unkill` entry:

- `PreToolUse`, matcher `Bash|apply_patch` (a pattern of `[A-Za-z0-9_|]` is an exact name list), command `$HOME/.local/share/engine-sync/bin/engine-sync hook codex`. Pre, not Post: an edit receives its rule before it writes.
- `PostCompact`, command `$HOME/.local/share/engine-sync/bin/engine-sync hook codex --reset`, clearing the ledger because hook developer messages are not carried across a local compaction.

Codex runs a hook only once trusted, by a `sha256` of its event, matcher and handler. The command strings never change across engine-sync versions, so trust survives an update. pfm records trust for the fleet through `codexappendix.RegisterHookTrust` (`pfm/internal/codexappendix/trust.go:264`), as it does for `resume-unkill`; a standalone user trusts once in `/hooks`.

### OpenCode wiring

**Designed, not built.** `engine-sync global` would write an engine-sync-owned shim at `~/.config/opencode/plugins/engine-sync.js`, auto-loaded from that directory, with an ownership receipt. Its `tool.execute.after` runs `{engine-sync home}/bin/engine-sync hook opencode` for `read`, `edit`, `write`, `apply_patch` and `bash`, and appends the result to `output.output`. Appending to the tool result keeps the system prompt byte-stable for prompt caching. A session-compaction event resets the session's ledger. pfm's workbench seat plugin (`pfm/internal/workbench/opencode_seat.go:15`) is the precedent for a staged OpenCode plugin file.

## Global rule source

**Built.** `templates/global/rules/*.md` is the fourth machine-global template class, beside agents, commands and skills.

**Install:** `pfm install` (`wireGlobalRules`, `pfm/internal/installer/global_fanout.go`) links each regular `.md` file in the source clone's `templates/global/rules/` to `{claude config dir}/rules/{name}.md` in the Claude store, one symlink per file. It uses the link classifier that global commands use: a real file that is not a byte copy stays in place as a conflict, and a byte copy is relinked. A non-`.md` entry or a directory is skipped with a line naming it. Every account sees the rules through the shared `rules` store entry (`{claude config dir}/rules` → `~/.claude/rules`).

**Retire:** `pfm install --uninstall` removes the rules links pfm owns (`global_unwire.go`). A dead or orphaned rules link is pruned (`registry_orphans.go`). `registry-ownership` scans `{store}/rules` with the other registries (`registry_ownership.go`).

**Doctor:** `pfm doctor` prints `doctor: rules` rows (`ReportGlobalRules`, called from `skill_sources_report.go`):

- A rule link in state `missing`, `copy`, `wrong-target` or `conflict` gets a warning row naming the fix. The fix for a conflict is to move the file aside, then run `pfm install --yes`.
- A clone or rules source that cannot be read is a failing `state=CHECK-FAILED` row, never shown as zero rules.
- An absent clone prints `state=NO-CLONE`, and an absent source prints `state=NO-SOURCE`.
- A healthy store prints `doctor: rules ok ({N} linked)`.

**Shipped rules:**

| Rule | Class | Says |
| --- | --- | --- |
| `rm-rf-absolute-path.md` | always-on | `rm -rf` in any shell command an agent or main chat runs takes a literal absolute path, never a variable, substitution, glob or relative path; code written into files is exempt |
| `git-writes-through-gitter.md` | always-on | every git write goes to the `gitter` agent, `git fetch` included (check a remote with `ls-remote`); others run reads plus the own-file operations the git-guard hook allows (`stash push -- <paths>`, `checkout -- <file>`, `restore <file>`, dry runs, `worktree.sh list`); a denied command is never re-spelled past the guard; push, tag and release only on the user's in-turn ask; on Codex and OpenCode the engine guard denies every git write, `gitter`'s included ([engine-guard](../hooks/engine-guard.md)) |
| `professor.md` | path-scoped (`**/CLAUDE.md`, `**/.claude/**`), visible to the main chat only | only `pcm` writes a `CLAUDE.md` or a `.claude/**` file, shell writes included; others hand `pcm` the change, however small; Professor's own `templates/**` law lives in its `CLAUDE.md` |
| `flights-life-cycle.md` | always-on | binds the main chat: settle, then size — small work in place with no worktree or lander, larger work in a gitter-made `.worktrees/{flight}/`; related changes found while a flight's worktree is open join that worktree; one `flights-lander` per worktree once every foreman returned; gitter merges and tears down |
| `coding.md` | always-on, visible to the foremen and the lander only (`visible-to`) | the write-time coding laws: one door per operation and mechanism, every caller uses it, grep before writing; a copy met in the touched code folds into its door before the change builds on it, every copy found by grepping the expression, one outside the task's files reported, one left named with why; a fold keeps every caller's exact result, a disagreement becoming a parameter or a named behavior change, tests green after the fold; one concept folded, never a coincidence; a file over the size ceiling split before logic is added; presentation displays values, domain computation moves to its door; one name per concept; code lives with what changes with it, no negation-named directories; lists derived or generated, repeated scripts in `scripts/`; a failure never reads as absence |
| `testing.md` | always-on, visible to the foremen and the lander only (`visible-to`) | how to write a test, never how to run one (running laws live in each project's testing manual): extend an existing test before adding one; test each decision once at the lowest layer; assert the exact value, never a substring, existence or a bare status; a test that can fail, watched failing; a fixed user-visible failure pinned at its surface; runs the code; guards both ways; fakes through the production seam; bounded polls, no sleeps; run-unique resources and id-scoped reads; changes inside the blast radius; failure messages carry expected, actual and artifact path; no self-skip or quarantine; domain names |
| `integration-tests.md` | path-scoped (`integration`, `e2e`, `hermetic`, `lanes`, `suites` paths) | points suite design to `/quality:integration-suite`; tier placement, process boot, scripted protocol mocks, sequenced lanes with preludes, after-effect checks, reading probes, clean-log verdicts, LLM output by structure |

## Global always-on rules

**Built** in `plugins/engine-sync/internal/machine/rules.go` and `rules_opencode.go`. `engine-sync global` reads the rules under the first Claude config dir (the store), `{store}/rules/**/*.md`. It compiles through `codex.CompileRules`, the door project rules use too ([Always-on project rules](#always-on-project-rules)): every rule without `paths:` inline and every rule with `paths:` as its [read-on-match pointer](#read-on-match-pointer), each under its `<!-- rule: … -->` line in sorted path order, frontmatter stripped; a rule carrying rule-control's `visible-to:` is compiled the same way ([the engine-sync gap](#agent-targeting)). A rule whose frontmatter does not parse is skipped with a `RULE-FRONTMATTER` line. `engine-sync global --uninstall` removes the Codex block and the OpenCode entry without reading any rule.

### Codex: `{codex home}/AGENTS.md`

`engine-sync global` writes the compiled set (always-on bodies and pointers) as one block between `<!-- BEGIN engine-sync rules — owned -->` and `<!-- END engine-sync rules — owned -->` in each target Codex home's `AGENTS.md`. Text outside the fence is preserved. If the block was all the file held and the set empties, the file is deleted. Codex sends the global file as the first user-role message, before project docs, which matches Claude's order of user rules before project rules. `project_doc_max_bytes` does not cap it, it is rebuilt into the initial context after compaction, and sub-agents inherit the global instruction snapshot. A `{codex home}/AGENTS.override.md` makes Codex skip `AGENTS.md`, and doctor reports that as `SHADOWED`.

### OpenCode: the `instructions` entry

`engine-sync global` compiles the same set into `{engine-sync home}/rules/global-always-on.md`. It registers that path in the global `instructions` array of `~/.config/opencode/opencode.jsonc`, recording the entry it added in the receipt `{engine-sync state}/opencode-instructions.json`. The entry sits beside pfm's fleet-prompt entry. OpenCode rebuilds instructions on every step and gives them to sub-agents. A glob straight at `~/.claude/rules/*.md` cannot work, because it would include path-scoped rule bodies and raw frontmatter.

### Freshness

No project build covers `~/.claude/rules/`. Triggers 1, 3 and 4 run `engine-sync global`; trigger 2 only reports drift:

1. **Built:** `pfm install`, for every Codex home and OpenCode.
2. **Built:** `engine-sync doctor --machine`, rendered by `pfm doctor`. It compares each compiled target with its sources and reports `STALE`, which catches drift from every rule edit made since the last install.
3. **Built:** the engine-sync plugin's Claude `SessionStart` hook (`engine-sync hook claude start`, `Start` in `plugins/engine-sync/internal/hook/hook.go`), which runs `engine-sync global` over the machine roster after repairing the project's stale mirror.
4. **Designed:** every pfm launch of a Codex or OpenCode chat, writing only when bytes differ. `pfm` runs `engine-sync global` only at `pfm install`.

## Checks

| Check | Broken state it reports | Status |
| --- | --- | --- |
| `pfm doctor` | `doctor: rules` link `missing`, `copy`, `wrong-target`, `conflict`; unreadable source `CHECK-FAILED` | built |
| `engine-sync doctor --machine` | `global-rules codex {path} state=MISSING`, `STALE` or `SHADOWED` | built |
| `engine-sync doctor --machine` | `global-rules opencode {file} state=MISSING` or `STALE`; `global-rules opencode instructions entry {config} state=MISSING` or `STALE` | built |
| `engine-sync doctor --machine` | `RULE-FRONTMATTER {path}: {parse error}` or `RULE-FENCE {path}: …` for a global rule, level `fail`, the rule skipped | built |
| `engine-sync doctor --machine` | `global-rules PATH-SCOPED {rule} delivered to Codex and OpenCode as a read-on-match pointer` (`ok`: the [pointer](#read-on-match-pointer), no repair) | built |
| `engine-sync doctor --machine` | `global-rules VISIBLE-TO {rule} delivered to every Codex and OpenCode loop` (`ok`: the [agent-targeting](#agent-targeting) gap, no repair) | built |
| `engine-sync doctor --machine` | `GLOBAL-BUDGET {bytes} > 9216` when the compiled global set (always-on bodies and pointers) exceeds 9 KiB, the cost every agent of every project pays (warning) | built |
| `engine-sync doctor --machine` | `global-rules ERROR …` when a source, target or config cannot be read, `read rule {path}: …` and `read rules {dir}: …` included; never shown as a clean state | built |
| `pfm doctor` | every `engine-sync doctor` row, rendered; a missing binary as a failing row | built |
| `engine-sync codex check` | `STALE` or `MISSING` `AGENTS.md` after a project rule edit (the reconcile compare) | built |
| `engine-sync codex check`, `build` | `RULE-FRONTMATTER {path}: {parse error}`, `RULE-FENCE {path}: …`, `read rule {path}: …` or `read rules {dir}: …` for a project rule: a problem, that project's `AGENTS.md` not compiled | built |
| `engine-sync codex check` | `AGENTS-BUDGET {path}: its Codex chain is {bytes} > {limit} bytes (…)` when a compiled `AGENTS.md` plus the project `AGENTS.md` above it exceeds `project_doc_max_bytes`; a failure in `check`, a warning in `build`, never a cut | built |
| `engine-sync opencode check`, `doctor` | `STALE {path}` for an engine-sync-owned `AGENTS.md` that differs from a fresh compile, remedy `OpenCode reads it; run engine-sync codex build` | built |
| `engine-sync doctor` | Codex hook entry `MISSING` or `UNTRUSTED` in a `{codex home}` | designed |
| `engine-sync doctor` | OpenCode shim `MISSING` or `STALE` | designed |
| `engine-sync doctor` | injector errors logged since the last doctor run | designed |

## Rejected alternatives

### A skill whose description says "load when working in X"

Description matching is the model's judgment, so the load is probabilistic: the check passes when the model happens to choose the skill and fails silently when it does not. The skill stays for explicit invocation only.

### Global rules inside `developer_instructions`

That block is the fleet prompt, the main chat's system layer by design, and a Codex agent role replaces it. Rules must reach every agent.

### `~/.config/opencode/AGENTS.md`

OpenCode takes the first of `~/.config/opencode/AGENTS.md` and `~/.claude/CLAUDE.md` that exists, so writing it would hide the global `CLAUDE.md` from OpenCode.

### `~/.codex/rules/`

That name is Codex's command-approval policy (exec policy); compiling instruction rules there would mix two unrelated systems.

### Codex `SessionStart` and `SubagentStart` injection of global rules

Live, with no compiled copy to go stale, but hook messages are not carried across compaction and pfm's `SessionStart` entry matches only `resume`. The compiled block has neither problem.

### A filesystem watcher on `~/.claude/rules/`

Always fresh, at the cost of one more background process; refresh at `pfm install` (launch-time refresh designed) plus the doctor `STALE` row covers the same ground.

### Compiling every rule always-on

It ignores `paths:`, spends Codex's 32 KiB project budget, and loads text into sessions that never touch the files it governs.

### A Codex plugin and an OpenCode plugin for the injector

A Codex plugin cannot carry roles or instructions, so it would add only the hook, which needs trust either way; a hook entry and a shim written by `engine-sync global` deliver the same runtime with nothing installed in either engine (`docs/design/engine-sync/engine-sync.md`).

## The canary probe

Acceptance for each engine. A scratch project holds a rule with `paths: ["pfm/**/*_test.go"]` whose body is one line, `CANARY-7731: if this text is in your context, the code word is PLUM.`, plus `pfm/internal/x/x.go` and `pfm/internal/x/x_test.go`. Evidence comes from the engine's own record (Claude's `InstructionsLoaded` log, the Codex rollout, the OpenCode session store), never from the model's self-report alone.

1. At launch: no canary.
2. After reading `x.go`: no canary.
3. After reading `x_test.go`: the canary, exactly once.
4. After a second read of `x_test.go`: no second copy.
5. In a sub-agent that reads `x_test.go`: the canary in the sub-agent; the parent unchanged.
6. After compaction and another read: the canary again.
7. A global always-on rule: present at launch in the main chat and a sub-agent.

Claude passed 1–5 on 2.1.295 in `/tmp/professor/rules-test/`.

## Unverified

- The field names of Codex `exec_command` in `tool_input` (command and working directory).
- Whether a Codex hook runs inside the sandbox, which decides whether the ledger write in `{engine-sync state}` succeeds.
- Whether Codex hook developer messages are dropped on compaction (inferred from source, not run).
- Whether OpenCode code-mode (`experimentalCodeMode`) skips `tool.execute.after`, and whether OpenCode compaction drops text appended to a tool result.
- Whether a `SubagentStart` hook's `additionalContext` survives a sub-agent's compaction.
- The Go glob library: engine-sync's `go.mod` starts with none; the build picks and validates one that supports `**` and brace expansion.

## Open questions

- **Global `CLAUDE.md` on the same channel:** whether the owned Codex block and OpenCode entry also carry `~/.claude/CLAUDE.md`. It is empty today and unruled; the built channel carries rules only.

## First consumer

**Global, shipped:** `templates/global/rules/rm-rf-absolute-path.md`, an always-on rule ([Global rule source](#global-rule-source)). It reaches Claude natively, and reaches Codex and OpenCode through the built global channel.

**Project path-scoped, built:** `.claude/rules/no-hardcoded-locations.md`, this repo's own rule (`paths: ["pfm/**/*.go"]`), binds every loop editing Go under `pfm/`. It reaches Claude natively, and Codex and OpenCode through the [pointer](#read-on-match-pointer) `engine-sync codex build` compiles into the root `AGENTS.md` under `<!-- rule: .claude/rules/no-hardcoded-locations.md (path-scoped) -->`. The pointer delivers an instruction to read the rule, not its body; the injector would add the body.

**Project path-scoped, not yet a rule:** `pfm-testing-manual`. Its file, `.claude/commands/pfm-testing-manual.md`, carries no `paths:` and no `.claude/rules/pfm-testing.md` exists, so no pointer serves it. The plan is a symlink `.claude/rules/pfm-testing.md` to `../commands/pfm-testing-manual.md`, whose frontmatter gains `paths:` for `pfm/**/*_test.go`; the pointer would then serve it on Codex and OpenCode, with the injector adding the body later. Local to this repo, no `templates/project/` twin. One file stays both the `/pfm-testing-manual` command and the rule; Claude ignores `name` and `description` on the rule, and the skill loaders ignore `paths`. The flights briefs keep naming the manual explicitly: the rule adds automatic delivery, the explicit read stays the protocol on every engine.

## Surfaces that stay in sync

Built channel, kept in step with this file:

- `INSTALL.md` (the registries `pfm install` writes) and `docs/SETUP.md` (where machine-global templates are linked) name `templates/global/rules/` beside agents, commands and skills.
- `docs/design/engines/claude-config-dir.md`: the `rules` shared entry and its `{store}/rules` registry, with consumers in Claude, Codex and OpenCode.
- `docs/design/engine-sync/engine-sync.md`: what `engine-sync global` carries, and the `registry-ownership` registries.
- `CLAUDE.md` § Vocabulary, the machine-global tier: `rules` beside agents, commands and skills, through the `pcm` agent.

Changes that land with the designed parts:

- `docs/design/hooks/hooks.md`: the Codex hook rows and the OpenCode row ("carries none") gain engine-sync's entries and shim, once the injector ships.
- `.claude/commands/pfm-testing-manual.md`: the `paths:` key, through the `pcm` agent, when it becomes a rule.
- `README.md`, `docs/SETUP.md`: wherever they list what the engine mirrors compile, now that project rules compile into `AGENTS.md`.

## Sources

- `.professor/RR/codex-path-scoped-instruction-injection-2026-10-09.md`
- `.professor/RR/opencode-path-scoped-instruction-injection-2026-10-09.md`
- Claude Code memory and sub-agent docs: `https://code.claude.com/docs/en/memory`, `https://code.claude.com/docs/en/sub-agents`
