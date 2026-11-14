# Read versus write in builder runs: where the dollars go and what moves them

Measured on every builder run recorded by callmeter in professor, monorepo and plugin-repo, 2 October to 13 November 2026.
Companion: the proposed standard built on these numbers, `docs/epics/llm-codebase/standard.md`.

## 1. The answer in twelve lines

1. A builder makes 4 navigation calls for every edit call, in all three repos: professor 3.98, monorepo 4.00, plugin-repo 4.09.
2. In dollars, navigation requests take 61 % of builder spend, edit requests 25.5 %, test requests 4.4 %.
3. The three repos have very different layouts and nearly identical spending: results of reads and searches are 39 %, 39 % and 40 % of the dollars. The ratio is set by how the agent works, and the repo moves it by tens of percent, not by multiples.
4. The unit of cost is the request, not the token read: dollars rise in a straight line with requests (correlation 0.965, log-log slope 1.00). One Opus request costs 6.4 cents, of which 3.4 cents are paid before any result arrives (the context is re-read, the model thinks). One Sonnet request costs 3.2 cents.
5. 93 % of requests carry exactly one tool call.
6. Builders do not hunt for the place. In 24 transcripts read line by line, finding the location was 8 % of what they read before the first edit; learning the contract their edit must agree with (callers, schemas, scripts, tests) was the largest use in every repo, 52 % in professor.
7. A run reads its own edited files most: 33.5 % of navigation tokens. Files elsewhere in the repo are 17 %; the run's own captured logs are 15 %.
8. Builders already read in slices: a median range read asks for 40 to 61 lines, and a run sees 11 % of a 400 to 800 line file it only consults. Reading whole files instead would cost 8.6 % more, so file size ceilings buy little.
9. What measurably moves navigation with task size held fixed: directories touched by the change (2 to 8 directories: 1.25 times the navigation; one directory costs $0.32 per edit call against $0.47 to $0.51 for 2 to 7), files per directory (11 to 51: 1.10 times), start context, paths in the brief (0.92 times).
10. What does not measurably move it: size of the edited files, test beside its source, path depth, repeated file names, failed searches (1.4 to 2.3 % of searches).
11. 8.1 % of builder dollars are prompt-cache misses; two thirds come from a command or a wait that outlived the 5-minute cache.
12. Replaying all 1,421 runs under the repo and brief rules of the proposed standard prices them 25 to 31 % cheaper: 25 % with no cap on tool output, 31 % if the caps cause no extra request. With two calls per navigation request and a 10k smaller start context: 34 to 40 %.

The published record, checked against these numbers: § 13.

## 2. What was measured

| Item | Value |
|---|---|
| Store | callmeter snapshot, 580,352 tool calls, 22,424 agents, 2 Oct to 13 Nov 2026 |
| Builder | a sub-agent whose type matches foreman, executor or worker; landers are counted apart |
| Runs kept | 2,877 builder and lander runs in the three repos, $9,696 |
| Claude builders | 1,421 runs, $6,586: professor 498 runs / $2,417, monorepo 737 / $3,194, plugin-repo 186 / $975 |
| Claude landers | 1,036 runs, $1,961 |
| GPT builders | 279 runs, $923, kept for comparison only |
| Requests replayed | 113,441 Claude builder requests, each with its billed dollars |
| Tool calls classified | 188,413, by `callmeter classify` on the full command text from the transcript |
| Transcripts read line by line | 24 (8 per repo: 2 longest surveys, 2 most expensive, 2 lowest read:write, 1 typical Opus, 1 costliest Sonnet) |

Method, in one paragraph: every request's billed dollars are split between what it carried.
The context of each request is decomposed into the start context, the output of earlier requests and each tool result, labelled by the file it came from.
A token is charged its cache-write price when it enters, the cache-read price at every later request that carries it, and the write price again on a cache miss.
The accounting was checked out of sample: predicting a run's dollars from its context trajectory alone, on 711 runs the model was not fitted on, gives R² 0.973, total within 1 %, median error 6.3 %.

Definitions:
- navigation call: a call classed read or search (a `grep` chained with a `sed -n` slice is one navigation call);
- edit call: a call that authors file content, through the Edit and Write tools or through `sed -i` and Python heredocs in Bash;
- survey: everything before the first edit of a repo file;
- tail: everything after the last edit of a repo file.

## 3. The read:write numbers per repo

