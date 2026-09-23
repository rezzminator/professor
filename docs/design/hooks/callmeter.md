# callmeter

`callmeter` records every tool call a Claude chat or sub-agent makes, with what it touched and what it cost, into one SQLite file, and answers four questions from it: which files are read most and how large they were, which commands return the most text into a context, how context grows call by call, and which call sequences repeat often enough to deserve one command of their own. It exists because the measured cost of development here is context blown up in long-running agents, and nothing recorded where the context went.

Decisions live in this file. A change lands here first, then in the code, then in every surface under [Surfaces that stay in sync](#surfaces-that-stay-in-sync). The fleet's other hooks and the `pfm doctor` check that proves each one is installed live in [hooks.md](hooks.md).

## Contents

- [What it answers](#what-it-answers)
- [What the harness gives a hook](#what-the-harness-gives-a-hook)
- [The hooks](#the-hooks)
- [Where each fact comes from](#where-each-fact-comes-from)
- [The store](#the-store)
- [Parsing a command](#parsing-a-command)
- [Reports](#reports)
- [Corpus findings and fixes](#corpus-findings-and-fixes)
- [Build decisions](#build-decisions)
- [What it does not do](#what-it-does-not-do)
- [Evidence](#evidence)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)
- [Open items](#open-items)

## What it answers

1. Every call: the tool, its input, who made it (chat, sub-agent, agent type), how long it ran, whether it failed, and how many bytes the model received.
2. Files: read most, by count and by bytes delivered; their size on disk at read time; whole reads against ranged reads; the same file read again by the same agent; files written most and how much each write grew them.
3. Commands: which command shapes (`dev.sh test`, `go test`, `git diff`) deliver the most bytes into a context, per call and in total.
4. Context: the context size at every model request, per agent, so the call that pushed an agent from 40K to 400K has a name.
5. Sequences: runs of calls that repeat across agents (a search, then a read of the file it found, then a narrower read of the same file), ranked by how often they recur and what they cost, as candidates for one command or one script.

## What the harness gives a hook

Measured on Claude Code 2.1.280 with a capture hook that logged every raw payload; see [Evidence](#evidence).

| Event | Fires | Carries what callmeter needs |
| --- | --- | --- |
| `PreToolUse` | once per tool call, before it runs | `tool_use_id`, `tool_input`, `cwd`: the directory the command starts in |
| `PostToolUse` | once per tool call that ran | `tool_use_id`, `tool_name`, full `tool_input`, the tool's own result object, `duration_ms`, `cwd` (it follows the shell's `cd`: for `cd /x/pfm && grep …` started in `/x`, `PreToolUse` carries `/x` and `PostToolUse` `/x/pfm`), `session_id`, `transcript_path`; a sub-agent's call adds `agent_id` and `agent_type` |
| `PostToolUseFailure` | once per call that failed | the same, with `error` in place of the result |
| `PostToolBatch` | once per model request, after all its calls | `tool_calls`: every call of that request, each with `tool_use_id` and `tool_response` as the exact text the model received |
| `SubagentStart` / `SubagentStop` | once per sub-agent turn: an agent woken again (an orchestrator waiting on its children, a `SendMessage`) starts and stops once per turn | `agent_id`, `agent_type`; the stop adds `agent_transcript_path` and `last_assistant_message`, and fires 20-50 ms after the final message is stamped, before its line is flushed to the agent's transcript |
| `Stop` | once per turn of a chat | `transcript_path` |

What no payload carries: the context size of a request and the model message id. Both live in the transcript, keyed by `tool_use_id`. The request is not on disk when `PreToolUse` fires, is usually on disk when `PostToolUse` fires, and was on disk in every check made 0.5 s later.

Three sizes exist for one Bash output, and only one is the cost:

| Size | Where | `seq 1 7000` |
| --- | --- | --- |
| Real | `tool_response.persistedOutputSize`, or the stdout length when nothing was persisted | 33,893 |
| The tool's result object | `PostToolUse` `tool_response.stdout`, cut at `BASH_MAX_OUTPUT_LENGTH` (default 30,000 chars) | 30,000 |
| Delivered to the model | `PostToolBatch` `tool_response` | 2,241: a `<persisted-output>` notice, the file path and a 2 KB preview |

Output under 30,000 chars is delivered whole; output over it is saved to `{session}/tool-results/*.txt` and delivered as the preview. The costly band is therefore just under the limit, and a later `Read` of a `tool-results/` file is the model going back for the rest.

A `PostToolUse` hook can replace a Bash result before the model sees it, with `hookSpecificOutput.updatedToolOutput` (measured with a test-output filter; see [Evidence](#evidence)). `PostToolBatch` carries the text after that replacement, so the delivered size stays true when a filter is in play, and the gap between real and delivered bytes measures what a filter saved.

## The hooks

One command, `pfm internal callmeter`, registered in every Claude config dir `pfm install` writes, on seven events: `PreToolUse` takes matcher `Bash`; `PostToolUse`, `PostToolUseFailure`, `SubagentStart` and `SubagentStop` take matcher `*`; `PostToolBatch` and `Stop` take no matcher (empty), since neither event carries a tool name to match. Every entry is `async: true`, written by the installer and checked by `pfm doctor` as part of the registration itself, never as a separate flag. It is one command registered seven times: a duplicate registration is the same command appearing twice under one event, and a wrong registration is the command appearing under an event outside those seven.

- Async, because the hook fires on every call of every chat: a synchronous hook puts a process start and a database write in front of each call. Async also gives the transcript its time to reach the disk.
- `PreToolUse` for Bash only, and only for its `cwd`: the parser replays a command's own `cd` from the stored directory, so it must be the one the command started in, and only `PreToolUse` carries that. Everything a pre-call stat would give is in the result: `Write` and `Edit` return `originalFile` and a `structuredPatch`, so the size before and after comes from the result; `Read` returns `startLine`, `numLines` and `totalLines`, and the file is stat'ed when the record is written.
- The hook never blocks and never fails a call: it exits 0 on every path. A failure to record goes to pfm's log with the session and `tool_use_id`, and is counted in the store's `faults` table, so a report can say "N calls were not recorded" instead of showing fewer calls.
- Registered, checked and retired like every pfm-owned hook: [hooks.md](hooks.md).

## Where each fact comes from

| Fact | Source |
| --- | --- |
| Tool, input, duration, agent | `PostToolUse` / `PostToolUseFailure` |
| cwd | a Bash call's `PreToolUse`, the directory the command started in; `PostToolUse`'s, where the command left the shell, only fills a call whose `PreToolUse` was never recorded (either may land first: `PreToolUse` overwrites, `PostToolUse` fills only an empty `cwd`) |
| Real output size, persisted path | `PostToolUse` `tool_response` |
| Bytes delivered to the model; which calls shared one request | `PostToolBatch` |
| File size at read time | stat of `tool_input.file_path` when the record is written |
| File size before and after a write | `Write` / `Edit` result: `originalFile` and the new content or patch |
| Model message id and context size of the request | the transcript: the assistant message holding the call's `tool_use_id`, its `usage` (input + cache read + cache write). Read when `PostToolBatch` is recorded; a request not yet on disk is marked pending and filled at the next `Stop` or `SubagentStop` |
| Sub-agent transcript | `{transcript_path without .jsonl}/subagents/agent-{agent_id}.jsonl` |
| Parent of a sub-agent | the `Agent` call's result `agentId`, joined to `SubagentStart` |
| A sub-agent's totals | the agent's own transcript, read at every `SubagentStop` once its final message is on disk (the hook re-reads until the last assistant entry carries a stop reason other than `tool_use`, at most 3 s): `total_tokens` as the sum over its requests of input + cache read + cache write + output, `tool_uses` as its count of `tool_use` blocks. The latest stop overwrites, so an agent woken for more turns holds all of them; `started` stays its first start, `stopped` its last. The `Agent` call's result (`totalTokens`, `totalToolUseCount`, `resolvedModel`) counts the first turn only and only fills what no stop wrote; a background agent's result is `status: async_launched` and carries none of them. `model` is the result's `resolvedModel`, else the last model the transcript names. Claude Code writes `stop_reason: null` on many finished background-agent transcripts, so for those the stop's wait for a final entry runs its full 3 s before the sums are read |
| Chat name | pfm's transcript index, opened read-only by `session_id` at report time (`store.OpenTranscriptNames`); never `fleet.db`, whose chat label nothing writes |

## The store

`$HOME/.local/state/pfm/callmeter.db`, its own file: every call of every chat writes to it, and it must never contend with `fleet.db`. WAL mode, `busy_timeout` 5 s against the concurrent async writers, one short transaction per event. `PRAGMA user_version` carries the schema version (2 for this design); a store written by a newer schema is refused with an error naming both the store's version and the binary's, never opened and misread.

Version 2 holds hook rows only. A store opened at version 1 is purged once, in the same transaction that sets version 2: it first sets to NULL a kept call's `request_id` that names a request about to be deleted, the only column it nulls — so a hook call the replay had filled shows no context tokens from the replay, while a `request_id` that named no row stays as it was, and a kept call's or request's `agent_id` stays as the hook wrote it, even when the `agents` row it names is deleted: the hook saw the sub-agent's calls but never its `SubagentStart`; a kept agent's `parent_tool_use_id` stays as the hook wrote it too, even when the call it names is deleted: the hook took it from the `Agent` or `Task` call's result — then deletes every `calls`, `requests` and `agents` row whose `source` is not `hook` — a transcript replay's `transcript`, or no `source` at all, as the replay left the agents its task notices named — with no exception, every fault of the stage retired with them, and the command parts and parse faults whose call no longer exists. A fresh store gets version 2 with nothing to purge, and a store already at version 2 is not touched, so the purge removes rows once per store: an open that read version 1 before a concurrent open committed version 2 runs it again over the store that open already purged, and it deletes or changes no hook row. A failed purge statement rolls the whole transaction back and fails the open with an error naming the store and the statement; the store stays at version 1.

Every write is an upsert by its row's key, setting only the columns its event owns and leaving the rest untouched, because async hooks arrive in any order — a `PostToolBatch` can land before its `PostToolUse`. `ts` is an integer Unix timestamp in milliseconds, UTC, in every table that carries one.

| Table | One row per | Key | Columns |
| --- | --- | --- | --- |
| `calls` | tool call | `tool_use_id` | `session_id`, `agent_id`, `agent_type`, `request_id`, `ts`, `tool`, `input` (see below), `cwd`, `duration_ms`, `failed`, `error`, `bytes_real`, `bytes_delivered`, `persisted_path`, `file_path`, `file_bytes`, `file_bytes_before`, `read_start`, `read_lines`, `read_total_lines`, `source`, `config_dir` |
| `requests` | model request | `request_id` | `session_id`, `agent_id`, `ts`, `context_tokens`, `output_tokens`, `calls`, `pending`, `source`, `config_dir` |
| `agents` | sub-agent | `agent_id` | `session_id`, `agent_type`, `parent_tool_use_id`, `started`, `stopped`, `transcript_path`, `total_tokens`, `tool_uses`, `model`, `source`, `config_dir` |
| `command_parts` | simple command inside one Bash call | `tool_use_id`, `seq` | `lang` (`sh`, `python`, …), `program`, `args`, `files` (a JSON list, one `{action}\t{range}\t{exists 0\|1}\t{path}` per file), `conditional`, `parse_status`, `parser` (the `cmdparse.Version` that produced the part) |
| `faults` | failure to record or parse | — | `ts`, `session_id`, `tool_use_id`, `stage`, `error` |

- `source` is `hook`: every row comes from a hook event, and no reader branches on it. `config_dir` is the config dir whose `projects/` physically holds the row's transcript (`callmeter.ProjectsHome`: the `projects/` directory resolved through symlinks, then its parent). Accounts that share one `projects/` — on this machine `~/.claude2/projects` and `~/.claude3/projects` are symlinks to `~/.claude/projects` — hold one file per chat, so they are one config dir: a chat keeps one history and one set of metrics whichever account runs it, the hook names the same dir for each of them, and `--config-dir` with any of those accounts shows the same history. Accounts with separate `projects/` stay separate.
- `calls.error` is the failure text of a failed call, cut to 500 characters.
- The request key: `requests.request_id` is the model message id once the transcript shows it. Until then it is the provisional key `pending:{first tool_use_id of the batch}`, with `pending = 1`. When the id is later read from the transcript, the fill rewrites that request row to its real `request_id` and rewrites every `calls.request_id` that still carried the provisional key, in one transaction. `context_tokens` is `input_tokens + cache_read_input_tokens + cache_creation_input_tokens` from that message's `usage`.
- A sub-agent's parent comes from the `Agent` or `Task` call whose result carries `agentId`: that agent is upserted into `agents` with the calling `tool_use_id` as `parent_tool_use_id`, together with the result's `totalTokens`, `totalToolUseCount` and `resolvedModel`.
- `faults.stage` is one of `payload`, `store`, `transcript`, `parse`, naming where the failure happened. A fault that cannot itself be written to the store (the store is unreachable) goes to pfm's log only.
- What is stored of an input: the sanitized input JSON keeps a Bash `command` and `description`, file paths, search patterns and the `Read` range. `content`, `old_string`, `new_string`, an `Edit`'s `edits`, and an agent's `prompt` are never stored; each is replaced by its byte length under the same key with a `_bytes` suffix (for example `content_bytes` in place of `content`). The transcript already holds the full values, and this store must hold nothing a project would not want copied.
- Retention: rows older than 30 days are pruned before every `pfm callmeter report` run; the hook itself never prunes, so a write always succeeds even mid-prune-cycle.

## Parsing a command

A Bash call is often several commands (`F=x; wc -l "$F" && grep -n func "$F"`) and often carries another language inside it (`python3 -c "…"`, `python3 - <<'EOF' … EOF`, `node -e "…"`). The parse happens at report time only (`report.EnsureParsed`), never in the hook, and its result is cached in `command_parts`, keyed by the call. No parser is written here; each language uses its established one:

| Language | Parser | Yields |
| --- | --- | --- |
| Shell | `mvdan.cc/sh/v3`, the parser behind `shfmt`, pinned at `v3.12.0`: the highest release whose `go` directive does not exceed pfm's own (v3.13 needs go 1.25; pfm's `go` directive is never raised to accommodate it) | every simple command with its arguments through pipes, `&&`, `;`, loops, `$(…)`, assignments, redirections and heredoc bodies |
| Python | Python's own `ast` module, one `python3 -c {embedded script}` process per batch of snippets | calls and string constants: `open(…)`, `Path(…)`, `subprocess` arguments |
| JavaScript (`node -e`) | not parsed in the first version: a named gap, counted in every report that counts commands and in `faults` | — |

The Python batch protocol is one process per batch: stdin is a JSON list of `{id, code}`, one entry per snippet found in that batch's Bash calls; stdout is a JSON list of `{id, calls, strings, error}` in the same order. A Python snippet is found in `python3 -c ARG`, in a `python3 - <<EOF … EOF` or `python3 <<EOF … EOF` heredoc body (the same forms for the bare `python` alias), and `python3 script.py` attributes the file `script.py` with the action `exec` rather than parsing it.

`command_parts.parse_status` is one of:

| Value | Meaning |
| --- | --- |
| `ok` | parsed cleanly |
| `error` | the parser rejected the snippet; the message is kept in `faults` |
| `unparsed` | a language not parsed (`node -e` and others) |
| `python-unavailable` | no `python3` on `PATH`: every Python snippet in the run is marked this way, and `faults` reports the count so the gap is never a silent zero |

From the parsed commands, a file is attributed to a call when an argument, an assigned value or a string constant resolves to a file from the directory the command started in, braces and globs expanded as the shell expands them (§ Corpus findings and fixes names when a missing file counts). What a command did with the file (whole read, line range, search, write) comes from a table of the common readers (`cat`, `head`, `tail`, `sed -n`, `grep`, `rg`, `wc`, `open`), each row naming which argument is the range. A command the table does not know attributes its files with the action `unknown`.

One Bash call returns one output: the bytes of a compound command belong to the call, never split across its parts.

A static parse cannot know which branch ran, so every file a part names is attributed as read, whether or not its branch ran; the call's bytes are measured at its end either way. What the parse does know is where the part sits: `command_parts.conditional` is 1 for a part inside an `if` branch or the `else` chain (an `elif` condition included), a `case` arm, or on the right-hand side of `&&` or `||`, and 0 otherwise; the first `if` condition and the `case` word run unconditionally. A Bash call's read of a file is certain when any unconditional part of that call names the file, conditional otherwise. A file test (`[`, `test`, `[[ ]]`) reads nothing and attributes no file. The `files` report shows `READS` (every attributed read), then `CERTAIN` and `CONDITIONAL`, so a file that ranks high only through guarded reads (`if [ -f x ]; then cat x; fi`) shows as such.

## Reports

`pfm callmeter report {topic} [--since D] [--project P] [--agent-type T] [--session S] [--config-dir DIR] [--limit N]`, each topic a fixed query over the store. Every report prunes rows older than 30 days before it runs; the hook never prunes.

Flags:

| Flag | Meaning |
| --- | --- |
| `--since D` | a duration (`7d`, `24h`) or a date (`2026-09-01`); default is the whole 30-day retention window |
| `--project P` | calls whose `cwd` is `P` or under it |
| `--agent-type T` | calls made by that agent type |
| `--session S` | calls in that session |
| `--config-dir DIR` | narrows to one Claude config dir; default covers every config dir the machine config names |
| `--limit N` | rows per table, default 25 |

Output is a plain fixed-width table: one header line naming the topic, the active filters and the window, then the rows, then a note line for every named gap the query hit — calls not recorded (from `faults`), requests still pending, snippets unparsed per reason, chat names that could not be read. An empty window prints `callmeter: no calls recorded in window`; a store that cannot be opened prints `callmeter: cannot open store {path}: {error}` and exits 1 — absence and failure never share a line.

| Topic | Answers |
| --- | --- |
| `files` | files by bytes delivered, read count (certain and conditional as two columns), distinct agents, size on disk, whole against ranged reads, re-reads by one agent; `Read` bytes and Bash-attributed bytes are two separate columns, never summed, because a Bash call's bytes are shared evenly across the files it credits |
| `writes` | files by write count and total growth |
| `commands` | command shapes by bytes delivered (sum, p50, p95), count in the band 20,000–30,000 chars, persisted count, follow-up reads of `tool-results/`. A call's shape is the shapes of its non-trivial parts in order (`cd`, `export`, `set` and bare assignments dropped, and `echo`, `printf`, `true` and `:` when their arguments are literal and they redirect to no file), deduplicated, joined with ` ; `, at most three parts. One part's shape is the program's base name plus its first argument, when that argument is neither a flag nor a path (`go test`, `git diff`, `dev.sh test`). A stream filter (`head`, `tail`, `grep`, `sed`, `awk`, `sort`, `uniq`, `wc`, `cut`, `tr`, `tee`, `cat`, `jq`, `less`, `more`, `column`, `nl`) naming no file is dropped when the call has another non-trivial part, so `cd x && go test ./... \| tail` is `go test` (`report/commands.go`, `callShape`) |
| `context` | per agent: requests, start and peak context, mean growth per request, and the three requests with the largest growth, named by their calls |
| `sequences` | call runs of length 2 to 5 that recur across agents, each step normalized to its shape (tool, program, the file's role), ranked by occurrences × bytes delivered. Steps are taken per agent (`session_id` + `agent_id`) in `ts` order; a step's shape is the tool, for Bash the first non-trivial program, and the file's role — `same` when the step repeats a file an earlier step in the same run touched, `new` otherwise, `-` when the step names no file. A run must recur across at least two distinct agents to be listed |
| `faults` | calls not recorded, snippets not parsed |

Chat names come from pfm's transcript index, read-only and never migrated, by `session_id`. A missing index leaves names blank; an unreadable one leaves the name column `?` and prints one note line with the error, rather than failing the report.

The command surface (`pfm/internal/callmeter/command/`, a thin dispatch in `pfm/cmd/pfm/main.go`): with no store it prints `callmeter: no store at {path}: nothing recorded yet`, exits 0 and creates no file; a store that cannot be opened exits 1; a bad topic, action or flag, or a `--config-dir` the machine config does not name, exits 2 with usage naming the configured dirs; a `--since` older than 30 days is clamped with one stderr note; `--project` is made absolute, symlinks unresolved.

## Corpus findings and fixes

A one-time study of 30 days of real transcripts (62,005 calls, 4,068 transcripts, three accounts) parsed all 37,355 Bash calls, and exposed where attribution was wrong. Each rule below answers one measured defect:

- **Byte share.** A Bash call's bytes are split evenly across the files it attributes: each file is credited `call bytes ÷ files attributed`, so per-file sums add up to the call. One `grep -l` over a 1,445-file glob had credited its whole output to every file.
- **Wrappers.** `command`, `builtin`, `exec`, `env` (its assignments and flags), `timeout` (its duration), `nice`, `nohup`, `time`, `/usr/bin/time`, `sudo` and `xargs` are unwrapped to the program they run, with that program's arguments. A program given as a path is matched by its base name (`/bin/ls` is `ls`); a program path that is a file under the directory its part runs in (after any `cd`) is also attributed with the action `exec`.
- **Inner shells.** The `-c` string of `bash`, `sh` and `zsh` is parsed as shell with the same directory and conditional depth. `ssh`, `docker exec`, `kubectl exec` and any other remote runner are never parsed: their paths live on another machine. A project's own runner that takes a command string (`dev.sh iso run "…"`) is not parsed: a named gap.
- **`cd` within a call.** A literal `cd DIR` moves the directory later parts of the same call resolve against; a `cd` inside `( … )` ends with the subshell; a `cd` to a non-literal target stops relative attribution for the rest of that list.
- **Redirects.** `> f` and `>> f` attribute a `write`, `< f` a `read-whole`; heredoc bodies, `/dev/*` and fd duplications (`2>&1`) attribute nothing.
- **Metadata is not a read.** `ls`, `du`, `stat`, `file`, `find`, `realpath` and `readlink` attribute the action `stat`, which no read count includes; `[`, `test` and `[[ ]]` attribute nothing.
- **Missing files.** A path that a known reader or writer names (a file operand of `cat`, `head`, `tail`, `sed`, `grep`, `rg`, `wc`, a redirect target, a Python `open()`) is attributed even when it does not exist at parse time, with `exists = 0`: history keeps its deleted and scratch files. An unknown program's arguments are attributed only when they exist, so a word that merely looks like a path is never a file. Text that still carries an expansion nobody performed — a `$` or a backquote the shell left inside a Python string, a glob character, braces (`{}` of `xargs -I{}` and `find -exec`, a quoted `{a,b}`), a leading `~` — names a file only when that exact file exists, never as a missing one: 1,315 missing-file rows of the second pass were such text.
- **Tilde and braces.** A word's leading unquoted `~` or `~/` is the home of the user the commands ran as (the report's home); `~user`, `~+` and `~-` attribute nothing. A Python string starting `~/` is expanded the same way, since it is `expanduser`'s input. An argument's `{a,b}` is brace-expanded as bash does before any glob (mvdan's `syntax.SplitBraces` and `expand.Braces`), so `wc -l internal/{a,b}/x.go` names two files; a sequence (`{1..9}`) or a product over 64 words stays one literal word. Before this, 756 rows were a `~` joined to the call's directory as a literal folder.
- **The directory a command starts in.** `PostToolUse`'s `cwd` is where the command left the shell, and the parser replays the command's own `cd` from the stored directory, so the hook stores `PreToolUse`'s (§ The hooks). The transcript's `cwd` is the starting directory in 16 of the 23 calls of the corpus that open with a relative `cd`; the other 7 are calls sent in parallel.
- **A parser fix reaches stored calls.** Every part records the `cmdparse.Version` that produced it, and a report parses again every call whose parts carry an older one, replacing its parts and its parse faults; without it a fix would leave 30 days of stale attributions.
- **Shapes.** `echo`, `printf`, `true` and `:` with literal-only arguments are trivial in a command shape, like `cd` and `export`, so a separator `echo ---` does not split `sed ; grep` into a new shape.

### Test corpora

Unit tests alone passed while this corpus failed, so two tests made of real data now guard the parser and the hook:

1. **Real commands.** A table-driven test of Bash commands taken from the one-time study of real transcripts (nested quoting, heredocs, `cd` chains, wrappers, globs, redirects, inner `bash -c`, Python heredocs, loops, arrays), each with its expected parts, attributions, actions and `conditional` flags. Paths are rewritten to `/tmp/demo-proj/…`; the case keeps the command's structure verbatim. The corpus lives in `pfm/internal/callmeter/cmdparse/testdata/commands-corpus.json`; a case whose parse differs from the spec carries `known_defect` and is skipped by name until it matches.
2. **Captured hook payloads.** The payloads captured from headless sessions (§ Evidence), in `pfm/internal/hookentry/testdata/callmeter/`, are fed through the hook into a real store; the test then runs `report.EnsureParsed` and `report.Files` over what the hook recorded.

Fixtures come only from this repository's own sessions, never from a project holding clinical or client data, and every fixture file passes `scripts/leak-check.sh` before it is committed.

## Build decisions

Decided while building, one line each, file named:

- Store (`store.go`, `write.go`): prune keeps rows with no timestamp and ages agents by `COALESCE(stopped, started)`; `ResolveRequest` merges a provisional row into the message row (stored values win, NULLs filled, `calls` summed, the earlier `ts` kept); the 500-character error cut counts characters, not bytes; an open writes nothing when the schema version is current, creates the schema under `BEGIN IMMEDIATE`, and retries a busy open until `BusyTimeout` (the first async hooks race to create the file, and SQLite's switch to WAL takes no busy wait).
- Sanitized input (`sanitize.go`): `prompt` is dropped for every tool; any other field over 4096 bytes becomes `{name}_bytes`; input that is not a JSON object is an error.
- The hook (`hookentry/callmeter.go`): `bytes_real` is the first of `persistedOutputSize`, stdout, `file.content`, `content`, the response JSON's length; a batch spanning several messages writes one request per message; `Stop` and `SubagentStop` read the transcript only when a request is pending; a payload with no `session_id`, and a stat failure other than not-exist, are `payload` faults; the store's home resolves through `paths.HomeFrom`, so `PFM_HOME` jails it; `SubagentStop` fills an agent's still-empty `total_tokens`, `tool_uses` and `model` from its transcript (a message id's usage counted once, distinct `tool_use` blocks), never over the `Agent` result's values; a store open that fails names a batch's call ids.
- Delivered bytes (`callmeter.DeliveredBytes`): only `text` blocks count; a shape it cannot measure is an error, never a guess.
- File columns (`FileColumnsFromInput`, `FileColumnsFromResult`), used by the hook: `Read`, `Write`, `Edit`, `MultiEdit`, `NotebookEdit`; a relative path joins `cwd`; the Read input's `offset`/`limit` are overridden field by field by the result's `startLine`/`numLines`/`totalLines`; `type: create` with a null `originalFile` is 0; a file tool with no path is a fault.
- Parser (`cmdparse`): a bare assignment makes no part and its literal value is substituted into later uses; ranges are `head` → `1,N`, `tail` → `-N` or `K,$`, `sed` → `A,B`; `sed -i` is a write; a Python snippet's id is `{callID}#{seq}`; `node file.js` is a plain shell part, only `-e`/`--eval`/`-p`/`--print` are unparsed; a file operand of `cat`, `head`, `tail`, `sed`, `grep`/`egrep`/`fgrep`/`rg`, `wc`, a redirect target and a Python `open()`/`Path(…)` read or write is attributed when missing, `Exists = false`, unless its text is a placeholder (`placeholder`: `$`, a backquote, a glob character, `{…}`, a leading `~`); `-`, a directory, a glob that matched nothing and a reader's operands under `xargs` never are; a leading unquoted `~`/`~/` is `Call.Home` (the report passes `runtime.Paths.Home`), and no home, `~user`, `~+` or `~-` leave the word unknown; an argument word is brace-expanded first (`braces`, over `syntax.SplitBraces` and `expand.Braces`, which `syntax.Walk` cannot visit, hence `braceBound`), a sequence or a product over 64 kept literal; a wrapper is unwrapped in `wrappers.go` and the part records the program it runs with that program's arguments; `command -v`/`-V` and a wrapper naming no program stay the wrapper's part and attribute no file; a program path resolves against the directory its part runs in after any `cd` and is `exec` only when it is a regular file under that directory; `find` stats only its starting points, never a word of its expression; a literal `bash`/`sh`/`zsh` `-c` string (flag clusters `-lc`, `-ec` included) keeps the shell part (its program file and redirections) and appends the string's parts after it at the same conditional depth, starting in the current directory, in a child scope whose `cd` and variables end with it; a non-literal string is an ordinary part, one that fails to parse is one error part; `cd` moves the directory statically, even in a branch, and `( … )`, `$(…)` and each side of a pipe restore it; after a `cd` the parse cannot know, relative paths attribute nothing until a literal absolute `cd`; a Python part resolves its strings in the directory it ran in; `NAME=(…)` with literal elements expands through `${NAME[@]}`, `${NAME[*]}` (every element), `${NAME[N]}` and `$NAME` (the first); a `for` loop's items and an array's elements expand as any unquoted word does, and a value built from a glob that matched nothing is never a missing file; an unquoted `\X` is `X` and a backslash-newline is removed, so an escaped glob character stays literal; a backslash inside double quotes escapes only `$`, a backquote, `"`, `\` and a newline; `ssh`, `mosh` and the `exec`/`run` verbs of `docker`, `podman`, `kubectl` and `oc` attribute no argument; `cmdparse.Version` is raised with every change to what a command parses to, and `report.EnsureParsed` parses again every call whose parts carry another version (`Tx.ReplaceCommandParts` replaces the parts and the call's parse faults together); word evaluation lives in `words.go`, the walk in `cmdparse.go`.
- Reports (`report/*.go`): a Bash call's bytes are shared evenly across the distinct files it credits for read, search and unknown actions, a missing file included, bytes ÷ N each and the remainder one byte each to the first files in part order, so the shares sum to the call's bytes exactly (`bashShares`); `echo`, `printf`, `true` and `:` are trivial in a shape only when the part credits no file and no argument's stored text holds `$`, a backquote, `<(` or `>(` (one rule, `trivialPart`, shared by `commands` and `sequences`); `files` shows SIZE `gone` when the latest call naming a file found it missing, `-` when no size was ever seen; a Read is ranged when `read_start > 1` or `read_lines < read_total_lines`; the 20,000–30,000 band is measured on `bytes_real`; p50 and p95 are nearest-rank; `context` lists growth only and names each jump by the previous request's calls; `sequences` counts overlapping windows, drops a shorter run occurring exactly as often as a longer run containing it, and ranks by occurrences × bytes.
- Found live, first minutes after install (HOUSING and this repo's chats): a failed Bash call's size is its error text, the hook's `PostToolUseFailure` `error` (the transcript's `toolUseResult` is the same text behind `Error: `); Claude Code stops its own internal agents with a `SubagentStop` that has no `agent_type`, no `SubagentStart` and no transcript on disk, and the hook records nothing for those (`untypedAgentMissingTranscript`; 16 false transcript faults before), while a typed agent missing its transcript stays a fault; the same exemption covers `PostToolBatch` for one of these agents, which used to fail `FindRequests` against the missing file, write a pending request, then fail `resolvePending` against the same file for a second fault; a call still running has only its `PreToolUse` row until its result lands; a headless `claude -p` cancels running hooks at exit ("Hook cancelled"), so its last calls can lose their `PostToolUse` and keep only their `PreToolUse` row. Running chats pick up the new hooks without a restart.
- Pending requests: every `PostToolBatch` also retries the pending requests of its own chat or sub-agent (`resolvePending`), so a request that missed the disk at its batch resolves at the next one, not at a `Stop` an hour away (live: 11 pending on running sub-agents, every id already on disk).
- Agent totals (`settledAgentTotals`, `recordAgent`): 31 of 33 live agents were summed without their final message, because `SubagentStop` fires before that line is flushed; a waiting orchestrator stored 10 of its 86 tool uses and a `started` after its `stopped`, because each turn restarts and re-stops it. The stop now waits for the final message, the latest stop's sums overwrite, and `started` keeps the first start.
- The hook is only as present as the installed binary: `pfm install` run from a build without callmeter (a worktree of `develop` before this work lands) leaves `pfm internal callmeter` unknown and drops its registrations, so every chat goes unrecorded until the next install from this tree (live: two such installs from another chat, 18 and 7.5 minutes of calls lost to the hook, never recorded).
- Request lookup (`callmeter.FindRequests`): reads the transcript's last MB first and widens fourfold only while a wanted id is missing, and decodes a line only when it names a wanted id; a malformed line naming one is an error at its byte offset, a malformed line naming none is never read. Measured on a 40 MB transcript: `PostToolBatch` 111 ms → 10 ms at the median. Every event's hook runs about 12 ms (process start, store open, one write) on a 63,000-call store, all of it async.
- The start directory (`hookentry/callmeter.go`, `recordStartCwd`): `PreToolUse` upserts `cwd` with `Overwrite` and `ts`/`tool` with `FillEmpty`, so a call that never finishes still ages out; `PostToolUse` upserts every other column with `Overwrite` and `cwd` alone with `FillEmpty`; a `PreToolUse` payload without `tool_use_id` or `cwd` is a `payload` fault.

## What it does not do

- It changes nothing a model sees: no hook output, no added context, no blocked call.
- No Codex or OpenCode recording in the first version.
- No calls the harness refused before running (an unavailable tool): they leave no hook event, so they are not recorded.

## Evidence

A capture hook, loaded only through `--settings` into headless sessions in a scratch directory, logged every raw payload:

- A scripted sonnet run read one file eight ways (Read whole and ranged, `cat`, `head | tail`, `sed -n`, `python3 -c "open(…)"`, a compound with a variable, a loop), then wrote, edited, appended through Bash, printed a large output, failed a command and spawned a haiku sub-agent. Every call that ran produced `PostToolUse` or `PostToolUseFailure`; the sub-agent's calls carried its `agent_id`.
- `seq` at 5,000 / 6,000 / 6,500 / 7,000 / 9,000 lines: delivered whole at 23,892 and 28,892 chars, as a 2,775-byte preview at 31,393 and above.
- `PostToolBatch`: three parallel calls came as one batch of three, a single call as a batch of one; its `tool_response` for `seq 1 7000` was the `<persisted-output>` preview.
- `updatedToolOutput`: with the since-removed template test filter wired on `PostToolUse`, a fake `pytest` printing 302 lines reached the model as 200 lines, and `PostToolBatch` carried those 200.
- Transcript timing, 11 calls across a chat and a sub-agent: the call's request was absent at `PreToolUse` in 11 of 11, present at `PostToolUse` in 11 of 11 in one run and 0 of 5 in another, and present 0.5 s later in 11 of 11.
- A natural opus run under the fleet prompt in bypass mode, where the harness withholds the Grep and Glob tools, searched with `grep -rn` through Bash in 4 of its 6 calls and read with ranged `Read` in 2.

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The hook entry | `pfm/internal/hookentry/` | `pfm internal callmeter` |
| The registration | the installer's hook table, per [hooks.md](hooks.md) | events, matcher, `async` |
| The doctor check | per [hooks.md](hooks.md) | the seven registrations present in every Claude config dir |
| The store and reports | `pfm/internal/callmeter/` and its `cmdparse/`, `report/`, `command/` packages; `pfm/cmd/pfm/main.go` | schema, parser, the CLI `report` |
| The testing law | `.claude/commands/pfm-testing-manual.md` | the store is real SQLite in tests; payload fixtures are the captured ones |
| The surface reference | `docs/dev/pfm-surface.md` | the `callmeter` and `internal callmeter` command rows |
| The lane map | `docs/dev/testing/landscape.md`, `infra/fence/lanes/` | the landscape row for callmeter, its beat, and its map row |
| The test timing budgets | `pfm/.testtiming.yml` (format in `docs/dev/testing/timing.md`) | each callmeter package's timing budget |

## Open items

- Retention: **Decided.** 30 days. Every `pfm callmeter report` prunes rows older than 30 days before it runs; the hook never prunes.
- Whether reports read the other accounts' config dirs by default or only the current one: **Decided.** Reports cover every Claude config dir the machine config names by default; `--config-dir DIR` narrows to one.
- `node -e` parsing: **Decided.** It stays unparsed in this version: a named gap, counted in every report that counts commands and in `faults`.
- `BASH_MAX_OUTPUT_LENGTH`: **Decided.** Unchanged.
