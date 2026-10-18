---
name: dev
description: MANDATORY — route every build, typecheck or install here, never go/npm directly. `/dev {status|install|build|typecheck} [templates|pfm]`, no args = status all. A missing toolchain reports TOOLCHAIN-MISSING, never a silent skip. Not for tests → `.claude/scripts/test.sh`.
argument-hint: "[status|install|build|typecheck] [templates|pfm]"
---

# Dev

```bash
.claude/scripts/dev.sh $ARGUMENTS
```

Run it, then report what it printed. No arguments = `status all`. `verify`, `test`, `cover` and `all` refuse on the host: a test runs through `.claude/scripts/test.sh <path>…` (`/pfm-testing-manual`).

## The roster

| Project | Directory | Stack |
| --- | --- | --- |
| `templates` | `templates/` | markdown + shell, no build |
| `pfm` | `pfm/` | Go 1.27 |

## The law

- **TOOLCHAIN-MISSING and NOT-INSTALLED are failures, not skips.** "We could not look" never prints as "nothing is wrong" — that is the one thing this script exists to prevent. If a project could not be checked, the report says which and why.
- Failing anywhere → the report leads with the failing step, its exit status, and the first real error line, not with the passes.

## After a green run

Committing is gitter's: dispatch `gitter` Phase COMMIT with the exact paths and the verification that ran. Never `git commit` yourself.
