# 2026-09-21 — gitter refuses Phase SETUP over an untracked directory the flight never touches

Observed: the Codex-side gitter refused twice to create a flight worktree because the live checkout held one unrelated untracked directory (`docs/design/tracing/`); the orchestrator supplied explicit preservation authorization and gitter still refused, so flight `chat-mcp` died at setup with zero tasks started. The previous flight (`module-rename`) hit the same wall and only passed after a speccer revision round. `git worktree add` never reads, stages or moves untracked files; the Claude-side gitter created the same worktree in three calls with the live checkout's status byte-identical before and after.

Amend: `.claude/agents/gitter.md` (+ its `templates/project/**` twin) § SETUP — the clean-base requirement means no tracked modifications and no staged changes; untracked paths outside the flight's files are preserved, reported in the return, and never a reason to refuse. Same reading for the MERGE phase: an untracked path that no commit in the range touches does not block a fast-forward.

Resolved: 2026-09-22 — worked around all session by running SETUP through the Claude gitter; amendment to gitter.md still open → /pcm
