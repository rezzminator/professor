---
name: gpt-agent-investigator
from: agent-investigator
extends:
  - shared/gpt.md
description: agent-investigator on GPT sol high
model: gpt-6.1-sol[1m]
effort: high
visibility:
  visible-to:
    include:
      - main
rename:
  transcript-reader: gpt-transcript-reader
  Haiku: GPT luna
---
