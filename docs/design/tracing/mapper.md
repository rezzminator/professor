# mapper

`mapper` maps the whole area around one target — a stored table, an endpoint, a module, a job, a field, a symbol: what defines it, who writes it and what triggers each write, who reads it and where each read ends, what runs on a clock, what guards and configures it, its neighbours, every name it leads to. It is a lead over the shared ledger design; the script, the row format, the checks and the reader are specified in [ledger.md](ledger.md). This file holds what is the mapper's own.

## Contents

- [The run](#the-run)
- [How it differs from tracer](#how-it-differs-from-tracer)
- [Probing the empty facets](#probing-the-empty-facets)
- [The map](#the-map)
- [The return](#the-return)
- [Failure reports](#failure-reports)
- [Measured](#measured)
- [Open items](#open-items)

## The run

1. SURVEY. `ledger survey {root} {target}`, the first act.
2. READ. One `scribe` per bucket, every bucket, brief `Bucket file: DIR/bucket-N.txt. Mapping the whole area of {target}: what defines it, who writes it and what triggers each write, who reads it and where each read ends, clocks, guards, registries, neighbours.` With four or more buckets the smallest goes first, alone, to put the readers' shared prompt in the cache. The round is awaited with one `ledger wait DIR N…` call, Bash timeout 600000.
3. VERIFY AND WIDEN. `ledger verify DIR; ledger widen DIR`, one call a round. Every new bucket belongs to the area, another project of the same repo included. A bucket with no rows file is respawned once. At most 5 rounds; stop when widen prints `the frontier is closed`.
4. PROBE. With the last round, the facets `ledger show DIR --by facet` leaves empty are probed ([Probing the empty facets](#probing-the-empty-facets)).
5. COMPOSE. `ledger show DIR --by facet` is the whole source; `ledger absent DIR name…` settles every absence; the map is written once and linted in the same call; one editing call over the flagged lines; one more lint; publish.

## How it differs from tracer

| | `tracer` | `mapper` |
| --- | --- | --- |
| Buckets read | Those the question needs | All of them |
| Lead reads the code itself | At most 6 code files, or a chain question | At most 3 code files in the whole survey |
| New buckets from widening | Read when they lie between target and question | All read: they are the area |
| A TOO COMMON name | Left as a lead | Grepped with its receiver or module path, widened on the qualified spelling |
| Empty facets | Not applicable | Probed |
| View of the ledger | `--by file`, to link chains | `--by facet` |

## Probing the empty facets

A facet with no row, a writer with no TRIGGERS row behind it, or a read chain ending in a served name nobody consumes is a question, not yet an absence. A reader cannot search, so the lead finds the name and the script makes the bucket: one Grep for the repo's own registry of that mechanism, then `ledger widen DIR {names}`.

| Gap | Probe |
| --- | --- |
| TIMING empty | The job, cron or worker registry; widen on its name and on the writers' and deleters' names |
| CONTROL thin | Widen on the guard, validation and audit names the entry-point rows quote |
| NEIGHBOURS empty | Widen on the entities the DEFINES and RELATES rows name — declared links, joins, unions, rows removed or anonymised together |
| A deleter or retention method with no trigger | Widen on its name until a route, mutation, job or command row appears |

A facet still empty after its probe is reported `nothing found`, with the probe's searches. "No clock found" rests on the registry's rows; without them it is a run-scoped statement, not a fact about the repo.

## The map

`DIR/report.md`, in this order:

1. SUMMARY — four to eight sentences a newcomer could act on: what the target is, the write paths and what starts them, the read paths and where they end, what runs on a clock or the quoted proof that nothing does, what guards it, and whatever the code does that its comments or docs say otherwise. Every claim ends in its row ids.
2. One section per facet, one row's fact a line — `file:line — what it does [R#]` — writers grouped with their triggers, readers laid out as chains to their terminals.
3. NAMES — every name the area leads to that a newcomer would need: entry points, wire names, registries, config keys, jobs, neighbouring entities, cited-but-absent files, each with its `file:line`.
4. NOT READ — unread code files; the TEST, DOC, GENERATED and DATA files naming the target; rejected row counts; rounds run; readers spawned against returned; lint flags left standing. The list enumerates and never judges: calling an unread file "a passing mention" cost one run a real writer.

The size of a registry, enum or column list comes from a row's `= COUNTED BY THE SCRIPT` line and from nowhere else.

## The return

The SUMMARY verbatim, the lint's `COUNTS` line copied, and `Full map: DIR/report.md · ledger: DIR/ledger.txt`.

## Failure reports

| State | Return |
| --- | --- |
| The survey errors | `SURVEY FAILED — {the command and the error text}` |
| Nothing found after one retry with an added spelling | `NOTHING FOUND — {the counts table}` |
| A reader never writes | Named by `ledger wait`; respawned once; then listed in NOT READ |

## Measured

One target — an audit-log table written from a request plugin and several services, read through two role-gated queries and an export route, with a two-tier retention sweep — scored against a 33-point key. Judging method and cost basis: [ledger.md](ledger.md#what-the-evidence-says).

| Design | Score /33 | Inventions | Cost (USD) |
| --- | --- | --- | --- |
| Lead with walkers, `medium` lead | 24.5 | 2 | 1.84 |
| Lead with walkers, `low` lead | 22.0 | 3 | 0.97 |
| Ledger, readers free to search | 27.0 | 4 | 3.54 |
| Ledger, readers holding `Read` and `Write` only | 27.5 | 2 | 2.64 |
| Ledger, with the prose auditor | 22.0 | 5 | 5.82 |

- The best ledger run leads the best walker run by 3 points at equal inventions and 0.80 USD more.
- Every absence row in the last three judged runs verified true, and so did every script-counted size; the one wrong registry size published was the lead's own number, written where a truncated note had hidden the script's. The inventions are summary prose — an invented scheduler link, a role list narrowed to one role, a guard said to sit in front of every operation.
- Breadth varies: the same target took 12 to 17 readers across runs, and cost followed.
- About half of the lost points sit in files or directories a run had already opened.

## Open items

- The one-pass lint cap has not been through a judged run; the last unjudged run cost 4.98 USD with 17 readers.
- A breadth bound: widening treats every file naming an out name as the area, and a widely called writer pulls in every caller.
- A second target, to separate prompt effect from run-to-run variance.
