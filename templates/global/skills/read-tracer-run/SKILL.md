---
name: read-tracer-run
description: Before any follow-up question to tracer or writing a tracer NOT READ item as unsettled, and whenever tracer's answer ends `RUN {path}` or `MAP {path}` — drill a node's hops, paste exact lines into a task file, walk the component map down or up (`--from`/`--to`), pull it as JSON, or open it as a page on request. tracer → here.
---

# read-tracer-run

A tracer run is a directory, `{RUN}`, under `/tmp/{project}/tracer/`: one write-once `{ID}.hops` file per sub-tracer, and `map.txt`. Every command below re-reads each quoted line from the repository when you run it, so it prints the file's current text, never the tracer's memory of it. `tt` stands for `python3 ~/.claude/skills/tracetree/tracetree.py` — write it out in full in every call.

| Command | Gives |
| --- | --- |
| `tt check {RUN}` | each node's ANSWER lines, every cite checked against the verified hops |
| `tt check {RUN} --hops {ID}` | node `{ID}` and the children it split off, one line per hop: `path:line  the line as it is now` |
| `tt check {RUN} --full {ID}` | the same, with every line of each hop's quoted range numbered `NNN\|` |
| `tt map {RUN}`, or `{RUN}/map.txt` | the component map: per file, each function or section holding a verified hop — `name :line · hops · threads` — and its links |
| `tt map {RUN} --from NAME` | the tree of everything NAME reaches: what you must understand before changing it |
| `tt map {RUN} --to NAME` | the tree of everything that reaches NAME: what a change to its contract can break |
| `tt map {RUN} --json [--from NAME] [--to NAME]` | the map, or that part of it, as data: `components`, `edges`, and `threads` with each thread's `walk` |
| `tt map {RUN} --html OUT` | a page that draws the map and replays the run — only when the user asks to see it |

NAME is a component id (`path::name`), a function or section name, a file, or a piece of an id; a name matching nothing, or too many, exits 2 and says which. `--all` adds walk-order links.

## Reading the map

- `A → B`: B's name, in call shape, is in A's body, and the thread moved between them. `A ⇢ B`: A's body names B's file — a script run, an import, a doc pointer. `A ⇉ B`: A's node split the thread to a child that starts in B. `■ kind`: the thread leaves the code in A.
- `A · B`, with `--all`: two hops in a row and no name links them — walk order, not a dependency.
- The `MAP` line names the hub, the component the most threads cross: `--from` it for what it does, `--to` it for every way in. In `map.txt` the files with the most hops come first.
- A link is a name found in a body: strong, never proof — read the line before a change rests on it. A component, caller or link absent from the map was not traced; it is never evidence that none exists.

## Working a run

1. A final opening with `⚠` WARNINGS lines goes to the user in your next reply, copied whole: a sub-tracer placed, refused or rejected hops, or the code drifted, and an answer resting on those hops is drilled (`--hops {ID}`) before you use it. The final answers the brief; go to the run only for what it leaves open. Grep the map (`grep -n '::Render' {RUN}/map.txt`) or ask `--from`/`--to` rather than reading it whole; `--full` only for the node you copy from.
2. Lines a task file or a child's brief quotes come from `--full {ID}`: every line of the range, each `NNN|` prefix stripped, `path:A-B` on the line above the block.
3. A child gets the run, never a re-trace: its brief names `{RUN}` and the node IDs covering its unit, and `tt check {RUN} --hops {ID}` gives it the evidence in one call.
4. The reads a unit needs, as a list: `tt map {RUN} --json --from NAME | python3 -c 'import json,sys; [print(c["file"] + ":" + str(c["line"] or c["hops"][0])) for c in json.load(sys.stdin)["components"]]'`.
5. When the run's ROOT is the tree you are editing, `tt check {RUN} --hops` after an edit shows each traced line that moved (`re-addressed`) or changed since the walk (`≈ DRIFTED`).
6. Evidence: a verified hop's line is the file's text now; a hop's `=` clause and an ANSWER line are a reader's words, evidence only where their cites print unflagged; an `unbacked`, `?`, `REJECTED` or `MISSING` line is not evidence; a `≈ DRIFTED` hop or `≈` cite was true when walked — re-read its line before you rely on it.
7. Before opening a file, ask the run: `tt check {RUN} --hops | grep -n 'engine.go:10[1-3]'`. A verified hop there is the line as it is now, and `(+N lines)` the range it holds — evidence as it stands, never re-read to confirm it; `quote broke after 1 line(s)` holds that one line only. Read yourself only a block no hop holds, one `cat -n FILE | sed -n 'A,Bp'` each.
8. Each NOT READ item names its next step: `run: node {ID}` is `tt check {RUN} --hops {ID}` first; `ask: …` goes to tracer as written; a `path:line` marked `(grep only — read it)` no hop holds, so it is one block of your own read. A question the run does not settle — a LEAD, a NOT READ, a component absent from the map — goes back to tracer as a new numbered question before it goes into your output as unsettled; a fact the final already cites is never re-asked.
9. A run is scratch under `/tmp`: a tracked file cites the repository's `path:line`, never the run.
