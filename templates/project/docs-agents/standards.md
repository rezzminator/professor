# Architectural Standards

Source of truth for cross-project architecture decisions. These rules **override** other docs. A design that conflicts with one of them is wrong — flag it back, do not design around it. Distilled from the root `CLAUDE.md` Non-Negotiable Rules and the child project `CLAUDE.md` placement conventions; this file is what those rules mean for _architecture_.

> **Ownership:** owned by `/pfm`. New standards land here through `/pfm`, never through a pipeline. A pipeline that discovers a missing standard reports it; the user invokes `/pfm` to add it. The main-loop session reads this in full before designing architecture.

<!-- INSTALL: replace the example sections below with this project's real invariants.
     Each section is one standard: a declarative heading + the rules that make it
     enforceable. Good standards are checkable ("every X carries Y") — not aspirations.
     Seed candidates: whatever the interview named as sacred ground, the project's
     data-isolation boundary, its cross-process contracts, and its test/env discipline. -->

## {SACRED_GROUND} is the first invariant

- State the boundary concretely: which identifier scopes every persisted row, queue message, log line, cache key, and external call — and why its absence is a defect, not a style issue.
- The boundary must hold across **every hop** of the system: a design that preserves it in one layer but drops it in the next is not protected.

## Cross-process contracts — no in-process assumptions

- Name the real transports (queue, HTTP, storage) and forbid their in-memory stand-ins outside tests.
- Every shared resource has **one writer**; everyone else goes through its contract.
- **Fail loud on misconfiguration** — a missing env that silently falls back to an in-process path strands work invisibly.