Claude builders with at least one repo edit, 1,254 runs.

| Repo | Navigation : edit calls | Read : write tokens | Read : write dollars | Files read : edited | Navigation calls before the first edit, p50 / p75 / p90 | Dollars before the first edit, p50 | Share of dollars before the first edit |
|---|---:|---:|---:|---:|---:|---:|---:|
| professor | 3.98 | 4.43 | 2.52 | 2.07 | 18 / 30 / 45 | $1.02 | 26 % |
| monorepo | 4.00 | 4.40 | 2.60 | 2.02 | 15 / 27 / 41 | $0.76 | 25 % |
| plugin-repo | 4.09 | 5.37 | 2.89 | 1.83 | 10 / 19 / 31 | $0.56 | 18 % |

Opus surveys about twice as long as Sonnet before its first edit: professor 21 against 10 navigation calls, monorepo 19 against 9, plugin-repo 12 against 8.

The call ratio falls with task size and the dollar ratio does not:

| Edit calls in the run | Runs | Share of dollars | Dollars per run, p50 | Dollars per edit call | Navigation : edit calls | Read : write dollars | Share spent before the first edit |
|---|---:|---:|---:|---:|---:|---:|---:|
| 1-2 | 206 | 4.1 % | $0.70 | $0.83 | 10.2 | 3.7 | 53 % |
| 3-5 | 236 | 7.0 % | $1.15 | $0.49 | 5.8 | 3.9 | 41 % |
| 6-10 | 303 | 13.7 % | $2.12 | $0.37 | 4.5 | 3.3 | 37 % |
| 11-20 | 227 | 18.9 % | $4.29 | $0.36 | 4.0 | 3.7 | 26 % |
| 21-40 | 200 | 30.4 % | $8.98 | $0.34 | 3.7 | 4.0 | 20 % |
| 41+ | 99 | 26.0 % | $14.93 | $0.28 | 3.4 | 4.2 | 13 % |

The top 20 % of runs hold 61 % of builder dollars, and their median length is 196 requests.

## 4. Where a builder dollar goes

All Claude builders, $6,586.

| What the dollar paid for | Share |
|---|---:|
| Start context (system prompt, tools, orientation, brief), carried through the run | 10.5 % |
| Writing the edits (8.3 %) and thinking on edit requests (2.1 %) | 10.4 % |
| Navigation commands and prose (4.8 %) and thinking on navigation requests (7.8 %) | 12.6 % |
| Other output: tests, git, spawns, final report | 4.2 % |
| Earlier output carried in the context of later requests | 18.2 % |
| Read results: source 11.1, tests 5.2, captured logs 4.9, orientation and prompts 3.4, config and other 3.5, docs 0.7 | 28.8 % |
| Search results | 10.7 % |
| Test and gate output | 0.9 % |
| Other results and harness additions | 3.6 % |

By mechanism: cache write 22.1 %, carrying context at the cache-read price 42.5 %, cache misses 8.1 %, output 27.2 %.

By phase and kind of request (runs with repo edits):

| Phase | Read | Search | Edit | Test | Other | Final | Total |
|---|---:|---:|---:|---:|---:|---:|---:|
| Survey, before the first edit | 6.8 % | 14.7 % | 1.3 % | 0.3 % | 1.3 % | 0 % | 24.4 % |
| Build, first to last edit | 9.2 % | 24.4 % | 22.6 % | 2.8 % | 4.3 % | 0.5 % | 63.9 % |
| Tail, after the last edit | 1.6 % | 4.0 % | 1.5 % | 1.3 % | 1.0 % | 2.2 % | 11.7 % |
| All | 17.6 % | 43.2 % | 25.5 % | 4.4 % | 6.5 % | 2.7 % | |

Navigation between edits (33.6 %) is larger than the survey (21.5 % of navigation inside 24.4 %).

Landers ($1,961) spend the same way: reads 29.8 %, search results 14.5 %, carried output 16.8 %, cache misses 13.4 %.

## 5. The mechanics of one request

| | Opus builder | Sonnet builder |
|---|---:|---:|
| Requests measured | 92,607 | 20,834 |
| Cents per request | 6.38 | 3.24 |
| of which re-reading the carried context | 2.74 | 1.26 |
| of which output (thinking inside it) | 1.74 (0.69) | 0.87 (0.32) |
| Mean context per request, tokens | 140,594 | 129,837 |

