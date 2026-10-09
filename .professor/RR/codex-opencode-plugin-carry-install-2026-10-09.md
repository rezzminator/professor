# RR — What Codex CLI and OpenCode plugins can carry and how they install (Oct 2026), for one engine-sync project shipping native plugins to Claude Code, Codex and OpenCode

Question: what can Codex CLI plugins and OpenCode plugins carry and how are they installed, as of October 2026 — to decide whether one "engine sync" project can ship an engine-native plugin for Claude Code, Codex and OpenCode each, with the Codex and OpenCode halves running inside their own engine.

## Answer

Yes, one repo can ship all three. A Codex plugin carries skills, MCP servers, apps and hooks, and commands only by being converted into skills at install time. It cannot carry agent roles or AGENTS.md-style instructions ([manifest.rs](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/core-plugins/src/manifest.rs#L380-L396), [command_migration](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/core-plugins/src/command_migration/plugin.rs#L16-L53), [agent-roles/loader.rs](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/agent-roles/src/loader.rs#L75-L82), [issue #36855](https://github.com/openai/codex/issues/36855)). Its hooks are plain `$SHELL -lc` commands with no runtime guaranteed, and they run only once the user trusts them ([command_runner.rs](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/hooks/src/engine/command_runner.rs#L384-L461), [discovery.rs](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/hooks/src/engine/discovery.rs#L715-L735)). An OpenCode plugin is a JS/TS module imported into OpenCode's own Bun process ([loader.ts](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/plugin/loader.ts#L123-L145)). Its V1 `config` hook edits the live config before anything reads it, so it can add agents, commands, instruction files, MCP servers and skill paths ([bootstrap.ts](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/project/bootstrap.ts#L32-L46), [instance-store.ts](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/project/instance-store.ts#L45-L62)). The V2 API is separate and never calls that hook ([external.ts](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/core/src/config/plugin/external.ts#L15-L30)). obra/superpowers already ships one repo to all three engines, with one OpenCode file that covers both V1 and V2 ([superpowers.js](https://github.com/obra/superpowers/blob/8ca22dba9a94f28898bbce59f2537ff4d87c747d/.opencode/plugins/superpowers.js#L219-L266)).

## Map

### A. Codex plugin manifest and components
- **Manifest lookup order:**
  - A root `plugin.json` wins only when its `$schema` is `https://agent-plugins.org/schemas/`.
  - Otherwise Codex takes the first of `.codex-plugin/plugin.json`, `.claude-plugin/plugin.json` and `.cursor-plugin/plugin.json` ([protocol.rs#L51-L55](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/exec-server-protocol/src/protocol.rs#L51-L55), [plugin_namespace.rs#L43-L81](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/utils/plugins/src/plugin_namespace.rs#L43-L81)).
  - The docs call `.codex-plugin/plugin.json` "a compatibility fallback" and recommend the root `plugin.json` ([Package your plugin](https://developers.openai.com/plugins/build/plugins)).
- **Legacy manifest fields:** `name, version, description, keywords, skills, mcpServers, apps, hooks, interface, extensions`, plus a separate `commands` read ([manifest.rs#L47-L83](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/core-plugins/src/manifest.rs#L47-L83)).
- **What a plugin carries:** skills, onboarding_skill, mcp_servers, apps and hooks ([manifest.rs#L380-L396](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/core-plugins/src/manifest.rs#L380-L396)). The default paths are `skills/`, `hooks/hooks.json`, `.mcp.json` and `.app.json` ([loader.rs#L71-L74](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/core-plugins/src/loader.rs#L71-L74)).
- **Commands:** converted into skills at install time, under `.codex-plugin/migrated-command-skills`. Only `.md` files with a frontmatter description and at most 4,000 bytes are converted, and never for Agent Plugins manifests ([command_migration/plugin.rs#L16-L53](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/core-plugins/src/command_migration/plugin.rs#L16-L53), [store.rs#L639-L646](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/core-plugins/src/store.rs#L639-L646)).
- **Agent roles are absent:**
  - Roles come only from a config layer's `agents/*.toml` ([agent-roles/loader.rs#L75-L82](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/agent-roles/src/loader.rs#L75-L82)).
  - Issue #36855, opened Aug 4, 2026 and still open, says: "Plugins can ship skills, MCP servers, apps, and hooks, but not agent roles." ([issue #36855](https://github.com/openai/codex/issues/36855)).
- **Instructions are absent:** no AGENTS.md-style instruction component appears in the manifest struct (same manifest.rs lines).
- **Root Agent Plugins manifest:**
  - It carries only metadata fields ([agent_plugin_manifest.rs#L17-L28](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/core-plugins/src/agent_plugin_manifest.rs#L17-L28)).
  - Codex hard-codes `./skills` and `./mcp.json` for it. Hooks and the onboarding skill come only from `extensions."com.openai"` or a `.codex-plugin/plugin.json` overlay ([agent_plugin_manifest.rs#L166-L208](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/core-plugins/src/agent_plugin_manifest.rs#L166-L208)).
  - Apps are dropped ([loader.rs#L1149-L1157](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/core-plugins/src/loader.rs#L1149-L1157)).
- **The spec itself:** "Agent Plugins v1 defines exactly two component types: skills and MCP servers." Commands, hooks, agents, rules and LSP servers "remain too client-specific" ([agent-plugins.org spec](https://agent-plugins.org/specification)). Its backers are named as Amazon, Cursor, Microsoft, OpenAI and Vercel ([AAIF post](https://aaif.io/blog/from-skills-and-tools-to-portable-agent-plugins)).

### B. Codex install, disk and trust
- **CLI:**
  - `codex plugin {add, list, marketplace, remove}` and `codex plugin marketplace {add, list, upgrade, remove}` ([plugin_cmd.rs#L59-L77](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/cli/src/plugin_cmd.rs#L59-L77)).
  - `marketplace add` takes a local path, `owner/repo[@ref]` or an HTTPS/SSH Git URL, with `--ref` and `--sparse` ([marketplace_cmd.rs#L43-L82](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/cli/src/marketplace_cmd.rs#L43-L82)).
- **Marketplace entry sources:** a `./` path, or an object of type `local`, `url`, `git-subdir` or `npm` ([marketplace.rs#L1035-L1070](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/core-plugins/src/marketplace.rs#L1035-L1070)).
  - For npm, "Codex downloads the package without running lifecycle scripts" ([Package your plugin](https://developers.openai.com/plugins/build/plugins)).
- **Marketplace file locations:** `.agents/plugins/marketplace.json` at repo and personal scope ([Package your plugin](https://developers.openai.com/plugins/build/plugins)).
- **On disk:**
  - Installs go to `$CODEX_HOME/plugins/cache/{marketplace}/{plugin}/{version}`, and data to `plugins/data/{plugin}-{marketplace}` ([store.rs#L86-L127](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/core-plugins/src/store.rs#L86-L127)).
  - The version comes from the manifest. With no manifest version it is `local` ([installed.rs#L10-L11](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/core-plugin-common/src/installed.rs#L10-L11)), or `1.0.0` for Agent Plugins manifests ([store.rs#L425-L449](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/core-plugins/src/store.rs#L425-L449)).
- **Enabling:** `[plugins."{plugin}@{marketplace}"] enabled` in config.toml ([types.rs#L1050-L1058](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/config/src/types.rs#L1050-L1058), [plugin_edit.rs#L90-L103](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/config/src/plugin_edit.rs#L90-L103)).
- **Hook trust gate:**
  - A plugin hook runs only when the plugin is enabled and the hook is Managed or Trusted, or `bypass_hook_trust` is set ([discovery.rs#L715-L735](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/hooks/src/engine/discovery.rs#L715-L735)).
  - Trust is a stored `sha256:` hash of the normalized event, matcher and handler, so changing a hook brings back the review ([discovery.rs#L768-L823](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/hooks/src/engine/discovery.rs#L768-L823)).
  - `codex exec` goes through the same gate and silently leaves out untrusted hooks unless `--dangerously-bypass-hook-trust` is passed ([exec/cli.rs#L164-L168](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/exec/src/cli.rs#L164-L168)).
  - The docs agree: "Installing or enabling a plugin doesn't automatically trust its hooks", and "Use `/hooks` in the CLI to inspect hook sources, review new or changed hooks, trust hooks" ([Codex hooks](https://learn.chatgpt.com/docs/hooks)).
- **Plugin hooks in the CLI versus desktop:** the docs and the code read differently. The hooks page says "When a plugin is enabled, Codex can load lifecycle hooks from that plugin alongside user, project, and managed hooks." ([Codex hooks](https://learn.chatgpt.com/docs/hooks)). The build page says "Lifecycle hooks are supported for plugins installed manually in Codex desktop" ([Package your plugin](https://developers.openai.com/plugins/build/plugins)). The code gate above applies to the CLI and to `codex exec` alike.
- **Hook environment:**
  - Every plugin hook gets `PLUGIN_ROOT`, `PLUGIN_DATA`, `CLAUDE_PLUGIN_ROOT` and `CLAUDE_PLUGIN_DATA`; the `CLAUDE_*` pair carries the comment "For OOTB compat with existing plugins" ([discovery.rs#L244-L292](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/hooks/src/engine/discovery.rs#L244-L292)).
  - Codex does not substitute `${PLUGIN_ROOT}` itself. Commands run as `$SHELL -lc` (fallback `/bin/sh`, or `%COMSPEC% /C` on Windows), so the shell expands it ([command_runner.rs#L384-L461](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/hooks/src/engine/command_runner.rs#L384-L461)).
  - The cwd is the session cwd, not the plugin root ([command_runner.rs#L214-L226](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/hooks/src/engine/command_runner.rs#L214-L226)).

### C. Codex compatibility with Claude Code formats
- **What Codex reads:** `.claude-plugin/marketplace.json` as its third marketplace location ([marketplace.rs#L20-L25](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/core-plugins/src/marketplace.rs#L20-L25)), and `.claude-plugin/plugin.json` as its second manifest location, in the legacy format ([protocol.rs#L51-L55](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/exec-server-protocol/src/protocol.rs#L51-L55)).
- **What maps from a Claude plugin:**
  - `commands/` becomes migrated skills.
  - `skills/`, `.mcp.json` and `hooks/hooks.json` are auto-discovered.
  - The `CLAUDE_PLUGIN_*` env vars are exported.
  - Claude `agents/` is not mapped.
  - Claude's `"source":"github"` marketplace form matches no variant, and local sources must start with `./` ([marketplace.rs#L691-L703](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/core-plugins/src/marketplace.rs#L691-L703)).

### D. OpenCode plugin API and distribution
- **Load sources:**
  - Merged opencode.json `plugin` arrays; path specs resolve relative to the declaring file ([config/plugin.ts#L42-L60](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/config/plugin.ts#L42-L60)).
  - A glob `{plugin,plugins}/*.{ts,js}` over each config dir ([config/plugin.ts#L18-L30](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/config/plugin.ts#L18-L30)). The config dirs are the global config, each `.opencode` walking up, `~/.opencode` and `OPENCODE_CONFIG_DIR` ([paths.ts#L23-L41](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/config/paths.ts#L23-L41)).
- **npm installs:** `Npm.add` (Arborist) installs into `{cache}/packages/{spec}` with `ignoreScripts: true`, and a bare name becomes `@latest` ([npm.ts#L87-L145](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/core/src/npm.ts#L87-L145), [shared.ts#L207-L213](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/plugin/shared.ts#L207-L213)). The docs page still says "npm plugins are installed automatically using Bun at startup" ([OpenCode plugins](https://opencode.ai/docs/plugins/)).
- **V1 `Hooks` interface:** 21 keys. Most take `(input, output)` and are mutated in place ([index.ts#L222-L335](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/plugin/src/index.ts#L222-L335)). `Plugin.trigger` passes the same `output` object to every plugin in load order ([plugin/index.ts#L284-L297](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/plugin/index.ts#L284-L297)).
- **What the `config` hook can do:**
  - It is awaited, one hook at a time, with the cached live `s.config` object ([plugin/index.ts#L244-L253](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/plugin/index.ts#L244-L253), [config.ts#L614-L622](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/config/config.ts#L614-L622)).
  - Bootstrap runs `plugin.init()` first, with the comment "Plugin can mutate config so it has to be initialized before anything else" ([bootstrap.ts#L32-L46](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/project/bootstrap.ts#L32-L46)).
  - Every V1 entry point (TUI, `run`, `serve`, `agent list`) loads its instance through `InstanceStore.load`, which runs that bootstrap ([instance-store.ts#L45-L62](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/project/instance-store.ts#L45-L62), [instance-context.ts#L23-L35](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/server/routes/instance/httpapi/middleware/instance-context.ts#L23-L35)).
  - The consumers then read the mutated keys:
    - `cfg.agent`: unknown keys become new agents with `mode: "all"` ([agent.ts#L267-L294](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/agent/agent.ts#L267-L294))
    - `cfg.command` ([command/index.ts#L90-L100](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/command/index.ts#L90-L100))
    - `config.instructions` ([instruction.ts#L135-L150](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/session/instruction.ts#L135-L150))
    - `cfg.mcp` ([mcp/index.ts#L492-L496](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/mcp/index.ts#L492-L496))
    - `cfg.skills.paths` ([skill/index.ts#L205-L220](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/skill/index.ts#L205-L220))
  - A throwing config hook is logged and ignored.
- **Prompt transforms:**
  - `experimental.chat.system.transform` works on the `system` array; extra entries after the header are collapsed into a second element ([request.ts#L56-L78](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/session/llm/request.ts#L56-L78)).
  - `experimental.chat.messages.transform` runs on `{messages}` before the messages are converted for the model ([prompt.ts#L1255-L1262](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/session/prompt.ts#L1255-L1262)).
- **No file scanning of plugin packages:** OpenCode does not scan a package for skills, commands or agents. The only package.json field it reads is `oc-themes` ([shared.ts#L238-L261](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/plugin/shared.ts#L238-L261)).
- **Native skill dirs:** OpenCode reads `.claude/skills`, `~/.claude/skills`, `.agents/skills` and `~/.agents/skills` without any plugin ([skill/index.ts#L185-L227](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/skill/index.ts#L185-L227), [OpenCode skills](https://opencode.ai/docs/skills/)).
- **Claude Code plugins are not loaded:** they are "not loaded from" `.claude/plugins/`. Issue #8158 was "closed as not planned on Mar 25, 2026" ([issue #8158](https://github.com/anomalyco/opencode/issues/8158)).
- **V2 plugin API:**
  - The shape is `define({ id, setup(ctx) })` from `@opencode-ai/plugin/v2/promise`, with agent, command, skill and other transforms on `ctx` ([v2 plugin.ts#L1-L15](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/plugin/src/v2/promise/plugin.ts#L1-L15), [context.ts#L12-L22](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/plugin/src/v2/promise/context.ts#L12-L22)).
  - V2 core accepts only a default `{id, effect|setup}` export and never calls the V1 config hook ([external.ts#L15-L91](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/core/src/config/plugin/external.ts#L15-L91)).
  - The docs say "V1 plugin implementations do not run in V2. Moving a file or renaming its config entry is not enough." and "V1 object entrypoints are supported in OpenCode `1.18.29` and newer." V1's `experimental.chat.system.transform` maps to `ctx.session.hook("context", ...)` ([migrate-v1](https://opencode.ai/v2/docs/build/plugins/migrate-v1)).
  - V2 is still in transition: PLAN.md calls itself "an implementation plan, not documentation for the current API" ([PLAN.md#L476-L480](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/plugin/src/v2/effect/PLAN.md#L476-L480)).

### E. Precedent: one repo, three engines
- **obra/superpowers:**
  - **OpenCode:** one dependency-free ESM file, `.opencode/plugins/superpowers.js`, which `package.json` `main` points to ([package.json](https://github.com/obra/superpowers/blob/8ca22dba9a94f28898bbce59f2537ff4d87c747d/package.json#L1-L23)).
    - V1: a `config` hook pushes the shared `skills/` onto `config.skills.paths`, and `experimental.chat.messages.transform` injects a bootstrap ([L219-L266](https://github.com/obra/superpowers/blob/8ca22dba9a94f28898bbce59f2537ff4d87c747d/.opencode/plugins/superpowers.js#L219-L266)).
    - V2: the default export `{id, server, setup}` registers skills through `ctx.skill.transform` ([L286-L383](https://github.com/obra/superpowers/blob/8ca22dba9a94f28898bbce59f2537ff4d87c747d/.opencode/plugins/superpowers.js#L286-L370)).
  - **Claude Code:** the manifest is metadata only ([.claude-plugin/plugin.json](https://github.com/obra/superpowers/blob/8ca22dba9a94f28898bbce59f2537ff4d87c747d/.claude-plugin/plugin.json#L1-L20)). Its SessionStart hook runs `${CLAUDE_PLUGIN_ROOT}/hooks/run-hook.cmd`, a wrapper valid as both cmd and bash ([hooks.json](https://github.com/obra/superpowers/blob/8ca22dba9a94f28898bbce59f2537ff4d87c747d/hooks/hooks.json#L1-L17)).
  - **Codex:** the manifest sets `"skills": "./skills/"` and `"hooks": {}`, which suppresses auto-discovery of `hooks/hooks.json` ([.codex-plugin/plugin.json](https://github.com/obra/superpowers/blob/8ca22dba9a94f28898bbce59f2537ff4d87c747d/.codex-plugin/plugin.json#L1-L48)).
  - **Version sync:** `scripts/bump-version.sh` keeps the 11 version fields listed in `.version-bump.json` in step ([bump-version.sh](https://github.com/obra/superpowers/blob/8ca22dba9a94f28898bbce59f2537ff4d87c747d/scripts/bump-version.sh#L1-L40)).
  - **Codex distribution:** a sync script mirrors the tree into a Codex marketplace fork ([sync-to-codex-plugin.sh](https://github.com/obra/superpowers/blob/8ca22dba9a94f28898bbce59f2537ff4d87c747d/scripts/sync-to-codex-plugin.sh#L1-L83)).
  - **OpenCode install:** `"plugin"` (V1) or `"plugins"` (V2) set to `superpowers@git+https://…` ([INSTALL.md](https://github.com/obra/superpowers/blob/8ca22dba9a94f28898bbce59f2537ff4d87c747d/.opencode/INSTALL.md#L9-L42)).
- **Geekfish/agent-plugins:**
  - Each plugin has `plugins/<plugin>/.claude-plugin/plugin.json` and `.codex-plugin/plugin.json`.
  - Catalogs sit at `.claude-plugin/marketplace.json`, `.agents/plugins/marketplace.json` and `.opencode/marketplace.json`.
  - Its rule: "When a plugin changes, bump the version in both of its manifests. Keep them equal." ([Geekfish/agent-plugins](https://github.com/Geekfish/agent-plugins)).
- **EveryInc/compound-engineering-plugin:**
  - "The repository root is the plugin package (`plugin.json` plus `skills/`)."
  - On OpenCode: "OpenCode auto-discovers `.opencode/plugins/compound-engineering.js`" ([repo](https://github.com/EveryInc/compound-engineering-plugin), [OpenCode INSTALL](https://github.com/EveryInc/compound-engineering-plugin/blob/main/.opencode/INSTALL.md)).

### F. Runtime constraints
- **Codex:**
  - It is a native Rust binary. Hook commands get only the user's shell ([command_runner.rs#L384-L461](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-rs/hooks/src/engine/command_runner.rs#L384-L461)).
  - The npm package is a Node launcher (`"node": ">=16"`) that spawns the platform binary ([package.json](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-cli/package.json#L1-L21), [codex.js#L241-L244](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/codex-cli/bin/codex.js#L241-L244)).
  - The standalone installer and the Homebrew cask need no Node ([README.md#L14-L48](https://github.com/openai/codex/blob/64335d1afd83f3aaabd6a3a58ebbdf0bf7b994ff/README.md#L14-L48)). So node and python are not guaranteed.
- **OpenCode:**
  - The binary is a `Bun.build` single-file compile ([build.ts#L163-L181](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/script/build.ts#L163-L181)).
  - Plugins load in-process with `await import(row.entry)` ([loader.ts#L123-L145](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/plugin/loader.ts#L123-L145)).
  - `PluginInput.$` is `Bun.$` ([plugin/index.ts#L163-L168](https://github.com/sst/opencode/blob/388406238bd5ca15564a762840a2362c3a45bd9c/packages/opencode/src/plugin/index.ts#L163-L168)).

## Coverage
- A. Codex manifest & components: settled
- B. Codex install, disk and trust: settled
- C. Codex compatibility with Claude formats: settled
- D. OpenCode plugin API & distribution: settled
- E. Precedent repos: settled
- F. Runtime constraints: settled
- Code result files: tracer-rr/openai-codex-2026-10-09-5.md, tracer-rr/sst-opencode-2026-10-09-2.md, tracer-rr/sst-opencode-2026-10-09-3.md, tracer-rr/obra-superpowers-2026-10-09.md

Digging ended: every sub-area settled (round 2)

## Verification
I checked 22 web facts on 7 pages. Facts from `tracer-rr` were read from pinned commits and count as fetched.
- NOT ON PAGE: "Agent Plugins compatible-clients list names VS Code, Cursor, GitHub Copilot, OpenAI Codex, Kiro" (agent-plugins.org/specification). Removed from the map.
- NOT ON PAGE: "OpenCode has no per-plugin manifest in this layout" (Geekfish/agent-plugins). The layout only implies it; removed from the map.
- UNCHECKED: none.

## Open rabbit holes
- Which hosts actually load a root Agent Plugins `plugin.json`, and whether Claude Code or OpenCode read it. Dug, unsettled.
- Whether OpenCode V2 is now the default, and whether V1 `server` plugins stop loading on it. Dug, unsettled; the docs give no default.
- Whether Codex desktop and the CLI really differ on plugin hooks. The docs say desktop; the code gate is shared. Dug, unsettled.
- Codex issue 28491 and related PRs adding an `agents` key to plugin manifests. Undug.
- How compound-engineering converts Claude plugins for other hosts. Undug.
- superpowers issue #571, a bash 5.3+ heredoc hang affecting bash hook scripts. Undug.
- superpowers `docs/porting-to-a-new-harness.md`, read only in part. Undug.
- Codex versus Claude Code hook event parity, i.e. whether one hook script can serve both engines. Undug.
