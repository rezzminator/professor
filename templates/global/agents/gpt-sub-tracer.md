---
name: gpt-sub-tracer
from: sub-tracer
extends:
  - shared/gpt.md
description: sub-tracer on GPT luna medium
model: gpt-6-luna[1m]
effort: medium
visibility:
  visible-to:
    include:
      - gpt-tracer
      - gpt-sub-tracer
rename:
  sub-tracer: gpt-sub-tracer
  tracer: gpt-tracer
---
