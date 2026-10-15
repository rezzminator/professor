# 2026-10-13 — v0.81.0 release: review prose tripped ready rule R1 twice

Observed: review report prose quoting `status:`, `- **F{n}:` or `F9 · resolved` matched the ready check's R1 grammar; READY failed twice on prose, not on findings.

Amend: .claude/agents/releaser.md — a REVIEW revise round runs the R1 grammar over its reports before returning DONE.
