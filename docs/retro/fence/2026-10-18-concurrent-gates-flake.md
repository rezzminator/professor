# 2026-10-18 — concurrent iso gates flake under host contention

Observed: with four chats' gates in the fence at load 27-28, `iso gate templates` ran over its time budget and `iso gate pfm` stretched past six minutes; landers reported gate pfm and templates run concurrently flake under CONTENTION, and a fresh worktree's check-templates reports a jscpd GAP unless TOOLS_BIN points at the main checkout's tools.

Amend: .claude/commands/pfm-testing-manual.md — run one gate at a time per flight, and name TOOLS_BIN for a fresh worktree's check-templates.
