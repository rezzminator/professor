---
name: pcm
description: 'USER-ONLY — Dispatches the pcm agent: `/pcm {change request}` edits CLAUDE.md, .claude/** or templates/** (themes → themer); `/pcm audit [scope|all]` reports framework consistency, read-only.'
argument-hint: "[change request|audit [scope|all]]"
disable-model-invocation: true
---

# PCM — Professor Change Manager

Dispatch the `pcm` agent (Agent tool, `subagent_type: pcm`) with this request verbatim, then relay its report:

> $ARGUMENTS

The dispatch carries all five fields (root `CLAUDE.md` § Dispatch):

- Goal and artifact: carry out the request above; the artifact is the agent's change report, or for `audit` the audit report.
- Boundary: in scope `CLAUDE.md`, `.claude/**`, `templates/**` and their docs and mirrors; out of scope `templates/themes/` and theme work (the `themer` agent), the pfm CLI (`/pfm`), publishing (`/pfm:release`) and any git write (`gitter`).
- Anchors: the files the request names, plus `docs/commands/pcm/references/` for an audit.
- Tier and effort: the agent's pinned `opus` at `high`; no budget cap.
- Failure shape: an unresolved question is returned as a question, never guessed; a blocked edit is reported with the file and the cause.

An empty `$ARGUMENTS` asks the user what to change before dispatching.
