# Headless execution

`pfm headless exec` runs Claude or Codex without a terminal. Both engines accept the same PFM options. `pfm headless --engine …` is equivalent. Existing `pfm headless run`, `ask`, and other chat aliases still address managed chats; `pfm chat` is their direct interface.

```sh
pfm headless exec --engine claude --prompt 'Explain this result.'
pfm headless exec --engine codex --prompt-file task.md --output-format json
pfm headless exec --engine claude --system-file judge.md --schema answer.schema.json \
  --sealed --timeout 600 --output-format json < case.txt
```

## Common options

| Option | Meaning |
| --------------------------- | ---------------------------------------------------- |
| `--engine` | `claude` or `codex`; default from PFM config |
| `--model`, `--effort` | Override configured model and reasoning effort |
| `--account` | Configured account ID; default first account |
| `--config-dir` | Select a configured account by its directory |
| `--prompt`, `-p` | Prompt text; otherwise read stdin |
| `--prompt-file` | Read raw prompt text from a file |
| `--task`, `--task-file` | Task text or file, using labeled task framing |
| `--files`, `--labels` | Zero or more files and labels; default basenames |
| `--system`, `--system-file` | Replace the system prompt with text or file contents |
| `--schema`, `--json-schema` | JSON Schema file or inline JSON |
| `--cwd` | Working directory |
| `--timeout` | Wall-clock seconds; default 600, zero unlimited |
| `--output-format` | `text`, `json`, or `native` |
| `--out` | Write validated structured output or the text answer |
| `--receipt` | Append a content-free JSON execution receipt |
| `--env` | Repeatable `KEY=VALUE` environment override |
| `--engine-arg` | Repeatable native engine argument |
| `--allow-unsupported` | Continue without unsupported common controls, with diagnostics |

`--system-prompt` and `--system-prompt-file` alias the system options. Prompt/task, system, and schema each accept one input source.

Prepared-file callers can use the lab's argument layout directly:

```sh
pfm headless exec --engine claude --sealed \
  --files cases.md rubric.md --labels cases rubric \
  --task 'Apply file 2 to every case in file 1.' \
  --system-file judge.md --schema verdict.schema.json --out verdict.json \
  --model sonnet --effort high --timeout 600 --account 1 --receipt receipt.jsonl
```

`--files` and `--labels` consume values until the next option. Labels must match the file count; omitted labels use basenames. File contents are placed between numbered `===== FILE N: label =====` / `===== END FILE N =====` delimiters, followed by `TASK: instruction`. `--task-file` trims surrounding whitespace from the instruction. A task without files still receives the `TASK:` framing. Raw prompt options without file options remain unchanged. Files are read before launching the engine; receipts contain execution metadata and no input content. Codex accepts the same prepared-file options with its own model, subject to the isolation limits below.

## Results and native forwarding

`--output-format json` emits one common envelope: `engine` (`cc` or `cx`), `model`, `effort`, `result`, optional `structured_output`, optional `usage`, `total_cost_usd`, `exit_code`, `is_error`, and `timeout`. Usage exposes `input_tokens`, `cached_input_tokens`, and `output_tokens`. Unknown cost is `null`. Codex retry and warning messages appear in optional `diagnostics` and on stderr; recovery requires an explicit terminal success event. A schema request is validated locally before an output file is written; external schema references are not fetched.

`--output-format native` streams the engine's stdout and stderr directly and accepts streaming stdin. Arguments after `--` pass to the selected engine unchanged, so native features do not require another PFM release:

```sh
pfm headless exec --engine claude --output-format native --prompt 'Read the sources.' \
  -- --output-format stream-json --verbose --allowedTools Read
pfm headless exec --engine codex --output-format native --prompt 'Inspect this repository.' \
  -- --json --sandbox read-only
```

Native forwarding preserves the engine's wire format. It does not normalize an engine-specific event protocol into the common JSON envelope. `--out` requires normalized output. PFM owns timeout and cancellation in either mode, including termination of the child process group.

## Context and engine capabilities

Claude accepts `--tools`, `--setting-sources`, `--strict-mcp-config`, and `--no-session-persistence`. `--sealed` requires a replacement system prompt and uses Claude's `--safe-mode` to disable user/project customizations, plus a scratch working directory, disabled tools and inherited settings/MCP, and no session persistence. Administrator policies still apply. Native overrides that could weaken this contract are refused.

Codex supports system-prompt replacement, schema output, and `--no-session-persistence` through its own equivalents. Its CLI does not provide a complete empty-tool or inherited-context suppression mode. PFM refuses unsupported sealing, tool selection, settings-source suppression, and strict MCP requests by default. A read-only sandbox still exposes tools and is not a sealed judge.

`--allow-unsupported` explicitly bypasses those capability rejections. PFM omits unsupported tool/settings/MCP controls and reports each one in `diagnostics`, on stderr, and in the receipt. With `--sealed`, the supported parts still apply: a replacement system prompt, scratch working directory, ephemeral session, and Codex's `--ignore-user-config` / `--ignore-rules`. The run still has native tools and does not provide full sealing. Native arguments remain available for this opted-out Codex run, including provider configuration overrides; they can further change the engine's behavior. The override does not suppress input, schema, execution, or timeout errors. Claude's sealed runs continue to reject native overrides.

```sh
pfm headless exec --engine codex --sealed --allow-unsupported \
  --system-file judge.md --schema verdict.schema.json --task-file task.md \
  --out verdict.json --output-format json
```

## Internal entry point

Every isolated harness process owned by PFM passes through `Run` in `pfm/internal/headless/run`. Engine adapters translate options and output; callers own domain preparation. `internal/ask` prepares harvested content and chat excerpts, then calls this runner. Harness-prompt capture, credential refresh, and Walker equivalence use the same entry point. External callers can adopt the CLI without maintaining a harness subprocess implementation.

Exit codes: `0` success, `2` invalid CLI input, `3` timeout, `4` execution or I/O failure, `5` structured-output validation failure. A failed call never becomes an empty successful answer.
