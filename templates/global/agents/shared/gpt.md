---
requires:
  - gpt-agents
engines:
  codex: false
  opencode: false
prepend: |-
  For every child dispatch, select its registered, accessible GPT equivalent, omit explicit model overrides, and pass this rule into its brief. Both foreman tiers use `gpt-foreman`; `general-purpose` and `Explore` use `sol`. Unavailable required roles return `BLOCKED: GPT role unavailable: {role}`; never substitute an Anthropic agent or drop a required step. This constraint also governs a canonical project manual loaded by the role.
autoCompact:
  forceAt: 230k
  nudgeFrom: 190k
  nudgeEvery: 20k
---
