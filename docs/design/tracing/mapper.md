# mapper

`mapper` maps one target's whole area from code: a stored table, an endpoint, a module, a job, a field or a symbol. The map covers its definition, its writers and what triggers each write, its readers and where each read ends, clocks, guards, neighbours, tests and checks. It is one agent over the shared `codeprobe` script, the tracer's loop run over a fixed list of facet asks. The script, the directives, the row checks and the return are specified in [codeprobe.md](codeprobe.md); this file holds what is the mapper's own.

## Contents

- [The run](#the-run)
- [The facets](#the-facets)
- [Measured](#measured)
- [Open items](#open-items)

## The run

1. Read `SKILL.md` § Extraction verbs and § Probe commands.
2. `init ROOT --map TARGET` at the root the caller names, never a sub-project of it. The brief's own questions, when it holds any, go on stdin as Q asks; `mentions` joins only when the brief removes or renames the target.
3. READ, facet by facet, with Read, Grep and `refs`. Each census line is a writer, a reader or neither. Writers are followed through their callers until a route, command, job or handler triggers them. Readers are followed to where the value ends; a served field, route or event name ends at the client that consumes it, in any project under the root.
4. ROWS. A file whose every line naming the target is one fact takes a `path:*` row. A facet searched and found empty takes an `absent` row or an UNANSWERED row naming the searches.
5. At 35 calls, or when every facet has rows, the final message is the manifest the script printed, naming the return file.

## The facets

| Ask | Facet | Directive | Rows the facet expects |
| --- | --- | --- | --- |
| M1 | Definition | — | Declaration, creating migration, later changes, keys, constraints, indexes, the enums and types its columns use with their values, as extraction rows |
| M2 | Writers | `census` on every spelling | Each writer and the route, command, job or handler that triggers it |
| M3 | Readers | `census` | Each reader and where its value ends |
| M4 | Timing | — | A job, cron or scheduler registry naming the target, a writer, or a writer's caller |
| M5 | Control | — | Guards, validation, config and registration on the entry points; the constants a guard reads |
| M6 | Neighbours | — | Entities it references, joins, or changes or removes together with it |
| M7 | Tests | `tests` | What the tests assert |
| M8 | Check | `checks` | The check command, a scoped variant, whether two copies can run at once |

## Measured

One target — an audit-log table written from a request plugin and several services, read through two role-gated queries, an export route and a frontend view, tombstoned by an erasure cascade — scored against a fixed 33-point key by an independent `opus` judge; HIT 1, PARTIAL 0.5; an invention is any stated fact the code contradicts or does not show. Cost at assumed list rates.

| Design | Requests | Cost (USD) | Seconds | Caller's copy /33 | Map on disk /33 | Inventions | Copy of the render |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Previous: walker lead, `low` effort | — | 0.97 | — | 22.0 | — | 3 | — |
| Previous: walker lead, `medium` effort | — | 1.84 | — | 24.5 | — | 2 | — |
| Previous: ledger with bucket readers | — | 2.64 | — | 27.5 | — | 2 | — |
| codeprobe 1 | 25 | 0.93 | 198 | 7.5 | 17.0 | 6 | a summary and "full detail above" |
| codeprobe 2: root rule, census once, final-message banner | 34 | 1.02 | 226 | 14.0 | — | 6 | whole |
| codeprobe 3: trigger chains, served names to their client, enums and guard constants | 20 | 0.94 | 204 | 18.5 | 18.5 | 7 (5 in the render) | rewritten |

- The mapper costs less than the previous designs' medium and ledger runs, and maps less than any of them: its best run is 3.5 points under the cheapest previous design.
- Every quote and every computed count checked true in run 3. The inventions are one-line notes after correct quotes: a write said to follow the export when it precedes it, a method put on the wrong class, a flow misnamed.
- What run 3 still missed sits one name away from the target: writers that reach the table through a service method never name it, and neither do the contract and documentation surfaces.
- The judge corrected the key: a migration the key called a phantom existed and was squashed into the baseline.

## Open items

- Breadth: one agent reading one hop out does not reach the writers that call the table only through a service. A mechanical second census, on the names of the functions holding each census line, is the lever the previous ledger design used and this one has not measured.
- Note inventions: five a run, all in notes after correct quotes. The note check catches a name the code does not hold, not a real name put in the wrong place.
- The final message is a model's copy of the render: whole once in three runs, a summary once, a rewrite once.
