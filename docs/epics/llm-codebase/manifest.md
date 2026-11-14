---
epic: llm-codebase
status: IN_PROGRESS
created: 2026-11-13
updated: 2026-11-14
pipelines: []
flights: []
---

# LLM codebase: the cheapest repo, brief and harness for an LLM builder

## Vision & Scope

The owner's north star for the flights plugin: the ratio of reading to writing in builder agents, meaning how many reads and searches a builder makes before it reaches its target and writes, when and why it navigates, and what that costs in dollars.

The initiative runs for weeks to months and is worked step by step with the owner. It covers the whole chain of one change: the owner's request, the brief the main chat writes, the foreman that builds, its reads, edits and tests, the lander. It ends in a design for the repo structure, the brief and the tooling that is cheapest for LLM coding, judged on three things the owner named: token price in dollars, easy navigation, the read:write ratio.

Method, fixed: measure on recorded runs (callmeter and transcripts), state a hypothesis with its dollar size, prove it on a bench of replayed tasks, then ship it. Nothing is adopted on argument, and the old standard (`/design:codebase`) is evidence under test, never a premise.

Done means:

1. the standard that replaces `/design:codebase` holds only laws that won their bench arm;
2. the flights prompts, rules and brief shape carry the levers the repo cannot pull;
3. new recorded builder runs cost measurably less per change than the baseline of `findings.md` (replay floor: 25 %).

Repos measured, named by label in every tracked file:

| Label | What it is |
| --- | --- |
| `professor` | this repo: a Go engine with tests beside the source, plus prompts and templates |
| `monorepo` | an adopter: TypeScript and Python, six projects, separate test trees |
| `plugin-repo` | an adopter: Python, about 950 plug-in directories of one fixed shape |

The real names and checkout paths of the two adopters are in the private key, `~/.local/state/pfm/flights/professor/llm-codebase/names.md`.

## Key Decisions

Owner rulings:

- 2026-11-13: the read:write ratio of builder agents is what the flights plugin optimizes; the factors that count are token price in dollars, easy navigation and that ratio.
- 2026-11-13: "be skeptical towards it and don't let it bias you", said of `/design:codebase`.
- 2026-11-13: measure with callmeter and with agent-investigators over the three repos; find every factor.
- 2026-11-14: this is a long-running project; all knowledge, findings and progress live in this epic and are updated as work goes on.
- 2026-11-14: the work proceeds one step at a time with the owner, starting from the brief: how the main chat tells a foreman what the work is.

Decisions taken under the owner's "god speed", each reversible:

- A builder is a sub-agent whose type matches foreman, executor or worker; landers are counted apart; main chats are not measured. Flip: the type filter at the top of `build.py`.
- GPT runs are kept for comparison only; no number in this epic comes from them.
- The store is read through a snapshot, never live; the snapshot is deleted after each analysis.
- Read:write is a reported number, never the target; the targets are navigation requests per edit call, navigation before the first edit, cents per request and the cache-miss share (`standard.md` § 1).
- The standard is a proposal inside this epic until its bench is run; `templates/**` and `/design:codebase` are untouched.
- The quiet-tools law went from adopt-now to the bench after a published test showed less tool output can cost more; every replay total is given as a range.
- Tracked epic files carry no adopter name, no machine path and no raw brief or transcript text; that material stays in the private directory (`method.md` § Where things live).

## Progress Log

- 2026-11-13 — session: measured 1,421 Claude builder runs in three repos, ranked 18 factors, checked the published record, wrote the proposed standard (uncommitted at the time)
- 2026-11-14 — session: epic created; census of 1,223 foreman briefs and the modal brief shape (`briefs.md`)

## Discoveries

- The ratio is 4 navigation calls per edit call in three unlike repos; it measures the agent and the task size far more than the repo.
- The cost unit is the request: 3.4 cents of an Opus request are spent before any result arrives, so a saved request beats a saved kilobyte.
- Builders rarely hunt for the place (8 % of reading before the first edit); they read to learn the contract their edit must agree with (52 %).
- A path in the brief removes the hops to find a file and leaves the hops to understand it.
- A cap on tool output is not a saving until benched: published, 38.4 % fewer tool-output tokens came with a 6.8 % higher bill.
- Orientation text written for agents has a cost even when short (published: over 20 % more inference cost, no gain in success).
- Briefs name files and almost never quote them: a median of 8 paths per brief, a fenced block of code or output in 1 % of briefs.
- Brief size does not follow task size: about 3,200 to 4,200 bytes at the median from the smallest runs to the largest.
- The trace's first-edit position disagreed with the transcript in 5 of 24 runs read by hand; treat pre-edit numbers as good to a few calls.
- The derived data holds no parent id, so "who wrote this brief" (main chat or another agent) is not in it; the live callmeter store has it.
- The derived briefs are cut at 30,000 characters; none of the 1,223 foreman briefs reached the cut.

## Open Questions

Awaiting the owner:

1. The seven defaults of `standard.md` § 8 (directory ceiling, command budget, start-context budget, front-door block size, size ceilings, order of adoption, where the standard lives).
2. A go for the bench of `standard.md` § 7; it spends real money and its cap is stated before the first run.
3. Whether the standard replaces `/design:codebase` (through `pcm`) once its bench is in.
4. The next step after the brief shape of `briefs.md`.

## Files

- `update.md` — the state of work now, the ordered next steps, what is delivered
- `findings.md` — the measurement: where a builder dollar goes, the factors ranked, the simulator, the old standard judged, the published record
- `standard.md` — the proposed standard, version 2: laws with grades, the bench, the open rulings
- `briefs.md` — the brief study: what the main chat hands a foreman, the modal shape, what it leaves out
- `method.md` — how every number is produced: where things live, definitions, the pipeline, the rerun recipe, traps
- `.professor/RR/repo-properties-agent-cost-2026-11-13/repo-properties-agent-cost.md` — the literature scan (outside this directory)
