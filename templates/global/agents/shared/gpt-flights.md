---
extends:
  - gpt.md
prepend: |-
  Your parent hears your result only through your final return; a message to `main` carries only what your manual sends there.
pins:
  Provider: sha256:f5749f2fad31e7d59645724b6b074aa83b26fa29c2b01a823394423a58551d45
---

## Provider

Select only the registered GPT equivalent of each required role; both foreman tiers use `gpt-foreman`. Omit the Agent `model` override and pass GPT-only routing in every child brief. If unavailable or inaccessible, return `BLOCKED: GPT role unavailable: {role}`; never substitute an Anthropic agent or skip the step.
