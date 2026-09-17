# infra — the fence, the demo, and the manifest gate

Three independent things share this directory; nothing here is one system.

```
infra/
  README.md                      this file — the map
  check-self-hosted-manifest.sh  repo gate, not fence-only (dev.sh verify templates calls it)
  fence/                         the isolated dev fence — build/test in a fresh container
  demo/                          the live-demo fence — a real install for presentations
  readme-cards/                  the README section-card recorder (screenshots/motion clips)
```

## fence/

Development must never destabilize the live box: code changes happen in a git worktree under `.worktrees/`, and every build/test runs inside the `pfm-dev` container with that worktree mounted at `/worktree` — own HOME, own tmux, no published ports. Files are edited on the host; the container only builds and tests. Design and law: `docs/dev/isolated-dev-foundation.md`.

`docker-compose.yml` + `pfm-dev.Dockerfile` build the image (Go pinned to `pfm/go.mod`, Node to the mirror compilers' minimum); `fence-env.sh` resolves the `PFM_DEV_*` mount contract every caller sources; `tools.env` + `tools.sh` pin the dev tools `make tools` installs on the host and bakes into the image; `release-rehearsal.sh` drives the fenced adopter machine `/pfm:release` rehearses updates on.

Entry point — from the worktree checkout:

```bash
.claude/scripts/dev.sh iso test pfm      # suite, in-container
.claude/scripts/dev.sh iso e2e           # the tagged e2e harness
.claude/scripts/dev.sh iso all templates # gates + mirror checks, in-container
.claude/scripts/dev.sh iso shell         # interactive fresh machine
```

Every run builds the current Dockerfile first, then prints the fence proof (`fence: container=… HOME=/root`); a run that cannot print it did not run inside the fence. Docker absent reports `TOOLCHAIN-MISSING` — never a silent host fallback.

## demo/

A REAL Professor install inside the same fence image, for live presentations — real Claude Code + Codex + pfm, real chats over the chat MCP. Entry point: `infra/demo/up.sh` (see `infra/demo/README.md`).

## check-self-hosted-manifest.sh

Verifies this checkout's own `.professor/manifest.json` against the tracked tree — `dev.sh verify templates` calls it. Not fence-only, so it stays at the top level rather than moving into `fence/`.
