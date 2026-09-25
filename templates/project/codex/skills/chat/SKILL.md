---
name: chat
description: Messages the tmux agent chats through `pfm chat` — `pfm chat inject {target} '{one line}'` sends a turn to a teammate's pane, `pfm whoami` gives your own address; read the delivery receipt. Use when a protocol says "ping", "inject", or "reply to {session}". Compaction → `pfm chat self-compact`.
---

<!--
HAND-WRITTEN CARD — no generated marker. `pfm codex build` preserves it because
chat is a host-level MCP/CLI surface with no project-local Claude command source.
This card carries the one page a Codex lane actually needs mid-flight.
-->

# pfm chat essentials (Codex shape)

Canonical tool: `$HOME/.local/bin/pfm chat`.

## Send (inject)

`$HOME/.local/bin/pfm chat inject {target} '{one-line message}'`

- `{target}` = exact tmux session name or a pane label; resolution scans every socket. Executor lanes get the orchestrator's address from the BRIEF.
- ONE line per message — send N injects for N lines. Plain text is auto-signed with your reply address; a `/`-prefixed harness command travels unsigned.
- Success prints `injected LIVE … Enter confirmed` plus a delivery-proof screen capture — read it; that is your receipt. Queued-on-busy is normal (a busy pane queues your turn).
- exit 3 = the target chat is dead.
- exit 4 = no matching chat; exit 6 = the message was not delivered.

## Who am I

`$HOME/.local/bin/pfm whoami` → your own address; include it in pings so verdicts route back.

## Coordination

Native subagents communicate through `collaboration.send_message` to the parent agent path; the parent waits with `wait_agent`. Separate terminal chats use the professor MCP's `chat_*` tools or `pfm chat inject` with the exact target supplied by the orchestrator. Agent paths such as `/root/...` are mailbox addresses, never terminal-chat targets. A delivery failure is reported to the caller with the failed target; no event spool is consumed by the harness.

## Boundaries

- Use `pfm chat self-compact` for compaction; `chat inject` rejects `/compact`. Check `pfm chat inject --help` for supported delivery flags.
- Never capture or scrape another pane to infer state — ping and ask; the orchestrator rules from reports.
