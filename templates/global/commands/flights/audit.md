---
name: flights:audit
description: 'USER-ONLY — /flights:audit [directory], default the newest under $HOME/.local/state/pfm/flights/{project}/: checks every DONE, check, gate and commit claim of a running or landed flight against run.md, requirements.md, git and the transcripts; routes findings, fixes nothing. flights-foreman → here. Writes {directory}/audit.md.'
disable-model-invocation: true
---

# Audit — doubt the flight

Ground truth only: `run.md`, `requirements.md`, git, the transcripts, the checks' own output, the seats' state. Never a return, a recap, a message, or your own earlier picture. You find defects and inefficiencies and route them; you fix nothing. Input: $ARGUMENTS — the flight directory, else the newest under `$HOME/.local/state/pfm/flights/{project}/`.

## The anchors

An anchor you cannot read is a finding ("failed to look"), never an absence ("nothing there").

| Anchor | Where | Proves |
| --- | --- | --- |
| `requirements.md` | the directory | The rulings and the requirement rows, each naming the unit that owns it; absent for a flight with no spec, whose intent is the root's `GOAL` line |
| `run.md` | the directory | The header's baseline sha and date; `BASE`, `GOAL`, `DECIDE`, `FROZEN`, `CLAIMED`, verdict, `gate` and `RETRO` lines |
| `briefs/{unit}-r{round}.md`, `briefs/{id}-r{round}.md`, `briefs/gate-r{round}.md`, `agents.tsv` | the directory | What each child, executor and lander was told; a spawn in `agents.tsv` without its brief file is a finding |
| `tasks/{id}.md` | the directory | Each mechanical bulk edit the foreman fixed, and the `Files` its executor's diff must stay inside |
| `returns/{unit}-r{round}.md`, `returns/gate-r{round}.md` | the directory | Each child's, seat's and lander's return as written; a verdict line in `run.md` without its return file is a finding |
| `audit.md` | the directory | The previous audit, for a delta |
| Git | `~/.claude/agents/flights-foreman.md` § Change inventory, `git log {baseline}..HEAD`, `git status --porcelain` | Tracked and untracked content and line counts per unit, with strays named |
| Transcripts | `$CLAUDE_CONFIG_DIR/projects/{cwd slug}/{session id}/subagents/agent-*.jsonl`, default `~/.claude`, one flat directory whatever the depth; the `.meta.json` beside each names its `agentType` and `spawnDepth`; a seat's through `chat_read` | Calls per agent against its cap, peak context, what each foreman read and when, waits, children spawned |
| The gate | `gate.md` and the `flights-lander` transcript of each round: each project's full runs (a second only after a failed one), the `/code-review` run and its effort, the attack map, the tests it wrote; the return's `GATE` row is the claim checked against them | Every changed hunk mapped; findings and their terminal state |
| `REVIEW.md` | the directory, for a flight that merged | Every `gate.md` finding as `F{n}` with its status; one missing or contradicted is a finding |
| Spend | your own run of `node ~/.claude/commands/tokens/token-audit.mjs --flight {directory} --metrics-out {a scratch file}`; `metrics.md` and the `COST` row are claims compared against it | Per agent: calls, context, tokens, price, failed commands, poll calls, re-reads, compactions, over cap; an `UNMATCHED` row is a finding |

## The audit, piece by piece

1. Before the first edit, per foreman, from its transcript: the base gate ran on the untouched tree; a `GOAL` line and the door searches came before the first edit; every door hit was built or ruled out by its own `path:line`; each data shape the work relies on was read from its producer, not only a consumer or one sample; a `DECIDE` line exists where no ruling decided the design.
2. The split: children exist only at build-unit boundaries or for unrelated problems; depth is two (no child spawned a foreman or a lander); `FROZEN` with its check line precedes the first child's spawn; each child's brief carries its own requirement rows verbatim, the interface's paths and acceptance, and no steps or beliefs about code the parent did not read.
3. Ledger truth: every `DONE` line's unit shows its tracked and owned untracked changes in § Change inventory; name every stray and verify the gate's inventory and sizing include the untracked files; each requirement row has its case or assertion in the owning unit's test, read by you, its red and green runs in a log or a transcript; a `CLAIMED` line without a verdict is in flight (a live agent) or lost (named).
4. Repairs: a red went back to the child that built it; a second red of one child is `BLOCKED` in the return; the parent edited a child's unit only when the cause was the frozen interface; a mechanical executor's `SPEC-DRIFT` was answered by a changed task file.
5. Conformance spot-check on the highest-stakes rows (protected-data channels, contracts): each row against the code and its test, mechanically where it can be, by reading where it cannot.
6. Cost and cadence: calls per foreman against its 250 cap, the lander against 200; a cap reached that returned `DONE`; poll chains (`sleep`, `echo idle`, a repeated log peek); a turn ended while a child, a watch or a gate still ran; any per-step report.
7. Liveness, in flight only: a transcript still growing, a seat's watch lines, a pane captured full-screen and judged from process evidence; an empty capture is a failed probe, never a quiet seat.
8. Landing: one lander for the flight; `gate.md` holds every project's last full run, passed; each new lander test was watched failing; every finding is terminal; the commit sha exists and its diff matches the flight's; a landed sha is on the integration branch it names.

## Report

One table — `unit · claimed · evidence found · gaps` — then findings ranked most severe first, each with its artifact quoted and its prescription: the owning foreman, `gitter`, or the user when only the user can. All clear: the table plus its coverage line, what was probed and what would have failed the probe.

## Writes

`{directory}/audit.md`, this report, overwritten each run. Nothing else.

## Rules

- The judge is never the thing being judged: read the artifact, never the verdict asserted about it.
- A clean grep proves the pattern absent, never the flight clean; scope every claim to what was measured.
- An error is never reported as absence: "could not read the transcripts" and "no transcripts" are two findings.
- Findings are defects to route, never fixes to make by hand.
- User contact only for the physically user-only, opening with why it is.
