# RR — Claude Code skills after compaction
Question: does Claude Code's own context compaction (manual /compact and automatic auto-compact) lose skills that were invoked earlier in the session — i.e. after the compaction, is the invoked skill's body (SKILL.md content) still in the model's context, re-attached, or summarized away?
Determine retention, date version changes, distinguish skill listings from invoked bodies, and establish limits and failures.
Version history — settled when exact changelog version lines establish retention changes.
Lens: release engineering — method: compare published release entries; sources: official CHANGELOG.
Documented lifecycle — settled when official skills, memory and hooks pages establish compaction behavior and hook scope.
Lens: technical specification — method: reconcile lifecycle contracts; sources: official documentation.
Mechanism and limits — settled when primary evidence establishes invoked_skills attachments, budgets, truncation and subagent scope or explicitly identifies missing evidence.
Lens: software implementation — method: inspect primary technical artifacts; sources: official repository issues and published artifacts.
Reported failures — settled when relevant issues have fetched reports, current state and maintainer responses distinguished from reporter claims.
Lens: incident analysis — method: compare reproduction and resolution evidence; sources: anthropics/claude-code issues.
Opening: fetched first 100000 characters of raw CHANGELOG; 2.1.304 resends skill list, 2.1.299 reload-skills listing behavior; older body retention entries not yet read.
