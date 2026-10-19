# 2026-10-19 — a forbidden verb was invoked on the host to see whether it existed

Observed: while verifying a benchmark whose brief made `pfm restart` sacred on this host, the main chat ran `pfm restart --dry-run` on the host to learn whether the installed pfm knew the verb. It did not, so nothing ran, but the command was the one the brief forbids.

Amend: judgment — a verb's presence on the host is checked passively (`strings` on the binary, `pfm help`), never by invoking the verb.
