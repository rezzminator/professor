# 2026-10-13 — a probe script's glob deleted 221 transcripts

Observed: on 2026-10-11 a flights-foreman probe script (`drive.sh`, transcript d560ba39…/subagents/agent-afa3f7dcaf0e4af12.jsonl:85) cleaned up with `rm -rf "$PROJECTS"/*"$(basename "$WORK")"*`; `WORK` ended in `/work`, so it ran `rm -rf ~/.claude/projects/*work*` and erased every project folder containing "work" — 221 sessions, a private project's 9 chats among them. No backup existed (Time Machine never configured).

Amend: the Claude transcript store is never a glob target — a cleanup removes only the exact paths it created, and a probe runs under its own CLAUDE_CONFIG_DIR/HOME (the fence) so its transcripts never land in the real store; candidate homes: templates/global/agents/flights-foreman.md and the git-guard/kill-guard hook family as a transcript-store rm guard.
