---
name: codeprobe
description: Internal kit of collector and mapper — codeprobe.py copies code verbatim, computes caller, test and mention lists, verifies quoted rows and renders their returns. Not invoked directly — ask collector or mapper.
---

# codeprobe

Run `python3 ~/.claude/skills/codeprobe/codeprobe.py {command}`, the path written in full in every command. Python 3 standard library; it reads the repo and writes only under `/tmp/{project}/codeprobe/`. A failure prints `CODEPROBE FAILED — {reason}` and exits 2. Data dumps and fixture records (data files over 200 KB or under a `data/`, `seed/`, `snapshots/`, `dumps/`, `exports/` or `backups/` directory; data and page files under a fixture directory) are counted and never opened.

## Extraction verbs

PATH is relative to ROOT; a directory PATH searches every file under it.

| Verb | Returns |
| --- | --- |
| `file PATH` | The whole file |
| `lines PATH FROM TO` | A range, TO may be `end`; the header names any definition the range cuts through |
| `def PATH NAME…` | Each definition whole, doc comment and decorators included; `Type.method` for a method; a NAME not in PATH is looked up in PATH's directory, then the repo |
| `sig PATH NAME…` | Each signature only |
| `defs PATH [--public] [--containing REGEX]` | Every definition's signature; `--containing` keeps the definitions whose body matches, matching lines marked `*` |
| `block PATH REGEX [-n K]` | The block opening at the K-th matching line: a YAML entry, a SQL statement, a Markdown section, a Makefile target, a brace or indent block |
| `grep REGEX [PATH…] [-C N] [-w] [-i] [-F]` | Every matching line; grep's `\|` form is read the way grep reads it; no match is a MISS |
| `consts PATH TYPE` | Every constant or variable declared with type TYPE under PATH, iota runs included |

"The columns of table T" is `block MIGRATION 'CREATE TABLE T'` plus `grep 'ALTER TABLE T' MIGRATIONS_DIR`; "the line an insert goes after" is `grep` on that line with `-C 3`.

## collect — the collector's command

```bash
python3 ~/.claude/skills/codeprobe/codeprobe.py collect ROOT --expect 1-2 <<'EOF'
= 1 the Runtime struct in server.go, whole
def pfm/internal/mcpserv/server.go Runtime
= 2 base.py lines 110-236, and the signature of parse_detail
lines src/pkg/adapters/base.py 110 236
sig src/pkg/adapters/base.py parse_detail
EOF
```

`= ID text` opens an order with the caller's own number and words; the lines under it are its commands, one per thing the order names. `--expect` carries the caller's order ids (`1-9`, `1,2,3a`); a plan whose ids differ is refused. The script writes the verbatim text to `DIR/return.md` and prints a manifest, `COLLECTED` to `END`: each order, the `@ path:FROM-TO` header of every block the file holds for it, and each MISS with the nearest names found. First run with a MISS: run `collect ROOT DIR --expect IDS` once more with the whole corrected plan; an order or command the retry drops returns as a MISS, and a third run prints the same manifest again.

## Probe commands — tracer and mapper

```bash
python3 ~/.claude/skills/codeprobe/codeprobe.py init ROOT --expect 1-3 --budget 2500 <<'EOF'
= Q1a the public functions and classes of robots.py, with signatures
defs src/pkg/fetch/robots.py --public
= Q1b what the robots fetch does on a 5xx
= Q2a every caller of RobotsCache, and what each does with it
census RobotsCache
= Q3a every test of robots.py, and what each asserts
defs tests/pkg/fetch/test_robots.py
tests RobotsCache
EOF
```

`= ID text` opens an ask: one fact, under the caller's own question number (question 1's parts become Q1a, Q1b), in its words. `--expect` carries the caller's question numbers; a number no ask carries is refused. `--budget` carries the brief's word limit; the manifest reports the return's words against it. The lines under an ask are its commands: an extraction verb (§ Extraction verbs), whose text `init` prints for your reading only, or a directive, whose list the return carries. The return carries your rows and the directives' lists, never the text init printed: a quote reaches the caller only as a row. `init --map TARGET` prepends the facet asks M1–M8, their directives on every spelling of TARGET the repo holds.

| Directive | The return carries |
| --- | --- |
| `census NAME…` | Every code line naming NAME, comments, docstrings and imports left out, then the test lines naming NAME grouped by the test function holding them. When the ask is what each line does, a line no row sits within 3 lines of is printed `UNCLASSIFIED` and makes the ask PARTIAL |
| `tests NAME…` | Every test and fixture line naming NAME, grouped by the test function holding it |
| `mentions NAME…` | Every line in every file naming NAME |
| `checks` | Nothing computed: `init` prints the repo's check commands and the lines deciding whether two copies can run at once; the ask is answered by rows |

NAME is a word, or `/regex/` when the word is too common to match alone: `/(FROM|INTO|UPDATE|JOIN|TABLE) listing\b/`.

| Command | Does |
| --- | --- |
| `init ROOT [--map TARGET] [--expect IDS]` | Asks on stdin; prints DIR, the file classes (CODE, TEST, DOC, GENERATED, DATA), the census excerpts (`REFS`), the test lines by test (`TESTS`), each command's text (`TEXT`) and the check lines |
| `refs DIR NAME… [-C N] [--class CODE,TEST] [--path PREFIX]` | Every line naming NAME with N lines of context, grouped by file |
| `absent DIR NAME [--in PATH]` | Word-matches NAME under PATH; a zero becomes an absence row `A#`, a hit prints the lines instead |
| `rows DIR` | Rows on stdin appended, every row verified, the return written, the manifest printed |
| `render DIR` | The return written again from the rows, the manifest printed |

A row is `ASK | LOCATION | a piece of the line | what it means`. LOCATION is one of:

- `path:LINE`: the anchor is a contiguous piece of that line — a name or a call on it, 10 to 40 characters — and the return prints the whole line; a drifted line is re-addressed to the nearest line holding the anchor
- `path:A-B`, at most 12 lines: the anchor is a piece of one of them, and the return carries the lines A to B verbatim — one row for a quote of several lines. The return holds one quoted range line per 60 words of `--budget` (2500 without one), 24 at least; a range past that is rejected
- an extraction verb (`sig|def|lines|block|defs PATH …`), anchor `-`: the return carries that verbatim text — for what the brief asks in full; an ask for text in full stays PARTIAL until an extraction or range row carries it
- `path:*`, anchor `-`: the whole file, for a file whose every line naming the target is one fact (a generated schema snapshot, a fixture list)
- `A#`, anchor `-`: an absence row made by `absent`
- `UNANSWERED`, anchor `-`: what was read and what is still not settled

`rows` rejects a row whose anchor the file does not hold, whose note hedges (probably, likely, seems, might, actually…), or whose ask id is unknown; each rejection prints its reason. A note naming an identifier or a number of two or more digits that the file does not show within 40 lines of the anchor — nor the ask, nor the enclosing definition, nor another file the probe anchored — is withheld, the row kept. A row sent again for the same ask and line supersedes the earlier one. A note saying something is absent, unused, never, none or only, outside quotes, without citing an `A#` and in an ask whose census is not fully classified, is rendered `[unchecked]`.

The return is `DIR/return.md`: a `PROBE` line with the counts; one `## ID — question · WITH ROWS|PARTIAL|NOT ANSWERED` block per ask holding its rows, its computed lists and the verbatim text of its commands; `## NOT ANSWERED`; `## REJECTED ROWS`; `END`. `rows` and `render` print the manifest — the counts, the file's path, each ask not answered in full, `END` — which is the agent's final message.
