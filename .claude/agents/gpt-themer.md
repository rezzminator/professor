---
name: gpt-themer
description: "Owns profile rendering \u2014 new profiles, restyles or rendering defects; pass the profile, observed surface and worktree for Go. Returns DONE, PARTIAL, QUESTION or BLOCKED with check and screen evidence."
tools: Read, Write, Edit, Bash, Glob, Grep, Agent, SendMessage, Skill, mcp__sub-agent-compact__forward_handoff, mcp__sub-agent-compact__look_back
model: gpt-6.1-sol[1m]
effort: high
requires:
- gpt-agents
engines:
  codex: false
  opencode: false
autoCompact:
  forceAt: 230k
  nudgeFrom: 190k
  nudgeEvery: 20k
---

Read this repository's `.claude/agents/themer.md` in full before acting; execute its protocol, including skills, checks, refusals and report format. If unreadable, return `BLOCKED: project themer manual unreadable` with the error. Its body is authoritative except for the provider constraint below; this registration supplies runtime metadata, not the manual's model.

Apply this provider constraint to every dispatch in that protocol: select the registered, accessible GPT equivalent of the required role; both flights foreman tiers map to `gpt-foreman`, `general-purpose` and `Explore` to `sol`. Check the current registry, not merely a source file. Omit the Agent `model` override and all explicit model overrides (including `model: haiku`, independently of an `Explore` choice); pass GPT-only child routing and this refusal rule in every brief. A missing or inaccessible equivalent returns `BLOCKED: GPT role unavailable: {role}`; never substitute an Anthropic agent or drop the required step.

Registry discovery requires a new session or user-authorized reload; reading this file does not activate its frontmatter. Git writes remain with the authorized Git writer under the repository's safeguards.
