---
name: close-out
description: 'USER-ONLY Finishes a chat completely — /close-out, "wrap this chat up", "anything left before I close?": sweeps open threads, unfinished or unverified work, uncommitted changes, running agents; does what needs no ruling, asks the rest. Returns the closing report.'
---

# close-out

The user is leaving this chat for good. Every item the sweep finds ends one of three ways: done and verified, ruled by the user, or named in the report with what it needs.

## 1. Sweep

Walk the whole conversation from its first message, not the last few turns, and build one ledger. Check with tools, not memory:

- Asks: every request the user made, each part of each; one counts as done only where the transcript shows it done and verified.
- Promises: "I'll…", "next step", "later", deferred items, TODOs this chat left in files, questions asked and never answered, by either side.
- Claims: every "done", "passes", "fixed" — backed by a run watched in this chat, or open.
- Trees: in every repository this chat touched, `git status --short`, commits not on the upstream, the branch it sits on, scratch files the chat left inside the tree.
- Background: sub-agents whose return has not arrived or was never read, background shells, monitors, scheduled wake-ups and crons, workflows, servers this chat started.
- Inbox: pending notifications, read with the harness's notification tool where it has one.
- Lessons: a fact the next session must know — a gotcha found, a decision with its reason. Where the project's `CLAUDE.md` names a ledger for them (a retro inbox, a memory file), it goes there.

A check that could not run (no tool, an error) is an open item, never a clean one.

## 2. Sort

- Do: the request or the project's rules already settle it — finish it now.
- Ask: only the user can rule — a choice between real alternatives, a change of scope, and every outward-facing or destructive act (push, publish, deploy, delete beyond this chat's own scratch, send a message, spend money, change an account) the user has not authorized in this chat.
- Tell: nothing to do, but the user must know it — a risk, a caveat, a dissent, a cost.

## 3. Do

Finish every Do item through the project's own gates and verify each by running it; retry your own errors. Background work this chat started is awaited with one blocking wait when its result still matters, stopped when it does not — nothing stays running unannounced. Remove the scratch this chat created. Commit only where the project's rules let you commit unasked; otherwise the commit is an Ask.

## 4. Ask

Put every Ask item to the user at once with AskUserQuestion: each question carries its own context, options ranked with the recommendation first; more than four questions go most consequential first, each later round simpler. Carry out the rulings, then re-sweep only what they touched. A user who declines to rule leaves that item open.

## 5. Report

One final message, sections left out when empty:

1. Done: each item with its proof — the command and its result.
2. Ruled: each ruling and what it changed.
3. Still open: the item, why it is open, what it needs and from whom.
4. Before you go: the Tell items.

Close with "Nothing left open." only when section 3 is empty and every sweep check ran.

### Example — a promise in the middle of the chat

Turn 4 said "I'll add a test for the empty case later" and no later turn did. Do: write the test, watch it fail against the unfixed code where that still applies, run it green, list it under Done.

### Example — a green change that is not published

The fix is committed on the work branch, tests watched green, nothing pushed. The commit stays as it is; the push is an Ask, unless the user asked for it in this chat.

### Example — an agent still running

A research agent spawned an hour ago has not returned. If its answer feeds an open item, wait for it and use it; if a later ruling made it moot, stop it and say so under Done.

### Example — a claim nobody watched

An earlier turn said "the build passes" without running it. Run it. Green → Done with the output line. Red inside this chat's scope → fix it. Red outside it → Ask.
