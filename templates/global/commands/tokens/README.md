# token-ledger

Token attribution for both local agent harnesses — per-agent / per-operation for Claude Code sessions, per-session-thread for the Codex CLI (`--codex`) — parsed straight from the JSONL each harness writes locally. Zero dependencies (node: builtins only), READ-ONLY over transcripts, no network. Node 20+. Full human reference — Claude's own routing/invocation entry point is `SKILL.md`.

This is the "WHICH agent / WHICH operation burned the tokens" view that Claude Code's native OpenTelemetry **metrics cannot give** — `agent.name` is redacted to `"custom"` for user-defined sub-agents, so the JSONL files are the only local source of per-agent truth. (See the RR report that produced this tool.)

## Usage

Run from the repo root (the project slug is derived from the cwd):

```bash
# Most recent session for the current project (cwd-derived):
node .claude/commands/tokens/token-ledger.mjs

# Every session for this project (heaviest token burner = top row, sorted by cost):
node .claude/commands/tokens/token-ledger.mjs --all

# What did each workflow run cost? (one row per wf_* run, sorted by cost):
node .claude/commands/tokens/token-ledger.mjs --all --by-workflow

# Total one flight or feature (by label substring):
node .claude/commands/tokens/token-ledger.mjs --all --filter my-feature

# A specific conversation (by id or by path to its dir / main .jsonl):
node .claude/commands/tokens/token-ledger.mjs --session <session-id>

# Drill into one agent's individual API calls (by agentId OR label substring):
node .claude/commands/tokens/token-ledger.mjs --detail "BE developer"
node .claude/commands/tokens/token-ledger.mjs --session <id> --detail a26fc4c505ee2af1b

# Machine output:
node .claude/commands/tokens/token-ledger.mjs --json

# Extra root / project override:
node .claude/commands/tokens/token-ledger.mjs --root /some/other/.claude --project -Users-you-work-project
```

### Flags

| Flag | Purpose |
| ----------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--all` | Every session for this project (default is the most recent with sub-agents). |
| `--session <id\|path>` | One conversation, by id or by path to its dir / main `.jsonl`. |
| `--project <slug>` | Project slug override (default: slugified cwd). |
| `--root <dir>` | Extra transcript root (repeatable). |
| `--detail <id\|substr>` | List one agent's individual API calls in order. |
| `--by-workflow` | Group by workflow run (`wf_*`) — one row per run + a `(non-workflow agents)` summary row + TOTAL. |
| `--filter <substr>` | Restrict the per-agent table + totals to rows whose label or model id contains `<substr>` (case-insensitive); prints the match count. Composes with `--all` / `--session` / `--json`. |
| `--since <YYYY-MM-DD>` | Drop sessions older than that calendar day. |
| `--json` | Machine-readable output. |

### `--by-workflow` honesty caveat

`--by-workflow` groups every agent file under each distinct `wf_*` run directory. It captures **Workflow-engine runs** (e.g. `/deep-rr`) **exactly**.

It does **NOT** total a flight. `/flights:orchestrate-live` spawns each executor as a **session-level** sub-agent and `/flights:orchestrate-nested` runs them under one sub-agent — neither is a `wf_*` workflow run. Those land in the `(non-workflow agents)` summary row. To total one flight, use `--filter <flight-label>`.

Default scope is the most recent session **that has sub-agents** for the current project. The project is identified by slugifying the cwd (every `/` → `-`), matching Claude Code's own `projects/{slug}` naming.

## What it reads

Two transcript roots, auto-discovered:

1. `~/.claude/projects/{slug}/` — standard layout.
2. `~/.claude-sessions/s*/projects/{slug}/` — an optional multi-account session layout.

Where both roots are **hardlinks to the same inodes**, the tool de-duplicates sessions by `(dev, inode)` of the main file — it never double-counts a session seen under both roots.

Within a session:

- `{conversationId}.jsonl` — the **MAIN** conversation loop → its own row.
- `{conversationId}/subagents/agent-*.jsonl` — each sub-agent → one row.
- `{conversationId}/subagents/workflows/wf_*/agent-*.jsonl` — nested workflow sub-agents (a Workflow-engine run, e.g. `/deep-rr`) → one row each. See the `--by-workflow` honesty caveat above: a plain orchestrated flight is NOT a `wf_*` run.

## Schema notes (verified against real files)

- **Usage** lives on every `assistant` line at `message.usage`: `input_tokens`, `output_tokens`, `cache_creation_input_tokens`, `cache_read_input_tokens`. Model is `message.model`.
- **Dedup is mandatory.** Streaming writes multiple `assistant` lines per API call, all sharing one `message.id` (verified: 43 raw lines → 18 distinct calls in one file). The tool keys on `(message.id, requestId)` and keeps the **last** occurrence — the final line carries complete cumulative usage. Summing raw lines overcounts ~2-3x.
- **Agent label** is resolved in priority order:
  1. `agent-{id}.meta.json` → `description` (the richest — e.g. `"BE developer"`, `"gitter SETUP"`, `"FE QA pre-merge"`; present for sub-agents spawned with a description, flight executors included).
  2. `agent-{id}.meta.json` → `agentType` (e.g. `"workflow-subagent"`, `"general-purpose"`).
  3. `attributionAgent` on the assistant line.
  4. First-user-message prompt snippet (the agent's task brief).
  5. The raw `agentId`.

## Codex mode (`--codex`)

Same script, same `PRICING` table, different truth source: the Codex CLI writes one JSONL rollout per session thread.

```bash
# This repo's Codex sessions since a date, heaviest first:
node .claude/commands/tokens/token-ledger.mjs --codex --since <YYYY-MM-DD>

