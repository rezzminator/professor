# Rules design

| Topic | File | Covers |
| --- | --- | --- |
| rules | [rules.md](rules.md) | Claude Code `.claude/rules/` carried to Codex and OpenCode: build status, the one source, `templates/global/rules/` linked by `pfm install` and its `doctor: rules` rows (built), global always-on rules in an owned Codex `AGENTS.md` block and an OpenCode `instructions` entry (built), project always-on rules compiled into `AGENTS.md` and path-scoped rules injected by `engine-sync hook` (designed), agent targeting by the rule-control plugin's `visible-to:` key (built, Claude Code only) and its engine-sync gap, freshness, checks, the canary probe, verified engine facts, open items |
