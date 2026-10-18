---
# professor: SOURCE TEMPLATE — edit here for a framework change (routes through the `pcm` agent); project-scaffold customization belongs in its installed local source; engine mirrors are never hand-edited.
name: chatlog
description: 'Digests and prices agent runs, and is the transcript readers'' kit — "what did that agent do", "where did the tokens go", "what does model X cost"; Claude, Codex or OpenCode, by path, id, chat name or flight: `pfm transcript show|locate|agents`, `pfm tokens`, `pfm model-cost`, and `chatlog.py prep|read|find|note|report|verify|prompts|writes|runs|census`. Returns a digest, a spend report or a verified row report. Faults, an audit or "did it really do Y" → agent-investigator; static context → /context-meter.'
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
| `locate` | The resolved path |
| `agents` | A session's sub-agents, including nested `subagents/workflows/wf_*/` files, oldest first: id · agent type · description · first → last UTC timestamp · records · size · calls; `META ERROR {reason}` where its `.meta.json` is missing or unreadable; `AGENTS 0 — no subagents directory at {path}` for a missing directory; a failed stat or enumeration exits 1 |

### An OpenCode session

An OpenCode session is rows in its store, not a file, and the digest names what it maps:

- `STORE {db}#{session id} · N records: M messages, P parts (L = record number in store order; no file lines or bytes)` replaces `FILE`.
- `L{n}` and `--lines` are record numbers: each message in creation order, then its parts; a part with no message comes last as `part (no message)`. No record carries a byte size.
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
- OpenCode sessions: `--opencode` — one row per session, each assistant message priced from its own token record; `OC USD`, OpenCode's own recorded cost, stands beside `EST USD`, never in its place. It refuses every Claude-only flag and mode (`--codex`, `--flight`, `--root`, `--family`, `--session`, `--out`, `--view`, `--briefs` and kin, exit 2); a session's messages count only when created inside the window.
- One flight's agents: `--flight <dir>` — see below.
- One agent run call by call: `chatlog.py prep TARGET` and its `timeline.txt` (the kit, below): a clock, the wait since the last tool result, context, output and price per model call, with the `TOTALS`, `MODEL-WAIT` and `COSTLIEST` rows.
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

