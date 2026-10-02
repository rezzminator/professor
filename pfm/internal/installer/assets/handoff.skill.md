---
name: handoff
description: 'USER-ONLY Moves every open thread of the session to a NEW chat — the user types /handoff [--branch] [message]; never run it unasked. Writes a file, seeds a fresh chat from it: default reboots this pane into it; --branch starts it detached. Not for branching/forking this conversation → pfm chat branch.'
---

# `/handoff [--branch] [message]`

Write the handoff file first — this step is IDENTICAL in both modes. Then either reboot this pane
into the new chat (default) or spawn a separate detached one and leave this pane alone (`--branch`).

1. **Write the handoff file.** Its scope is the whole session, never just the current task: the
   new chat inherits every open thread and this chat keeps none. Enumerate the threads first, from
   the session's first message (a compaction summary included):
   - every user ask not yet closed;
   - every agent, chat, background command and waiter still running;
   - every repo's uncommitted, unmerged and unpushed work, and its worktrees;
   - every item held for the user.

   Then `mkdir -p ~/.local/share/pfm/handoff` and write
   `~/.local/share/pfm/handoff/<YYYYMMDD-HHMMSS>-<cwd basename>.md` with these sections, in this
   order:

   - **Task** — every ask of the session VERBATIM, in order, each marked done or open.
   - **State** — per thread, what's done and what's running: agent ids, chats, worktrees, commits,
     files touched.
   - **Decisions** — each one with its why.
   - **Open questions**
   - **Next steps** — ordered, concrete; the first one runnable as-is.
   - **Anchors**:
     - `path:line` for every file that matters;
     - this session's transcript path;
     - every checkpoint or scratch file in a session-scoped directory, copied beside the handoff
       file first (that directory dies with the session).
   - **Commands** — the exact gates/scripts to run.
   - **Rules learned** — constraints discovered this session.
   - **Owed to the user** — every item the final report must contain, every held thread included.

   The new chat has NO access to this conversation. Write what it needs to continue without
   asking: complete sentences, the user's own words wherever wording matters, never a summary of
   a summary. A thread from the enumeration that is absent from the file makes the handoff partial.

2. **Without `--branch` — reboot into the new chat and hide this one, once, via Bash:**
   ```
   ~/.local/bin/pfm chat reload --new --hide --then "Read <path> in FULL before anything else, then continue from its § Next steps. <the user's /handoff message, if any>"
   ```
   `--hide` is what hides the conversation being handed off — the command records it only after
   the reboot completes, so a failed reload leaves this chat listed and live. Keep any
   `--account N` / `--cache 1h|5m` / `--model` / `--effort` the user asked for. Never pass `--sock`.

   **With `--branch` — leave this pane and conversation completely untouched.** No reload, no
   hide. Instead start a SEPARATE, detached chat seeded with the handoff, once, via Bash:
   ```
   ~/.local/bin/pfm chat new --name "<short task name>" --cwd "<the current project dir>" "Read <path> in FULL before anything else, then continue from its § Next steps. <the user's /handoff message, if any>"
   ```
   Deliberately NO `--attach` — the seat is born detached and the human opens it themselves from
   the picker; that is the whole point of `--branch`. Never `--hide`, never `--sock`. The
   successor is born on THIS chat's engine — `chat new` defaults to the calling chat's engine;
   pass `--engine` only when the user asks for the other one. Keep any
   `--account N` / `--cache 1h|5m` / `--model` / `--effort` the user asked for — `chat new` accepts all of
   those.

3. **Without `--branch`** — reply ONE short line, the handoff path, and END THE TURN. In-flight
   sub-agents die with the reboot, so land or checkpoint them in the file FIRST — this warning
   applies only here; nothing reboots under `--branch`.

   **With `--branch`** — reply with BOTH the handoff file path AND the new chat's name, so the
   human knows what to open. Do not end the turn early and do not treat this chat as finished:
   nothing rebooted, nothing was hidden, and this conversation keeps going exactly as before.
