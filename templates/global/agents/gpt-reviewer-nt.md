---
name: gpt-reviewer-nt
from: reviewer-nt
extends:
- shared/gpt.md
description: "Reviews without tracer dispatch \u2014 pre-merge or post-merge diff ranges, HEAD WORKTREE and optional PATHSPECS/REPORT_PATH. Returns a review path."
model: gpt-6.1-sol[1m]
effort: high
unset:
- effortControl
tools: Read, Write, Grep, Glob, Bash, mcp__sub-agent-compact__forward_handoff, mcp__sub-agent-compact__look_back
---