`pfm model-cost <model-id>` prints one model's row of pfm's one price table (the newer of the clone's `prices.json` and the copy embedded in pfm, with `pfm.prices.json` merged in), USD per million tokens (a provider prefix or snapshot date resolves to its key), `--all` every row, `--json` the table document, `--check` the summary line only (exit 1 when the table cannot load). No mode, two modes or `--check --json` exits 2; an id the table does not price exits 1 with `{id} is not priced in the table fetched {time}; pfm model-cost --all lists every priced key`. The table is refreshed from the official Claude and OpenAI pricing pages at most once a day per pfm binary, so a newly installed pfm refreshes on its first read; `--force` refreshes now (exit 1 when the refresh fails), `PFM_PRICES_OFFLINE=1` never fetches. Without `--force` a failed refresh still exits 0 and serves the last table: stderr carries `warning: refresh failed, serving the table fetched {time}`, as it does an unpersisted refresh, a failed check stamp and an unusable clone file, so read stderr before trusting the prices. A failed refresh during `pfm tokens` is named in its gaps line.

## The reader kit — `chatlog.py`

`K="python3 -I $HOME/.claude/skills/chatlog/chatlog.py"` does every mechanical step of reading a transcript, and counts across many (`census`), so a reader (the `transcript-reader` agent, or you) only reads and labels what needs reading. It reads the raw stores itself, natively on all three engines (no `pfm` call except `pfm model-cost --json --all`, the one price table, cached for a day), and resolves any target: a path, a session id or prefix, an agent id, a chat name, a Codex agent path, an OpenCode id, title or `{store}#{session}`.

| Verb | Does |
| --- | --- |
| `$K prep TARGET [--lines A-B] [--dir DIR] [--window 40000] [--reader-max 250000]` | the digest with noise collapsed (task notifications, system reminders, compact summaries), the timeline, every mechanical row, the reading windows; prints `DIR`, the header, `DIGEST … windows`, `TOOLS`, `MECH` counts, and `SPLIT … Lines: …` when one reader cannot hold it |
| `$K read DIR N` · `$K read DIR DA-DB` · `$K read DIR LA-LB` | window N, digest lines A–B (bare `A-B` too), or the digest lines rendering transcript lines A–B, result tails included; each as `D{n} ‹labels› {text}`. A selector outside the digest, or an L range whose records were dropped, prints `KIT FAILED — …` and exits 1, never an empty result |
| `$K find DIR REGEX [-C N]` | digest lines matching REGEX, case-insensitive, with their labels |
| `$K note DIR 'D41 CLAIM' 'D37 BEARS D41' "D88 CORRECTION - exact words"` | adds reader rows (line, label, ref, anchor), refusing a bad one with its reason: a line past the digest, a label outside `CORRECTION CLAIM BEARS WRITE O{n}`, a `BEARS` without its claim, an anchor not in its line |
| `$K report DIR [--out FILE]` | `report.tsv`: header (target, path, digest, timeline, tools), one row per labelled line copied from its source, counts |
| `$K verify PATH… [--print]` | every row of each report (a dir is searched for `report.tsv`) against its source line: `BAD` per mismatch, `FAILED` for an unreadable source, never a silent zero; `--print` prints the rows that held |
| `$K prompts DIR` | every user prompt, beside the assistant line before it |
| `$K writes DIR` | every file the run wrote (tool writes, redirections, `tee`, `sed -i`, `cp`/`mv` targets), its state now: size, mtime, git status, `GONE`, `UNRESOLVED` |
| `$K runs --type T [--since 7d] [--project SUBSTR]` · `$K runs --session S` | every run of one agent type across projects, or a session's sub-agents |
| `$K census --type T [--since 30d] [--project SUBSTR]` · `$K census --session S` · `$K census PATH…`, each `[--tree] [--out F]` | a count, cost or spawn tree across many runs: one TSV row per transcript (`id engine agent_type description start_utc wall_s calls errors denials compactions spawns usd final parent`) from the kit's own engines, labels and cost, then a `TOTAL` row. `--type` selects as `runs` does; `--session` takes a top-level Claude, Codex or OpenCode session and all its sub-agents. One `FAILED {path} — {reason}` line per transcript it could not read or whose ownership walk hit a read error (its dollars unsure), exit 1 when any; a `GAPS {path} — data gaps: …` line per transcript whose timeline names other data gaps, and one `GAPS prices — …` for the price table's, as `prep` prints them; a usage error exits 2. In the TOTAL and branch sums an unpriced cell counts as `+n n/a`, a failed one as `+n FAILED`. `--tree` prints the spawn tree (parent → child, indented) with calls and USD rolled up per branch; `--out F` writes the full TSV plus a `path` column, then the same FAILED and GAPS lines, stdout staying compact |

### The package and the engines

`chatlog.py` is the entry only (it adds its own resolved directory to `sys.path`, since `-I` drops it); the work is the stdlib package `chatlogkit/` beside it, one concern per module: `engines/{claude,codex,opencode}.py` (each raw record or store row into the one event model in `model.py`), `locate.py` (targets, a session's sub-agents, `runs`), `digest.py` (the digest and its header), `labels.py` (the mechanical labels, one rule set for every engine), `writes.py` (tool and shell writes), `cost/` (per-call timeline, TOTALS, COSTLIEST, MODEL-WAIT, priced from each record's usage), `rows.py` (read, find, note, report, verify, prompts, writes), `census.py` (the census verb) and `cli.py` (prep and the dispatch). Tests: `chatlog_test.py` and `census_test.py`, run by the gate.

| Engine | Source | Compaction | Spawn | Failed call | Cost per call |
| --- | --- | --- | --- | --- | --- |
| Claude | `projects/**.jsonl`, `subagents/agent-*.jsonl` + `.meta.json` | `compact_boundary` and `isCompactSummary` | `Agent`/`Task` call | `tool_result.is_error` | each assistant `usage` |
| Codex | `sessions/**/rollout-*.jsonl` | `compacted` record | `spawn_agent` call | nonzero exit, `Script failed`, failed patch | each `token_count` `last_token_usage` (de-duplicated), priced on that call's model |
| OpenCode | `opencode.db`, read-only | `compaction` part | `task` tool, `subtask` part | tool state `error` | each assistant message's `tokens` |

`L{n}` is pfm's numbering (the file line, or an OpenCode record in store order), so a citation opens the same record through `pfm transcript show --lines`. A label an engine has no concept of is a header row `LABEL n/a (reason)`, never a silent zero. The header also carries `RECORDS n = rendered + header + dropped + unparsed`, one `DROPPED {kind} n` row per kind the digest does not show, and `UNPARSED L{n}` per line holding no JSON; every record is counted. A model the price table lacks is `n/a` and named in the data gaps, never $0.

Mechanical labels `prep` sets: `BRIEF PROMPT FINAL NOTE NOTICE COMPACTION COMPACT-SUMMARY COMPACT-CALL HANDOFF SPAWN ERROR ERROR-OUTPUT DENIAL AFTER-ERROR NO-RESULT UNFINISHED WAIT WRITE REPEAT` on digest lines (`D{n}`), `TOTALS MODEL-WAIT COSTLIEST` on timeline lines (`M{n}`). Work directories land under `chatlog-kit/` in the system temp directory (`CHATLOG_KIT_DIR` overrides) unless `--dir` names one; the price table is cached there for a day.
