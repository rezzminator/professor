# LLM codebase standard, version 2 (proposal)

A codebase is cheap for an LLM builder when a change takes few requests and each request is light.
Every law below is ranked by dollars measured on 1,421 recorded builder runs in three production repos (a Go engine with tests beside the source; a TypeScript and Python monorepo of six projects with separate test trees; a Python repo with about 950 plug-in directories of one fixed shape), 113,441 requests, $6,586.
The evidence, table by table: `docs/epics/llm-codebase/findings.md`.

This is a proposal: it replaces `/design:codebase` only after the owner rules on § Open rulings and the bench of § Validation is run.
Published work was checked against each law (`findings.md` § 13); where it changed a law, the law says so.

## 1. What is optimized

`dollars of a change = requests × cents per request`

| Number | Meaning | Measured today | Proposed target |
|---|---|---:|---:|
| Navigation requests per edit call | read and search requests divided by edit calls, runs of 6 to 20 edit calls | 4.0 to 4.5 | 3.0 |
| Navigation requests before the first edit | median, brief names its targets | 10 to 18 | 8 |
| Cents per request (apex model) | billed dollars divided by requests | 6.4 | 5.0 |
| Dollars lost to prompt-cache misses | share of run dollars | 8.1 % | under 3 % |

Read:write stays a reported number, never the target: counted in calls it is 4.0 in three unlike repos and falls from 10 to 3.4 with task size alone; counted in dollars it stays between 3.3 and 4.2 at every size.

## 2. The reader this is designed for

Measured habits, which prompt wording did not change in earlier benches:

1. One tool call per request (93 % of requests), each request re-reading about 140,000 tokens of context: 3.4 cents on the apex model before any result arrives.
2. A navigation call is a `grep` chained with a slice read (59 % of calls); a slice asks for 40 to 61 lines at the median.
3. A file is read in 1 to 3 calls; a run sees 11 % of a 400 to 800 line file it consults and 24 % of one it edits.
4. A run reads its own edited files most (33.5 % of navigation tokens), then its own captured logs (14.9 %), then files elsewhere in the repo (17 %).
5. Before its first edit a run reads to learn the contract the edit must agree with (52 % of pre-edit bytes), then the rules and manuals (18 %), the test home (11 %), a sibling to copy (10 %); finding the place is 8 %, dead ends under 1 %.
6. The prompt cache lasts 5 minutes; a command or wait that outlives it re-buys the whole context.

Design to these habits: put what the reader needs inside the first slice of the file it opens anyway.

## 3. Laws

Grade: M = measured on billed dollars or by regression with task size held fixed; T = seen in transcripts read line by line; H = follows from M and T, unproven until § Validation.

### Part A: fewer navigation requests

**A1. One change, one directory.** (M)
A unit is the set of files that change together, and it owns one directory.
Among apex-model runs of 6 to 20 edit calls, those that edit one directory cost $0.32 per edit call and those that edit 2 to 7 cost $0.47 to $0.51; across all runs, going from 2 to 8 directories multiplies navigation by 1.25.
Cut units from the commit history (file pairs that share commits), never from a layering diagram.
Check: directories per commit over a stated window, median 1 and 90th percentile at most 3.

**A2. The contract sits in the first 60 lines of the unit's front door.** (H, built on T: contract reads are 52 % of pre-edit reading)
The front door is the file named for its directory (`{package}.go`, `index.ts`, `__init__.py`, `mod.rs`).
Its opening comment, at most 60 lines of at most 160 characters, states in this order:
1. what the unit does, in one line;
2. what it exports: each entry point's signature and the one invariant a caller must keep;
3. what it consumes: the units and wire contracts it calls;
4. who consumes it: the directories that import it, or `none outside`;
5. its test home: the test files, the fixtures and the shared setup they load;
6. the one command that runs its tests;
7. the sibling to copy when adding the unit's repeated shape (`to add a reader: copy readers/price.py`), when it has one;
8. one line per other non-test file.
Sixty lines because that is the slice the reader asks for; a contract below line 60 costs a second request.
Risk, from the published record: orientation files written for agents raised inference cost by over 20 % with no general gain in success, and the machine-written ones lowered it.
So the block states only what the first slice of the code below it does not show, it is never loaded into the start context, and this law stays H until its bench arm wins.
Check: front door present; block length; every path and symbol it names exists; the test command it names is the narrow runner of B1.

