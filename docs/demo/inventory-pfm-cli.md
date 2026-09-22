# pfm Go CLI — user-facing capability inventory

Scope: `pfm/cmd/pfm/commands.go`, `pfm/cmd/pfm/*_command.go` (non-test), `pfm/cmd/pfm/main.go`, `pfm/internal/harvest/README.md`, `pfm/internal/headless/README.md` (no `pfm/README.md` in this repo), plus the top-of-file comment of each named `internal/*` package's main file (no `doc.go` files exist in this repo — verified by listing). Harvester internals (`internal/harvest*`), tests, and quality judgments are out of scope.

Git stamp: HEAD `00da35b5`, 0 dirty lines (clean working tree) at trace start.

Method: 6 parallel Explore/haiku tracers (chat core verbs; reload+satellite; mcp/harvest/headless; doctor family; install/update/codex lifecycle; misc top-level+internal), 1 conditional mop-up tracer closing 5 named gaps (`name-sync` disposition, 8 headless-dispatched chat verbs, `chat_new_command.go` role, exact `install` flag spellings, `goal`/`kill-exit` absence checks). Telemetry: 7 tracers dispatched, 7 reports received (reconciled).

Registry finding: `pfm/cmd/pfm/main.go`'s `run()` switch (lines 55–111) is the top-level command registry. `pfm chat <verb>` fans out through `pfm/cmd/pfm/chat_command.go`'s `runChatWithRuntime`, which for some verbs dispatches directly and for others forwards into `pfm/cmd/pfm/headless_command.go`'s own verb switch (lines ~73–140) — both switches were read by tracers; the exact boundary between the two switches (which verbs chat_command.go handles inline vs. forwards) was not fully re-derived byte-for-byte and is flagged AMBIGUOUS below where relevant, never asserted past what was grepped.

---

## Flat capability list

### Top-level commands (`pfm <cmd>`) — registry: `pfm/cmd/pfm/main.go:55-111`

