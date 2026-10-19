---
name: imagine-professor-theme
description: 'Authors an all-surface pfm profile — `/imagine-professor-theme [concept|name]` → /imagine, or asks like "make a new pfm theme", "design me a theme". Returns a new templates/themes/{name}/ directory and how to see it. Not for switching → `pfm theme use`, editing → the `pcm` agent.'
argument-hint: '[concept|name]'
---

# Imagine a Professor theme

The concept: $ARGUMENTS — with none, the theme is the maker's own.

## The check

`pfm theme check {dir}` answers three ways:

- pass: an `ok` row naming the directory;
- `FAIL {error}`: the error names the file and the key or line to fix;
- anything else — `pfm` not found, `unknown subcommand "check"`, no `ok` or `FAIL` row: the check did not run. Stop, report `BLOCKED — pfm theme check did not run` with its output verbatim, and claim no pass.

## Steps

1. Record `git status --porcelain`, then run § The check on the reference profile `templates/themes/tokyo-night`; anything but its `ok` row stops here, `BLOCKED` with the output verbatim.
2. Load `/imagine` with the Skill tool, the concept as its work; a load that fails stops here: `BLOCKED — /imagine: {cause}`. It runs as written, its brief fixing these edges:
   - Target and publish route: the build written as the new directory `templates/themes/{name}/`, its full file set, in the main checkout. `{name}` is bare kebab-case, taken by no directory in `templates/themes/` and no file in `~/.claude/themes/`; a taken name the user gave goes back to them in one question.
   - Build directory: `build/` beside `brief.md`.
   - Law, read whole: the shape and authoring rules in `templates/themes/README.md` § Profiles and `docs/design/engines/theme-sync.md` § Profile pins; the reference profile above.
   - Scope, every surface: `claude.json`, a full VS Code theme (`vscode/workbench/*.json`, `vscode/syntax/*.json` one file per family, `vscode/semantic.json`), and the `tmux.json` and `herdr.json` pins.
   - Acceptance: § The check passes on the target.
   - Out: every other file, the README's theme list and `templates/refresh-map.json` included (install finds a profile by scanning); `pfm install`, `pfm theme use`, live settings and git.
3. The look adds § The check on the target, read from disk: a `FAIL` row goes verbatim to the same builder (`SendMessage`), and the check reruns on its return until `ok`. Against step 1's record, `git status --porcelain` shows `?? templates/themes/{name}/` as the one new path; any other change is named in the report.
4. `/imagine` § Deliver, its first line how to see it: `pfm install`, reload VS Code's windows, then pick the `ok` row's label (`Professor · {its name}`) in Preferences: Color Theme, or run `pfm theme use {name}`. Claude Code, VS Code, tmux and Herdr stay unseen until the user does, and the report says so.

The directory ends uncommitted: a commit is `gitter`'s, on the user's ask.
