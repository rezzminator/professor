---
# professor: SOURCE TEMPLATE — edit here for a framework change (routes through /pcm); project-scaffold customization belongs in its installed local source; engine mirrors are never hand-edited.
name: chatlog
description: 'Digests and prices agent runs — "what did that agent do", "where did the tokens go", "what does model X cost"; Claude, Codex or OpenCode, by path, id, chat name or flight: `pfm transcript show|counts|types|locate|agents`, `pfm tokens` (--codex --opencode --flight --timeline --family --session --since), `pfm model-cost`. Returns a digest or a spend report. Static context → /context-meter.'
---

# chatlog

Three pfm verbs over Claude Code transcripts, Codex rollouts and OpenCode sessions:

- `pfm transcript {verb} {target} [filters]`: what one session, sub-agent, Codex thread or OpenCode session did, one line per event.
- `pfm tokens [flags]`: what runs cost, ranked and attributed per agent, priced from pfm's live price table.
- `pfm model-cost`: that price table.

All three are read-only on every transcript; a report reaches a file only through `--out`, `--metrics-out`, `--view` or `--briefs`, or as a flight's own `metrics.md`, and the daily price refresh `pfm tokens` and `pfm model-cost` share may rewrite the clone's `prices.json` and its check stamp. They search pfm's resolved Claude roots, Codex homes and OpenCode roots (each root's `opencode.db`, opened read-only). Transcript content can carry sensitive prompt text: read the digest or the report, never pipe or retain transcript bodies.

## What a run did — `pfm transcript`

Every failure prints `TRANSCRIPT FAILED — {reason}` on stderr: a bad flag or filter value exits 2; a transcript that cannot be found or read exits 1, including `NOT FOUND {id} — searched {roots}`, `AMBIGUOUS {id} — {n} transcripts: {paths}` (a name lists each with its size and last record), an empty or unparseable file and an unwritable `--out`. None of these is an empty transcript. A target that resolves while some roots or rollouts could not be read also prints `TRANSCRIPT WARNING — {n} places unreadable while resolving {target}: {errors}` on stderr: the true match may sit in what went unread.

