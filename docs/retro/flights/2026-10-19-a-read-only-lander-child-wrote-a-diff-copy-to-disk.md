# 2026-10-19 — a read-only lander child wrote a diff copy to disk

Observed: in a bench review run of the lander, one of its read-only children (hunter, spec walker or sub-lander, all `Read, Grep, Glob, Bash`) redirected a 483 KB `git diff` into `/tmp/professor/bench/x`, outside its worktree. The sub-lander and spec-walker prompts already forbid a redirect into any file in prose; the hunter prompt does not say it. Prose alone did not hold the line, and Bash keeps a write path open for every child.

Amend: templates/global/agents/flights-hunter.md#READ-ONLY — carry the same "no redirect `>` into any file, no copy of the diff on disk" clause as its two siblings; and, since the rule was already broken in prose, close the write path by mechanism (a PreToolUse guard refusing a Bash redirect or `tee` for the three FLIGHTS-LANDER-ONLY roles) rather than a third rewording.