- A navigation request brings in 3,110 new tokens on average and costs 5.98 cents; an edit request 7.40 cents; a test request 5.58 cents.
- A request that returns nothing still costs 3.42 cents on Opus.
- 1,000 tokens of result cost 0.5 cents once, then 0.02 cents at every later request: 2 cents more over 100 requests.
- So one saved request is worth about 1,400 tokens carried for 100 requests.
- Context saturates: runs of 60 to 119 requests end near 193k tokens; longer runs compact (context before a compaction: 193k at the median, after: 44k) and hover at 140 to 165k. That is why dollars grow in a straight line with requests instead of with their square.
- The price of one read token therefore depends on the run: $5.8 per million in runs under 30 requests, $18 to $19 per million in runs over 120.
- Thinking stays in the context: context growth per request = 1.10 × spoken output + 0.89 × thinking + 0.47 tokens per delivered byte + 291 (53,913 Opus requests, R² 0.53).
- Search requests think on 73 % of requests, 446 thinking tokens on average, more than the 259 tokens they speak.

Cache misses, $535 (8.1 % of builder dollars):

| Cause | Events | Dollars | Share |
|---|---:|---:|---:|
| A wait of 5 to 60 minutes on something else (sub-agent, queue) | 343 | $199 | 37 % |
| A tool that ran longer than 5 minutes (mostly tests and gates) | 338 | $172 | 32 % |
| After a compaction (the context is replaced) | 1,023 | $141 | 26 % |
| A gap over 60 minutes | 35 | $18 | 3 % |
| Other | | $6 | 1 % |

Every sub-agent cache write is at the 5-minute price (0.0 % at the 1-hour price).
plugin-repo loses most to long tools: 53 % of its $133.

## 6. What the reads are

Shape of a navigation call (77,102 calls, Bash is 94.8 % of them):

| Shape | Share of calls | Share of navigation tokens | Tokens per call |
|---|---:|---:|---:|
| `grep` and a slice read chained in one call | 59.0 % | 45.2 % | 1,630 |
| Slice or `cat` read only | 24.0 % | 28.8 % | 2,557 |
| `ls`, `find`, `wc` | 5.9 % | 9.0 % | 3,246 |
| The Read tool | 5.2 % | 13.5 % | 5,574 |
| `grep` only | 4.5 % | 2.3 % | 1,087 |

- 87 % of Bash navigation calls chain three or more commands, and 57 % cap their output with `head`.
- Navigation requests come in streaks: 22 % of them sit in streaks of 12 or more consecutive read or search requests.
- A search result is 895 tokens at the median and 3,313 at the 90th percentile; the 22 % of results over 2,000 tokens hold 63 % of search tokens.
- A read is 1,819 tokens at the median; the 10.7 % of reads over 8,000 tokens hold 43 % of read tokens.

What the navigation tokens are, relative to the files the same run edited:

| Relation | Share of navigation tokens |
|---|---:|
| A file this run edited | 33.5 % |
| The run's own captured logs and output files | 14.9 % |
| Listings and piped output with no file named | 9.2 % |
| Source elsewhere in the repo | 7.6 % |
| A sibling in a directory this run edited (7.3 % non-test, 3.2 % test) | 10.5 % |
| Flight state: spec, brief, reports | 6.2 % |
| Other files elsewhere in the repo | 4.1 % |
| Same package, another directory | 3.4 % |
| Orientation: CLAUDE.md, `.claude/**` | 3.3 % |
| The test or source twin of an edited file | 2.8 % |
| Docs | 1.7 % |
| A test elsewhere, config, outside the repo, engine config | 3.0 % |

How one file is read (pairs of run and file):

| File lines | Read calls, edited file (mean) | Share of the file seen, edited (p50) | Share seen, consulted only (p50) | First read is the whole file |
|---|---:|---:|---:|---:|
| 1-100 | 1.6 | 100 % | 100 % | 58 % |
| 101-200 | 1.8 | 78 % | 51 % | 27-35 % |
| 201-400 | 2.0 | 40 % | 24 % | 16-21 % |
| 401-800 | 2.6 | 24 % | 11 % | 10-13 % |
| 801-1500 | 2.8 | 14 % | 6 % | 4-5 % |
| 1501+ | 3.6 | 9 % | 4 % | 3-7 % |

