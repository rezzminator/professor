# infra/demo — the live-demo fence

A REAL Professor install inside the dev-fence container: real Claude Code and
Codex binaries, real seats (credentials copied in from this host), real pfm
built from this checkout, real chats talking through the chat MCP. Nothing in
the container is a stand-in — the readme-gif recorder keeps its own fake
harness under `infra/readme-gif/` for the take that must never spend a token.

```bash
infra/demo/up.sh                 # container → tools → config → credentials → pfm install → adopt → fleet
infra/demo/up.sh --no-fleet      # everything but the chats
docker exec -it -w /work/express -e TERM=xterm-256color -e COLORTERM=truecolor pfm-demo zsh -i
docker exec -w /tmp pfm-demo bash /worktree/infra/demo/storm.sh start   # the cosmos storm; `stop` ends it
docker rm -f pfm-demo            # tear down; the copied credentials die with it
```

- `up.sh` (host): starts `pfm-demo` on THIS checkout with the same mounts as
  `dev.sh iso`, then drives the three in-container scripts in order.
- `setup.sh tools|install` (container): builds pfm, installs Claude Code + Codex +
  Starship; then `pfm install`, the fleet's invented projects, and the headless
  kit (`~/headless`).
- `adopt.sh [url] [name]` (container): Professor onto a real, well-known repo —
  clone, `pfm init`, then a real chat runs the SETUP.md interview with the
  answers given up front. Default: Express.
- `storm.sh start [N] [SENDS] | stop` (container): the cosmos storm — N cheap
  chats round-robin across Claude, Codex and OpenCode (seat/model/effort per engine
  set by `STORM_*` env, see its header) answering every message with a real
  `chat_inject` back and a ping onward, until each has sent SENDS lines.
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
