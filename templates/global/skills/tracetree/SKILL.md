---
name: tracetree
description: Internal kit of tracer and sub-tracer — tracetree.py opens a trace run, stores each node's hops once, waits for the whole tree, checks every quote against the file and maps the components. Not invoked directly — ask tracer; a finished run → /read-tracer-run.
visibility:
  visible-to:
    include: [tracer, sub-tracer]
---

# tracetree

Run `python3 ~/.claude/skills/tracetree/tracetree.py {command}`, the path written in full in every command. Python 3 standard library; it reads the repo and writes only the run directory under `/tmp/{project}/tracer/`. A failure prints `TRACETREE FAILED — {reason}` and exits 2.

| Command | Who runs it | Does |
| --- | --- | --- |
| `new ROOT [--seek REGEX] [--questions]` | tracer | Creates the run directory, holding `ROOT` and, in a git tree, `BASE` (the HEAD commit), and prints its path; `--questions` stores stdin's numbered questions as `RUN/questions`; `--seek` then prints a `PRIOR` line for each verified hop of an earlier run on the same root, from the last 6 hours, whose path or line matches, re-read from disk and cut at 160 characters — at most 30, then a `PRIOR:` count naming the runs it could not read |
| `start RUN ID PATH:LINE [QUESTION]` | sub-tracer, first call | Registers node `ID` as running (`{ID}.started`, holding `PATH:LINE` and QUESTION for the map page), prints the stored text of a `Q{n}` QUESTION, then `PATH` from `LINE`−3 to `LINE`+40, numbered |
| `write RUN ID` | sub-tracer, once | Stores stdin as `{ID}.hops`, each `@` rewritten to its resolved address, beside `{ID}.write.json` (each hop's status and file hash at write); refuses an incomplete file unstored, refuses once unstored a file with a hop that does not hold — the file's own lines printed beside it — stores the second attempt as it is, and refuses any write after the store |
| `wait RUN ID… [--timeout S] [--grace S] [--start S]` | tracer | Blocks until the named nodes and every node that ran `start` have written, then prints the answers view of the named nodes and their children (COUNTS tallies the whole run), the `MAP` line and an `OPEN` block — each node's NOT READ and LEAD lines suffixed `run: node {ID}`, a `path:line` no hop holds marked `(grep only — read it)`, a line range (`path:A-B`, `path lines A-B`) verified hops hold marked `(held: node {ID} verified {path}:{lines})`; the full view goes to `RUN/answers.txt`, and past 28000 characters the printout keeps the flagged lines, OPEN and MAP whole and trims the answers; a named node not started after `--start` seconds (default 120) is reported `never started`, never waited for; a timeout names each node that started and never wrote |
| `read RUN FILE:A-B…` | tracer | Prints each block numbered by the file's lines; refuses a block over 30 lines or one a verified hop holds (naming its `--full ID`), and a second call before the next `wait` |
| `check RUN` | tracer | The answers view: each node's ANSWER lines, cites checked against the verified hops |
| `check RUN --hops [ID]` | tracer | Every hop of the run, or of `ID`'s subtree, one line each, re-read from disk |
| `check RUN --full [ID]` | tracer | Every hop with every line of its quoted range, numbered |
| `map RUN [--from NAME \| --to NAME] [--all] [--json \| --html OUT]` | tracer's caller | The component map from the verified hops (`tracemap.py`): a text declaration, the tree below (`--from`) or above (`--to`) a component, JSON, or on request a self-contained page (`tracemap_html.py`) that adds the questions, each node's checked answers, the run's clock and the traced files' text; `wait` writes the text map to `RUN/map.txt` and prints a `MAP` line |

## The check

Each hop's quote is re-read from its file: found at its address it is verified; found within 30 lines or elsewhere in the file it is re-addressed. A path that names no file from ROOT — `server.go`, `internal/x.go`, `pkg/x.go` for `app/internal/x.go`, `a/../x.go` — resolves to the one file under ROOT ending in it, else to the one file of that basename holding the quote near the line; a quote absent from its named file resolves to the one sibling file holding it within 2 lines of the cited line, else, for a line of 24 or more non-space characters, to the one file of the same extension holding that line and the only file under ROOT that does; each prints as its full path with `(path resolved from …: {how})`. A hop no step places is `REJECTED` — unless it held when walked: its node's write recorded it verified (`{ID}.write.json`), or the commit the run started from (`RUN/BASE`, else the reflog) holds it, and the file changed since; then it is `≈ DRIFTED`, counted apart. A git lookup that fails prints `DRIFT UNKNOWN` and leaves the hop REJECTED. The printout always shows the file's own text, never the node's.

In the answers view, a cite whose backticked text sits on another line of the cited file is moved there (`↻`); a line citing no verified hop and no quoted text is `unbacked`; a backticked text found in no cited file is `?`; a `SPLIT` child that started and wrote nothing is `MISSING`. A cite whose only hops drifted is `≈`. The last line is `COUNTS:`, the run's tally of nodes, hops, verified, moved, rejected, drifted, placed, followed, refused and flagged lines.

Every `wait` and `check` view opens with the WARNINGS block: a `⚠` header naming how many nodes broke the hop contract, then a `⚠` line per node that named paths the kit had to place away from the cited line, stored hops after a refusal or has REJECTED hops, a run line for drift, and — beside those — one for hops that followed a file moved since the walk, which breaks no contract. A placed hop whose quote sits at its very cited line only miscopied its path: it breaks no contract and prints a `· node {ID}: the kit corrected a miscopied path …` note after the block, or after `WARNINGS: none`. Flagged answers stay in COUNTS alone, so a clean walk prints the single line `WARNINGS: none`. The MAP line, the page's red block and the JSON's `warnings` carry the same lines, the JSON's `notes` and a quiet block on the page the notes; `write` warns its own node on stderr.
