# rules

A rule is a Markdown instruction file under `.claude/rules/`. Claude Code reads rules natively; Codex and OpenCode have no equivalent. engine-sync (`docs/design/engine-sync/engine-sync.md`) carries every rule to both engines over two channels: an always-on rule (no `paths:`) is compiled into an instruction file the engine reads at session start, and a path-scoped rule is injected mid-session by one engine-sync matcher, run from a Codex hook entry and an OpenCode shim that engine-sync writes. This file is the design; none of it is built yet.

**Status:** designed, not built — **Build path:** engine-sync phase P3, `flights-foreman` with this file as the spec — **First consumer:** `pfm-testing-manual`, local to this repo

`{codex home}` is one Codex account's home; `{claude config dir}` is one Claude account's `configDir`; `{engine-sync home}` is `~/.local/share/engine-sync`; `{engine-sync state}` is `~/.local/state/engine-sync`.

## Contents

- [Decisions](#decisions)
- [Engine facts](#engine-facts)
- [Claude Code, native](#claude-code-native)
- [Delivery matrix](#delivery-matrix)
- [Always-on project rules](#always-on-project-rules)
- [Path-scoped rules](#path-scoped-rules)
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

- **`.claude/rules/` is the one source.** Project rules live in `{project}/.claude/rules/**/*.md`, global rules in `~/.claude/rules/**/*.md`. `rules` is already a shared store entry (`pfm/internal/installer/claude_store.go:42`), so every `{claude config dir}/rules` links to `~/.claude/rules`. engine-sync reads rules and never writes into either directory.
- **Two classes, one scope law.** A rule without `paths:` is always-on; a rule with `paths:` is path-scoped. A rule's scope is its owner directory intersected with its `paths:`: a rule in a nested `{dir}/.claude/rules/` applies only under `{dir}`, so a nested always-on rule is path-scoped to `{dir}/**`.
- **Always-on compiles; path-scoped injects.** Compiled text survives compaction and costs nothing per tool call; injection is the only way to make text appear when a matching file is touched.
- **One matcher for both non-Claude engines.** `engine-sync hook {codex|opencode}` (Go, in the engine-sync binary) holds path extraction, glob matching, the once-per-agent ledger and the output shape. The Codex hook entry and the OpenCode shim are thin callers of `{engine-sync home}/bin/engine-sync`. A Go binary exec costs about 10 ms on the host (measured on `pfm --version`, three runs).
- **Claude stays native.** engine-sync adds nothing to Claude's delivery; it only lints rule frontmatter, because Claude silently loads an unparseable `paths:` rule as always-on.
- **Rules reach every agent, the fleet prompt does not.** Rules never ride pfm's fleet-prompt channels (`developer_instructions` on Codex, the composed prompt's `instructions` entry on OpenCode). Those carry the main chat's system layer, and a Codex agent role replaces `developer_instructions`.
- **Global always-on on Codex:** an engine-sync-owned fenced block in every `{codex home}/AGENTS.md`.
- **Global always-on on OpenCode:** an engine-sync-owned entry in the global `instructions` array pointing at a compiled file, never `~/.config/opencode/AGENTS.md`.
- **Freshness is launch-time.** `engine-sync global` compiles global rules; it runs at `pfm install`, at every pfm launch of a Codex or OpenCode chat, and at every Claude `SessionStart` through the plugin, writing only when bytes differ. `engine-sync doctor` reports `STALE` for a session started outside all three. No filesystem watcher.
- **Codex nested `AGENTS.md` rides the injector.** A Codex session never reads an `AGENTS.md` below its working directory; the injector treats each one as a rule scoped to its own directory.
- **Rules carry protocol, engines differ in mechanics.** A compiled or injected rule body gets the same transform `CLAUDE.md` gets for `AGENTS.md` (`/command` and model-name rewrites), and nothing else.

## Engine facts

Verified against Claude Code 2.1.295 (probe), Codex 0.161.0 (source at commit `979011409de0`), OpenCode 1.18.31 (source at commit `014614d3`).

| Fact | Claude Code | Codex | OpenCode |
| --- | --- | --- | --- |
| Native glob-scoped rules | yes, `.claude/rules` with `paths:` | no | no |
| Instruction discovery | launch, plus nested `CLAUDE.md` and rules on file access | once at start, git root down to the working directory | upward from the project dir at start; `read` attaches nested `AGENTS.md`/`CLAUDE.md`/`CONTEXT.md` |
| Reads `.claude/rules` | yes | no | no |
| Mid-session injection point | built in (`nested_memory`) | `PreToolUse`/`PostToolUse` `additionalContext` | plugin `tool.execute.after` mutating `output.output` |
| Injected text lands as | a system-reminder after the tool result | a `developer`-role message | part of the tool result |
| File read tool | Read (also `cat`/`head` on one file) | none: `exec_command`, matched as `Bash` | `read` (`filePath`) |
| File edit tools | Edit, Write | `apply_patch`, matched as `apply_patch`, `Edit`, `Write` | `edit`, `write` (`filePath`), `apply_patch` (`patchText`) |
| Hooks in sub-agents | built in, fires (probed) | yes, child inherits hook config | yes, `task` runs the same prompt loop |
| Unknown `paths:` in a skill | n/a | dropped silently | dropped silently |
| Project instruction cap | none (warns past 200 lines per file) | `project_doc_max_bytes`, 32 KiB, project docs only | none found |

## Claude Code, native

**Mechanism:** a path-scoped rule is not a user hook. The session transcript records a `nested_memory` attachment, the one that also loads a subdirectory's `CLAUDE.md`, rendered as `<system-reminder>Contents of {path}:` plus the body, after the tool result that touched the file. It loads once per agent per session and loads again on demand after compaction.

**Triggers:** Read, Write or Edit on a matching file, and a Bash command that counts as a read (`cat`, `head` on one file). Glob and Grep do not trigger.

**Sub-agents:** a sub-agent's own Read loads the rule into the sub-agent only. Probe: the `InstructionsLoaded` hook logged `agent_type: general-purpose`, `load_reason: path_glob_match`, `trigger_file_path: …/x_test.go`, and the parent reported no canary.

**Frontmatter:** `paths` is the only key read (a YAML list or a comma-separated string); other keys are ignored, and the frontmatter is stripped before loading. Unparseable YAML loads the rule as always-on. A rule may be a symlink; one pointing outside the project needs the external-import approval, and then only its always-on rules load.

**Debugging:** the `InstructionsLoaded` hook reports `file_path`, `memory_type`, `load_reason` (`session_start`, `path_glob_match`) and `trigger_file_path`; `/context` lists what loaded at launch.

## Delivery matrix

| Scope | Class | Claude Code | Codex | OpenCode |
| --- | --- | --- | --- | --- |
| project | always-on | native | compiled into the project's `AGENTS.md` | the same `AGENTS.md` |
| project | path-scoped | native | `engine-sync hook codex` via `PreToolUse` entry | `engine-sync hook opencode` via shim |
| global | always-on | native | owned block in `{codex home}/AGENTS.md` | owned `instructions` entry |
| global | path-scoped | native | `engine-sync hook codex` via `PreToolUse` entry | `engine-sync hook opencode` via shim |
| nested `{dir}/.claude/rules` | either | native, on demand | the injector, scoped to `{dir}/**` | the injector, scoped to `{dir}/**` |
| nested `{dir}/AGENTS.md` | n/a | native (`CLAUDE.md`) | the injector, scoped to `{dir}/**` | native on `read` |

## Always-on project rules

**Compile point:** engine-sync's `codexgen` appends the project's always-on rules after the `CLAUDE.md` body and before the root adapter (the block cut from `pfm/internal/codexgen/compiler.go:120-128`), for the root and for every child project with its own `.claude/`. Order is path order. Each rule becomes a block opened by `<!-- rule: .claude/rules/{relpath} -->`, frontmatter stripped, body transformed like `CLAUDE.md`.

**Check:** `engine-sync check` compares the compiled `AGENTS.md` byte for byte (the reconcile cut from `reconcile.go:209`), so a rule edit not rebuilt reports `STALE`. OpenCode reads the same `AGENTS.md`; the OpenCode compiler needs no change.

**Budget:** Codex silently stops adding project docs past `project_doc_max_bytes`. The root `AGENTS.md` is 18417 bytes before any rule.

**Adopters:** the engine-sync plugin's `Stop` hook already builds both mirrors, so adopter rules compile with no template change.

## Path-scoped rules

### `engine-sync hook {codex|opencode}`

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

`engine-sync global` adds two engine-sync-owned entries to every target `{codex home}/hooks.json`, beside pfm's `resume-unkill` entry:

- `PreToolUse`, matcher `Bash|apply_patch` (a pattern of `[A-Za-z0-9_|]` is an exact name list), command `$HOME/.local/share/engine-sync/bin/engine-sync hook codex`. Pre, not Post: an edit receives its rule before it writes.
- `PostCompact`, command `$HOME/.local/share/engine-sync/bin/engine-sync hook codex --reset`, clearing the ledger because hook developer messages are not carried across a local compaction.

Codex runs a hook only once trusted, by a `sha256` of its event, matcher and handler. The command strings never change across engine-sync versions, so trust survives an update. pfm records trust for the fleet through `codexappendix.RegisterHookTrust` (`pfm/internal/codexappendix/trust.go:264`), as it does for `resume-unkill`; a standalone user trusts once in `/hooks`.

### OpenCode wiring

`engine-sync global` writes an engine-sync-owned shim at `~/.config/opencode/plugins/engine-sync.js`, auto-loaded from that directory, with an ownership receipt. Its `tool.execute.after` runs `{engine-sync home}/bin/engine-sync hook opencode` for `read`, `edit`, `write`, `apply_patch` and `bash`, and appends the result to `output.output`. Appending to the tool result keeps the system prompt byte-stable for prompt caching. A session-compaction event resets the session's ledger. pfm's workbench seat plugin (`pfm/internal/workbench/opencode_seat.go:15`) is the precedent for a staged OpenCode plugin file.

## Global always-on rules

### Codex: `{codex home}/AGENTS.md`

`engine-sync global` compiles every always-on rule in `~/.claude/rules/` into one block between `<!-- BEGIN engine-sync rules — owned -->` and `<!-- END engine-sync rules — owned -->` in each target Codex home's `AGENTS.md`, keeping any text outside the fence, the ownership pattern of pfm's `mergeCodexDeveloperInstructions` (`pfm/internal/installer/codex_developer_instructions.go:52`). Codex sends the global file as the first user-role message, before project docs, matching Claude's user-rules-before-project-rules order. `project_doc_max_bytes` does not cap it, it is rebuilt into the initial context after compaction, and sub-agents inherit the global instruction snapshot. A `{codex home}/AGENTS.override.md` makes Codex skip `AGENTS.md`; doctor reports that as `SHADOWED`.

### OpenCode: the `instructions` entry

`engine-sync global` compiles the same always-on set into `{engine-sync home}/rules/global-always-on.md` and registers its path in the global `instructions` array of `~/.config/opencode/opencode.jsonc` with its own receipt, beside pfm's fleet-prompt entry and through the same mechanism (`pfm/internal/installer/opencode_instructions.go:156`). OpenCode rebuilds instructions on every step and gives them to sub-agents. A glob straight at `~/.claude/rules/*.md` cannot work: it would include path-scoped rules and their raw frontmatter.

### Freshness

No project build covers `~/.claude/rules/`. Four triggers keep the compiled copies current, each running `engine-sync global`:

1. `pfm install`, for every Codex home.
2. Every pfm launch door of a Codex or OpenCode chat, writing only when bytes differ.
3. The engine-sync plugin's Claude `SessionStart` hook, for every target.
4. `engine-sync doctor` compares each compiled target with its sources and reports `STALE`, catching a session started outside the other three.

## Checks

| Check | Broken state it reports |
| --- | --- |
| `engine-sync check` | `STALE` or `MISSING` `AGENTS.md` after a rule edit (existing reconcile) |
| `engine-sync check` | `RULE-FRONTMATTER {path}: {parse error}` for a project rule whose YAML does not parse |
| `engine-sync check` | `AGENTS-BUDGET {bytes} > {limit}` when a compiled project `AGENTS.md` exceeds Codex's `project_doc_max_bytes` |
| `engine-sync doctor` | Codex hook entry `MISSING` or `UNTRUSTED` in a `{codex home}` |
| `engine-sync doctor` | Codex global block `MISSING`, `STALE` or `SHADOWED` |
| `engine-sync doctor` | OpenCode shim `MISSING` or `STALE`; `instructions` entry `MISSING` or `STALE` |
| `engine-sync doctor` | `RULE-FRONTMATTER` for a global rule |
| `engine-sync doctor` | `GLOBAL-BUDGET {bytes}` when the global always-on set exceeds 8 KiB, the cost every agent of every project pays |
| `engine-sync doctor` | injector errors logged since the last doctor run |
| `pfm doctor` | every `engine-sync doctor` row, rendered; a missing binary as a failing row |

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

Always fresh, at the cost of one more background process; launch-time refresh plus the doctor `STALE` row covers the same ground.

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
- Whether Claude sub-agents load user-level rules from `~/.claude/rules/` (the docs name only `~/.claude/CLAUDE.md` and project rules).
- The Go glob library: engine-sync's `go.mod` starts with none; the build picks and validates one that supports `**` and brace expansion.

## Open questions

- **Global `CLAUDE.md` on the same channel:** whether the owned Codex block and OpenCode entry also carry `~/.claude/CLAUDE.md`. It is empty today; unruled, so the first build carries rules only.

## First consumer

`.claude/rules/pfm-testing.md`, a symlink to `../commands/pfm-testing-manual.md`, whose frontmatter gains `paths:` for `pfm/**/*_test.go`. Local to this repo, no `templates/project/` twin. One file stays both the `/pfm-testing-manual` command and the rule; Claude ignores `name` and `description` on the rule, and the skill loaders ignore `paths`. The flights briefs keep naming the manual explicitly: the rule adds automatic delivery, the explicit read stays the protocol on every engine.

## Surfaces that stay in sync

- `docs/design/hooks/hooks.md`: the Codex hook rows and the OpenCode row ("carries none") gain engine-sync's entries and shim.
- `docs/design/engines/claude-config-dir.md`: the `rules` shared entry gains its non-Claude consumers.
- `CLAUDE.md` § Vocabulary: a `rules` entry and the engine-mirror line, through `/pcm`.
- `.claude/commands/pfm-testing-manual.md`: the `paths:` key, through `/pcm`.
- `README.md`, `docs/BLUEPRINT.md`, `docs/SETUP.md`: wherever they list what the engine mirrors compile.

## Sources

- `.professor/RR/codex-path-scoped-instruction-injection-2026-10-09.md`
- `.professor/RR/opencode-path-scoped-instruction-injection-2026-10-09.md`
- Claude Code memory and sub-agent docs: `https://code.claude.com/docs/en/memory`, `https://code.claude.com/docs/en/sub-agents`
