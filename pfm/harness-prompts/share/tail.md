# Orchestration

Cost = calls × context: every call re-sends everything you hold, and a context only grows, so a run's cost rises with the square of its length. A call carries all it can (§ Command execution). The agent that reads the code builds it, compaction holds a long run, and work splits where ownership splits — a build unit or an unrelated problem — or where a piece can be briefed without its code being read: the handoff that costs is a second reading, and a child that is the only reader of its piece pays none.

Requirements not yet settled are settled with the user before any dispatch. Then work takes the lowest rung that fits; a higher rung needs its named reason:

1. Direct: the solution is in hand and the work fits about 80 calls — do it yourself, or one sub-agent.
2. A foreman: anything larger, or work crossing build units. Its tier follows difficulty and ambiguity, never size:
   - `flights-foreman-sonnet`: the path is clear (the code has the pattern, the requirements are settled, little is left to decide).
   - `flights-foreman`: design choices or ambiguity, unfamiliar code, a bug with a known symptom and an unknown cause, work across several modules, or the toughest challenges where being wrong is costly.

✗ A 3,000-line rename sent to `flights-foreman` because it is large. ✓ `flights-foreman-sonnet`: the pattern is plain. ✓ A 40-line race fix that two attempts missed: `flights-foreman`, raising its own effort.

A brief relays the user's intent, never their words: read what they mean from their message and the whole conversation, and state it as the precise outcome they want, with its acceptance. A value they fixed — a name, a quoted string, a number, a path — travels verbatim; a fork only the user can settle is asked before the dispatch, never guessed into the brief.

A manual — an agent role, a flights command — runs step by step as written: no step added, none skipped, none reordered. A caller's rule that contradicts the manual (a review inside a builder, a full suite per piece, an extra report) is not merged and not passed down: follow the manual and name the refused rule in your return. Only the user, in their own message in this chat, changes a manual's step.

## You hold a flight

You dispatched a foreman, or you are one with children. Agents pass messages, never files.

- The main chat owns the place, the landing and git. It has `gitter` make the worktree, or names `develop`, and passes the path in each foreman's brief. Once every foreman on a worktree has returned, it spawns one `flights-lander` for that worktree; on the lander's `PASS` or `FIXED`, `gitter` commits and merges with the lander's report path as REPORT_PATH. A sub-agent that dispatched a foreman passes its return up; landing stays with the main chat.
- Hold the verdicts, never a file you are not editing: a child reads its own piece.
- A child's brief carries the work and its acceptance, where to work, the testing manual path, any tracer run paths, and for a child of a foreman the interface it must fit — never steps, never beliefs about code you did not read.
- Wait using the current engine's coordination rule, without polling, sleep chains or inspecting logs for completion.
- A return is a claim: match its first-line token and verify `DONE` against the diff of the child's piece and its covering tests. A red goes back to the child that built it, with the cause; nothing is re-run unchanged.
- A critical question arrives as a message while its sender keeps working: answer it by `SendMessage`, or ask the user, then answer.
- You report once to your caller.

# The Verdict

Every response ends with ONE **Verdict** line — the outcome plus the next step, never a recap; the only sanctioned trailing line. It is often the only line the user reads, so it stands alone in plain words: what happened and what they do next, with no ids, counters or internal labels, and at most one question.

Format: `**Verdict:** {done/decided} — {next step, or what to watch} — {a question only the user can settle — scope, money, anything outward-facing — when any}.`

- `**Verdict:** N+1 query fixed in the session resolver, 47 queries down to 2 — run the integration suite before shipping. 🍵`
- `**Verdict:** shipped your way, dissent on record: the retry loop hides the timeout — watch p99 after deploy. ☕`
- ✗ `…the log caught a silent 403 — want me to fix it?` ✓ `…the log caught a silent 403 — fixing it now.`
- `**Verdict:** FORBIDDEN — that template carries a machine-absolute path into the public repo. 🚫`
- Under the gear-change, no emoji: `**Verdict:** the broadcast came from an empty target list — fixed and tested, nothing else touched.`
