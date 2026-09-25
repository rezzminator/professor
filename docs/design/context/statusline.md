# statusline

pfm shows how full each context is where the operator already looks. Every sub-agent gets its own row in Claude Code's agent panel through the `subagentStatusLine` setting. The main statusline carries the model and effort in one block, in the same palette, so a row and the main line read alike.

Decisions live in this file. A change lands here first, then in the code, then in every surface under [Surfaces that stay in sync](#surfaces-that-stay-in-sync).

## Contents

- [What Claude Code offers](#what-claude-code-offers)
- [The sub-agent row](#the-sub-agent-row)
- [Where each field comes from](#where-each-field-comes-from)
- [Effort](#effort)
- [The main statusline](#the-main-statusline)
- [Palette and glyphs](#palette-and-glyphs)
- [What it does not do](#what-it-does-not-do)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## What Claude Code offers

Read in the Claude Code 2.1.281 source and confirmed live:

- `subagentStatusLine`: `{"type": "command", "command": "…"}` in `settings.json`. Claude Code runs the command 300 ms after the first task appears and then every 5 s, with a 5 s timeout, and only in a trusted workspace.
- Its stdin is one JSON object: `session_id`, `transcript_path`, `cwd`, `scratchpad_dir`, `prompt_id`, `columns` and `tasks[]`. Each task carries `id`, `name`, `type`, `status`, `description`, `label`, `startTime`, `model`, `effort`, `contextWindowSize`, `tokenCount`, `tokenSamples` (the last 16 readings) and `cwd`.
- `tokenCount` is the latest input tokens plus the cumulative output tokens plus an estimate of what is streaming.
- Its stdout is one `{"id", "content"}` line per task. A row decorates only a task row; the `⏺ main` row has no decoration slot, and a line whose id is not in the current task list is dropped.
- Claude Code draws the row body faint in its muted theme colour.
- Claude Code, not the command, decides how long a row stays: 2.1.282 evicts a finished task 30 s after it ends (`evictAfter`, `GA=30000`), unless something keeps it (`keepaliveReasons`: a background agent's result not yet delivered, an agent below it still running) or the operator has it open (`retain`). A task in the payload is one not yet evicted, so the command can shape a finished row but never remove it.

## The sub-agent row

`pfm statusline --subagents` (`pfm/internal/statusline/subagents.go`) prints one row per task with an id and a size, fields in this order, joined by `│`:

| Field | Shows | Rule |
| --- | --- | --- |
| nested | `2/5` | agents below this one working right now (their own turn is open), in green, over every agent below it at any depth, in the tools colour; absent when it spawned none. It leads the row because a parent row is read for it |
| gauge | `▰▰▱▱▱▱▱▱ 31% 312.0K/1.0M` | `tokenCount` of `contextWindowSize` |
| identity | `scout·tracer` | the task name, then its role (`agentType`) |
| model | `opus·🏎️ high` | the model family, then the effort (see [Effort](#effort)) |
| status | `running 2m0s` | status plus time since `startTime`; a finished task's clock stops at its transcript's last entry. `delegating` replaces a stopped status while any agent below still works, and its clock runs on |
| idle | `idle 1m20s` | only while running and silent for 60 s or more; yellow, red from 5 min |
| tools | `12 tools` | distinct `tool_use` ids in the sub-agent's transcript |
| errors | `1 error` | tool results marked `is_error`; absent at zero |
| cache | `cache 94%` | cache reads over total input on the newest assistant usage |
| compactions | `⟲2` | `compact_boundary` entries; absent at zero |
| cwd | `repo` | only when it differs from the session's cwd |
| label | the task's label, else its description | |

The row opens with `ESC[22m`, which cancels Claude Code's faint, so the colours read at full strength.

### A finished row

A row is finished once its status is `completed`, `failed`, `killed` or `error` and no agent below it still works. Claude Code marks an orchestrator completed while its background workers work on; that row says `delegating` and stays a full row.

- A completed row, for its first minute: the whole row in one muted colour, without `ESC[22m`, so Claude Code's faint stays on and the row reads as disabled.
- A failed, killed or errored row, for its first minute: a full row, because it is an alert.
- Any finished row after one minute since its transcript's last entry: collapsed to `completed 3m0s ago │ scout·tracer │ label`, muted, the status word red when it is a failure.

## Where each field comes from

Claude Code keeps each sub-agent's files beside the session's transcript: `{transcript minus .jsonl}/subagents/agent-{id}.jsonl` and `agent-{id}.meta.json`. The task id is the agent id.

- Read from the transcript: tools, errors, cache, compactions, the last entry's time. The reader skips a streamed assistant line repeated with the same `tool_use` id, ignores `tool_use` text quoted inside a tool result, and skips a torn final line, since the agent may be mid-write.
- Read from the meta file: the role (`agentType`).
- Read from every meta file in the directory, once per render: the nesting. An agent spawned by another agent carries `parentAgentId` (and `spawnDepth`); one spawned by the main loop carries neither. An agent is working while its own turn is open; an orchestrator that ended its turn to wait on background workers is not working, its workers are, and its row says `delegating`. A turn is open unless the transcript's last message entry is an assistant message with a `stop_reason` and no `tool_use` (`pfm/internal/statusline/subagents_nest.go`).

An unreadable fact renders as a failure to look, never as zero or empty: `tools ?`, `cache ?`, `role ?`, `?/?` when the directory or any meta file cannot be read (its parent is then unknown), and `(1 unread)` for an agent below whose transcript cannot be read. The cause goes to stderr, one line per row. This covers a payload with no session transcript, a missing file, a torn meta file and a meta file without `agentType`.

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

- The model block reads `◆ Opus 5.5·🚀 xhigh`: model symbol and name, a muted `·`, then the effort (`pfm/internal/statusline/model_segment.go`).
- The session label sits second from the end of the first line.
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
| The row renderer | `pfm/internal/statusline/subagents.go`, `subagents_nest.go`, `session_effort.go`, `model_segment.go`, `palette.go` | fields, order, effort, colours |
| The command | `pfm/cmd/pfm/statusline_command.go` | `pfm statusline --subagents` (combining it with `--refresh-gpt` is a usage error, exit 2) |
| The installer | `pfm/internal/installer/settings.go` | writes `subagentStatusLine` = the statusline overlay command plus ` --subagents` when absent, keeps an operator's own value, and uninstall removes only pfm's |
| The goldens | `pfm/internal/statusline/testdata/render-*.golden` | the main line byte for byte |
| The lane map | `docs/dev/testing/landscape.md` (T39), `infra/fence/lanes/` | the landscape row, its beat and its map row |

