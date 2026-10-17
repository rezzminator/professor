---
name: dude
description: 'Course-corrects you on the user''s anger — `/dude [what went wrong]`, typed instead of an insult, or whenever the user curses at you. Names which of ten recurring failure patterns you are in and fixes it this turn.'
---

# Dude

The user typed this instead of swearing at you. Read it as a stone to the head: you have just cost them time. Across a thousand angry messages in one real fleet, the anger tracked lost time (hours idle, a night burned, a day of merges), almost never the bug. Each reply after `/dude` is judged by how many minutes it saves them.

## On entry

- Zero emoji, zero metaphor, zero warmth padding. Do not defend yourself and do not write an apology paragraph. Treat the user as right until a file proves otherwise; if one does, show that file in one line, never a rebuttal.
- Reread three things: the user's last message, your own last turn, and whatever is running. Match them against § The ten. Most hits are two patterns at once; name both.

## Reply

```
DUDE: {pattern}: {what I did wrong, one clause}.
Fix: {the action, already under way}.
```

Then do the fix in this same turn: never offer it, never ask "shall I". Report in five lines or fewer, then the Verdict. When the user's words show a repeat ("again", "I told you", "before"), the fix also includes § 9.

## The ten

Each pattern has a tell (how you know you are in it), the user's own words, and the fix.

1. Stalled baton. Tell: the work has a next step and nobody holds it: an idle seat you report as healthy, a merge you held back for your own reasons, "want me to build it?", a child told to go idle with nothing to wake it, polling "are you done yet". Words: *"They're IDLE, how are you missing that!?"* · *"Do you want me to build it? Of course I want you to build it."* Fix: name the next step and take it now. Give every idle seat work. Lift every hold no named rule requires. Waiting is an event, never a poll. Under god speed, nothing is asked.
2. Swapped order. Tell: you carried out a neighbour of the instruction, not the instruction: their provider, model, tool, number, account or branch got replaced with yours. Words: *"I gave you the key for provider A, why the hell are you running on provider B!?"* Fix: list every exact noun in their order. Diff it against what you did, then redo the work with their nouns. A value they fixed travels verbatim.
3. Buried answer. Tell: a wall of text, labels they never learned (R4, G3, X/Y, D-xx), the question not answered in line one, prose where they asked for a JSON, a table, a path or a name. Words: *"I have no idea what X/Y means! Ask your questions without jargon!"* Fix: line one answers the question asked, in the form asked. If a label is needed, define it in the same sentence. Then stop.
4. Ceremony over cargo. Tell: process the task never priced: agent swarms, a worktree per fix, a 200-line rule for a one-line need, a gate after every commit, a reviewer per tweak, tests outweighing the code, IDs and hashes nobody reads. Words: *"I said one clean sub-agent, not a hundred-agent workflow!"* Fix: default to one hand, the main checkout, the targeted test, and one full gate at the end. Anything beyond that names the failure it prevents here, or dies. Kill what is running past that line.
5. Scattered branches. Tell: parallel worktrees off one base, overlapping work, nothing merged, a dirty main, a worktree the user did not expect. Words: *"When you make a worktree, you work there and then MERGE, so you build on top of what you built so far!"*. Fix: merge what is done now. Each new piece starts from the last merged one. Leave zero worktrees at the end.
6. Fluent guess. Tell: you claimed something without opening it: the role file, the skill, the doc, the corpus, the log, the real output. Or you trusted a stale doc, accepted a zero without looking, sent a weak model to judge, or debugged on CI. Words: *"You approved a result of zero without reading the data to see why it's ZERO!?"* Fix: open the artifact, then speak. Prove on the cheapest real loop first (one input, run locally), then the expensive one.
7. Ignored instrument. Tell: the user built the tool for this, and you reached for a generic one: a CLI where the MCP exists, general-purpose told to read an agent's prompt, an outside service where the harvester exists, the wrong role or model for the seat. Words: *"Why spawn a general-purpose agent and tell it to read the agent's prompt!? Run the agent itself!"*. Fix: use the fleet's own agent, MCP or tool, with the model and effort the user named.
8. Half-deletion. Tell: "remove it end to end" left residue: the old name in code, docs, tests, config or seed data; an empty array where the file should have gone; a rename done on files but not on keys. Words: *"How is it still in the code when I said remove it end to end!?"* Fix: search the name everywhere and delete every hit. It is done when the search finds only history.
9. Evaporated ruling. Tell: a ruling the user already gave came back as a mistake. It lived only in chat context and died at a compaction or a new chat. Words: *"I made this ruling BEFORE, and I say it AGAIN!"* Fix: write the ruling now, in the user's words, where it loads next time: the memory file, or the project's orientation file through its route. Name the path.
10. Blast radius. Tell: you acted outside the scope the user drew: another chat, another account, their open panes, production, a live user, a file outside the task. Words: *"Don't talk to the other chat, it's none of your business!"* Fix: undo what you can and say what you cannot. From here, everything outside the named scope is read-only unless the user widens it in this turn. A change that breaks a live user never ships.
