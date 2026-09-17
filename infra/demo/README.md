# infra/demo — the live-demo fence

A REAL Professor install inside the dev-fence container: real Claude Code and
Codex binaries, real seats (credentials copied in from this host), real pfm
built from this checkout, real chats talking through the chat MCP. Nothing in
the container is a stand-in — the readme-gif recorder keeps its own fake
harness under `infra/readme-gif/` for the take that must never spend a token.

## Tier B — wave-close only

Tier B is the live end-to-end gate at wave close, not a per-commit check. It
uses real seats and spends real tokens. The wave closer runs these exact
commands, in order:

```bash
infra/demo/up.sh
docker exec -w /tmp pfm-demo bash /worktree/infra/demo/verify.sh
```

Other demo-fence entry points:

```bash
infra/demo/up.sh --no-fleet      # everything but the chats (and so no verify)
infra/demo/up.sh --no-verify     # the fleet without the end-to-end gate
docker exec -it -w /work/express -e TERM=xterm-256color -e COLORTERM=truecolor pfm-demo zsh -i
docker exec -w /tmp pfm-demo bash /worktree/infra/demo/verify.sh [CHECK...]   # the deck exercised end to end, ✓/✗ per beat
docker exec -w /tmp pfm-demo bash /worktree/infra/demo/storm.sh start   # the cosmos storm
docker exec -w /tmp pfm-demo bash /worktree/infra/demo/kill-storm.sh      # ends the STORM_<n> rows, nothing else
code infra/demo/pfm-demo.code-workspace                                   # VS Code attached inside the container
code infra/demo/pfm-demo-host.code-workspace                              # the Mac-side window: this checkout + a "pfm-demo" terminal profile
docker rm -f pfm-demo            # tear down; the copied credentials die with it
```

- `up.sh` (host): starts `pfm-demo` on THIS checkout with the same mounts as
  `dev.sh iso`, then drives the three in-container scripts in order.
- `setup.sh tools|install` (container): builds pfm, installs Claude Code + Codex +
  Starship; then `pfm install`, the fleet's invented projects, and the headless
  kit (`~/headless`).
- `adopt.sh [url] [name]` (container): Professor onto a real, well-known repo —
  clone, `pfm init`, then a real chat runs the SETUP.md interview with the
  answers given up front (Codex dual-runtime yes). Default: Express. A dead
  interview row is resumed in place; leftover tokens go back to the chat once;
  the `professor: install` marker is committed only after `pfm codex build` +
  `check` pass and the codex-sync hook script is in place.
- `verify.sh [CHECK...]` (container, last step of up.sh): the deck's beats run for
  real and are judged from pfm's reports — seats (`/reload` linked, theme, TUI),
  daemon, fleet live + system prompt, Express install fidelity, inject round
  trip Claude→Codex→Claude, `/reload --account` in place, self-compact, storm +
  kill-storm proof, idle down/up, both headless commands. `express` emits five
  independently counted ✓/✗ beats: the `professor: install` marker; `pfm doctor`
  exit 0 with `doctor: clean`; `pfm update check --json` exit 0 with zero
  `UPDATED`/`NEW`/`GONE-UPSTREAM`/`LOCAL-DELETED`, `reviewRequired: 0`, and
  terminal `clean`; `pfm codex check .` exit 0 with `CODEX CHECK PASS`; and
  exhaustive command-hook validation proving at least one hook, every command rooted at
  `$CLAUDE_PROJECT_DIR`, and every referenced target present. Every other named
  check emits one line. The closing count includes all beats, exit 1 on any ✗,
  and throwaway chats are ended and hidden.
- `storm.sh start [N] [SENDS] | stop` (container): the cosmos storm — N cheap
  chats round-robin across Claude, Codex and OpenCode (seat/model/effort per engine
  set by `STORM_*` env, see its header) answering every message with a real
  `chat_inject` back and a ping onward, until each has sent SENDS lines.
- `look.sh` (container, after setup.sh install): the presenter's terminal layer — zinit + plugins, fzf/zoxide/eza/bat, the Sonar tmux bar (magenta "demo" accent) and prompt, CLI colours — from `~/.config/code-theme` that up.sh mirrors from the host; live chat servers are re-sourced. A host without the theme gets named SKIPPED lines.
- `vscode-attach.json`: the Dev Containers attach config up.sh drops into VS Code's `nameConfigs/<container>.json` — the workspace-side extensions (vim, errorlens, gitlens, go, shell, yaml, …) VS Code installs into the container's server on attach.
- `idle.sh down | up` (container): the non-storm slide chats off the sky (ended + hidden, recorded) and back (unhidden + resumed in place via `pfm chat open`; fleet.sh respawns any that will not resume). Closing line: the non-storm live count.
- `aliases.zsh` (container shell, sourced by setup.sh): `storm-up`, `storm-down`, `idle-down`, `idle-up`, `demo-verify` — one word per runbook command; `headless` prints the two slide-11 commands (`claude -p` sealed by hand, then the `pfm headless exec` twin) to copy and run live; verify.sh runs exactly what it prints.
- Docker Desktop memory: the fleet plus a 6-chat storm needs ~12 GiB; up.sh prints a NOTE below that.
- `kill-storm.sh` (container): ends every `STORM_<n>` chat and hides the ended rows —
  exact-name match, so the slide chats survive; its closing line is the proof (0 STORM
  rows left, other live rows before → after equal), non-zero exit otherwise.
- `creds.sh` (host, macOS): copies the selected seats' OAuth credentials from the
  Keychain, `~/.codex/auth.json`, and OpenCode's ChatGPT `auth.json` into the
  container over stdin — nothing lands on the host disk, nothing is printed.
- `daemon.sh` (container): keeps `pfm mcp serve` up — the fence has no init system,
  and Codex rows reach the chat MCP only over that loopback daemon; every spawning
  script calls it first, idempotently.
- `fleet.sh` (container): spawns the demo chats — every one a real harness on a
  real seat, so every spawn costs one short model turn.

Seats: `--accounts 1,2,3` (default) mirrors those ids from the host's
`pfm.config.json` into the container, same config dirs, same emoji; every seat
shares one transcript store (`~/.cc/N/projects → ~/.claude/projects`, as on the
host) so `/reload --account N` resumes the same conversation. Codex gets
`~/.codex`, OpenCode its single ChatGPT-authenticated home. A container that refreshes an
OAuth token rotates it; if a host seat later reports signed-out, log it in again.
