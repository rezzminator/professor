# tracer

`tracer` answers one question about a codebase from its code — where a value goes, who writes or reads an entity, whether two things are connected. It is a lead over the shared ledger design; the script, the row format, the checks and the reader are specified in [ledger.md](ledger.md). This file holds what is the tracer's own.

## Contents

- [The run](#the-run)
- [Who reads](#who-reads)
- [Which buckets are read](#which-buckets-are-read)
- [The report](#the-report)
- [The return](#the-return)
- [Failure reports](#failure-reports)
- [Measured](#measured)
- [Open items](#open-items)

## The run

1. SURVEY. `ledger survey {root} {target}`, the first act; two surveys when the question names two entities. A spelling is added by hand only when the entity wears an unrelated name.
2. READ. The lead reads the code itself or spawns one `scribe` per bucket the question needs ([Who reads](#who-reads)). With four or more buckets the smallest bucket's reader goes first, alone, so the readers' shared prompt is in the cache before the rest spawn together. The round is awaited with one `ledger wait DIR N…` call, Bash timeout 600000.
3. VERIFY AND WIDEN. `ledger verify DIR; ledger widen DIR` in one call. New buckets between the target and what the question asks about get another round; buckets beyond the question stay unread and become leads. A bucket with no rows file is respawned once. At most 5 rounds, fewer when widen prints `the frontier is closed`.
4. ANSWER. `ledger show DIR --by file`; `ledger absent DIR name…` for every absence the answer will claim; the report written once with a quoted heredoc and linted in the same call; one editing call over the flagged lines; one more lint; publish.

## Who reads

| Survey | Reader | Budget |
| --- | --- | --- |
| At most 6 code files | The lead, writing rows into `DIR/rows-1.txt` in the reader's format | 20 calls |
| A chain question ("where does X go", "is A connected to B") walkable hop by hop | The lead, the same way | 20 calls |
| Anything larger | One `scribe` per needed bucket, brief `Bucket file: DIR/bucket-N.txt. Question being answered: {question}.` and nothing more | 1 read + at most 4 extra reads each |

The lead's own rows pass through `verify` like any reader's.

## Which buckets are read

The question decides. A bucket the question cannot touch — scratch scripts when the question is about production readers — may stay unread; it is then listed as unread and never described. This is the tracer's cost lever and its accuracy risk: one run that read 5 of its buckets finished with 34 rows and 43 unread code files.

## The report

`DIR/report.md`, in this order:

1. ANSWER — one to four sentences; every claim ends in its row ids `[R12]`, every absence in `[A3]`. A closed tally ("deletion happens three ways") is checked against every row of that kind and says "found" while code files remain unread.
2. CHAINS — one hop per line, `file:line — quoted line [R#]`, each chain ending in its terminal: RENDERS, PROMPTS (and where the model's reply goes, when a row shows it), EGRESS, STORES, `[A#]`, or UNREAD. A function that returns the value, a resolver or endpoint that serves it, is a hop.
3. LEADS — names the ledger met and the question did not need, each with its `file:line` and what it would tell the asker.
4. NOT READ — unread code files and buckets; the TEST, DOC, GENERATED and DATA files naming the target; rejected row counts; rounds run; readers spawned against returned; lint flags left standing.

## The return

The final message is short, because the report is on disk: the ANSWER verbatim, the three most useful LEADS, the lint's `COUNTS` line copied, and `Full report: DIR/report.md · ledger: DIR/ledger.txt`.

## Failure reports

| State | Return |
| --- | --- |
| The survey errors | `SURVEY FAILED — {the command and the error text}` |
| Nothing found after one retry with an added spelling | `NOTHING FOUND — {the counts table}` |
| A reader never writes | Named by `ledger wait`; respawned once; then listed in NOT READ |

## Measured

One question — every anchor of one stored table: what defines it, its writers and their triggers, its readers and where each read ends, what deletes it — scored against a 71-point key. Judging method and cost basis: [ledger.md](ledger.md#what-the-evidence-says).

| Design | Score /71 | Inventions | Cost (USD) |
| --- | --- | --- | --- |
| Lead with walkers, best of ten runs | 36.5 | 1 | 1.25 |
| Lead with walkers, every agent on `sonnet` | 43.5 | — | 2.95 |
| Ledger, readers free to search | 49.5 | 3 | 2.46 |
| Ledger, readers holding `Read` and `Write` only | 49.0 | 3 | 2.35 |

- The ledger design finds about a third more of the key and costs about twice the walker design's best run.
- Inventions did not fall: the remaining three a run are summary sentences — a closed tally contradicted by the run's own rows, a served field called a terminal with no client consuming it — not quotes, addresses or symbols.
- Chain questions cost 0.20–0.60 USD on the walker design with every edge true; the ledger tracer's read-it-yourself path for them has not been benchmarked.
- A run with lint and audit fix loops uncapped cost 3.81–5.74 USD; the one-pass cap that replaced them has not been through a judged run.

## Open items

- Benchmark a chain question and a small-survey question on the read-it-yourself path.
- The served-field trap: a GraphQL field declared in a schema file and consumed by no client was called a terminal in two judged runs.
- A second target, to separate prompt effect from the 5-point run-to-run variance.
