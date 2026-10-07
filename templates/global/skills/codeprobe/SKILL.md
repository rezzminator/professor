---
name: codeprobe
description: Internal kit of collector — codeprobe.py copies code verbatim for numbered orders and prints their manifest. Not invoked directly — ask collector.
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
| `grep REGEX [PATH…] [-C N] [-w] [-i] [-F]` | Every matching line; grep BRE forms `\|`, `\<`, `\>` and `\{n,m\}` are read the way grep reads them; a MISS names the pattern's reading |
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
