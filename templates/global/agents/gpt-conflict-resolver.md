---
name: gpt-conflict-resolver
from: conflict-resolver
extends:
- shared/gpt.md
description: "GPT-GITTER-ONLY Resolves stopped conflicts \u2014 Checkout, Operation, Ours, Theirs and Purpose from gpt-gitter. Returns RESOLVED or UNRESOLVED rows."
model: gpt-6.1-sol[1m]
effort: high
visibility:
  visible-to:
    include:
    - gpt-gitter
---
