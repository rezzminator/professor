# professor

Claude Code plugin for Professor's terminal look: it draws each sub-agent's row in the agent list as one instrument strip, the same columns at the same cells in every row, so a glance down a column compares the agents.

```text
◯ ⎽⎽⎻⎼⎼⎻_⎼⎽⎽⎽⎽⎼⎼⎼     +4.5K  17% 171.9K/1.0M   │ planner              │ ◆ opus·▰▰ medium  │ running 12m04s │ Σ $3.41 · 2.0M in · 24.3K out · 57 tools   │ 💾5m✓3m08s 94%     │ 2 errors │ ⟲1 │ ↳ 1/3 below │ Map the parser › Reading lexer.go
└ ◯  ____________⎼↓  −22.0K   5%   9.0K/200.0K │ Explore              │ ○ haiku           │ done           │ Σ $?                                       │ 💾– ?%             │ pfm ✗ open agent-a3.jsonl: permission denied │ Find the callers
◯       ⎺⎻⎽⎻⎻¯⎽⎽⎽      +144  86% 172.9K/200.0K │ door·flights:foreman │ ◇ sonnet·▰▰▰ high │ running 1m01s  │ Σ $12.50+? · 812.0K in · 9.3K out · 1 tool │ 💾5m✗1m40s was 31% │ idle 3m20s │ @door │ adv $0.30 │ Build the door › Running go test
```

- Pulse gauge: the lit cells, from the left, are the share of the context window in use (green under 50%, yellow under 80%, red from there). Each cell's height is how much the context grew in one engine reading, about five seconds, newest on the right, on one scale for every row: `_` nothing, then `⎽ ⎼ ⎻ ⎺ ¯` from 1, 1K, 5K, 20K and 50K tokens. `↓` marks a reading in which it shrank by 1,000 tokens or more (a compaction); blank cells are before the agent's first reading, and the terminal shows the lit ones in reverse video. The newest reading's growth in figures, the share, and tokens over window follow it.
- Who: the agent's name and type. Model: its family's symbol and name, the effort bar and level.
- State: running, waiting, idle, pending, paused, done, failed or killed, with the time since it started, or how long it ran.
- `Σ`: dollars, prompt tokens, output tokens and tool calls of the agent and every agent below it; `+?` marks a lower bound, `$?` an unpriced sum.
- `💾`: the prompt cache's lifetime, the time left (`✓`) or since it lapsed (`✗`), and the share of the last prompt read from it; `–` no cached request, `∅` a model that keeps no cache.
- Flags, only when true: `pfm 45s ago` when pfm's facts for the row are that old (its last answer gave none for the agent), `idle` after a minute without a transcript entry, tool errors, `⟲` compactions, `↳` working/total agents below.
- Notes, where there is room: `@` the working directory when it is not the session's, `adv` the advisor's part of the sum, `API-eq` a sum priced at API rates.
- Then the task and, after `›`, what the agent is doing now.

The second line of pfm's status line is the chat's own row of the same instrument: the same pulse on the same scale, one cell for five seconds, then the share, tokens over the window, `Σ` and `💾` in the same order, units and colours, so the chat and its agents compare by eye.

The engine's row props give the live half; `pfm statusline --agents` gives the rest, the colours, the pulse scale and the colour thresholds, one call for every agent of the list, running or ended, every four seconds, and the rows are redrawn each second while an agent runs. A fact pfm has not answered yet draws as `…`, one it could not give as `?`. When pfm fails the rows keep the look it last gave; until its first answer, or when it is not on `PATH`, the rows keep the engine's fields without colour and the pulse gauge flat (its fill alone), the engine's own dollars marked `own`, the first row names the cause after `pfm ✗` and the others carry the mark.

When the terminal is too narrow every row gives up the same parts in the same order, so the columns stay aligned: the sum's words, the growth figure, the task past 20 cells, the flags' words (`2 err`, `↳1/3`), the sum's counts, half the gauge, the token figures, the cache's lifetime and hit share, the effort's word, the cache, the model's name, the sum, the model; then the name is cut to 12 cells and the task goes. The pulse gauge, the share, who, the state and the flags always stay; a row still too wide ends in `…`. A nested row, which the engine draws under its parent behind a `└` once that agent is viewed, sits on the same cells.

Install with `/plugin install professor --marketplace rezzminator/professor`. Requires function hooks (`CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1`), Claude Code 2.1.326 or later, and `pfm` on `PATH`. Remove it with `claude plugin uninstall professor@professor`. Logs go to `$PLUGIN_LOG_DIR/professor`, else `~/.local/state/professor/logs`.

Built and maintained with [Professor](https://github.com/rezzminator/professor).
