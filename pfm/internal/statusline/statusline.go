// Package statusline renders the lines pfm draws under an engine's composer:
// Render, Claude Code's and Codex's main status line; ServeSubagents and
// RenderSubagents, Claude Code's agent-panel rows; Paced, the front that serves
// a chat's cached line and the detached worker that renders it; RefreshCodex
// and SpawnDetached, the detached cache refreshers a render arms.
//
//   - render.go: Render, the main line from its payload, and the 🔖 label's tmux window name.
//   - runtime.go: Runtime, a render's paths, engine, environment and seams.
//   - input_validation.go: out-of-range payload values degraded to unknown, one note each.
//   - herdr_name.go: a Claude /rename carried to the chat's native Herdr seat; the render's fleet database.
//   - paced.go: Paced, the per-chat front and its detached render worker.
//   - process.go: SpawnDetached and the Codex App Server rate-limit read.
//   - refresh_codex.go: RefreshCodex, the detached Codex rate-limit cache refresh.
//   - codex_weekly.go: the Codex weekly quota row from the shared Codex cache.
//   - rate_limits_cache.go: the quota windows' cache and their reset stamps.
//   - full_width_row.go: quota readings drawn as labelled full-width bars.
//   - cache_window.go: the prompt cache's time left and hit share, 💾1h✓59m:28s 94%.
//   - cache_keepwarm.go: the cache-keepwarm shield beside the cache window.
//   - context.go: the main line's spend segment and context gauge.
//   - context_capacity.go: context windows from the gateway's catalog, read once per render.
//   - ledger_spend.go: a session's price from the cost ledger.
//   - scan_checkpoint.go: checkpoints that resume the whole-transcript folds.
//   - model_segment.go: the model and effort block.
//   - session_effort.go: RecordSession and ReadSession, the effort the main line last showed.
//   - palette.go: the 256-colour palette the main line and the agent rows share.
//   - peripheral.go: the fleet and git segments.
//   - subagents.go: ServeSubagents and RenderSubagents, the agent-panel rows.
//   - subagents_fault.go: the row input's cap and the fault shown on every row.
//   - subagents_fit.go: a row's parts dropped by rank to fit its width.
//   - subagents_gauge.go: a row's context gauge.
//   - subagents_nest.go: what a row says about the agents below its task.
//   - subagents_spend.go: what an agent billed over its whole transcript.
//   - subagents_trace.go: the opt-in, bounded trace of the rows' input and faults.
package statusline