**A3. A contract that crosses a boundary is written once, in the repo.** (T)
- A wire contract (schema, API, event, CLI output another surface parses) lives in one contract package with a consumer index: producer and each consumer as `path:symbol`.
- The current shape of stored data lives in one schema file beside the code that uses it; migration history is not where a reader learns the shape.
- A fact about something outside the repo (a vendor binary's behaviour, an upstream project's internals, a platform quirk) is written to `docs/external/{name}.md` in the change that first learned it, with how it was observed.
Runs that met an unwritten outside contract spent 22 to 70 calls on it; runs that met a schema in migrations grepped the migrations.
Check: every consumer the index names exists; orientation truth for `docs/external/`.

**A4. Directories stay small.** (M)
Going from 11 to 51 files in the edited directories multiplies navigation by 1.10 and dollars by 1.11.
A directory over the ceiling splits along co-change, each part a unit with its own front door.
Check: entries per directory against a ceiling (§ Open rulings), ratcheted.

**A5. One searchable name per thing, end to end.** (T)
The same word in file name, identifier, wire key, environment variable and test.
User-visible text is found in one grep: its key names the component that renders it.
Every tunable value has a name a grep finds; a bare number in two places is a value with no name.
Failed searches are already 1.4 to 2.3 % of searches, so this law holds what works; the two failures seen cost 21 and 8 search calls.

**A6. The test home is derived from the path, never searched.** (T; no effect at run level)
A test sits beside its source or at the same relative path under one test root; fixtures and shared setup are named in the front door (A2 item 5).

### Part B: lighter requests

**B1. No command outlives the cache.** (M: 5.9 % of dollars, 9.9 % in the repo with the slowest gate)
Every command a builder waits on returns within 4 minutes: a narrow runner `test {path}` that runs the tests of the paths named, and a full gate that is either under the budget or built to run in the background and be polled in slices under the budget.
Check: wall time per test scope against the budget; an unbudgeted scope fails.

**B2. Tools are quiet.** (M for the cost: captured-output read-backs are 14.9 % of navigation tokens; search results over 2,000 tokens hold 63 % of search tokens. H for the saving)
- A passing test or gate prints at most 40 lines; a failing one prints the failing cases, each with expected, actual and location, then the path of the full log.
- Output never exceeds the harness's inline limit, so it is never saved to a file and read a second time.
- A gate failure prints the rule it enforces and the fix; nobody opens the gate script to learn the rule (10 % and 14 % of runs in two repos read theirs).
- Quiet means the tool chooses what to print, never that a wrapper cuts what the reader needs: a reader that has to ask again pays a whole request, 3.4 cents on the apex model before any result. A published test of tool-output compression delivered 38.4 % fewer tokens and a 6.8 % higher bill.

**B3. The search path holds hand-written source only.** (M, T)
Generated code, vendored code, lock files, snapshots, recorded fixtures, data files, engine mirrors of prompts and nested checkouts are excluded by the search tool's ignore file, and reachable by naming their directory.
Lines stay under 300 characters in every file a builder slices.
Check: tracked files matching the generated, vendored and fixture patterns that the ignore file does not cover; files whose longest line exceeds the limit.

**B4. The start context has a token budget.** (M: 10.5 % of dollars; every 10,000 tokens cost 2.5 % of a run, 4.3 % on the mechanical model)
The orientation file, always-on rules and injected maps fit the budget; everything else loads by path.
A manual is consulted by section through greppable headings and never loaded whole; the command a builder needs from it is already in the front door (A2 item 6).
The most-read file in two of the three repos was a testing manual, opened by 32 % and 18 % of runs.
Published: with an orientation file, agents took 2.45 to 3.92 more steps per task and cost over 20 % more than without it; the replay here counts no saved request, so its 2.5 % is a floor.
Check: token count of the start context per agent type against the budget.

