# RR — What OpenCode 1.18.31 offers for injecting path-scoped instructions mid-session

Question: establish, from the OpenCode source code (https://github.com/sst/opencode, also published as anomalyco/opencode — the `packages/opencode` TypeScript tree) plus official docs (opencode.ai/docs), exactly what OpenCode 1.18.31 offers for injecting path-scoped instructions mid-session — plugin hooks and load paths; whether tool.execute.after can mutate model-visible output or a plugin can inject system/synthetic messages; built-in tool names and path args; whether hooks fire in sub-agent sessions and sub-agents get AGENTS.md/instructions; native path-scoped loading and Claude Code compat paths/flags; `instructions` field resolution; command/skill frontmatter tolerance of unknown keys like `paths:`.

## Answer

OpenCode 1.18.31 already scopes instructions by path natively, but only for AGENTS.md, CLAUDE.md and CONTEXT.md files. The `read` tool walks up from the file it reads and appends any such file not yet loaded to its output as a system-reminder block ([instruction.ts:171-221](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/session/instruction.ts#L171-L221), [read.ts:355-368](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/tool/read.ts#L355-L368)). Nothing reads `.claude/rules/` ([tracer-rr grep](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/skill/index.ts#L183-L227)).

A plugin can reproduce `paths:` rules in either of two ways:
- **`tool.execute.after`:** a change to `output.output` on a built-in tool reaches the model's tool_result ([tools.ts:99-134](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/session/tools.ts#L99-L134), [message-v2.ts:290-296](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/session/message-v2.ts#L290-L296)).
- **`experimental.chat.system.transform` and `experimental.chat.messages.transform`:** both run on every LLM step ([prompt.ts:1255-1286](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/session/prompt.ts#L1255-L1286)).

Either way works in sub-agent sessions too, because the `task` tool runs its child session through the same prompt loop ([task.ts:197-212](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/tool/task.ts#L197-L212)).

## Map

All code links are pinned to tag v1.18.31 = commit `014614d35b397775e5d397a490fc72368c894ec2` (tracer-rr, read from clone).

### Q1 — Plugin hooks and load locations
- **The `Hooks` interface** ([index.ts:222-335](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/plugin/src/index.ts#L222-L335)) declares:
  - `dispose`, `event`, `config`, `tool`, `auth`, `provider`
  - `chat.message`, `chat.params`, `chat.headers`, `permission.ask`
  - `command.execute.before`, `tool.execute.before`, `shell.env`, `tool.execute.after`
  - `experimental.chat.messages.transform`, `experimental.chat.system.transform`, `experimental.provider.small_model`
  - `experimental.session.compacting`, `experimental.compaction.autocontinue`, `experimental.text.complete`
  - `tool.definition`
- **Hook signatures:**
  - `tool.execute.after`: input `{tool, sessionID, callID, args}`, output `{title, output, metadata}`.
  - `experimental.chat.messages.transform`: input `{}`, output `{messages: {info, parts}[]}`.
  - `experimental.chat.system.transform`: input `{sessionID?, model}`, output `{system: string[]}`.
  - Every (input, output) hook mutates `output` in place, and `trigger` runs the hooks in sequence on the same object ([plugin/index.ts:284-297](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/plugin/index.ts#L284-L297)).
- **Where plugins load from:**
  - The `plugin` config array. Path specs resolve relative to the config file that declared them.
  - Auto-discovery of `{plugin,plugins}/*.{ts,js}` (top level only) in every config directory ([config/plugin.ts:18-77](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/config/plugin.ts#L18-L77)).
  - The config directories, in order: `~/.config/opencode`, each `.opencode` from the project directory up to the worktree, `~/.opencode`, then `OPENCODE_CONFIG_DIR` ([paths.ts:23-41](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/config/paths.ts#L23-L41)).
  - Docs: "`.opencode/plugins/` - Project-level plugins", "`~/.config/opencode/plugins/` - Global plugins", "Files in these directories are automatically loaded at startup." ([docs/plugins](https://opencode.ai/docs/plugins/), unquoted in step 6)
- **Load order:** internal plugins load first unless OPENCODE_DISABLE_DEFAULT_PLUGINS is set. External ones load after, and OPENCODE_PURE disables them ([plugin/index.ts:170-188](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/plugin/index.ts#L170-L188)). External plugins import in parallel but stay in config order, and on duplicates the last declaration wins ([loader.ts:208-236](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/plugin/loader.ts#L208-L236)).
- **Docs vs. code:** the docs list `tool.execute.after` only as an event name, with no signature ([docs/plugins](https://opencode.ai/docs/plugins/)). The transform hooks are undocumented ([issue #33025](https://github.com/anomalyco/opencode/issues/33025)).

### Q2 — Output mutation and mid-session injection
- **Built-in tools:** `tool.execute.after` receives the same object that `execute` returns ([tools.ts:99-134](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/session/tools.ts#L99-L134)). The processor stores `value.result.value.output` as `part.state.output` ([processor.ts:257-275](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/session/processor.ts#L257-L275)), and that is what goes to the model ([message-v2.ts:290-296](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/session/message-v2.ts#L290-L296)). So appending to `output.output` reaches the model and is persisted.
- **MCP tools:** the hook gets the raw MCP result, and the model text is rebuilt from `result.content` after the hook. Setting `output.output` has no effect; mutating `result.content` works ([tools.ts:398-430](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/session/tools.ts#L398-L430)). Issue history: [#3384](https://github.com/anomalyco/opencode/issues/3384) and fix [PR #3381](https://github.com/anomalyco/opencode/pull/3381), "moves the output extraction to after the hook is triggered" (unquoted in step 6). [#25918](https://github.com/anomalyco/opencode/issues/25918), on 1.14.39, says MCP output "ignores any mutations".
- **Issue #25918's other claim:** it says the hook is never invoked. That does not hold at this tag: the hook is called on every tool path, the subtask path included ([prompt.ts:389-411](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/session/prompt.ts#L389-L411)). The exception is code-mode, which is unexplored (see rabbit holes).
- **`chat.message`:** fires once per user message, before it is saved ([prompt.ts:999-1009](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/session/prompt.ts#L999-L1009)). Parts a plugin adds or edits are persisted ([prompt.ts:1046-1049](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/session/prompt.ts#L1046-L1049)).
- **The two transforms, per LLM step:** messages are reloaded from the database each step. `messages.transform` mutates that in-memory copy, which is not persisted, and then `instruction.system()` is rebuilt ([prompt.ts:1255-1286](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/session/prompt.ts#L1255-L1286)). The `system` array goes through `system.transform` and is sent as `role: "system"` messages ([request.ts:56-78](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/session/llm/request.ts#L56-L78), [request.ts:101-112](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/session/llm/request.ts#L101-L112)). Issue [#33025](https://github.com/anomalyco/opencode/issues/33025) claims that reassigning `output.system = [...]` is a silent no-op, so mutate the array in place. This claim is unconfirmed by a maintainer.
- **SDK client injection:** a plugin can add persisted synthetic text with `noReply`, a `system` string and `synthetic` text parts ([prompt.ts:1499-1515](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/session/prompt.ts#L1499-L1515), [schema session.ts:397-410](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/schema/src/v1/session.ts#L397-L410)). Core uses the same pattern for background-task results ([task.ts:227-250](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/tool/task.ts#L227-L250)).
- **Prior art:** the community plugin [frap129/opencode-rules](https://github.com/frap129/opencode-rules) records file observations in `tool.execute.after` and attaches rules to user messages "so the system prompt stays byte-stable for prompt caching" (unquoted in step 6).

### Q3 — Built-in tool ids and path arguments
| Tool | Path-related arguments |
|---|---|
| `read` | `filePath` ([read.ts:28-36](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/tool/read.ts#L28-L36)) |
| `edit` | `filePath`, `oldString`, `newString` |
| `write` | `filePath`, `content` |
| `apply_patch` | `patchText` |
| `glob` | `pattern`, `path` |
| `grep` | `pattern`, `path`, `include` |
| `bash` | `command`, `workdir` ([shell/prompt.ts:16-25](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/tool/shell/prompt.ts#L16-L25)) |
| `lsp` | `filePath` |

- The other built-ins are `task`, `webfetch`, `websearch`, `todowrite`, `skill`, `question` and `plan_exit`.
- There is no `patch`, `multiedit` or `list` tool at this tag.
- `apply_patch` replaces `edit`/`write` only for `gpt-` models other than oss and gpt-4 ([registry.ts:291-303](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/tool/registry.ts#L291-L303)). A plugin must therefore parse file paths out of `patchText` for those models.

### Q4 — Sub-agents
- **Hooks fire in child sessions.** The `task` tool creates a child session with `parentID` ([task.ts:156-172](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/tool/task.ts#L156-L172)) and runs it through `ops.prompt` ([task.ts:197-212](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/tool/task.ts#L197-L212)). That is the same `SessionPrompt.prompt` main sessions use ([prompt.ts:144-150](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/session/prompt.ts#L144-L150)). The tool context sets `sessionID: input.session.id` ([tools.ts:60-66](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/session/tools.ts#L60-L66)), so every chat, tool and transform hook fires with the child's sessionID.
- **Children get the same instructions.** The child's system prompt carries the same AGENTS.md/CLAUDE.md and `instructions`, because `instruction.system()` is instance-wide (tracer-rr reading of prompt.ts:1255-1286). Nested AGENTS.md attachment is tracked per session, so a child re-attaches files its parent already loaded.
- **Older contrary report:** issue [#16148](https://github.com/anomalyco/opencode/issues/16148), on 1.2.17, reported a sub-agent not reading AGENTS.md. The code at this tag supersedes it.

### Q5 — Native path-scoped loading and Claude Code compatibility
- **On read:** `read` calls `Instruction.resolve` ([read.ts:300](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/tool/read.ts#L300-L300)). It walks from the read file's directory up to, but not including, the project root, and attaches each AGENTS.md/CLAUDE.md/CONTEXT.md not already loaded ([instruction.ts:171-221](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/session/instruction.ts#L171-L221)). The content is appended to the read output as a system-reminder block ([read.ts:355-368](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/tool/read.ts#L355-L368)). Only `read` triggers this; `edit` and `write` do not, per the tracer-rr findings.
- **At session start:**
  - Global: `~/.config/opencode/AGENTS.md` is checked first, then `~/.claude/CLAUDE.md`, and the first that exists wins.
  - Project: the first filename among AGENTS.md, CLAUDE.md and CONTEXT.md that `findUp` finds from the directory to the worktree wins, together with its ancestor copies ([instruction.ts:110-169](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/session/instruction.ts#L110-L169)).
- **Flags** live in `effect/runtime-flags.ts`, not `flag/flag.ts`:
  - OPENCODE_DISABLE_CLAUDE_CODE or OPENCODE_DISABLE_CLAUDE_CODE_PROMPT turns off the CLAUDE.md files.
  - OPENCODE_DISABLE_CLAUDE_CODE or OPENCODE_DISABLE_CLAUDE_CODE_SKILLS turns off `.claude` skills ([runtime-flags.ts:16-30](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/effect/runtime-flags.ts#L16-L30)).
  - Docs: `OPENCODE_DISABLE_CLAUDE_CODE=1` "Disable all .claude support" ([docs/rules](https://opencode.ai/docs/rules/)).
- **`.claude` skills:** unless disabled, `.claude/skills/**/SKILL.md` is scanned in `~/.claude` and in every `.claude` from the project directory up to the worktree ([skill/index.ts:183-227](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/skill/index.ts#L183-L227)). Docs: "Project Claude-compatible: `.claude/skills/<name>/SKILL.md`" and "Global Claude-compatible: `~/.claude/skills/<name>/SKILL.md`" ([docs/skills](https://opencode.ai/docs/skills/), confirmed).
- **No other `.claude` paths:** nothing reads `.claude/rules/`, `.claude/agents` or `.claude/commands`. A tracer-rr grep for `.claude` in `*.ts` under `packages/` found only skill/index.ts:21, instruction.ts:62 and the TUI's `~/.claude/ide`.

### Q6 — The `instructions` field
- **Resolution** ([instruction.ts:60-89](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/session/instruction.ts#L60-L89)):
  - URLs are fetched with a 5 s timeout.
  - A leading `~/` expands to home: `raw.startsWith("~/") ? path.join(global.home, raw.slice(2)) : raw`.
  - An absolute path is globbed by basename in its directory.
  - A relative entry is searched upward from the project directory to the worktree with `fs.globUp(instruction, ctx.directory, ctx.worktree)`, not from the config file's directory. So a relative glob in the global config resolves against each project.
- **Timing and reach:** the entries are part of `instruction.system()`, which is rebuilt every LLM step and reaches sub-agents (Q4). They are always-on, never path-triggered.
- **Global file name:** the code looks for `["opencode.jsonc", "opencode.json", "config.json"]`, so a global `opencode.jsonc` is accepted (sub-rr reading of the v1.18.31 source; unquoted in step 6).
- **Docs:** the `instructions` field "takes an array of paths and glob patterns to instruction files" ([docs/config](https://opencode.ai/docs/config/), unquoted in step 6). The docs say nothing about relative paths or `~` ([docs/rules](https://opencode.ai/docs/rules/)).

### Q7 — Frontmatter tolerance
- **Parser:** commands, agents and skills all use gray-matter, retried once with a sanitizer for unquoted `key: a:b` values, so a YAML list under `paths:` parses ([markdown.ts:1-36](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/core/src/config/markdown.ts#L1-L36)).
- **Skills:** only `name` (string) and `description` (string or absent) are checked; other keys are dropped silently ([skill/index.ts:53-59](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/skill/index.ts#L53-L59), [105-140](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/opencode/src/skill/index.ts#L105-L140)). Docs: "Unknown frontmatter fields are ignored." ([docs/skills](https://opencode.ai/docs/skills/), confirmed).
- **Agents:** the schema is `StructWithRest` with an any-key rest, and unknown keys are moved into `options` with no warning, so `paths:` becomes `options.paths` ([core agent.ts:12-89](https://github.com/sst/opencode/blob/014614d35b397775e5d397a490fc72368c894ec2/packages/core/src/v1/config/agent.ts#L12-L89)). The docs say unknown agent keys are passed to the provider as model options, so a `paths:` key on an agent could reach the provider call (UNVERIFIED for the markdown path).
- **Commands:** decoded with `const decodeInfo = Schema.decodeUnknownExit(ConfigCommandV1.Info)` and called as `decodeInfo(config, { errors: "all", propertyOrder: "original" })`, with no `onExcessProperty`. A failed markdown parse is skipped silently: `const md = await ConfigMarkdown.parse(item).catch(() => undefined)` then `if (!md) continue` ([command.ts](https://raw.githubusercontent.com/sst/opencode/v1.18.31/packages/opencode/src/config/command.ts), confirmed). Schema is imported with `import { Cause, Exit, Schema } from "effect"`.
- **Effect's default for unknown keys:** "By default, any properties not defined in the schema are removed from the output when parsing a value", and the "`onExcessProperty` option (default value: `"ignore"`)" ([Effect v3 docs](https://effect.website/docs/v3/schema/getting-started), confirmed). So an unknown command key like `paths:` is most likely stripped with no warning. This is not fully settled: which Effect major OpenCode pins was not checked, and the Effect 4 default (`@default "ignore"` in SchemaAST.ts) was seen only through a fetch summary.

## Coverage
- A. Plugin hooks and loading (Q1): settled
- B. Output mutation and injection (Q2): settled
- C. Built-in tool names and path arguments (Q3): settled
- D. Sub-agents (Q4): settled
- E. Native path-scoped loading and Claude Code compatibility (Q5): settled
- F. The `instructions` field (Q6): settled
- G. Frontmatter tolerance (Q7): partial. The excess-key default for commands rests on the Effect v3 docs; OpenCode's Effect major and the v4 default are unverified.
- tracer-rr result: `tracer-rr/sst-opencode-2026-10-09.md`

Digging ended: round 2 settled nothing and added no sub-area.

## Verification
- **Checked:** 10 facts on 3 pages (docs/skills ×4, Effect v3 docs ×2, v1.18.31 command.ts ×4). All 10 were confirmed.
- **NOT ON PAGE:** none.
- **UNCHECKED:** none.
- **Not re-fetched:** the tracer-rr code facts carry links pinned to the commit and count as fetched.
- **Still unquoted:** the docs-plugins load-dir quotes (read by the lead in step 1), PR #3381, #25918, #33025, opencode-rules, the docs-config `instructions` quote and the `opencode.jsonc` lookup.

## Open rabbit holes
- Issue #25918 against code-mode: whether `tool.execute.after` is skipped under experimentalCodeMode (tools.ts:388, tool/code-mode.ts:181). undug
- Which Effect major OpenCode 1.18.31 pins (package.json), and the Effect 4 `onExcessProperty` default from raw SchemaAST.ts. dug, unsettled
- Issue #33025: whether reassigning `output.system`/`output.messages` (rather than mutating in place) is a silent no-op. dug, unsettled
- Issue #17637: the user message is not passed to `experimental.chat.system.transform`, which matters for prompt-keyed injection. undug (snippet only)
- Issue #51262: OpenCode v2 config reportedly drops `instructions` entries, a migration risk. undug (snippet only)
- Whether compaction drops content injected through `tool.execute.after` or synthetic parts, and whether nested AGENTS.md re-attaches after compaction. undug
- The PR #3381 MCP fix's release vs the #25918 regression report: DISPUTED historically. At v1.18.31 the code shows MCP text is rebuilt from `result.content` after the hook. dug, unsettled
