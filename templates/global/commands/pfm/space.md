---
# professor: SOURCE TEMPLATE — edit here for a framework change (routes through the `pcm` agent); project-scaffold customization belongs in its installed local source; engine mirrors are never hand-edited.
name: pfm:space
description: Moves this chat to a Herdr workspace — `/pfm:space <name>` joins the named space and keeps it there, bare `/pfm:space` returns it to its project space; asks like "move this chat to glass", "group this chat under X". Returns pfm's one output line.
argument-hint: "[name]"
---

# Space

Arguments: $ARGUMENTS

A chat joins a space only by this command; a new chat starts in its folder-derived project space and never inherits one. A joined chat stays in its space through reload, resume, restore and reconcile, and the picker's rail groups it under the space's name. Outside Herdr the space is still recorded and grouped in the rail.

## Run

- `<name>`: run `pfm chat space '<name>'`, the name quoted for the shell. It records the space for this chat, creates the Herdr workspace of that name when missing, and moves this chat's tab there.
- No argument: run `pfm chat space`. It clears the space and returns the chat to its project space.

Relay the verb's one output line as printed; outside Herdr it says the chat is not in Herdr. On a non-zero exit, relay its stderr verbatim and report nothing as done.