**B5. Split by heat, never by size alone.** (M)
The size of the edited files has no measurable effect on navigation or dollars, and reading whole files instead of slices would cost 8.6 % more.
The cost is in the tail: reads over 8,000 tokens hold 43 % of read tokens.
Split a file when it is on the list of files that cost the most to read in recorded runs, or when its commits touch disjoint regions (two units in one file); keep existing size ratchets, stop tightening them.
Files meant to be read whole (front-door blocks, contract files, manual sections) stay under 150 lines.
Check: the hot list, regenerated from recorded runs; no new line ceiling.

### Part C: the brief and the work tree

**C1. The brief quotes; it never points.** (M, T)
A task carries, inline: each target as `file:line`, the contract lines the edit must agree with, the failing output, the exact test command and the consumer list.
A target's path in the brief cuts the hops to find it from 10 to 3; the 5 to 8 hops to understand it remain unless the contract lines are there too.
One hop from the prompt to the facts: no prompt that names a brief file that names a handoff.

**C2. Evidence is cut per task.** (T)
A shared log, fault table or handoff is sliced into the brief of the task that needs the slice.

**C3. Independent items are worked one at a time.** (T)
A brief listing unrelated fixes orders read, edit, test per item; a survey of all items first puts every later request on top of it.

**C4. The fewest agents.** (M)
Each spawn pays the start context again; work splits where ownership splits, never to add readers.

### Part D: levers the tree cannot pull

Named so no one expects the layout to deliver them:

| Lever | Replayed saving |
|---|---:|
| Independent reads issued together, 2 or 3 calls per navigation request | 12.2 % to 16.6 % |
| Thinking on navigation requests halved | 6.4 % |
| Model tier: an apex run makes 1.70 times the navigation requests of a mechanical run of the same size and costs 3.28 times | by routing |

## 4. What each part is worth

Replay of the 1,421 recorded runs (`scripts/sim.py`):

| Assumed change | Laws | Saving |
|---|---|---:|
| A third of the navigation before the first edit is not needed | A2, A3, C1, C2 | 9.9 % |
| A quarter of the navigation between edits is not needed | A1, A2, A4, A6 | 9.3 % |
| No command or wait outlives the cache | B1 | 5.9 % |
| No read of repo files over 4,000 tokens | B5, A2 | 4.3 % |
| No search result over 2,000 tokens | B3 | 2.7 % |
| No read-back of captured output over 1,500 tokens | B2 | 2.7 % |
| No line read twice in one context | | 1.6 % |
| The seven rows above together | Parts A, B, C | 31.1 % |
| The same without the three cap rows (reads, search results, read-backs) | Parts A, B, C | 25.1 % |
| Start context 10,000 tokens smaller | B4 | 2.5 % |
| Two calls per navigation request | Part D | 12.2 % |
| All nine together | | 40.3 % |
| All nine without the three cap rows | | 34.3 % |

The first two rows are assumptions about behaviour; the rest are accounting on recorded requests.
The three cap rows hold only if the reader asks for nothing more, so the totals with them are a ceiling and the totals without them the floor (`findings.md` § 13, row 3).

## 5. Dropped from version 1, and why

