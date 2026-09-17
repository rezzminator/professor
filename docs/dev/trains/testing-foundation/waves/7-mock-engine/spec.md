# Wave 7 — mock-engine for every commit; a weaker real model for the release rehearsal

## mock-engine (Tier A, no model, no credential)

One scripted stand-in binary, `pfm/internal/mockengine` + `cmd/mock-engine` (built only for tests), that speaks what pfm needs from an engine: Claude Code's TUI pane shapes, hook invocations (UserPromptSubmit/Stop/PreToolUse with real JSON), statusline input, transcript JSONL writes, `/exit` and resume; Codex's rollout files, appendix hook and MCP-over-HTTP client calls; OpenCode's session store and MCP client. Behaviour comes from a script file per scenario (turns, delays on the fake clock, tool calls, a busy period, a background sub-agent, a menu left open, a crash). It replaces the three ad-hoc fakes in `pfm/e2e` (`fakeEngine`, `newFakeCodex`, `writeFakeHarnessClaude`) — one implementation, selected as `claude`/`codex`/`opencode` by argv[0], never a fourth engine in pfm's own engine list. Every Tier B lane beat that does not need a real model's judgement gets a Tier A twin on mock-engine, so the sequence's pfm-side logic is gated on every commit.

Staleness guard: mock-engine's protocol shapes are pinned by golden files captured from the real engines by the Tier B lanes (`lanes/capture-shapes.sh` writes them); a lane run whose real capture differs from the golden is a red row naming the drifted shape — the mock can never silently imitate an engine that no longer exists.

## Release rehearsal on a weaker model (Tier B, real)

The update rehearsal — express in the fence, the adopter chat shown which template files changed (`pfm update check`'s report) and asked to re-adapt them — runs on a non-frontier model on purpose: Codex with the Terra model at high effort by default (`run.sh --rehearsal-engine cx --rehearsal-model <id> --rehearsal-effort high`; the id lives in `lanes/budgets.yml`, never hard-coded). If a less instruction-following model still ends at `pfm update check` clean with express's own suite green, the adopter instructions are good; a frontier model passing proves much less. Pass = asserted end state + clean activity log (Wave 6), never the model's own claim.
