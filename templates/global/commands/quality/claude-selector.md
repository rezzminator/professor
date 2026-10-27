---
name: quality:claude-selector
description: Designs arrow-key pickers — menus a Claude Code user steers; "a small menu over the prompt bar", "build a picker"; `design <what>` picks the site and specs one, `check <path…>` certifies one (APPROVED/REJECTED). Returns a spec or a verdict. Mod mechanics → plugin-authoring.
argument-hint: "[design <what> | check <path…>]"
---

# Selector Law

This law governs where a selector draws, how keys reach it, what it costs, how a pick lands, how it ends, and how it is proven; `/quality:prompt` governs any prompt text it ships. Engine and tmux facts here were verified on Claude Code 2.1.300 and tmux 3.7c.

## Contents

- Modes
- Site first
- Keyboard and focus
- Cost and completion
- Mod gates
- tmux anchoring
- Terminal craft
- Verification
- Approval — certify a selector

## Modes

- No argument: the law, applied while building.
- `design <what>`: § Site first, then a spec naming the site and its rung's limits, row count, keys, the on-screen focus cue, model-turn cost, how a pick completes, every exit path, and the § Verification matrix it must pass. Returns the spec; builds nothing.
- `check <path…>`: edits nothing; run § Approval over each selector, driving its live checks per § Verification.

## Site first

Settle where the selector draws before any code; when the request does not name it, ask "where does it draw?". A selector for a Claude Code user is a small menu over the prompt bar: 12 rows at most, anchored on the prompt bar, the look of the `/design` picker. Full-screen is a different product, built only on an explicit ask.

Take the first rung that fits:

