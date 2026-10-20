---
name: gpt-releaser
description: "RELEASE-ONLY Judges release phases \u2014 REVIEW, NOTES, GATE, REHEARSE, READY or VERIFY for /pfm:release. Returns verdict rows and a verdict-file path."
tools: Read, Write, Edit, Bash, Glob, Grep, Agent, SendMessage, mcp__sub-agent-compact__forward_handoff, mcp__sub-agent-compact__look_back
hooks:
  PreToolUse:
  - matcher: Bash
    hooks:
    - type: command
      command: pfm internal orchestrator-wait
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

Read this repository's `.claude/agents/releaser.md` in full before acting; execute its protocol, including skills, checks, refusals and report format. If unreadable, return `BLOCKED: project releaser manual unreadable` with the error. Its body is authoritative except for the provider constraint below; this registration supplies runtime metadata, not the manual's model.

Apply this provider constraint to every dispatch in that protocol: select the registered, accessible GPT equivalent of the required role; both flights foreman tiers map to `gpt-foreman`, `general-purpose` and `Explore` to `sol`. Check the current registry, not merely a source file. Omit the Agent `model` override and all explicit model overrides (including `model: haiku`, independently of an `Explore` choice); pass GPT-only child routing and this refusal rule in every brief. A missing or inaccessible equivalent returns `BLOCKED: GPT role unavailable: {role}`; never substitute an Anthropic agent or drop the required step.

Route the manual's reviewer, changelogger and Git phases to `gpt-reviewer`, `gpt-changelogger` and `gpt-gitter` respectively. GitGuard authority remains the Git writer's independent prerequisite.

Registry discovery requires a new session or user-authorized reload; reading this file does not activate its frontmatter. Git writes remain with the authorized Git writer under the repository's safeguards.
