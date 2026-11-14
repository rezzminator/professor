# LLM codebase — The brief

What the main chat hands a foreman, measured on the recorded briefs. Source: `scripts/b1.py` over the first user message of every Claude foreman run in the three repos, 2 October to 13 November 2026; raw output `results/b1.txt` (both under the private root, `method.md` § Where things live).

## 1. The numbers

1,223 briefs with text, of 1,256 Claude foreman runs: `professor` 466, `monorepo` 606, `plugin-repo` 151.

Size:

| | 10th percentile | Median | 90th percentile |
| --- | ---: | ---: | ---: |
| Bytes | 331 | 3,492 | 6,145 |
| Lines | | 18 | 41 |
| File paths named | | 8 | |

One brief in ten is a pointer of a few hundred bytes to a brief file the foreman must open first.

Brief size does not follow task size:

| Edit calls in the run | Runs | Brief bytes, median | Paths, median | Briefs with a `file:line` | Navigation calls before the first edit, median | Dollars per run, median |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 1-5 | 373 | 3,194 | 7 | 46 % | 9 | $1.04 |
| 6-10 | 259 | 3,602 | 9 | 40 % | 14 | $2.40 |
| 11-20 | 206 | 3,647 | 8.5 | 35 % | 21 | $4.55 |
| 21-40 | 197 | 4,166 | 10 | 36 % | 26 | $8.98 |
| 41+ | 95 | 3,515 | 7 | 29 % | 29 | $15.28 |

What a brief contains, share of briefs:

| Content | All | `professor` | `monorepo` | `plugin-repo` |
| --- | ---: | ---: | ---: | ---: |
| An absolute path | 100 % | 100 % | 100 % | 100 % |
| A worktree path | 70 % | 78 % | 66 % | 65 % |
| A return shape (DONE, PARTIAL, what to report) | 65 % | 74 % | 62 % | 51 % |
| An out-of-scope or do-not-touch line | 62 % | 68 % | 53 % | 78 % |
| "God speed" | 53 % | 59 % | 49 % | 54 % |
| A ban on git writes | 50 % | 50 % | 51 % | 42 % |
| The testing manual, named | 47 % | 62 % | 33 % | 56 % |
| A test command | 45 % | 75 % | 19 % | 55 % |
| Numbered steps | 45 % | 40 % | 48 % | 42 % |
| An acceptance or done-means line | 42 % | 34 % | 47 % | 50 % |
| "Do it yourself", no spawning | 42 % | 40 % | 39 % | 58 % |
| At least one `file:line` | 38 % | 49 % | 31 % | 36 % |
| "Read X first" | 32 % | 35 % | 28 % | 35 % |
| A why, background or root cause | 30 % | 25 % | 32 % | 33 % |
| Other agents working beside it, named | 25 % | 26 % | 23 % | 33 % |
| A spec, brief, plan or handoff file to read | 24 % | 32 % | 19 % | 23 % |
| A watched-red order | 21 % | 46 % | 6 % | 7 % |
| The owner's words, quoted | 18 % | 13 % | 24 % | 15 % |
| A `TRACE` line from the tracer | 2 % | 2 % | 2 % | 0 % |
| A fenced block: quoted code or output | 1 % | 0 % | 2 % | 0 % |
| An effort tag | 0.1 % | 0 % | 0 % | 0.7 % |

Labels that open a line, with where they sit in the brief (0 = first line, 1 = last):

| Label | Share of briefs | Position |
| --- | ---: | ---: |
| God speed | 45 % | 0.00 |
| Goal | 29 % | 0.09 |
| Worktree / Where / Where to work | 16 % / 25 % / 5 % | 0.12 to 0.24 |
| Context / Why | 6 % / 3 % | 0.17 to 0.20 |
| Owns / Not yours | 6 % / 7 % | 0.35 to 0.42 |
| Testing manual | 9 % | 0.50 |
| Tests / Check | 6 % / 7 % | 0.67 to 0.74 |
| Rules / Standing rules / Boundary | 5 % / 5 % / 4 % | 0.72 to 0.75 |
| Acceptance | 27 % | 0.78 |
| Out of scope | 12 % | 0.82 |
| Budget | 2 % | 0.89 |
| Failure looks like | 18 % | 0.94 |
| Return | 29 % | 0.94 |

The shape settled over the six weeks: "God speed" opens 1 % of briefs in the first week and 76 % in the last; "Failure looks like" goes from 13 % to 39 %; the testing manual is named in 60 % of briefs in the first week, 34 % in the third and 59 % in the last. In the third week the median brief was 325 bytes: a pointer to a brief file.

## 2. The modal brief

The skeleton the recorded briefs converge on, in their order. Braces mark what the main chat fills in.

