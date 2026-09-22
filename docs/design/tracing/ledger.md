# ledger

The ledger is the design `tracer` and `mapper` share: a script computes everything that can be computed, cheap readers copy code lines into fact rows, the script checks every row against the files, and a lead composes its answer from verified rows only. This file holds every decision the two agents have in common; [tracer.md](tracer.md) and [mapper.md](mapper.md) hold what differs.

A change lands in these files first, then in the templates, then in every surface listed under [Surfaces that stay in sync](#surfaces-that-stay-in-sync).

## Contents

- [The family](#the-family)
- [Why a ledger](#why-a-ledger)
- [The script](#the-script)
- [Buckets](#buckets)
- [Rows](#rows)
- [What verify checks](#what-verify-checks)
- [Widening](#widening)
- [Absence](#absence)
- [The lint](#the-lint)
- [Mechanisms that replaced rules](#mechanisms-that-replaced-rules)
- [Not part of the design](#not-part-of-the-design)
- [What the evidence says](#what-the-evidence-says)
- [Measuring a run](#measuring-a-run)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)
- [Open items](#open-items)

## The family

| Member | Kind | Does | Runs at |
| --- | --- | --- | --- |
| `tracer` | agent | The lead for one question: surveys, reads or spawns readers, verifies, widens, answers | spec-execution (`sonnet`), effort `medium` |
| `mapper` | agent | The lead for one target's whole area: the same loop, every bucket read, empty facets probed | `sonnet`, effort `medium` |
| `scribe` | agent | The reader: one bucket file in, one rows file out; tools `Read`, `Write` and nothing else | collector (`haiku`), effort `high` |
| `ledger` | skill | `ledger.py`, Python 3 standard library, the computed half; never invoked by a user | — |

Vocabulary, one term per concept: the target is the entity asked about; a spelling is one case variant of its name; a bucket is a set of files one reader takes; a row is one quoted fact; an out name is a name under which the target leaves a file; an absence row `[A#]` is a computed zero; a facet is one of DEFINITION, WRITERS, READERS, TIMING, CONTROL, NEIGHBOURS, MENTIONS.

## Why a ledger

An agent that reads code and then writes prose invents where nothing checks it. In judged runs the invention moved to whichever field was unchecked: file addresses, then row notes, then the symbol column, then counts, then the lead's own summary sentences. Instructions forbidding each class did not stop it; a mechanical check on the field did. The design therefore closes fields one at a time in the script and lets no claim reach the reader without a row id.

## The script

`python3 ~/.claude/skills/ledger/ledger.py {command}`. A failure prints `LEDGER FAILED — {reason}` to stderr and exits 2. State lives in a directory under the system temp dir, `ledger/{target-slug}-{HHMMSS}/`: `state.json`, `bucket-N.txt`, `rows-N.txt`, `ledger.txt`, `report.md`.

| Command | Does |
| --- | --- |
| `survey ROOT TARGET [SPELLING…]` | Lists files with `git ls-files -co --exclude-standard` (nested repos walked), derives snake, kebab, SCREAMING, camel and Pascal spellings, prints per-spelling file counts, classes every file CODE, TEST, DOC, GENERATED or DATA, cuts CODE files into buckets and writes each bucket file |
| `wait DIR N…` | Blocks until every named bucket has a rows file, at most 540 s; names a bucket whose reader never wrote |
| `verify DIR` | Checks every row against its file, numbers the survivors `R1…`, writes `ledger.txt`, prints verified, replaced, withheld and rejected counts, buckets with no rows file, files with no verified row |
| `widen DIR [NAME…]` | Word-greps every out name across the repo; new CODE files become new buckets; a name with no hit outside its own file becomes an `[A#]` row listing its in-file uses; more than 25 new files is reported TOO COMMON; prints `the frontier is closed` when nothing is new |
| `absent DIR NAME…` | Word-match file count per name beside a control spelling that must be found, with a per-directory and per-class breakdown; each zero gets an `[A#]` row |
| `show DIR [--by facet\|file]` | Prints verified rows (long quote lists trimmed), served names no consumer row picks up, absence rows, unread files per class |
| `lint DIR REPORT` | Flags the report, lists uncited rows, prints the `COUNTS` line the lead copies |

DATA files — a data extension over 200 KB, or any file under `data/`, `seed/`, `snapshots/`, `dumps/`, `exports/` or `backups/` — are listed and never opened.

## Buckets

A bucket never crosses a top-level directory, holds at most 8 files and 45 hit lines, and tiny buckets merge. The bucket file is the reader's whole input: ROOT, TARGET, the NAMES that led here, the ROWS FILE path, and each file — with the full path to read it at — as numbered excerpts of 28 lines around each hit, hits marked `>>`, capped at 320 lines a file. Hits the cap hides are listed for the reader to open itself.

## Rows

```text
@ path/from/root.ext:LINE | KIND | symbol | out: name1, name2
> the line at that address, copied whole
> another line of the same block
# what it does to the target, in under 25 words
```

Kinds, by facet:

| Facet | Kinds |
| --- | --- |
| DEFINITION | DEFINES, MIGRATES, RELATES |
| WRITERS | WRITES, DELETES, TRIGGERS |
| READERS | READS, CALLS, SERVES, CONSUMES, RENDERS, PROMPTS, EGRESS, STORES |
| TIMING | SCHEDULES, EXPIRES |
| CONTROL | GUARDS, VALIDATES, AUDITS, CONFIGURES, REGISTERS |
| NEIGHBOURS | RELATES |
| MENTIONS | CITES-ABSENT, MENTIONS |

RENDERS, PROMPTS, EGRESS and STORES are terminals. SERVES is a hop: a served field, route or event is a terminal only when a CONSUMES or RENDERS row picks its name up.

## What verify checks

| Field | Check | On failure |
| --- | --- | --- |
| Quote | A line of the named file under whitespace-normalised matching; a quote that glues consecutive lines passes when every character is in the file, in order | Row rejected |
| Address | Nearest line carrying the quote | Re-addressed |
| Extra quotes | Within 60 lines of the address, matched in sequence | Extra dropped, row kept |
| Header | `path:LINE`; a range takes its first number; kind aliases (`UPDATES`→`WRITES`, `TRUNCATES`→`DELETES`…) | Unknown kind filed as MENTIONS with the reader's word kept in the note |
| Symbol | Exists in the file | Replaced by the computed enclosing definition |
| Out names | Exist in the file; the innermost enclosing function is added | Dropped |
| Note identifiers | Every `snake_case` or `camelCase` word shows within 40 lines of the quote | Note withheld, row kept |
| Note numbers | Every number of two or more digits shows in the quoted lines | Number replaced by `(?)` |
| Block size | For DEFINES, REGISTERS, CONFIGURES, VALIDATES rows whose line opens a bracket: items counted by bracket depth and `,` / `;` separators | Printed as `= COUNTED BY THE SCRIPT`; omitted when items are not separator-delimited |

Rows are deduplicated per file, line and facet. A re-run renumbers rows, so a report is linted against the ledger it was written from.

## Widening

A search by the target's name cannot see a consumer that knows it under a later name — a repository method, a wire field, a component. Each round the script greps every out name and turns the files that name it into buckets, so one round carries the trace one hop outward. Only the innermost enclosing function feeds widening: enclosing class names pulled every file of a dependency-injection container into one run. A name consumed only inside its own file gets an absence row listing those uses, which is a hop to follow by hand (`widen DIR {consumer}`), not proof that nothing uses it.

## Absence

An `[A#]` proves one thing: no other file names that word. It does not prove the thing is unused — a registry can be consumed inside its own file, a table reached through introspection. The control spelling beside each count distinguishes "searched and found nothing" from "the search did not run".

## The lint

| Flag | Fires on |
| --- | --- |
| Bad id | `[R#]` / `[A#]` no ledger row carries |
| Unbacked address | A `file:line` with no verified row within 2 lines |
| Universal | sole, solely, exclusively, never, none, nothing, no other, nowhere, only — in a line citing no `[A#]` |
| Unbacked count | A count of entries, operations, rows, roles and the like that no quoted line and no script count of the sentence's cited rows shows |
| Served as terminal | A line saying terminal, rendered, displayed or "to the client" whose cited SERVES row no consumer row picks up |

The lint echoes each flagged line, lists UNCITED rows, and prints `COUNTS (copy these): N verified rows, M rejected at the last verify, K code files unread`. The lead fixes flagged lines in one editing call, lints once more, and publishes; a flag still standing is copied into NOT READ.

## Mechanisms that replaced rules

| Rule that failed | Mechanism |
| --- | --- |
| "Stay inside your bucket" — readers grepped the repo, 20–40 calls each | `scribe` holds `Read` and `Write` only |
| "Write all rows in one call" — one heredoc per row | One `Write` of the rows file; a refused overwrite means another reader was first, and the name takes `-2` |
| "Wait by ending your message" — a third of a lead's calls were `true` | `ledger wait`, one blocking call a round |
| "Recount before reporting" — the lead reported the flagged number | The number is removed and the script's own count printed |
| "Report written once" | The lint's one-pass cap |

## Not part of the design

- A prose auditor: a second agent checking each summary sentence against its cited quotes. One judged mapper run with it scored 22.0/33 with 5 inventions against 27.5 and 2 without; it added 1–3 USD a run and, uncapped, looped four times. Sentences came out hedged, not truer.
- Respawning a bucket because some of its files got no row: nine respawns in one run. A bucket is respawned only when it has no rows file.
- Following enclosing class names, or every computed name, when widening.
- A line-count fallback for block sizes: it reported 23 for a block of 9 fields. No count beats a wrong count.
- A graph index, embeddings or a skeleton tier: published comparisons put grep navigation level with graph baselines, and no run here was limited by search.

## What the evidence says

Judged by an independent `opus` agent against fixed keys on one private multi-project repository (backend, frontend, contracts, Python pipeline): HIT 1, PARTIAL 0.5; an invention is any stated fact the code contradicts or does not show, absence claims and tallies included; a fact only an uncited row holds scores PARTIAL at most. Costs are at assumed list rates, not billed. Run-to-run variance on one prompt is about 5 points.

- Mechanical checks end the class they target: no invented address after quote checking, no invented symbol after the symbol check, no wrong registry size once the script counted it.
- What remains is synthesis prose: closed tallies ("three ways"), narrowed lists ("restricted to X" where the constant holds three roles), links between two rows that no row shows.
- About a fifth of lost points sit in files a run opened, a few lines outside its excerpts or quotes.
- Reader cost fell from 0.20–0.26 USD to 0.03–0.11 USD a bucket when search tools were removed, with no loss of judged accuracy.
- The lead is the largest cost line: the harness wakes it once per returning reader, and every call re-sends its context.

Per-agent scoreboards: [tracer.md](tracer.md#measured), [mapper.md](mapper.md#measured).

## Measuring a run

Read the usage before the findings: `tool_uses: 0` beside any claim is a fabricated return. Attribute cost per agent type and model from the sub-agent transcripts; a lead that names no `subagent_type` silently gets a general-purpose agent at the main model's rate. When benchmarking a prompt revision, write it under a fresh agent name, launch it in a turn started after the write, and delete the copies only after every run naming them has returned.

## Surfaces that stay in sync

- `templates/global/skills/ledger/{ledger.py,SKILL.md}`
- `templates/global/agents/{tracer,mapper,scribe}.md`
- `.claude/agents/tracer.md` — the machine-global body verbatim under a local `description:`
- `docs/BLUEPRINT.md` — the cast line naming `tracer`, `mapper`, `scribe`
- The Codex and OpenCode mirrors, by `pfm codex build` and `pfm opencode build`

## Open items

- The one-pass lint cap and the glued-line quote check have not been through a judged run.
- Cost is about twice the design this replaced; the lead's request count is the lever.
- Uncited rows: one run cited 60 of 92 verified rows.
- The served-as-terminal flag catches a served out name, not yet a served field quoted inside a DEFINES row.
- A language server's find-references as the widening substrate, grep as the fallback, is untried.
- One benchmark target per agent; a second would separate prompt effect from variance.