| Version 1 | Ruling | Evidence |
|---|---|---|
| Target: first-edit hops at most 3 | Dropped; replaced by the measured baselines of § 1 | met by 2 to 6 % of runs; the hops are contract reading, which a tree alone does not remove |
| Target: at most 2 files re-read | Dropped | met by 20 to 33 % of runs; half of re-reading follows a compaction |
| Target: zero-result searches about 0 | Dropped as a goal | already 1.4 to 2.3 % |
| A line ceiling for every source file | Kept as existing ratchets, no longer a design law | no effect of file size at run level |
| Fixed file set per unit | Optional pattern, not law | the directories built to it saw almost no builder work in six weeks, so it is untested |
| Flow-ordered directory prefixes | Dropped | no evidence either way; costs a rename of every path |
| Glossary and spelling census as a gate | Reduced to A5 | finding the place is 8 % of pre-edit reading |
| Twelve-step procedure and ten-section design document | Reduced to §§ 3 and 6 | the cost sits in six properties, each checkable alone |
| The gate protocol: one `CHECK id PASS, FAIL or ERROR` line per check, baselines that only shrink | Kept unchanged | sound engineering, not measured here |
| Format, lint, duplicate, coverage and publication gates | Out of scope | they serve correctness and drift, which this data does not price |

## 6. Checks

Every check follows version 1's protocol (one line, three states, shrinking baselines); the new ones:

| Check | Law | Counts | Broken state reports |
|---|---|---|---|
| dirs-per-commit | A1 | distinct directories per commit over the window: median, 90th percentile | the commits over the limit |
| front-door | A2 | units without a front door; blocks over 60 lines; named paths, symbols or commands that do not exist | the unit and the missing item |
| contract-index | A3 | consumers the index names that do not exist; importers of the contract package missing from the index | the path |
| dir-size | A4 | directories over the ceiling | the directory and its count |
| command-budget | B1 | test scopes over the wall-time budget or without one | the scope and its seconds |
| quiet-tools | B2 | lines printed by a passing gate; a gate failure without its rule line (tested on a planted violation) | the script |
| search-surface | B3 | tracked generated, vendored and fixture files the ignore file does not cover; lines over the limit | the files |
| start-context | B4 | tokens in the start context per agent type | the type and its count |
| hot-files | B5 | the 20 files with the highest read dollars in recorded runs | the list, for the owner |

Run-level numbers (§ 1) come from the tool-call store, with the scripts beside this document: navigation requests per edit call, navigation before the first edit, cents per request, cache-miss dollars and their cause.

## 7. Validation

Laws graded H or T are not adopted on argument. The bench, before any tree is migrated:

1. Tasks: 6 recorded tasks per repo, picked from runs of 6 to 20 edit calls, replayable from their brief at their base commit.
2. Arms, one change each against the recorded baseline:
   - A2: front-door contract blocks for the units each task touches, written by a building agent as production would write them;
   - C1: the same brief with contract lines quoted in place of pointers;
   - B1 and B2: the narrow runner and quiet output in place of the full gate;
   - A2 and C1 together.
3. Measure per run: navigation requests per edit call, navigation before the first edit, dollars, whether the task passed its gate. Two runs per arm and task.
4. A law ships when its arm is cheaper on at least two thirds of the tasks and no task loses its gate; otherwise it is dropped from the standard, with the number.
5. Stated before the first run: the metric is dollars per passed task, and the cap for the bench.

## 8. Open rulings

Each with the recommendation first.

1. Directory ceiling for A4: **20 entries.** Over it today: 42, 40 and 9 directories in the three repos, holding 60 %, 35 % and 12 % of their source and test files. Alternative 40: 21, 7 and 3 directories.
2. Command budget for B1: **240 seconds**, one minute inside the cache. Alternative 270 seconds, the slice the lander already waits in.
3. Start-context budget for B4: **30,000 tokens** per builder type; today 44,000, and 19,000 six weeks ago. Alternative: hold at today's 44,000 and only forbid growth.
4. Front-door block for A2: **60 lines of 160 characters.** Today's front doors are a median 272 lines in one repo and 4 to 5 lines (nearly empty) in the other two.
5. Size ceilings: **keep the existing ratchets, add none.** Alternative: remove them, which the data would also allow.
6. Order of adoption: **B1 and B4 first** (accounting, no bench needed, 8 % together), then the bench for A2, B2 and C1, then A1 and A4 by ratchet on touch. B2 left the first group when the published record showed that less tool output can cost more.
7. Where this standard lives: **replace `/design:codebase` after the bench**, through `pcm`. Alternative: publish it beside version 1 now, marked proposal.
