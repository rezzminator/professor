---
name: flights-foreman-pro
from: flights-foreman
description: 'Builds the toughest challenges, the highest level of intelligence among the builders — no pattern to follow, deep ambiguity, a root cause earlier attempts missed, concurrency, data integrity or security. Pass what flights-foreman takes. main chat → here → flights-worker. Returns DONE or PARTIAL, decisions, files, test lines.'
effort: xhigh
effortControl:
  floor: high
  ceiling: xhigh
add:
  tools: [mcp__effort-live-control__effort_up, mcp__effort-live-control__effort_down, mcp__effort-live-control__effort_auto]
append: |-
  Before a design choice the code leaves open, `effort_up`; once it is decided, `effort_auto`.
---
