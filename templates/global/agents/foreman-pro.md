---
name: foreman-pro
from: flights-foreman
description: flights-foreman on opus high
model: opus
effortControl:
  floor: medium
  ceiling: xhigh
  landing: flights-lander gpt-flights-lander
add:
  tools: [mcp__effort-live-control__effort_up, mcp__effort-live-control__effort_down, mcp__effort-live-control__effort_auto]
---
