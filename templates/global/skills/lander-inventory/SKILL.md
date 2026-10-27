---
name: lander-inventory
description: Internal kit of flights-lander — lander-inventory.py lists what a diff left behind (LEFT, LITERAL, GONE rows) and computes the review's spawn PLAN. Not invoked directly — ask flights-lander.
visibility:
  visible-to:
    include: [flights-lander]
---

# lander-inventory

Run `python3 -I $HOME/.claude/skills/lander-inventory/lander-inventory.py {WORKTREE} {BASE} [HEAD] [--spec FILE …]`, the path written in full. Python 3 standard library; it reads the worktree through `git` and writes nothing. The diff is BASE against the working tree — commits, staged, unstaged and untracked work; with HEAD, the commits BASE..HEAD alone. Exit 0 prints the rows; exit 2 prints `FAILED` and the cause when git or the input breaks, or the usage line when an argument is missing.

- `LEFT`: a name the diff removed everywhere it touched, still in the tree outside history files.
- `LITERAL`: a name a code or data file dropped from inside a string or key on a replaced line.
- `GONE`: a file the diff deleted or renamed away, still named in the tree.
- `PLAN`: the spawn — `PLAN HUNTER` clusters (k = min(4, ceil(production diff lines / 600)), none at 400 lines or fewer), `PLAN WALKER` row ranges over each `--spec` file with 10 or more numbered rows (4 in all at most), `PLAN SUB-LANDER` ranges over the rows above by line number (6 at most).
