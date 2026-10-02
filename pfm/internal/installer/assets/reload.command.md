---
name: reload
description: 'USER-ONLY — the user types /reload; never run this without the user''s permission. {{RELOAD_USAGE}}'
---

# `/reload [--account N] [--model M] [--effort E] [--cache 1h|5m (or --1h, --5m, --cache on|off)] [--new [--hide]] [--then "<prompt>"]` — reboot this chat in place

Run this ONCE via the Bash tool — and make it your LAST action, the chat is about to exit:

```
~/.local/bin/pfm chat reload [--account N] [--model M] [--effort E] [--cache 1h|5m (or --1h, --5m, --cache on|off)] [--then "<prompt>"]
```

**Every setting has a flag. There are no positional arguments.** Whatever words the request
used, map them to a flag first:

| the request says | pass |
| --- | --- |
| "cache off", "5m cache", "short cache" | `--cache 5m` |
| "cache on", "1h cache", "long cache" | `--cache 1h` |
| "account 2", "switch seats", "other account" | `--account 2` |
| "then continue with X" | `--then "X"` |
| "on opus", "switch to sonnet", "reload as <model>" | `--model <model>` |
| "high effort", "think harder", "low effort" | `--effort high` / `--effort low` |
| "fresh", "new conversation", "start over here" | `--new` |
| "fresh and hide the old one", "replace this chat" | `--new --hide` |
| nothing in particular | no flags at all |

`pfm chat reload cache off` is not a call — `cache` is not an argument, and the command will
refuse it. If a rejection ever comes back, read the flag it names rather than reshuffling the
words.

**Do not pass `--sock`.** With no `--sock`, the command finds the CALLING chat's own pane by
itself — Claude panes from tmux, Codex from the fleet-bound thread identity. `--sock` exists only
to reboot a DIFFERENT chat from outside it, which is not what this command is for.

**Typed by the human, `/reload …` never reaches the model** — the `pfm internal reload-intercept`
UserPromptSubmit hook executes it and blocks the prompt (Claude seats only; a Codex seat still
routes through this body). This body is for the calls the user asked the model to make — a limit
rescue, a config change that needs a fresh session.

With no flags, the current engine account is preserved. The chat auto-exits and reboots in the
same window and pane; split siblings are untouched. With `--then`, the script waits for the reborn
chat's input box, then types and submits the prompt.

After running it, reply with ONE short line and END YOUR TURN immediately. The worker types `/exit`
only once your turn has ended (it holds up to two minutes for the pane to go idle, then refuses
without typing), confirms Claude Code's "background work is running" exit dialog itself, and if
the chat still has not exited after 20s it takes back whatever it put on the screen — the dialog
or the typed `/exit` — leaves the chat running, and says so in `reload-<socket>.log`. In-flight
sub-agents, background shells, and session crons die with the reboot.

## Cache-only reboot — `/reload --cache 1h|5m`

For Claude, `--cache` selects the reborn chat's starting prompt-cache TTL,
handed to the cache-live-control plugin as `CACHE_LIVE_CONTROL_MAIN_TTL=1h|5m`;
the plugin owns every TTL from then on, main chat and sub-agents alike.
With no `--account`, `/reload --cache 5m` keeps the current account.
With no `--cache`, reload carries the chat's recorded cache choice. A chat
without a launch record uses its selected account's configured cache.

## Fresh conversation — `/reload --new [--hide]`

`--new` reboots into a NEW session id in the same pane, account, and cwd — the old conversation
is untouched and stays resumable from the picker. Add `--hide` and the conversation left behind is
hidden from the picker instead (a permanent kill recorded once the reboot completes — never before,
so a reload that fails leaves the live chat listed; `pfm chat unkill <id>` brings it back).
`--hide` needs `--new`: a reload that resumes the same conversation cannot hide it. Pairs with
`--then`: reboot fresh, hide the chat being replaced, hand the reborn chat its first prompt.

## Model and effort — `/reload --model M --effort E`

Both pin what the REBORN pane is born with; neither changes the conversation. `--model` takes the
engine's own model name. `--effort` takes one of `low`, `medium`, `high`, `xhigh`, `max` on Claude,
or `minimal`, `low`, `medium`, `high`, `xhigh`, `max`, `ultra` on Codex — an unknown value is
refused by name with the accepted set, never silently dropped. Omit either and the seat keeps what
it is running now. The same two flags spell the same thing on `pfm chat new`, so a seat's tier is
requested identically whether it is being born or rebooted.

## Reloading onto another account (limit rescue)

When the user sends you to another account because the current one's usage limit is nearly
exhausted and work remains:

1. **Land in-flight work first** — sub-agents, workflows, and background tasks do NOT survive the
   reboot. Finish or checkpoint them; never reload mid-flight.
2. **Pick a different configured account for the current engine** from `pfm config show`.
3. **Reload with a baton prompt**:
   `~/.local/bin/pfm chat reload --account <other-n> --then "Continue: <what you were doing + the next concrete step>"`
4. One short line to the user (which account you moved to and why), end turn. The reborn you
   reads the `--then` prompt and continues on the fresh account's budget.
