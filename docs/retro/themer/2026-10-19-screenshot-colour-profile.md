# 2026-10-19 — a screenshot pixel sampled without its colour profile named the wrong cause

Observed: the user's prompt background looked grey. The main chat sampled the screenshot raw and read `#303030`, the xterm-256 grey nearest the theme's `#221634`, and concluded a truecolor value was being quantised. The PNG embedded the display's ICC profile; converted to sRGB the pixel was `#373737`, Claude Code's built-in dark `userMessageBackground`. The real cause was a chat started before the theme switch, still on the built-in theme. A pane's ANSI capture (`herdr pane read --format ansi`, `tmux capture-pane -e`) showed the exact escape values and settled it.

Amend: .claude/agents/themer.md § Render defects — read a screenshot pixel only after converting it to sRGB from its embedded profile, and prefer the pane's ANSI capture over pixels whenever the pane can be read; a near-match to a palette index is a lead, never the proof.
