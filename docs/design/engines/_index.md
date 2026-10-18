# Engines design

| Topic | File | Covers |
| --- | --- | --- |
| pfm home | [pfm-home.md](pfm-home.md) | `{clone}/pfm.config.json` (gitignored, seeded from `example.pfm.config.json`), the `chat.backend` birth backend, the two state databases `pfm.db` and `pfm-cache.db` and their config keys, pfm's state dir |
| Claude launch | [claude-launch.md](claude-launch.md) | The launch registry `claudelaunch.Knobs`: every flag, env var and `--settings` key a Claude chat starts with, where each value comes from, the doors including the Herdr seat door, the managed launcher, the zsh shim, `pfm config claude`, spawn-audit |
| Cost ledger | [cost-ledger.md](cost-ledger.md) | The per-call cost ledger: the exporters and loopback sink every Claude chat starts with, the `request_id` join, `{session_id}.jsonl`, side calls no transcript records, every number on both status lines and in `pfm tokens` with its source, `claude.costLedger`, the doctor row |
| Workbench | [workbench.md](workbench.md) | The nested sub-project marked by .professor/workbench.json: manifest, discovery, persona precedence across every launch door, mirrors, picker group and ✦ row, cache, doctor |
| Claude headless | [claude-headless.md](claude-headless.md) | `claude -p` through `headlessrun.Run`: config-free by design, argv, sealed mode, callers, environment, the harness-prompt drift probe |
| Claude config dir | [claude-config-dir.md](claude-config-dir.md) | The shared store in `~/.claude`, identity-only accounts, all shared-entry links, settings layers, MCP config, install build and doctor checks |
| Claude project tier | [claude-project.md](claude-project.md) | Project `.claude/settings.json` here and in the template, `settings-global.json`, the `outputStyle` gate, SETUP.md's manual steps |
| Host checks | [host-checks.md](host-checks.md) | Read-only detectors, install refusal, doctor rows and operator fixes |
| Theme sync | [theme-sync.md](theme-sync.md) | `pfm theme [status\|sync [--force]\|on\|off]`: Claude Code's `theme` as the read-only source and the governing account rule, the derivation table with the contrast lift, `~/.local/state/pfm/theme/`, the surfaces claude (source), tmux, herdr, vscode and follower with render, live reload, read-back and off, the verdicts including SOURCE, the generation and `lastFlip` latency line, the three tmux doors |
