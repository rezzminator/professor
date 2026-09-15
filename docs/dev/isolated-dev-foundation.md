# Isolated Dev Foundation — worktree + container for pfm development

**Status:** APPROVED 2026-08-20 — rulings: (a) markdown-only waves stay on `develop` (`main` is release-only, moved by the release PR), code changes only in `.worktrees/`; (b) the mirror runs when a fenced wave fully closes — QA pass, orchestrator review with issues fixed, stable — then gitter merges and the host build runs; (c) one image for dev+e2e, files edited on the HOST worktree, the container mounts it for build/test only, and `infra/` at root holds the compose so the setup replicates every time.

## Problem

pfm development happens on the live checkout and ships straight onto the live box. `go build -o ~/.local/bin/pfm` swaps the binary the user's fleet tooling is running mid-session; `pfm install` rewires the live `~/.claude` settings, hooks, and shim; the e2e harness and jail tests, although jailed, share the box with the user's real work. Evidence from one evening: the CLI verb surface changed (#20) while live docs and muscle memory still called the retired forms; the live binary was swapped twice mid-session; a defect in picker synthesis shipped onto the box and produced fleet-invisible chats while the fleet manager itself was being rebuilt underfoot. The instrument being developed IS the instrument the user is holding.

This mirrors the pattern already proven in the user's other projects: development lives in a worktree with a fully isolated environment (own database, own container, own ports) and only verified work mirrors out.

## The fence — three layers

### 1. Worktree — code never changes on the live checkout

All train/code work happens in a `git worktree` under `.worktrees/{train-or-wave}/` (gitignored). The main checkout is the user's stable instrument: its binary, docs, and prompts change only by a gitter merge of verified work. The builder seat's cwd IS the worktree.

### 2. Container — a brand-new machine per run

A dev container (`ubuntu:24.04` + zsh/tmux/git, pinned Go 1.24, and pinned Node 22) mounts the worktree and behaves as a fresh box:

- Own `$HOME` inside the container — `pfm install --yes` runs against it, doctor runs in it, the shim sources into its zshrc. Ephemeral by default (fresh machine per run); a named volume when a task needs iterative state.
- Own tmux server, own socket dir — fleet experiments (spawn/inject/capture) run against container-local chats, never the live `cc-*`/`cx-*` sockets. The live-box law gains teeth: it is now physically satisfied, not just promised.
- Own ports — the MCP HTTP daemon binds container-local; nothing publishes to the host unless a task explicitly asks.
- Builds land in the container's `~/.local/bin`, never the host's.

### 3. The mirror — landing on live is explicit and last

gitter merges worktree → develop only after in-fence verification (project gates + walker). The live install (`go build -o ~/.local/bin/pfm` + `pfm install --yes` on the host) is a separate, user-gated mirror step — never a side effect of a task finishing. "Ship = installed" is redefined: ship = installed **in the container**; the host install is the mirror.

## Mechanics

- `dev.sh` provides `iso {install|build|typecheck|verify|test|all|status|e2e|shell} [project]` through Docker Compose. The active checkout is read-only; Go and npm caches use container volumes. Every invocation builds the current Dockerfile before running. Docker absent → loud `TOOLCHAIN-MISSING`, never a silent host fallback.
- **Broken-state report:** every `iso` run prints the container id and the in-container `$HOME` as its first line — a run that cannot prove it is inside the fence did not run inside the fence. A host-toolchain fallback is impossible by construction (the verb IS the docker invocation).
- Builder briefs change one clause: all build/test through `dev.sh iso`; never build to the host's `~/.local/bin`; never run `pfm install` on the host.
- The CLAUDE.md § Process "no worktree pipeline — deliberate scope choice" clause is reversed for code waves (a /ptm change, ordered by the user 2026-08-20). Blueprint/docs-only waves are markdown and cannot destabilize the box — see decision (a).

## Unchanged

gitter-only git writes; guarded files; the leak gate; the publication boundary; wave/walker verification order.

## Sequence

1. Stabilize: commit the current verified state on main (this design lands with it).
2. Build the foundation as its own wave: container image + `dev.sh iso` + worktree setup + builder re-brief.
3. The train resumes inside the fence: #21 → #22 → #11-close, then S7–S10.

## Open decisions (the user's)

a. **Markdown-only waves** (templates/docs, e.g. #14) — stay on main, or also fenced through the worktree? b. **Live-install mirror trigger** — only on the user's explicit command, or automatically once a train fully closes (S10-equivalent)? c. **Container flavor** — extend the #11 e2e image (one image for dev and e2e), or a separate devcontainer definition?
