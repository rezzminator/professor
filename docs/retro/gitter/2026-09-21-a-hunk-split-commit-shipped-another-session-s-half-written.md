# 2026-09-21 — a hunk-split commit shipped another session's half-written tests

Two sessions editing one checkout; gitter split review_test.go by hunk and committed tests whose symbols lived only in the other session's uncommitted review.go; the committer's "verified" claim was false.

Amend: gitter.md — never hunk-split a file another session has modified; leave it out or wait. A commit is verified by exporting the committed tree (git archive) and building it, never by the committer's word.

Resolved: 2026-09-22 — baseline repaired (ce29317c); rule recorded in the RETRO checkpoint; gitter.md amendment open → /pcm
