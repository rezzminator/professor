# 2026-10-14 — stray identity files in the shared Claude store

Observed: every stray `~/.claude/.claude.json` (2026-10-13 21:32:29, 22:35:24, 23:32:27; 2026-10-14 14:33:21) was born within a second of a chat running `CLAUDE_CONFIG_DIR=$HOME/.claude claude plugin …` to refresh a dev plugin; no file teaches it, agents improvise it, and `make host-install` refuses until the files are moved aside. Each chat's own config dir already reaches the shared plugin store through its `plugins` and `settings.json` symlinks.

Amend: a `claude` run never names the shared store as its config dir; plugin refreshes run under the chat's own `CLAUDE_CONFIG_DIR` (store-config-guard flight builds the refusal).
