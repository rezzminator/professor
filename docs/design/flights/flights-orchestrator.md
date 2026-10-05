# flights-orchestrator

`flights-orchestrator` is the one manual of running a flight: a spec directory written by [`flights-speccer`](flights-speccer.md), one fresh executor per task file, none of them executed by the orchestrator itself. It holds the index and the verdicts and nothing else. Its body is read three ways: as a sub-agent (the nested container), by a main chat acting as it (live), and by a main chat driving chat seats (cross-harness); the substitutions are in [`flights.md`](flights.md#three-containers-one-manual), and the manual keeps one branch for them: the `main-chat` row, which a main chat applies itself and a sub-agent orchestrator returns as `MAIN-CHAT`.

Decisions live in this file. The executable wording lives in [`templates/global/agents/flights-orchestrator.md`](../../../templates/global/agents/flights-orchestrator.md). A change lands here first, then in the template, then in every surface listed under [Surfaces that stay in sync](#surfaces-that-stay-in-sync).

## Contents

- [Why one manual, held by a sub-agent](#why-one-manual-held-by-a-sub-agent)
- [Input](#input)
- [The run](#the-run)
- [The executor brief](#the-executor-brief)
- [Verdicts are evidence, not truth](#verdicts-are-evidence-not-truth)
- [Situations](#situations)
- [Review](#review)
- [`run.md`](#runmd)
- [Landing](#landing)
- [The return](#the-return)
- [Retro lines](#retro-lines)
- [The universal laws](#the-universal-laws)
- [Not part of the design](#not-part-of-the-design)
- [What the evidence says](#what-the-evidence-says)
- [Measuring a run](#measuring-a-run)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)
- [Open items](#open-items)

## Why one manual, held by a sub-agent

A main chat outlives every agent, so its context is the dearest in the family. The orchestration of a flight is a loop of dispatch, wait, verify, react, dispatch that runs for the whole flight; run in the main chat it re-sends the chat's entire history on every step. The loop therefore lives in a sub-agent on `claude-sonnet-5-5` at effort `high`, the full model ID pinned in its frontmatter because the alias resolves differently per account, that holds only the index and the verdicts, and the main chat hears from it once. Its frontmatter sets `experimental: { cacheTtl: 1h }`: the hour-long prompt cache survives the waits between returns, which outlast the default five minutes.

The same body is the only description of the protocol. A second copy for the live or cross-harness container would drift; the commands for those containers substitute the transport (how an executor is spawned, briefed, waited for, questioned, stopped) and read everything else from the agent file.

## Input

| Input | Rule |
| --- | --- |
| The flight directory | Required. Work that arrives without one goes to `flights-speccer` first, and its return is the index |
| Standing rules the executors work under | Where and with what the executors work, pasted into every brief: [Standing rules](#standing-rules) |
| The projects and their testing manuals | Each project the flight touches, with its manual's path: it travels in every executor's and lander's brief. A project without one is a `NOTES` line |
| A worktree | Used when the flight runs outside the checkout; otherwise the checkout |
| The cap | Executors in flight at once; absent, ten. An executor's own cap (80 calls) lives in its agent; a `CLAIMED` line is stale after 60 minutes, the nested container's bound; a cross-harness seat is watched instead, by Monitors on its return file and on its `pfm chat watch` transitions |
| The landing | Which checks run after the gate, whether gitter commits. Absent: no checks beyond the gate, no commit. The gate itself is never optional and its review effort is the lander's to size, unless the user ordered a level above `medium`: that order travels to the lander as given |

### Standing rules

What the project contract and the testing manual do not carry: the worktree, the fenced command that runs one package's affected tests, anything the caller adds for this flight. Pasted into every executor brief, never into a task file; the orchestrator authors none. The `CLAUDE.md` / `AGENTS.md` contract reaches every executor from the harness and is never pasted or named.

A standing rule says where and with what an agent works, never what steps it runs: one that adds, drops or replaces a step of a role is refused and named under `NOTES`. The measured case: a launch message written by a chat born before the redesign ordered "every executor's self-review is a review over its own change"; the orchestrator pasted it as "overriding the role's no-review rule", and six executors ran 23 review processes. A second measured case: a brief whose rule read "every build/test runs … `dev.sh iso test pfm`" meant the script's path but named the full suite, and the orchestrator added "launch it backgrounded and wait once"; 146 of the six executors' 201 minutes went to full-suite waits, so the refusal keys on the full-suite command itself, however the rule frames it.

A rule that widens a test scope the testing manual sets (whole packages where it names single tests) or prescribes a fixed sleep is refused the same way: an audit found a brief widening `-run` to `./internal/{pkg}/...` and one ordering `time.sleep(60)` before a rebuild.

The fleet prompt's manual law (`pfm/harness-prompts/share/tail.md` § Orchestration) refuses, for every manual, a caller's rule that contradicts it — a review inside an executor, a full suite per task, an extra report; the test-scope and fixed-sleep refusals live in the agent alone.

## The run

1. Read `index.md` and `run.md`. On a fresh flight write the `run.md` header: the directory, the baseline commit (`git rev-parse HEAD` at this moment), the date. A task with a `DONE` line is done; a task with a `CLAIMED` line and no verdict was in flight when the last run stopped: treat it as not started, and say so in the return. A task with a `BLOCKED` line and no later verdict goes out only when the brief names it revised (its file rewritten by a revising call after the user's ruling); otherwise it stays `BLOCKED` in the return. A task whose last line is `WAIT` is ready. A task whose last line is `FAILED`, `SPEC-DRIFT` or `TOO-LARGE` goes out only once a revising call's `REVISED` line names it; otherwise the resume makes that call first: an orchestrator stopped before its revising call returned would otherwise re-send an unchanged task file.
2. Dispatch every task whose `needs` are all done and whose `External need: {flight directory} {id}` Decisions lines (one `grep` of the task file, the orchestrator's only read of one) each show `{id} DONE` in that flight's `run.md`, in one message, as many at once as its `shares` and the concurrency cap admit. The harness reports nothing when its cap is hit, a spawn past it does not happen: count the executors in flight (`CLAIMED` lines with no later line for that id) and stay under the cap. Write one `CLAIMED` line per task before the spawn. Holding a ready task for a sibling is a violation; holding it for a free slot is the one legal hold.
3. Wait by ending the message with one line and no tool call; each executor's return arrives on its own.
4. On each return: read its first line, verify it, append its verdict line to `run.md`, react per [Situations](#situations), dispatch whatever it unblocked.
5. After the last verdict: the landing.
6. Return once.

The orchestrator never opens a task file and never reads a repository file to judge a task. What it must know about a task is in the index; what it must check is in the executor's return and in git.

## The executor brief

The brief carries, and nothing more:

- the task file path and the paths its index row `reads`; the executor opens them together with the brief file in its first message;
- the `DONE` lines of the tasks it `needs`, pasted from `run.md`, so an upstream adaptation reaches it without a spec rewrite;
- the standing rules, and the worktree when one exists, stating that every `Files` path and every command resolves under it;
- every `RETRO` line `run.md` holds so far, pasted under the standing rules;
- the path of the testing manual of the project the task changes.

Nothing else: the task file is the spec, and the [executor's agent](flights-executors.md) holds what the brief used to restate — the cap, the tests it writes, its hand (open inside `Files` on `precise` and `smart`, the listed adaptations only on `mechanical`), the return's shape, git read-only. The index row's `rating` picks the agent type: `mechanical` → `flights-mechanical-executor`, `precise` → `flights-precise-executor`, `smart` → `flights-smart-executor`; the spawn carries no model override, so each tier runs the model and effort of the [tier table](flights-executors.md#the-tiers).

## Verdicts are evidence, not truth

An executor's return is a claim. The orchestrator matches the first line's token and then verifies:

- `DONE`: the return names what changed; per `Done when` row, a covering test with its failing line in the red log whose path the return names, or marked `pre-existing, no red proof` with that test passing in that log and the commit or `run.md` line that introduced the behaviour, so a pre-existing row is proved rather than asserted; or, for a row with no behaviour change (a rename, a move, a deletion, a doc) or one a written deliverable meets, its check line and the quoted line that meets it; and `git diff {baseline} --stat -- {the index's files}` shows a change: the files come from the index row, never from the return, so the judge is never the judged. A return that claims done with no proof, or with nothing changed, gets one question back to the same executor; a second such return is recorded `FAILED`.
- `FAILED`, `SPEC-DRIFT`: the return names a cause, or names what was read and says the cause is unknown; a red with neither is shapeless. Recorded as returned with the executor's transcript named on the line; the reaction is in [Situations](#situations).
- `BLOCKED`: recorded as returned; the reaction is in [Situations](#situations).
- No token on the first line, or a red with no cause and no reading named: one question back asking for the return in shape; a second shapeless return is `FAILED`.

Every message the orchestrator sends an executor after its dispatch — a question back, a re-brief, a malformed-return bounce — closes with "continue, then return once more in the return shape", so the executor's next message is again a return.

The baseline is the commit recorded in the `run.md` header, so a resumed flight judges against the same tree the flight started from.

## Situations

Every situation the manual answers, with who acts. The orchestrator fixes nothing itself and patches no task file.

| Situation | Reaction |
| --- | --- |
| `DONE`, verified | `run.md` line names what was adapted or `as specified`; dispatch what it unblocked |
| `DONE` without proof or without a change in git | one question back to the same executor; a second such return → `FAILED` |
| `FAILED` (including a cap) | start nothing that needs it; send `flights-speccer` the report, what is already done, the completed ids and the executor's transcript — nested and live: the sub-agent transcript path (`$CLAUDE_CONFIG_DIR/projects/{cwd slug}/{session id}/subagents/agent-{id}.jsonl`, the id is the agent id the spawn returned, `agents.tsv` column 3), on Codex the agent path (`/root/{name}`) that column holds, which `transcript.py` resolves; cross-harness: the seat's session id (`pfm chat resolve`, read right after birth); the revised task files cut the task smaller or re-approach it, and the return carries the rows its `REVISED` line names (`REVISED {ids} · REMOVED {ids}`, every task file it added or rewrote): re-read `index.md` once and dispatch from it. The same task file is never re-run unchanged |
| `FAILED {id}: blocked by {files}`: an executor whose own tests are stopped by causes outside its `Files` returns every cause at once, the first line naming every file, then one `{file}: {error line}` line per cause; every named file is in the index `files` of a task in flight | `{id} WAIT · {those tasks' ids} · {the first error line}`; it frees the slot, is no red of this task and is never sent to the speccer; the task goes out once more, unchanged, after those tasks' verdicts, and a resume treats a task whose last line is `WAIT` as ready. A named file no task in flight changes: the `FAILED` road |
| `SPEC-DRIFT` | the same road as `FAILED`, with the drift report as the reason |
| `SPEC-DRIFT {id}: too large` (a `smart` executor's estimate, nothing changed) | `{id} TOO-LARGE · {the split it proposes}`: no round, no red, no transcript; a revising `flights-speccer` call cuts the task |
| Any revising round | the speccer is pinned at `opus`; every revising round — a red, a failed landing check, a lander residual, the diagnose-first round included — is a fresh `Agent(subagent_type: "flights-speccer", model: "opus")` handed the directory and the reason, since a `SendMessage` keeps the running speccer's model; the one `SendMessage` revision is [`/flights:spec`](flights-spec.md)'s S4 |
| A return carries `RETRO {lesson}` | [Retro lines](#retro-lines) |
| A second `FAILED` or `SPEC-DRIFT` of the same id | the orchestrator marks the revising call diagnose-first (a `WAIT` or `TOO-LARGE` line is no red) and it carries every transcript of that id; the speccer runs diagnose-first only on a call so marked; `flights-speccer` names the cause in the task file before rewriting (its § Drift). The `run.md` line carries the round: `{id} SPEC-DRIFT · round 2 · …` |
| A third red of the same id | `{id} BLOCKED · {the executor's cause line}`; no third revising call. The question travels in the return like any `BLOCKED`; the flight ends without the task |
| `BLOCKED` with a question only the user can answer, from an executor or in a revising `flights-speccer` return | record `BLOCKED`; start nothing that needs it; every other task continues; the question travels in the return. The ruling comes back as a revising `flights-speccer` call: the live container makes it on the answer; the nested container's caller makes it after the return, then resumes the run naming the revised ids |
| A `main-chat` task whose needs are done | no executor: its files are the main chat's alone, and the guard denies every sub-agent. The main chat as orchestrator claims and applies it under `/pcm`, verified like any `DONE`; a sub-agent orchestrator writes `{id} MAIN-CHAT · waits for the main chat`, starts nothing that needs it and returns it; `/flights:orchestrate-nested` applies each `MAIN-CHAT` task file in the main chat under `/pcm`, inside the worktree when the flight has one, and re-runs naming `applied {ids}`; step 1 then checks `git diff {baseline} --stat -- {the index row's files}`: a change → `{id} DONE · applied by the main chat`, none → it stays `MAIN-CHAT`. Its files join the commit through the `DONE` union |
| `BLOCKED {id}: {question}` the index or the brief answers | answer it by message to the same executor; an executor's question always arrives as this return, never as a token-less failure |
| A question from an executor nobody but the user can answer | `BLOCKED` for that task, as above |
| The concurrency cap is reached | never reported by the harness: the count of in-flight executors is the only guard. Hold the task; dispatch it as the next return arrives |
| An executor never returns | seen only when something wakes the loop (a sibling's return; in the live and cross-harness containers, the user): a `CLAIMED` line older than 60 minutes with no verdict. Named in `DISPATCHED` as a missing return; its task stays `CLAIMED`; the flight ends without it and the return says so. When it was the last executor, nothing wakes a nested orchestrator: the user re-runs the container, and the resume rule treats the task as not started |
| A cross-harness seat stopped without finishing | [Cross-harness seat liveness](#cross-harness-seat-liveness) |
| A landing check fails | unspecified work: a revising `flights-speccer` call with the check's output and the completed ids; its task files dispatch like any other |
| The lander returns `PASS {flight}` | the gate line in `run.md`; then the landing checks, then the commit |
| The lander returns `FIXED {flight}` | the gate line in `run.md`; the files it changed join the commit |
| The lander returns `FAIL {flight}`, a cap included | the residuals are unspecified work: a revising `flights-speccer` call, its task files dispatch like any other, then the gate runs again, one fresh lander over the whole flight; a second `FAIL` travels in the return and the flight ends without a commit |
| A defect a return names outside the task's files, or a lander's finding outside the flight | `NOTES`; never a fix by the orchestrator |
| A spec fault the orchestrator can see (two decisions contradict, an index row without a file) | a revising `flights-speccer` call; never a patch, never a ruling written beside the directory |

### Cross-harness seat liveness

A seat stops without finishing on a model-server error, a hung turn, a brief never submitted, a dialog or a dead process. The seats Monitor's line names it and the cross-harness command's action table answers it: re-prompt, Enter once, capture and judge, answer, or re-dispatch. A model-server error writes `{id} COMA · {kind} · re-prompted · {time}`; a second coma of one seat within 10 minutes re-dispatches the task on the Claude engine at the task's tier model, and writes `{id} COMA · {kind} · re-dispatched on {engine} {model} · {time}`.

## Review

Executors run no review. The flight is reviewed once, over its whole diff, by [`flights-lander`](flights-lander.md): after the last verdict the orchestrator spawns one lander for the whole flight, briefed with the flight directory, every project touched with its testing manual's path, the standing rules and the worktree or worktrees — and no review effort, which the lander sizes from the diff, unless the caller's brief carries the user's own order for a level above `medium`, passed on as given.

- One lander per flight, not one per project: a contract change has its producer in one project and its consumers in others, and only one review over the whole diff sees both sides together. It opens and closes every project's gate and holds the testing manuals' floors as rows of that gate, never as a task's `Done when` row ([`flights-lander`](flights-lander.md#the-run)).
- The per-executor review was the largest single cost of the audited flights (67 review sessions, 182M tokens, 38 of them whole-branch reviews of one growing diff); one review per flight sees the same diff once.
- The lander fixes what it finds; the orchestrator grades nothing and fixes nothing. Its return is a claim verified like any other: `gate.md` exists and the return quotes each project's two full-run verdict lines, or those a `FAIL {flight}: cap` reached. Missing either: one question back by `SendMessage` to the same lander; a second such return is recorded `FAIL`.
- The lander's return is also a file. Its last act before returning writes the return, verbatim, to `{flight directory}/returns/gate-r{round}.md`, under a temporary name moved into place so it appears whole. Right after the spawn the orchestrator starts one background command that waits for that file, bounded, and prints it: `timeout 10800 bash -c 'until [ -s "$1" ]; do sleep 20; done; cat "$1"' _ {flight directory}/returns/gate-r{round}.md`. A lander that ends a turn mid-gate (its gate runs and its forked review are background work) can have its final return delivered to the main chat instead of the agent that spawned it, while a background command's completion always reaches the agent that started it ([What the evidence says](#what-the-evidence-says)). Whichever wakes the orchestrator first, the file is the return it verifies and the other is ignored, as is an earlier round's watch. A wake without the file, an interim lander message included, ends the turn while the watch runs; no file by the watch's timeout is recorded `gate FAIL · no return`, named in `DISPATCHED`, with no commit. A question back to the lander takes its answer in `returns/gate-r{round}-q.md` under a fresh watch, so the answer wakes the orchestrator by the same road.
- `FIXED` adds the lander's files to the commit; `FAIL` sends the residuals to `flights-speccer`; a finding outside the flight goes to `NOTES`.

## `run.md`

One file, `{flight directory}/run.md`. A header line on creation — `flight {directory} · baseline {sha} · {date}` — then one line per event: `{id} {CLAIMED|DONE|FAILED|SPEC-DRIFT|TOO-LARGE|WAIT|BLOCKED|MAIN-CHAT} · {one line}`, `gate {CLAIMED|PASS|FIXED|FAIL} · {one line}` for the lander, and `{id} RETRO · {lesson}` for a lesson a return carried ([Retro lines](#retro-lines)); the cross-harness container adds `COMA` as a substitution, and the manual never names it. A `CLAIMED` line names the executor and the time; a `WAIT` line frees the executor's slot and a resume treats the task as ready; a `DONE` line names what the executor adapted, or `as specified`, and is what downstream briefs carry; a `FAILED` or `SPEC-DRIFT` line names the round for that id, the cause the executor gave and the executor's transcript, so the revising call and the audit can read how it got where it got. The file is the resume point and the ledger an audit reads.

Beside it, `{flight directory}/agents.tsv`: one tab-separated row per spawn, appended the moment the spawn returns its agent id — task id (or `gate`, or `spec`), agent type, agent id, round, ISO time, engine (`claude`, `codex`, `seat`) — for every executor, lander and speccer. It is append-only and the orchestrator never reads it back, so it costs the orchestrator's context nothing; `run.md` is re-read on resume and pasted into briefs, which is why the ids stay out of it. The ledger is what lets the metrics script and the audit open exactly a flight's transcripts instead of guessing them from a time window. The id is whatever the spawn returned, verbatim: an agent id on Claude; on Codex the agent path (`/root/{name}`), which the rollout's `session_meta.agent_path` repeats — a Codex orchestrator never sees a thread id, and the first two measured Codex flights matched 0 of 10 rows while the design assumed one. The time is printed by `date -u` inside the appending command, never typed: a measured ledger carried `19:00:00` for a spawn at 18:49, and a clockless date opens a match window at midnight. A voided claim keeps its row, because the agent ran and its spend is the flight's.

Beside both, `{flight directory}/briefs/`: one file per spawn, `{id}-r{round}.md` for an executor and `gate-r{round}.md` for the lander, written by the command that appends the `CLAIMED` line. `{round}` counts this id's spawns: every spawn, a `WAIT`, `TOO-LARGE` or re-seat re-dispatch included, takes the next round, so no re-dispatch overwrites an earlier brief or return; only `FAILED` and `SPEC-DRIFT` lines count as reds. The spawn message carries only paths: the brief file, the task file, its `reads`. Codex stores a spawn message encrypted in both rollouts, so on that engine a pasted brief can never be audited; the file costs no extra call and makes the brief an artifact on every engine. Beyond these the orchestrator writes one file, `REVIEW.md` at the landing ([Landing](#landing)): no other report, no per-step lines, no rulings.

## Landing

After the last verdict the gate runs ([Review](#review)), `PASS` or `FIXED`; then the landing checks the brief names run once, and the result recorded is the one the orchestrator watched print; a command emitted is not a check run. A check the lander's closing full run already ran on the unchanged tree is quoted from the lander's log, never run a third time: the fenced suite takes about thirteen minutes. A failing check is unspecified work and goes to `flights-speccer` as a revising call. When the brief asks for a commit, `gitter` makes it: Phase COMMIT in the checkout the flight ran in (the worktree, or the project), the files named as the union of the index's `files` over the `DONE` tasks plus the files the lander's return names, the message summarising the flight; the return carries the sha. A worktree flight committed on the brief's ask, every task `DONE`, then lands on its integration branch, and the lander's `PASS` or `FIXED` is the merge nod: no user order and no second review stand between the gate and the merge. A flight with a task `BLOCKED`, `MAIN-CHAT` or never returned is committed and ends `NOT LANDED · unfinished`, its worktree kept: the re-run that finishes it (`revised`, `applied`, or the missing-return re-run) lands it, so "lands" keeps one meaning. A repository's git writer gates a merge on a merge-gating review report with a status per finding, and a flight carries its findings in `gate.md`; so the orchestrator writes `{flight directory}/REVIEW.md` from `gate.md`, which stays the source: § 1 `VERDICT — MERGE`, then each finding as `F{n}` with `status: resolved @{commit sha}` for one fixed in the flight, or `status: waived — outside the flight` for one `gate.md` marks outside the flight; the resolved sha is the flight's commit before landing, and the `LANDED` row names where it went. Then one `gitter`, Phase MERGE, `REPORT_PATH` that file, its gate the lander's full-run verdict lines: it lands the flight branch on its integration branch, named in the brief beside the worktree, by the repository's own merge rules, authorised to rebase it onto the tip when the tip has moved, then removes the worktree and deletes the branch. The protocol dictates no git mechanics: a fast-forward-only writer rebases on that authority, a merge-commit writer merges, a writer that resolves conflicts under its own rules does so. A landing the git writer stops (a conflict, a divergence it will not resolve, a refusal) lands nothing: the worktree stays and the return says why. The orchestrator verifies a landing like any return: `git merge-base --is-ancestor {landed sha} {integration branch}` must pass, else the row is `NOT LANDED · refused`; an ancestry check, not the tip, so a concurrent flight landing right after it fails nothing. The landing's kind is read from git by the tree, never from how the writer merged: `gated tree` when `git rev-parse {landed sha}^{tree}` equals the flight commit's tree, the exact tree the lander gated, else `new tree`. The orchestrator runs no fix itself.

What follows the return is the main chat's: after a `LANDED` row the next flight it starts branches from that tip; when the last flight it is running has landed, the integration branch's gate runs once (the testing manuals' gate rows), skipped when every landing was a `gated tree`, because the lander gated that exact tree. A `NOT LANDED` conflict or refusal, or a red at that gate, goes to one `general-foreman`, which resolves it and lands the flight through the repository's git writer; `unfinished` follows the re-run roads; nothing else in a landing gets a foreman. A lander's return that reaches the main chat is the orchestrator's and is ignored there: the orchestrator's watch reads the same file. One gate after the last landing replaces a gate per merge, a foreman per merge and worktrees left standing between flights.

The landing's last act is the measurement: `token-audit.mjs --flight {flight directory}` writes `metrics.md` — one row per agent with calls, wall time, start and peak context, growth per call, tokens, price, failed commands, poll calls, re-reads, contract-file reads, compactions, over cap and how the row was matched — and the return's `COST` row quotes its totals and its most expensive agent. A failed run is reported as failed, never omitted. Hooks were rejected for this: the Codex compile drops an agent's hooks, Codex hooks see only shell commands, and no hook on either engine receives token counts; every measure lives in the transcripts. `/flights:audit` runs the script itself and treats `metrics.md` as a claim.

## The return

```text
FLIGHT {directory}
{id} · {verdict} · {one line}          one row per task
GATE {PASS|FIXED|FAIL} · {n} findings, {m} fixed, {k} residual | none
CHECKS {command → result} | none
COMMIT {sha} | none
LANDED {integration branch} @{sha} · {gated tree|new tree} | NOT LANDED · {conflict: {files}|refused: {the git writer's reason}|unfinished: {ids}} | none
DISPATCHED {n} executors, {g} landers, {m} returns, {k} revising rounds
COST {calls} calls · {tokens} · {price} · worst {agent}: {price}, {calls} calls | failed: {error}
BLOCKED {id}: {question} | none
MAIN-CHAT {id}: {task file} | none
RETRO {id}: {lesson} [MANUAL] | none
NOTES {up to five lines} | none
```

`DISPATCHED` reconciles agents sent against returns received; a missing return is named, never silent. `LANDED` names where the flight went and its kind, read from the tree as [Landing](#landing) says: `gated tree` when the landed tree is the one the lander gated, else `new tree`, which decides whether the main chat's integration gate runs; `NOT LANDED` names why the flight stayed off its integration branch (a conflict's files, the git writer's refusal, the unfinished task ids), its worktree left standing; `none` means no worktree or no commit asked, never a failed landing. `GATE` repeats the lander's first line and counts; the audit checks it against `gate.md` and the lander's transcript. `NOTES` carries the findings reported outside the flight's files and anything the user should know that fits no row.

## Retro lines

A flight learns while it runs. Every executor and lander return may carry one line `RETRO {lesson}`: something about the environment, the tooling, the project law or the testing manual that cost it calls and would cost the next agent the same. The measured case: every agent of one flight hit `pnpm: command not found` and burned six command rounds each, and nobody told the next one or the user.

- One line per agent, at most 200 characters: a fact plus the working alternative. Never a progress note, never about its own task's content; `none` is the normal case.
- The orchestrator stays `run.md`'s only writer: it appends `{id} RETRO · {line}` unless the same cause is already recorded or the lesson serves a step the executors do not run (a review, a full suite).
- Every RETRO line of the flight is pasted into each later brief under the standing rules; an environment or tooling lesson also goes by one `SendMessage` to the executors still in flight.
- All of them travel in the return's `RETRO` row, so the user hears what is broken on the host. A lesson that proposes a testing-manual addition is marked `MANUAL`: the manual is guarded, and `/pcm` folds it.
- A revising `flights-speccer` round reads the RETRO lines of `run.md` with the transcript.
- The cap that keeps it small: one line per agent, deduplicated, no second file.

## The universal laws

These hold for every orchestrator and every executor on every harness, and belong in the harness prompt's orchestration section as well as here:

- An executor reports once, when done, plus a real question or blocker; an orchestrator reports once to its caller, plus a question only the user can answer. Never routine progress, never a diff, a log or a file's contents in a message: progress lives in the artifacts.
- Waiting is one call or none: end the turn and let the return arrive. A poll, a sleep chain or a log peek re-bills the whole context each time. The agent's frontmatter hook `pfm internal orchestrator-wait` denies a Bash call that only waits. The gate's background wait on the lander's return file passes it: one command whose completion wakes the orchestrator, billing no model call while it waits.
- Only `flights-speccer` changes a task file. A fault goes back to it as a revising call; nobody patches a spec or writes rulings beside it.

## Not part of the design

| Left out | Reason |
| --- | --- |
| The orchestrator executing a task itself | Its context would carry the work of one task into the dispatch of every other |
| Opening a task file in the orchestrator | The index carries what dispatch needs; the file is the executor's |
| A per-step ledger | One verdict line per task is the resume point; more is chatter |
| Patching a task file or writing rulings beside the directory | A second spec over the first; `flights-speccer` alone edits |
| Stuck detection by pattern (repeated actions, monologues) | No published accuracy; a token, a cap and a stale bound cover the cases |
| Re-running a failed task file unchanged | The same spec fails the same way; the fix is a smaller or different task |
| Summarising an executor's context to keep it going | Executors are fresh per task; a task too big for one context is cut |
| A stuck-agent kill from inside the nested container | Not possible through the Agent tool; the missing return is named |
| A cold reviewer agent per `smart` task, briefed by the orchestrator and graded by it | Replaced by [`flights-lander`](flights-lander.md): one `/code-review` of the whole diff per flight, the fix with the agent that found it, nothing for the orchestrator to grade and no report to route |
| Trains, waves, a worktree per wave, BUILD-GREEN handshakes, an entry-point census, a conformance pass | The pipeline this agent replaces |

## What the evidence says

The rulings above rest on measured results, collected in the runtime research of 2026-09-20:

- Fan out only file-disjoint work: multi-agent gains +80.8% on decomposable work and loses 70% on sequential work; cross-agent pull requests conflict at 41.7% against 19.8% for one agent. The index's `needs` and `shares` are that rule made mechanical.
- Keep every dispatch single-turn and self-contained: models lose 39% on average when a task is spread over turns.
- Completion is a token matched in code, never text interpreted: every mature runtime (a finish action, a literal sentinel, a terminal event) does this; every runtime that scrapes text calls it a heuristic.
- Every cap in the field is hand-picked; the useful ones name their exit instead of dying as a generic error. The cap here returns `FAILED {id}: cap` with what is done.
- Decompose on failure rather than up front: +33 points over fixed decomposition. `FAILED` goes to `flights-speccer` to be cut, never retried as is.
- Reviewers habituate to agent output (approval +14.5 points, inline comments −22% over ten deciles of exposure) and human detection collapses past about 400 lines. The ruling still reviews the flight's whole diff once, on cost: a review per executor re-read one growing diff 38 times in one flight. The lander raises its review effort with the diff's size, and a flight too large to review in one pass is a flight to cut.
- A fresh sub-agent costs about 54K tokens of cold start; siblings dispatched in one message share the cached prefix at a tenth of the price. One-message dispatch is also the cache law.
- A sub-agent's final return can miss the agent that spawned it. Measured on one flight, 2026-10-05: the lander ran its gates as background commands and ended turns mid-gate; its final return (`FIXED`, 18:48:15Z) was delivered to the main chat, not to the orchestrator, which had ended its turn at 18:32 after an interim lander notice and sat idle 41 minutes until the main chat relayed the return by `SendMessage`. The lander's five background commands did report back to the lander that started them; executors, which run in the foreground and stop once, were unaffected. Hence the lander's return file and the orchestrator's own background wait on it.

## Measuring a run

| Measure | Healthy | A broken run shows |
| --- | --- | --- |
| Orchestrator peak context | Tens of K: the index, the briefs, the verdicts | Task file content or repository files read into it |
| Orchestrator calls | About two per task plus the landing | Polling, or per-step reads |
| Executors dispatched vs returns | Equal | A silent loss |
| Ready tasks left waiting | None, except for a free slot | A batch that waited for a sibling |
| Executor calls | Between about 40 and 80 | Under: tasks cut too small; over: a task that should have been cut, or a cap that never fired |
| Revising rounds | Rare | Specs that contradict themselves, or a stale directory |
| Review findings left in a return | Few, all outside the task's files | A finding inside the task's files left unfixed |

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The agent | `templates/global/agents/flights-orchestrator.md` | The manual; runs on `claude-sonnet-5-5`, effort `high` |
| The wait guard | `pfm internal orchestrator-wait`, attached in the agent's frontmatter | A Bash call that only waits (`echo`, `printf`, `true`, `:`, `sleep N`) is denied; design in [hooks.md](../hooks/hooks.md#agent-attached-hooks-not-machine-global) |
| The containers | `templates/global/commands/flights/orchestrate-{nested,live,cross-harness}.md` | The substitutions, nothing of the manual restated; the nested command's roads for a `BLOCKED` ruling, a `MAIN-CHAT` task and a `LANDED` or `NOT LANDED` row |
| The fleet prompt | `pfm/harness-prompts/share/tail.md` § Orchestration | The ladder's third rung ends here; the universal laws; the lander as the only review |
| The spec writer | [`flights-speccer`](flights-speccer.md) | The index this agent dispatches from (`files` included), the revising call |
| The adopter contract | `CLAUDE.md` and `templates/project/CLAUDE.md` | The executor's first move on a brief naming a task file; in this repository's `CLAUDE.md` also the fenced-flight rules under § Process |
| The executors and the lander | [`flights-executors`](flights-executors.md), [`flights-lander`](flights-lander.md) | What the brief no longer restates; the `RETRO` line in every return |
| The git writer | the repository's `gitter`, Phase MERGE | Reads `REVIEW.md` as its `REPORT_PATH`; a finding neither `resolved` nor `waived` refuses the merge |

## Open items

- The caps (80 tool calls per executor, 150 per lander, ten executors in flight at once) are first values, held by prompt alone: no hook enforces them, by ruling. Measure against the next flight.
- The cross-harness seats watch's `--quiet-after 900` is a first value: ordinary working seats go quiet for 20 seconds and more; measure against the next flight.
- Whether `/code-review` runs inside a `flights-lander` sub-agent (the Skill tool at depth); verify on the next nested flight, and measure the gate's cost per flight.
- The lander carries no `shares`: it runs alone after the last verdict, and each flight has its own worktree or runs on the main branch, so nothing contends with it. No gate points inside a large flight: the gate runs once, at the landing.
