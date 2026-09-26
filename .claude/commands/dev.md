---
name: dev
description: MANDATORY — route every build, test, typecheck or install here, never go/npm directly. `/dev {status|install|build|typecheck|verify|test|all} [templates|pfm]`, no args = status all; run it before claiming anything works. A missing toolchain reports TOOLCHAIN-MISSING, never a silent skip.
argument-hint: "[status|install|build|typecheck|verify|test|all] [templates|pfm]"
---

# Dev

```bash
.claude/scripts/dev.sh $ARGUMENTS
```

Run it, then report what it printed. No arguments = `status all`.

## The roster

| Project | Directory | Stack | What `verify` means |
| --- | --- | --- | --- |
| `templates` | `templates/` | markdown + shell, no build | `leak-check.sh` over changed public files + the placeholder-registry gate |
| `pfm` | `pfm/` | Go 1.24 | `go vet ./...` |

## The law

- **Never report a suite you did not watch run.** Paste what the script printed; a summary of a run you inferred is a fabrication.
- **TOOLCHAIN-MISSING and NOT-INSTALLED are failures, not skips.** "We could not look" never prints as "nothing is wrong" — that is the one thing this script exists to prevent. If a project could not be checked, the report says which and why.
- **A filtered or partial run is a NAMED gap.** Running one package's tests is fine; calling it "tests pass" is not.
- **A regression test counts only if it was watched FAILING first** against the unfixed code (root `CLAUDE.md` § Testing).
- Failing anywhere → the report leads with the failing step, its exit status, and the first real error line, not with the passes.

## After a green run

Committing is gitter's: dispatch `gitter` Phase COMMIT with the exact paths and the verification that ran. Never `git commit` yourself.
