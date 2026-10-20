---
name: gpt-sub-rr
from: sub-rr
extends:
- shared/gpt.md
description: "GPT-RR-ONLY Digs research subqueries \u2014 one assigned lens and numbered questions from gpt-rr, gpt-rr-pro or gpt-rr-pro-max. Returns cited findings and a report path."
model: gpt-6.1-sol[1m]
effort: high
visibility:
  visible-to:
    include:
    - gpt-rr
    - gpt-rr-pro
    - gpt-rr-pro-max
---