# Daily spend rollup:
node .claude/commands/tokens/token-ledger.mjs --codex --since <YYYY-MM-DD> --by-day

# Every project, every row, machine-readable:
node .claude/commands/tokens/token-ledger.mjs --codex --all --top 0 --json

# One agent role, or one session:
node .claude/commands/tokens/token-ledger.mjs --codex --filter developer
node .claude/commands/tokens/token-ledger.mjs --codex --all --filter <thread-id>
```

| Flag | Purpose |
| ---------------------- | ------------------------------------------------------------------------------------ |
| `--codex` | Read Codex rollouts instead of Claude transcripts. |
| `--since <YYYY-MM-DD>` | Keep rollouts stamped on or after that day (the filename stamp is **local** time). |
| `--all` | Span every project (default: the repo you are standing in) and add a PROJECT column. |
| `--by-day` | One row per calendar day instead of per session. |
| `--top <n>` | Cap the session table (default 25; `0` = every row). |
| `--filter <substr>` | Match on label, model, project, cwd, or session id. |
| `--codex-root <dir>` | Override `~/.codex`. |

### What it reads

`~/.codex/sessions/YYYY/MM/DD/rollout-<local-ISO-ts>-<threadId>.jsonl` and `~/.codex/archived_sessions/rollout-*.jsonl`. The `~/.codex/` root itself is deliberately **not** scanned — it holds `rollout-backup-*.jsonl` copies that would double-count a session. Sessions are de-duplicated by thread id.

Per rollout: line 1 is `{type:"session_meta"}` (cwd, thread id, and for a subagent thread its `agent_role`/`agent_nickname`); `{type:"turn_context"}` carries the model; token accounting rides on `{type:"event_msg", payload:{type:"token_count", info:{…}}}`, where `info` is `null` on older/idle events.

Because a Codex subagent writes its **own** rollout, per-session rows already give per-subagent attribution — the LABEL column shows the role (`{project}-developer (Nickname)`) or `main` for a top-level thread.

### Counting rule (verified against a local corpus)

`info.total_token_usage` is **cumulative**, and the tool sums the **peak of each segment**:

- Cumulative within a segment — a cumulative of 19,575 plus a 22,531 `last_token_usage` delta is followed by a cumulative of 42,106.
- It **resets to ~0 on resume/compaction**. One 110 MB rollout resets 3×; its four segment peaks are 108.1M / 77.2M / 353.2M / 384K. Reading only the final counter reports **384,439** instead of **538,898,717** — a 1400× undercount.
- Duplicate `token_count` events re-emit an **identical** cumulative total, so summing `last_token_usage` deltas **overcounts** (observed exactly 2× on a 2-event rollout).

Invariants that held on every event sampled, and that the arithmetic relies on: `total = input + output`; `cached_input ⊆ input`; `reasoning_output ⊆ output`; `cache_write = 0`.

### Performance

Rollouts reach 110 MB, mostly tool output. The scanner reads 4 MB windows (64 KB overlap, longer than any `token_count`/`turn_context` line, so every match lands whole in some window) and JSON-decodes **only** matching lines. On a 110 MB file that is ~0.35 s versus ~5.3 s for a plain readline pass, with byte-identical results; a 3.1 GB / 1500-rollout corpus scans in ~12 s.

## Cost model

Per-MTok rates are **EDITABLE constants** at the top of `token-ledger.mjs` (`PRICING`), matched by substring on the lowercased model id; first match wins, so keep specific ids above broader ones. **Update these rates when prices change** — they are best-effort defaults, not authoritative billing.

- **Claude models** (`opus`, `sonnet`, `haiku`, `fable`, `mythos`): cache-write = 1.25× input rate, cache-read = 0.1× input rate (standard Anthropic prompt-caching multipliers).
- **Codex models** (`gpt-6-astra`, `gpt-5.6-sol`, `gpt-5.6-luna`): a 4th `PRICING` column carries the cached-input rate, billed separately because Codex reports `cached_input_tokens` as a subset of `input_tokens`. Output already includes reasoning. All three rows are the vendor's published standard-tier input / cached-input / output rates (source and read date in the `PRICING` comment); Fast mode and Batch/Flex multipliers are not modelled. The vendor's higher long-context tier (>272K input) never applies — the Codex context window is smaller.

A model with **no** `PRICING` row reports cost **`n/a`**, not `$0`: its tokens still count toward the totals, its dollars do not, and the footer says how many sessions are affected. A session that spans more than one model is priced at the last model seen, with a warning naming every model it used.

## Token-definition calibration (read this to interpret the harness's numbers)

The Claude Code workflow harness reports a `subagent_tokens` figure. Validated against a known run (`wf_2c1d0117-cad`: harness reported **31 agents / 1,268,238 subagent_tokens / 579 tool_uses**), this tool's 31-agent totals were:

| definition | value | vs 1,268,238 |
| ----------------------------------------------------- | ------------- | ------------ |
| output-only | 131,933 | 10% |
| input + output (no cache) | 207,996 | 16% |
| **input + output + cache-write ("fresh"/billed-new)** | **1,382,232** | **109%** |
| + cache-read (grand total) | 10,231,459 | 807% |

So the harness's `subagent_tokens` maps to the **fresh / billed-new** definition — `input + output + cache_creation`, i.e. everything **except** the 8.8M cache-read tokens. It is NOT output-only, NOT input+output, and NOT the grand total. The ~9% gap (the harness reads ~1.27M; this tool sums 1.38M) is a flush-timing artifact: the harness fired its report before the last one or two streaming agents flushed their final usage lines — one agent's fresh-token total (113,452) almost exactly equals the gap (113,994).

The table footer prints all four definitions on every run so you can read whichever the context calls for.

## Caveats

- Read-only by design. `--detail` content hints are truncated at ~80 chars — but these transcripts can contain sensitive prompt content, so treat `--detail` output as sensitive and do not pipe it anywhere it would be retained.
- `attributionAgent`/`attributionSkill` are generic for workflow sub-agents (`"workflow-subagent"`/`"deep-rr"`); the real per-worker identity for those lives only in the task prompt (first user line), which the label falls back to when meta has no `description`.
- Cost is an **estimate**. Verify against the provider's actual billing before trusting absolute dollar figures; the relative ranking is what's reliable.
- Malformed JSONL lines are skipped silently and counted (reported on stderr).
