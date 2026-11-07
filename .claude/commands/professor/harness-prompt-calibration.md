---
name: professor:harness-prompt-calibration
description: 'Rebuilds § Talking to the user — `/professor:harness-prompt-calibration [days]`, default 60: from the user''s reactions to replies in every main chat; deploys and commits it. /professor:harness-prompt-calibration → agent-investigator, gitter. Returns the correction rate, changed rules and commit.'
argument-hint: '[days]'
---

# Professor Harness Prompt Calibration

Every rule about how a reply reads survives only when evidence rows or a hard constraint stand behind it.

## Constants

- REPO: this checkout's absolute root.
- SECTION: `pfm/harness-prompts/share/tail.md` from `# Talking to the user` to the end of the file.
- WINDOW: the days given ($ARGUMENTS), 60 when none, read in full; older history is sampled.
- RUN: `/tmp/professor/harness-prompt-calibration/{YYYYMMDD}/`, scratch that quotes the user verbatim, so nothing in it is ever tracked.

## Steps

1. Baseline: `git log -1 --format=%B --grep='^Calibration:' -- pfm/harness-prompts/share/tail.md` holds the last run's `Calibration:` line; none means this run is the baseline.
2. Spawn one `general-purpose` agent, model `opus`, effort xhigh, with § Coordinator brief, REPO, RUN and WINDOW filled in; wait for it.
3. Verify before applying: read `RUN/talking.md` and `RUN/rationale.md` whole, then check at least 10 evidence rows against their transcript `src` lines, every rule that reverses a live rule among them. A row that does not hold drops the rule it alone backs, or restores the live one; name each in the report.
4. Apply: replace SECTION with `RUN/talking.md`; when its `##` subsection names changed, update the tail's description in `pfm/harness-prompts/README.md` to match.
5. Check: `.claude/scripts/test.sh pfm/harness-prompts/harnessprompts.go pfm/internal/codexprompt/fleetprompt.go pfm/harness-prompts/share/tail.md pfm/harness-prompts/README.md` ends `VERDICT PASS`, the leak check included.
6. Deploy: `pfm generate`, then confirm each of `pfm/harness-prompts/generated/{claude,codex,opencode}.md` holds the new section's first `##` heading.
7. Commit: `gitter` COMMIT of `pfm/harness-prompts/share/tail.md` and `pfm/harness-prompts/README.md` alone, no push, naming step 5's run as the verification; a refused commit is reported verbatim; the message names each added, changed and cut rule in a clause, and its last line is `Calibration: {N} reactions, {R}% of prompts, {WINDOW} days`.
8. Report: the correction rate against the baseline; the reaction count per pattern; each rule added, changed or cut with the row count behind it; the live rules cut for lack of evidence, any of which the user may restore; coverage holes; the commit; and that open chats keep the old prompt until reloaded.

## Coordinator brief

Goal: draft a replacement for SECTION in REPO from evidence of how the user reacts to replies, and return the draft with the evidence ledger it rests on.

Load the `ultimate` skill as the law for this work, and `prompt:use` before writing the draft.

Read SECTION, the live section, and the rest of the prompt — `pfm/harness-prompts/share/head.md`, each `pfm/harness-prompts/{claude,codex,opencode}/professor.md` and `tail.md` above SECTION — so the draft neither repeats nor contradicts it.

### Corpus

- Extract every human prompt from the main chats in WINDOW, each with the assistant text before it: every Claude config directory's `projects/` (`~/.claude` and each account's), with symlinks resolved so no transcript is read twice; `~/.codex/sessions/`; and the OpenCode database.
- Skip sub-agent transcripts and messages one chat delivered to another.
- Save the extractor under `RUN/corpus/`, with an index per shard: WINDOW split into three equal-count time shards `c1`–`c3`, read in full; Codex and OpenCode as shard `x`; older Claude as shard `o`, sampled.

### Investigators

Spawn at most 4 `agent-investigator` sub-agents, model `opus`, effort high, in one message, each with the shards it owns.

A row is a user message reacting to HOW a reply communicated, never to what the work did, under exactly one primary pattern code:

- `CONFUSION`: "what?", "I don't understand", "what does X mean" about a term, label, id or name the reply used.
- `REASK`: asks again what the reply should have answered, or points at an unanswered question.
- `STEER-LENGTH`: too long, shorter, "just tell me".
- `STEER-FORMAT`: tables, lists, headings, paths, line breaks, where things go.
- `STEER-TONE`: persona, warmth, humour, emoji, apologies.
- `STEER-WORDING`: jargon, coined labels, ids, plain words.
- `STOP-ASKING`: complaints about being asked, or offered options instead of action.
- `ANGER`: swearing aimed at a reply's form.
- `STYLE-REQUEST`: a standing request for how to communicate.
- `PRAISE`: approval of a reply's form.
- `OTHER-COMM`: any other reaction to communication, named in `note`.

Each row also flags, in `elements`, every live-section element it touches, since each live rule is under test.

Row fields: `id`, `src` (transcript path:line), `ts`, `engine`, `pattern`, `also`, `confidence` (`STRONG` when the words are about the reply's form, `LIKELY` when inferred, with why in `note`), `user` (verbatim), `reply` (the excerpt reacted to), `cause`, `rule`, `elements`, `note`.

Each investigator writes `RUN/inv-{shard}.jsonl` and `RUN/inv-{shard}-coverage.md` (rows read, window, NOT READ).

### Measure

- Check every row's `user` text against the corpus verbatim, by script.
- Estimate recall with an independent keyword pass and a Lincoln–Petersen estimate.
- Compute the correction rate (reacting prompts / all prompts) per week and for the whole WINDOW.
- Measure the rate per reply trait — words, lines, question marks, relative paths, coined labels, emoji, the address — on a build set, with the newest shard held out; a trait counts only when both sets agree.

### Draft

- Every rule cites rows, or is a hard constraint below.
- A live rule the evidence contradicts goes; a live rule with no row is cut and listed for the user.
- Close with whole invented example replies, one of them a ✗ reply shown beside the ✓ reply that repairs it; never one example per rule.

Hard constraints:

- The section stays the last part of `tail.md`, under `# Talking to the user`, with a `## The Verdict` heading (`pfm/internal/codexprompt/fleetprompt_test.go` looks for `# The Verdict`).
- A reply with two or more sections closes each with a `**Verdict:**` line, each sub-section inside one with an italic `*Verdict:*`, and ends with one `# Ultimate verdict:` line, a level-1 heading; a reply with one section or none ends with a lone `**Verdict:**`. The prefixes `**Verdict:**` and `# Ultimate verdict:` stay exact: `pfm/internal/headless/watch.go` reads the last such line.
- The gear-change stays, triggered by the leak line, the publication line or swearing: no emoji, no figure of speech, cause or fix first.
- Every path handed to the user is absolute.
- Nothing identifying: no names, client or private project names, `/home/…` or `/Users/…` paths, or transcript quotes; examples are invented, their paths under `/srv/shop/`.

### Outputs

- `RUN/evidence.md`: method, counts per pattern, the weekly and whole-window correction rate, the trait measures, the ledger (one row per item), and each investigator's coverage.
- `RUN/talking.md`: the replacement, from `# Talking to the user` to its last example.
- `RUN/rationale.md`: each rule mapped to its rows, then each live rule's fate and why.

Boundary: write only under RUN; no edit in REPO, no git command, no message to any chat.

Return the three paths, the counts per pattern, the correction rate, the five changes that matter most and every coverage hole.

Failure is reported as it is, never padded: an investigator that returns nothing or fails, fewer than about 15 rows, or a draft rule with no row behind it.
