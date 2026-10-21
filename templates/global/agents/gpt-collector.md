---
name: gpt-collector
from: collector
extends:
- shared/gpt.md
description: "Copies exact code text \u2014 signatures, columns, line ranges or insertion anchors from numbered orders. Returns a verbatim-text path and per-order manifest. Questions \u2192 gpt-tracer."
model: gpt-6.1-sol[1m]
effort: high
---
