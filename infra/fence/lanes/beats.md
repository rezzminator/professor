# Tier B — hermetic lane beat map

Per-lane ordered beat list. Each beat names what it asserts and the fixture seat or engine it uses (`cc:$SEAT`, `cx`, `oc`, or none); `map.tsv` names the pfm commands and MCP tools each beat tests. `$SEAT` is the primary seat selected by `--seats`; fixture seats 1 and 2 always exist. A beat marked `known-gap` reports `known` and does not fail the suite — see `infra/fence/lanes/known-gaps.yml`. A behavior that needs a service manager gets a beat that asserts the named container advisory.

The canonical order is **O1 → E1 → E2 → E3 → F → M → A → O2**, run in ONE container over accumulating pfm state (`run.sh`); any lane also runs alone from a fresh root, its `need` prelude making what the sequence would have made. Lane O is one area with two entry points: **O1** before any chat exists (install idempotence, host assets, hooks, seats, credentials, dropped seat, symlinked home, doctor, heal) and **O2** the destructive tail (reap over F's storm, archive/index over a real history, headless, harvester, and `uninstall` LAST, since it tears the machine down). The four beats marked **cross-lane** are the spec's planned overlap: they assert the state ANOTHER lane built, so `map.tsv` may name their command a second time, under their own lane.

## Lane R — root

The root image build. Its steps build the shared `pfm-lane-root:<hash>` image every other lane forks from.

- `R.01-build-image` · `provision.sh tools` builds pfm and the mock engine, linked as `claude`, `codex` and `opencode` · spends none
- `R.02-install-yes` · `provision.sh seats` writes two fixture Claude seats and Codex/OpenCode fixture homes · spends none
- `R.03-creds` · `cred-scan.sh` refuses any credential outside the registered fixtures, naming its path; root step 3 first wires pfm and invented local projects with `provision.sh install` · spends none
- `R.04-express-init` · `adopt.sh` deterministically adopts the invented express project with `pfm init` and `pfm init --render` · spends none
- `R.05-launch-empty` · `pfm ls --plain` confirms the fleet is empty · spends none
- `R.06-commit` · `docker commit` → `pfm-lane-root:<hash>` (hash covers the root's build inputs listed in `root.sh` `HASH_PATHS`) · spends none

## Lane E1 — Claude

- `E1.01-open-seat1` · opens one cc chat through the managed Claude launcher (spawn ceremony, label converges) · spends cc:$SEAT
- `E1.02-statusline-theme` · statusline (seat glyph/model/effort/context%) and theme `custom:professor-*` render · spends cc:$SEAT
- `E1.03-reload-account` · `/reload --account 2` keeps the same session id and recalls a pre-reload token on the second fixture seat · spends cc:${ALT:-none}
- `E1.04-reload-model-effort` · `/reload --model`/`--effort` reboot in place · spends cc:${ALT:-$SEAT}
- `E1.05-reload-1h` · `/reload --cache 1h|5m` toggles the cache window · spends cc:${ALT:-$SEAT}
- `E1.06-reload-new` · `/reload --new`/`--new --hide` spawns a fresh session variant · spends cc:${ALT:-$SEAT}
- `E1.07-reload-then` · `/reload --then "<steer>"` queues a follow-up after the reboot · spends cc:${ALT:-$SEAT}
- `E1.08-reload-sock` · `/reload --sock <own>` reboots against the caller's own socket · spends cc:${ALT:-$SEAT}
- `E1.09-reload-while-busy` · the reload worker's log records the hold while busy, then the reboot and its steer · spends cc:${ALT:-$SEAT}
- `E1.11-role-reload` · a reloaded role seat still answers under the role carried by its per-seat prompt channel · spends cc:$SEAT
- `E1.12-status` · `status` reports working/idle, still `working` under a live background sub-agent · spends cc:$SEAT
- `E1.13-last-read-stream` · `last`/`read`/`stream` read the transcript from outside · spends cc:$SEAT
- `E1.14-capture-keys` · `capture`/`keys` drive the pane directly · spends cc:$SEAT
- `E1.15-ask` · `ask` waits for a fresh assistant turn · spends cc:$SEAT
- `E1.16-inject` · `inject` delivers (busy → queued; menu open → refused by name) · spends cc:$SEAT
- `E1.17-watch` · `watch --idle-after` fires on the idle transition · spends cc:$SEAT
- `E1.18-name` · `name` converges the label in tmux + statusline together · spends cc:$SEAT
- `E1.19-kill-unkill` · `kill`/`unkill` toggle the chat's reachability · spends cc:$SEAT
- `E1.21-exit-close` · SessionEnd hooks close the pane without stranding it mid-reload · spends none
- `E1.22-handoff` · `/handoff` carries the file to a new chat and hides the old one · spends cc:$SEAT
- `E1.23-launcher` · the managed Claude launcher entry starts the pane · spends none
- `E1.24-exit-contract` · the shared headless-verb exit contract holds across the matrix · spends none
- `E1.25-end` · `end` kills the whole tmux server, ending the lane · spends none
- `E1.26-resolver-prefers-live` · a name held by exactly one live row plus its own resume row (what `E1.06`'s `--new` leaves behind) resolves to the live row · spends cc:$SEAT

## Lane E2 — Codex

- `E2.01-open-seat` · opens one cx chat, label/title converge on the Codex home · spends cx
- `E2.02-statusline` · statusline renders the Codex usage segment · spends cx
- `E2.03-reload-matrix` · `/reload` matrix as far as Codex supports it (no UserPromptSubmit hook — the beat asserts the model-relay path) · spends cx
- `E2.04-recover` · `recover` rebuilds a chat from a rollout · spends cx
- `E2.05-fleet-prompt` · the fleet prompt reaches the first turn through `developer_instructions` · spends cx
- `E2.06-mcp-stdio` · MCP over stdio: `chat_*` tools are listed in the Codex session · spends cx
- `E2.07-inject-ask-watch` · inject/ask/watch on the Codex home · spends cx
- `E2.08-kill-self` · `chat kill self/me` alias incl. the tmux-less Codex tool-shell (`CODEX_THREAD_ID`) · spends cx
- `E2.10-codex-launch` · the Codex launcher accepts no args and forwards Codex args · spends cx
- `E2.11-doctor-codex-pane` · `pfm doctor`'s `codex_pane` rows stay clean while the chat lives · spends cx

## Lane E3 — OpenCode

- `E3.01-open-seat` · opens one oc chat, label/title/statusline converge on the OpenCode home (same shape as E1) · spends oc
- `E3.02-mcp-registered` · OpenCode's professor (local stdio, `pfm mcp serve --stdio`) MCP registration and `pfm doctor`'s healthy row · spends none
- `E3.03-everything-else` · a live OpenCode row reports idle status, named read/last content refusal, capture, inject delivery, rename, kill and unkill · spends oc

## Lane F — fleet

- `F.01-new-engine` · `chat new` across every engine: cc/cx/oc, resolved by flag/fallback/socket-prefix · spends cc:$SEAT+cx
- `F.02-new-label-role` · `chat new` dimension: `{name}:{group}` label / hidden `_KILL`/`_HIDE` / role / prompt-file · spends cc:$SEAT
- `F.03-new-account-1h-model` · `chat new` dimension: account / cache / model-effort · spends cc:$SEAT
- `F.04-new-await-attach` · `chat new --await`/`--attach` mechanics · spends cc:$SEAT
- `F.05-storm` · a storm (`storm.sh`) spans cc + cx + oc · spends cc:$SEAT+cx
- `F.06-ls-rows` · `pfm ls` rows and kinds render for every chat · spends cc:$SEAT
- `F.07-row-kinds` · live, resume, new, split, agent and booting row kinds appear where the mock can create them; the ProfessorUpdate row follows the build's cache state · spends none
- `F.08-tui-picker` · the TUI: picker entry, tab cycling, fuzzy-find, every Chats/Stats/Limits/Cosmos keybinding · spends none
- `F.09-tui-golden` · golden/stress regression shapes hold against the live captured pane · spends none
- `F.10-concurrent-new` · N parallel `chat new` stress the fleetdb's atomic writers · spends cc:$SEAT
- `F.11-idle-states` · status distinguishes absent, dead, idle and working; promptless booting and no-transcript Claude panes report idle with zero idle seconds · spends none
- `F.12-idle-down-subagent` · idle-down must NOT take a chat with a running background sub-agent · spends cc:$SEAT
- `F.13-name-sync` · `name-sync` converges the tmux window name / label · spends none
- `F.14-kill-storm` · kill-storm tears the storm down cleanly · spends cc:$SEAT+cx
- `F.15-addressing` · fleet-wide addressing/search: `find`, `save`, `history`, `resolve`, `modal` deny · spends none
- `F.16-picker-plumbing` · picker plumbing: per-window pane opener, tmux-session shim · spends none
- `F.17-additional-k-coverage` · additional K-category items this lane also asserts (auto-reconciled) · spends none
- `F.18-e1-chat-survives-storm` · **cross-lane** — after the storm and kill-storm, E1's named chat still answers `status`/`last`/`inject` (the sequence's first cross-lane state effect) · spends cc:$SEAT

## Lane M — MCP

- `M.01-register-claude` · Claude `--mcp-config` payload carries the professor stdio server (`pfm mcp serve --stdio`); a re-install leaves no pfm server key in the seat's `.claude.json` · spends none
- `M.02-register-codex` · registration per engine: Codex stdio, fenced block preserves a foreign entry · spends none
- `M.03-register-opencode` · registration per engine: OpenCode — professor local stdio, `pfm doctor`'s healthy row · spends none
- `M.04-doctor-mcp` · `pfm doctor` Codex + project-scope cutover rows (`foreign-registration`, `legacy-pfm`, `legacy-standalone`) + daemon reachability + version-skew · spends none
- `M.05-daemon-core` · daemon: single loopback port, `/mcp/professor` and its family views, health, restart on replaced binary (rebuild in-container, exit-75) · spends none
- `M.06-daemon-units` · daemon service units: the container has no user service manager, so doctor gives a named advisory · spends none
- `M.07-stdio-transports` · `pfm mcp serve --stdio`: forwards to the daemon, serves in process without one, the caller's ambient identity; malformed-frame parse error · spends none
- `M.08-mcp-cli` · `pfm mcp` CLI surface: `ls`, `enable`/`disable`, the usage exit, `serve` dispatch · spends none
- `M.09-chat-tools` · chat family tools driven by a direct MCP client against the loopback daemon's `/mcp/professor`, including dying-pane `chat_keys` reporting dead/3/partial · spends cc:$SEAT
- `M.10-chat-tools-gap` · chat family tools with no dedicated test file: open/name/kill/unkill/save/`servicedesk` · spends cc:$SEAT
- `M.11-harvester-tools` · harvester tools read local content and return named per-item errors for offline public sources; `pfm harvest download-file` is exercised · spends none
- `M.12-harvester-cache-gate` · harvest local cache (an `include_content: false` `harvester_read` re-read is `cached`) + search-backend gating of `harvester_search_web` · spends none
- `M.13-dropped-seat-roster` · a dropped seat's absence shows up in the daemon's own seat roster · spends none
- `M.14-end-to-end` · mock Claude and Codex chats drive MCP calls through their registered stdio server and report the resulting tool records · spends cc:$SEAT+cx
- `M.15-live-chats-survive-daemon-restart` · **cross-lane** — after the exit-75 restart, E1's Claude chat (stdio) and E2's Codex chat (stdio) each make their next MCP call successfully · spends cc:$SEAT+cx

## Lane A — adopter

- `A.01-scaffold` · the scaffold roster lands from `pfm init` (CLAUDE.md, settings, commands, agents, scripts, skills, epics, codex + docs mirrors); `pfm init --render` fills a second scratch project's install-time tokens from its manifest · spends none
- `A.02-phase2-never-deployed` · Phase-2-only scaffold is confirmed never auto-deployed by `pfm` · spends none
- `A.03-baseline-pin` · `.professor/baseline.json` pins the install · spends none
- `A.04-project-updates` · `pfm doctor --project-updates` is clean; every status class is exercised · spends none
- `A.05-update-verbs` · `pfm update` verbs: pin / pin --all / pin --template / drop / ignore / ignore --undo · spends none
- `A.06-update-adopt` · `pfm update adopt` pins a pre-init install, incl. `--at REF` · spends none
- `A.07-self-update` · bare `pfm update` self-updates: rebuild, doctor before/after, rollback on failure · spends none
- `A.08-codex-mirror` · `pfm codex build`/`pfm codex check` PASS · spends none
- `A.09-codex-agents` · `pfm codex agents` compiles the global agent mirror · spends none
- `A.10-symlinked-blueprint` · a symlinked blueprint is reached correctly through `pfm update` (P10.1 class, update side) · spends none
- `A.11-guard-hook` · the guard hook DENIES a mock chat's Edit of `.claude/**` without the `/pcm` stamp and ALLOWS it with the stamp; the Stop hook recompiles `AGENTS.md` · spends cc:$SEAT
- `A.12-dev-suite` · `/dev status|test` runs express's own suite · spends none
- `A.13-opencode-layer` · the OpenCode compile layer for adopters: `pfm opencode build|check|doctor` all PASS over the adopter project · spends none
- `A.14-release-notice` · the picker's cached release-notice refreshes · spends none
- `A.15-fleet-unchanged-by-update` · **cross-lane** — `pfm update` + the hook rewrite while the fleet is up: `pfm ls` rows and every live chat's hook ownership are unchanged · spends cc:$SEAT

## Lane O1 — ops & host, before any chat exists

Runs FIRST in the sequence: it asserts the machine the other lanes will live on, and every beat that mutates the install restores what it changed (a dropped seat is a spare, never the seat E1 runs on).

- `O1.01-install-idempotent` · a second `pfm install --yes` is idempotent (changed=0) · spends none
- `O1.02-host-assets` · host asset staging is present after install (launcher, overlays, skill/command links, themes, harness baseline) · spends none
- `O1.02a-session-store` · every non-primary seat has four installer-created session-store links and doctor has no `session-store:` finding · spends none
- `O1.03-hooks-installed` · hooks are installed correctly per engine · spends none
- `O1.04-seats` · seats configuration: implicit + explicit accounts, fanout, Codex homes, OpenCode home absence · spends none
- `O1.06-dropped-seat` · a dropped spare seat loses exactly its owned hooks and ledger rows (P10.2), then is restored · spends none
- `O1.07-symlinked-home` · a Claude home reached through a symlink, and a blueprint reached through one (P10.1 class) · spends none
- `O1.08-duplicate-seat-login` · two seats' registries recording one OAuth login (planted `oauthAccount.emailAddress`) → `pfm doctor` advises by name (`duplicate-seat-login email=… seats=…`) · spends none
- `O1.09-doctor-pass` · `pfm doctor` every row green or a named advisory (no systemd/launchd in the container is the named one) · spends none
- `O1.10-doctor-exit-contract` · the top-level `pfm doctor` command contract (exit 0/1/2/3) · spends none
- `O1.11-heal` · `pfm heal` reports/rebuilds wedged Codex thread-history · spends none
- `O1.12-doc-vs-code` · doc-vs-code confirmed behaviors (settings-global merge, project hooks, git-bridge skill, themes, MCP) · spends none
- `O1.13-misc-ops` · misc ops CLI: version, config, issues, whoami, usage-hook · spends none
- `O1.14-price` · `pfm price` reports a table header and valid-table summary; conflicting output flags fail with a named usage error · spends none

## Lane O2 — ops & host, the destructive tail

Runs LAST: it reaps the graveyard F's storm filled, archives the fixture transcript history, and ends by tearing the container install down.

- `O2.01-reap` · `pfm reap` classification + actions (dry-run default, --apply, --horizon, --busy-recent, --json) over states F's mock fleet left · spends cc:$SEAT
- `O2.01b-reap-unprovokable` · fork is asserted; `IDLE` and `UNKN` remain blocked by the missing clock door (`pfm reap` has no `--now`) · spends none · `blocked clock-door`
- `O2.02-archive` · `pfm archive` (apply / subagents / restore / prune-orphans) over fixture transcript history · spends none
- `O2.03-index` · `pfm index` · spends none
- `O2.04-headless` · headless Claude selector returns a cc envelope; the codex selector runs through OpenCode's server and returns an ox envelope with usage · spends cc:$SEAT+cx
- `O2.05-internal-plumbing` · misc internal plumbing: launcher-repair, primary get/set, stale sweep, statusline alias and --subagents rows, clear-kill, kill-exit, claude-version, explore-deny, git-guard, epic-inject, title-renudge · spends none
- `O2.05b-activity-log` · the activity-log reader: `pfm log` shows the lane's own records, a filter narrows, an unknown `--comp`/`--level` and a positional argument exit 2 · spends none
- `O2.05c-callmeter` · the call store's reader over a scratch home and config: `pfm callmeter report` with no store prints the no-store line, exits 0 and creates nothing; `pfm internal callmeter` fed a `PostToolUse` Read payload, then `report files` names the file; an unknown topic exits 2 · spends none
- `O2.06-doctor-codex-pane` · `pfm doctor`'s `codex_pane` rows read clean from the operator's side while E2's chat lives · spends none
- `O2.07-reload-while-busy-operator` · the reload-while-busy seam from the OPERATOR's side: `inject` during a busy turn queues, and the reload worker's reboot-in-place holds · spends cc:$SEAT
- `O2.08-dropped-seat-with-live-chat` · **cross-lane** — a seat dropped while a chat lives on it: the chat keeps working and `pfm doctor` names the seat · spends cc:${SPARE:-none}
- `O2.09-harvester` · `pfm harvest` local content and named offline public-source errors, ask's usage line, and sidecar provisioning · spends cc:$SEAT · linux-arm64 `known-gap`
- `O2.10-uninstall` · LAST beat of the run: `uninstall` removes everything owned, keeps a foreign hook planted before install, leaves no ledger · spends none

## Coverage

- `map.tsv` maps every pfm command (the `--help` tree, `pfm internal` verbs and hidden verbs included) and every MCP tool to the beat that tests it; `check-map.sh` is the gate and prints the live counts.
- All eight lanes are written; `pending.txt` is empty. `O2.01b` reports its clock-door blocker by name.
