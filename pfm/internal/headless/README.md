# Headless execution

## Contents

- [Common options](#common-options)
- [Results and native forwarding](#results-and-native-forwarding)
- [Context and engine capabilities](#context-and-engine-capabilities)
- [Internal entry point](#internal-entry-point)

`pfm headless exec` runs Claude or OpenCode without a terminal. `--engine codex` is an alias for OpenCode in this command; both `codex` and `opencode` use the OpenCode binary and account. Interactive Codex, account probes, and the internal Codex runner retain their native engine. Both execution backends accept the common PFM options. `pfm headless --engine …` is equivalent. Existing `pfm headless run`, `ask`, and other chat aliases still address managed chats; `pfm chat` is their direct interface.

```sh
pfm headless exec --engine claude --prompt 'Explain this result.'
pfm headless exec --engine codex --prompt-file task.md --output-format json
pfm headless exec --engine opencode --model openai/gpt-5.6-luna --prompt 'Explain this result.'
pfm headless exec --engine claude --system-file judge.md --schema answer.schema.json \
  --sealed --timeout 600 --output-format json < case.txt
```

## Common options

| Option | Meaning |
| --------------------------- | ---------------------------------------------------- |
| `--engine` | `claude`, `codex`, or `opencode`; `codex` routes to OpenCode; default from PFM config |
| `--model`, `--effort` | Override configured model and reasoning effort |
| `--account` | Configured account ID; default first account |
| `--config-dir` | Select a configured account by its directory |
| `--prompt`, `-p` | Prompt text; otherwise read stdin |
| `--prompt-file` | Read raw prompt text from a file |
| `--task`, `--task-file` | Task text or file, using labeled task framing |
| `--files`, `--labels` | Zero or more files and labels; default basenames |
| `--system`, `--system-file` | Replace the system prompt with text or file contents |
| `--schema`, `--json-schema` | JSON Schema file or inline JSON |
| `--tools` | Tool allowlist; an explicit empty value disables ordinary tools |
| `--setting-sources` | Comma-separated `user`, `project`, `local`; an explicit empty value suppresses all three |
| `--strict-mcp-config` | Disable inherited MCP servers |
| `--no-session-persistence` | Discard the run's session storage |
| `--sealed` | Replacement system prompt, scratch directory, disabled ordinary tools and inherited settings/MCP, ephemeral session |
| `--cwd` | Working directory |
| `--timeout` | Wall-clock seconds; default 600, zero unlimited |
| `--output-format` | `text`, `json`, or `native` |
| `--out` | Write validated structured output or the text answer |
| `--receipt` | Append a content-free JSON execution receipt |
| `--env` | Repeatable `KEY=VALUE` environment override |
| `--engine-arg` | Repeatable native engine argument |
| `--allow-unsupported` | Retained for compatibility; does not weaken OpenCode's explicit controls or input validation |

`--system-prompt` and `--system-prompt-file` alias the system options. Prompt/task, system, and schema each accept one input source.

Prepared-file callers can use the lab's argument layout directly:

```sh
pfm headless exec --engine claude --sealed \
  --files cases.md rubric.md --labels cases rubric \
  --task 'Apply file 2 to every case in file 1.' \
  --system-file judge.md --schema verdict.schema.json --out verdict.json \
  --model sonnet --effort high --timeout 600 --account 1 --receipt receipt.jsonl
```

`--files` and `--labels` consume values until the next option. Labels must match the file count; omitted labels use basenames. File contents are placed between numbered `===== FILE N: label =====` / `===== END FILE N =====` delimiters, followed by `TASK: instruction`. `--task-file` trims surrounding whitespace from the instruction. A task without files still receives the `TASK:` framing. Raw prompt options without file options remain unchanged. Files are read before launching the engine; receipts contain execution metadata and no input content. Both OpenCode selectors accept the same prepared-file options.

## Results and native forwarding

`--output-format json` emits one common envelope: `engine` (`cc` or `ox`), `model`, `effort`, `result`, optional `structured_output`, optional `usage`, `total_cost_usd`, `exit_code`, `is_error`, and `timeout`. Usage exposes `input_tokens`, `cached_input_tokens`, `cache_creation_input_tokens`, and `output_tokens`. Unknown cost is `null`. Engine retry and warning messages appear in optional `diagnostics` and on stderr; recovery requires an explicit terminal success event. A schema request is validated locally before an output file is written; external schema references are not fetched.

`--output-format native` streams the engine's stdout and stderr directly and accepts streaming stdin. Arguments after `--` pass to the selected engine unchanged, so native features do not require another PFM release:

```sh
pfm headless exec --engine claude --output-format native --prompt 'Read the sources.' \
  -- --output-format stream-json --verbose --allowedTools Read
pfm headless exec --engine opencode --output-format native --prompt 'Inspect this repository.' \
  -- --format json
```

Native forwarding preserves the engine's wire format. It does not normalize an engine-specific event protocol into the common JSON envelope. `--out` requires normalized output. PFM owns timeout and cancellation in either mode, including termination of the child process group.

## Context and engine capabilities

Claude accepts `--tools`, `--setting-sources`, `--strict-mcp-config`, and `--no-session-persistence`. `--sealed` requires a replacement system prompt and uses Claude's `--safe-mode` to disable user/project customizations, plus a scratch working directory, disabled tools and inherited settings/MCP, and no session persistence. Administrator policies still apply. Native overrides that could weaken this contract are refused.

Both `--engine codex` and `--engine opencode` use OpenCode's `run` protocol, validated against OpenCode 1.18.18. PFM supplies a private plugin for system-prompt replacement, structured output, tool availability, and MCP restrictions. The plugin must initialize before inference; failure to load it is an execution error. Structured output is also validated locally before PFM writes `--out`.

For OpenCode, explicit settings-source selection uses this mapping. Omitting the option retains native discovery; an empty value selects none.

| Source | OpenCode configuration |
| --- | --- |
| `user` | Global XDG configuration and the user's `.opencode` directory |
| `project` | Nearest project `opencode.jsonc`, otherwise `opencode.json` |
| `local` | Nearest project `.opencode` directory |

`--tools` preserves existing permission rules for selected tools, including file and Git restrictions. Claude tool names are translated to OpenCode IDs; `LS` uses `read`, and `NotebookEdit` is rejected because OpenCode has no corresponding built-in tool.

Permission requests are rejected in headless mode. Forwarding `--auto`, `--yolo`, or `--dangerously-skip-permissions` approves requests once; explicit permission denials still apply.

OpenCode installs plugin dependencies when its configuration directory is new. Sealed runs and an empty settings selection use fresh configuration directories, so this installation can recur and take several minutes. Startup is included in the execution timeout; a plugin that fails to initialize produces an error.

Project searches stop at the repository root. Selecting `project` alone does not enable `.opencode` directory discovery. Native arguments that conflict with explicit controls are rejected.

`--no-session-persistence` uses private session storage and removes it after the run. Subscription credentials are retained separately; a refreshed credential store is written back only when the original store has not changed. `--sealed` also isolates the working directory and inherited configuration. A schema request retains OpenCode's `StructuredOutput` submission tool even when ordinary tools are disabled.

A bare OpenAI model name such as `gpt-5.6-luna` becomes `openai/gpt-5.6-luna`; provider-qualified names pass through. `--effort` selects the OpenCode model variant and sets the OpenAI reasoning effort. The `codex` alias retains its configured model and effort preferences, but resolves the executable, account ID, and `--config-dir` against OpenCode. Results and receipts report the actual engine as `ox`.

Authenticate the selected OpenCode account with `opencode auth login` and choose OpenAI's subscription login. Both selectors reuse that OpenCode OAuth store. Native Codex credentials are not automatically imported. `--config-dir` names a configured OpenCode data directory, not a Codex configuration directory.

```sh
pfm headless exec --engine codex --sealed \
  --system-file judge.md --schema verdict.schema.json --task-file task.md \
  --out verdict.json --output-format json
```

## Internal entry point

Every isolated harness process owned by PFM passes through `Run` in `pfm/internal/headless/run`. Engine adapters translate options and output; callers own domain preparation. `internal/ask` prepares harvested content and chat excerpts, then calls this runner. Harness-prompt capture, credential refresh, and Walker equivalence use the same entry point. External callers can adopt the CLI without maintaining a harness subprocess implementation.

Exit codes: `0` success, `2` invalid CLI input, `3` timeout, `4` execution or I/O failure, `5` structured-output validation failure. A failed call never becomes an empty successful answer.
