// Package statusline renders the lines pfm draws under an engine's composer:
// Render, Claude Code's and Codex's main status line; Agents, the facts of a
// Claude Code session's running sub-agents the professor plugin's agent row
// draws; Paced, the front that serves
// a chat's cached line and the detached worker that renders it; RefreshCodex
// and SpawnDetached, the detached cache refreshers a render arms.
//
//   - render.go: Render, the main line from its payload, and the 🔖 label's tmux window name.
//   - line_one.go: line 1, the badge, model, effort, places and names, and what a narrow terminal gives up.
//   - runtime.go: Runtime, a render's paths, engine, environment and seams.
//   - input_validation.go: out-of-range payload values degraded to unknown, one note each.
//   - herdr_name.go: a Claude /rename carried to the chat's native Herdr seat; the render's fleet database.
//   - paced.go: Paced, the per-chat front and its detached render worker.
//   - process.go: SpawnDetached and the Codex App Server rate-limit read.
//   - refresh_codex.go: RefreshCodex, the detached Codex rate-limit cache refresh.
//   - codex_weekly.go: the Codex weekly quota row from the shared Codex cache.
//   - rate_limits_cache.go: the quota windows' cache and their reset stamps.
//   - full_width_row.go: quota readings drawn as labelled full-width bars.
//   - cache_window.go: the prompt cache's time left and hit share, 💾1h✓59m28s 94%.
//   - keepwarm.go: the keep-warm shield (cache-live-control plugin) beside the cache window.
//   - instrument.go: line 2, the chat's instrument row: the context block's width table, clock, spend, cache, and what a narrow terminal gives up.
//   - pulse.go: the context strip, the ▰▱ gauge of the share used (fillBar) and the pulse of the growth a step.
//   - spend_text.go: a spend's forms, words, slashes or dollars alone.
//   - context.go: the main line's spend and the context gauge's facts.
//   - context_capacity.go: context windows from the gateway's catalog, read once per render.
//   - ledger_spend.go: a session's price from the cost ledger.
//   - scan_checkpoint.go: checkpoints that resume the whole-transcript folds.
//   - model_segment.go: the model and effort block.
//   - session_effort.go: RecordSession and ReadSession, the effort the main line last showed.
//   - palette.go: the 256-colour palette the main line draws in, handed to the agent row through agents_look.go.
//   - agents.go: Agents, every running sub-agent's facts in one read, the door of `pfm statusline --agents`.
//   - agents_look.go: the main line's look and units an agent's facts carry.
//   - agents_running.go: the running sub-agents, read from the session's subagents directory.
//   - agent_ends.go: the agents a transcript's task notifications end, each at the time of its end.
//   - peripheral.go: the fleet and git segments.
//   - subagents.go: what an agent's own transcript says; the machine's served price table.
//   - subagents_nest.go: the agents below an agent's task.
//   - subagents_spend.go: what an agent billed over its whole transcript.
package statusline
