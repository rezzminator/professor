# Release design

| Topic | File | Covers |
| --- | --- | --- |
| release | [release.md](release.md) | The family: why it exists, the reader it serves, the members, the lifecycle, the phases, the release directory and its writers, the verdict tokens, the invariants, versioning, where each rule lives, names, what it replaced, the accepted risk, the surfaces in sync, open items, evidence |
| releaser | [releaser.md](releaser.md) | The phase agent: the brief, each phase's protocol (REVIEW, NOTES, GATE, REHEARSE, READY, VERIFY), the areas and the seams sweep, the delta re-run, the return, the bounds |
| changelogger | [changelogger.md](changelogger.md) | The notes writer: the reader's needs, the delivery routes, grounding, the traps, the coverage ledger, the grammar decisions, revise mode, the return |
| release-check | [release-check.md](release-check.md) | The script: `scope`, `notes`, `ready`; the tier table, every rule and its failure line, the carry-over rules, exit codes, tests |
