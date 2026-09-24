# compaction

pfm gives the main chat and its sub-agents separate auto-compact thresholds. Claude Code compacts both at one window, so a sub-agent otherwise inherits the main chat's threshold. Two machine-config keys name the point each party may compact at. `pfm install` sets Claude Code's window to the lower of the two and wires a `PreCompact` hook, `pfm internal compact-gate`, that blocks an automatic compaction whose party has not yet reached its own threshold.

Decisions live in this file. A change lands here first, then in the code, then in every surface under [Surfaces that stay in sync](#surfaces-that-stay-in-sync). The fleet's other hooks and the `pfm doctor` check live in [../hooks/hooks.md](../hooks/hooks.md); the meters that make a context's size visible live in [statusline.md](statusline.md).

## Contents

- [What Claude Code offers](#what-claude-code-offers)
- [Configuration](#configuration)
- [What pfm install writes](#what-pfm-install-writes)
- [The gate](#the-gate)
- [Sizing a context](#sizing-a-context)
- [Measurements](#measurements)
- [What it does not do](#what-it-does-not-do)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)
- [Known limit](#known-limit)

## What Claude Code offers

Read in the Claude Code 2.1.281 source and confirmed live:

| Mechanism | Scope | Consequence |
| --- | --- | --- |
| `CLAUDE_CODE_AUTO_COMPACT_WINDOW`, `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE` | read from `process.env`: one value per process | A settings `env` block (user or project `settings.json`) reaches the chat and every sub-agent it spawns. |
| Sub-agent window | the sub-agent's options take `autoCompactWindow` from the parent, through an identity function; a `fork` agent takes the parent's directly | No per-sub-agent threshold exists, and no agent frontmatter key sets one. |
| Trigger points | the 2.1.281 source: the compaction triggers at `window − 13,000`; the first attempts (a background precompute) start at `window × (1 − f)`, `f` 0.2 by default and set per window by a server-side flag | Attempts begin 16–25% below the window as measured, and the point can move without a Claude Code release, so no setting can make attempts start exactly at a threshold. |
| `PreCompact` hook | fires before every compaction attempt, the main chat's and each sub-agent's | Exit 2 blocks that attempt ("not compacted · reason"); exit 0 allows it. A blocked attempt makes no summary call and costs no tokens. |
| `PreCompact` input | `session_id`, `transcript_path` (always the main chat's), `cwd`, `hook_event_name`, `trigger` (`auto` or `manual`), `custom_instructions` | It carries no agent id, and the hook's environment adds only `CLAUDE_PROJECT_DIR`. The gate must infer which party is compacting. |

A pfm chat is a whole Claude Code process, not a sub-agent: the per-process window already separates two chats. The problem this design solves is inside one process, between a chat and the sub-agents it spawns.

## Configuration

Two keys in the top-level `claude` block of the machine config (`pfm/internal/config/compact.go`):

| Key | Means | Value |
| --- | --- | --- |
| `claude.autoCompactMain` | the main chat may auto-compact from this many tokens | an integer or a `k`/`m` suffix (`150000`, `150k`, `0.5m`), 100k to 1m inclusive |
| `claude.autoCompactSubagent` | a sub-agent may auto-compact from this many tokens | same |

- Both keys or neither: with either unset, pfm writes no window and no hook, and Claude Code behaves as shipped.
- Machine-wide only: an `accounts[i].claude` block that names either key is a load error, never silently ignored, because the window is one per Claude Code config dir and the gate reads one config.
- The keys are edited in the config file by hand; `pfm config` prints both with their source (`unset` when absent), and a value out of range is a load error naming the key.
- The installed binary decodes its config with unknown fields refused: a binary older than these keys fails on a config that holds them. Set the keys only after the binary that knows them is installed.

## What pfm install writes

Per Claude config dir, only while both keys are set (`pfm/internal/installer/settings_compact.go`):

1. `env.CLAUDE_CODE_AUTO_COMPACT_WINDOW` = the lower threshold. pfm writes it only when the variable is absent or still holds the value pfm wrote last, recorded in the settings ownership ledger as a `settings.env` entry; an operator's own value is kept.
2. A `PreCompact` hook: `$HOME/.local/bin/pfm internal compact-gate`, matcher `""`.

Unsetting either key and running `pfm install --yes` removes both, and `pfm uninstall` removes what the ledger owns.

Why the window is the lower threshold and not a multiple of it: Claude Code makes its first compaction attempt 25–50K below the window, and that gap varies from run to run (see [Measurements](#measurements)). No fixed ratio predicts it. Setting the window to the lower threshold means attempts start before either party is due. The gate turns every early attempt away at no token cost, and allows the first attempt at or past the party's own threshold. The cost is time: Claude Code asks before every request once a chat is past the window, so a main chat living between the window and its own threshold calls the gate on every request. The gate therefore reads and answers without waiting (see [The gate](#the-gate)).

## The gate

`pfm internal compact-gate` (`pfm/internal/compactgate/compactgate.go`) reads the hook input on stdin and answers per attempt:

1. `trigger` is not `auto` (a `/compact` the user typed): allow.
2. The thresholds are unset or the config fails to load: allow.
3. Find the party. Sub-agent transcripts live at `{main transcript minus .jsonl}/subagents/agent-*.jsonl`; one written within the last 60 s is active. An active sub-agent estimated below half the window cannot be making an attempt (the first attempts start at `window × (1 − f)`, and half leaves `f` room to double), so it is passed over.
   - No active candidate: the main chat is compacting.
   - An active candidate written more recently than the main transcript: the newest such candidate is compacting. A sub-agent's work writes its own transcript, not the main one.
   - Otherwise: the main chat.
4. Estimate the party's context (below). At or above its threshold: allow (exit 0). Below: block (exit 2) with one stderr line naming the party, the estimate and the threshold.

The gate reads the transcript as it stands, without waiting for Claude Code to finish writing it. An attempt can arrive while the last tool result is still being written; when that unwritten tail is what carries the party over its threshold, the gate blocks one attempt too early. Claude Code asks again before the next request, the tail is on disk by then, and the gate allows it: the compaction lands one request late, never lost. Sub-agents hit this more than the main chat, since they often read several large files at once.

A 1 s settle wait (up to 3 s) once closed that window. It was removed on the user's ruling: measured live, it cost a median 1,257 ms (p90 1,354 ms) on every gate call of a working chat, and a main chat between the window and its threshold called it on every request (31 calls in 6.5 minutes, 26 s added). Dissent on record (the FLIGHTS chat): without the wait, rerun 1's wrong block of a sub-agent returns, and a sub-agent that ends right after that block never compacts. Accepted: an agent that ends needs no compaction, and one that continues is allowed on its next request.

Any read failure (a transcript, the directory, the config) allows the compaction and logs the cause through `obs`. The gate never blocks on an error: a compaction it fails to judge is Claude Code's normal behaviour, while a compaction it wrongly blocks leaves the context growing toward the model's hard limit.

## Sizing a context

The transcript records usage only on assistant lines, and a tool result lands before the next model call records its usage, so the last recorded usage lags the real context. The estimate is:

- the newest recorded context: an assistant line's `input_tokens + cache_read_input_tokens + cache_creation_input_tokens`, or a `system`/`compact_boundary` line's `compactMetadata.postTokens` when that is newer (after a compaction the old usage no longer applies);
- plus the transcript bytes written after that line, divided by 8.

The divisor is measured, not assumed: transcript bytes per Claude Code token came out at 8.27 and 8.44 against Claude Code's own `preTokens`. The first build used 4, which overestimated by 10–15% and let the main chat compact at 141.7K against a 150K threshold; `TestGateCompactionEstimateMatchesClaudeCodesOwnCount` pins that case.

## Measurements

All on Claude Code 2.1.281, in pfm-launched chats, with a scratch `XDG_CONFIG_HOME` so the host's real config never held the new keys.

| Run | Setup | Result |
| --- | --- | --- |
| Window reach | `env.CLAUDE_CODE_AUTO_COMPACT_WINDOW` in settings | The sub-agent compacted at the window (pre 134,024 → post 113,839): the variable reaches sub-agents. |
| Block | a `PreCompact` hook exiting 2 | The main chat was blocked five times from 99K to 129K, then compacted when the hook allowed it at 152,484. |
| Gate rerun 1 | main 150K, sub-agent 100K; divisor 4, no settle wait | The main chat was held (blocked 52.9K–141K, compacted at 172.9K). The sub-agent was wrongly blocked at ~79K: its transcript was still being flushed and read 173K six seconds later. This led to the compact-boundary reset, the measured divisor and a settle wait, since removed (see [The gate](#the-gate)). |
| Gate rerun 2 | same thresholds, fixes in | The sub-agent was blocked at 94.9K and 97.1K, then compacted at 105,981. The main chat was blocked at 81K, 109K and 133K, then compacted at 141,752: the divisor-4 error, now corrected and pinned. |
| Gate latency | installed build, main 600K / sub-agent 150K, three live chats | 57 gate calls with the settle wait: median 1,257 ms, p90 1,354 ms; an idle chat's quiet transcript answered in 2–6 ms. A sub-agent compacted at 161,888 and 161,858 with the estimate within 0.17% of Claude Code's count. |
| Live sub-agent, window 150K | one Sonnet sub-agent reading ~456 KB of source, this chat's main at ~280K against 600K | The sub-agent was blocked at 106K, 121K and 136K, allowed at 164K (its first attempt past 150K) and compacted 178,853 → 23,041; later estimates of 118K and 139K matched Claude Code's recorded usage exactly. Gate decisions took 0–4 ms. |
| Parallel executors | a live orchestrator run, ~10 executors at once, window 150K | 19 compactions after the thresholds were set: 15 at 150.0–173.8K, 4 early at 120K–136K. In each early one the gate had named another active sub-agent past 150K; the log also shows sub-agents a second old (6K) named for others' attempts. The candidate floor now passes over those; see [Known limit](#known-limit). |
| Compaction pause | the same runs, and the firing-point runs with the gate allowing everything | A compaction stops its agent while Claude Code writes the summary: 37–80 s gated (median 60 s at ~153K; one outlier of 318 s), 27–92 s ungated (41.5 s and 50.5 s at 168K and 179K). The pause is Claude Code's, not the gate's. |
| Firing point | the hook allows everything; windows of 100K, 200K and 300K | 100K: first attempt ~74.5K, compactions 68.9–74.3K. 200K: first attempt 152.7K, compactions 168.2–178.8K. 300K: first attempt 250.9K, compaction 265.0K. There is no fixed fraction: a prototype run at 100K started at ~53K. |

## What it does not do

- It does not raise a threshold above the window: the window is the lower threshold, so a threshold below it cannot happen by construction.
- It does not force a compaction: it only refuses early ones. A party past its threshold compacts when Claude Code next attempts, which is at most one attempt later.
- It does not judge a manual `/compact`.
- It does not give two sub-agents different thresholds: every sub-agent shares `autoCompactSubagent`.
- It does not reach Codex or OpenCode: the mechanism is Claude Code's `PreCompact` hook and window variable.

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The config keys | `pfm/internal/config/compact.go`, `pfm/cmd/pfm/config_command.go` | parsing, range, the machine-wide rule, the `pfm config` rows |
| The installer | `pfm/internal/installer/settings_compact.go`, `expected_hooks.go`, `settings_ownership.go` | the window, the hook, the ownership ledger entry |
| The gate | `pfm/internal/compactgate/`, `pfm internal compact-gate` in `pfm/cmd/pfm/main.go` | party rule, estimate, decision |
| The doctor check | `pfm/internal/installer/compact_probe.go`, wired in `hook_probe.go` | the `env compact-window` row, per [../hooks/hooks.md](../hooks/hooks.md#the-pfm-doctor-check) |
| The hook inventory | [../hooks/hooks.md](../hooks/hooks.md) | the `compact-gate` row |
| The surface reference | `docs/dev/pfm-surface.md` | the `internal compact-gate` row |
| The lane map | `docs/dev/testing/landscape.md` (T40), `infra/fence/lanes/` | the landscape row, its beat and its map row |
| The test timing budgets | `pfm/.testtiming.yml` | the `compactgate` package budget |

## Known limit

Claude Code knows which agent is compacting (its hook runner receives the agent id) but passes it neither in the `PreCompact` input nor in the hook's environment, in 2.1.281 and 2.1.282. The gate therefore infers the party from the transcripts, and with several sub-agents active at once it can name the wrong one:

- It allows too early when another active sub-agent past its threshold is named for the attempt of one below it. That sub-agent compacts early, but never below half the window (the candidate floor).
- It blocks too late when a sub-agent below its threshold is named for the attempt of one past it. That compaction lands on a later request.
- A main chat working while sub-agents run in the background can be confused with them in either direction.

Tighter rules were replayed against the 19 real compactions of the parallel-executor run. Blocking whenever the candidates disagree would have held back 12 of the 15 legitimate compactions, since ten executors sat between 120K and 160K together; the replay cannot model when Claude Code flushes each line, so no heuristic could be proven better than the current one plus the floor. The fix that removes the limit is Claude Code passing `agent_id` in the `PreCompact` input, as it already does in `PostToolUse` and `SubagentStart`; the gate would then read the named agent's transcript and stop inferring.
