---
name: tracer:use
description: 'MANDATORY for a tracer caller — before sending tracer questions, and on an answer ending `RUN {path}` or `MAP {path}`: when to trace, question shapes, drilling a node, exact lines for a brief, the map `--from`/`--to`, JSON, a page on request, a run handed to a child. tracer → here.'
---

# tracer:use

`tracer` answers numbered questions from code: Haiku readers walk each thread hop by hop and its kit re-reads every quoted line from disk, at a third of what your own reading costs. Its answer ends with a run, `{RUN}`, a directory under `/tmp/{project}/tracer/` holding one write-once `{ID}.hops` file per reader and `map.txt`. Every command below re-reads each quoted line from the repository when you run it, so it prints the file's current text, never the tracer's memory of it. `tt` stands for `python3 $HOME/.claude/skills/tracetree/tracetree.py` — write it out in full in every call, `$HOME` and never `~`, which a quoted string or variable leaves unexpanded.

## Sending

1. Code you will change, judge or brief on goes to tracer before you read its bodies, at intake or any later step, however small the change. A brief carrying `TRACE {RUN} · nodes {IDs}` hands you a run: drill those nodes (`tt check {RUN} --hops {ID}`) before you trace more.
2. Ask in the request's own terms: tracer finds the code. An overview, when you take one, is `ls` and `grep -n` on names — never a function body.
3. One spawn, `subagent_type: "tracer"`: `Repo root: {path}`, then numbered questions, one fact each, in the terms of what you will do with the answer — where each change lands, with the function you will change quoted whole, so `--full` is your read of it; every caller or reader of what it changes and what each does with the new, absent or failing value; the test and fixture a new case copies. Ask for facts; you judge.
   - ✓ `1. Where does install replace an entry under ~/.claude/skills/, and what happens to a real directory already there? 2. Which test pins that replacement, and which fixture does it build?`
   - ✗ `How does the installer work?` — a survey: every thread, none deep.
   - ✗ `Every place that writes ~/.claude/`, when your change must hold at each — a closed-world list is your own chained `grep`; a run is evidence of what it walked, never proof that nothing else exists.
4. While it walks, do the work its questions leave untouched — the testing manual, a base check, a doc; a file it is walking, read now, is paid for twice.

## The run

| Command | Gives |
| --- | --- |
| `tt check {RUN}` | each node's ANSWER lines, every cite checked against the verified hops |
| `tt check {RUN} --hops {ID}` | node `{ID}` and the children it split off, one line per hop: `path:line  the line as it is now` |
| `tt check {RUN} --full {ID}` | the same, with every line of each hop's quoted range numbered `NNN\|` |
| `tt show {RUN} {file}:{A}-{B}…` | the code as the file holds it now, numbered: each line a verified hop holds marked `[{IDs}]`, the holding nodes' NOT READ and LEAD lines under the block — your read of code once the run exists |
| `tt map {RUN}`, or `{RUN}/map.txt` | the component map: per file, each function or section holding a verified hop — `name :line · hops · threads` — and its links |
| `tt map {RUN} --from NAME` | the tree of everything NAME reaches: what you must understand before changing it |
| `tt map {RUN} --to NAME` | the tree of everything that reaches NAME: what a change to its contract can break |
| `tt map {RUN} --json [--from NAME] [--to NAME]` | the map, or that part of it, as data: `components`, `edges`, and `threads` with each thread's `walk` |
| `tt map {RUN} --html OUT` | a page that draws the map and replays the run — only when the user asks to see it |

NAME is a component id (`path::name`), a function or section name, a file, or a piece of an id; a name matching nothing, or too many, exits 2 and says which. `--all` adds walk-order links.

- `A → B`: B's name, in call shape, is in A's body, and the thread moved between them. `A ⇢ B`: A's body names B's file — a script run, an import, a doc pointer. `A ⇉ B`: A's node split the thread to a child that starts in B. `■ kind`: the thread leaves the code in A. `A · B`, with `--all`: two hops in a row and no name links them — walk order, not a dependency.
- The `MAP` line names the hub, the component the most threads cross: `--from` it for what it does, `--to` it for every way in. In `map.txt` the files with the most hops come first.
- A link is a name found in a body: strong, never proof — read the line before a change rests on it. A component, caller or link absent from the map was not traced; it is never evidence that none exists.

## Working a run

1. `⚠` WARNINGS lines at the top of the answer go into your next output whole — your reply to the user, or your return. Every answer line of a node that broke the hop contract ends `⚠ node {ID} broke the hop contract`: it is unchecked until `tt check {RUN} --hops {ID}` shows its lines, and never cited as checked before.
2. The answer answers the questions; go to the run for what it leaves open. Grep the map (`grep -n '::Render' {RUN}/map.txt`) or ask `--from`/`--to` rather than reading it whole; `--full` only for the node you copy from.
3. Read code through the run: `tt show {RUN} {file}:{A}-{B}`, every block of a step in one call, in place of `sed -n` or `cat`. A line marked `[{ID}]` is a verified hop of node `{ID}` — evidence as it stands, never re-read to confirm it; the block's `held N/M` says how much of it the run already held; after your edits the marks follow the quoted lines.
4. The reads a unit needs, as a list: `tt map {RUN} --json --from NAME | python3 -c 'import json,sys; [print(c["file"] + ":" + str(c["line"] or c["hops"][0])) for c in json.load(sys.stdin)["components"]]'`.
5. Lines and commands a task file or a child's brief quotes come from `--full {ID}`, never from the answer as its notification shows it — the notification escapes `<`, `>` and `&` as `&lt;`, `&gt;`, `&amp;`: every line of the range, each `NNN|` prefix stripped, `path:A-B` on the line above the block.
6. A child gets the run, never a re-trace: its brief carries `TRACE {RUN} · nodes {IDs}`, the node IDs covering its unit, and `tt check {RUN} --hops {ID}` gives it the evidence in one call.
7. Each NOT READ item names its next step: `run: node {ID}` is `tt check {RUN} --hops {ID}` first; `ask: …` goes to tracer as written; a `path:line` marked `(grep only — read it)` no hop holds, so it is one `tt show` block; a range marked `(held: node {ID} verified {path}:{lines})` is held there, so `tt check {RUN} --full {ID}` before calling it unread. A question the run does not settle — a LEAD, a NOT READ, a component absent from the map — goes back to tracer as a new numbered question before it goes into your output as unsettled; a fact the answer already cites is never re-asked.
8. A hop address `{rev}:{path}:{line}` pins a git revision of ROOT: the line is `git -C ROOT show {rev}:./{path}` as it is, a commit never drifts, and nothing is checked out. When the run's ROOT is the tree you are editing, `tt check {RUN} --hops` after an edit shows each traced line that moved (`re-addressed`) or changed since the walk (`≈ DRIFTED`).
9. Evidence: a verified hop's line is the file's text now; a hop's `=` clause and an ANSWER line are a reader's words, evidence only where their cites print unflagged; an `unbacked`, `?`, `REJECTED` or `MISSING` line is not evidence; a `≈ DRIFTED` hop or `≈` cite was true when walked — re-read its line before you rely on it.
10. A run is scratch under `/tmp`: a tracked file cites the repository's `path:line`, never the run.
