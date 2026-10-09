# RR — What Codex CLI 0.161.0 offers for injecting path-scoped instructions mid-session

Question: establish, from the openai/codex source code (https://github.com/openai/codex, the Rust `codex-rs` tree) plus official docs, exactly what Codex CLI 0.161.0 (installed here) offers for injecting path-scoped instructions mid-session. Questions: (1) hooks config locations, events, matchers, PreToolUse/PostToolUse stdin fields; (2) can a hook inject model-visible context, which events honour additionalContext/systemMessage, where it lands; (3) read/edit tools, names reaching matchers, apply_patch input paths; (4) hooks and AGENTS.md/developer_instructions in sub-agents; (5) SKILL.md frontmatter unknown-key handling; (6) any native path-scoped or lazy instruction loading; (7) global instruction channels, precedence, project_doc_max_bytes scope.

Source ref: every code link is pinned to commit `979011409de0a60b52f179721948e65531d26144`, which the `tracer-rr` digger cloned and resolved for tag `rust-v0.161.0`. Base: `https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/`.

## Answer

Codex 0.161.0 has no native path-scoped or lazy instruction loading. AGENTS.md discovery walks only from the project root down to cwd, and it re-runs only when the environment selection or project trust changes ([agents_md.rs:56-116](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/agents_md.rs#L56-L116), [agents_md_manager.rs:61-144](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/agents_md_manager.rs#L61-L144)). The workable way to inject is a `PreToolUse`/`PostToolUse` hook that returns `hookSpecificOutput.additionalContext`. Codex records that text as a separate `developer`-role message in history ([output_parser.rs:121-182](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/hooks/src/engine/output_parser.rs#L121-L182), [hook_additional_context.rs:1-35](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/context/hook_additional_context.rs#L1-L35)). The hook has to work out the file paths itself, because there is no read tool: files are read through `exec_command` (matched as `Bash`) and edited through `apply_patch`, whose `tool_input` is `{"command": <raw patch text>}` ([spec_plan.rs:1078-1117](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/tools/spec_plan.rs#L1078-L1117), [apply_patch.rs:414-451](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/tools/handlers/apply_patch.rs#L414-L451)). Sub-agents get the parent's hook config, so tool hooks fire for their tool calls too ([child_config.rs:109-157](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/agent/child_config.rs#L109-L157), [session.rs:1606-1624](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/session/session.rs#L1606-L1624)).

## Map

### Q1 — Hooks: where configured, events, matchers, stdin input

- **Discovery order.** Managed `requirements.toml` hooks load first. Then, for each config layer from low to high precedence, Codex reads both `{layer folder}/hooks.json` and that layer's inline `[hooks]` table, and warns when one layer has both. Plugin hook sources load last. ([discovery.rs:94-206](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/hooks/src/engine/discovery.rs#L94-L206), [load_hooks_json :339-400](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/hooks/src/engine/discovery.rs#L339-L400), [managed :208-242](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/hooks/src/engine/discovery.rs#L208-L242))
- **Layer folders.** The user layer is `~/.codex`, the parent of the user `config.toml`. The project layer is `<repo>/.codex/`. Linked worktrees use a root-checkout override. ([config/src/state.rs:225-243](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/config/src/state.rs#L225-L243))
- **Docs agree on locations.** The docs list `~/.codex/hooks.json`, `~/.codex/config.toml`, `<repo>/.codex/hooks.json` and `<repo>/.codex/config.toml`. They also say: "Project-local hooks load only when the project `.codex/` layer is trusted." ([docs hooks](https://learn.chatgpt.com/docs/hooks))
- **Plugin hooks.** "By default, Codex looks for `hooks/hooks.json` inside the plugin root." ([docs hooks](https://learn.chatgpt.com/docs/hooks))
- **Trust is checked per hook.** A non-managed hook (User, Project or SessionFlags) runs only when it is enabled and its `trusted_hash` equals its current hash, or when `bypass_hook_trust` is set. ([discovery.rs:715-730](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/hooks/src/engine/discovery.rs#L715-L730), [:796-841](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/hooks/src/engine/discovery.rs#L796-L841)) The docs say the same: "new or changed hooks are marked for review and skipped until trusted." ([docs hooks](https://learn.chatgpt.com/docs/hooks))
- **Events (12).** PreToolUse, PermissionRequest, PostToolUse, PreCompact, PostCompact, SessionStart, SessionEnd, UserPromptSubmit, SubagentStart, SubagentStop, Stop, Interrupt. ([protocol/src/protocol.rs:1577-1592](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/protocol/src/protocol.rs#L1577-L1592))
- **Matcher semantics.** ([hooks/src/engine/matcher.rs:11-41](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/hooks/src/engine/matcher.rs#L11-L41), [hooks/src/events/common.rs:112-146](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/hooks/src/events/common.rs#L112-L146))
  - An empty matcher, `"*"`, or no matcher at all matches everything.
  - A pattern made only of `[A-Za-z0-9_|]` is an exact match against the `|`-separated alternatives.
  - Anything else is an unanchored Rust `regex` `is_match`.
  - Tool events match against the canonical tool name plus its aliases.
  - UserPromptSubmit, Stop and Interrupt ignore the matcher.
- **PreToolUse/PostToolUse stdin.** The structs are `Serialize` with `deny_unknown_fields`. ([hooks/src/schema.rs:275-342](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/hooks/src/schema.rs#L275-L342))
  - Fields: `session_id`, `turn_id`, `agent_id`/`agent_type` (sub-agents only), `transcript_path` (nullable), `cwd`, `hook_event_name`, `model`, `permission_mode`, `tool_name`, `tool_input`, `tool_use_id`.
  - PostToolUse adds `tool_response`.
  - `cwd` comes from the local environment's cwd, falling back to the turn cwd. ([core/src/hook_runtime.rs:247-253](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/hook_runtime.rs#L247-L253))

### Q2 — Model-visible context from hooks

- **PreToolUse accepts `hookSpecificOutput.additionalContext` in this tag.**
  - The wire struct has the field. ([schema.rs:228-255](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/hooks/src/schema.rs#L228-L255))
  - `parse_pre_tool_use` extracts it. ([output_parser.rs:121-182](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/hooks/src/engine/output_parser.rs#L121-L182))
  - It is recorded unless the output is invalid or comes from a control-effect hook. ([hooks/src/events/pre_tool_use.rs:214-240](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/hooks/src/events/pre_tool_use.rs#L214-L240))
- **This replaces the older rejection.** Issue #19385 (on `codex-cli 0.124.0`) reported that the hook "produces: PreToolUse hook returned unsupported additionalContext". The contributor who closed it wrote: "`PreToolUse.additionalContext` is implemented across schema, parsing, context, tests, and docs." ([issue #19385](https://github.com/openai/codex/issues/19385)) That error string does not appear among the unsupported-* messages in 0.161.0. ([output_parser.rs:369-399](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/hooks/src/engine/output_parser.rs#L369-L399), [:431-500](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/hooks/src/engine/output_parser.rs#L431-L500))
- **Rejected output fields.**
  - PreToolUse rejects `continue:false`, `stopReason`, `suppressOutput`, `permissionDecision:"ask"`, `allow` without `updatedInput`, and `decision:"approve"`.
  - PostToolUse rejects only `suppressOutput` and `updatedMCPToolOutput`.
  - Every output wire struct uses `deny_unknown_fields`, so a stray key fails the JSON parse. ([output_parser.rs:207-239](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/hooks/src/engine/output_parser.rs#L207-L239))
- **Where additionalContext lands.** Each string becomes its own `developer`-role `ResponseItem::Message`, written to history through `record_conversation_items`. ([core/src/context/hook_additional_context.rs:1-35](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/context/hook_additional_context.rs#L1-L35), [hook_runtime.rs:849-873](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/hook_runtime.rs#L849-L873))
  - PreToolUse context is recorded before the tool runs. ([hook_runtime.rs:213-221](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/hook_runtime.rs#L213-L221))
  - PostToolUse context is recorded after the hooks finish and before the tool result returns. It is never appended to the tool output. ([core/src/tools/registry.rs:717-740](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/tools/registry.rs#L717-L740))
  - The docs agree for SessionStart, UserPromptSubmit and PostToolUse: "That `additionalContext` text is added as extra developer context." ([docs hooks](https://learn.chatgpt.com/docs/hooks))
- **`systemMessage` is shown to the user only.** It becomes a Warning entry in the hook-run events, and the code read never turns it into a ResponseItem. ([hooks/src/events/post_tool_use.rs:194-208](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/hooks/src/events/post_tool_use.rs#L194-L208))
- **Plain stdout.** It becomes developer context for SessionStart, UserPromptSubmit and SubagentStart, and is ignored for PreToolUse and PostToolUse. ([docs hooks](https://learn.chatgpt.com/docs/hooks))
- **Size limit.** Hook output is "roughly 2,500 tokens" by default before it spills to disk. `additionalContextLimit` overrides this per handler. ([docs hooks](https://learn.chatgpt.com/docs/hooks))

### Q3 — How Codex reads and edits files; tool names reaching matchers

- **No dedicated read tool.** Core has no `read_file`, `list_dir` or `grep_files` handler. The only `read_file` is in the `notes` extension namespace. ([core/src/tools/handlers/extension_tools.rs:85-108](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/tools/handlers/extension_tools.rs#L85-L108))
- **Default local tools.** ([core/src/tools/spec_plan.rs:1078-1117](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/tools/spec_plan.rs#L1078-L1117), [:1269-1295](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/tools/spec_plan.rs#L1269-L1295))
  - `exec_command` + `write_stdin` under `Feature::UnifiedExec`, or a one-shot `exec_command` otherwise. Both need `Feature::ShellTool`.
  - `apply_patch`, gated on `model_info.apply_patch_tool_type`.
  - `view_image`.
  - `shell_tool`, `unified_exec` and `view_image` are Stable with `default_enabled: true`. ([features/src/lib.rs:987-989](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/features/src/lib.rs#L987-L989), [:993-995](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/features/src/lib.rs#L993-L995), [:1011-1013](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/features/src/lib.rs#L1011-L1013))
- **Names that reach matchers.**
  - `exec_command`/unified exec appear as `Bash`. ([core/src/tools/handlers/unified_exec.rs:85-98](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/tools/handlers/unified_exec.rs#L85-L98))
  - `apply_patch` appears as `apply_patch`, with aliases `Write` and `Edit`. ([core/src/tools/hook_names.rs:28-66](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/tools/hook_names.rs#L28-L66), [registry.rs:833-850](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/tools/registry.rs#L833-L850))
  - `spawn_agent` appears as `spawn_agent`, with alias `Agent` (same sources).
  - MCP tools appear as `mcp__{server}__{tool}`. ([core/src/tools/handlers/mcp.rs:116](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/tools/handlers/mcp.rs#L116-L116))
  - The docs agree: shell and `exec_command` "Match as `Bash`"; `apply_patch` "Match as `apply_patch`, `Edit`, or `Write`"; `WebSearch` and other hosted tools "don't use the local function-tool hook path". ([docs hooks](https://learn.chatgpt.com/docs/hooks))
- **apply_patch input.** `tool_input` is `serde_json::json!({ "command": command })`: the raw patch text, with no parsed file paths. PostToolUse uses the same shape. A path-scoped hook therefore has to parse the `*** Update File:` / `*** Add File:` headers itself. For Bash, it would have to parse the shell command string. ([apply_patch.rs:414-451](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/tools/handlers/apply_patch.rs#L414-L451))

### Q4 — Sub-agents

- **Tool hooks fire in sub-agents.**
  - A spawned sub-agent is a new Session, and every Session builds its own `Hooks` through `build_hooks_config`. ([session.rs:1606-1624](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/session/session.rs#L1606-L1624))
  - The child config is a clone of the parent's effective config, so the child has the same hook sources. ([core/src/agent/child_config.rs:109-157](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/agent/child_config.rs#L109-L157))
  - The hook payload gains `agent_id`/`agent_type`. ([hook_runtime.rs:1042-1061](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/hook_runtime.rs#L1042-L1061))
  - PreToolUse/PostToolUse dispatch has no sub-agent check, while SessionStart and Stop skip sub-agents (`SessionSource::SubAgent(_) => return false`). ([hook_runtime.rs via raw tag](https://raw.githubusercontent.com/openai/codex/rust-v0.161.0/codex-rs/core/src/hook_runtime.rs))
  - The docs: "Subagent hooks use the parent session id"; `Interrupt` "doesn't run for subagents"; `SessionEnd` "won't run for subagents". ([docs hooks](https://learn.chatgpt.com/docs/hooks))
  - The subagents docs page says nothing about hooks. ([docs subagents](https://learn.chatgpt.com/docs/agent-configuration/subagents))
- **developer_instructions.** The child inherits the parent turn's `developer_instructions`. Under multi-agent V2, `subagent_developer_instructions` replaces them, and so does a role's `developer_instructions` override. ([core/src/agent/role.rs:177-189](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/agent/role.rs#L177-L189)) The docs: "Every standalone custom agent file must define:" `name`, `description`, `developer_instructions`. ([docs subagents](https://learn.chatgpt.com/docs/agent-configuration/subagents))
- **AGENTS.md.** The child inherits the parent's global and thread instruction snapshot, and its own `AgentsMdManager` re-discovers project docs. ([agents_md_manager.rs:150-162](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/agents_md_manager.rs#L150-L162), [session.rs:1477](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/session/session.rs#L1477-L1477)) The docs page does not say whether sub-agents load AGENTS.md. ([docs subagents](https://learn.chatgpt.com/docs/agent-configuration/subagents))

### Q5 — SKILL.md frontmatter parsing

- **Unknown keys are ignored.** `SkillFrontmatter` has only `name`, `description` and `metadata.short-description`. It derives `Deserialize` without `deny_unknown_fields`, so a `paths:` YAML list is dropped silently, with no warning and no error. ([skills/src/parser.rs:6-28](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/skills/src/parser.rs#L6-L28), [:43-80](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/skills/src/parser.rs#L43-L80))
- **No path trigger.** No path or glob field exists in the parser, and `paths` does not appear in `skills/src/loading.rs`.
- **Docs.**
  - "The `SKILL.md` file must include `name` and `description`."
  - Selection is by description only: "Codex can choose a skill when your task matches the skill `description`."
  - "Codex scans `.agents/skills` in every directory from your current working directory up to the repository root."
  - `agents/openai.yaml` has "`allow_implicit_invocation` (default: `true`)".
  - ([docs build-skills](https://learn.chatgpt.com/docs/build-skills))

### Q6 — Native path-scoped or lazy instruction loading

- **None exists. The docs' startup-only claim mostly holds.**
  - `agents_md_paths` finds the nearest ancestor with a project-root marker (default `.git`) and walks root→cwd. In each directory it takes the first of `AGENTS.override.md`, `AGENTS.md`, then the fallback filenames.
  - Untrusted projects get no project docs.
  - Nothing below cwd is read. ([core/src/agents_md.rs:56-116](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/agents_md.rs#L56-L116), [:189-296](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/agents_md.rs#L189-L296))
- **One refinement to "startup only".**
  - The only caller is `AgentsMdManager::refresh`, which runs at session start ([session.rs:1503](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/session/session.rs#L1503-L1503)) and again at each step ([session/mod.rs:3738-3749](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/session/mod.rs#L3738-L3749)).
  - It re-discovers only when the environment selections or the project trust level change, and uses its cache otherwise. ([core/src/agents_md_manager.rs:61-144](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/agents_md_manager.rs#L61-L144))
  - Reading or editing a file never triggers it.
- **Docs.** "Codex builds an instruction chain when it starts (once per run; in the TUI this usually means once per launched session)." "Codex stops searching once it reaches your current directory". ([docs agents-md](https://learn.chatgpt.com/docs/agent-configuration/agents-md))

### Q7 — Global instruction channels

- **Global file.** `{codex_home}/AGENTS.override.md`, else `AGENTS.md`, loaded as user instructions. ([codex-home/src/instructions/mod.rs:12-60](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/codex-home/src/instructions/mod.rs#L12-L60)) The docs: "Codex uses only the first non-empty file at this level." ([docs agents-md](https://learn.chatgpt.com/docs/agent-configuration/agents-md))
- **Combined order.** User instructions, then thread instructions, then project entries, with `--- project-doc ---` before the first project entry. ([agents_md.rs:386-419](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/agents_md.rs#L386-L419))
- **Message roles.**
  - The combined AGENTS.md text is a `"user"`-role message placed after the developer message(s). ([core/src/context/user_instructions.rs:15-17](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/context/user_instructions.rs#L15-L17), [session/mod.rs:4386-4410](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/session/mod.rs#L4386-L4410))
  - `developer_instructions` is a section of the first `developer` message. ([session/mod.rs:4213-4223](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/session/mod.rs#L4213-L4223))
  - The docs: "Additional developer instructions injected into the session (optional)." ([docs config-reference](https://learn.chatgpt.com/docs/config-file/config-reference))
- **model_instructions_file replaces the base (system) prompt, not AGENTS.md.** Precedence is `base_instructions` override, then `model_instructions_file`, then the model's built-in `instructions`. ([core/src/config/mod.rs:3999-4015](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/config/mod.rs#L3999-L4015)) The config source comment says it overrides "the built-in instructions for the selected model. Users are STRONGLY DISCOURAGED from using this field." ([config_toml.rs @ tag](https://raw.githubusercontent.com/openai/codex/rust-v0.161.0/codex-rs/config/src/config_toml.rs))
  - DISPUTED wording. The docs call it "Replacement for built-in instructions instead of `AGENTS.md`." ([docs config-reference](https://learn.chatgpt.com/docs/config-file/config-reference)) The code path shows it replacing base instructions, while the AGENTS.md user message is assembled separately.
- **experimental_instructions_file.** It has zero hits in codex-rs at this commit (tracer-rr grep). The docs' rename note ("Rename `experimental_instructions_file` to `model_instructions_file`") is `unquoted` (see Verification).
- **project_doc_max_bytes.** The default is `32 * 1024` ("pub const DEFAULT_PROJECT_DOC_MAX_BYTES: usize = 32 * 1024;", [config_toml.rs @ tag](https://raw.githubusercontent.com/openai/codex/rust-v0.161.0/codex-rs/config/src/config_toml.rs)).
  - It caps only project-doc discovery, not the global `~/.codex/AGENTS.md`. ([agents_md.rs:68](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/agents_md.rs#L56-L116))
  - The source comment calls it "Maximum total bytes of project instruction content across all selected environments" (combined, not per file).
  - The docs say "Maximum bytes read from `AGENTS.md` when building project instructions." ([docs config-reference](https://learn.chatgpt.com/docs/config-file/config-reference)) The agents-md guide says Codex "stops adding files once the combined size reaches the limit defined by `project_doc_max_bytes` (32 KiB by default)." ([docs agents-md](https://learn.chatgpt.com/docs/agent-configuration/agents-md))
- **Thread instructions** have a separate cap of 10,000 estimated tokens. ([agents_md_manager.rs:165-178](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/agents_md_manager.rs#L165-L178))

### Implication for reproducing `.claude/rules/*.md` `paths:` (inference, not a source fact)

This is my inference from the sources above. Two hooks could do it:
- A `PreToolUse` hook matching `apply_patch` would parse the patch headers.
- A hook matching `Bash` would heuristically parse the command for file arguments.

When a path matches a rule's glob, the hook returns `hookSpecificOutput.additionalContext` with the rule body. Codex records that as a developer message before the tool runs. The hook would have to dedupe through its own state file keyed by `session_id` (plus `agent_id` for sub-agents) to get "first time only". Skills cannot carry this, because `paths:` is silently ignored.

## Coverage

- A Hooks config/events/input — settled
- B Hook context injection — settled (whether context survives compaction is out of the plan line; see open rabbit holes)
- C Read/edit tools and matcher names — settled
- D Sub-agents — settled
- E Skills frontmatter — settled
- F Lazy/path-scoped instructions — settled
- G Global channels — settled

Digging ended: every sub-area settled after round 1.

tracer-rr result file: `tracer-rr/openai-codex-2026-10-09.md`

## Verification

16 web facts were checked on 4 pages (config-reference, build-skills, subagents, issue #19385). The `tracer-rr` code facts count as fetched and were not re-checked. The hooks and agents-md doc quotes come from my own step-1 reads.

- NOT ON PAGE: the docs digger's wording that `project_doc_max_bytes` covers "how much to read from each `AGENTS.md` file". The page actually says "Maximum bytes read from `AGENTS.md` when building project instructions.", and that is the quote used above.
- NOT ON PAGE: a PR or release named in issue #19385 as shipping PreToolUse additionalContext.
- UNCHECKED — config-reference page truncated at 100,000 of 158,266 characters: the `experimental_instructions_file` rename note (the digger sourced it from config-advanced; marked `unquoted`).

## Open rabbit holes

- Whether hook-injected developer messages survive local compaction. Source suggests compaction keeps only the initial context, the summary and user messages ([compact.rs:566-582](https://github.com/openai/codex/blob/979011409de0a60b52f179721948e65531d26144/codex-rs/core/src/compact.rs#L566-L582)), so hook developer messages would likely be dropped and a dedupe ledger would need resetting on PostCompact. dug, unsettled (inference only).
- The PR or release that added PreToolUse additionalContext between 0.124.0 and 0.161.0. dug, unsettled.
- Whether the `experimental_instructions_file` legacy key still parses in 0.161.0. Zero hits in codex-rs suggests it was removed. dug, unsettled.
- Whether `developer_instructions` reaches App-initiated threads (openai/codex issue 11004). undug.
- How the sub-agent `AgentsMdManager` resolves cwd when a role sets a different working directory. undug.
