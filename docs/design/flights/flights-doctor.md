# /flights:doctor

`/flights:doctor` is the meter. It measures how much code a flight wrote per token it read, then shows where the reading went. It believes transcripts and git, never a return or a recap. It finds and routes, and fixes nothing. User-only: a model never invokes it.

## Contents

- [Input](#input)
- [The measure, first](#the-measure-first)
- [Where the reading went](#where-the-reading-went)
- [The report](#the-report)
- [Rules](#rules)

## Input

- **Target:** a main-chat session id, or a foreman's agent id.
- **Optionally:** the worktree and its base sha, for landed lines.

The tree is walked from transcripts: each sub-agent's `.meta.json` `toolUseId` links it to the parent's Agent `tool_use`.

## The measure, first

`pfm tokens --tree {target} [--worktree DIR --base SHA]` prints, per agent:

- type and model;
- input, cache-read and cache-write tokens;
- bytes written (Edit and Write to files under the worktree or repo, `/tmp` excluded);
- bytes read (tool results);
- USD.

Per tree, it prints:

- landed code lines (`git diff --numstat BASE` of the worktree, with tests, docs, generated and lock files excluded);
- landed lines per million input tokens;
- the silent share: the input taken by agents that wrote no code.

The report opens with those three tree numbers and the written / read ratio per agent type.

## Where the reading went

Each row names its transcript `path:line`.

| Check | Finding |
| --- | --- |
| Tier fit | a foreman tier above or below the work's difficulty; a lander or reviewer tier off its size bar |
| Re-reads | a file read again by an agent after a tracer answer covering it reached that agent |
| Store hits | tracer questions answered fresh from the store, and questions walked again with unchanged files |
| Full suites outside the lander | any full suite or gate run by a foreman or worker |
| Idle gaps | a gap over 5 minutes, with who waited on what |
| Greedy foreman | a foreman that wrote pieces a worker could have taken: its written bytes against its workers' |
| Silent agents | agents that wrote nothing, ranked by input tokens |

## The report

- **Where it goes:** `/tmp/{project}/doctor/{target}.md`.
- **What it holds:** the measure, the checks above ranked by input tokens spent, and NOT READ for anything it failed to look at.
- **Holes:** an anchor it could not read is reported as a failure to look, never as an absence.

## Rules

- **Transcripts, not claims.** A return's claim is checked against the tool calls and results that back it.
- **Counted, not estimated.** Each number carries the method it was counted by.
- **Routed, not fixed.** A prompt defect goes to `pcm`; a code defect is named with its file.
