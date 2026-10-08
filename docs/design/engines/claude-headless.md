# Claude headless runs

Every non-interactive Claude Code run pfm makes — `claude -p` for `pfm headless exec`, the `ask` engine, the credential-refresh ACK and the doctor's harness-prompt probe — goes through `headlessrun.Run` (`pfm/internal/headless/run/run.go`). This file holds its argv, its sealed mode, each caller, the environment it strips and sets, the harness-prompt drift probe, and how session identity is kept out of children. Interactive tmux launches are in [claude-launch.md](claude-launch.md).

## Contents

- [Decisions](#decisions)
- [The argv](#the-argv)
- [Sealed mode](#sealed-mode)
- [Callers](#callers)
- [Environment](#environment)
- [Harness-prompt drift probe](#harness-prompt-drift-probe)
- [Session identity](#session-identity)

## Decisions

- **One runner.** Every `-p` run is built by `arguments()` and `setEnvironment` in `run.go`; a caller varies only the request fields and its own `Args`.
- **Headless reads no launch value from `pfm.config.json`.** For `pfm headless exec`, model and effort come from its flags. Theme, sub-agent caps, cache, truecolor, lean prompt, the web-search cap and `autoCompactWindow` come from `Request.Settings` or `--pfm-settings PATH`, a JSON file shaped like the config's `claude` block (conventionally `pfm.settings.json`). `--pfm-settings` keys are the supported knob names in `claudelaunch.Knobs`, and `pfm headless exec` renders them through the registry. A value not passed is not sent, except the function-hooks constant and the auto-compact window (default `100000`), which are always sent. The config is read only for where the harness is: the account roster (to turn `--account N` into its config dir), each account's `claude` binary, and the default engine when `--engine` is absent. Constants of the launch registry — the hygiene unsets and `outputStyle` — still apply ([claude-launch.md](claude-launch.md#knobs)).
- **Internal callers state their values.** The ask engine (status and summary helpers), the credential-refresh ACK and the doctor probe build their requests with explicit values — the ask engine from the config's `ask` block, the others from constants; `Run` itself never reaches into the config for a launch value.
- **The prompt travels on stdin, the system prompt in a file.** `Run` writes any system prompt to a `pfm-headless-system-*.txt` file and passes `--system-prompt-file` (`run.go:458-469`, `:606-611`), and feeds the user prompt on stdin (`:519-521`) — neither lands in argv, where `ps` would show it.
- **An explicit environment is the caller's word.** A run with no `Env` strips the provider and endpoint variables; a caller that passes `Env` keeps them (`run.go:697-724`). That is the only way to aim a headless run at a proxy.
- **pfm has no gateway mode.** Nothing in pfm sets `ANTHROPIC_BASE_URL` except the doctor's capture sink; every other occurrence is a strip list.

## The argv

`arguments()`, Claude branch, in order (`run.go:589-686`):

| # | Word | Condition |
| --- | --- | --- |
| 1 | `-p` | always |
| 2 | `--safe-mode` | `Sealed` |
| 3 | `--model <m>` | `Model` non-empty — from the caller's flag |
| 4 | `--effort <e>` | `Effort` non-empty |
| 5 | `--output-format json` | not `Native` |
| 6 | `--system-prompt-file <f>` | a system prompt given (`--system-prompt` is unreachable through `Run`) |
| 7 | `--json-schema <schema>` | `Schema` set; `validateSchema` runs first (`:296-299`) |
| 8 | `--tools <list>` | `Tools` set (`""` disables every tool) |
| 9 | `--setting-sources <list>` | `SettingsSources` set (`""` disables inherited settings files) |
| 10 | `--strict-mcp-config` | `StrictMCP` |
| 11 | `--no-session-persistence` | `NoSessionPersistence` |
| 12 | caller `Args` | verbatim (`:676`) |
| 13 | `--settings <payload>` | always, unless `Args` carries `--settings` — `{"env":{"CLAUDE_CODE_AUTO_COMPACT_WINDOW":"100000","CLAUDE_CODE_ENABLE_FUNCTION_HOOKS":"1"},"outputStyle":"default"}` plus passed values from `Request.Settings` or `--pfm-settings` |

The process runs in its own process group (`:528`). `--permission-mode`, `--add-dir`, `--agent`, `--input-format` and `--append-system-prompt` are never emitted.

## Sealed mode

`pfm headless exec --sealed` makes `Resolve` force `Tools=""`, `SettingsSources=""`, `StrictMCP`, `NoSessionPersistence` and a scratch working directory (`run.go:260-266`); it requires a system prompt (`:246-249`) and refuses native args (`:267-268`). The result is a run with no tools, no settings files, no MCP servers, no transcript and no project tree — an extractor, not an agent.

For Codex, which cannot honour these controls, the run is refused before launch unless `--allow-unsupported` is given (`run.go:271-294`); Claude never takes that branch.

## Callers

### pfm headless exec

`pfm/cmd/pfm/headless_exec_command.go:29`. `--output-format` defaults to `text`, and only `native` sets Native, so text and json both add `--output-format json` (`:77-81`, `:134`). `--pfm-settings` reads and validates a Claude settings file (`:149-167`). `--tools` and `--setting-sources` pass only when present (`:228-233`); `--system`/`--system-file` set the system prompt (`:207-220`); `--schema`/`--json-schema` the schema (`:211-227`); `--engine-arg` and anything after `--` become `Args` (`:136`); any `--env K=V` makes the environment explicit (`:234-253`). The fence lanes drive it this way (`infra/fence/lanes/O2.sh:433-449`).

### ask

`pfm/internal/ask/ask.go:185-195`, used by the status and summary helpers (`pfm/internal/headless/statusask.go:138`, `pfm/internal/headless/summary.go:89`). Native, Args `--output-format text`: `claude -p [--model M] [--effort E] --output-format text --settings {…}`.

### Credential-refresh ACK

`pfm/internal/stats/limits.go:871-881`: `claude -p --model claude-haiku-4-5 --max-turns 1 --settings {"env":{"CLAUDE_CODE_AUTO_COMPACT_WINDOW":"100000","CLAUDE_CODE_ENABLE_FUNCTION_HOOKS":"1","CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT":"1"},"outputStyle":"default"}` with prompt `ACK` and no explicit environment — the cheapest request that makes Claude Code refresh an account's credentials.

### Doctor harness probe

See [Harness-prompt drift probe](#harness-prompt-drift-probe). `pfm doctor` also runs `claude --version` directly (`pfm/internal/doctor/harness_prompt.go:384`).

## Environment

`setEnvironment` (`run.go:689-741`). The base is `os.Environ()`, or the caller's `Env` (`:497-501`). The Claude names in both drop lists and `CODEX_THREAD_ID` come from the registry; `TMUX`, `TMUX_PANE` and `OPENAI_*` are the runner's own additions.

- **Always dropped** (`:696-707`, plus the engine `HomeEnv`): `CLAUDE_CODE_SESSION_ID`, `CLAUDECODE`, `CLAUDE_CODE_CHILD_SESSION`, `CLAUDE_CONFIG_DIR`, `CODEX_THREAD_ID`, `TMUX`, `TMUX_PANE`.
- **Dropped unless the environment is explicit** (`:697-724`): `CLAUDE_PROJECT_DIR`, `ENABLE_PROMPT_CACHING_1H`, `FORCE_PROMPT_CACHING_5M`, `CLAUDE_CODE_PROMPT_CACHE_TTL`, `CLAUDE_CODE_SUBAGENT_PROMPT_CACHE_TTL`, `CACHE_LIVE_CONTROL_MAIN_TTL`, `CACHE_LIVE_CONTROL_AGENTS_TTL`, `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_API_KEY`, `ANTHROPIC_MODEL`, `ANTHROPIC_SMALL_FAST_MODEL`, `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT`, `CLAUDE_CODE_AUTO_COMPACT_WINDOW`, `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC`, `CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK`, `CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY`, and the registry's `WireSettings` environment targets (caps, web-search cap, truecolor and native cursor).
- **Set:** `CLAUDE_CONFIG_DIR={config dir}` when an account resolved. Sub-agent caps, web-search cap, truecolor and simple prompt reach Claude through the `--settings` `env` block when passed through `Request.Settings` or `--pfm-settings`; function hooks and the auto-compact window always reach it through that block.

## Harness-prompt drift probe

`pfm doctor` proves Claude Code's own built-in system prompt has not changed under the fleet since it was last reviewed.

- **Baselines.** `pfm/harness-prompts/claude/baselines/` holds one reviewed capture per model — `harness-original-v{cli}.md` (Sonnet) and `harness-opus-v{cli}.md` (Opus) — each with a `.model` provenance file and a `.sha256` pin, captured in print mode with dynamic sections excluded (`pfm/harness-prompts/README.md:65-84`). The probe reads them from the repo, `{clone}/pfm/harness-prompts/claude/baselines/`; nothing is staged. The checked aliases are `HarnessPromptModels = {{"sonnet", "harness-original"}, {"opus", "harness-opus"}}` (`pfm/internal/doctor/harness_prompt_baselines.go:24`).
- **Capture.** A local sink on `127.0.0.1:0` answers every request with HTTP 400 and keeps the first `/messages` body (`harness_prompt.go:396`, `:602-617`). The probe runs `WithoutAccount`, Native, a 20-second timeout, stdin `/dev/null`, in a throwaway config dir that is also its cwd (`:369`, `:413-428`, `:472`): the CLI loads `.claude/settings.json` in its cwd as project settings, and their `env` beats the probe environment's sink URL — run from `~`, that file is the user's own settings.

  ```text
  claude -p --model <alias> x --output-format json --strict-mcp-config --mcp-config {"mcpServers":{}} --max-turns 1 --exclude-dynamic-system-prompt-sections --settings {"env":{"CLAUDE_CODE_AUTO_COMPACT_WINDOW":"100000","CLAUDE_CODE_ENABLE_FUNCTION_HOOKS":"1"},"outputStyle":"default"}
  ```

- **Probe environment** (`claudelaunch.ProbeEnv`, `harness_prompt.go:436,482`): strips the registry's hygiene names; pins `ANTHROPIC_BASE_URL=<sink>`, `ANTHROPIC_API_KEY=pfm-doctor-sink`, `ANTHROPIC_AUTH_TOKEN=pfm-doctor-sink`, `CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT=0`, `FORCE_PROMPT_CACHING_5M=1`, `CLAUDE_CONFIG_DIR=<tmp>`.
- **Compare.** The captured system blocks are joined, normalized (the CLI version, model ids and names, and the environment identity and cutoff lines are masked, `:161-184`), hashed, and compared with the pinned SHA-256 (`:202-205`).

| Verdict | Meaning |
| --- | --- |
| `matches baseline <name>` | unchanged |
| `DRIFT live=… baseline=…` | the built-in prompt changed; up to 20 section added/removed/changed lines follow — review before re-pinning |
| `CANNOT CAPTURE … — drift unknown` | the CLI answered from the real endpoint and ignored `ANTHROPIC_BASE_URL` |
| `CHECK FAILED to run … — drift unknown` | the probe failed, including no request reaching the sink |
| `BASELINE UNAVAILABLE … — {state}; update or restore the clone at {dir}` | a baseline file is missing, unreadable, malformed, has a malformed digest or is inconsistent; `path=` and `error=` name its cause |
| `BASELINE UNAVAILABLE … dir=(unresolved) …` | no source repo recorded: run `pfm install` from the clone; recorded clone unusable: restore it, or run `pfm install` from a working clone |
| `skipped (no Claude Code binary installed)` | nothing to probe |

The header names what stays unchecked: `unchecked=active-chat,fable,codex` (`printModelHarnessPromptDoctorWithDeps` in `harness_prompt_baselines.go`).

## Session identity

pfm reads `CLAUDE_CODE_SESSION_ID` to know which chat is calling — the engine's `SessionEnv` (`pfm/internal/engine/builtin.go:44`), `whoami` (`pfm/internal/resolve/whoami.go:19`, `:154`), `kill`, `chat find`, inject's sender id, and the caller-engine check that consults it before `CODEX_THREAD_ID` (`pfm/cmd/pfm/chat_caller_engine.go:21`). It never sets it, and strips it with `CLAUDECODE` and `CLAUDE_CODE_CHILD_SESSION` from every child it starts: a probe fired from inside a chat must not inherit that chat's identity or endpoint (`pfm/internal/reap/busy.go:95-98`), and a fork's session id is always explicit (`pfm/internal/action/headless.go:128-130`).
