# RR — How multi-agent frameworks hand a sub-agent's large output back to its orchestrator
Question: learn how current multi-agent frameworks hand a sub-agent's large output back to its orchestrator — inline in the return, through a file or artifact store, or through shared state — so we can judge our design, where each research digger writes its full cited report to a file and returns only short findings plus the path.

1. Framework return channels — settled when each of the six frameworks' official docs or source is quoted on its return channel and any size limit or truncation.
   Lens: software documentation analysis — method: read official API docs and source; sources: framework docs, GitHub repos
2. Documented write-to-storage-return-reference patterns — settled when the frameworks or vendors documenting the pattern are quoted with what they say it costs or saves.
   Lens: systems engineering — method: compare design-guide claims; sources: vendor engineering blogs, design guides
3. Information loss when condensing — settled when at least two published evaluations or write-ups (2024-2026) are quoted with findings.
   Lens: empirical ML evaluation — method: read benchmark/eval papers; sources: arXiv papers, eval write-ups
4. File-handoff failure modes — settled when documented incidents or analyses of stale files, unread files and parallel path collisions are quoted.
   Lens: distributed systems failure analysis — method: incident and issue review; sources: GitHub issues, failure taxonomies (e.g. MAST)
