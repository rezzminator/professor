---
name: gpt-clone-distiller
from: clone-distiller
extends:
- shared/gpt.md
description: "Investigates external repository code \u2014 implementation and history questions from a clone URL and numbered questions. Returns a result path and commit-linked findings. This repository \u2192 gpt-tracer."
model: gpt-6.1-sol[1m]
effort: high
rename:
  tracer: gpt-tracer
---
