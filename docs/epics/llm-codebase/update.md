# LLM codebase — Work

## State of work

Phase 1, the measurement, is finished: `findings.md` and the proposed `standard.md`. Phase 2 is open and worked with the owner one step at a time, starting at the top of the chain: the brief.

Position now: the census of recorded foreman briefs is done and the modal brief shape was shown to the owner on 2026-11-14 (`briefs.md` §§ 2 to 4). The owner names the next step.

Nothing of this epic is in `templates/**`, in a prompt, or in a rule yet. No bench has run.

Next steps, in order:

1. Take the owner's next step on the brief; record his ruling in `manifest.md` § Key Decisions and the result in `briefs.md`.
2. Likely follow-ups on the brief, each a script beside `b1.py`: split each brief into its work half and its process half by bytes; join each brief's paths to the files the run edited (were the paths the right ones); read the dispatcher from a fresh callmeter snapshot (main chat or another agent).
3. On the owner's go: the bench of `standard.md` § 7. State the dollar cap first; pick 6 recorded tasks per repo from runs of 6 to 20 edit calls; arms: front-door contract blocks, the brief that quotes, the narrow runner with quiet output, the first two together; two runs per arm and task.
4. After the bench: the laws that won go to `pcm` as the replacement of `templates/global/commands/design/codebase.md`; the brief levers go into the flights prompts and `templates/global/rules/flights-life-cycle.md`.
5. Measure again on new recorded runs against the baseline of `findings.md` § 3.

To rerun any number: `method.md` § Pipeline; a fresh callmeter snapshot is needed for `build.py`, the derived data is enough for every other script.

## Delivered

### The measurement (`findings.md`)

1,421 Claude builder runs, 113,441 requests, $6,586, 2 October to 13 November 2026, in three repos.

- Navigation calls per edit call: 3.98, 4.00 and 4.09; navigation requests take 61 % of builder dollars, edit requests 25.5 %, tests 4.4 %.
- Where the dollar goes: read results 28.8 %, earlier output carried forward 18.2 %, navigation commands and their thinking 12.6 %, search results 10.7 %, start context 10.5 %, writing the edits 10.4 %.
- By billing mechanism: re-reading held context 42.5 %, output 27.2 %, cache writes 22.1 %, cache misses 8.1 %.
- The cost unit is the request: 6.4 cents on Opus, 3.4 before any result; 93 % of requests carry one tool call.
- 18 factors ranked by dollars with evidence grades (§ 7); six believed factors show no effect (file size, test beside source, path depth, share of tests, line anchors, repeated file names).
- The old standard judged by its own metrics (§ 10): its hop target is met by 2 to 6 % of runs; "one change, one directory" is the one claim that holds.
- The published record checked row by row (§ 13).

### The simulator (`findings.md` § 8)

Replays every recorded run with one rule changed and re-prices it: the repo and brief levers together are 25 to 31 % cheaper, 34 to 40 % with two calls per navigation request and a start context 10,000 tokens smaller.

### The proposed standard (`standard.md`)

Version 2, a proposal: six laws for fewer navigation requests, five for lighter requests, four for the brief and the work tree, three levers the tree cannot pull; each graded measured, seen in transcripts, or unproven; a bench (§ 7); seven open rulings (§ 8); what was dropped from version 1 and why (§ 5).

### The brief study (`briefs.md`)

1,223 recorded foreman briefs: median 3,492 bytes, 18 lines, 8 file paths; a `file:line` in 38 %; quoted code or output in 1 %; size flat across task sizes. The modal shape, a filled example, one recorded statusline brief with its run ($2.67, 27 navigation calls before 3 edit calls), and five unbenched hypotheses.

### The literature scan

`.professor/RR/repo-properties-agent-cost-2026-11-13/repo-properties-agent-cost.md`: 2024 to 2026; what is published measures the interface an agent reads through, almost never the repository; six of its quotes re-fetched and found on their pages.