```text
God speed: the owner is away. Decide every fork yourself, never ask, return your rulings.

Goal: {the outcome, one or two sentences}. Return DONE or PARTIAL with {what to report}.

{Owner's words (bind): "{quote}"}

{Symptom | Context | Why}: {what was seen, where, when}
{Lead | What I found}: {the main chat's reading of the cause; 1 to 3 file paths, sometimes file:line}
{Evidence | Read first}: {paths of logs, transcripts, a seed or spec file}

Where: worktree {absolute path} (branch flight/{name}, base {sha}). Work only there; commit nothing.
{Siblings | Not yours}: {files other agents hold in the same worktree}

{Order | Steps}:
1. {step}
2. {step}

Tests: {testing manual path}; affected tests through {command}; pin the fix in {existing test}; watch it fail on the unfixed code. Not the full gate.

Out of scope: {neighbouring areas, git writes, files of others}
{Budget: about {N} tool calls}
Failure looks like: {the dead end to report instead of guessing}
```

Roughly the first half states the work and the second half states process: where to work, what not to touch, how to test, what to return.

## 3. The same shape, filled

Invented values throughout, for a request of the usual kind: "the quota figure in the statusline is wrong".

```text
God speed: the owner is away. Decide every fork yourself, never ask, return your rulings.

Goal: the statusline's quota segment must show the real remaining share after a weekly reset. Return DONE or PARTIAL with the root cause as file:line, the fix, and the test lines.

Symptom: after Monday's reset the segment still reads "7d 0 %" in two chats for about an hour, then corrects itself.
Lead (verify, do not trust): the segment is rendered in pfm/internal/statusline/quota.go; the per-chat cache may be serving the value from before the reset.
Evidence: the render inputs under ~/.local/state/pfm/statusline/; `pfm log` rows named statusline.warn.

Where: worktree {repo}/.worktrees/statusline-quota-reset (branch flight/statusline-quota-reset). Work only there; commit nothing.

Tests: the pfm testing manual, § Run commands; affected tests through .claude/scripts/test.sh pfm/internal/statusline; pin the fix in the existing quota test; watch it fail on the unfixed code. Not the full gate.

Out of scope: the segment's layout and colours, other segments, git writes.
Failure looks like: the stale value cannot be reproduced from the saved inputs; report what is missing.
```

## 4. A recorded brief of that kind, and what followed

Agent `a95776f99f3f696ba`, an Opus foreman in `professor`, 4 November 2026: a statusline defect (the count of nested agents in the sub-agent rows).

- Brief: 3,476 bytes, sections Goal, Symptom, Lead, Evidence, Where, Out of scope.
- The lead named the file and two `file:line` anchors, and a hypothesis of the cause.
- Run: 27 navigation calls before the first edit, 3 edit calls, 5 files in 1 directory, 50 requests, $2.67.

The path and the lines were in the brief; the 27 calls went to understanding the code around them. This is the general finding of `findings.md` § 6 in one run: a target in the brief cuts the hops to find it, and the reading to learn the contract remains.

Two sibling briefs of the same family, both Sonnet foremen with a fully specified fix: `a22028bcd69d0c859` (1,827 bytes, 4 navigation calls before the first edit, 3 edit calls, $0.24) and `a5e9dd3a66dc01c85` (2,508 bytes, 4 navigation calls, 4 edit calls, $0.30).

## 5. What the brief leaves out

Set against what a builder reads before its first edit (`findings.md` § 6):

| What the builder goes and reads | Share of its reading before the first edit | In the brief today |
| --- | ---: | --- |
| The contract its edit must agree with: callers, schemas, the target itself | 52 % | named by path (median 8 paths); quoted in 1 % of briefs |
| Rules: manuals, specs, the brief's own evidence files | 18 % | pointed at: the testing manual in 47 %, a spec or brief file in 24 % |
| The test home: test file, fixtures, how to run | 11 % | a test command in 45 % (19 % in `monorepo`) |
| A sibling to copy | 10 % | rarely named |
| Finding the place | 8 % | paths in nearly every brief |

## 6. Hypotheses, none benched

1. Quoting the contract lines in the brief removes part of the 52 % (the bench arm "brief quotes" of `standard.md` § 7).
2. The process half of the brief is the same text in most briefs and belongs in the foreman's own definition, paid once in the start context instead of written per dispatch.
3. A pointer brief costs at least one extra request before any work.
4. A brief that does not grow with the task leaves the large runs to find their own contract: navigation before the first edit rises from 9 to 29 calls while the brief stays near 3,500 bytes.
5. The test command belongs in every brief: it is in 75 % of `professor` briefs and 19 % of `monorepo` briefs, and testing manuals are the most-read files of two repos.

## 7. Gaps

- The dispatcher is not in the derived data, so a brief written by a lander or another foreman is not told apart from the main chat's.
- Labels are found by a line-start pattern; a brief written as prose carries the same content without the label, which is why the content table (pattern anywhere) reads higher than the label table.
- No brief was scored for correctness: whether its lead was right, or its paths the ones edited.