A target is a transcript path, a session id or any unique prefix of one (a Codex rollout `…/sessions/YYYY/MM/DD/rollout-*-{id}.jsonl`, a Claude chat `…/projects/{slug}/{id}.jsonl`), a Claude sub-agent id, with or without `agent-` (`…/{session}/subagents/agent-{id}.jsonl`), a Claude chat name (case ignored; a chat's name is its last title record, so a renamed chat answers to its new name only, and several chats sharing one name are `AMBIGUOUS`), or a Codex agent path (`/root/{name}`), matched against each rollout's `session_meta` and unique within one thread tree only, so a reused path is `AMBIGUOUS`. An OpenCode session answers to its id, any unique prefix of it, its title (case ignored, as a chat name) or its locator `{store}#{session id}`, which `locate` prints and `agents` lists. `--root DIR` adds a Claude or Codex directory to the search (OpenCode is read from pfm's OpenCode roots alone); a file reached twice counts once. A Codex seat's name is not a target: `pfm chat resolve {name}` prints its session id in the third column while the seat is alive.

Name lookup reads the last 256 KiB of each session; a title outside that tail is missed, so resolve older titles by id or path. A failed directory scan or unreadable session yields `INCOMPLETE name lookup` instead of a unique match or `NOT FOUND`.

| Verb | Returns |
| --- | --- |
| `show` | The digest: a header, one line per event, then the `SKIPPED` line |
| `counts` | The header, then per tool for the calls kept by the filters: calls, errors, total and largest result, calls without a result |
| `types` | Record types and dispositions (rendered, header, skipped) inside `--lines`, counted; the three counts sum to `RECORDS`. Other filters are refused |
| `locate` | The resolved path |
| `agents` | A session's sub-agents, including nested `subagents/workflows/wf_*/` files, oldest first: id · agent type · description · first → last UTC timestamp · records · size · calls; `META ERROR {reason}` where its `.meta.json` is missing or unreadable; `AGENTS 0 — no subagents directory at {path}` for a missing directory; a failed stat or enumeration exits 1 |

### An OpenCode session

An OpenCode session is rows in its store, not a file, and the digest names what it maps:

- `STORE {db}#{session id} · N records: M messages, P parts (L = record number in store order; no file lines or bytes)` replaces `FILE`.
- `L{n}` and `--lines` are record numbers: each message in creation order, then its parts; a part with no message comes last as `part (no message)`. No record carries a byte size.
- `types` counts `message.user` and `message.assistant` (header records), `message.assistant (error)`, `part.{type}` and `part.text (synthetic)`.
- `agents` lists its child sessions, every depth: id · agent · title · span · records · calls; `AGENTS 0 — no child session of {id} in {store}` when it has none.
- A failed assistant message reads `NOTE ENGINE ERROR — {name}: {message}`; an attached file reads `[file {name}]` in its prompt.
- A locator whose store is missing fails as `cannot read {locator}: no opencode store at {db}`, one whose store lacks the session as `cannot read {locator}: no such opencode session {id} in {db}` (`locate` included), a session with no records as `empty transcript: {locator}`, each exit 1. An id or title with no store to search is `NOT FOUND`.

### Reading the digest

- Header: engine, session or agent id, model and effort, agent type and description (a sub-agent's `.meta.json`), `FILE` with its size and record count, `NAME` (the chat's title, when it has one), `SPAN`, `CALLS` per tool with errors and calls that never got a result, and `FILTER`, which names what was kept and `shown N of M events`.
- Event lines start with `L{n}`, the transcript line of the record, and the UTC clock. They read `PROMPT`, `PROMPT(queued)` (a message the user typed mid-run), `PROMPT(plugin)` (a message a plugin submitted in the user's place), `SAY` (a reply mid-run), `NOTE` (task and system notifications, queued ones included, compactions, aborted turns), `FINAL` (a reply that ended its turn, printed whole), or a tool name and its target: `→ ok {size}` or `→ ERR {size}` (`EXIT {code}` for a command), `· {seconds}s` from 5 s up, or `→ NO RESULT` for a call that never got an answer, which usually means the run was cut off there.
- `UNFINISHED` before `SKIPPED`: the last turn has no final reply; the run was cut off or is still running. This warning applies even when filters hide that turn.
- A failed call prints the tail of its output under it, `    | `-prefixed. A command that succeeded prints its last output line in `‹…›`; a read prints only its size.
- Codex `exec` scripts appear as the commands, edits and MCP calls they ran. A script that ran none shows up as itself.
- Every record not rendered is counted by type on the `SKIPPED` line: token counts, encrypted reasoning, harness attachments, duplicate event records, and any type the reader does not know. Header records are listed after `| HEADER`.

### Filters

| Filter | Keeps |
| --- | --- |
| `--only KINDS` | Kinds from `prompt,reply,call,error,note,final`; default all but `error`. `error` without `call` keeps only failed calls |
| `--tool NAMES` | Only calls of these names, comma-separated or repeated (case ignored; Codex: `exec`, `edit`, `exec_command`, `apply_patch`, `mcp__{server}__{tool}`); prose stays |
| `--grep REGEX [-i]` | Events whose full text (input, whole result, prose) matches, RE2 syntax; up to three matching lines print under each with `~` |
| `--since` / `--until` | `HH:MM[:SS]` UTC on the session's first date — a chat spanning days takes an ISO time —, an ISO time, `+5m` from the start, `-15m` from the end |
| `--lines FROM-TO` | The events of those transcript lines (`FROM-` runs to the end; one number is one line) |
| `--first N` / `--last N` | The first or last N events once every other filter has run |
| `--results MODE` | `brief` (default), `none`, `tail:N` (the last N lines of every result), `full` |

Shape: `--text N` caps a prompt or reply (500 by default; 0 prints it whole), `--final N` caps the final reply (whole by default), `--width N` caps a call's target (180), `--err-lines N` sets a failure's tail (12), and `--out FILE` writes the digest there and prints its path and size.

### Reading a failed run

1. `show {id}`: the default digest. On the measured executor seats it is 13–33 KB where the raw rollout is 1.1–2.8 MB.
2. Follow up on a line the digest names: `--lines {n}-{m} --results full`, `--grep '{beat or test id}' --results tail:40`, or `--since HH:MM --until HH:MM`.
3. Open the raw `.jsonl` only at a line number that a finding needs.

## What runs cost — `pfm tokens`

`~/.claude/skills/chatlog/README.md` carries every flag, the mechanics, the schema notes and the counting rules. A usage error exits 2 before the price table loads; a table that cannot load or any read error exits 1.

### Which invocation answers which question

- Where did the last day go: no flags — the default report, bounded, with `data gaps:` and `CROSS-CHECK` lines.
- A longer window, one repo: `--since 3d --project <substr>`.
- Heaviest single runs: section `9 · TOP {N} SINGLE RUNS`; heaviest agent groups: section `8`; both carry each row's cache TTL (legend in the README's `--flight` section).
- One chat and its agents: `--family <title|agent-type|session-id-prefix>`, or `--session <sid-prefix>` when a sub-agent orchestrated the work and no chat title exists.
- Codex threads: `--codex` — one row per rollout thread, sub-agents attributed from `session_meta.source`.
- OpenCode sessions: `--opencode` — one row per session, each assistant message priced from its own token record; `OC USD`, OpenCode's own recorded cost, stands beside `EST USD`, never in its place. It refuses every Claude-only flag and mode (`--codex`, `--flight`, `--timeline`, `--root`, `--family`, `--session`, `--out`, `--view`, `--briefs` and kin, exit 2); a session's messages count only when created inside the window.
- One flight's agents: `--flight <dir>` — see below.
- One agent run call by call: `--timeline <transcript.jsonl>` (repeatable) — Claude transcripts only, a Codex rollout prints `NO CALLS`; whole file, no window; a header (agent type, models, effort, calls, wall, peak context, output, tool errors, results over 20 KB, USD) then one row per model call with its clock, wait since the last tool result, context, output, price and each tool it issued (`name: target`, result chars, `ERR`, wait).
- The full dataset for a page or a diff: `--out FILE` (JSON); the per-project page data: `--view FILE` (`--host-label NAME` sets the host it shows); every brief and what its agent did: `--briefs FILE` (`--family-rx RE` keeps the families whose titles match, case ignored).
- `--root DIR` (repeatable) reads these Claude roots in place of pfm's; `--codex-root DIR` reads this one Codex home in place of every Codex home pfm resolves; `--opencode-root DIR` reads the `opencode.db` in this directory in place of pfm's OpenCode roots; `--top N` sets the rows per section (12).

### One flight — `--flight <dir>`

```bash
pfm tokens --flight $HOME/.local/state/pfm/flights/{project}/<name>
```

Writes `<dir>/metrics.md` (override with `--metrics-out FILE`; `--out FILE` adds the JSON) and prints the same report, one row per agent, joined to its transcript through `<dir>/agents.tsv` (else `run.md`'s `CLAIMED` lines); a ledger row or a transcript the join cannot pair is listed under `UNMATCHED`, never dropped. The README's `--flight` section carries the columns, the ledger format and every matching rule.

### Reading the report

The `data gaps:` line is the report's own honesty, and `data gaps: none` means the scan was clean, not that nothing was checked; a model the table cannot price renders **`n/a`**, never `$0`. The README's honesty rules and counting rules carry the rest: what the gaps line names, `CROSS-CHECK`, Codex's resetting counters, and why current-price estimates are not historical bills.

## Current prices — `pfm model-cost`

`pfm model-cost <model-id>` prints one model's row of pfm's one price table (the newer of the clone's `prices.json` and the copy embedded in pfm, with `pfm.prices.json` merged in), USD per million tokens (a provider prefix or snapshot date resolves to its key), `--all` every row, `--json` the table document, `--check` the summary line only (exit 1 when the table cannot load). No mode, two modes or `--check --json` exits 2; an id the table does not price exits 1 with `{id} is not priced in the table fetched {time}; pfm model-cost --all lists every priced key`. The table is refreshed from the official Claude and OpenAI pricing pages at most once a day; `--force` refreshes now (exit 1 when the refresh fails), `PFM_PRICES_OFFLINE=1` never fetches. Without `--force` a failed refresh still exits 0 and serves the last table: stderr carries `warning: refresh failed, serving the table fetched {time}`, as it does an unpersisted refresh, a failed check stamp and an unusable clone file, so read stderr before trusting the prices. A failed refresh during `pfm tokens` is named in its gaps line.
