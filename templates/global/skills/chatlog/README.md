# pfm tokens

One read-only pfm verb over all three engines' transcripts, one pricing table: **where did the tokens go?** No report is written outside `--out` / `--view` / `--briefs`; the daily price refresh it shares with `pfm model-cost` may rewrite the clone's `prices.json` and its check stamp.

```bash
pfm tokens            # last 24h, every project
pfm tokens --since 3d --project <substr>
pfm tokens --codex    # Codex CLI threads
pfm tokens --opencode # OpenCode sessions
pfm tokens --tree <session-or-agent-id> --worktree <dir> --base <sha>
```

A RUN is one transcript file: one main chat loop, one sub-agent, one Codex rollout thread, or one OpenCode session. A FAMILY is a main chat plus every sub-agent it spawned.

## Flags

| Flag | Purpose |
| --- | --- |
| `--since 24h\|3d` | Window (default `24h`). Cuts per call **and** skips files older than the window. |
| `--root <dir>` | A Claude transcript root read in place of pfm's resolved roots (repeatable). A root that will not resolve is a hard error. |
| `--project <substr>` | Keep runs whose cwd contains the substring. |
| `--family <substr>` | Drill into one family — matches a chat title, an agent type, a sub-agent's spawn description, or a session-id prefix. |
| `--family-rx <re>` | `--briefs`: only the families whose titles match the regular expression, case ignored (default: every family). |
| `--session <sid-prefix>` | Restrict every section to one session. The selector a sub-agent-orchestrated run has, where a chat title does not exist. Ownership indexing still reads every transcript discovered under the roots; the gaps line names that scope and any read failures. |
| `--top <n>` | Rows per section (default 12). |
| `--out <file>` | Full JSON dataset. |
| `--codex` | Read Codex rollouts instead of Claude transcripts. |
| `--codex-root <dir>` | Read this one Codex home in place of every Codex home pfm resolves (one per configured Codex account). |
| `--opencode` | Read OpenCode sessions instead; refused beside `--codex`, `--tree`, `--root`, `--family`, `--family-rx`, `--session`, `--out`, `--view` or `--briefs` (exit 2). |
| `--opencode-root <dir>` | Read the `opencode.db` in this directory in place of pfm's OpenCode roots; refused without `--opencode` (exit 2). |
| `--tree <id>` | One Claude spawn tree's written-per-read measure (below). |
| `--worktree <dir>` / `--base <sha>` | `--tree`: count the code lines the checkout landed against the base; the pair goes together. |
| `--json` | `--tree`: print the JSON document instead of the text. |
| `--view <file>` / `--briefs <file>` | Compact per-project page data / the briefs-and-behaviour dataset. |
| `--host-label <name>` | `--view`: the host name the page shows (default: this host's name). |
| `--prices <file>` | Price from a saved table instead of pfm's current one (below). |

Without `--root`, the Claude roots are pfm's resolved roots (`PFM_CLAUDE_ROOTS`, else `~/.claude/projects`, which each account's `~/.cc/{N}/projects` links to). Roots are resolved through symlinks and de-duplicated, and each file is de-duplicated by its last three path segments — one project tree shared by several accounts is never double-counted.

## What it reads

**Claude Code.** `{root}/{projectSlug}/{conversationId}.jsonl` (the main loop) and `{conversationId}/subagents/agent-{agentId}.jsonl` (+ `.meta.json` for `agentType`, `description`, `spawnDepth`), including nested `subagents/workflows/wf_*/agent-*.jsonl`.

- Usage rides on every `assistant` line at `message.usage`; the model is `message.model`.
- **Dedup is mandatory**: streaming writes several lines per API call. The tool keys on `message.id`, else a namespaced `requestId`, else `uuid`, else the file and line, and keeps the last — summing raw lines overcounts 2-3x. Records lacking both response identifiers are counted as uncertain in the gaps line: a per-record fallback may include streamed copies. A forked or resumed session copies earlier responses into its own transcript: each response is billed once, to its named origin or first unmarked holder in path order within the ownership scan, in every mode; the gaps line counts copies and transcripts holding only copies. An inherited line's `forkedFrom` mark, or the same call in the origin a copy names, proves a copy; a call several transcripts hold with no such evidence (forked sub-agents) is billed once, to the first holder in path order; a `session_id` mismatch alone proves nothing, since a fork's own calls may still name its origin.
- `tool_use` blocks are paired with their `tool_result` so every tool call, its size, its duration and its `is_error` are known — that is what makes the per-agent measures possible.
- A `system`/`compact_boundary` line, or a context that shrinks below 60% of what is carried, resets the replay: that is a compaction.