| Command | What it does | Evidence | Unique/Novel |
| --- | --- | --- | --- |
| `pfm version` / `pfm --version` | Prints resolved pfm version, falling back to VCS-embedded revision when unstamped | `pfm/cmd/pfm/main.go:56-57`, `main.go:213-224` (`func runVersion`) | No — standard CLI version flag |
| `pfm ls` | Interactive TUI to list/pick every live and resumable Claude/Codex chat across the fleet | `pfm/cmd/pfm/main.go:58`, `pfm/cmd/pfm/commands.go:31` (`func runLS`) | Yes — cross-engine (Claude+Codex+Opencode) live fleet picker in one TUI |
| `pfm ls -a` / `--all` | Include killed, background, and uncapped rows in the listing | `pfm/cmd/pfm/commands.go:43-44` | No — flag variant of `ls` |
| `pfm ls -K` / `--killed` | List the killed ledger (stable 3-column TSV) | `pfm/cmd/pfm/commands.go:45-46`, `main.go:353` (`func runKilled`) | No — variant |
| `pfm ls --plain` | Render a noninteractive list to stdout | `pfm/cmd/pfm/commands.go:47` | No — variant |
| `pfm ls --tsv` | Render stable tab-separated rows for scripts | `pfm/cmd/pfm/commands.go:48` | No — variant |
| `pfm ls --no-sky` | Disable the interactive "sky"/cosmos widget | `pfm/cmd/pfm/commands.go:49` | Yes — cosmos/starfield visualization toggle is unusual for a fleet CLI |
| `pfm ls --safe auto\|on\|off` | vscode-safe cosmos rendering mode (auto arms on `TERM_PROGRAM=vscode`) | `pfm/cmd/pfm/commands.go:50` | No — terminal-compat variant |
| `pfm ls <id>` | Open one specific chat by id | `pfm/cmd/pfm/commands.go:77-83` (`func openID`) | No — direct-open variant |
| `pfm chat <verb>` | Dispatcher for all chat-scoped operations (see Chat section below) | `pfm/cmd/pfm/main.go:60-61` | Yes — see per-verb entries |
| `pfm harvest` | Fetch and convert a URL/DOI/ISBN/PMID/PMCID/local path into cached, readable content | `pfm/cmd/pfm/main.go:62-63`, `pfm/cmd/pfm/harvest_command.go:79-126` | Yes — multi-identifier-type document harvester with local cache (harvester internals out of scope) |
| `pfm harvest --refresh` | Bypass cache, fetch fresh | `pfm/cmd/pfm/harvest_command.go:79-126` | No — cache-bypass variant |
| `pfm harvest --size-only` | Fetch + cache but print only size/path | `pfm/cmd/pfm/harvest_command.go:79-126` | Yes — size-probe mode without full content emission |
| `pfm harvest --json` | Machine-readable harvest results | `pfm/cmd/pfm/harvest_command.go:79-126` | No — output-format variant |
| `pfm harvest ask -p <prompt> <sources>...` | Ask a question answered from one or more harvested sources | `pfm/cmd/pfm/harvest_command.go:143-263` | Yes — structured Q&A grounded in fetched documents, one CLI call |
| `pfm harvest ask --engine claude\|codex` | Override the configured answering engine | `pfm/cmd/pfm/harvest_command.go:143-263` | No — engine-selection variant |
| `pfm harvest ask --model <MODEL>` | Override model | `pfm/cmd/pfm/harvest_command.go:143-263` | No — variant |
| `pfm harvest ask --effort <EFFORT>` | Override reasoning effort | `pfm/cmd/pfm/harvest_command.go:143-263` | No — variant |
| `pfm harvest ask --refresh` | Bypass harvest cache for the ask sources | `pfm/cmd/pfm/harvest_command.go:143-263` | No — variant |
| `pfm headless` (exec form) | Run Claude or Codex through one isolated, scriptable process interface (prompt/task in, structured result out) | `pfm/cmd/pfm/main.go:64-65`, `pfm/cmd/pfm/headless_exec_command.go:33-65` | Yes — one-shot sealed/scriptable engine execution outside the tmux fleet model |
| `pfm headless --engine claude\|codex --model M --effort E --account ID` | Select engine/model/effort/account for the isolated run | `pfm/cmd/pfm/headless_exec_command.go:33-65` | No — selection flags |
| `pfm headless --prompt/--prompt-file/stdin` | Supply the prompt via flag, file, or stdin | `pfm/cmd/pfm/headless_exec_command.go:33-65` | No — input-source variant |
| `pfm headless --files/--labels/--task/--task-file` | Attach files and a labeled task to the run | `pfm/cmd/pfm/headless_exec_command.go:33-65` | No — input variant |
| `pfm headless --system/--system-file/--schema/--json-schema` | Supply a system prompt and/or a JSON output schema | `pfm/cmd/pfm/headless_exec_command.go:33-65` | Yes — schema-constrained structured output from a headless run |
| `pfm headless --sealed --tools LIST --setting-sources --strict-mcp-config --allow-unsupported` | Lock down tool access / MCP config surface for the run, or explicitly waive unsupported-control diagnostics | `pfm/cmd/pfm/headless_exec_command.go:33-65` | Yes — sealed/least-privilege one-shot execution mode |
| `pfm headless --no-session-persistence --cwd --timeout --output-format --out --receipt --env --engine-arg` | Session, timing, output-shape, and passthrough controls | `pfm/cmd/pfm/headless_exec_command.go:33-65` | No — plumbing flags |
| `pfm index` | Refresh the transcript index (incremental by default) | `pfm/cmd/pfm/main.go:66-67`, `pfm/cmd/pfm/commands.go:612` (`func runIndex`) | Yes — purpose-built incremental indexer replacing ~300 forks/refresh of the predecessor zsh tool |
| `pfm index --full` | Reparse every indexed file | `pfm/cmd/pfm/commands.go:618` | No — variant |
| `pfm index --progress` | Report start/elapsed time to stderr | `pfm/cmd/pfm/commands.go:619` | No — variant |
| `pfm doctor` | Runs one unified suite of ~30 environment/fleet-health checks (config, engine binaries, MCP daemon, PATH, pre-push hook, harness-prompt baseline, spawn-audit, tmux-titles, launcher, host overlay, deps, git hooks, DB version/integrity/counts/WAL, process table, project roots, professor repo state, Codex pane bindings, crumb dir, harvestpy runtime, harvest cache) — no user-selectable subcommands | `pfm/cmd/pfm/main.go`, `pfm/internal/doctor/doctor.go` | Yes — single consolidated multi-subsystem health gate spanning Go+shell+DB+process-table |
| `pfm doctor --verbose` | Write raw dependency-probe output under `tmp/` | `pfm/internal/doctor/doctor.go` | No — verbosity flag |
| `pfm doctor --skip-harvest` | Exclude the optional harvestpy runtime from the health pass | `pfm/internal/doctor/doctor.go` | No — scoping flag |
| `pfm config` | Initialize, inspect, or validate machine configuration | `pfm/cmd/pfm/main.go:70-71` | No — standard config subcommand family |
| `pfm config init [--force]` | Write config + harvester files | `pfm/cmd/pfm/config_command.go:22` | No |
| `pfm config show` | Display resolved configuration with sources | `pfm/cmd/pfm/config_command.go:24` | No |
| `pfm config validate` | Validate config against schema | `pfm/cmd/pfm/config_command.go:34` | No |
| `pfm reap [--apply] [--horizon] [--busy-recent] [--json]` | Classifies the tmux socket graveyard; `--apply` reclaims idle/orphaned sockets (dry run is default) | `pfm/cmd/pfm/main.go:74-75`, `pfm/cmd/pfm/reap_command.go:24` | Yes — socket-graveyard classifier/reclaimer with symmetric preview=apply law |
| `pfm archive [--apply] [--subagents [--older-than]] [--restore id] [--prune-orphans] [--yes]` | Moves killed chats and old subagent transcripts out of sight, reversibly | `pfm/cmd/pfm/main.go:76-77`, `pfm/cmd/pfm/archive_command.go:49` | Yes — reversible archival (vs. delete) of fleet history |
| `pfm archive --prune-orphans [--yes]` | Reports (and only with `--yes` deletes) orphaned kill-ledger rows | `pfm/cmd/pfm/commands.go:678-726` (`func pruneOrphanedKills`) | No — cleanup variant, dry-run-by-default |
| `pfm heal [--apply] [--thread id]` | Reports or repairs wedged Codex history projections | `pfm/cmd/pfm/main.go:78-79`, `pfm/cmd/pfm/heal_command.go` | Yes — Codex-specific rollout-projection repair, no common analog |
| `pfm name-sync [--dry-run]` | Converges every live chat's tmux window name (the fleet's naming "DNS record") | `pfm/cmd/pfm/main.go:80-81`, `pfm/cmd/pfm/namesync_command.go:28-30` | Yes — window-name convergence across a live tmux fleet |
| `pfm statusline [--refresh-gpt]` | Renders the native Claude Code status line from stdin JSON | `pfm/cmd/pfm/main.go:82-83`, `pfm/cmd/pfm/statusline_command.go:24` | No — hook-only surface, standard status-line contract |
| `pfm usage-hook` | The fail-open usage-limit prompt hook | `pfm/cmd/pfm/main.go:84-85`, `pfm/cmd/pfm/statusline_command.go:149` (`func runUsageHook`) | No — hook-only, fail-open safety pattern is not itself novel |
| `pfm install [--yes] [--vscode] [--skip-harvest] [--skip-engine NAME] [--skip-themes] [--config-dir DIR]` | Stages the self-contained host integration (binaries, launch agent/service, optional VS Code extension) | `pfm/cmd/pfm/main.go:86-87`, `pfm/cmd/pfm/install_command.go:38-43` | Yes — self-contained multi-surface installer (binary+shim+launchd/systemd+VS Code) in one command |
| `pfm uninstall [--config-dir DIR]` | Reverses every install step, preserving config directories | `pfm/cmd/pfm/main.go:88-89`, `pfm/cmd/pfm/uninstall_command.go:9-30` | No — symmetric teardown of `install` |
| `pfm update` | Upgrades the pfm binary and harvestpy sidecar (`--to vX.Y.Z`) | `pfm/cmd/pfm/main.go`, `pfm/internal/update/run.go` | No — standard self-update |
| `pfm update check [--root DIR] [--json]` | Scans an adopter project against its pinned baseline: UPDATED / NEW / GONE-UPSTREAM / LOCAL-DELETED | `pfm/internal/professor/project.go` | Yes — three-tier template-baseline reconciliation report, not a generic diff |
| `pfm update pin <local>...\|--all [--template T] [--root DIR]` | Pins local/newly-adopted files to the current blueprint snapshot | `pfm/internal/professor/project.go` | Yes — per-file adoption gate tied to a template baseline |
| `pfm update ignore <template>... [--undo] [--root DIR]` | Adds/removes templates from a project's ignore list (skips future comparisons) | `pfm/internal/professor/project.go` | Yes — template-level opt-out from the update-check surface |
| `pfm update drop <local>... [--root DIR]` | Deletes a file's baseline record so it stops being tracked for updates | `pfm/internal/professor/project.go` | Yes — asymmetric "forget" distinct from ignore |
| `pfm update adopt [--root DIR] [--at REF]` | Bootstraps a new project baseline from the store's HEAD or an explicit ref (for installs that predate scaffolding) | `pfm/internal/professor/project.go` | Yes — retroactive baseline capture |
| `pfm init [dir] [--force]` | Scaffolds project templates once and pins their baseline | `pfm/cmd/pfm/main.go`, `pfm/cmd/pfm/init_command.go`, `pfm/internal/professor/scaffold.go` | No — one-time scaffold command, common pattern (framework-specific content is novel, the verb shape is not) |
| `pfm whoami [--json] [--label]` | Prints this chat's own tmux session name/identity | `pfm/cmd/pfm/main.go:94-95`, `pfm/cmd/pfm/whoami_command.go:24` | Yes — self-identity disclosure for a process inside a live tmux/engine session |
| `pfm issues [--all] [--json]` | Lists servicedesk complaints filed through the `issue_servicedesk` MCP tool | `pfm/cmd/pfm/main.go:96-97`, `pfm/cmd/pfm/issues_command.go:25` | Yes — durable complaint ledger fed by an MCP tool, read back via CLI |
| `pfm mcp` (bare) | Alias for `pfm mcp chat serve` (preserves historical bare-`pfm mcp` invocation) | `pfm/cmd/pfm/main.go:98-99`, `main.go:124-135` | No — back-compat alias |
| `pfm mcp ls` | Lists registered MCP servers, enabled state, and config source | `pfm/cmd/pfm/main.go:137-148` | No — standard registry listing |
| `pfm mcp <server> enable\|disable` | Toggles one registered MCP server on/off in config | `pfm/cmd/pfm/main.go:160-172` | No — standard toggle |
| `pfm mcp chat serve` | Runs the chat fleet's MCP server over stdio | `pfm/cmd/pfm/main.go:192-210`, `pfm/cmd/pfm/mcp_serve_command.go` | Yes — exposes live fleet control (18 tools, see MCP section) as MCP tools |
| `pfm mcp harvester serve` | Runs the harvester MCP server | `pfm/cmd/pfm/main.go:189-190` | No — standard MCP server exposure (harvester internals out of scope) |
| `pfm codex` | Compiles or checks the Codex project mirror | `pfm/cmd/pfm/main.go:100-101` | No — mirror-generation family |
| `pfm codex build [repo-root] [--home] [--model alias=value] [--root-adapter] [--agent-preamble] [--exclude-dir\|--exclude-project] [--never-register] [--suffix-mode\|--suffix-prefix]` | Compiles repo agent `.md` sources into Codex `.toml` + generates a root adapter skeleton | `pfm/cmd/pfm/codex_command.go:30-111` | Yes — agent-prompt-DSL-to-Codex-config compiler |
| `pfm codex check [repo-root]` (same flags) | Validates agent TOML compilation without writing, flags unregistered agents | `pfm/cmd/pfm/codex_command.go:30-111` | Yes — read-only compile validation pass |
| `pfm codex agents [--home]` | Compiles machine-global agents from `{home}/.professor/templates/global/agents/*.md` and symlinks them into Claude+Codex registries | `pfm/cmd/pfm/codex_command.go:135-171` | Yes — global (cross-project) agent registry wiring |
| `pfm help` / `-h` / `--help` | Prints top-level usage | `pfm/cmd/pfm/main.go:104-106` | No — standard help |

