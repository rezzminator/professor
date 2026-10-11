# tracer

`tracer` answers a spec writer's numbered questions about a repository from its code, in prose: each fact a `path:line` with the line quoted, plus test homes, the check command and NOT READ. A `sonnet` lead looks wide once and cuts the questions into threads; `haiku` `sub-tracer` agents walk each thread hop by hop and split where it forks; the `tracetree` kit stores each node's hops once, re-reads every quoted line from disk and maps the components; the caller works the finished run with the `read-tracer-run` skill. Exact text without a question goes to `collector`.

The kit's commands, check states and run directory are in [tracetree.md](tracetree.md); every measurement a rule below cites is in [tracer-bench.md](tracer-bench.md).

## Contents

- [Parts](#parts)
- [Why a tree](#why-a-tree)
- [A run](#a-run)
- [Rules](#rules)
- [Harness behaviour](#harness-behaviour)
- [Model](#model)
- [Open items](#open-items)

## Parts

| Part | File | Model, effort |
| --- | --- | --- |
| Lead | `templates/global/agents/tracer.md` | `sonnet`, `high` |
| Reader | `templates/global/agents/sub-tracer.md` | `haiku`, `low` |
| Kit | `templates/global/skills/tracetree/`: `tracetree.py`, `tracemap.py`, `tracemap_html.py`, `tracemap.html`, `tracetree_test.py`, `SKILL.md` | — |
| Caller's skill | `templates/global/skills/read-tracer-run/SKILL.md` | — |

`sub-tracer` and the `tracetree` skill are visible only to `tracer` and `sub-tracer`. In the prompts, `tt` stands for `python3 ~/.claude/skills/tracetree/tracetree.py`.

## Why a tree

Every call re-sends an agent's whole context, so a single agent pays for the code it holds at its own model's price: on bench brief B1, $0.181 of the single-agent body's $0.344 was context written to cache and $0.126 its output. A sub-tracer reads the same code on Haiku 5.5, about a twentieth of Sonnet 5.5's price per token, and hands the lead a checked answer of at most 15 lines instead of the code. The lead stays 60–80% of a run's cost.

The tree pays where a brief makes one agent carry much code: on B4 it cost 29% less at an equal answer, and on five replayed heavy briefs it cost $2.99 against the $10.75 callers had paid, winning three of five blind judgments. On a short brief it ties the single agent on value at a higher cost and 2–3 times the latency ([Tree bench](tracer-bench.md#tree-bench), [Replay bench](tracer-bench.md#replay-bench)).

## A run

1. The lead's one Bash call: `tt new {root} --seek '{names}' --questions` creates the run directory `/tmp/{project}/tracer/{stamp}`, stores the brief's numbered questions and prints the `PRIOR` hops earlier runs on this root verified; the same call greps every symbol, file and string the brief names.
2. The lead cuts threads, each with an ID, FUEL and a card, and spawns every one as a `sub-tracer` in one message whose last call is `tt wait {RUN} {IDs}`.
3. Each sub-tracer's first call is `tt start`, which registers it and prints its question and FROM window. It walks hop by hop, spawns children `{ID}.1`, `{ID}.2`… where the thread forks into another file and fuel remains, stores its hops once through `tt write`, and returns one line.
4. `tt wait` returns once every named and every started node has written and the run has been quiet 15 s. It prints the WARNINGS block, the answers view of the named nodes and their children, COUNTS, the `MAP` line and the OPEN block.
5. After each wait the lead drills at most twice (`tt check --hops {ID}`, a node once), runs at most one gap round, reads what stays open through `tt read`, and writes the final.
6. The final: the WARNINGS lines, if any; the answers by the brief's question numbers; `CHECK`, `TESTS`, `NOT READ`; the last wait's `MAP` line; `RUN {RUN} · {COUNTS}` with a runnable `check --hops | grep` command.
7. The caller relays the WARNINGS lines to the user and, with `read-tracer-run`, asks the run before it asks tracer again or writes a NOT READ item as unsettled.

## Rules

Each rule holds in the prompts or the kit today; the clause after it is why.

### The lead

- Read-only: it never edits and never runs the project's code, tests, package manager or network; the run directory, written through `tt`, is the one thing written. The single-agent body once ran a `uv run` that created a `.venv`.
- `tt` is written out in full in every call, never put in a shell variable: inside quotes `~` never expands, and probe 1 lost a call to `can't open file` ([Probe 1](tracer-bench.md#probe-1)).
- Exactly one Bash call before spawning, holding `tt new` and the greps, each numbered as the file numbers it; the second call spawns. Code the lead reads costs twenty times a sub-tracer's price and rides every later call: one lead delegated nothing on B3, and a `medium` lead made four reads of its own and wrote 13 cites wrong ([Tree bench](tracer-bench.md#tree-bench)).
- A question the `PRIOR` lines answer gets a drill of that node, never a thread. Probe 3's follow-up calls re-asked 14 threads' worth of held facts, $0.63, 26% of agent spend ([Probe 3](tracer-bench.md#probe-3)).
- Every question gets a thread in the first message, and one thread always serves test homes and the check command. FUEL is 0 for a thread that will not branch and 3–6 for a chain that forks, at most 16 a round: the cap limits forks, never questions. Probe 2's 11 questions got 7 threads and the rest a second wave 83 s later ([Probe 2](tracer-bench.md#probe-2)).
- A question asking for quotes and for behaviour is two threads: a thread asked for both returns part of the quote and leaves the lead to read the rest at its own price.
- The card is RUN, ROOT, ID, FUEL, `QUESTION: Q{n}`, THREAD and FROM; `tt start` prints the question's text. Probe 3's cards repeating 350–500-character questions cost 4.5–7.6K output tokens and 28–96 s per lead.
- Every spawn and `tt wait` go in one message, the Bash timeout at 600000; a sub-tracer's completion notification needs no reply. A wait the Bash tool moves to the background gets exactly one more `tt wait` at timeout 600000; still not back, its IDs go under NOT READ as `run: node {ID}`. In the field one such wait cost ten calls over eleven minutes: a re-wait at 120000, `pkill` twice, a `sleep` loop. A lead counting wakes answered one of two completions 13 s apart and its final was lost ([Tree bench](tracer-bench.md#tree-bench), v3).
- At most two drills after each wait, one ID each, a node drilled once: probe 1's `for` loop over four `--hops` IDs poured 24.5K characters into every later call. The field audit of 183 leads found 23 drilling more than twice; the third and fourth drills supplied 8–28 cites each, and the cap reads one way in every prompt line. `--hops` without an ID is the caller's grep form only (one caller dumped 27.3K characters into its context), and a trimmed wait printout is drilled by node, never re-read whole from `answers.txt` (two leads, five calls and about 58K characters).
- A brief that compares git revisions is one run: one `tt new`, a thread per revision, its FROM `{rev}:{path}:{line}`, read with `git -C {root} show` and `grep`, never a checkout. The kit verifies a pinned hop against `git show {rev}:{path}` and never drifts it. Without it a lead built three trees by hand and read with unnumbered `sed`.
- A node holding an OPEN item is drilled before the item is handed on. Probe 3 handed on 12 `run: node` items with 0 of 12 drills spent, and 3 came back as follow-up threads.
- A spawn the harness refuses never started: it goes into the gap round after the wait, never replaced by the lead's own reading. One lead whose seven spawns were all refused read 13 whole files itself (context 29.8K to 95.7K) and ran no gap round; another respawned its seven refused threads and all ran.
- At most one gap round, for what is still open, from a narrower FROM; a thin answer is drilled, never respawned however its THREAD is reworded. Probe 1's gap round respawned three threads for $0.17.
- The lead's own reads serve open sub-asks only: one `tt read` call a round, blocks of at most 30 lines that no hop holds; an OPEN line marked `(grep only — read it)` is read, never handed on; a pasted block comes from `tt check --full`. A refused block is split into blocks of at most 30 lines in the same call, never bypassed: a lead that bypassed a refusal with `sed -n 39,122p` had 9 of its 20 cites land a line or two off. Probe 2's lead read 636 lines in three calls and still handed on the 20 lines the caller needed; in probe 3 the same bounds, as prose, failed in 4 of 6 leads, so the kit enforces them.
- Every read is numbered by the file's own lines, `cat -n FILE | sed -n 'A,Bp'` or `grep -n -A`: on replay P20 a `sed -n | cat -n` read numbered from 1 and six cites landed 19 lines off ([Replay bench](tracer-bench.md#replay-bench)).
- The lead runs at effort `high`: a `medium` lead dropped facts while writing the final, 23/33 on B1 and 14/21 on B3 against 30.5 and 18.

### The sub-tracer

- Its first call is `tt start`, and a SPLIT line names only a child it spawned. SPLIT lines naming children never spawned held the wait to its 540 s timeout (v5).
- It reads only the code that decides where the thread goes, numbered by the file, and follows only what THREAD names. An error branch is a hop, and the walk ends in one END: returned, written, sent, executed, printed, dropped or unread.
- A child spawn the harness refuses is a LEAD, never a SPLIT and never walked by the node: the lead's gap round picks it up.
- Where the next hop is a function in another file and FUEL remains, it spawns the children of one split in one message, k children costing k fuel, keeps walking its own branch and never waits for a child; its own walk stops at 40 hops. Nodes without the file-boundary split and the cap spawned no grandchild and walked about 70 hops each (v1).
- It runs at effort `low`: a `medium` node cost about $0.04, mostly thinking; `low` costs $0.01–0.02.
- ANSWER comes first, at most 15 lines, each cite copied from its own hop's `@` line. A printed tree as large as the code put the lead's peak context at 61–69K tokens, against the single agent's 72K (v2).
- Backticks hold only text copied whole from one line, and a quoted range skips no line and holds no `...`. In probe 1, 44 answer lines were flagged `?` for an argv assembled from several lines, and 71 of 509 hops kept one line because the quote skipped one.
- A path is copied as the read printed it: 49 of the day's 239 rejected hops named an invented directory ([Rejected-hop survey](tracer-bench.md#rejected-hop-survey)).
- One `tt write`; a refused write is fixed from the lines the refusal prints and written again, and the stored file is final. A node rewrote its file 45 s after its first write, finished after the lead's final and woke it (v4).
- It returns one line and never opens a child's file.

### The final

- It opens with the last wait's `⚠` WARNINGS lines, copied whole; `WARNINGS: none` is not copied. A node that broke the hop contract is named where the caller reads, by the user's ruling after the engine trace ([Engine trace](tracer-bench.md#engine-trace)); every answer line resting on such a node also ends `⚠ node {ID} broke the hop contract`, rendered by the kit and copied by the lead, because the field's callers did not relay WARNINGS lines they were handed.
- Facts come from unflagged answer lines, hops and the lead's own reads; a list claim names each item, or counts them and names the exceptions. The single-agent body wrote "all 12 catches give X" when 5 did not.
- It always carries test homes, the check command and anchors. Fenced lines come only when the brief asks for quotes or writes task files, copied from `--full` with `path:A-B` above: probe 1's 2,903 fenced characters went unused.
- NOT READ copies the last wait's unsettled OPEN lines with their suffix, and gives each sub-ask of the lead's own a next step: `run: node {ID}` only for what a drill left open or a node past the two drills of a wait, else `ask: {question}`. Probe 2's caller called 4 of 8 open items unsettled while the run held parts of 2; in probe 3, 12 of 34 items carried no next step.
- The last two lines are the `MAP` line and `RUN {RUN} · {COUNTS} — before another tracer question or writing a NOT READ item as unsettled: … check {RUN} --hops | grep -n '{symbol}' shows what this run verified, and the same check with `--hops {ID}` drills a marked node; /read-tracer-run has the rest`. The callers of probes 2 and 3 never loaded the skill a pointer named; a runnable command needs no skill.
- A wake after the final is answered with the final again, unchanged: a late wake's reply replaces the final, and on v4 the lead rewrote it at 18K output tokens.

### The caller

`read-tracer-run` triggers before any follow-up question to tracer, before writing a tracer NOT READ item as unsettled, and on any answer ending `RUN` or `MAP`. It has the caller:

- relay a final's WARNINGS lines to the user in its next reply, and drill an answer resting on them, or marked `⚠ node {ID}`, before using it;
- ask the run (`check --hops | grep`) before opening a file, and paste task-file lines from `--full {ID}`;
- hand a child the run and the node IDs covering its unit, never a re-trace;
- send a question the run does not settle back to tracer before calling it unsettled, and never re-ask a fact the final cites;
- cite the repository's `path:line` in a tracked file, never the run, which is scratch.

`flights-foreman` sends code it has not read to tracer at intake or any later step, carries `Skill` to load `read-tracer-run`, keeps the `RUN` through every compaction and puts `RUN` and node IDs in each child's brief (`templates/global/agents/flights-foreman.md:32`, `:57`, `:91`). `reviewer` spawns `subagent_type: tracer` for each review seat, asked for facts and told to skip CHECK, TESTS and lines to paste (`templates/global/agents/reviewer.md:43`). `clone-distiller` sends the code part of every question about an external clone to one tracer, its prompt naming the clone read-only, runs the history commands itself, reads the clone only for a NOT READ item its goal turns on, drops CHECK, TESTS, MAP and RUN except to drill a marked answer with the RUN line's check command, and treats a marked answer as unchecked (`templates/global/agents/clone-distiller.md:45-48`). `reviewer` and `clone-distiller` carry no `Skill` tool, so the RUN line alone must serve them. The reviewer prompt and the fleet prompt still lack the rule that a marked answer is unchecked until drilled and that a final's WARNINGS lines are relayed whole.

## Harness behaviour

What the tree rests on, each observed in the lab:

- A child runs asynchronously; its completion arrives as a notification that wakes its parent.
- Notifications that arrive during a blocking tool call are absorbed into the parent's next request and wake nothing; several completions can arrive as one wake. So the lead waits inside `tt wait`, never on wakes.
- A child finishing after the lead's final wakes the lead, and the lead's reply replaces the final.
- A spawn past the harness's concurrency cap fails at once with the tool result `Concurrent subagent limit reached. You can run 50 subagents at once. Do not retry.`; a spawn past the depth cap was never observed. The fleet's default depth is 8 (`pfm/internal/config/subagents.go:19`). Either way the thread never starts, so `tt wait` reports a named node not started after 120 s as `never started`, and the gap round respawns it: in the field, seven threads refused this way all started on the respawn ([Field runs](tracer-bench.md#lead-audit)).
- Agent types load at session start: an edited agent file runs in the next session.

## Model

`tracer` runs on `sonnet` at `high` effort; its sub-tracers on `haiku` at `low`. On the single-agent body before the tree, it cost 43% of an `opus` lead's price at 98% of its accuracy and 58% of its clean blocks ([tracer bench](tracer-bench.md#against-an-opus-lead)).

- The bench measured Sonnet 5.5 behind the alias `sonnet`; a new Sonnet release behind the alias re-opens the bench.
- Both agents run on Claude only, by the user's ruling: a generator of GPT role twins must never map `tracer` or `sub-tracer`. Over 85 field runs the twins, `gpt-tracer` on `gpt-6-sol` and `gpt-sub-tracer` on `gpt-6-luna`, cited a line past the file's end on 8.3% of hops against `tracer`'s 0.11%, and had 0.84% of hops rejected against 0.17% ([Field runs](tracer-bench.md#field-runs)).
- Codex: `tracer` and `sub-tracer` carry `codex-sandbox: workspace-write`, since `tt` writes the run directory and a read-only role could not.

## Open items

- Run-to-run variance is as large as any prompt effect measured: one setup's two B1 runs scored 2.45 and 1.40 in value. A prompt change needs several runs per arm before it ships.
- Splitting: on the installed B1 run no node split, and the lead passed over a needed branch a node had left as a LEAD. The v7 fix for both cost 40% more for half a facet and was reverted ([Tree bench](tracer-bench.md#tree-bench)); a cheaper fix is open.
- Every tree number rests on one or two runs per setup, on briefs over one repository.
- A lead answering a short brief itself, without spawning, is unbenched.
- The 120 s `never started` window rests on lab starts 8–21 s after `tt new`; a depth-cap refusal was never observed in a run.
- One thread per item of a list the brief asks to classify does not hold yet: on replay P0 the lead grouped 24 docs into three threads of 8, left two unread and ran no gap round.
- The read-numbering fix behind P20's 12 wrong line numbers is unbenched on a replay.
- Callers do not work the run. Across 84 callers in the field none re-asked a fact a final held, yet 3 ran `check` or `map` on a returned run, 1 loaded `read-tracer-run`, none of six audited callers relayed a final's `⚠` lines, and reviewer callers re-read quoted ranges with `sed` to verify them ([Caller audit](tracer-bench.md#caller-audit)). The per-line `⚠ node {ID}` mark sits where the caller reads the claim; whether callers act on it is unmeasured.
- 55 of the 88 rejected hops left after the resolver are one run on a submodule plugin since renamed: the resolver follows a moved file only where its basename, a sibling or a unique line places the quote ([Resolver and drift](tracer-bench.md#resolver-and-drift)).
