# Wave 10 — OpenCode as a first-class live chat (proposal; the user rules)

**Found by:** Tier B lane E3 (Wave 4), 2026-09-18, while trying to write `E3.01-open-seat` the way `E1.01`/`E2.01` open a Claude and a Codex chat.

## What exists today (closed world, read from `16c2222f`)

- **Engine id** `ox` (long name `opencode`) parses everywhere an engine flag is read.
- **Index + matcher only:** `cmd/pfm/engines.go` registers an OpenCode `index.Source` and a `gather.Matcher`; the launcher, headless planner, ask runner and usage source are registered for Claude and Codex only, and `engineCapabilityExceptions` names that as deliberate ("no usage API, headless planner, ask runner, or managed launcher in this tree").
- **Row kinds:** `compose.Kind` has `LiveClaude`, `LiveCodex`, `LiveSplit`, `Agent`, `Booting`, `ResumeOpenCode`, `NewOpenCode` — there is NO `LiveOpenCode`, and `Kind.IsLiveSeat()` / `IsAddressable()` are false for every OpenCode row.
- **The one door:** the picker's `New OpenCode chat` row (`compose.NewOpenCode`, `action.Route` `'P'`, `internal/action/synth.go`) launches a fresh OpenCode TUI in a fleet-owned `ox` socket. `pfm chat new --engine opencode` has no launcher to call and refuses.

## What the lanes therefore cannot assert (and neither can an operator)

Every by-name verb resolves through the live row set: with no live kind, `pfm chat name|capture|inject|last|read|status|kill` on an OpenCode pane refuse structurally, `pfm ls` shows the pane only as a `resume-opencode` row once its session store has a record, and the MCP tools (`chat_inject`, `chat_capture`, …) cannot address it. Lane E3 opens the chat through the picker and pins each refusal by name (`infra/fence/lanes/E3.sh`); lane M's `M.03` (OpenCode MCP registration) is a `known-gap` until Wave 8 item 1 lands.

## The feature, if ruled in

1. `compose.LiveOpenCode` (appended AFTER `ProfessorUpdate` — Kind values are golden-fixture numbers), `String()` → `live-opencode`, `IsLiveSeat()` true; the gather matcher already finds the process, so `fleet.Scan` classifies the pane as that kind. Golden fixtures grow one row; nothing renumbers.
2. A `spawn.Launcher` for OpenCode (`internal/engine/opencode/launch.go`): argv, env (its config dir / `XDG_DATA_HOME` for the session store the index already reads), the sid crumb the statusline writes for Claude — OpenCode has no statusline hook, so the crumb comes from the launcher's own pid + socket, the way `Booting` rows are keyed today.
3. `pfm chat new --engine opencode` → the same `action` route the picker uses (one door, not two).
4. Addressing: `capture`/`inject`/`last` work on any live pane (tmux); `read`/`status` read the OpenCode session store (`index.ReadOpenCodeSessions`) — `busy` detection needs the OpenCode pane footer pinned (Tier B captures it; the Wave 7 mock refuses to render it until then).
5. Lane E3 flips from "pin the refusals" to the E1/E2 shape; `M.03` closes with Wave 8 item 1.

## What stays out

No usage source (OpenCode has no usage API), no headless planner or ask runner (no `-p` equivalent pinned), no self-compact (`/compact` semantics unknown). Each of those is its own ruling.

## Cost

Roughly the Codex launcher's footprint (`internal/engine/codex` + its spawn/compose rows): one new Kind, one launcher, one route join, golden fixture rows, and the E3 rewrite. Touches `compose`, `spawn`, `action`, `cmd/pfm/engines.go`, `picker` — all of which have open work in the current train, so this waits for the train to close.

**Ruling needed:** build it as Wave 10, or leave OpenCode picker-only and make E3's refusal pins the permanent contract.
