---
# professor: SOURCE TEMPLATE — edit here for a framework change (routes through /pcm); project-scaffold customization belongs in its installed local source; engine mirrors are never hand-edited.
name: reminder
description: Arms a recurring wake-up for this chat — `/reminder {weekly|15d|30d|<N>d|<duration>} [what to do]`, `/reminder ls`, `/reminder rm <id>`, or asks like "remind me in 30 days", "check this again weekly". Returns the reminder id and its next fire time.
argument-hint: "{weekly|15d|30d|<N>d|<duration>} [what to do] | ls | rm <id>"
---

# Reminder

Arguments: $ARGUMENTS

Each fire, pfm wakes this chat, resuming it first when it has exited, types the stored prompt into it, and pins its picker row red at the top until the user opens it. The chat itself stays down between fires.

## Route by the first word

- `ls`: run `pfm chat reminder ls` and show its rows.
- `rm <id>`: run `pfm chat reminder rm <id>` and report what it removed.
- Anything else is an interval: `weekly`, `<N>d` (`15d`, `30d`), or a duration of at least `1m` such as `12h`. Anything other than these is a usage error: show the argument hint and stop.

## Arm

1. Write the prompt the woken chat will receive. It arrives weeks later, possibly after compaction, so it stands alone:
   - opens with `Reminder ({interval}):`;
   - names the task: the words after the interval, or, when none were given, the work this chat did that the user wants repeated;
   - carries every exact step to redo it: commands, paths, the files to read;
   - carries today's result as the baseline to compare against: the numbers, the date, the verdict;
   - ends with what to report: the new result next to the baseline, and what changed.
2. Arm it for this chat: `pfm chat reminder set --every {interval} --prompt '{prompt}'`. Quote the prompt for the shell. It prints the new id alone; a non-zero exit means nothing is armed: show its stderr and stop.
3. Read the new row from `pfm chat reminder ls` and report its id, interval, next fire time and the prompt's first line.
