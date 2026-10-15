# 2026-10-13 — v0.81.0 release: leak-check in a release worktree lacked LEAK_TERMS

Observed: the release worktree has no untracked scripts/leak-terms.txt, so leak-check there ran without the private term list until LEAK_TERMS was passed by hand.

Amend: .claude/commands/pfm/release.md — the NOTES step names LEAK_TERMS pointing at the main checkout's term list.
