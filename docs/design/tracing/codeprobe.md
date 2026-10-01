# codeprobe

`codeprobe` is the script under `collector`. It extracts code text verbatim into a file and prints a manifest of it in the shape the caller checks; the agent only turns the caller's orders into one plan. This file holds the script; [collector.md](collector.md) holds the agent. `tracer` reads in prose without the script: [tracer.md](tracer.md).

A change lands here first, then in the templates, then in every surface under [Surfaces that stay in sync](#surfaces-that-stay-in-sync).

## Contents

- [The family](#the-family)
- [What a caller needs](#what-a-caller-needs)
- [The script](#the-script)
- [Extraction verbs](#extraction-verbs)
- [The return](#the-return)
- [Mechanisms that replaced rules](#mechanisms-that-replaced-rules)
- [Not part of the design](#not-part-of-the-design)
- [Measuring a run](#measuring-a-run)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## The family

| Member | Kind | Does | Runs at |
| --- | --- | --- | --- |
| `collector` | agent | Turns numbered extraction orders into one `collect` plan; returns the manifest | `haiku`, effort `medium`, tools `Bash` |
| `codeprobe` | skill | `codeprobe.py`, Python 3 standard library, plus `SKILL.md`, the manual the collector reads | — |

Vocabulary: an order is one extraction the caller numbered; a plan is the orders with their verbs, sent on stdin.

## What a caller needs

The callers are spec writers. They ask a collector for exact text: a signature, a struct, a line range, a table's columns. That text has to come back verbatim, with every order accounted for. The collector returns a manifest and the path of a file the script wrote; the caller reads that file, several in one message, so no line passes through a model's retyping.

## The script

`python3 ~/.claude/skills/codeprobe/codeprobe.py {command}`. A failure prints `CODEPROBE FAILED — {reason}` to stderr and exits 2. State lives under `/tmp/{project}/codeprobe/{slug}-{HHMMSS}-{pid}/`, `{project}` the basename of the probed root with any leading dots stripped.

| Command | Does |
| --- | --- |
| `verbs` | Prints `SKILL.md`'s § Extraction verbs and § collect, the syntax's one source |
| `collect ROOT [DIR] --expect IDS` | A plan on stdin; writes the verbatim text to `DIR/return.md`; prints the manifest |

Files are listed with `git ls-files -co --exclude-standard`, nested repos walked. DATA files are counted and never opened. That covers data extensions over 200 KB, any file under a `data/`, `seed/`, `snapshots/`, `dumps/`, `exports/` or `backups/` directory, and data and page files under a fixture directory.

## Extraction verbs

Every line a verb prints is the file's own text, prefixed with its line number by the script.

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

## The return

The collector's return is the manifest: one line per order under the caller's number, the `@ path:FROM-TO` header of every block the file holds for it, each MISS with its reason, `END`. The verbatim text stays in `return.md`. An order or command a retry drops comes back as a MISS, and a third run into one DIR prints the same manifest again.

## Mechanisms that replaced rules

| Rule that failed | Mechanism |
| --- | --- |
| "Copy the text verbatim" — typed copies differed from the file on 1–4% of lines, with invented lines | The script writes the text; the collector returns the manifest |
| "Never drop an order" — misses vanished on retry | Plan history: a dropped order or command returns as a MISS |
| "Keep the caller's numbering" — nine orders came back as eighteen | `--expect`: a plan whose order ids differ is refused |
| "Your final message is the return" — a return came back rewritten | The final message is a manifest naming the file the script wrote |

## Not part of the design

- A collector that retypes the text into its message.

## Measuring a run

Read the usage before the findings: `tool_uses: 0` beside a claim is a fabricated return. Meter each run from its transcript: requests, tool calls, input, cache-write, cache-read and output tokens, at list rates. A collector return is checked mechanically: every numbered line against the file, every order echoed, no range gaps. Benchmark a prompt revision under a fresh agent name, launched in a turn that starts after the write.

## Surfaces that stay in sync

- `templates/global/skills/codeprobe/{codeprobe.py,SKILL.md}`
- `templates/global/agents/collector.md`
- `docs/BLUEPRINT.md` — the cast line naming the collector and the skill
- The Codex and OpenCode mirrors, by `pfm codex build` and `pfm opencode build`