**Codex CLI.** `{home}/sessions/**/rollout-*.jsonl` and `{home}/archived_sessions/**` of every Codex home pfm resolves, or of `--codex-root`. A home's root itself is deliberately not scanned — it holds `rollout-backup-*.jsonl` copies that would double-count a thread. Symlinked rollouts are skipped.

- Line 1 is `session_meta`: `id` (thread), `session_id` / `parent_thread_id` (the parent thread), `context_window.window_id`, `cwd`, `thread_source`, and `source.subagent` (the role) for a sub-agent thread. A Codex sub-agent writes its **own** rollout, so it is attributed individually.
- `turn_context` carries the model; `token_count` carries the counters; `custom_tool_call` / `function_call` and their `*_output` carry the tools, their output size and their failures.
- For the `--codex` table only matching lines are decoded (rollouts reach 110 MB).

**OpenCode.** `opencode.db` under each of pfm's OpenCode roots (or `--opencode-root`), opened read-only through pfm's one OpenCode store reader (`pfm/internal/index`): every session, each with its messages created inside the window, in one statement; a message older than the window is not counted, though its session was updated inside it. When no store can be read the run exits 1 (`no OpenCode store could be read: {db} is missing`, or `{db}: {error}`), as it does with no configured root (`no OpenCode root is configured; pass --opencode-root DIR`).

- Each assistant message carries its own token record — uncached input, output, reasoning (outside output), cache read, cache write — so a session is the plain sum of its messages, each priced at its own model from pfm's table. OpenCode records no 5m/1h split, so a cache write on a Claude model prices at the 5-minute rate and is counted in the gaps line.
- OpenCode's own recorded `cost` prints as `OC USD` beside the estimate `EST USD` and never stands in for it: under a subscription it is $0, under an API key it is the provider's rate.

## Counting and pricing

- **Codex counters reset.** `info.total_token_usage` is cumulative but restarts on resume and on compaction, and duplicate events re-emit an identical cumulative. So: dedupe on the cumulative, split the thread wherever it drops, sum **each segment's peak**. The final counter alone undercounts a long thread by orders of magnitude; summing per-turn deltas double-counts. Cached input is a subset of input and bills at the cached rate; output already includes reasoning.
- **Claude, per response:** input × input rate + output × output rate + `cache_creation.ephemeral_5m_input_tokens` × the published 5m write rate + `ephemeral_1h_input_tokens` × the 1h write rate + `cache_read_input_tokens` × the cache-read rate, at that response's own model and prompt length (input + cache reads + cache writes). Any part of `cache_creation_input_tokens` the TTL breakdown does not cover bills as the default 5-minute TTL; a write with no breakdown is counted in the gaps line. Zero top-level counts are priced from `usage.iterations[]`.
- **Every dollar is traced to the context that caused it**: the replay charges `[0, cache_read)` at the read rate, `[cache_read, +cache_write)` at the write rate and the tail at 1x, then attributes each slice to the category that put it there.
- **Prices load once, before transcripts are read, through `pfm model-cost`'s own path:** pfm's one price table (`pfm/internal/pricing/prices.json` in the Professor clone, plus any `pfm.prices.json` override), refreshed from the official Anthropic and OpenAI pages at most once a day (`pfm model-cost --force` refreshes now; `PFM_PRICES_OFFLINE=1` never fetches). A refresh pfm could not complete leads the data-gaps line, naming the cause and the fetch time of the table served. A table that cannot load exits 1 with the cause; no static fallback exists.
- **Exact IDs and dimensions:** a model id resolves by pfm's one rule — case, provider prefix, Bedrock and Vertex suffixes and Claude dots normalized, then its exact key, else its key without a trailing snapshot date; a family or substring never inherits another model's price. Estimates use the Standard input, output and cache rates, both Claude cache TTLs, and a row's `long` tier when the call's context is above its threshold. A missing rate for a nonzero usage dimension renders that call `n/a`.
- **Saved tables:** `--prices FILE` reads a saved `pfm model-cost --json --all` document, or the `prices.json` file itself, and never refreshes; a document row without `source` is refused. A document's `override: {path, rows}` leads the data-gaps line. Every report's `pricing:` line names the saved table or current list prices, the table's fetch time and its providers. These current-price estimates are not historical billing truth.
- **Codex context estimates:** the segment-peak counting rule stays unchanged. Its aggregate tokens are estimated at the run's peak-context price tier; per-call historical costs cannot be reconstructed from these counters. A mixed-model rollout is still priced at its last model and named in the gaps line.

