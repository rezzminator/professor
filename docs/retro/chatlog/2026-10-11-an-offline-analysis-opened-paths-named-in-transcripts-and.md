# 2026-10-11 — an offline analysis opened paths named in transcripts and took the host down

Observed: a throwaway script that sized every file path found in transcript tool calls opened each with an unbounded read(); one path was /dev/zero, the read never ended, two orphan python copies grew past 12 GB each and the host panicked on swap exhaustion. The repair was a helper that stats first, refuses anything but a regular file (S_ISREG), never opens /dev, /proc or /sys, caps every read and runs under an alarm plus a CPU limit. The user's standing rule since: any analysis that opens paths from transcripts reads only S_ISREG files with a bounded read().

Amend: templates/global/skills/chatlog/SKILL.md#rules — any ad-hoc analysis that opens a path found in a transcript stats it first, reads only regular files with a bounded read, never /dev, /proc or /sys, and runs under a timeout; /pcm retro may find a wider home (a global rule) for the same line.