### `pfm internal <verb>` — hook/wiring plumbing, not typed by a human in normal use (registry: `pfm/cmd/pfm/main.go:382-529`, `runInternal`)

| Command | What it does | Evidence | Unique/Novel |
| --- | --- | --- | --- |
| `pfm internal clear-kill` | Forgets killed sessions on unclean shutdown (stdin-driven) | `pfm/cmd/pfm/main.go:387-389`, `pfm/cmd/pfm/clear_kill_command.go` | No — recovery utility, hook-only |
| `pfm internal agent-open --id --cwd [--config]` | Opens an agent thread in an IDE; resolves account list + session metadata | `pfm/cmd/pfm/main.go:390-391`, `pfm/cmd/pfm/agent_open_command.go` | Yes — CLI-to-IDE handoff protocol for a live agent session |
| `pfm internal codex-launch BINARY [args...]` | Back-compat shim: execs the Codex launcher via the deps registry | `pfm/cmd/pfm/main.go:393-395`, `pfm/cmd/pfm/codex_launch_compat.go:13-28` | No — deprecated compatibility shim |
| `pfm internal codex-appendix` | Codex post-expansion hook; appends professor metadata to output (stdin-driven) | `pfm/cmd/pfm/main.go:396-397`, `pfm/cmd/pfm/codex_appendix_command.go:9-14` | Yes — workflow-integration hook point specific to Codex's expansion step |
| `pfm internal launch --real PATH [--cwd DIR] -- [claude args]` | Intercepts a Claude launch, establishes the tmux session/pane, forwards args | `pfm/cmd/pfm/main.go:399-400`, `pfm/cmd/pfm/launch_command.go:66` | Yes — capture-and-redirect launcher pattern for wrapping the real Claude binary |
| `pfm internal launcher-repair` | Detects and fixes a broken Claude launcher symlink/script | `pfm/cmd/pfm/main.go:402-403`, `pfm/cmd/pfm/launcher_repair_command.go` | No — heal utility, hook-only |
| `pfm internal explore-deny` | PreToolUse hook: fail-open sandbox deny-list injector (stdin-driven) | `pfm/cmd/pfm/main.go:405-406`, `pfm/cmd/pfm/explore_deny_command.go` | No — hook-only policy gate |
| `pfm internal epic-inject` | Subagent "epic" payload injection (stdin-driven) | `pfm/cmd/pfm/main.go:408-409`, `pfm/cmd/pfm/epic_inject_command.go` | No — hook-only, internal dispatch |
| `pfm internal reload-intercept` | UserPromptSubmit hook: intercepts `/reload` (stdin-driven) | `pfm/cmd/pfm/main.go:411-412`, `pfm/cmd/pfm/reload_intercept_command.go` | No — hook-only interception |
| `pfm internal exit-intercept` | UserPromptSubmit hook: intercepts `e`/`/e` close (stdin-driven) | `pfm/cmd/pfm/main.go:414-415`, `pfm/cmd/pfm/exit_intercept_command.go` | No — hook-only interception |
| `pfm internal exit-close` | SessionEnd hook: closes the terminal session (stdin-driven) | `pfm/cmd/pfm/main.go:417-418`, `pfm/cmd/pfm/exit_close_command.go` | No — hook-only lifecycle |
| `pfm internal compact-nudge` | Compact-milestone reminder hook (stdin-driven) | `pfm/cmd/pfm/main.go:420-421`, `pfm/cmd/pfm/compact_nudge_command.go` | No — hook-only reminder |
| `pfm internal reload-run` | Worker entry for a queued chat reload (backs `pfm chat reload`) | `pfm/cmd/pfm/main.go:423-424` (`runChatReloadWorkerWithRuntime`) | No — internal worker for a user-facing verb already counted under `chat reload` |
| `pfm internal then --socket --target [--self] --steer TEXT...` | Detached waiter that chains follow-up steers once a pane settles | `pfm/cmd/pfm/main.go:426-427`, `pfm/cmd/pfm/then_command.go:32` | Yes — event-driven async action chaining tied to pane completion |
| `pfm internal update-check --cache --current --url` | Checks a release endpoint and caches the result for a background update notice | `pfm/cmd/pfm/main.go`, `pfm/internal/hookentry/update_check.go` | No — standard background update-check plumbing |
| `pfm internal primary-get` | Prints the current primary account | `pfm/cmd/pfm/main.go:432-435` | No — plumbing read |
| `pfm internal chat-server <socket> <cwd> <run>` | Creates a bare tmux session for a chat (platform-specific wiring) | `pfm/cmd/pfm/main.go:436-437`, `pfm/cmd/pfm/chat_server_command.go` | No — session bootstrap plumbing |
| `pfm internal stale [args]` | Delegates to the `stale` package's own CLI | `pfm/cmd/pfm/main.go:439-440` | UNKNOWN — `stale.Run` implementation not in this trace's file boundary; not further resolved |
| `pfm internal primary-set <account>` | Sets the primary account | `pfm/cmd/pfm/main.go:442-464` | No — plumbing write, symmetric with `primary-get` |
| `pfm internal kill-exit --engine --id --path --socket --socket-name --pane` | Finishes a kill's exit choreography (hook/unit-only; unknown-subcommand path exits non-blocking 2 for a version-mismatch safety reason) | `pfm/cmd/pfm/main.go:470-529` | Yes — version-mismatch-safe hook contract (unknown subcommand still exits non-blocking, documented inline) |