- Consulting a 401 to 800 line file costs $0.048 per run against $0.016 for a file under 100 lines: three times the dollars for eight times the size.
- Of all lines read from source and test files, 11.3 % were already in the same context, and 11.4 % were read again after a compaction removed them.

Why builders read before the first edit (24 transcripts, each pre-edit call labelled; professor's 8 runs, 261 calls, 1.31 MB):

| Purpose | Calls | Share of bytes |
|---|---:|---:|
| Contract: the code the edit must agree with, the target file included | 134 | 52 % |
| Rule: manuals, specs, the brief's own evidence files | 25 | 18 % |
| Test home: the test file, fixtures, how to run it | 26 | 11 % |
| Pattern: a sibling to copy | 16 | 10 % |
| Locate: finding where the change goes | 38 | 8 % |
| Tooling | 14 | 1 % |
| Dead end | 8 | under 1 % |

The monorepo and plugin-repo readers found the same order: contract reads were the largest use of every run (about 113 calls in the monorepo's 8 runs).
What the readers tagged as avoidable, with the proving runs in `~/.local/state/pfm/flights/professor/llm-codebase/agents/`:
- large files read whole or in many ranges when the brief named one function (professor: 17 calls, 306 KB; two runs carry most of it);
- a manual, spec or CI log read whole where the run used a few lines (the testing manual is 22 KB per load; a run "used only its runner lines");
- a contract that lives outside the repo and is written down nowhere (the Claude Code binary, an upstream checkout, vendored crates): 22 calls tagged, about 70 with one run's upstream reads;
- a brief that points instead of quoting (prompt, then brief file, then a 2,000-line handoff);
- output over the harness's inline limit, saved to a file and read again whole (three monorepo runs, 20 to 36 KB each);
- a finding named by its on-screen text: 21 search calls from the text to a locale key to a component;
- a value with no single searchable name: 8 calls;
- tests not beside their source and fixtures spread over several directories (plugin-repo: 25 calls, about 170 KB);
- a schema that lives in migration files far from its use;
- an importer search that returns hundreds of identical lines, one per plug-in directory;
- the environment before the code: 119 of one run's 135 pre-edit calls started a dev stack because the brief ordered before-screenshots;
- surveying nine independent failures before fixing any (57 calls) where a sibling run with the same brief file fixed them one by one (first edit after 7 calls).

The files most runs read are not source:

| Repo | File | Share of runs reading it | Read dollars |
|---|---|---:|---:|
| professor | the pfm testing manual (112 lines, 22 KB) | 32 % | $29.3 |
| professor | the arch-check gate script (819 lines) | 10 % | $3.8 |
| monorepo | five testing runbooks | 7 to 18 % each | $61.7 together |
| monorepo | `.env.ports` | 26 % | $14.7 |
| plugin-repo | the suite-checks gate script (1,639 lines) | 14 % | $7.1 |
| plugin-repo | two testing manuals | 13 and 15 % | $12.0 together |

On top of that, 35 % of builders load at least one skill, 3,488 tokens per load, 3.0 million tokens in all.

## 7. Factors, ranked by dollars

Grades: A = accounting on billed dollars; B = replay of the recorded runs under a stated assumption; C = regression with task size held fixed, 1,270 runs, held-out R² 0.73 for navigation requests and 0.86 for dollars; D = read in the 24 transcripts.

| # | Factor | Size | Grade |
|---:|---|---|:--:|
| 1 | Number of requests; navigation is 64 % of them | 61 % of dollars sit in navigation requests | A |
| 2 | One tool call per request (93 % of requests) | two calls per navigation request: 12.2 % cheaper, three: 16.6 % | B |
| 3 | Model tier | an Opus run makes 1.70 times the navigation requests of a Sonnet run of the same size and costs 3.28 times; Opus also gets the harder work by rule | C |
| 4 | Navigation between edits | 33.6 % of dollars; a quarter of it removed: 9.3 % cheaper | A, B |
| 5 | Navigation before the first edit | 21.5 % of dollars; a third of it removed: 9.9 % cheaper | A, B |
| 6 | Prompt-cache misses | 8.1 % of dollars; no command or wait past 5 minutes: 5.9 % cheaper (plugin-repo 9.9 %) | A, B |
| 7 | Thinking on navigation requests | 7.8 % of dollars generated, more carried; halved: 6.4 % cheaper | A, B |
| 8 | Start context | 10.5 % of dollars; grew from 19k to 44k tokens over the period; 10k smaller: 2.5 % cheaper (Sonnet 4.3 %) | A, B |
| 9 | Directories touched by one change | 2 to 8 directories: 1.25 times the navigation, 1.15 times the dollars; one directory: $0.32 per edit call against $0.47 to $0.51 | C |
| 10 | Large single reads | reads over 8,000 tokens hold 43 % of read tokens; no read over 4,000 tokens: 4.3 % cheaper | A, B |
| 11 | Search result size | results over 2,000 tokens hold 63 % of search tokens; capped at 2,000: 2.7 % cheaper, at 1,000: 4.0 % | A, B |
| 12 | Reading back captured output | 14.9 % of navigation tokens; capped at 1,500 tokens: 2.7 % cheaper | A, B |
| 13 | Files per directory | 11 to 51 files: 1.10 times the navigation, 1.11 times the dollars | C |
| 14 | Manuals and skills loaded whole | about 2 % of dollars | A |
| 15 | Re-reading | 11.3 % of read lines were already in context: 1.6 % cheaper without | A, B |
| 16 | The brief | 3 to 14 paths in the brief: 0.92 times the navigation; the target's path in the brief cuts the hops to find it from 10 to 3 and leaves the 5 to 8 hops to understand it | C |
| 17 | New files | a third of the edited files new: 1.07 times the dollars | C |
| 18 | Contracts outside the repo, findings named by on-screen text, values with no searchable name | tens of calls in the runs that hit them | D |

No measurable effect at the run level (95 % interval includes 1): mean size of the edited files (0.96), largest edited file (1.09), test beside its source (0.99), path depth (1.02), share of test files (1.02), line anchors in the brief (1.01), repeated file names (1.05, borderline).
Already near zero: searches that return nothing, 1.4 to 2.3 %; dead ends, under 1 % of pre-edit bytes; failed calls: 2.4 % of edit calls fail (the Edit tool 2.6 %, Write 1.0 %; Sonnet 4.2 %, Opus 1.5 %), and requests whose every call failed are 1.6 % of dollars, 0.6 % of dollars for edits (`results/a10.txt`).
Over the six weeks, navigation at fixed task size rose 1.12 times.

## 8. The simulator

`~/.local/state/pfm/flights/professor/llm-codebase/scripts/sim.py` replays each of the 1,421 runs with one rule changed and re-prices it with the checked accounting.
Each line assumes the behaviour it names; the dollars follow from the recorded requests.

| Lever | All | professor | monorepo | plugin-repo | Requests |
|---|---:|---:|---:|---:|---:|
| A third of the navigation before the first edit is not needed | 9.9 % | 11.2 % | 9.8 % | 6.8 % | -6.9 % |
| Half of it | 14.2 % | 16.1 % | 14.1 % | 9.9 % | -10.0 % |
| A quarter of the navigation between edits is not needed | 9.3 % | 9.9 % | 8.6 % | 9.8 % | -8.8 % |
| Two calls per navigation request | 12.2 % | 13.6 % | 11.8 % | 9.9 % | -25.8 % |
| Three calls per navigation request | 16.6 % | 18.6 % | 16.0 % | 13.4 % | -34.1 % |
| No command or wait outlives the 5-minute cache | 5.9 % | 3.3 % | 6.7 % | 9.9 % | 0 |
| Start context 10k tokens smaller | 2.5 % | 2.6 % | 2.6 % | 1.9 % | 0 |
| Start context 20k tokens smaller | 4.6 % | 5.0 % | 4.7 % | 3.3 % | 0 |
| No search result over 2,000 tokens | 2.7 % | 2.4 % | 2.6 % | 3.5 % | 0 |
| No read of repo files over 4,000 tokens | 4.3 % | 4.1 % | 4.5 % | 4.0 % | 0 |
| No read-back of captured output over 1,500 tokens | 2.7 % | 2.2 % | 2.8 % | 3.7 % | 0 |
| No line read twice in one context | 1.6 % | 1.8 % | 1.5 % | 1.2 % | 0 |
| Thinking on navigation requests halved | 6.4 % | 7.3 % | 5.8 % | 6.3 % | 0 |
| Repo and brief levers together | 31.1 % | 30.2 % | 31.2 % | 32.6 % | -15.7 % |
| Those plus two calls per request and 10k less start context | 40.3 % | 40.4 % | 40.3 % | 39.9 % | -34.0 % |
| Repo and brief levers without the three output caps | 25.1 % | 24.9 % | 25.1 % | 25.9 % | -15.7 % |
| Those plus two calls per request and 10k less start context | 34.3 % | 35.0 % | 34.1 % | 33.3 % | -34.0 % |

In dollars on the recorded $6,586: the repo and brief levers together $2,046, or $1,655 without the caps; with batching and the smaller start context $2,652, or $2,260 without the caps.
The three cap rows (search results, reads of repo files, read-backs of captured output) price the tokens removed and assume the reader asks for nothing more.
A published test found the opposite can happen (§ 13, row 3), so the totals with the caps are a ceiling and the totals without them the floor.

## 9. Beliefs tested and how they fared

| Belief | Verdict | Evidence |
|---|---|---|
| The read:write ratio measures how good the repo is | Mostly not | the call ratio is 4.0 in three unlike repos, falls from 10 to 3.4 with task size alone, and the dollar ratio stays 3.3 to 4.2 at every size |
| Agents spend their reads finding the place | No | locate is 8 % of pre-edit bytes; searches returning nothing are 2 %; dead ends under 1 % |
| Cost grows with the square of run length | No | straight line; context saturates near 150 to 190k and compacts |
| Reading whole files would save hops (my own belief going in) | No | a run reads a file in 1 to 3 calls and sees 11 to 24 % of a large one; whole reads would cost 8.6 % more |
| Big files are the main read cost | Partly | no run-level effect of edited-file size; the cost is in the tail of whole reads (reads over 8,000 tokens hold 43 % of read tokens) |
| One change should land in one directory | Yes | one directory: $0.32 per edit call against $0.47 to $0.51 |
| A target path in the brief removes the survey | Partly | it removes the hops to find the file, not the hops to understand it |
| Line anchors in the brief cut navigation | Not at run level | 1.01, interval 0.94 to 1.07; the cheapest transcripts did have file:line plus the failure text |

## 10. The old standard, `/design:codebase`

Implemented: professor has `pfm/scripts/arch-check.sh` (since 12 September) and monorepo has an `arch-checks.sh` in all six projects (since 5 September); plugin-repo has none.
monorepo also built 31 directories to its fixed file set.

Working, by its own metrics: no.

| Its metric | Its target | Measured |
|---|---|---|
| Hops from first source read to first edit | at most 3 | locate 3 to 6 plus understand 5 to 7 at the median; met by 2 %, 5 % and 6 % of runs |
| Files opened twice in a session | at most 2 | median 7, 4 and 4; met by 20 %, 33 % and 30 % of runs |
| Searches with no result | about 0 | 2.3 %, 2.0 %, 1.4 % |
| Directories per change | one | per commit: professor median 2 (90th percentile 11), monorepo 4 (29), plugin-repo 1 (3); per builder run: median 4 in all three, one directory in 13 to 17 % of runs |

Per claim:
- "One change lands in one directory": supported, and its largest structural effect.
- "The first grep finds the name": already true, and not where the dollars are.
- Size ceilings for every source file: no measurable effect on navigation or dollars.
- Tests mirrored beside the source: no measurable effect at run level; test-home hunts are real in the plugin-repo's transcripts (25 calls).
- Fixed file set per unit: cannot be judged. Zero of 636 monorepo builder runs worked mostly in the 31 directories built to it, and 12 plugin-repo builder runs worked mostly in the 949 plug-in directories.
- Flow-ordered directory prefixes, the glossary census, façades: not measurable from these runs.
- The gate scripts themselves became reading: 10 % of professor runs read arch-check.sh and 14 % of plugin-repo runs read suite-checks.sh, to learn a rule the gate could have printed.
- Its lint, format, duplicate and coverage gates serve correctness and drift, which this data does not measure; they are not judged here.

The gates predate the callmeter store (2 October), so no before-and-after exists.
Inside the store's six weeks, with the gates, the front-door rule (7 November) and the codebase map (8 November) in place, navigation at fixed task size rose 1.12 times.

## 11. Gaps

- Main chats are not measured: only sub-agent builders and landers.
- the monorepo's and the plugin-repo's fixed-file-set directories saw almost no builder work, so that design is untested.
- The front-door rule and the codebase map are too recent to isolate (one week of runs, adopted on touch only).
- The trace's edit detection was checked against 24 transcripts: the first-edit index disagreed in 5 of them (by 1 to 5 calls in four; by 63 calls in one, where an edit of a gitignored env file was counted), and Python-heredoc writes were missed in 1. Skill loads are counted under "other results", not under reads.
- Agent thinking text is not stored; "hunting or understanding" in the transcripts is inferred from commands and timing.
- Simulator levers are assumptions about behaviour priced exactly, not experiments. Earlier benches showed wording alone does not change the read habit; a forced trace in a Sonnet brief did (27 % cheaper, 35 % fewer requests).
- One harness and one set of tools: what the published record ranks highest (the edit format, the width of the file viewer, a structure-aware read tool) is not varied here, so this report cannot rank the interface against the repo.
- Model tier and start context are confounded with which work each agent type is given.
- File line counts are today's; files since deleted or moved carry none.
- No row of this report comes from GPT runs.

## 12. Files

| What | Path |
|---|---|
| Scripts (join, census, factor tables, regression, simulator) | `~/.local/state/pfm/flights/professor/llm-codebase/scripts/` |
| Every table's raw output | `~/.local/state/pfm/flights/professor/llm-codebase/results/` |
| Derived data, gzipped JSON lines (agents, requests, calls, episodes, files, briefs) | `~/.local/state/pfm/flights/professor/llm-codebase/data/` |
| The three transcript investigations | `~/.local/state/pfm/flights/professor/llm-codebase/agents/` |
| The proposed standard | `docs/epics/llm-codebase/standard.md` |
| The literature scan and its eight sub-reports | `.professor/RR/repo-properties-agent-cost-2026-11-13/` |

To rerun: `build.py` reads the callmeter snapshot path set at its top (`DB`) and the three checkouts, then each `aN.py` and `sim.py` reads its output; `a8.py` needs numpy (`uv run --with numpy python a8.py`); `a10.py` reads the gzipped files under `data/`.
`a1.py` to `a9.py` and `sim.py` read uncompressed copies from the session scratch directory named at their top (`SP`): once that directory is gone, unpack `data/*.jsonl.gz`, `data/census.json` and `data/prices.json` into a directory named `out/` and set `SP` to its parent.

## 13. The published record

A literature scan of 2024 to 2026, one research agent with eight diggers: `.professor/RR/repo-properties-agent-cost-2026-11-13/repo-properties-agent-cost.md`.
The scan checked 13 of its own facts against 6 pages and confirmed 11; the 2 it could not find are not used here.
The quotes this section leans on most (rows 1, 2, 3, 4, 5 and 7) were fetched a second time by a separate agent, and all six are on their pages; the step counts in row 2 and the phase shares in row 4 came from the fetch tool's summary of the page, not from its verbatim text.
Every other number in the table rests on the scan alone.

Its headline: what has been measured is the interface an agent reads through, almost never the repository.
File length, cohesion, naming, duplication, one repo against several, generated code in the search path and a cap on search results have no controlled measurement with an agent.
No paper splits the reading before an edit into finding the place and understanding the code.
On those points §§ 6 and 7 of this report are, as far as the scan found, the only numbers; they are observations on recorded runs, not controlled experiments.

| # | Published finding | Source | This report | Reading |
|---|---|---|---|---|
| 1 | A file viewer of 100 lines resolved 18.0 % of tasks, the whole file 12.7 %, 30 lines 14.3 % | SWE-agent, arXiv 2405.15793 | builders ask for 40 to 61 lines at the median; reading whole files would cost 8.6 % more | Agrees on whole files; theirs is success, ours is cost. A 30-line window lost too, so the 60-line front-door block of the standard sits near the lower edge of what is known to work. |
| 2 | Orientation files (AGENTS.md): no general gain in success, inference cost up over 20 %, 2.45 to 3.92 more steps per task; machine-written files lowered success by 0.5 to 2 %, hand-written ones raised it 2.4 % (not significant) | Gloaguen et al., arXiv 2602.11988 | the start context is 10.5 % of dollars and grew from 19k to 44k tokens while navigation at fixed task size rose 1.12 times; rules and manuals are 18 % of the reading before the first edit | Agrees, and goes further than the replay: a 10k smaller start context is priced here with no change in requests, and the paper measured fewer steps without the file. A warning for the standard's front-door block: text written for the agent has a cost even when it is short, and in this fleet agents write it. |
| 3 | The most aggressive compression of tool output delivered 38.4 % fewer tool-output tokens and a 6.8 % higher bill; token reduction and cost correlate at 0.154 | "Token Reduction Is Not Cost Reduction", arXiv 2607.12161, against a Claude Code baseline | dollars follow requests (correlation 0.965); 3.4 cents of an Opus request are spent before any result arrives | Agrees with the mechanism and contradicts three simulator rows: the caps on search results, on reads and on read-backs assume the reader asks for nothing more. § 8 now gives the saving with and without them. |
| 4 | Share of rounds: explore 30.4 %, fix 33.5 %, validate 16.6 % (Sonnet 4.5, SWE-bench Verified); by action: search 15 % plus explore 14 %, write the fix 23 %, run tests 19 % | arXiv 2604.22750; Bouzenia and Pradel, arXiv 2506.18824 | navigation requests are 64 % of requests and 61 % of dollars; test requests 4.4 % of dollars | Disagrees by a factor of two, and the count is not the same: theirs is one phase of one bug fix in an open-source Python repo, ours every read and search of a build that edits a median of 4 directories, navigation between edits (33.6 % of dollars) and read-backs of logs included. |
| 5 | Before the first patch an agent spends 18.5 turns, 48 % of the interaction and over 320k tokens (55 models, 22 agent frameworks) | SHERLOC, arXiv 2606.24820 | navigation calls before the first edit: median 18, 15 and 10 in the three repos; everything before the first edit is 24.4 % of dollars | Agrees on the number of turns. The share is half theirs because a build goes on editing long after its first edit. |
| 6 | "Cache reads remain the dominant cost contributor in every phase"; a production agent spends 41 % of tokens on exploring, 35 % on editing, 19 % on planning, 5 % on verifying | arXiv 2604.22750; arXiv 2606.15579 | carrying the context at the cache-read price is 42.5 % of dollars | Agrees. |
| 7 | Success is stable for single-file problems and falls sharply as the file count of the task grows | SWE-Bench Pro, arXiv 2509.16941 | 2 to 8 directories edited: 1.25 times the navigation; one directory costs $0.32 per edit call against $0.47 to $0.51 | Agrees in direction with "one change, one directory". Theirs counts files and success, ours directories and dollars; the paper does not define its file count. |
| 8 | A change of edit format took one model from 20 % to 61 % on one benchmark; checking each edit with a linter was worth 3.0 points | Aider; SWE-agent, arXiv 2405.15793 | 2.4 % of edit calls fail; requests whose every call failed are 1.6 % of dollars (`results/a10.txt`) | The largest published factor is small in this harness today. |
| 9 | A structure-aware read tool is worth 5.2 to 7.8 points; a code graph 2.0 points, or a tie at more tokens; language-server tools from 26 % fewer to 118 % more tokens depending on the model (6 issues, preliminary) | CODESTRUCT, arXiv 2604.05407; RepoGraph, arXiv 2410.14684; CodexGraph, arXiv 2408.03910; arXiv 2608.13568 | not varied here | An earlier bench in this fleet agrees with the last: three builder runs given a brief with file and line anchors made no language-server call. |
| 10 | Renaming and other meaning-preserving rewrites: up to 6.7 points lower resolve rate; a test beside its source "dominates when capability is constrained", measured on code generation without an agent | arXiv 2608.18389; arXiv 2604.19826 | searches that return nothing are 1.4 to 2.3 %; test beside its source: 0.99, no effect at run level | Weak support for one searchable name per thing. On test placement the two measurements are of different things and neither settles it. |
| 11 | A cache write costs 1.25 times the input price for 5 minutes and 2 times for 1 hour; a read 0.1 times | Anthropic documentation | the prices fitted from billed requests (`data/prices.json`); every sub-agent cache write was billed at the 5-minute price | Same prices. |

What the scan changed:
- § 8 gained the two rows without the output caps, and line 12 of § 1 gives the range instead of one number.
- The standard's quiet-tools law (B2) shapes output at its source and never cuts what the reader needs; it moved from "adopt on accounting" to the bench.
- The standard's front-door law (A2) names row 2 as its main risk, and its bench arm has the blocks written by a building agent, as production would write them.
- § 7 gained failed calls under "already near zero", and § 11 the one-harness gap.

Left open by the scan: the token share per phase exists only in figures of two papers; no test of a search-result cap, of renaming alone with an agent, or of context length on coding agents was found; the repo-size success rates of SWE-bench-Live were not on the page it read.
