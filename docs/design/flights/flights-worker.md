# flights-worker

A worker writes one piece a foreman defined, runs that piece's tests, and returns. It is the family's writer: Sonnet, reading only its own files, so most of the tree's code is written at the lowest price per token read.

## Tiers

| Agent | Model · effort | For |
| --- | --- | --- |
| `flights-worker` | sonnet · high | the files, the change and the acceptance are fully stated |
| `flights-worker-pro` | sonnet · xhigh | the piece needs debugging or design inside it, or code nobody has read |

Workers are Claude Sonnet on every engine's foremen: the GPT foremen spawn these same agents. They are visible only to `flights-foreman-mini`, `flights-foreman`, `flights-foreman-pro`, `gpt-flights-foreman` and `gpt-flights-foreman-pro`.

## Input

The brief, as the spawn prompt:

- the piece;
- its files;
- the exact change, or the problem for a worker-pro;
- the acceptance;
- the test command;
- the worktree;
- the interface it must fit;
- the tracer run paths that cover its area.

## Work

- **Reading:** it reads its files and the tracer answers it was given. Understanding beyond about 10 files or 2,000 lines it will not edit requires `tracer`, the only agent a worker may spawn. After a trace, it reads only the lines it edits inside the answered files.
- **Editing and testing:** it edits only its piece's files, and runs the brief's test command until green.
- **Commands:** they run in the foreground with a timeout. One longer than 10 minutes gets one blocking wait.
- **Decisions:** it decides what the brief leaves open and lists it in the return. It never asks.

## The return

One message:

- **First line:** `DONE {piece}`, or `PARTIAL {piece}: {why}`.
- **Changed files.**
- **Tests:** the test command and its result lines, quoted from what ran.
- **Decisions:** any it made.

## Never

- **Scope:** no edit outside its piece's files.
- **Spawning:** no child but a tracer.
- **Testing and landing:** no full suite, no commit.
- **Machinery:** no hooks, no files of its own.

## Declaration

- **Base:** `templates/global/agents/flights-worker.md`, sonnet at effort high.
- **Pro tier:** `flights-worker-pro.md` is a `from: flights-worker` declaration at effort xhigh.
- **Effort buttons:** the base carries `effortControl: on` and the `mcp__auto-effort-control__` effort buttons in its `tools:` line; the pro tier inherits them and narrows `effortControl` to floor high, ceiling xhigh ([flights.md](flights.md#effort-buttons)).
- **Tools:** Read, Write, Edit, Bash, Glob, Grep, Agent (tracer only), the sub-agent-compact tools and the three effort buttons.
