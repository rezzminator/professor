# 2026-10-11 — a brief capped the total number of test chats an agent could launch but not how many could run at once

Observed: the auto-effort experiments brief allowed 40 test chats in total with no concurrency limit. The agent launched its 14-chat pilot at once, an hour after a host crash, and the load average reached 137. The user had every chat killed, and three pilot runs were lost. The same run also showed two side effects of test chats: `/effort` typed into a test chat saved its level into the shared `~/.claude/settings.json`, and a test chat followed `pfm chat ask`'s "to reply" line and messaged the user's main chat.

Amend: CLAUDE.md § Dispatch: a dispatch that lets an agent launch chats or processes caps how many run at once, as well as the total. A test chat changes settings only through launch flags, never through a slash command that saves a default, and is told to answer only in its own chat.
