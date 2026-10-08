# statusline

pfm shows how full each context is where the operator already looks. Every sub-agent gets its own row in Claude Code's agent panel through the `subagentStatusLine` setting. The main statusline carries the model and effort in one block, in the same palette, so a row and the main line read alike. Both commands ride the launch `--settings` payload of every Claude chat pfm starts; no settings file carries them.

Decisions live in this file. A change lands here first, then in the code, then in every surface under [Surfaces that stay in sync](#surfaces-that-stay-in-sync).

## Contents

- [What Claude Code offers](#what-claude-code-offers)
- [How a chat gets it](#how-a-chat-gets-it)
- [The sub-agent row](#the-sub-agent-row)
- [Where each field comes from](#where-each-field-comes-from)
- [Effort](#effort)
- [The main statusline](#the-main-statusline)
- [Palette and glyphs](#palette-and-glyphs)
- [What it does not do](#what-it-does-not-do)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## What Claude Code offers

Read in the Claude Code 2.1.281 source and confirmed live:

- `subagentStatusLine`: `{"type": "command", "command": "…"}`, a settings key like `statusLine`, read from any settings layer, the `--settings` flag included. Claude Code runs the command 300 ms after the first task appears and then every 5 s, with a 5 s timeout, and only in a trusted workspace.
- Its stdin is one JSON object: `session_id`, `transcript_path`, `cwd`, `scratchpad_dir`, `prompt_id`, `columns` and `tasks[]`. Each task carries `id`, `name`, `type`, `status`, `description`, `label`, `startTime`, `model`, `effort`, `contextWindowSize`, `tokenCount`, `tokenSamples` (the last 16 readings) and `cwd`.
- `tokenCount` is the latest input tokens plus the cumulative output tokens plus an estimate of what is streaming.
- Its stdout is one `{"id", "content"}` line per task. A row decorates only a task row; the `⏺ main` row has no decoration slot, and a line whose id is not in the current task list is dropped.
- Claude Code draws the row body faint in its muted theme colour.
- Claude Code, not the command, decides how long a row stays: 2.1.282 evicts a finished task 30 s after it ends (`evictAfter`, `GA=30000`), unless something keeps it (`keepaliveReasons`: a background agent's result not yet delivered, an agent below it still running) or the operator has it open (`retain`). A task in the payload is one not yet evicted, so the command can shape a finished row but never remove it.

## How a chat gets it

The status lines are one row of the launch registry `claudelaunch.Knobs` (`pfm/internal/claudelaunch/knobs.go`), a constant, rendered by `claudelaunch.Render` into the single `--settings` JSON every interactive Claude launch carries ([claude-launch.md](../engines/claude-launch.md#knobs)):

```json
{
  "statusLine": { "type": "command", "command": "~/.local/bin/pfm-statusline", "padding": 0, "refreshInterval": 3, "hideVimModeIndicator": true },
  "subagentStatusLine": { "type": "command", "command": "~/.local/bin/pfm-statusline --subagents" }
}
```

The command is rendered with `~` expanded to the home directory. `~/.local/bin/pfm-statusline` overlays `pfm statusline`. Installation writes no `statusLine` or `subagentStatusLine` into an account `settings.json`; an old pfm value there produces the `pfm-settings` host check’s BLOCK row because it would run beside the launch payload. Doctor prints the keys and the operator’s removal fix, and installation refuses until resolved. A Claude run passed through by the launcher and a headless run carry no status line. Spawn-audit reads each live chat’s argv.

## The sub-agent row

`pfm statusline --subagents` (`pfm/internal/statusline/subagents.go`) prints one row per task with an id and a size, fields in this order, joined by a bare `│` with no padding, because the panel is narrow:

| Field | Shows | Rule |
| --- | --- | --- |
| nested | `2/5` | agents below this one working right now (their own turn is open), in green, over every agent below it at any depth, in the tools colour; `solo`, muted, when it spawned none. It leads the row because a parent row is read for it |
| gauge | `▰▰▱▱▱▱▱▱ 31% 312.0K/1.0M` | `tokenCount` of `contextWindowSize` |
| spend | `$1.24/3.1M/42K/12` | follows the gauge after a space, not a `│`: USD, prompt tokens (uncached, cache read, cache write), output tokens, distinct `tool_use` calls, billed by this agent and every agent below it at any depth, over each whole transcript, so spend from before a compaction counts. A response counts once per `message.id`; a usage block whose counts sit only in `usage.iterations` is folded, and a cache write is priced by its TTL split, the uncovered part at the 5-minute rate. Rates come from pfm's price table (`pfm/internal/pricing/prices.json`, the newer of the clone file and the embedded copy, plus any `pfm.prices.json` override), read without a fetch; a row's long tier applies to a response whose context is above its threshold, as `/tokens` prices it. A floor carries a yellow `+?`: a model the table cannot price, or a transcript below it that cannot be read. `$?` when nothing could be priced, `$?/?/?/?` when the agent's own transcript cannot be read |
| identity | `scout·tracer` | the task name, then its role (`agentType`) |
| model | `opus·🏎️ high` | the model family, then the effort (see [Effort](#effort)) |
| status | `running 2m:0s` | status plus time since `startTime`, in the cache window's h:m:s shape; a finished task's clock stops at its transcript's last entry. `delegating` replaces a stopped status while any agent below still works, and its clock runs on |
| idle | `idle 1m20s` | only while running and silent for 60 s or more; yellow, red from 5 min |
| errors | `1 error` | tool results marked `is_error`; absent at zero |
| cache | `💾5m✓3m:8s 94%` | the main line's cache segment, per agent: the time left on the agent's own prompt cache (the length its newest cache write used, from its `usage.cache_creation` split, counted from its newest request — the row payload carries no `prompt_cache` per agent), then cache reads over the whole prompt on its newest call, green from 80, yellow from 50, red below — muted as `was 94%` once the window has lapsed; `💾–` before its first reply |
| compactions | `⟲2` | `compact_boundary` entries; absent at zero |
| cwd | `repo` | only when it differs from the session's cwd |
| label | the task's label, else its description | |

A row wider than the panel fits itself to the payload's `columns`, less 8 for Claude Code's own `❯ ⏺` prefix (two leading spaces and one trailing space included) and a margin (`rowPrefixWidth`, measured on a live 125-column pane). It gives parts up in rank order until it fits: cwd, cache, compactions, then the effort, leaving the model family alone. Nesting, gauge and spend, identity, status, idle, errors and the label always stay, and the label is last, so Claude Code's own truncation cuts only the label. A payload without `columns` keeps the full row.

The row opens with `ESC[22m`, which cancels Claude Code's faint, so the colours read at full strength.

### A finished row

A row is finished once its status is `completed`, `failed`, `killed` or `error` and no agent below it still works. Claude Code marks an orchestrator completed while its background workers work on; that row says `delegating` and stays a full row.

- A completed row, for its first minute: the whole row in one muted colour, without `ESC[22m`, so Claude Code's faint stays on and the row reads as disabled.
- A failed, killed or errored row, for its first minute: a full row, because it is an alert.
- Any finished row after one minute since its transcript's last entry: collapsed to `completed 3m0s ago│$1.24/3.1M/42K/12│scout·tracer│label`, muted, the status word red when it is a failure.

## Where each field comes from

Claude Code keeps each sub-agent's files beside the session's transcript: `{transcript minus .jsonl}/subagents/agent-{id}.jsonl` and `agent-{id}.meta.json`. The task id is the agent id.

- Read from the transcript: tools, errors, cache, compactions, the last entry's time. The reader skips a streamed assistant line repeated with the same `tool_use` id, ignores `tool_use` text quoted inside a tool result, and skips a torn final line, since the agent may be mid-write.
- Read from the meta file: the role (`agentType`).
- Read from every meta file in the directory, once per render: the nesting. An agent spawned by another agent carries `parentAgentId` (and `spawnDepth`); one spawned by the main loop carries neither. An agent is working while its own turn is open; an orchestrator that ended its turn to wait on background workers is not working, its workers are, and its row says `delegating`. A turn is open unless the transcript's last message entry is an assistant message with a `stop_reason` and no `tool_use` (`pfm/internal/statusline/subagents_nest.go`).

An unreadable fact renders as a failure to look, never as zero or empty: `$?/?/?/?`, `💾!`, `role ?`, `?/?` when the directory or any meta file cannot be read (its parent is then unknown), and `(1 unread)` for an agent below whose transcript cannot be read. The cause goes to stderr, one line per row. This covers a payload with no session transcript, a missing file, a torn meta file and a meta file without `agentType`.

## Effort

The effort shows as an emoji and its level, in the model block of both the main line and every row:

| Level | Emoji |
| --- | --- |
| low | 🚲 |
| medium | 🏍️ |
| high | 🏎️ |
| xhigh | 🚀 |
| max | 🛰️ |
| a level Claude Code adds later | 🔆 |
| thinking off (main line) | 💤 in place of the level's emoji, muted, with `(off)` |

A sub-agent without an effort of its own runs at its parent's live effort for its own model. The row payload then carries no effort, because `task.effort` is only the agent definition's. To fill the gap, the main statusline records the session's effort and model on every render, in `$PFM_SID_DIR/statusline-effort-{session}` (`pfm/internal/statusline/session_effort.go`). A row with no effort of its own shows that record: in full colour on the same model version, and muted on a different one, where Claude Code may resolve another level. A record that fails to read costs only the effort on that row, with the cause on stderr.

## The main statusline

- After the context gauge, the spend block reads `💰$3.20/1.2M/40K/88`: Claude Code's own `cost.total_cost_usd` (dim, yellow from $2, red from $10), then the prompt tokens, output tokens and distinct tool calls of the chat's transcript and every sub-agent transcript beside it, the same scope that cost covers. It is read the way the sub-agent spend is, a response once per `message.id`, its last line. `+?` marks a sub-agent transcript that could not be read, and `?/?/?` a chat transcript that could not be. A Codex chat keeps `🧮` context tokens and `✎` prompts in its place (`pfm/internal/statusline/context.go`).
- The model block reads `◆ Opus 5.5·🚀 xhigh`: model symbol and name, a muted `·`, then the effort (`pfm/internal/statusline/model_segment.go`).
- The session label sits second from the end of the first line.
- The cache window reads `💾1h✓59m:28s 94%`: the cache window, the time left on the prompt cache, then the share of the last call's prompt read from it (the payload's `context_window.current_usage`; Claude only). That share changes only when a call completes, so once the window has lapsed it describes a warm cache that is gone: `💾5m✗4m:24s was 99%`, muted, never live health beside the expiry.
- The window (`1h` or `5m`) comes first from Claude Code's own `prompt_cache` object in the payload (`ttl`, `expires_at`), which it measures from its own requests (2.1.282: `summary()`, expiry = the newest request's time plus its TTL). When the payload carries no expiry — no cached request yet, or an older build — pfm's launch record decides, never the statusline's own environment: `fleetdb.LaunchFor(ctx, session_id)`, keyed by the `session_id` in the statusline payload, returns the `cache1h` the chat was launched with ([claude-launch.md](../engines/claude-launch.md#the-launch-record)). A session pfm never launched (`ErrNoLaunch`) falls to the transcript (`cacheAnchor`, `pfm/internal/statusline/cache_window.go`): the length from the newest cache write's `usage.cache_creation` split, else no cache marker. A failed launch read shows `💾⚠` and writes the cause to stderr, never a silent `5m`.
- The countdown runs to the payload's `prompt_cache.expires_at` when it carries one; otherwise from the newest request record in the transcript, a user record that is not a local command's echo, plus the window; a Codex rollout counts from its newest reply. Claude Code re-runs the command every `refreshInterval` seconds, so the countdown ticks while the chat is idle.
- The line uses the same palette as the rows (`pfm/internal/statusline/palette.go`).

## Palette and glyphs

One definition in `palette.go` serves both surfaces, so a part that means the same thing on both (model, effort, tokens, elapsed time) wears the same colour. The colours are bright 256-colour tones, chosen to stay legible over Claude Code's faint row body.

Glyphs obey the WebGL glyph guard (`pfm/cmd/pfm/webgl_glyph_guard_test.go`): no Block Elements, Braille or Powerline. The gauge uses Geometric Shapes (`▰▱`). `🏍️`, `🏎️` and `🛰️` are two code points each, the base plus a variation selector. A terminal that counts them as one column shifts the text after them by one cell; they have rendered in the host's tmux panes since install, and the single-code-point fallbacks are 🛵 (medium) and 🚘 (high).

## What it does not do

- It does not decorate the `⏺ main` row: Claude Code 2.1.281 renders no decoration there.
- It does not show a row for a task that has neither a token count nor a window.
- It does not render anything for Codex or OpenCode: `subagentStatusLine` is Claude Code's.
- It does not tell a killed child from a working one: a child stopped mid-turn leaves its turn open on disk and counts as running.

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The row renderer | `pfm/internal/statusline/subagents.go`, `subagents_nest.go`, `subagents_spend.go`, `session_effort.go`, `model_segment.go`, `palette.go` | fields, order, effort, spend, colours |
| The command | `pfm/cmd/pfm/statusline_command.go` | `pfm statusline --subagents` (combining it with `--refresh-gpt` is a usage error, exit 2) |
| The launch registry | `pfm/internal/claudelaunch/knobs.go`, `render.go` | the status-lines row: `statusLine` and `subagentStatusLine` in every launch's `--settings` payload |
| The launch record | `fleetdb.LaunchFor` over `pfm.db` table `launch` | the cache window per `session_id` |
| The account-file host check | `pfm/internal/hostcheck/owned.go` | `pfm-settings` BLOCK row for old pfm status lines, with the operator’s fix |
| The goldens | `pfm/internal/statusline/testdata/render-*.golden` | the main line byte for byte |
| The lane map | `infra/fence/lanes/` | its beat and its map row |
