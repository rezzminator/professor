# LLM codebase — Method

How every number in this epic is produced, and how to produce it again.

## Where things live

| What | Path | Tracked |
| --- | --- | --- |
| The epic: narrative, findings, standard, brief study | `docs/epics/llm-codebase/` | yes |
| The literature scan | `.professor/RR/repo-properties-agent-cost-2026-11-13/` | synthesis yes, digger reports ignored |
| Private root | `~/.local/state/pfm/flights/professor/llm-codebase/` | no |
| Adopter names and checkout paths | private root, `names.md` | no |
| Scripts | private root, `scripts/` | no |
| Raw output of every table | private root, `results/` | no |
| Derived data, gzipped JSON lines | private root, `data/` | no |
| The three transcript investigations | private root, `agents/` | no |

The private root holds adopter names, machine paths and raw brief text, so nothing in it is copied into a tracked file. A real brief is cited here by agent id and printed with `python3 scripts/show_brief.py {agent id}` from the private root.

## Definitions

- builder: a sub-agent whose type matches foreman, executor or worker; a lander is counted apart;
- request: one call to the model, billed once; it may carry several tool calls;
- navigation call: a tool call classed read or search (a `grep` chained with a `sed -n` slice is one navigation call);
- edit call: a call that authors file content, through the Edit and Write tools or through `sed -i` and Python heredocs in Bash;
- survey: everything before the first edit of a repo file; tail: everything after the last;
- start context: system prompt, tools, orientation files and the brief, as carried from the first request;
- brief: the first user message of a sub-agent's transcript.

## Pipeline

1. Snapshot the callmeter store (callmeter skill, § Open the store); never read the live store in an analysis.
2. `scripts/build.py` joins, per agent: the transcript (full commands, the brief), callmeter's per-request billing, and `callmeter classify` on each command; it reads the snapshot path set at its top (`DB`) and the three checkouts; it writes six JSON-lines files (agents, requests, calls, episodes, files, briefs).
3. `scripts/census.py` writes line counts and directory sizes of the three checkouts (`data/census.json`).
4. Each analysis script prints its tables; its raw output is kept under `results/` with the same name.

| Script | What it answers |
| --- | --- |
| `a1.py` to `a7.py` | ratios per repo and run size, where a dollar goes, request mechanics, cache misses, shape of reads, relation of reads to edited files |
| `a8.py` | regression with task size held fixed (needs numpy: `uv run --with numpy python a8.py`) |
| `a9.py` | the old standard's own metrics |
| `a10.py` | failed calls |
| `sim.py` | replay of every run with one rule changed, re-priced |
| `b1.py` | census of foreman briefs: size, labels, order, content features |
| `show.py`, `show_brief.py` | print one run, one brief |

`a1.py` to `a9.py` and `sim.py` read uncompressed copies from a directory named at their top (`SP`): unpack `data/*.jsonl.gz`, `data/census.json` and `data/prices.json` into a directory named `out/` and set `SP` to its parent. `a10.py`, `b1.py` and `show_brief.py` read the gzipped files in place.

## The accounting

Every request's billed dollars are split between what it carried. The context of each request is decomposed into the start context, the output of earlier requests and each tool result, labelled by the file it came from. A token is charged its cache-write price when it enters, the cache-read price at every later request that carries it, and the write price again on a cache miss. Checked out of sample: predicting a run's dollars from its context trajectory alone, on 711 runs the model was not fitted on, gives R² 0.973, total within 1 %, median error 6.3 %.

Evidence grades used across the epic: A = accounting on billed dollars; B = replay under a stated assumption; C = regression with task size held fixed; D = read in transcripts. A lever graded B or D is an assumption until a bench arm proves it.

## Traps

- A cap on output prices the tokens removed and assumes no extra request; published data says the extra request can cost more than the cap saves.
- The trace's edit detection missed a Python-heredoc write in 1 of 24 transcripts and counted an edit of an ignored env file in another.
- Skill loads are counted under "other results", not under reads.
- File line counts are today's; files since moved or deleted carry none.
- Model tier and start context are confounded with which work each agent type is given.
- The devbox login shell is zsh: run scripts with `python3` directly and gates through `bash -c`.
- The disk is near full: a store snapshot is about 2 GB and is deleted as soon as its tables are printed.
