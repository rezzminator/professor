# 2026-10-16 — the Herdr state reporter for pfm chats lives outside the repo

Observed: pfm runs Claude inside its own tmux, so Herdr's screen detection cannot see the agent. A hand-installed `~/.claude/hooks/herdr-pfm-state.py`, wired by hand into the shared `settings.json`, reports state through `pane.report_agent` as `pfm-claude`. Nothing in the repo ships, tests or checks it. A session nearly ran `herdr integration install claude`, whose bundled hook claims the pane without reporting state and would have locked that reporter out.

Amend: judgment — move the reporter into the pfm-installed host assets, pass it through the launch `--settings` hooks, add a resume command (`pane.report_agent` with `resume_argv`), and add a `pfm doctor` row that fails when Herdr's bundled Claude hook is installed.