1. `prompt.autocomplete` typeahead (mod hook): the arrow-key selector, the `/design` picker's engine. It fires for any non-empty token at the cursor, slash tokens included: rows `{ text, label, description }` returned for the token `/menu` list the moment the user types the command name; ↓, Tab, Enter and the ❯ are native, no focus chord.
2. `$.ui.ask` (the engine's AskUserQuestion dialog): 2–4 labels per question, no descriptions; page with a "More…" label. A typed "Other" returns verbatim — compare it to the labels exactly. The dialog adds its own "Chat about this" row, which likely starts a model turn.
3. `AbovePrompt` band (mod `ui.render`): below rungs 1–2 for any selector the user steers with arrows, since its keys arrive only after ctrl+x tab. The only site above the prompt at every width; one instance; `maxRows` caps it at half the terminal, prompt included. Yield when `hasSurvey`.
4. `Pane` via `$.ui.open({ focus: true, closeOnEscape: true, rows })`: docks beside the transcript in fullscreen from 110 columns, inline above the prompt otherwise; an unasked pane places only from 144 columns. `command.run` carries `presentation.isFullscreen` and `columns` — branch on them.
5. tmux `display-menu` or a content-sized `display-popup`, anchored on the prompt rule (§ tmux anchoring): any TUI host inside tmux.
6. Standalone terminal TUI (§ Terminal craft): only a CLI the user runs outside Claude Code. The Bash tool and the `!` shell have no TTY, so a TUI cannot run there; a tmux popup gives it one.

## Keyboard and focus

- Typeahead rows follow the engine's own rows and start unselected; a hook cannot put them first. The engine matches sibling commands by name and description, and each match lists above your rows: give the selector a name and description no sibling's shares, and keep every description in the list to one line, since a wrapped one uses up the few visible rows.
- Typeahead drill-in: taking a row writes its `text` over the token and asks again for the new token, so a row `/menu group` leads to the `group` token, where the hook returns its children. A `text` ending in a space leaves an empty token and no rows.
- No API hands a band the keyboard: `$.ui.focus` denies a site not already holding keys, and `autoFocus` applies only once the person handed keys over. The person's ctrl+x tab, a click, or a pane opened with `focus` hands them.
- The band and a `Client` are secondary surfaces beside a typeahead or `$.ui.ask`, never the arrow-key selector alone; their first visible line names the key that steers them ("ctrl+x tab steers"), since a selector needing an unannounced focus chord reads as broken.
- The zero-friction band pick is a Button `hotkey`: a bare digit typed into an empty prompt presses it. Draw it as `1: label`. Typed into a draft, the digit becomes argument text (`/menu 4`), and the transcript row shows `/menu` without it: answer an unknown argument with a message naming it.
- `prompt.edit` sees no arrows on an empty draft (history takes ↑↓); on a non-empty draft it can consume keys.
- A `Client` surface takes keys only after a click — ctrl+x tab does not reach it — and Esc never arrives (it returns focus): close on ← or a button. Injected text arrives as one multi-character key event.
- In a focused pane arrows scroll the body and Tab walks buttons; a `ui.scroll` hook turns arrows into row moves only while the body overflows.

## Cost and completion

- Launch from a mod command: a `command.run` hook answering without `next` runs no model turn, while a `.md` slash command with `!`-bash always costs one.
- Return `{}` to open a menu. A `{ text }` answer becomes a transcript row the model reads next turn; only a result (a fake run) returns text.
- A pick completes the prompt and leaves it unsent, the `/design` UX: `$.prompt.fill({ text, mode: "replace" })` in a mod, `send-keys -t %N -l 'text'` (literal, no Enter) from tmux. `isFilled` is false while a dialog is up or headless — fill after `$.ui.ask` resolves.

## Mod gates

- Pass `$` only to functions declared at module top level; a helper defined inside `register` fails the load.
- Register each command with a literal `$.command.register({ name: 'x' })` call. A loop over an array is classed a gating hook, and its `command.run` no longer answers its own command.
- `$.process.run` inherits the session env and takes `env` with string-literal names; it rejects when the program cannot start — `fs.stat` the program first and toast "missing".
- Hooking `ui.close` and then calling `$.ui.close` yourself skips your own hook as re-entry.
- The test kit has no default `ui.close` or `ui.log`; `$.prompt.autocomplete` runs in `claude plugin test` but is missing from the typings, so a test casts to call it. The kit's `find` matches whole `Text` nodes: merge same-style runs, one node per cell only where a gradient needs it.
- Engine-drawn rows take single-width glyphs; an emoji with VS16 (✈️) breaks column alignment.
- Run in order: `claude plugin validate`, `tsc -p`, `claude plugin test` with a test per command, then live via `claude --plugin-dir` in a tmux session, the debug log free of `ui.render (...) refused` lines.

## tmux anchoring

- Coordinates: `-x` is the left column; `-y` is the row just below the bottom edge (the box fills y-h to y-1). Both are client coordinates — a top status line shifts rows — while `pane_top` and `pane_left` are window coordinates: add them. Out-of-range values clamp. Specials: `P` (bottom on the pane's last row, left on `pane_left`), `C` centre, `S`/`W` above the bottom status line, `M` top, `R` right edge; formats like `#{popup_pane_top}` already include the box height.
- The prompt bar: in fullscreen `cursor_y` is the ❯ line, and the top rule is the nearest all-`─` line at or above it — search upward, since a multi-line draft pushes the cursor down. Read the rule's colour with `capture-pane -e -S r -E r` and draw the frame in it, so the box rests on the rule.
- `display-menu` and `display-popup` block the calling client until closed: background them with stdin, stdout and stderr redirected, then watch the job about 0.2 s to catch an argument error (exit 1 at once).
- `display-message -p -t %99` exits 0 with empty formats for a missing pane: verify `pane_id` equals the requested one.
- Popups keep a border: a borderless popup (`-B`, `-b none`) opened after a key-dismissed popup whose command had exited kills the tmux server.
- Resize with one command list, `display-popup -C -c X \; display-popup {new size}`; no in-place resize exists.
- `display-menu` styling: `-H 'bg=terminal'`, since `default` inherits menu-selected-style's yellow. An inline `#[...]` style overrides `-H`, so a coloured name stays uncoloured when selected; bare `#[dim]` keeps the selection colour. One style run per row and no `#[default]`: a `#[fg=#hex]…#[default]` row truncates with `>` at 80 columns though it fits. The shortcut always renders as a right-aligned `(k)`; BSpace as a key renders `(BSpace)`. `""` is a separator; height = items + separators + 2. The selected row shows by style only — no ❯ moves with it.
- Target an explicit server (TMUX passed via `-e`, or `-S socket`); a bare `tmux` without TMUX hits the default server.

## Terminal craft

- Inline (no alt-screen) when the picker is a step inside a shell flow: draw below the cursor, erase on exit, leave one summary line (`❯ flights land`, `✕ cancelled`). Alt-screen only for a full-screen product.
- Every frame is one write wrapped in synchronized output (DEC mode 2026) from a diffed cell buffer; zero bytes outside the brackets except the final summary and the restore.
- Measure cell width with a real width function (Go: `rivo/uniseg` or `x/ansi` StringWidth; zsh: `${(m)#s}`) and place columns by absolute cursor position after any wide glyph, since terminals disagree on emoji+VS16 widths. Truncate with `…`, never wrap.
- Colour: truecolor when `COLORTERM` says so, 256-colour fallback; under `NO_COLOR`, bold and reverse only and no animation.
- Restore on every path — q, Esc, ctrl-c, Enter-run, SIGTERM, panic (defer/recover): alt-screen off, mouse off, cursor visible, `stty -a` byte-identical to before. macOS flips `pendin` on raw-mode switches and `stty -g` does not record it: call cleanup explicitly on cancel paths and in signal traps, not only from the EXIT trap.
- I/O contract: draw on `/dev/tty`, print the choice to stdout so `x=$(pick …)` works; exit 0 chosen, 1 cancelled, 2 usage or no TTY, a non-TTY stdin or stdout getting a clear error.
- Esc versus arrow: after ESC, read again with a 30–50 ms timeout to assemble `ESC [ A`; a key inside that window reads as an alt-chord.
- zsh: TRAPWINCH does not fire while blocked in `read`, so poll the size about every 0.2 s when idle; `$E[` parses as a subscript (write `${E}[`); `IFS=$'\t' read` drops an empty first field (split on `\x1f`); in a popup, print the last row without a newline.
- Animation idles to 0 fps (0.0% CPU at rest under `ps -o %cpu,time`); `NO_COLOR` or 10 s idle stops it.
- Keys: ↑↓ and ctrl-p/ctrl-n with wrap, Home/End, PgUp/PgDn, digits jump, type-to-filter with matched characters highlighted and the best match first, →/Tab drills in with a breadcrumb, ← or Backspace on an empty filter goes back, Esc clears the filter and then quits, ctrl-c always quits, a danger item confirms inline in red, a usage hint under the list, `›` on items with children.
- A top-level fuzzy query may search the whole tree (`fl la` → `flights land`); description matches need 3+ characters.

## Verification

- A selector is verified in a real pty: a tmux session the test creates (`-L` own server, or a named session), driven by `send-keys` and read by `capture-pane -p` (`-e` for colour). A nested pair (`-L outer` running `tmux -L inner`) shows popups and menus without touching the user's screen. Kill only what the test created.
- Watch, never claim: each key, each exit path, each size (40×12, 80×30, 115×45, split panes), resize, `NO_COLOR`, non-TTY.
- Smoothness cannot be watched: report it NOT VERIFIED, and prove motion by captures that differ 100–300 ms apart.
- Testing a Claude Code selector submits no prompt to the model; slash commands only.

## Approval — certify a selector

A selector is APPROVED only when every check that applies to its site holds; otherwise REJECTED with the failing checks named, fixed and re-checked. A check needing a live run that was not run fails; a check about a site the selector does not use reads `n/a`.

- 1 Site: the site was not settled before code, a higher rung that fits was passed over, a prompt-bar selector exceeds 12 rows, or it goes full-screen without an explicit ask.
- 2 Focus: a first-time user's ↑/↓, pressed right after invoking the selector, does not move the selection (watched live, no focus chord sent); a band or `Client` stands alone as the selector or its first visible line does not name its key; the build relies on an API to grab the band's keys; or a `Client` surface closes only on Esc.
- 3 Cost: the launcher runs a model turn (a `.md` command with `!`-bash, a `command.run` answer carrying `next`), or opening the menu answers `{ text }`.
- 4 Completion: a pick submits instead of filling the prompt unsent, the fill runs before `$.ui.ask` resolves, a typed "Other" is matched loosely, or an unknown argument passes without a message naming it.
- 5 Mod gates: a § Mod gates rule is broken, or one of its gate commands did not run.
- 6 tmux: coordinates skip the status-line or pane offset, a popup is borderless, a blocking call runs in the foreground, a pane id goes unverified, a bare `tmux` reaches the default server, or a § tmux anchoring styling rule is broken.
- 7 Craft: a restore path is missing, width is measured in bytes or runes, a row wraps, colour ignores `COLORTERM` or `NO_COLOR`, the exit codes or the non-TTY error depart from the I/O contract, or animation burns CPU at rest.
- 8 Keys: a terminal picker lacks a key from § Terminal craft's key set.
- 9 Verification: a § Verification matrix row is claimed rather than watched, smoothness is reported verified, the test submitted a model prompt, or it touched a tmux server or session it did not create.

Emit `APPROVED: {path}`, `REJECTED: {path} — checks {n,…}`, or `UNREAD: {path} — {error}` when the source could not be read — only a selector read earns a verdict.
