---
name: gpt-pcm
from: general-purpose
extends:
  - shared/gpt.md
description: 'MANDATORY Edits framework prompts — CLAUDE.md, .claude/**, templates/** or engine mirrors; audit [scope|all]; retro [owner]. Returns changed paths and a consistency verdict.'
tools: Read, Write, Edit, Bash, Glob, Grep, Agent, Skill, mcp__sub-agent-compact__forward_handoff, mcp__sub-agent-compact__look_back
skills:
  - quality:prompt
model: gpt-6.1-sol[1m]
effort: high
append: |-
  Read this repository's `.claude/agents/pcm.md` in full before acting; execute its protocol, including skills, checks, refusals and report format. If unreadable, return `BLOCKED: project pcm manual unreadable` with the error. Its body is authoritative except for the provider constraint above; this registration supplies runtime metadata, not the manual's model.

  The prompt, description and orientation quality skills required by the manual remain required; `Explore` audit walkers and a separate cheap `model: haiku` choice both use registered `sol` with a read-only audit brief and no explicit model override.

  Registry discovery requires a new session or user-authorized reload; reading this file does not activate its frontmatter. Git writes remain with the authorized Git writer under the repository's safeguards.
---
