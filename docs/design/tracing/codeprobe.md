# codeprobe

`codeprobe` is the design `collector` and `mapper` share. One script extracts code text and computes every list that can be computed. It then checks each fact row against its file and renders the return in the shape the caller checks. The agent picks the commands and writes the judgment rows. This file holds what the two agents have in common: [collector.md](collector.md) and [mapper.md](mapper.md) hold what differs. `tracer` reads in prose without the script: [tracer.md](tracer.md).

A change lands here first, then in the templates, then in every surface under [Surfaces that stay in sync](#surfaces-that-stay-in-sync).

## Contents

- [The family](#the-family)
- [What a caller needs](#what-a-caller-needs)
- [The script](#the-script)
- [Extraction verbs](#extraction-verbs)
- [Asks and directives](#asks-and-directives)
- [Rows and what verify checks](#rows-and-what-verify-checks)
- [The return](#the-return)
- [Mechanisms that replaced rules](#mechanisms-that-replaced-rules)
- [Not part of the design](#not-part-of-the-design)
- [Measuring a run](#measuring-a-run)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## The family

| Member | Kind | Does | Runs at |
| --- | --- | --- | --- |
| `collector` | agent | Turns numbered extraction orders into one `collect` plan; returns the manifest | `haiku`, effort `medium`, tools `Bash` |
| `mapper` | agent | The same loop over a fixed facet list for one target; returns the manifest | `sonnet`, effort `medium`, 35 calls |
| `codeprobe` | skill | `codeprobe.py`, Python 3 standard library, plus `SKILL.md`, the manual both read | — |

No agent spawns another. Vocabulary: an order is one extraction the caller numbered; an ask is one atomic question; a directive tells the script which list to compute for an ask; a row is one fact tied to a verified line; the anchor is that line; a census is every code line naming a name.

## What a caller needs

The callers are spec writers. A collector or a probe returns a manifest and the path of a file the script wrote; the caller reads that file, several in one message, so no fact passes through a model's retyping. What they ask a probe for, each as a closed list of `path:line` rows:

| Facet | The row |
| --- | --- |
| Change surface | `path:line`, the verbatim anchor, why it changes |
| Readers and writers | The complete list; a stored table also needs its keys and constraints |
| Tests | Every test and fixture that asserts, constructs, patches or imports the target |
| Check | The check command, a scoped variant, and whether two copies can run at once on one worktree |
| Mentions | Every line naming a thing the brief removes or renames |
| Reuse | The existing pattern to follow |
| Not answered | Each requested facet that got no row |

The same callers ask a collector for exact text: a signature, a struct, a line range, a table's columns. That text has to come back verbatim, with every order accounted for.

## The script

`python3 ~/.claude/skills/codeprobe/codeprobe.py {command}`. A failure prints `CODEPROBE FAILED — {reason}` to stderr and exits 2. State lives under `/tmp/{project}/codeprobe/{slug}-{HHMMSS}-{pid}/`, `{project}` the basename of the probed root with any leading dots stripped.

| Command | Does |
| --- | --- |
| `verbs` | Prints `SKILL.md`'s § Extraction verbs and § collect, the syntax's one source |
| `collect ROOT [DIR] --expect IDS` | A plan on stdin; writes the verbatim text to `DIR/return.md`; prints the manifest |
| `init ROOT [--map TARGET] [--expect IDS] [--budget WORDS]` | An ask plan on stdin (`= Q1a question`, then its commands); a caller number no ask carries is refused; runs each ask's extraction commands for the agent's reading only; lists files with `git ls-files -co --exclude-standard` (nested repos walked) and classes them CODE, TEST, DOC, GENERATED or DATA; prints the census excerpts and check lines the asks name, and a NOTE for an ask whose words call for a directive it lacks |
| `refs DIR NAME… [-C N] [--class C] [--path P]` | Every line naming NAME, with context, grouped by file |
| `absent DIR NAME [--in PATH]` | A word-match zero becomes an absence row `A#`; a hit prints the lines instead |
| `rows DIR [--replace]` | Rows on stdin appended, all rows verified, the return written, the manifest printed |
| `render DIR` | The return written again, the manifest printed |

DATA files are counted and never opened. That covers data extensions over 200 KB, any file under a `data/`, `seed/`, `snapshots/`, `dumps/`, `exports/` or `backups/` directory, and data and page files under a fixture directory.

## Extraction verbs

The verbs serve both a collector's orders and a mapper's extraction rows. Every line they print is the file's own text, prefixed with its line number by the script.

| Verb | Returns |
| --- | --- |
| `file PATH` | The whole file, up to 3000 lines |
| `lines PATH FROM TO` | A range; the header names any definition the range cuts through (`CUT: range ends inside Resolve (129-262)`) |
| `def PATH NAME…` | Each definition whole, with its doc comment and decorators; `Type.method` for a method; a name not in PATH is looked up in its directory, then the repo |
| `sig PATH NAME…` | Each signature only |
| `defs PATH [--public] [--containing REGEX]` | Every definition's signature; `--containing` keeps definitions whose body matches, the matching lines marked `*` |
| `block PATH REGEX [-n K]` | The block opening at the K-th match: YAML entry, SQL statement, Markdown section, Makefile target, brace or indent block |
| `grep REGEX [PATH…] [-C N] [-w] [-i] [-F]` | Every matching line; grep's BRE form (`a\|b`, `f(`) is read the way grep reads it |
| `consts PATH TYPE` | Every constant declared with that type, iota runs included |

A block's end comes from a character scanner: strings, comments, raw strings and triple quotes are blanked before brackets are counted, and indentation ends a Python or YAML block. A definition that does not close within 3000 lines is a MISS, never a guessed end.

## Asks and directives

An ask opens with `= ID the caller's question`; the lines under it are extraction verbs, printed for the agent's reading and never carried by the return, and directives, whose lists the return carries. IDs keep the caller's numbering: item 3's parts become `Q3a`, `Q3b`. An ask naming four or more things gets a NOTE to split it. `init --map TARGET` prepends the facet asks `M1`–`M8` (definition, writers, readers, timing, control, neighbours, tests, check), their directives set on every spelling of TARGET the repo holds.

| Directive | The return carries |
| --- | --- |
| `census NAME` | Every code line naming NAME, comments, docstrings and imports left out, then test lines grouped by test function (pointed to instead when the same name carries `tests`). When the ask is what each line does, a line no row sits within 3 lines of is printed `UNCLASSIFIED` and makes the ask PARTIAL |
| `tests NAME` | Every test and fixture line naming NAME, grouped by test function |
| `mentions NAME` | Every line in every file naming NAME |
| `checks` | No computed list: `init` prints the repo's check commands and the concurrency lines, and the ask is answered by rows |

NAME is word-matched, or `/regex/` when the word is too common. Each computed list is printed once, under the first ask carrying it; a later ask with the same directive says where it is.

## Rows and what verify checks

A row is `ASK | LOCATION | anchor | note`; the anchor is a piece of the line, 8 characters at least. LOCATION is `path:LINE`, `path:A-B` (at most 12 lines, printed verbatim, within one quoted line per 60 words of the budget), `path:*` (a whole file whose every line naming the target is one fact), an extraction verb whose text the return carries, `A#`, or `UNANSWERED` with what was read and what is missing.

| Field | Check | On failure |
| --- | --- | --- |
| Ask id | One of the asks | Rejected |
| Anchor | A contiguous piece of a line of the file, whitespace-normalised | Rejected, with the nearest line's text |
| Address | The line nearest the cited one that holds the anchor | Re-addressed |
| Path | Relative to ROOT; a path given from a sub-project resolves by unique suffix | Rejected |
| Note hedge | probably, likely, seems, might, maybe, actually… | Rejected |
| Note names | Every identifier shows within 40 lines of the anchor, in the enclosing definition, in another file this probe anchored, or as a file stem in the repo | Note withheld, row kept |
| Note numbers | A number with a leading zero shows within that window | Note withheld, row kept |
| Universal | absent, unused, never, none, only, outside quotes, with no `A#` and no fully classified census on the ask | Rendered `[unchecked]` |

A row sent again for the same ask and line supersedes the earlier one. A rejected row is listed in the return unless a later row for its ask and file was accepted.

## The return

The collector's return is the manifest: one line per order under the caller's number, the `@ path:FROM-TO` header of every block the file holds for it, each MISS with its reason, `END`. The verbatim text stays in `return.md`. An order or command a retry drops comes back as a MISS, and a third run into one DIR prints the same manifest again.

The probe return is `DIR/return.md`: a `PROBE` line with the counts, one `## ID — question · WITH ROWS | PARTIAL | NOT ANSWERED` block per ask with its rows grouped by file, `## NOT ANSWERED`, `## REJECTED ROWS`, `END`. An ask for text in full stays PARTIAL until an extraction or range row carries it. The agent's final message is the manifest: the counts and words against the budget, the file's path, each ask not answered in full, `END`.

## Mechanisms that replaced rules

| Rule that failed | Mechanism |
| --- | --- |
| "Copy the text verbatim" — typed copies differed from the file on 1–4% of lines, with invented lines | The script writes the text; the collector returns the manifest |
| "Never drop an order" — misses vanished on retry | Plan history: a dropped order or command returns as a MISS |
| "Keep the caller's numbering" — nine orders came back as eighteen | `--expect`: a plan whose order ids differ is refused |
| "List every caller" — the census missed test callers and template-literal uses | The census lists test lines; the word boundary admits `${` |
| "Answer every question" — sub-questions dropped silently | Atomic asks, each rendered with its status; NOT ANSWERED computed |
| "Say it flatly" | Hedged notes rejected |
| "Do not invent" — notes named methods and migrations the code does not show | Note names and numbers checked against the anchor's neighbourhood |
| "Your final message is the return" — a map came back as "full detail above", a render came back rewritten | The final message is a manifest naming the file the script wrote |
| "Be compact" — exploration greps and range quotes tripled the return | Plan commands never reach the return; a quota on quoted range lines; words reported against `--budget` |

## Not part of the design

- Readers spawned per file bucket. The lead was woken once per returning reader and re-sent its whole context each time; one agent reading excerpts costs less.
- A prose summary above the rows. Every invention the previous design kept publishing sat in its summary prose.
- A collector that retypes the text into its message.
- Mentions in the default map. They are asked for when a brief removes or renames something.

## Measuring a run

Read the usage before the findings: `tool_uses: 0` beside a claim is a fabricated return. Meter each run from its transcript: requests, tool calls, input, cache-write, cache-read and output tokens, at list rates. A collector return is checked mechanically: every numbered line against the file, every order echoed, no range gaps. A probe return is scored per atomic facet against a key an independent judge builds from the code. Benchmark a prompt revision under a fresh agent name, launched in a turn that starts after the write.

## Surfaces that stay in sync

- `templates/global/skills/codeprobe/{codeprobe.py,SKILL.md}`
- `templates/global/agents/{collector,mapper}.md`
- `docs/BLUEPRINT.md` — the cast line naming the three and the skill
- The Codex and OpenCode mirrors, by `pfm codex build` and `pfm opencode build`
