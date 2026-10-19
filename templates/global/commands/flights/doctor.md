---
name: flights:doctor
description: 'USER-ONLY — /flights:doctor [session or agent id] [--worktree DIR --base SHA]: measures a flight — landed lines per million input tokens, written / read per agent, the silent share — then where the reading went; routes findings, fixes nothing. flights-lander → main chat → here. Writes /tmp/{project}/doctor/{target}.md.'
argument-hint: '[session or agent id] [--worktree DIR --base SHA]'
disable-model-invocation: true
---

# Doctor — the meter

Measure what a flight wrote per token it read, then show where the reading went. Believe transcripts and git, never a return, a recap or your own earlier picture. You find and route; you fix nothing.

Input: $ARGUMENTS — a main-chat session id or a foreman's agent id, and for landed lines the worktree and its base sha; no id given: this chat's own session. The tree is walked from transcripts: each sub-agent's `.meta.json` `toolUseId` links it to its parent's Agent `tool_use`. Load the `chatlog` skill for the transcript verbs. `{project}` is the checkout's directory name minus any leading dot.

## 1 The measure, first

Run `pfm tokens --tree {target} [--worktree DIR --base SHA]`, then the same with `--json` for the rows you rank. Per agent it gives type, model, input, cache-read and cache-write tokens, bytes written (Edit and Write on files under the worktree or repo, `/tmp` excluded), bytes read (tool results) and USD; per tree, landed code lines (`git diff --numstat BASE`, tests, docs, generated and lock files excluded), landed lines per million input tokens, and the silent share (the input taken by agents that wrote no code). A failed run is the report's first finding, never a zero.

The report opens with those three tree numbers, then written / read per agent type.

## 2 Where the reading went

Each row names its transcript `path:line` (`pfm transcript show {id}`).

| Check | Finding |
| --- | --- |
| Tier fit | a foreman tier above or below its brief's difficulty (the fleet prompt's foreman rubric: difficulty and ambiguity, never size); a lander spawn off its inventory PLAN — a child added, dropped or re-cut |
| Re-reads | a file an agent read again after a tracer answer citing it reached that agent |
| Store hits | tracer questions answered `TRACE Q{n} · none` from the store, and questions walked again whose cited files were unchanged |
| Full suites outside the lander | any full suite or gate a foreman ran |
| Gate repeats | a lander full gate run again with no red gate between, or a red gate's fix checked by rerunning the gate instead of its failing tests |
| Idle gaps | a gap over 5 minutes between an agent's records, with who waited on what |
| Shared-file children | a child a foreman spawned for an area sharing a file with the foreman's own or a sibling's |
| Silent agents | agents that wrote nothing, ranked by input tokens |

## 3 The report

Write `/tmp/{project}/doctor/{target}.md`, overwritten each run: the measure, the checks ranked by the input tokens they cost, and a NOT READ line for every transcript, file or command you could not read. Route each finding: a prompt defect to `pcm`, a code defect named with its file. Your reply is the measure's three numbers, the top findings and the report path.

## Rules

- A return's claim is checked against the tool calls and results that back it.
- Each number names the method that counted it; nothing is estimated.
- An error is never reported as absence: "could not read the transcripts" and "no transcripts" are two findings.
