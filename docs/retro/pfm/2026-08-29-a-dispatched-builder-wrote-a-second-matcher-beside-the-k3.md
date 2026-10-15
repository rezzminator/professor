# 2026-08-29 — a dispatched builder wrote a second matcher beside the K3 original

Observed: the spawn-door consolidation brief told an opus builder to classify live Claude processes; it wrote its own `isClaudeArgv` (basename == "claude") instead of reusing `gather.IsClaudeCommand`, whose K3 comment says "one spelling of it". Live seats run the version-named binary (`…/claude/versions/2.1.250`), so the audit reported an 11-seat fleet as "no live Claude chats found" — a false absence from the instrument built to catch false absences. Tests covered the classifier, not the enumerator, so the suite was green. Caught only by hand-replicating the walk against a real socket.

Amend: judgment — two spec-writing habits, no text fix: (1) an engine-work brief greps for the existing K3 single implementations the task must reuse and NAMES them; (2) demand a test at the enumeration layer with real-shaped fixtures, not only at the pure-verdict layer.

Resolved: 2026-08-29 — regression test at the resolver layer + audit routed through gather.IsClaudeCommand