## Honesty rules

- **`data gaps:`** prints on every report: malformed lines, calls with no timestamp, dropped synthetic/zero-usage calls, unpriced calls with their models, cache writes with no tier split, calls copied from another transcript, duplicate files, and read errors. `data gaps: none` means the scan was clean.
- **An unpriced model renders `n/a`, never `$0`.** Its tokens stay in every token total; its dollars stay out of every dollar total; the gaps line names the model.
- **A read error is a failure to look, not an empty result.** It is named in the gaps line and the process exits 1 (a usage error exits 2); an unresolvable `--root` is a hard error before anything is reported.
- **`CROSS-CHECK`** compares the estimate against the harness's own `cost-state` line for chats wholly inside the window, and prints the estimate with published context tiers applied. When no chat qualifies it says the estimate is UNCHECKED on this host.

## `--tree <id>`

One Claude spawn tree: a session id takes the main session and every sub-agent of it; an agent id (an `agent-` prefix is accepted) takes that sub-agent and its descendants, each placed through its `agent-{id}.meta.json` `parentAgentId`. The id matches exactly under every `--root`, else `$CLAUDE_CONFIG_DIR/projects` and pfm's resolved Claude roots; two matching transcripts are an error. No window applies: every call in the tree counts. `--tree` refuses `--since`, `--project`, `--family`, `--family-rx`, `--session`, `--top`, `--codex`, `--codex-root`, `--opencode`, `--opencode-root`, `--view`, `--briefs` and `--host-label` (exit 2).

- Per agent: depth from the tree root, type (`main` for the session), model, input tokens (uncached, cache read, cache write), dollars, written bytes, read bytes and their ratio `W/R`. Written bytes are Write content, Edit and MultiEdit `new_string` and NotebookEdit `new_source` whose target is under the write scope and never under `/tmp` or `/private/tmp`; read bytes are the text of every `tool_result` the agent received. One unpriced call renders the agent's dollars `n/a`.
- Write scope: `--worktree`, else the first `cwd` the session transcript records, else the root agent's.
- Per agent type: agents, input tokens, written and read bytes, `W/R`.
- Per tree: the totals, the silent share (the input-token share of agents that wrote nothing under the scope), and with `--worktree DIR --base SHA` the landed code lines — `git diff --numstat -z BASE` plus untracked files, with test, doc, generated, lock and binary files counted apart (rules `landed-rules/1`, `pfm/internal/tokens/landed.go`) — and landed lines per million input tokens. Without the pair both read `NOT MEASURED`.
- An agent whose meta cannot be read stays a row marked unreadable, counts in `notRead` and in no total, and the run exits 1.
- `--json` prints the document instead of the text; `--out FILE` writes it and adds a `full data →` line. Its keys, in order: `excluded`, `tree`, `types[]`, `agents[]` (`pfm/internal/tokens/tree.go` lists every field).

## Tests

The tests live in `pfm/internal/tokens/` and run the verb over the synthetic JSONL under `testdata/fixtures/` — a Claude main chat with four sub-agents (a re-read, a failed Bash, a compaction, 85 calls, an unpriced model, a synthetic call, a lone `sleep`), a Codex rollout whose counter resets after a compaction with three empty `write_stdin` polls, an OpenCode store seeded at test time (`opencode_test.go`: per-message pricing, `--project`, a missing or unreadable store), and a spawn tree (`testdata/fixtures/tree/`: a session, a foreman, two workers and a tracer) for `--tree`. `testdata/golden/` and `testdata/tree/` hold every mode's recorded output, which each run reproduces byte for byte. Every run reads the synthetic saved document `testdata/fixtures/model-cost.json`; no network is used by tests. Id resolution is tested in `pfm/internal/pricing` against its shared cases (`testdata/resolve-cases.json`).