### `pfm chat <verb>` family — dispatcher entry `pfm/cmd/pfm/chat_command.go` (`runChatWithRuntime`), some verbs forwarded into `pfm/cmd/pfm/headless_command.go`'s own switch (~lines 73-140)

| Verb | What it does | Evidence | Unique/Novel |
| --- | --- | --- | --- |
| `pfm chat open <id\|name\|socket>` | Opens a chat by name, socket, or id | `pfm/cmd/pfm/chat_command.go:88` (`case "open"`) | No |
| `pfm chat read [--tail] [--condensed] [--json]` | Reads the chat's transcript | `pfm/cmd/pfm/chat_command.go:90` (`case "read"`) | No |
| `pfm chat kill [self\|id] [--exit]` | Kills (hides) a chat; live chats also run the exit choreography | `pfm/cmd/pfm/chat_command.go:114` (`case "kill"`), `pfm/cmd/pfm/main.go:270-329` (`func runKill`) | No — see `internal kill-exit` for the novel version-safety detail |
| `pfm chat unkill <id>` | Reverses a kill | `pfm/cmd/pfm/chat_command.go:116` (`case "unkill"`), `main.go:331-351` | No |
| `pfm chat resolve <name>` | Resolves a chat name to its live tmux address (socket+pane) | `pfm/cmd/pfm/chat_command.go:131` (`case "resolve"`) | Yes — cross-engine identity-to-tmux-address binding |
| `pfm chat capture` | Prints a live chat's tmux scrollback | `pfm/cmd/pfm/chat_command.go:106` (`case "capture"`) | No |
| `pfm chat recover` | Rebuilds a Codex conversation from its rollout file | `pfm/cmd/pfm/chat_command.go:110` (`case "recover"`) | Yes — post-mortem conversation revival from Codex rollout data |
| `pfm chat name <newname>` | Renames a chat and converges its tmux window name | `pfm/cmd/pfm/chat_command.go:112` (`case "name"`) | No |
| `pfm chat end` | Ends a chat's tmux server | `pfm/cmd/pfm/chat_command.go:118` (`case "end"`) | No |
| `pfm chat find <excerpt-file>` | Finds a transcript by matching an excerpt | `pfm/cmd/pfm/chat_command.go:511` (`case "find"`) → `pfm/cmd/pfm/chat_satellite_command.go:38` | Yes — content-addressed transcript search |
| `pfm chat save <output-path>` | Exports a session's transcript to a file | `pfm/cmd/pfm/chat_command.go:513` (`case "save"`) → `chat_satellite_command.go:157` | No |
| `pfm chat branch [--engine] [--session-id] [--cwd] [--account] [--name] [name]` | Forks a session into a new detached chat | `pfm/cmd/pfm/chat_command.go:515` (`case "branch"`) → `chat_satellite_command.go:358-364` | Yes — session forking/branching, not a common fleet-CLI primitive |
| `pfm chat ls [--all\|-a]` | Lists live sessions (satellite variant of top-level `ls`) | `pfm/cmd/pfm/chat_command.go:517` (`case "ls"`) → `chat_satellite_command.go:238` | No |
| `pfm chat history [args]` | Shows a session's timeline/replay | `pfm/cmd/pfm/chat_command.go:519` (`case "history"`) → `chat_satellite_command.go:692` | Yes — session timeline/replay view |
| `pfm chat ask -p/--prompt [--timeout] [--settle] [--now] [--json] [--progress]` | Delivers a message to a chat and polls/waits for its answer | `pfm/cmd/pfm/ask_command.go:31`, forwarded via `pfm/cmd/pfm/headless_command.go:102` | Yes — synchronous two-way poll-until-answered, eliminating manual inject+poll loops |
| `pfm chat keys [--delay] [--literal] [--capture]` | Presses literal keys into a live chat's tmux pane | `pfm/cmd/pfm/chat_keys_command.go:27`, forwarded via `headless_command.go:108` | No — tmux key automation |
| `pfm chat reload [--new] [--hide] [--then TEXT] [--sock ADDR] [--model M] [--effort E] [--1h on\|off] [--account N] [--pane ID]` | Reboots a Claude seat in place inside its tmux pane, with fresh/hide/continuation/model/effort/account/cache overrides | `pfm/cmd/pfm/chat_reload_command.go` (`runChatReloadWithRuntime`, `runChatReloadWorkerWithRuntime`) | Yes — in-place session reboot with a rich override surface (model/effort/cache/account) unusual for a CLI reload primitive |
| `pfm chat new [--agent-role ROLE]` | Spawns a fresh chat with the fleet's full launch ceremony, optionally carrying a registered role through the seat's prompt channel while the caller prompt remains the first user message, then detaches | `pfm/cmd/pfm/chat_command.go` (`case "new"`) → `pfm/cmd/pfm/chat_new_command.go` (`func runRun`) | Yes — native fleet spawn with an engine-native role channel that walks away without an eval/attach step |
| `pfm chat last` | Retrieves the chat's latest answer | `pfm/cmd/pfm/headless_command.go:91` (`case "last"`) → `runHeadlessLast` | No |
| `pfm chat status` | Probes a chat's live state (idle/busy/dead) | `pfm/cmd/pfm/headless_command.go:93` (`case "status"`) → `runHeadlessStatus` | No |
| `pfm chat stream` | Streams a chat's responses live | `pfm/cmd/pfm/headless_command.go:95` (`case "stream"`) → `runHeadlessStream` | No |
| `pfm chat inject` | Sends a message into a running chat (fire-and-forget, vs. `ask`'s wait) | `pfm/cmd/pfm/headless_command.go:97` (`case "inject"`) → `runHeadlessInject` | No |
| `pfm chat self-compact` | Triggers self-compaction after the chat's own turn settles | `pfm/cmd/pfm/headless_command.go:99` (`case "self-compact"`) → `runHeadlessSelfCompact` | No |
| `pfm chat watch` | Monitors a chat's output live (observer, no interaction) | `pfm/cmd/pfm/headless_command.go:103` (`case "watch"`) → `runHeadlessWatch` | No |
| `pfm chat modal` | Drives a modal chat interaction mode | `pfm/cmd/pfm/headless_command.go:128` (`case "modal"`) → `runChatModal` | No — insufficient detail traced to call this novel |
| `pfm chat whoami` | Reports this chat's own identity (same underlying `runWhoami` as top-level `pfm whoami`) | `pfm/cmd/pfm/headless_command.go:125` → `runWhoami` | No — alias surface of top-level `whoami` |

**Confirmed ABSENT as CLI verbs (grepped, zero hits):** `pfm chat goal` — `grep -n '"goal"'` returned no matches in `chat_dispatch.go` or `chat_command.go`. `pfm chat kill-exit` as a chat verb — `grep -n '"kill-exit"'` returned no matches in `chat_command.go` (it is `pfm internal kill-exit`, listed above).

### `pfm mcp chat serve` — MCP tools exposed (registry: `pfm/internal/mcpserv/server.go:2-6`, `chatToolNames`)

| MCP tool | What it does | Evidence | Unique/Novel |
| --- | --- | --- | --- |
| `chat_capture` | Screen-capture a live chat's scrollback | `pfm/internal/mcpserv/server.go` `chatToolNames` list; registered via `mcp.AddTool(..., Name: "chat_capture", ...)` | Yes — tmux pane capture exposed as an agent-callable tool |
| `chat_find` | Search transcripts by excerpt | same registry | Yes — indexed transcript search as an MCP tool |
| `chat_inject` | Types a message into another chat | same registry | Yes — cross-chat agent-to-agent messaging |
| `chat_keys` | Presses tmux keys in a live chat | same registry | No — mirrors CLI `chat keys` |
| `chat_kill` | Hides a chat from the fleet | same registry | No — mirrors CLI `chat kill` |
| `chat_last` | Newest assistant answer | same registry | No — basic transcript read |
| `chat_ls` | Lists live + resumable chats | same registry | No — mirrors CLI `ls`/`chat ls` |
| `chat_name` | Renames/names a live chat | same registry | No — mirrors CLI `chat name` |
| `chat_new` | Spawns a detached, named chat | same registry | No — mirrors CLI `chat new` |
| `chat_open` | Reopens a resumable chat | same registry | No — mirrors CLI `chat open` |
| `chat_read` | Reads recent transcript turns | same registry | No — mirrors CLI `chat read` |
| `chat_resolve` | Resolves a name to socket+pane | same registry | No — mirrors CLI `chat resolve` |
| `chat_save` | Dumps a transcript to a file | same registry | No — mirrors CLI `chat save` |
| `chat_self_compact` | Self-triggered compaction after own turn settles | same registry | No — mirrors CLI `chat self-compact` |
| `chat_status` | Inspects chat state | same registry | No — mirrors CLI `chat status` |
| `chat_unkill` | Unhides a killed chat | same registry | No — mirrors CLI `chat unkill` |
| `chat_whoami` | Reports the calling chat's own identity | same registry | No — mirrors CLI `whoami` |
| `issue_servicedesk` | Files a durable complaint about pfm itself | same registry | Yes — a CLI/agent filing tool-initiated bug reports against its own host tool, read back via `pfm issues` |

### `pfm mcp harvester serve` — MCP tools (registered in `pfm/internal/harvestmcp`, cross-referenced via `pfm/cmd/pfm/mcp_serve_command.go:25-27`; internals out of scope)

| MCP tool | What it does | Evidence | Documented in `pfm/internal/harvest/README.md` |
| --- | --- | --- | --- |
| `fetch` | Fetch URL/DOI/ISBN/PMID/PMCID/local file | `mcp_serve_command.go:25-27`, `mcp.AddTool(..., Name: "fetch", ...)` | Yes (line ~23) |
| `findWorks` | Find papers/works by title, bibliographic candidates | same | Yes (line ~23) |
| `search` | Ranked web search with snippets | same | Implicit |
| `fetchImage` | Fetch an image/figure | same | Implicit |
| `searchCache` | Query already-cached documents | same | Yes (line ~28) |
| `archive` | Browse/list/extract from a compressed archive (.zip/.tar/.7z/.rar) | same | Implicit |

---

## Absence provenance

Every spelling checked and found ABSENT, with the grep that proved it:

- `pfm chat goal` — `grep -n '"goal"' pfm/cmd/pfm/chat_dispatch.go` and `pfm/cmd/pfm/chat_command.go`: zero hits.
- `pfm chat kill-exit` — `grep -n '"kill-exit"' pfm/cmd/pfm/chat_command.go`: zero hits. `kill-exit` exists only as `pfm internal kill-exit`.
- `pfm/README.md` — does not exist in this repo (`ls` confirmed; `pfm/internal/harvest/README.md` and `pfm/internal/headless/README.md` do exist and were read).
- `doc.go` for all 21 named `internal/*` packages in the boundary (chat, inject, reload, spawn, sky, fleet, headless, mcpserv, heal, reap, archive, updatecheck, installer, codexgen, statusline, stats, usagehook, nudge, agentopen, agentrole, recovery) — none exist (`ls pfm/internal/$p/doc.go` returned "No such file or directory" for all 21); the eponymous main file's top comment was used instead per the brief's fallback.

## Named gaps / not fully resolved (never silently dropped)

- `pfm internal stale [args]` — dispatches to `stale.Run(args[1:], stdout, stderr)` at `pfm/cmd/pfm/main.go:439-440`; the `stale` package's own subcommand surface was not in any tracer's bucket and was not separately walked. **what: UNKNOWN beyond the dispatch line** — named, not dropped.
- `pfm chat modal` — dispatch confirmed (`headless_command.go:128`, `runChatModal`), but no tracer read `runChatModal`'s body, so its one-line description is inferred from the case name only, not from behavior read on disk.
- The exact split of which `pfm chat <verb>` names `chat_command.go`'s own switch handles inline vs. forwards into `headless_command.go`'s switch was not byte-verified end-to-end across both files in the same pass — reported per-verb evidence is solid (each case line was grepped), but the two-switch relationship itself is AMBIGUOUS and stated as such above, not asserted.
- `pfm mcp <server> enable|disable` was only confirmed for the two registered servers seen (`chat`, `harvester`) via `config.RegisteredMCPServers()`; no other registered server names were enumerated by name.

## File dispositions (full repo-relative paths)

EDGE = contains one or more capabilities, quoted by a tracer. NOT-MINE = in scope but assigned elsewhere / contains no independent capability. FRONTIER = named but not walked to completion.

- pfm/cmd/pfm/commands.go — EDGE (`ls`, `index`, `archive --prune-orphans`, and shared open/kill helpers)
- pfm/cmd/pfm/main.go — EDGE (top-level registry + `internal` registry)
- pfm/cmd/pfm/action_dispatch.go — NOT-MINE (no tracer found independent user-facing capability; internal plumbing for emitted shell lines)
- pfm/cmd/pfm/agent_open_command.go — EDGE (`pfm internal agent-open`)
- pfm/cmd/pfm/archive_command.go — EDGE (`pfm archive`)
- pfm/cmd/pfm/ask_command.go — EDGE (`pfm chat ask`)
- pfm/cmd/pfm/caller_engine.go — NOT-MINE (no capability found)
- pfm/cmd/pfm/chat_command.go — EDGE (chat dispatcher + open/read/kill/unkill/resolve/capture/recover/name/end)
- pfm/cmd/pfm/chat_keys_command.go — EDGE (`pfm chat keys`)
- pfm/cmd/pfm/chat_satellite_command.go — EDGE (`chat find/read/save/ls/branch/history`)
- pfm/cmd/pfm/chat_server_command.go — EDGE (`pfm internal chat-server`)
- pfm/cmd/pfm/clear_kill_command.go — EDGE (`pfm internal clear-kill`)
- pfm/cmd/pfm/codex_appendix_command.go — EDGE (`pfm internal codex-appendix`)
- pfm/cmd/pfm/codex_command.go — EDGE (`pfm codex build/check/agents`)
- pfm/cmd/pfm/codex_launch_compat.go — EDGE (`pfm internal codex-launch`)
- pfm/cmd/pfm/compact_nudge_command.go — EDGE (`pfm internal compact-nudge`)
- pfm/cmd/pfm/config_command.go — EDGE (`pfm config init/show/validate`)
- pfm/internal/doctor/doctor.go — EDGE (`pfm doctor`, 30 checks)
- pfm/cmd/pfm/engines.go — NOT-MINE (no independent capability found by any tracer)
- pfm/cmd/pfm/epic_inject_command.go — EDGE (`pfm internal epic-inject`)
- pfm/cmd/pfm/exit_close_command.go — EDGE (`pfm internal exit-close`)
- pfm/cmd/pfm/exit_intercept_command.go — EDGE (`pfm internal exit-intercept`)
- pfm/cmd/pfm/explore_deny_command.go — EDGE (`pfm internal explore-deny`)
- pfm/internal/doctor/harness_prompt_baselines.go — EDGE (doctor's harness-prompt check helper, no independent CLI verb)
- pfm/internal/doctor/harness_prompt.go — EDGE (doctor's harness-prompt check helper, no independent CLI verb)
- pfm/cmd/pfm/harvest_command.go — EDGE (`pfm harvest`, `pfm harvest ask`)
- pfm/cmd/pfm/headless_command.go — EDGE (chat verb forwarding switch: new/last/status/stream/inject/self-compact/watch/modal/whoami/reload/ask/keys)
- pfm/cmd/pfm/headless_exec_command.go — EDGE (`pfm headless` exec flags)
- pfm/cmd/pfm/heal_command.go — EDGE (`pfm heal`)
- pfm/cmd/pfm/init_command.go + pfm/internal/professor/scaffold.go — EDGE (`pfm init`)
- pfm/cmd/pfm/inject_resume.go — NOT-MINE (helper, no independent dispatcher found)
- pfm/cmd/pfm/install_command.go — EDGE (`pfm install`, all 6 flags verified verbatim)
- pfm/cmd/pfm/issues_command.go — EDGE (`pfm issues`)
- pfm/cmd/pfm/launch_command.go — EDGE (`pfm internal launch`)
- pfm/cmd/pfm/launcher_repair_command.go — EDGE (`pfm internal launcher-repair`)
- pfm/cmd/pfm/mcp_serve_command.go — EDGE (`pfm mcp chat serve` / `pfm mcp harvester serve` wiring + harvester MCP tool names)
- pfm/cmd/pfm/mcp_shared.go — NOT-MINE (runtime bridge, no independent verb)
- pfm/cmd/pfm/namesync_command.go — EDGE (`pfm name-sync [--dry-run]`)
- pfm/cmd/pfm/pipeline.go — NOT-MINE (not independently walked; no tracer bucket claimed a user-facing capability here — named as unwalked, see coverage note)
- pfm/cmd/pfm/prepush_doctor.go — EDGE (doctor's pre-push check helper, no independent CLI verb)
- pfm/cmd/pfm/prompt_block.go — NOT-MINE (no capability found)
- pfm/cmd/pfm/reap_command.go — EDGE (`pfm reap`)
- pfm/cmd/pfm/chat_reload_command.go — EDGE (`pfm chat reload`; `runChatReloadWithRuntime` schedules `runChatReloadWorkerWithRuntime`)
- pfm/cmd/pfm/reload_intercept_command.go — EDGE (`pfm internal reload-intercept`)
- pfm/cmd/pfm/chat_new_command.go — EDGE (`pfm chat new` via `runRun`)
- pfm/cmd/pfm/runtime_config.go — NOT-MINE (no capability found)
- pfm/cmd/pfm/spawn_audit_doctor.go — EDGE (doctor's spawn-audit check helper, no independent CLI verb)
- pfm/cmd/pfm/statusline_command.go — EDGE (`pfm statusline`, `pfm usage-hook`)
- pfm/cmd/pfm/then_command.go — EDGE (`pfm internal then`)
- pfm/cmd/pfm/tmux_titles_doctor.go — EDGE (doctor's tmux-titles check helper, no independent CLI verb)
- pfm/cmd/pfm/uninstall_command.go — EDGE (`pfm uninstall`)
- pfm/internal/update/run.go — EDGE (`pfm update`)
- pfm/internal/hookentry/update_check.go + pfm/internal/picker/update_row.go — EDGE (`pfm internal update-check`)
- pfm/internal/professor/project.go — EDGE (`pfm update check/pin/ignore/drop/adopt`)
- pfm/cmd/pfm/whoami_command.go — EDGE (`pfm whoami`)
- pfm/README.md — ABSENT from repo (not a hole; confirmed nonexistent)
- pfm/internal/harvest/README.md — EDGE (read fully; cross-referenced harvester MCP tool docs)
- pfm/internal/headless/README.md — EDGE (read fully; cross-referenced headless exec flags)

FRONTIER (not walked to completion by any tracer, named rather than dropped):

- `pfm/internal/stale/*` — the `stale` package's own subcommand surface behind `pfm internal stale` was never opened.
- `runChatModal`'s body (behind `pfm chat modal`) — dispatch line confirmed, body not read.
- The precise line-by-line boundary of which verbs `chat_command.go` vs. `headless_command.go` each own natively (both files were read, but not cross-diffed exhaustively for this specific question).

## Coverage

- Git stamp: HEAD `00da35b5`, working tree clean (0 dirty lines), at trace start.
- Tracers dispatched: 7 (6 primary threads + 1 mop-up). Reports received: 7. Reconciled: yes, no missing report.
- Mop-up: run (not skipped) — closed 5 named gaps: `name-sync` disposition, 8 headless-forwarded chat verbs (new/last/status/stream/inject/self-compact/watch/modal), `chat_new_command.go`'s role (`chat new`), exact `install` flag spellings (verbatim-verified), and `goal`/`kill-exit` absence checks (verbatim grep, zero hits).
- Files in boundary read or dispositioned: 55 of 56 `pfm/cmd/pfm/*.go` non-test files (all except none knowingly skipped; `pipeline.go` read only enough to disposition NOT-MINE, not deep-walked for a hidden capability — named above), plus `main.go`, `commands.go`, `pfm/internal/harvest/README.md`, `pfm/internal/headless/README.md` (`pfm/README.md` does not exist), plus the eponymous main file of all 21 named `internal/*` packages for top-comment context (no `doc.go` exists in any of them, confirmed by directory listing).
- Commands/subcommands/flags found: **~90 distinct user-facing entries** across top-level commands (≈60 rows counting flag variants), `pfm internal` hook plumbing (19 verbs), `pfm chat` verbs (24 verbs), MCP chat tools (18), and MCP harvester tools (6) — see per-section tables above for the exact enumerated set; this total is a rollup of the tables, not a substitute for them.
- Never claimed "clean" from a partial read: `doctor.go` (54k, partial-read but its 30-check dispatch fully enumerated via targeted grep+read of the dispatch body, lines 59-303); `chat_reload_command.go` (`runChatReloadWithRuntime` and `runChatReloadWorkerWithRuntime` covered, worker internals not); `chat_satellite_command.go` (32k, ~250/~950 lines read, all 6 dispatched verbs covered).
