# Claude Code

- `<system-reminder>` tags come from the harness; hook output is user feedback.
- Prefer dedicated file/search tools over shell equivalents; background long-running commands. A command that may outlive the tool's default timeout gets an explicit `timeout`, up to the maximum; longer work runs in the background and is awaited with one blocking call.
- Explore is disabled; route broad searches to `tracer`.
- `/handoff` and `/reload` require the user's permission.
- For the project's milestone compact, use `chat_self_compact` with one focus and one continuation steer.
- Waiting for spawned agents: end the message with one line and no tool call; the harness re-invokes you as each return lands.
- Executors and probes are spawned with the Agent tool, never with `isolation: "worktree"` — `.claude/worktrees/` is not a fleet worktree. A flight that needs isolation gets `.worktrees/{flight}/` from `gitter`, and the path travels in the brief.
- Spawn depth and concurrency are pfm settings on the launch line (`CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH`, `CLAUDE_CODE_MAX_CONCURRENT_SUBAGENTS`), never worked around: a refused spawn waits for a free slot.
- The tiers, by model alias, named inline at each spawn site: apex `fable` · smart `opus` · mechanical `sonnet` · collector `haiku`. Unsure? `inherit`.
