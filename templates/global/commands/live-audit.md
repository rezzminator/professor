---
# professor: SOURCE TEMPLATE — edit here for a framework change (routes through the `pcm` agent); project-scaffold customization belongs in its installed local source; engine mirrors are never hand-edited.
name: live-audit
description: 'Judges features by real use — `/live-audit [feature|fix] [window|chats]`, or asks like "does X actually work", "stress test X in action", "how has X worked since install". Returns a verdict, per-occurrence evidence, NOT READ. Not for one past run → agent-investigator.'
argument-hint: '[feature|fix] [window|chats]'
---

# Live Audit

The subject: $ARGUMENTS — a plugin, hook, fleet behaviour or installed fix, then an optional window (`since the install`, `8h`) or the chats to read; with none, the feature in hand in this chat. The verdict is what the feature did in live chats, judged against its owner's intent. Tests, gates, log counts, installed-file diffs and the feature's own self-reports are preconditions, never the verdict.

## 1. Pin the intent

- Quote what the owner says the feature must do: their words in this chat, else the feature's orientation file (a "Function" / "A run, as expected" section, its README). With neither, draft a numbered expected-run list from the owner's words (what must survive, what the user must see, what must never reach their screen) and have them confirm it once; under god speed take it and name it in the report. The list lands in the feature's orientation file through that file's owner.
- Judge against that list, never against the code: "it does what its code says" is no verdict. An instruction or design that contradicts the intent is itself a finding.
- Name the trigger the user meets (a threshold, an event, a command) and the conditions it fires under in real use: the real threshold, the real caller's model, effort and engine, the premise the feature rests on (a warm cache stays warm).

## 2. Prove the running build

An observation counts only from a session that proves, by its own evidence, that it ran the build under audit: a version string the feature wrote into that session, a start row keyed to its session id, or a session start or `/reload-plugins` after the install. A long-lived process proves its binary: on Linux, `/proc/{pid}/exe` without `(deleted)`. A session without proof is OLD BUILD, excluded and named. "The installed copy matches the checkout" proves only the file.

## 3. Exercise it in action

- Launch test chats with `mcp__chat-professor__new` on the real caller's model, effort and engine; one at a time, each audited before the next starts; wait on `mcp__chat-professor__status`.
- Drive the cycle the feature exists for, at realistic sizes, through its real trigger, several times per chat. The feature's own tool is never called by instruction, and no chat is pushed to a backstop or ceiling the feature exists to avoid. A trigger the model ignores is a finding: record when it fired and how late the response came.
- Exercise each edge case the intent implies once, as its own measured case: a user message typed mid-transition, a manual alternative to the feature, a sub-agent, a background process, a pending question, a changed or lifted rule, each failure mode of the feature's own I/O (cancel, timeout, spawn failure).
- A probe that could touch a real account, credential or the user's live state runs in a throwaway home (a fresh config dir or `CODEX_HOME`), never on a copied credential.
- Kill each test chat at its signal with `mcp__chat-professor__kill`; at the end `mcp__chat-professor__ls` shows none of them.

## 4. Audit per occurrence

Enumerate every occurrence in the window, each with its session and transcript line; log rows and census counts are the index you read from, never the result. For each occurrence, from the raw transcript:

- the feature's full output as the model received it, against the state before it (what it was meant to carry or do)
- every carried artifact diffed against disk; every quoted user word and timestamp checked against the transcript
- the stated next step against the first actions after; the recovery work after it (re-fetches, re-reads, repeated searches), counted with what each fetched
- any user signal: a correction, a repeated ask, anger
- the screen surface: `system` records (the `informational` subtype is the notice line the user sees), hook output, errors. Any line the user did not ask to see is a finding.

Each occurrence gets WORKED, DEGRADED or FAILED with its `{transcript}:{line}` evidence. An occurrence not read is NOT READ, never clean.

Instruments:

- Your own reads: the `chatlog` skill (`chatlog.py prep|read|find|entry`) and the `Read` tool, never a `general-purpose` agent.
- Many transcripts: `agent-investigator`, its mission carrying the expected-run list and this section's checks verbatim; its readers' rows are read from disk before a claim is relayed.
- The kit's `prep`/`read` drop `system` / `informational` records (counted under `DROPPED`, never rendered), and the investigator reads raw lines only on a kit fault: read those records yourself, `grep -n '"subtype":"informational"' {transcript}` for the lines, then `Read` at each, or name them NOT READ.

## 5. Symptom, fix, done

- A user-reported symptom is reproduced live before the fix and again after install and reload, across its whole class (every cause that puts the same line on screen), never only the pasted line.
- A finding the user asked fixed takes the project's own route: a test watched failing, the fix, reinstall, reload, the clean count back to 0.
- Done is 3 consecutive clean occurrences, or the count the owner names, each fully audited before the next run starts.

## Report

1. The verdict line: works, partly or broken, and the dominant failure.
2. Preconditions: tests, gates, log counts, the build proof per session.
3. Per-occurrence table: occurrence · `{transcript}:{line}` · WORKED / DEGRADED / FAILED · evidence · what it cost the user.
4. Patterns ranked by cost to the user.
5. NOT READ: every occurrence, session and record kind left unread, and each instrument gap hit.
6. Test chats launched and killed.

## Examples

- ✗ "26 stored, 26 landed, no error rows: healthy." ✓ 29 occurrences found and each read: the feature's output carried state but not the briefs, so the same searches repeated after every occurrence → partly.
- ✗ A test chat fed until it crosses the hard ceiling, so the backstop fires. ✓ Real-size tasks cross the real threshold three times; whether and how late the model responds is the measurement.
- ✗ "All installed copies match their checkouts: fixed." ✓ A chat started after the install carries the new version in its own session, and the symptom reproduced there before is gone for cancel, timeout and spawn failure alike.

"Works" rests only on occurrences read in live transcripts; a count, a test or a self-report never stands in for one.
