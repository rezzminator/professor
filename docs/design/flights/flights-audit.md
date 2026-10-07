# /flights:audit

`/flights:audit` is the skeptic over a flight, running or landed. It believes artifacts: `run.md`, `requirements.md`, git, the transcripts, the checks' output, the seats' state. It never believes a message, a return, a recap or its own earlier picture. It finds defects and inefficiencies and routes them; it fixes nothing. User-only: a model never invokes it.

## Contents

- [The anchors](#the-anchors)
- [The pieces](#the-pieces)
- [Report and writes](#report-and-writes)
- [Rules](#rules)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## The anchors

An anchor that cannot be read is a finding ("failed to look"), never an absence.

| Anchor | Where | Proves |
| --- | --- | --- |
| `requirements.md` | the flight directory | The rulings and the requirement rows each unit owns; the statement every return and test is read against |
| `run.md` | the flight directory | The baseline sha; `BASE`, `GOAL`, `DECIDE`, `FROZEN`, `CLAIMED` and verdict lines; the `RETRO` lines |
| `briefs/{unit}-r{round}.md`, `briefs/{id}-r{round}.md`, `briefs/gate-r{round}.md`, `agents.tsv` | the flight directory | What each child, executor and lander was told; a spawn without its brief file is a finding |
| `tasks/` | the flight directory | Each mechanical bulk edit the foreman fixed |
| `returns/{unit}-r{round}.md`, `returns/gate-r{round}.md` | the flight directory | Each child's (sub-agent or seat) and each gate round's return as written; a verdict line in `run.md` without its return file is a finding |
| Git | `git diff {baseline} --stat`, `git diff {baseline} -- {unit}`, `git log {baseline}..HEAD`, `git status --porcelain` | What changed, per unit and outside every unit |
| Transcripts | `$CLAUDE_CONFIG_DIR/projects/{cwd slug}/{session id}/subagents/agent-*.jsonl`, the `.meta.json` beside each naming `agentType` and `spawnDepth`; a seat's session through `chat_read` | Calls per foreman against its cap, peak context, files read, waits, children spawned |
| The gate | `gate.md` and the lander's transcript | Each project's full runs (a second only after a failed one), the review, the attack map, findings and their terminal state |
| `REVIEW.md` | the flight directory | The merge-gating report against `gate.md` |
| Spend | its own run of `token-audit.mjs --flight {directory}`; `metrics.md` is a claim compared against it | Per agent: calls, context, tokens, price, compactions, over cap |

## The pieces

1. Before the first edit: each foreman's `BASE`, `GOAL` and door list precede its first edit in its transcript; every door on the list was opened; each data shape the work relies on was read from its producer; a `DECIDE` line exists where no ruling decided the design.
2. The split: children exist only at build-unit boundaries or unrelated problems; depth is two; no child received the parent's beliefs about code the parent did not read (its brief carries requirement rows, the frozen interface's paths, acceptance); the interface was frozen (`FROZEN` with its gate line) before the first child spawned.
3. Ledger truth: every `DONE` line's unit shows its changes in `git diff {baseline}`, nothing outside its unit; each requirement row has its case or assertion in the owning unit's test, read, and its run in a log or transcript, not in a claim.
4. Repairs: a red went back to the child that built it; a second red of one child is `BLOCKED`; the parent edited a child's unit only when the cause was the frozen interface; a mechanical executor's `SPEC-DRIFT` was answered by a changed task file.
5. Conformance spot-check on the highest-stakes rows (protected-data channels, contracts): each row against the code and its test.
6. Cost and cadence: calls per foreman against its cap; a cap reached returned `FAILED`, never `DONE`; poll chains; a turn ended while a gate or child still ran.
7. Liveness, in flight only: a transcript still growing, a seat's watch lines, a full-screen capture judged from process evidence.
8. Landing: one lander per flight; every project's last full run in `gate.md`, passed; each new lander test watched failing; every finding terminal; the commit's diff matches the flight's; a landed sha on its integration branch.

## Report and writes

One table — `unit · claimed · evidence found · gaps` — then findings ranked most severe first, each with its artifact quoted and its prescription (the owning foreman, `gitter`, or the user when only the user can). Writes `{flight directory}/audit.md` and nothing else.

## Rules

- The judge is never the thing being judged.
- A clean grep proves the pattern absent, never the flight clean.
- An error is never reported as absence.

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The command | `templates/global/commands/flights/audit.md` | The anchors and pieces |
| The builder | [`flights-foreman`](flights-foreman.md) | The `run.md` shape, the brief, the return |
| The lander | [`flights-lander`](flights-lander.md) | `gate.md` and the return file |
