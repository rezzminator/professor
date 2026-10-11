---
# professor: SOURCE TEMPLATE — edit here for a framework change (routes through /pcm); project-scaffold customization belongs in its installed local source; engine mirrors are never hand-edited.
name: pfm:schedule
description: Arms a wake-up for this chat — repeating `/pfm:schedule {weekly|<N>d|<duration>} [what to do]`, once `/pfm:schedule in <delay> [what]` or `at <time> [what]`, plus `ls`, `rm <id>`; asks like "check this again weekly", "remind me in 30 days", "wake me at 09:00". Returns the schedule id and its next fire time.
argument-hint: "{weekly|<N>d|<duration>} [what to do] | in <delay> [what to do] | at <time> [what to do] | ls | rm <id>"
---

# Schedule

Arguments: $ARGUMENTS

Each fire, pfm wakes this chat, resuming it first when it has exited, types the stored prompt into it, and pins its picker row red at the top until the user opens it. The chat itself stays down between fires. A one-time schedule fires once and leaves `ls` when the user opens the chat.

## Route by the first word

- `ls`: run `pfm chat schedule ls` and show its rows; a one-time schedule reads `once` in its `EVERY` column.
- `rm <id>`: run `pfm chat schedule rm <id>` and report what it removed.
- `in <delay>`: one-time, fires once after the delay, which takes the interval forms below.
- `at <time>`: one-time, fires once at that local time: `HH:MM` (its next occurrence), `YYYY-MM-DD HH:MM` (two words), or RFC 3339; at least `1m` ahead.
- Anything else is an interval, repeating: `weekly`, `<N>d` (`15d`, `30d`), or a duration of at least `1m` such as `12h`. Anything other than these is a usage error: show the argument hint and stop.

## Arm

1. Write the prompt the woken chat will receive. It may arrive weeks later, possibly after compaction, so it stands alone:
   - opens with `Schedule ({interval}):` when repeating, `Schedule (once, {delay or time}):` when one-time;
   - names the task: the words after the timing, or, when none were given, the work this chat did that the user wants redone;
   - carries every exact step to redo it: commands, paths, the files to read;
   - carries today's result as the baseline to compare against: the numbers, the date, the verdict;
   - ends with what to report: the new result next to the baseline, and what changed.
2. Arm it for this chat with its route's timing flag — `--every {interval}`, `--in {delay}` or `--at '{time}'`: `pfm chat schedule set {timing flag} --prompt '{prompt}'`. Quote the prompt for the shell. It prints the new id alone; a non-zero exit means nothing is armed: show its stderr and stop.
3. Read the new row from `pfm chat schedule ls` and report its id, interval or `once`, next fire time and the prompt's first line.
