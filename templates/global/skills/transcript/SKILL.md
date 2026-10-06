---
name: transcript
description: 'Digests one agent session — "read that chat''s transcript", "what did the executor do": a Claude or Codex transcript by path, session id, agent id or chat name; `show|counts|types|locate|agents`, `--only --tool --grep -i --since --until --lines --first --last --results`, shape `--text --final --width --err-lines --out --root`. Returns one line per event or sub-agent. Spend → /tokens.'
---

# transcript

Run `python3 ~/.claude/skills/transcript/transcript.py {verb} {target} [filters]`, the path written in full every time. Python 3 standard library, read-only on every transcript; `--out FILE` is its only write. A failure prints `TRANSCRIPT FAILED — {reason}` on stderr and exits 2: `NOT FOUND {id} — searched {roots}`, `AMBIGUOUS {id} — {n} transcripts: {paths}` (a name lists each with its size and last record), an empty or unparseable file, a bad filter value. None of these is an empty transcript.

A target is a transcript path, a session id or any unique prefix of one (a Codex rollout `…/sessions/YYYY/MM/DD/rollout-*-{id}.jsonl`, a Claude chat `…/projects/{slug}/{id}.jsonl`), a Claude sub-agent id, with or without `agent-` (`…/{session}/subagents/agent-{id}.jsonl`), a Claude chat name (case ignored; a chat's name is its last title record, so a renamed chat answers to its new name only, and several chats sharing one name are `AMBIGUOUS`), or a Codex agent path (`/root/{name}`), matched against each rollout's `session_meta` and unique within one thread tree only, so a reused path is `AMBIGUOUS`. The script searches `$CLAUDE_CONFIG_DIR`, `~/.claude`, `~/.cc/*`, `$CODEX_HOME` and `~/.codex`, plus each `--root DIR`; `~/.cc/*` link to one tree, so each file counts once. A Codex seat's name is not a target: `pfm chat resolve {name}` prints its session id in the third column while the seat is alive.

## Verbs

| Verb | Returns |
| --- | --- |
| `show` | The digest: a header, one line per event, then the `SKIPPED` line |
| `counts` | The header, then per tool: calls, errors, total and largest result, calls without a result |
| `types` | Every record type and its disposition (rendered, header, skipped), counted; the three counts sum to `RECORDS` |
| `locate` | The resolved path |
| `agents` | A session's sub-agents, oldest first: id · agent type · description · first → last timestamp · records · size · calls; `META ERROR {reason}` where its `.meta.json` is missing or unreadable; `AGENTS 0 — no subagents directory at {path}` for a session that spawned none |

## Reading the digest

- Header: engine, session or agent id, model and effort, agent type and description (a sub-agent's `.meta.json`), `FILE` with its size and record count, `NAME` (the chat's title, when it has one), `SPAN`, `CALLS` per tool with errors and calls that never got a result, and `FILTER`, which names what was kept and `shown N of M events`.
- Event lines start with `L{n}`, the transcript line of the record, and the UTC clock. They read `PROMPT`, `PROMPT(queued)` (a message the user typed mid-run), `PROMPT(plugin)` (a message a plugin submitted in the user's place), `SAY` (a reply mid-run), `NOTE` (task and system notifications, queued ones included, compactions, aborted turns), `FINAL` (the last reply of a turn, printed whole), or a tool name and its target: `→ ok {size}` or `→ ERR {size}` (`EXIT {code}` for a command), `· {seconds}s` from 5 s up, or `→ NO RESULT` for a call that never got an answer, which usually means the run was cut off there.
- A failed call prints the tail of its output under it, `    | `-prefixed. A command that succeeded prints its last output line in `‹…›`; a read prints only its size.
- Codex `exec` scripts appear as the commands, edits and MCP calls they ran. A script that ran none shows up as itself.
- Every record not rendered is counted by type on the `SKIPPED` line: token counts, encrypted reasoning, harness attachments, duplicate event records, and any type the script does not know. Header records are listed after `| HEADER`.

## Filters

| Filter | Keeps |
| --- | --- |
| `--only KINDS` | Kinds from `prompt,reply,call,error,note,final`; default all but `error`. `error` without `call` keeps only failed calls |
| `--tool NAMES` | Only calls of these names (case ignored; Codex: `exec`, `edit`, `exec_command`, `apply_patch`, `mcp__{server}__{tool}`); prose stays |
| `--grep REGEX [-i]` | Events whose full text (input, whole result, prose) matches; up to three matching lines print under each with `~` |
| `--since` / `--until` | `HH:MM[:SS]` UTC on the session's first date — a chat spanning days takes an ISO time —, an ISO time, `+5m` from the start, `-15m` from the end |
| `--lines FROM-TO` | The events of those transcript lines (`FROM-` runs to the end) |
| `--first N` / `--last N` | The first or last N events once every other filter has run |
| `--results MODE` | `brief` (default), `none`, `tail:N` (the last N lines of every result), `full` |

Shape: `--text N` caps a prompt or reply (500 by default; 0 prints it whole), `--final N` caps the final reply (whole by default), `--width N` caps a call's target (180), `--err-lines N` sets a failure's tail (12), and `--out FILE` writes the digest there and prints its path and size.

## Reading a failed run

1. `show {id}`: the default digest. On the measured executor seats it is 13–33 KB where the raw rollout is 1.1–2.8 MB.
2. Follow up on a line the digest names: `--lines {n}-{m} --results full`, `--grep '{beat or test id}' --results tail:40`, or `--since HH:MM --until HH:MM`.
3. Open the raw `.jsonl` only at a line number that a finding needs.
