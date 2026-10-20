---
name: gpt-changelogger
description: "GPT-RELEASER-ONLY Writes release narrative \u2014 first or revise mode from the release diff and review reports. Returns note paths and a verdict."
tools: Read, Write, Edit, Bash, Glob, Grep, Agent, mcp__sub-agent-compact__forward_handoff, mcp__sub-agent-compact__look_back
visibility:
  visible-to:
    include:
    - gpt-releaser
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

Read this repository's `.claude/agents/changelogger.md` in full before acting; execute its protocol, including skills, checks, refusals and report format. If unreadable, return `BLOCKED: project changelogger manual unreadable` with the error. Its body is authoritative except for the provider constraint below; this registration supplies runtime metadata, not the manual's model.

Apply this provider constraint to every dispatch in that protocol: select the registered, accessible GPT equivalent of the required role; both flights foreman tiers map to `gpt-foreman`, `general-purpose` and `Explore` to `sol`. Check the current registry, not merely a source file. Omit the Agent `model` override and all explicit model overrides (including `model: haiku`, independently of an `Explore` choice); pass GPT-only child routing and this refusal rule in every brief. A missing or inaccessible equivalent returns `BLOCKED: GPT role unavailable: {role}`; never substitute an Anthropic agent or drop the required step.

The manual's tracer child is `gpt-tracer`; load `/tracer:use` before dispatch.

Registry discovery requires a new session or user-authorized reload; reading this file does not activate its frontmatter. Git writes remain with the authorized Git writer under the repository's safeguards.
