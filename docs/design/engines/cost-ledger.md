# Cost ledger

Every API call a Claude Code chat makes is priced exactly once, in a per-session file, so the status line, `pfm tokens` and callmeter show one dollar figure that agrees with Claude Code's own session total. The ledger exists because the chat's transcripts do not hold every call: Claude Code makes calls it never records there, and a transcript-priced total undercounts by the whole of that spend.

The pieces: the launch registry points Claude Code's OpenTelemetry exporters at a loopback sink ([The launch](#the-launch)); a detached `pfm internal cost-sink` process joins the two exported halves of each call ([The sink](#the-sink), [The join](#the-join)) and appends one line per call to `{session_id}.jsonl` ([The ledger](#the-ledger)); readers sum it ([The status lines](#the-status-lines), [pfm tokens](#pfm-tokens)). The launch registry's own shape is in [claude-launch.md](claude-launch.md); the statusline's fields are in [statusline.md](../context/statusline.md).

## Contents

- [Decisions](#decisions)
- [Calls no transcript records](#calls-no-transcript-records)
- [The launch](#the-launch)
- [The sink](#the-sink)
- [The join](#the-join)
- [The ledger](#the-ledger)
- [The status lines](#the-status-lines)
- [Before the ledger](#before-the-ledger)
- [pfm tokens](#pfm-tokens)
- [Config and environment](#config-and-environment)
- [Doctor](#doctor)
- [Privacy](#privacy)
- [Tests and fixtures](#tests-and-fixtures)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## Decisions

- **The ledger is exact; a transcript is not.** Claude Code's session total (`cost.total_cost_usd`) prices at the published list and so does pfm's price table: no rate error exists. The gap is calls (below) that no transcript line records.
- **Telemetry is the only witness of an unrecorded call.** Claude Code's OpenTelemetry `api_request` log event carries the call's own `cost_usd_micros`; its `claude_code.llm_request` span carries the agent. Neither alone is a ledger row; the sink joins them on `request_id`.
- **The sink is per chat and detached.** The launcher binds the listening socket before Claude starts and hands it to a sink process that outlives nothing but its chat plus a short drain. No shared daemon, no port to configure, nothing leaves `127.0.0.1`.
- **A ledger failure is a marker, never a failed chat.** Every problem on the launch path becomes a `PFM_COST_LEDGER` value the status line renders and a line in the sink log `pfm doctor` shows; Claude starts regardless.
- **The user's own telemetry wins.** A launch whose environment or account `settings.json` already names `CLAUDE_CODE_ENABLE_TELEMETRY` or any `OTEL_*` variable keeps it and runs without a ledger.
- **One dollar on the main line.** The main status line shows exactly one dollar amount in every state; the amounts behind a flag are reported by `pfm tokens`.
- **The ledger measures helper spend; the launch routes it.** `ANTHROPIC_SMALL_FAST_MODEL` decides which model Claude Code's helpers use; with it unset they run on the main model. Every pfm launch sets it through `claude.helperModel`, `claude-haiku-5-5` by default ([claude-launch.md](claude-launch.md#decisions)); the ledger records that spend on whichever model ran it and changes none of it. `ANTHROPIC_DEFAULT_HAIKU_MODEL` also moves Haiku sub-agents, so pfm does not set it.

## Calls no transcript records

Measured by an OpenTelemetry probe of one chat, each call joined by `request_id`; the sum equalled Claude Code's own total. Claude Code 2.1.295 makes these calls and writes none of them to a transcript. The model column reads as measured with no helper model set; a launch with `claude.helperModel` runs `web_fetch_apply` and the `web_search_tool` call (while Claude Code's `tengu_plum_vx3` flag is true) on that model instead:

| `query_source` | What issues it | Cost shape |
| --- | --- | --- |
| `web_search_tool` | every `WebSearch`, on the main chat's model, even from a Haiku sub-agent | about 12.4K uncached input and 750 output tokens |
| `web_fetch_apply` | every `WebFetch` runs the page through the main model | thousands of uncached input tokens; the Haiku child's own call costs a fraction |
| `agent_summary` | a 30 s tick per running sub-agent, skipped while its transcript is unchanged | cheap on Haiku |
| `prompt_suggestion` | one cache read of the main context after a turn, backed off after unused suggestions | the whole main context at the cache-read rate |

A chat that runs hundreds of searches and fetches from Haiku researchers can carry a session total many times its transcripts' price; the difference is these rows. Calls the transcripts do record (`repl_main_thread`, `agent:builtin:{name}`, `compact`) are in the ledger too, so the ledger total is the whole session.

## The launch

Every interactive Claude start carries the cost wrapper unless the ledger is off or pre-empted. Each name and value below is a row of `claudelaunch.Knobs` (`pfm/internal/claudelaunch/cost.go`), set by the wrapper at exec time because the endpoint's port exists only then:

| Variable | Value |
| --- | --- |
| `CLAUDE_CODE_ENABLE_TELEMETRY` | `1` |
| `OTEL_LOGS_EXPORTER` | `otlp` |
| `OTEL_TRACES_EXPORTER` | `otlp` |
| `CLAUDE_CODE_ENHANCED_TELEMETRY_BETA` | `1` (the `llm_request` span needs it) |
| `OTEL_METRICS_EXPORTER` | `none` |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `http/json` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `http://127.0.0.1:{port}` |
| `OTEL_LOGS_EXPORT_INTERVAL` | `1000` |
| `OTEL_BSP_SCHEDULE_DELAY` | `1000` |

Nothing sets `OTEL_LOG_USER_PROMPTS`, `OTEL_LOG_TOOL_DETAILS` or `OTEL_LOG_RAW_API_BODIES`, so no prompt, tool detail or API body is exported.

### The wrapper

`pfm internal cost-launch -- {claude} {args}` (`pfm/internal/costsink/launch.go`) runs in front of the real binary. It decides the ledger (`costsink.Prepare`), arms a sink (`costsink.Arm`), and execs Claude with the resulting environment, keeping its own pid, which the sink watches. `Render` returns the words as `Launch.Wrapper` when `~/.local/bin/pfm` is installed and executable; `ClaudeSpawn.ShellCommand` prints them between the environment assignments and the binary, `ClaudeSpawn.Command` execs `Wrapper[0]` with the binary as its first argument. When the binary resolves to pfm's own `claude` shim, the pass-through reaches `costsink.Prepare` again in the same process: `PFM_COST_LEDGER_PID` names the pid the sink serves, so that pass keeps the armed sink (`claudelaunch.CostArmedFor`) instead of arming a second one.

### The marker

The wrapper records its decision as `PFM_COST_LEDGER` in Claude's environment (`claudelaunch.EnvCostLedger`); the status lines read it from their own environment, which Claude Code hands them.

| Value | Meaning | Set by |
| --- | --- | --- |
| `on` | a sink is armed and the exporters point at it | `Prepare` after a successful `Arm` |
| `off` | `claude.costLedger` is `false` | `Render` (no wrapper) and `Prepare` |
| `preempted` | the user's own telemetry settings were kept, or `settings.json` could not be read so they cannot be ruled out; no ledger | `Prepare`, cause in the sink log |
| `failed` | the sink could not be armed, or `~/.local/bin/pfm` is not installed where the wrapper must run | `Prepare`, or `Render` when the wrapper is missing (a warning in the activity log) |

A Claude started with no marker is one pfm did not launch, or a `claude -p` run: the status lines treat it as "no ledger decision", never as `off`.

### Pre-emption

`claudelaunch.CostPreemptedReason` returns the reason a launch must keep the user's telemetry: the process environment names `CLAUDE_CODE_ENABLE_TELEMETRY` or an `OTEL_*` variable, or the `env` block of the account's `settings.json` does (`claudelaunch.SettingsEnvNames`). One exception: a chat started from inside a pfm chat inherits pfm's own exporter variables and `PFM_COST_LEDGER=on`; when every inherited variable is exactly pfm's own, they are vouched for and replaced by the new launch's own sink, because the parent's sink dies with the parent. A launch that arms nothing (off, pre-empted, failed) drops those inherited variables (`claudelaunch.InheritedCostNames`: in the wrapper, and in `Render`'s `Unset`), so its Claude never exports to the parent's sink. An unreadable or unparseable `settings.json` is logged and pre-empts the ledger, since its real state is unknown and a caller must not read it as naming nothing. An empty or whitespace-only `settings.json` names nothing.

### Doors

| Door | Ledger |
| --- | --- |
| Render doors (`PlanClaude`, `HeadlessFork`, `Synthesize`, `AgentOpen` resume, `claudeRun`, `LauncherRun`) | `Launch.Wrapper`; the marker `off` or `failed` rides `Launch.Env` when there is no wrapper |
| Managed launcher passthrough that starts a session (`pfm internal launch`, `pfm/internal/hookentry/launch.go`) | `costsink.PrepareCurrent` before the exec; a print-mode run (`-p`, `--print`, `--output-format`) is not armed |
| Headless (`pfm headless exec`, `pfm ask`) and `PurposeQuery` | none: no chat to meter |

A passthrough start (for example a `claude` run inside an existing pfm tmux socket) therefore arms through the passthrough door, not the wrapper; both end in `Prepare`.

## The sink

`pfm internal cost-sink --pid {pid} --start {start}` (`pfm/internal/costsink/command.go`) is a detached child of the launcher with its own session and no controlling terminal. The launcher bound `127.0.0.1:0` itself and passes the listening socket as descriptor 3, so the port is known and held before Claude starts and connections queue until the sink accepts.

- **Endpoints.** `POST /v1/logs` and `POST /v1/traces` answer `200 {}`; any other path answers `404`, any other method `405`. A body over 8 MiB, compressed or decompressed, answers `413`; `Content-Encoding: gzip` is accepted.
- **What it keeps.** Only `api_request` log records and `claude_code.llm_request` spans; every other event and span is dropped before it is stored.
- **Session ids.** A record whose `session.id` is outside the ledger's character class (`costledger.ValidSessionID`) is refused and logged, never joined into a path.
- **Identity of the chat.** The launcher's pid plus that process's start time, read through `gather` at arm time. A reused pid has another start time and counts as gone.
- **Exit.** When the chat is gone the sink keeps accepting until three seconds pass without a POST (at most fifteen), writes every pending half and exits; Claude's exporters flush on exit and a late batch must still land. Thirty consecutive unreadable process-table reads also end it, with the cause logged.
- **Prune.** At start it deletes `*.jsonl` files older than 90 days from the ledger directory.
- **Failure.** Nothing in the sink can stop the chat. A refused POST, a ledger write that failed, a panic while serving, a failed prune: one line in the sink log, `~/.local/state/pfm/log/cost-sink.log` (`paths.CostSinkLog`), which the launcher also writes its own decisions to. `pfm doctor` shows its tail ([Doctor](#doctor)).

## The join

Claude Code exports one call as two OTLP records that arrive in separate POSTs, in either order:

| Half | Carries | Lacks |
| --- | --- | --- |
| `api_request` log | `request_id`, `session.id`, `query_source`, `model`, token counts, `cost_usd_micros` (an integer, or a JSON string; absent, the `cost_usd` double rounded to the micro), `speed`, `effort`, `event.timestamp` | the agent |
| `claude_code.llm_request` span | `request_id`, `session.id`, `agent_id` (absent for the main thread), `parent_agent_id`, `query_source_safe`, `model`, token counts, `speed`, `effort`, end time | the cost |

`request_id` equals the `requestId` of the transcript line that records the same call. The sub-agent `agent_id` equals the stem of its transcript file `agent-{id}.jsonl` and the status line's task id. A span's `query_source_safe` spells `agent.builtin.x` where the log says `agent:builtin:x`; a span-only row rewrites the prefix to the log's spelling.

A half waits 20 seconds for its partner (`costsink.DefaultWait`). Matched, the row is `log+span`. A log half unmatched after the wait is written as `log` with `agent_id` `"?"`; a span half unmatched is written as `span` with a `null` cost. Nothing is dropped. A partner that arrives after its half was flushed appends an upgraded `log+span` line; readers keep the best.

## The ledger

`{dir}/{session_id}.jsonl`, `dir` = `paths.CostLedgerDir(env, home)`: `$PFM_COST_LEDGER_DIR` when set, else `~/.local/state/pfm/cost-ledger`. One file per chat session, a resumed session appending to the same file; directory mode 0700, file mode 0600. A line is one JSON object, one `write(2)` on an `O_APPEND` descriptor, keys in this order:

| Key | Type | Meaning |
| --- | --- | --- |
| `request_id` | string | Anthropic API request id; unique after dedupe |
| `ts` | string | RFC 3339 UTC with milliseconds: the log half's `event.timestamp`, else the span's end time, else the sink's first receipt of the request |
| `session_id` | string | the file's session |
| `agent_id` | string | `""` main thread, `"?"` a log-only row whose agent is unknown, else the sub-agent id |
| `parent_agent_id` | string | the spawning sub-agent; `""` for main, a top-level sub-agent and a log-only row |
| `query_source` | string | what the call was for (`repl_main_thread`, `agent:builtin:general-purpose`, `web_search_tool`, `web_fetch_apply`, `agent_summary`, `prompt_suggestion`, `compact`, …) |
| `model` | string | model id as reported |
| `input_tokens` | integer | uncached input |
| `output_tokens` | integer | output |
| `cache_read_tokens` | integer | cache reads |
| `cache_creation_tokens` | integer | cache writes (no 5m/1h split) |
| `cost_usd_micros` | integer or null | Claude Code's own price of the call, USD × 10^6; `null` when the log half never arrived |
| `speed`, `effort` | string | as reported, `""` when absent |
| `join` | string | `log+span`, `log` or `span` |

### Readers

`costledger.ForSession(dir, sessionID)` (`pfm/internal/costledger/costledger.go`) parses line by line and dedupes by `request_id`: the best `join` wins (`log+span` over `log` over `span`), the later line among equals. A line that is not a JSON object (a torn tail) is a counted `Faults`, never silent; a missing file is `Present == false`, not an error. `costledger.Sum` totals rows; a `null` cost is counted in `UnknownCost` and named, never a zero. callmeter reads the same files standalone as optional data and never writes or deletes there.

### Side calls

A ledger row is held by a transcript when some transcript line of the session, the main file or any `subagents/agent-*.jsonl`, carries a `requestId` equal to its `request_id`. A row held by none is a side call: exact spend that no transcript shows (`Ledger.Side`).

## The status lines

The main line and the sub-agent rows read the ledger through `costledger.ForSession`; neither writes it. Both are in `pfm/internal/statusline/` (`context.go`, `ledger_spend.go`, `subagents.go`, `subagents_spend.go`).

### Main line numbers

Every number on the main line and where it comes from:

| Number | Shows | Source |
| --- | --- | --- |
| context gauge `42%` | share of the context window in use | the chat's transcript (`transcript.ReadMeta`); the payload's `context_window.used_percentage` when the transcript cannot be read; `~` marks an estimate after a compaction |
| dollars `💰$3.42` | the one dollar amount of the line, the complete session total | with a ledger, the ledger total (`Σ cost_usd_micros` over the deduped rows) plus [what came before the ledger](#before-the-ledger), unless the payload's `cost.total_cost_usd` is ahead of that by more than `max($0.01, 0.5% of it)` (then the payload: a call whose row has not reached the ledger yet, a launch that logged nothing); else the payload's `cost.total_cost_usd` (Claude Code's own total); with no such figure, the transcripts' price at pfm's price table; `$?` when nothing could be priced |
| prompt tokens `420.0K` | everything sent as input | with a ledger, `input_tokens + cache_read_tokens + cache_creation_tokens` summed over every row (the calls before the ledger have no tokens anywhere, so a pre-ledger chat's columns are the ledger's alone); else the chat's transcript and every sub-agent's, a response counted once per `message.id` |
| output tokens `31K` | everything the model wrote | with a ledger, `output_tokens` summed over every row; else the transcripts' |
| tool calls `46` | distinct `tool_use` calls | the chat's transcript and every sub-agent transcript beside it, always; the ledger records no tools. `?` when a transcript cannot be read |
| `+?` | the dollar figure is a floor | with a ledger, a row whose cost never arrived (`cost_usd_micros` null); without one, a transcript model the price table cannot price |
| cache window `💾1h✓48m:0s` and hit `77%` | prompt-cache time left; share of the last call's prompt read from cache | [statusline.md](../context/statusline.md#the-main-statusline); not cost |
| `⏳ 5m32s` | session wall time | the payload's `cost.total_duration_ms` |
| `5h-used:32%`, `7d-used:71%` | rate-limit windows | the payload's `rate_limits`, or pfm's refreshed copy |

The main line renders exactly one `$` in every state; a test (`TestMainLineRendersOneDollarAmountInEveryState`) renders every state, resumed, pre-ledger and crashed launches among them, and counts. The invariant that keeps every surface on one figure: the complete total equals the main thread's own ledger rows plus every top-level sub-agent row plus the spend before the ledger (which no agent owns), and `pfm tokens`' session total; callmeter's `PRE-LEDGER USD` is the same before-ledger figure by the same rule. A chat that was resumed, or that began before the ledger, still shows its whole spend: Claude Code restores its total from the transcript on resume, so nothing about the figure depends on which launch armed a sink.

### Markers

A state the line must flag ends in one marker (`spendMarker`, `pfm/internal/statusline/ledger_spend.go`), in the warning colour after a space, carrying no amount; at most one per line, the first that applies:

| Marker | When |
| --- | --- |
| `sink failed` | `PFM_COST_LEDGER=failed` |
| `ledger preempted` | `PFM_COST_LEDGER=preempted`: the user's own telemetry (or a `settings.json` that could not be read) kept the ledger from starting |
| `ledger off` | `PFM_COST_LEDGER=off`: `claude.costLedger` is `false` |
| `ledger unreadable` | the ledger file exists and could not be read, or its session id is invalid; the line then shows Claude Code's total and the cause goes to the activity log |
| `pre-ledger` | [spend before the ledger](#before-the-ledger) exceeds `max($0.01, 0.5% of the line's amount)`: the chat began before the ledger, or ran a launch without a sink. A fact of the files, so it does not flicker with a call in flight and a healthy resume (every earlier launch fully logged) never shows it |
| `unlogged` | no ledger, and the transcripts price below Claude Code's total by more than `max($0.01, 0.5% of Claude Code's total)`: calls no transcript records exist, and nothing measures them |

A launch's own marker (`failed`, `preempted`, `off`) wins even when a ledger file of an earlier launch of the session exists: this launch is not logging, so the ledger is a floor. The amount is still the complete total: the ledger plus what came before it while those agree with Claude Code's payload, then the payload once this launch's unlogged calls put it ahead by more than the tolerance; `pfm tokens` and callmeter, reading the files, catch up with it at the next exit's cost-state. Not flagged: no ledger and transcripts priced above Claude Code's total, a payload with no total, and a sink armed whose ledger has no row yet. The line never compares the ledger with Claude Code's total (the user ruled the `ledger≠cc` marker out of the status line); that difference, like the side-call spend and the unlogged gap, is what [pfm tokens](#pfm-tokens) reports.

### Sub-agent row numbers

A row's spend field `Σ$1.24/3.1M/42K/12` (`spendSegment`; the muted `Σ` marks a subtree sum):

| Number | Source |
| --- | --- |
| dollars | with a ledger: `cost_usd_micros` of the rows of that `agent_id` and of every agent below it, at any depth, its side calls included; the tree is the rows' `parent_agent_id` edges joined with the meta files' `parentAgentId`. An agent the ledger holds no row of yet (rows reach it up to 20 s late) keeps its transcripts' price. Without a ledger: the transcripts' price at pfm's price table |
| prompt tokens | with a ledger: `input + cache_read + cache_creation` of the same rows; else the transcripts' |
| output tokens | with a ledger: `output_tokens` of the same rows; else the transcripts' |
| tool calls | the agent's transcript and every agent below it, always |
| `adv $0.30` | the advisor's own spend, read from the transcript's advisor iterations, a part of the dollars; `adv n/a` when the table cannot price a consultation |
| `+?` | a floor: with a ledger, a row of the agent or below it whose cost never arrived; without one, a model the price table cannot price or a transcript below that cannot be read |
| `Σ$?/?/?/?` | no ledger and the agent's own transcript cannot be read; with a ledger the row keeps its ledger dollars and shows `?` for tool calls |

The invariant: the main thread's own rows plus every top-level row equal the ledger total. A row's gauge, status, cache and the other fields are in [statusline.md](../context/statusline.md#the-sub-agent-row).

## Before the ledger

A chat can hold spend its ledger never saw: it began before the ledger existed, or one of its launches ran without a sink (`failed`, `preempted`, `off`, or an older `pfm`); less often a launch with a sink lost exports, or Claude Code priced a call that sent no OTel `api_request` event (a sub-agent routed to a non-Anthropic model through a gateway, seen on a live host), or a call's cost never arrived (a null-cost row, which the line marks `+?`). The first two are the common cause and name the marker; the figure is the same for all of them. Claude Code keeps that spend in its total: when a session resumes it restores `cost.total_cost_usd` from the transcript's last `cost-state` line (measured on 2.1.296), so the payload's figure is the complete total from the first render, while the ledger holds the launches that logged. The difference is *before the ledger*, attributed to no agent.

`costledger.BeforeLedger(rows, states)` (`pfm/internal/costledger/before.go`) reads it from the transcript, never guesses it from a live payload. Each `cost-state` line is Claude Code's exact running total at one exit; the ledger's rows of the launches up to that exit, taken from it, leave what the ledger never saw. The answer is the largest such remainder over the transcript's cost-states, floored at 0:

```text
before = max over cost-states c of ( c.totalCostUSD − Σ cost_usd_micros of the rows with ts ≤ cutoff(c) )
cutoff(c) = max( exit(c) + 2 s, until(c) ), or no bound when no timestamped line follows c
```

- `exit(c)` is the later of the last timestamped transcript line before `c` and `startTime + totalDuration` of `c`. The clock is the exit's wall clock on a session's first launch only: Claude Code keeps `startTime` across a resume while `totalDuration` counts no time between launches, so a resumed launch's clock lags its exit by those gaps (measured on the host's transcripts: a fifth of the later cost-states lag their last stamp by more than a minute) and the stamp leads.
- `until(c)` is the first timestamped line after `c`: the next launch wrote it before its first call, so every row up to it belongs to the launches `c` totals, including a background agent's calls after the main thread's last line, which no stamp of the main transcript places. With no line after `c` (a closed chat) every row does. A cost-state with no placeable exit is bounded by `until(c)` alone.
- The 2 s grace covers a reload, whose next launch writes its first line within seconds of the exit: a call's row lands up to 0.25 s after its transcript line (measured over three live sessions), and the next launch's first call comes seconds later. A row with no readable `ts` counts as logged by every exit; a row with a null cost counts nothing.
- The statusline walks the main transcript once for its tokens and tool calls and collects the cost-states on the way (`statusline.spendScan`); `pfm tokens` does the same in its Claude reader. Both pass `costledger.Anchors` the lines in file order.

The one dollar amount is the ledger total plus `before`, shown unless Claude Code's payload is ahead of it by more than the tolerance. The payload therefore leads in the one or two seconds before a call's OTLP export reaches the ledger, and the ledger plus `before` leads after a crash that wrote no cost-state; in the ordinary case the line is exactly the sum of the rows `pfm tokens` prints. A gap trigger (payload against ledger) would flicker after every call and is not used.

callmeter names the same difference as `PRE-LEDGER USD` in `report cost`, from the same rule over the latest cost-state it holds, because callmeter is standalone and keeps no earlier ones; it holds no transcript stamps, so its exit is the later of `session_costs.started + wall_ms` and the session's latest `SessionEnd` event, the hook that fires at the exit that wrote that cost-state ([callmeter's design](../../../plugins/callmeter/docs/design.md)).

## pfm tokens

`pfm tokens` and `pfm tokens --session SID` (`pfm/internal/tokens/costreport.go`) read each Claude chat's ledger beside its transcripts and print what the status line's markers point at. The ledger directory is `paths.CostLedgerDir`, so `PFM_COST_LEDGER_DIR` redirects it. Codex and OpenCode runs have no Claude ledger and are unchanged.

A chat with a ledger gets a block under `== COST LEDGER`, the chats dearest first:

| Line | Source |
| --- | --- |
| `session total $X (N calls)` | `Σ cost_usd_micros` over the deduped rows plus the spend [before the ledger](#before-the-ledger), the main status line's number |
| `before ledger $X (not attributed)` | the spend before the ledger, shown when it is half a cent or more; the same figure is a `before ledger (not attributed)` row in `by agent` and in `by query_source`, so the rows add up to the session total |
| `side calls $X (N calls no transcript holds)` | rows no transcript holds: a row is held when any transcript line of the session, the main file or any `subagents/agent-*.jsonl`, in any file the ownership walk found however old, carries its `request_id` as `requestId` (`Ledger.Side`). A transcript that could not be read, or a line that could not be decoded, may hide a held request, so the block says `side calls may be overstated` and names the file |
| `N calls with no cost in the ledger` | rows with `cost_usd_micros` null: counted, named, not in the total |
| `N torn ledger lines skipped` | `Ledger.Faults` |
| `Claude Code says $X` | the main transcript's last `cost-state` line (`totalCostUSD`); `no cost-state line in this chat's transcript` when there is none (`pfm tokens` has no stdin payload, so the transcript is its only source of Claude Code's total). When a timestamped line follows that cost-state the chat has run past it, and the line reads `Claude Code's last cost-state says $X (written at an earlier exit; the chat has run since)` |
| `session total minus Claude Code: +$X` | shown only when the last cost-state is the chat's final word (no timestamped transcript line follows it) and the two differ by half a cent or more; both numbers print as dollars and cents here, so the threshold is half a cent. The session total includes the spend before the ledger, so a healthy chat reads 0 here |
| `by agent` | cost and share of the session total: the main thread's own calls, each top-level sub-agent as a tree (itself and everything it spawned, children indented), the calls of an agent the log never named (`?`) last. The parent is the ledger's `parent_agent_id`, else the meta file's `parentAgentId`; a parent that is no agent with calls, or a loop, makes the agent top-level. The main thread and the top-level rows sum to the session total |
| `by query_source` | cost and share per `query_source`, dearest first: `repl_main_thread`, each `agent:builtin:{name}`, `web_search_tool`, `web_fetch_apply`, `agent_summary`, `prompt_suggestion`, `compact`, … |

A chat with no ledger keeps today's output and one line, `side calls not measured (no cost ledger)` (per chat count when some chats have one). A ledger that exists and cannot be read is a read error on stderr and in the block, and exits 1, never "no ledger". The estimate columns elsewhere in the report (per-run dollars from the transcripts at the price table) are unchanged: the ledger adds a section, it replaces nothing.

`--out` gains a `costLedger` array, one object per chat with a ledger (every amount in micro-dollars, with `side`, `claudeCodeMicros`, `differenceMicros`, `beforeLedgerMicros`, `sessionMicros`, `byAgent`, `bySource`), only when a ledger exists, so a document from a machine without ledgers is byte-identical to before.

## Config and environment

| Name | Where | Effect |
| --- | --- | --- |
| `claude.costLedger` | `pfm.config.json`, default `true` | machine-wide switch; `false` launches with `PFM_COST_LEDGER=off` and no wrapper. An account-level `claude.costLedger` is refused (the sink is a machine concern) |
| `PFM_COST_LEDGER_DIR` | process environment | the ledger directory, for a test jail or a relocated state dir |
| `PFM_COST_LEDGER` | Claude's environment, set by pfm | the marker above; never set by hand |

## Doctor

`pfm doctor` prints the ledger's state and the sink log's tail (`pfm/internal/doctor/cost_ledger_check.go`):

```text
doctor: cost ledger state=on source=default dir=~/.local/state/pfm/cost-ledger
doctor: cost sink log path=~/.local/state/pfm/log/cost-sink.log bytes=412
doctor: cost sink log: 2026-10-09T18:27:28.025Z cost-launch: armed a sink on 127.0.0.1:54321 for pid 4242
```

`state` is `on` or `off` (`claude.costLedger`), `source` where the value came from (`default` or `file`). The log's last five lines show; a log nothing has written prints `state=absent`; one doctor could not read prints `state=unavailable error=…` and counts one warning, because "we failed to look" is not "there is nothing there".

## Privacy

No prompt, message, tool, path or identity field is ever written: the sink copies only the fields in the table above, and the `api_request` record's identity attributes (email, user, account and organization ids) are not among them. OTLP fixtures in tests are sanitized from the raw captures with invented placeholders for every such field.

## Tests and fixtures

- `pfm/internal/costsink/testdata/probe-session.jsonl` is the sanitized replay of the probe's 25 OTLP POSTs (`{"t":seconds,"path":…,"body":OTLP}` per line), produced by `testdata/sanitize-otlp.py`. The sink test replays it: the first 15 calls total 1294100 micros with the agent split of the reference join (main 340251, `a…01` 78574, `a…02` 787435, `a…03` 87840), and all 21 total 1435027.
- `pfm/internal/costledger/testdata/sample-15.jsonl` is those 15 rows as ledger lines, the fixture the readers, the status lines and `pfm tokens` test against.
- Real-process tests (`internal/costsink`) start a wrapper over a fake claude script and assert the pid is kept, the endpoint names the armed port, and the listener descriptor does not reach Claude.
- No suite runs a live engine; the live check after landing is a chat started on the host.

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The launch registry | `pfm/internal/claudelaunch/cost.go`, `knobs.go` | the exporter variables, the marker, the wrapper words |
| The wrapper and the sink | `pfm/internal/costsink/` | `Prepare`, `Arm`, `Meter`, the OTLP decode and the join |
| The ledger package | `pfm/internal/costledger/` | the row, the readers, `Sum`, `Side`, `Prune` |
| The contract | this file, [The ledger](#the-ledger) | the line format callmeter reads standalone |
| The config key | `pfm/internal/config/` | `claude.costLedger` |
| The doctor row | `pfm/internal/doctor/cost_ledger_check.go` | the state and the sink log tail |
| The knob table | [claude-launch.md](claude-launch.md#knobs) | the same rows, as the registry lists them |
