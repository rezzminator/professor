# tracetree

The kit under `tracer` and `sub-tracer`, in `templates/global/skills/tracetree/`: it opens a trace run, stores each node's hops once, waits for the whole tree, re-reads every quoted line from disk, places a hop whose path names no file, tells drifted code and moved files from a wrong quote, warns when a node broke the hop contract, and maps the components. The agents' rules are in [tracer.md](tracer.md); the measurements behind each mechanism are in [tracer-bench.md](tracer-bench.md).

`tt` below stands for `python3 ~/.claude/skills/tracetree/tracetree.py`. Python 3 standard library only; it reads the repository and writes only the run directory. A failure prints `TRACETREE FAILED — {reason}` to stderr and exits 2.

## Contents

- [Files](#files)
- [The run directory](#the-run-directory)
- [The hops file](#the-hops-file)
- [Commands](#commands)
- [The check](#the-check)
- [The resolver](#the-resolver)
- [Write-time refusal](#write-time-refusal)
- [Drift](#drift)
- [WARNINGS](#warnings)
- [The wait printout](#the-wait-printout)
- [The component map](#the-component-map)

## Files

| File | Holds |
| --- | --- |
| `tracetree.py` | The commands, the check, the resolver, drift, WARNINGS |
| `tracemap.py` | The component map, imported by `tracetree.py` |
| `tracemap_html.py` | The page writer, imported only for `map --html` |
| `tracemap.html` | The page template, with its title, warnings and `/*TRACEMAP_DATA*/null` data slots |
| `tracetree_test.py` | The spec, run by the fence's templates gate (`checks_templates_skill_tests` in `infra/fence/checks.sh`) |
| `SKILL.md` | The agents' command table and the check, visible to the tracer tiers and `sub-tracer` only |

## The run directory

`/tmp/{project}/tracer/{YYYYMMDD-HHMMSS}-{hex4}`. `{project}` is the repository's directory name minus any leading dot, taken from git's common directory, so a run from a worktree lands beside the main checkout's runs; outside git it is the root's own basename (`project_name`).

| File | Written by | Holds |
| --- | --- | --- |
| `ROOT` | `new` | The repository root, absolute |
| `BASE` | `new`, in a git work tree with a commit | The commit `HEAD` was at |
| `questions` | `new --questions` | The brief's numbered questions, as read from stdin |
| `{ID}.started` | `start` | `{PATH:LINE}<tab>{QUESTION}`, a line per `start`: the node is running; the map page reads its question and its time |
| `{ID}.refused` | `write`, at its refusal | Nothing: the node's first write was refused |
| `{ID}.hops` | `write`, once | The node's file, each placed hop's `@` rewritten to its real `path:line` |
| `{ID}.write.json` | `write`, with the store | One entry per hop: `given`, `path`, `line`, `end`, `how`, `status`, `sha256` |
| `answers.txt` | `wait` | The full wait view |
| `map.txt` | `wait` | The text map |
| `.reads` | `read` | The round's one read; `wait` removes it |

A node ID is dotted numbers, `3` or `3.1`. `.started` with no `.hops` is a node still running; a `.refused` node is still running until its second write.

## The hops file

```text
THREAD {ID}: {the card's THREAD}
ANSWER
- {one fact} [path:LINE]
@ path:LINE
> the line, copied whole
= what happens to the thread here
SPLIT {child ID}: {its thread} @ path:LINE
LEAD {a branch not followed, and why} @ path:LINE
END {returned|written|sent|executed|printed|dropped|unread}: {clause} @ path:LINE
NOT READ {what, and why}
```

Several `>` lines under one `@` quote consecutive lines; `@ path:A-B` addresses a range. `write` refuses, unstored, a file lacking a THREAD line, an ANSWER line, an `@ path:LINE` hop, or an END or SPLIT line.

## Commands

### new

**Function:** `cmd_new` — **Run by:** the lead, in its first call

`new ROOT [--seek REGEX] [--questions]` creates the run directory, writes `ROOT` and prints the run's path. `--questions` reads stdin to EOF and fails unless it finds a question numbered `1.`, `1)` or `Q1`; it stores the text as `questions`. In a git work tree with a commit it writes `BASE`; a git that cannot answer is named on stderr, never fatal.

`--seek` then lists every verified hop of an earlier run on the same ROOT, modified within the last 6 hours, running runs included, whose path or current line matches REGEX: `PRIOR {run} node {ID}: {path}:{line}  {text}`, newest run first, at most 30; a text past 160 characters is cut to 157 and ends `...`. The last line is `PRIOR: {n} hops in {k} runs — tt check {run} --hops {ID}`, naming any not shown and any run it could not read, or `PRIOR: none in {k} earlier runs on this root`, or `PRIOR: could not look — {reason}`.

### start

**Function:** `cmd_start` — **Run by:** each sub-tracer, as its first call

`start RUN ID PATH:LINE [QUESTION]` appends `{PATH:LINE}<tab>{QUESTION}` to `{ID}.started`. For a QUESTION of the form `Q{n}` it prints `QUESTION Q{n}: {text}` from `questions`, or names why it cannot. Then `started {ID} — {path}` and PATH from LINE−3 to LINE+40, numbered. A FROM path resolves by its tail like a hop's.

### write

**Function:** `cmd_write` — **Run by:** each sub-tracer, once

`write RUN ID < hops` checks the file's structure, then places every hop through [the resolver](#the-resolver) and refuses once a file with a hop that does not hold ([Write-time refusal](#write-time-refusal)). On storing it writes `{ID}.write.json` and `{ID}.hops` and prints `stored {ID}: {n} hops, {k} answer lines — {path}`, adding how many hops it re-addressed and how many it stored not holding. A write after the store fails: the file is final. Its stderr warnings are under [WARNINGS](#warnings).

### wait

**Function:** `cmd_wait` — **Run by:** the lead, as the last call of its spawn message

`wait RUN ID… [--timeout S] [--grace S] [--start S]`, defaults 540, 15 and 120 seconds. It removes `.reads`, then polls until every node it waits for has written and no `.hops` or `.started` file changed for `--grace` seconds. It waits for the named nodes and every node that ran `start`. A named node with neither `.started` nor `.hops` after `--start` seconds never ran and is reported `never started`, never waited for.

The first line is `WAIT done after {t}s: {n} files`, `WAIT TIMEOUT after {t}s — started but never wrote: {IDs}` or `WAIT ended after {t}s: no node started`, each ending `— never started: {IDs}` when one did not. The rest is [the wait printout](#the-wait-printout).

### read

**Function:** `cmd_read` — **Run by:** the lead

`read RUN FILE:A-B…` prints each block as `== FILE:A-B` then its lines, numbered by the file. One call a round: a second call before the next `wait` fails whole (`one read per round`). A block is refused on its own line, `REFUSED {block} — {why}`, when it is over 30 lines, not `FILE:A-B`, in no file under ROOT, outside the file, or held whole by one verified hop's quoted range, naming `tt check {RUN} --full {ID}`.

### check

**Function:** `cmd_check` — **Run by:** the lead and the caller

`check RUN` prints the answers view of every node; `--hops [ID]` every hop of the run, or of ID's subtree, one line each, re-read from disk; `--full [ID]` the same with every line of each hop's range numbered `NNN|`. Every view opens with the WARNINGS block and ends with COUNTS.

### map

**Function:** `cmd_map` — **Run by:** the caller

`map RUN [--from NAME | --to NAME] [--all] [--json | --html OUT]`: [the component map](#the-component-map).

## The check

Each hop's quote is re-read from the file at check time, in every view, so the printout shows the file's own text, never the node's. A quote line matches `exact`, else `loose` (whitespace collapsed), else as a `fragment` of at least 12 characters; a match short of exact is tagged. A pasted line-number prefix, wrapping backticks and one backslash escaping the indentation (`> \    def f():`) are dropped before matching.

| State | Printed | When |
| --- | --- | --- |
| verified | `{path}:{line}  {text}` | The quote's first line sits at the cited line |
| moved | `(re-addressed from :{n})` | Found within 30 lines of the cited line, else elsewhere in the file |
| placed | `(path resolved from {given}: {how})` | The resolver found the file; `{how}` is empty for a plain tail |
| followed | `(path resolved from {given}: followed a file moved since the walk)` | The resolver found the quote in another file, and the given path held it when walked ([Drift](#drift)) |
| partial | `quote broke after {n} line(s)` | A later quote line stopped matching; the hop holds the lines before it |
| DRIFTED | `≈ DRIFTED {path}:{line} — held when walked; the file changed since: re-read before pasting` | [Drift](#drift) |
| REJECTED | `✗ REJECTED {path}:{line} — {reason}` | No step places it: `no such file`, `quote not in the file`, `a directory, not a file` |
| BAD ADDRESS | `✗ BAD ADDRESS {path}:{line} — {reason}` | An unquoted hop past the file's end; a SPLIT, LEAD or END address in no file or past its end gets `(✗ BAD ADDRESS {path}:{line})` |

A `=` clause whose backticked names appear nowhere within 40 lines of its hop is marked `(? not near: {names})`. A SPLIT naming a child that started and wrote nothing is `✗ MISSING`; one naming a child that never started is `not spawned`. A node with neither END nor SPLIT gets `(no END line: where the thread ends is not stated)`.

The answers view checks each ANSWER line's cites against the verified hops. A cite is backed when a verified hop's range, give or take 3 lines, holds it, or its backticked text sits there.

| Flag | When |
| --- | --- |
| `↻ {path}:{a}→{b}` | The cite's backticked text sits on another line of the cited file; the cite moves there |
| `unbacked {path}:{n}` | No verified hop and no quoted text at the cite |
| `` ? `{text}` not in the cited file `` | A backticked text found in no cited file |
| `≈ {path}:{n}` | The cite's only hops drifted |
| `✗ no such file` | The cite names no file |
| `no [path:N] cite` | The line cites nothing |

A flagged line is not evidence. Each node's block ends `hops: {v} verified, {r} rejected`, adding `{d} drifted` when any did. The last line of every view is `COUNTS:`, the whole run's tally: `nodes`, `hops`, `verified`, `moved`, `rejected`, `drifted`, `placed`, `followed`, `refused`, `partial`, `unseen`, `answers`, `flagged`, `recited`, `missing`, `badaddr`, `noend` (nodes with neither END nor SPLIT), joined by ` · `.

## The resolver

**Function:** `place_hop`, `resolve_elsewhere`, `Files.find` — **Used by:** `write`, every `check` view, the answers view, `read`, `--seek`, the map

One resolver places a hop at write and at check alike, in this order:

1. The path as written, from ROOT.
2. Its tail: a file already read whose path ends in it, else a file one or two directories below ROOT at that relative path, else every file ending in it; of several, the one holding the quote, best match first, then nearest the line.
3. The path with `..` collapsed, by the same two steps.
4. Within the file found: the quote at the cited line, else within 30 lines, else anywhere in it (moved).
5. `basename and quote`: the one other file of the path's basename holding the quote within 30 lines; of several, the one holding it at the cited line.
6. `sibling file`, only when the path names a real file: the one other file of its directory holding the quote within 2 lines.
7. `unique line`: the one file under ROOT holding that whole line, at least 24 non-space characters long, with the same extension.

A hop no step places is REJECTED, unless it [drifted](#drift). The file list is `git ls-files --cached --others --exclude-standard`, else one walk of the tree, skipping `.git`, `node_modules`, `.worktrees`, `.venv` and `__pycache__`; the unique-line index skips binary files and files over 1 MiB.

Steps 5–7 exist because of the [Rejected-hop survey](tracer-bench.md#rejected-hop-survey): 49 hops named an invented directory, 6 a sibling file and 4 a line unique elsewhere, each with the quote exact.

## Write-time refusal

**Function:** `cmd_write`, `bad_hop`, `hop_hint`

`write` places every hop before storing. A hop does not hold when it is rejected, its address is past the file's end, or its quote breaks at a `...` or `…`. The first write holding one stores nothing, leaves `{ID}.refused` and exits 2. It prints `TRACETREE REFUSED — {b} of {n} hops do not hold; fix each, then write again (a second write stores the file as it is)`, then one line per bad hop, `@ {path}:{line} — {why}; {hint}`:

- `{why}` is the reason, `quote every line whole, no elision` for an elided quote, or `a hop cites a file line — name the file the listing points at` for a directory.
- `{hint}` is the file's own lines at the address, or the files bearing that basename, at most five, or `no file named {base} under ROOT`.

The second write stores the file as it is, the bad hops rejected at check time. The refusal is once only, because refusing for good would turn a node with one bad hop into a MISSING one. The refusal exists because the survey's 20 paraphrased or invented quotes, elisions, truncated lines and a directory cited as a file were caught only after their node was gone.

The stored `{ID}.write.json` holds one entry per hop, in order. `how` is `exact`, `tail`, `basename and quote`, `sibling file` or `unique line`, and null for a rejected hop. `status` is `verified`, `moved` or `rejected`, and `sha256` is the file's hash at write.

## Drift

**Function:** `drift_of`, `hop_incident`, `run_start`, `sidecar_entries`

A hop the check rejects but that held when its node walked it is `≈ DRIFTED`, counted in `drifted=`, apart from `rejected=`:

- With a sidecar: its entry says `verified` or `moved`, and the file's sha256 now differs or the file is gone.
- Without one, for an older run: the commit the run started from holds the hop's first quote line within 30 lines of the cited line, at the given or the resolved path; the line names the commit. That commit is `BASE`, else the reflog's `HEAD` when `ROOT` was written.

A git command that fails or takes over 10 s, or a reflog that begins after the run did, leaves the hop REJECTED. One `DRIFT UNKNOWN — {reason}; the rejected hops it could not look into stay REJECTED` line prints above COUNTS. A drifted hop builds nothing in the map, and an answer cite backed only by drifted hops is `≈`. `--seek` and `read` look only at verified hops and skip drift.

A hop the resolver places at check time in another file, whose given path held the quote when walked, is followed: the file moved since. With a sidecar, its entry was stored `verified` or `moved` and was no placement at write; without one, the commit the run started from holds the first quote line within 30 lines of the cited line at the given path. A start commit that lacks the path or the quote, a root outside git, and a commit git cannot name leave the hop placed. A followed hop keeps its `verified` or `moved` status at the new path, backs answers and the map, and counts in `followed=`.

Drift exists because 90 of the survey's 239 rejections were code that changed after the walk: a REJECTED that reads the same for "the tracer erred" and "the code moved" is a coincidence detector.

## WARNINGS

**Function:** `incidents`, `warnings_block`, `map_flags`

Every `check` view prints the WARNINGS block first; `wait` prints it right after its WAIT line. A node broke the hop contract when it has placed hops, a refused write stored with hops that still do not hold, or REJECTED hops (a count of one reads `1 hop`, `1 node`):

```text
⚠ WARNINGS — {b} of {n} nodes broke the hop contract; their answers rest on placed, refused or rejected hops: drill before trusting them
⚠ node {ID}: {k} hops named a path that is not the file — the kit placed them ({given} → {path}, …)
⚠ node {ID}: write refused once, then stored with {k} hops that still do not hold
⚠ node {ID}: {k} hops REJECTED
⚠ run: {k} hops DRIFTED — the code changed since the walk: re-read before pasting
⚠ run: {k} hops followed files moved since the walk ({given} → {path}, …)
```

- A hop counts as placed when the resolver placed it by `basename and quote`, `sibling file` or `unique line`, or when its given path is no tail of the file it landed in (a later hop on an invented directory reaches the file through the first), and its given path never held the quote. A plain tail (`server.go`, `..` collapsed) is no incident; a hop whose given path held the quote when walked is followed, not placed.
- A placed hop whose quote sits at the very line it cited only miscopied its path: the walker read the right lines and typed the directory wrong. It breaks no contract and prints a note after the block, or after `WARNINGS: none`: `· node {ID}: the kit corrected a miscopied path on {k} hops — each quote sits at its cited line ({given} → {path}, …)`. With a sidecar the entry was stored `verified` at its given line; without one the check-time resolver found the quote at the cited line. A placement at any other line keeps the `⚠` line. The user ruled this calibration after a run's two path slips read as untrustworthy evidence.
- The placed and miscopied lines show three `given → path` pairs, then `+{j} more`.
- A refusal the second write fixed is no incident.
- Drift prints its run line without counting a node as broken; when only drift occurs, the header reads `⚠ WARNINGS — no node broke the hop contract`. The followed line prints only beside other lines: followed hops alone leave `WARNINGS: none`.
- Flagged answer lines stay in the answers view and in COUNTS' `flagged=`, never in WARNINGS, so a clean walk never warns.
- A clean run prints the single line `WARNINGS: none`. A sidecar that cannot be used, with nothing else found, prints `⚠ WARNINGS — none found, but the kit could not look: {reason}`.

The same findings ride every surface:

- COUNTS carries `placed=` (hops, miscopied ones included) and `refused=` (nodes).
- The `MAP` line and the first line of `map.txt` and `map` end `· ⚠ {b} nodes broke the hop contract` and `· ⚠ {k} drifted` when they apply.
- `map --json` carries `"warnings"`, the block's lines, `[]` when none, and `"notes"`, the `·` lines.
- `map --html` opens its ledger with a red block of the lines, ahead of the map, and a quiet block of the notes.
- `write` prints to stderr for its own node and exits 0:
  - `⚠ WARNING — {k} of your hops named a path that is not the file; the kit placed them: {pairs}. Copy paths as your read printed them.` A miscopy warns here too: the node that typed the path is the one to learn.
  - `⚠ WARNING — stored with {b} hops that do not hold; your tracer sees them REJECTED and is warned`

The lead's final opens with the lines, and the caller relays them to the user. The user ruled for this after the [engine trace](tracer-bench.md#engine-trace): placing an invented path silently hid a node that broke the contract.

## The wait printout

**Function:** `cmd_wait`, `check_view`, `open_block`, `ceiling_view`

In order:

1. The WAIT line.
2. WARNINGS.
3. The answers view of the named nodes and their children, nested by ID. Each node shows `[{ID}] THREAD …`, its ANSWER lines with their flags, its `→ SPLIT`, `■ END`, `· LEAD` and `· NOT READ` lines, and its `hops:` line.
4. COUNTS for the whole run, with any `DRIFT UNKNOWN` line above it.
5. The `MAP` line.
6. The OPEN block.

The OPEN block lists every NOT READ and LEAD line of the shown nodes, each suffixed `— run: node {ID}`, plus `(grep only — read it)` when a `path:line` or `path:A-B` in it is held by no verified hop of the run; a line range it names (`path:A-B`, `path lines A-B and C-D`) that verified hops of any node hold adds `(held: node {ID} verified {path}:{lines})`, one per node — a marker, never a drop; `OPEN: none` when empty.

The full view always goes to `answers.txt`; when it cannot be written, `ANSWERS FAILED — {reason}` precedes the full view. At 28000 characters or more the printout keeps, in order:

- the WAIT line;
- WARNINGS, whole;
- COUNTS;
- the flagged, MISSING and NOT READ lines, within 35% of the ceiling;
- OPEN, within 25%;
- MAP.

Then each node's lines, cut to an even share of what is left, each cut marked `+{k} lines: {RUN}/answers.txt`, and a last `FULL {RUN}/answers.txt` line.

Each part answers a probe ([Caller probes](tracer-bench.md#caller-probes)):

- Printing only the named nodes: probe 1's gap waits reprinted round 1, costing 2 extra lead calls and 44.6K characters.
- The OPEN block and its `grep only` mark: probe 3's leads handed on grep-only lines in 5 of 6 runs.
- The ceiling: probe 3's 30–36 KB printouts overflowed the inline result and cost a Read each.

## The component map

**Module:** `tracemap.py` (`build`, `text`, `tree`, `data`, `hub`) — **Page:** `tracemap_html.py`

`map` turns the verified hops into components and the links between them, for the model that called tracer. Rejected and drifted hops build nothing.

A component is the definition enclosing a verified hop's line, found by scanning up from the line with the language's definition patterns; outside any, it is the file itself. Its id is `path::name`.

- Go: a func, a method or a type.
- Python: a def or a class.
- JS and TS: a function, an arrow-function const, a class or a method.
- Shell: a function.
- Make: a target, in a `Makefile` or `*.mk`.
- Markdown: a section; a heading inside a fenced block is no section.

Two consecutive verified hops of one thread in different components are linked by what the files show:

| Link | Text | When |
| --- | --- | --- |
| call | `A → B` | B's name, in call shape, is in A's body (either way round) |
| names | `A ⇢ B` | B's file name is in A's body, the files differing |
| split | `A ⇉ B` | A's node split the thread to a child whose first verified hop is in B |
| end | `■ kind` | The thread leaves the code in A |
| then | `A · B` | No name links them: walk order only, shown with `--all` |

A Go package function is called bare in its own package and as `pkg.Name(` from another, so `cmd.Run()` is no call to `spawn.Run`. SPLIT and END addresses are not quote-checked: they attach to a component a verified hop built, else to the node's last one. An END is a mark on its component, never a shared sink node.

`wait` writes `map.txt`, made of:

- the header;
- the link key;
- THREADS, each thread's entry component;
- the files, most hops first, each component as `name :line · hops … · threads …` with its outgoing links and ends.

The `MAP` line reads `MAP {RUN}/map.txt — {n} components in {f} files, {c} calls, {m} file names, {s} splits, {e} ends · hub {id} ({t} threads, {h} hops)`. The hub is the component the most threads cross, ties going to more hops. A map that cannot be built prints `MAP FAILED — {reason}` instead. Heaviest-first files and the hub exist because probe 1's caller grepped a name-sorted `map.txt` once, where the function its plan turned on came after 30 lines of fixtures.

- NAME, for `--from` and `--to`, is an exact id, else a component name, else a file or its tail, else any id containing it, ignoring case. One matching nothing or more than 6 components exits 2 and says which.
- `--from NAME` prints the tree of everything NAME reaches, `--to NAME` everything that reaches it.
- `--json` prints `run`, `root`, `components` (each with its first and last line, `line` and `end`), `edges`, `threads` with each thread's `walk` (each step's `line` to `b`, its `status` — `verified`, `moved`, `placed` or `followed` — the hop's flag, and its `link` from the step before — `call`, `names`, `then`, `same` within one component, null on the first), and `warnings`; with `--from` or `--to`, only that part.
- `--html OUT` writes `tracemap.html` with the data inlined, every `<` escaped so no file text closes its script: one self-contained page that loads nothing from the network, written only when a person asks to see it.

### The page

Beside the map data the page carries what `page_data` reads from the run:

- the questions and `BASE`;
- per node: its question and FROM from `{ID}.started`, its answers checked as the answers view checks them, its broken hops, its placed and followed paths;
- the nodes that started and never wrote;
- the clock, from the run files' mtimes: `ROOT` for `new`, `{ID}.started`, `{ID}.hops`, `answers.txt` for `wait`;
- the text of every file a shown component sits in: whole, most hops first, up to 4 MB; past that, the lines within 8 of a hop.

It draws three parts:

- the ledger: the run, the warnings block, the evidence bar of how every hop holds, each question with its threads and their answers, every `[path:N]` a link to its line;
- the code wall: one column per traced file, drawn to scale from its own text and grouped by directory, each thread's hops marked and its route drawn between them, solid where a call or a name in the code links two hops or they share a component, dashed and fainter for walk order only (`then`); the key says routes are the walkers' reading of the code, nothing run; zooming in reads the code itself, Fold collapses the lines no hop touches;
- the clock: each node's bar from start to write; replay walks the threads in the order they ran.

Search runs over the file text. Keys: `/` search, space replay, `←` `→` hops, `[` `]` threads, `z` Fold, `f` fit, `+` `-` zoom, `esc` clear.
