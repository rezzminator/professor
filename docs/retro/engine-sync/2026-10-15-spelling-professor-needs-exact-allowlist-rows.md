# 2026-10-15 — `spelling-professor` refuses spec-mandated literals until exact allowlist rows exist

Observed: engine-sync-tidy phase 2a hit engine-sync's tree check `spelling-professor`, which refuses the words `pfm`, `flight` and `professor` in Go, tests and `CLAUDE.md`. Two spec-mandated literals (`pfm.md` in a names test, `{plain|professor}` in `CLAUDE.md`) each needed an exact `path<TAB>line` row in `scripts/marker-allowlist.txt`, and a test file moved by the change needed its old rows moved with it, else the gate went red on the new path.

Amend: plugins/engine-sync/CLAUDE.md#Local (the Tree bullet) — a literal `spelling-professor` refuses but the work requires lands with its exact `path<TAB>line` row in `scripts/marker-allowlist.txt`; a moved or edited line moves its row.
