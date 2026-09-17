# Wave 0 — `/reload` tells the truth

**Diagnosed on a live host (Claude Code 2.1.268):** `/reload` works — the pane is respawned onto the requested seat — but its feedback lies twice. `pfm/internal/hookentry/prompt_block.go` answers a SUCCESSFUL `/reload` with `{"decision":"block","reason":"","suppressOriginalPrompt":true}`, which the harness now renders as `UserPromptSubmit operation blocked by hook: Blocked by hook` — the failure path's word. And a `/reload` typed mid-turn makes the worker hold `/exit` until the turn ends (`pfm/internal/reload/reload.go` `waitCallerIdle`, up to `IdleTries` polls) with the hold announced only in `/tmp/cc-sid/reload-<socket>.log`; the screen shows nothing.

## Fix

- `prompt_block.go`: `blockPrompt(stdout, reason)` marshals `{"decision":"block","reason":<reason>,"suppressOriginalPrompt":true}` with `encoding/json`; every caller of the old quiet helper is listed in the report; only the reload path changes its reason unless another caller's truthful one-liner is obvious.
- `reload_intercept.go`: success reason = `reload scheduled — this chat reboots when the current turn ends (<the front's own "reload scheduled in place (log …)" line>)`. Failure path unchanged (exit 2, captured text).
- `reload.go` `waitCallerIdle`: on first busy detection also `tmux.Display(… "pfm reload: waiting for this turn to end, then rebooting this chat")`; before `/exit` is typed, `Display("pfm reload: rebooting now")`. Display errors are logged, never fatal.

## Proof

Red first (both tests fail on the unfixed tree; logs `tmp/logs/W0-red.log`): the intercept test asserting a non-empty `reason` containing `reload scheduled` and the front's line; the reload test (fake tmux: busy, busy, idle) asserting both Display messages in order. Then green (`W0-green.log`), then `dev.sh iso test pfm` + `iso verify pfm` on the committed slice (`W0-test.log`, `W0-verify.log`). Codex path (no UserPromptSubmit hook) unchanged.

## Lane overlap (Wave 4 reads this)

The engine lanes' `/reload` matrix and the ops lane's reload-while-busy beat both assert the on-pane hold notice — the seam this wave adds is exercised from two lanes on purpose.
