# infra — the isolated dev fence

Development must never destabilize the live box: code changes happen in a git worktree under `.worktrees/`, and every build/test runs inside the `pfm-dev` container with that worktree mounted at `/worktree` — own HOME, own tmux, no published ports. Files are edited on the host; the container only builds and tests. Design and law: `docs/dev/isolated-dev-foundation.md`.

The image pins Go to `pfm/go.mod` and Node to the mirror compilers' minimum supported release. It runs as root over the read-only, uid-1000-owned source mount; Go's build and module caches plus the npm cache live in container volumes, never in the checkout. A worktree's gitdir is not mounted, so `docker-compose.yml` sets `GOFLAGS=-buildvcs=false` instead of changing source ownership.

Entry point — from the worktree checkout:

```bash
.claude/scripts/dev.sh iso test pfm     # suite, in-container
.claude/scripts/dev.sh iso e2e          # the tagged e2e harness
.claude/scripts/dev.sh iso all templates # gates + mirror checks, in-container
.claude/scripts/dev.sh iso shell        # interactive fresh machine
```

Every run builds the current Dockerfile first, then prints the fence proof (`fence: container=… HOME=/root`); a run that cannot print it did not run inside the fence. The tagged e2e command disables Go's result cache so every invocation exercises a fresh install lifecycle. Docker absent reports `TOOLCHAIN-MISSING` — never a silent host fallback.
