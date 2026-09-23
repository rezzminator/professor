# Wave 4 Tier B — beat map

Per-lane ordered beat list, DFS order as `docs/dev/trains/testing-foundation/waves/4-integration-dfs/spec.md` gives it. Each beat names what it asserts, the seat(s) it spends (`cc:$SEAT — the run's first cc seat, LANE_SEATS · cx · oc · none`), and the landscape ids (`docs/dev/testing/landscape.md`) it covers. A beat marked `known-gap` reports `known-gap` and does not fail the suite — see `infra/fence/lanes/known-gaps.yml`. Items whose `needs` column names `systemd/launchd` get a beat that asserts the named advisory in the container (no systemd/launchd host) rather than a skip; those beats are folded into the doctor/daemon-unit beats above by lane.

The canonical order is **O1 → E1 → E2 → E3 → F → M → A → O2**, run in ONE container over accumulating pfm state (`run.sh`); any lane also runs alone from a fresh root, its `need` prelude making what the sequence would have made. Lane O is one area with two entry points: **O1** before any chat exists (install idempotence, host assets, hooks, seats, credentials, dropped seat, symlinked home, doctor, heal) and **O2** the destructive tail (reap over F's storm, archive/index over a real history, headless, harvester, and `uninstall` LAST, since it tears the machine down). The four beats marked **cross-lane** are the spec's planned overlap: they assert the state ANOTHER lane built and carry that lane's landscape ids as second rows in `map.tsv`.

## Lane R — root

The root image build. Its own beats carry no landscape ids — they build the shared `pfm-lane-root:<hash>` image every other lane forks from, not a tested behavior surface.

- `R.01-build-image` · builds `pfm-dev` fresh, `pfm` compiled from the tree · spends none · (none)
- `R.02-install-yes` · runs `pfm install --yes` with every configured seat · spends none · (none)
- `R.03-creds` · copies credentials in (`lanes/creds.sh`, linux path — darwin has no Tier B) · spends none · (none)
- `R.04-express-init` · clones express, runs `pfm init`, install interview run by a real Claude chat (`adopt.sh`) · spends cc:1 · (none)
- `R.05-launch-empty` · launches `pfm` once and confirms an empty fleet · spends cc:1 · (none)
- `R.06-commit` · `docker commit` → `pfm-lane-root:<hash>` (hash covers `pfm/**`, `templates/**`, `docs/SETUP.md`, `infra/fence/**`) · spends none · (none)

## Lane E1 — Claude

- `E1.01-open-seat1` · opens one cc chat on seat 1 (spawn ceremony, label converges) · spends cc:$SEAT · K1
- `E1.02-statusline-theme` · statusline (seat glyph/model/effort/context%) and theme `custom:professor-*` render · spends cc:$SEAT · T31,T33,T35
- `E1.03-reload-account` · `/reload --account 2` reboots in place on the new seat · spends cc:${ALT:-none} · C50
- `E1.04-reload-model-effort` · `/reload --model`/`--effort` reboot in place · spends cc:${ALT:-$SEAT} · C51,C52
- `E1.05-reload-1h` · `/reload --1h on|off` toggles the cache window · spends cc:${ALT:-$SEAT} · C53,K26
- `E1.06-reload-new` · `/reload --new`/`--new --hide` spawns a fresh session variant · spends cc:${ALT:-$SEAT} · C54,C55,L33
- `E1.07-reload-then` · `/reload --then "<steer>"` queues a follow-up after the reboot · spends cc:${ALT:-$SEAT} · C56,X39
- `E1.08-reload-sock` · `/reload --sock <own>` reboots against the caller's own socket · spends cc:${ALT:-$SEAT} · C57
- `E1.09-reload-while-busy` · `/reload` typed while busy asserts the on-pane hold notice, then the reboot · spends cc:${ALT:-$SEAT} · L32,X35,X36,L34
- `E1.10-reload-credential` · `/reload --account` onto an expired/absent-credential seat refuses on the pane · spends cc:$SEAT · K23
- `E1.11-role-reload` · a reloaded role seat still answers under the role carried by its per-seat prompt channel · spends cc:$SEAT · C67
- `E1.12-status` · `status` reports working/idle, still `working` under a live background sub-agent · spends cc:$SEAT · C22,C23,C24,C25,C26,C27,L5
- `E1.13-last-read-stream` · `last`/`read`/`stream` read the transcript from outside · spends cc:$SEAT · C28,C29,C30,C31
- `E1.14-capture-keys` · `capture`/`keys` drive the pane directly · spends cc:$SEAT · C41,C42
- `E1.15-ask` · `ask` waits for a fresh assistant turn · spends cc:$SEAT · C39
- `E1.16-inject` · `inject` delivers (busy → queued; menu open → refused by name) · spends cc:$SEAT · C32,C33,C34,C35,C36,C37,L27,L28,L29,L30,L31
- `E1.17-watch` · `watch --idle-after` fires on the idle transition · spends cc:$SEAT · C40
- `E1.18-name` · `name` converges the label in tmux + statusline together · spends cc:$SEAT · C44,C45,K12
- `E1.19-kill-unkill` · `kill`/`unkill` toggle the chat's reachability · spends cc:$SEAT · C46,C48,X28
- `E1.20-self-compact` · `self-compact --then` leaves a receipt on the pane · spends cc:$SEAT · C38,L35,L37
- `E1.21-exit-close` · SessionEnd hooks close the pane without stranding it mid-reload · spends none · X27,X25
- `E1.22-handoff` · `/handoff` carries the file to a new chat and hides the old one · spends cc:$SEAT · T38,C61
- `E1.23-launcher` · the managed Claude launcher entry starts the pane · spends none · X20,X31,X38
- `E1.24-exit-contract` · the shared headless-verb exit contract holds across the matrix · spends none · C66
- `E1.25-end` · `end` kills the whole tmux server, ending the lane · spends none · C49
- `E1.26-resolver-prefers-live` · a name held by exactly one live row plus its own resume row (what `E1.06`'s `--new` leaves behind) resolves to the live row · spends cc:$SEAT · C32

## Lane E2 — Codex

- `E2.01-open-seat` · opens one cx chat, label/title converge on the Codex home · spends cx · K2
- `E2.02-statusline` · statusline renders the Codex usage segment · spends cx · T31,T32
- `E2.03-reload-matrix` · `/reload` matrix as far as Codex supports it (no UserPromptSubmit hook — the beat asserts the model-relay path) · spends cx · (none)
- `E2.04-recover` · `recover` rebuilds a chat from a rollout · spends cx · C43
- `E2.05-fleet-prompt` · the fleet prompt reaches the first turn through `developer_instructions` · spends cx · X23
- `E2.06-mcp-http` · MCP over HTTP: `chat_*` tools are listed in the Codex session · spends cx · M34
- `E2.07-inject-ask-watch` · inject/ask/watch on the Codex home · spends cx · C32,C39,C40
- `E2.08-kill-self` · `chat kill self/me` alias incl. the tmux-less Codex tool-shell (`CODEX_THREAD_ID`) · spends cx · C47
- `E2.09-self-compact` · self-compact composes the Codex bare `/compact` form (held, not disproved) · spends cx · L36
- `E2.10-codex-launch` · the Codex-specific launcher entry starts the pane · spends cx · X24
- `E2.11-doctor-codex-pane` · `pfm doctor`'s `codex_pane` rows stay clean while the chat lives · spends cx · I63

## Lane E3 — OpenCode

- `E3.01-open-seat` · opens one oc chat, label/title/statusline converge on the OpenCode home (same shape as E1) · spends oc · K3,T31
- `E3.02-mcp-registered` · OpenCode's chat (local stdio) + harvester (remote HTTP) MCP registration and `pfm doctor`'s healthy row · spends none · M36
- `E3.03-everything-else` · everything else on the OpenCode home is asserted for real, reusing the shared CLI surface already proven in E1/F/O · spends oc · (none)

## Lane F — fleet

- `F.01-new-engine` · `chat new` across every engine: cc/cx/oc, resolved by flag/fallback/socket-prefix · spends cc:$SEAT+cx · C9,C10,C11,C12,K4,K5,K6,K7,K8,K9
- `F.02-new-label-role` · `chat new` dimension: `{name}:{group}` label / hidden `_KILL`/`_HIDE` / role / prompt-file · spends cc:$SEAT · K12,K13,K14,K20,K21,C17,C18
- `F.03-new-account-1h-model` · `chat new` dimension: account / 1h / model-effort · spends cc:$SEAT · C13,C14,C15,C16,K24,K25,K28
- `F.04-new-await-attach` · `chat new --await`/`--attach` mechanics · spends cc:$SEAT · C19,C20,K15,K16
- `F.05-storm` · a storm (`storm.sh`) spans cc + cx + oc · spends cc:$SEAT+cx · K18,K19
- `F.06-ls-rows` · `pfm ls` rows and kinds render for every chat · spends cc:$SEAT · C1,C2,C3,C4,C5,C6,C7,C8,C21,C60
- `F.07-row-kinds` · every picker row kind renders (Live/Resume/New/Booting/Agent/ProfessorUpdate × cc/cx/oc) · spends none · K29,K30,K31,K32,K33,K34,K35,K36,K37,K38,K39,K40
- `F.08-tui-picker` · the TUI: picker entry, tab cycling, fuzzy-find, every Chats/Stats/Limits/Cosmos keybinding · spends none · T1,T2,T3,T4,T5,T6,T7,T8,T9,T10,T11,T12,T13,T14,T15,T16,T17,T18,T19,T20,T21,T22,T23,T24,T25,T26,T27
- `F.09-tui-golden` · golden/stress regression shapes hold against the live captured pane · spends none · T28,T29,T30,T36
- `F.10-concurrent-new` · N parallel `chat new` stress the fleetdb's atomic writers · spends cc:$SEAT · I96
- `F.11-idle-states` · the idle-detection state machine is walked across chat kinds · spends none · L1,L2,L3,L4,L6,L7
- `F.12-idle-down-subagent` · idle-down must NOT take a chat with a running background sub-agent · spends cc:$SEAT · L5
- `F.13-name-sync` · `name-sync` converges the tmux window name / label · spends none · L40,L41,L42
- `F.14-kill-storm` · kill-storm tears the storm down cleanly · spends cc:$SEAT+cx · L39
- `F.15-addressing` · fleet-wide addressing/search: `find`, `save`, `history`, `resolve`, `modal` deny · spends none · C58,C59,C62,C63,C64
- `F.16-picker-plumbing` · picker plumbing: per-window pane opener, tmux-session shim · spends none · X18,X19
- `F.17-additional-k-coverage` · additional K-category items this lane also asserts (auto-reconciled) · spends none · K10,K11,K27
- `F.18-e1-chat-survives-storm` · **cross-lane** — after the storm and kill-storm, E1's named chat still answers `status`/`last`/`inject` (the sequence's first cross-lane state effect) · spends cc:$SEAT · C22,C28,C32

## Lane M — MCP

- `M.01-register-claude` · registration per engine: Claude stdio + HTTP wired from the files the installer wrote · spends none · M30,M31,M32,M33
- `M.02-register-codex` · registration per engine: Codex HTTP-only, fenced block preserves a foreign entry · spends none · M34,M35
- `M.03-register-opencode` · registration per engine: OpenCode — chat local stdio + harvester remote HTTP, `pfm doctor`'s healthy row · spends none · M36
- `M.04-doctor-mcp` · `pfm doctor` MCP registration classification + daemon reachability + version-skew · spends none · M37,M38,M39
- `M.05-daemon-core` · daemon: single loopback port, health, restart on replaced binary (rebuild in-container, exit-75) · spends none · M40,M41,M42,M43,M44,M45,M46
- `M.06-daemon-units` · daemon service units: systemd live in the container, launchd = named advisory · spends none · M47,M48
- `M.07-stdio-transports` · stdio transports: chat ambient-identity, harvester non-ambient, malformed-frame parse error · spends none · M49,M50,M51
- `M.08-mcp-cli` · `pfm mcp` CLI surface: bare alias, `ls`, `enable`/`disable`, `serve` dispatch · spends none · M52,M53,M54,M55
- `M.09-chat-tools` · chat fleet server tools driven by a direct MCP client against the live daemon · spends cc:$SEAT · M1,M2,M3,M4,M5,M6,M7,M8,M9,M10,M11,M12,M13,M14
- `M.10-chat-tools-gap` · chat fleet server tools with no dedicated test file: open/name/kill/unkill/save · spends cc:$SEAT · M15,M16,M17,M18,M19,M20
- `M.11-harvester-tools` · the six harvester tools (readPage, parseLocalDocuments, download, findWorks, readWork, webSearch when configured) and caller headers driven against a real, small public document, plus `pfm harvest download` · spends none · M21,M22,M23,M24,M25,M26,M27,M28,M29,H13
- `M.12-harvester-cache-gate` · harvest local cache (a size_only `readPage` re-read is `cached`) + search-backend gating of `webSearch` · spends none · H10,H11
- `M.13-dropped-seat-roster` · a dropped seat's absence shows up in the daemon's own seat roster · spends none · I38
- `M.14-end-to-end` · one chat-driven call per server proves engine wiring end to end (Claude `chat_status` on itself + `readPage` a URL; Codex the same over HTTP) · spends cc:$SEAT+cx · (none)
- `M.15-live-chats-survive-daemon-restart` · **cross-lane** — after the exit-75 restart, E1's Claude chat (stdio) and E2's Codex chat (HTTP) each make their next MCP call successfully · spends cc:$SEAT+cx · M31,M34

## Lane A — adopter

- `A.01-scaffold` · the scaffold roster lands from `pfm init` (CLAUDE.md, settings, commands, agents, scripts, skills, epics, codex + docs mirrors) · spends none · P1,P2,P3,P4,P5,P6,P7,P8,P9,P10,P11,P32
- `A.02-phase2-never-deployed` · Phase-2-only scaffold is confirmed never auto-deployed by `pfm` · spends none · P12,P13,P14,P15
- `A.03-baseline-pin` · `.professor/baseline.json` pins the install · spends none · P16
- `A.04-update-check` · `pfm update check` is clean; every status class is exercised · spends none · P17,P18,P19,P20,P21,P22,P23
- `A.05-update-verbs` · `pfm update` verbs: pin / pin --all / pin --template / drop / ignore / ignore --undo · spends none · P24,P25,P26,P27,P28,P29
- `A.06-update-adopt` · `pfm update adopt` pins a pre-init install, incl. `--at REF` · spends none · P30,P31
- `A.07-self-update` · bare `pfm update` self-updates: rebuild, doctor before/after, rollback on failure · spends none · P33
- `A.08-codex-mirror` · `pfm codex build`/`pfm codex check` PASS · spends none · P34,P35
- `A.09-codex-agents` · `pfm codex agents` compiles the global agent mirror · spends none · P36
- `A.10-symlinked-blueprint` · a symlinked blueprint is reached correctly through `pfm update` (P10.1 class, update side) · spends none · I37
- `A.11-guard-hook` · the guard hook DENIES a real chat's Edit of `.claude/**` without the `/pcm` stamp and ALLOWS it with the stamp; the Stop hook recompiles `AGENTS.md` · spends cc:$SEAT · (none)
- `A.12-dev-suite` · `/dev status|test` runs express's own suite · spends none · (none)
- `A.13-opencode-layer` · the OpenCode compile layer for adopters: `pfm opencode build|check|doctor` all PASS over the adopter project · spends none · P37
- `A.14-release-notice` · the picker's cached release-notice refreshes · spends none · X41
- `A.15-fleet-unchanged-by-update` · **cross-lane** — `pfm update` + the hook rewrite while the fleet is up: `pfm ls` rows and every live chat's hook ownership are unchanged · spends cc:$SEAT · C1,I23

## Lane O1 — ops & host, before any chat exists

Runs FIRST in the sequence: it asserts the machine the other lanes will live on, and every beat that mutates the install restores what it changed (a dropped seat is a spare, never the seat E1 runs on).

- `O1.01-install-idempotent` · a second `pfm install --yes` is idempotent (changed=0) · spends none · I96
- `O1.02-host-assets` · host asset staging is present after install (launcher, overlays, skill/command links, themes, harness baseline) · spends none · I1,I2,I3,I4,I7,I8,I9,I10,I11,I14,I15,I16,I17,I18,I19,I20,I21,I22,I23
- `O1.03-hooks-installed` · hooks are installed correctly per engine · spends none · I24,I25,I26,I27,I28,I29,I30,I31,I32,I33
- `O1.04-seats` · seats configuration: implicit + explicit accounts, fanout, Codex homes, OpenCode home absence · spends none · I34,I35,I36,I39,I40
- `O1.05-credential` · a seat with an expired/absent credential refuses by name at every surface (doctor, `chat new`) · spends none · K23
- `O1.06-dropped-seat` · a dropped spare seat loses exactly its owned hooks and ledger rows (P10.2), then is restored · spends none · I38
- `O1.07-symlinked-home` · a Claude home reached through a symlink, and a blueprint reached through one (P10.1 class) · spends none · I37
- `O1.08-duplicate-seat-login` · two seats' registries recording one OAuth login (planted `oauthAccount.emailAddress`) → `pfm doctor` advises by name (`duplicate-seat-login email=… seats=…`) · spends none · (none)
- `O1.09-doctor-pass` · `pfm doctor` every row green or a named advisory (no systemd/launchd in the container is the named one) · spends none · I41,I42,I43,I44,I45,I46,I47,I48,I49,I50,I51,I52,I53,I54,I55,I56,I57,I58,I59,I60,I61,I62,I64,I65,I66,I67,I68,I5,I6,I12,I13
- `O1.10-doctor-exit-contract` · the top-level `pfm doctor` command contract (exit 0/1/2/3) · spends none · I98
- `O1.11-heal` · `pfm heal` reports/rebuilds wedged Codex thread-history · spends none · X11
- `O1.12-doc-vs-code` · doc-vs-code confirmed behaviors (settings-global merge, project hooks, git-bridge skill, themes, MCP) · spends none · I90,I91,I92,I93,I94,I95
- `O1.13-misc-ops` · misc ops CLI: version, config, issues, whoami, usage-hook · spends none · X1,X2,X3,X4,X12,X13,X14,C65

## Lane O2 — ops & host, the destructive tail

Runs LAST: it reaps the graveyard F's storm filled, archives a real history, and ends by tearing the machine down.

- `O2.01-reap` · `pfm reap` classification + actions (dry-run default, --apply, --horizon, --busy-recent, --json) over the states F's storm left — the sixteen states a live fleet can provoke · spends cc:$SEAT · L8,L9,L10,L11,L12,L13,L15,L17,L18,L19,L21,L22,L23,L24,L25,L26
- `O2.01b-reap-unprovokable` · the three reap states no live fleet can provoke (`fork`, `IDLE`, `UNKN`: a scripted engine AND a clock door — `pfm reap` has no `--now`) · spends none · L14,L16,L20 · `blocked wave7-mock-engine` until Wave 7 lands
- `O2.02-archive` · `pfm archive` (apply / subagents / restore / prune-orphans) over a real transcript history · spends none · X6,X7,X8,X9,X10
- `O2.03-index` · `pfm index` · spends none · X5
- `O2.04-headless` · headless on cc and cx · spends cc:$SEAT+cx · X15,X16,X17,K17
- `O2.05-internal-plumbing` · misc internal plumbing: launcher-repair, primary get/set, stale sweep, statusline alias, clear-kill, kill-exit, claude-version, explore-deny, epic-inject, title-renudge · spends none · X21,X22,X26,X29,X30,X32,X33,X34,X37,X40,T34,T37
- `O2.05b-activity-log` · the activity-log reader: `pfm log` shows the lane's own records, a filter narrows, an unknown `--comp`/`--level` and a positional argument exit 2 · spends none · X42
- `O2.06-doctor-codex-pane` · `pfm doctor`'s `codex_pane` rows read clean from the operator's side while E2's chat lives · spends none · I63
- `O2.07-reload-while-busy-operator` · the reload-while-busy seam from the OPERATOR's side: `inject` during a busy turn queues, and the reload worker's reboot-in-place holds · spends cc:$SEAT · L32
- `O2.08-dropped-seat-with-live-chat` · **cross-lane** — a seat dropped while a chat lives on it: the chat keeps working and `pfm doctor` names the seat · spends cc:${SPARE:-none} · I38
- `O2.09-harvester` · harvester: `pfm harvest` ask + sidecar provisioning (real on x86_64; linux-arm64 = known gap) · spends cc:$SEAT · `known-gap` · H1,H2,H3,H4,H5,H6,H7,H8,H9,H12
- `O2.10-uninstall` · LAST beat of the run: `uninstall` removes everything owned, keeps a foreign hook planted before install, leaves no ledger · spends none · I69,I70,I71,I72,I73,I74,I75,I76,I77,I78,I79,I80,I81,I82,I83,I84,I85,I86,I87,I88,I89,I97

## Coverage

- ids mapped / total: **430/430** (`check-map.sh` is the gate, `map.tsv` the index)
- ids mapped to ≥ 2 lanes: **18** — C1 (A+F), C22 (E1+F), C28 (E1+F), C32 (E1+E2+F), C39 (E1+E2), C40 (E1+E2), I23 (A+O1), I37 (A+O1), I38 (M+O1+O2), I63 (E2+O2), I96 (F+O1), K12 (E1+F), K23 (E1+O1), L5 (E1+F), L32 (E1+O2), M34 (E2+M), M36 (E3+M), T31 (E1+E2+E3)
- beats per lane (total / carrying landscape ids): E1 26/26, E2 11/10, E3 3/2, F 18/18, M 15/14, A 15/13, O1 13/12, O2 11/11, R 6/0
- written: all eight lanes (O1, E1, E2, E3, F, M, A, O2) — `pending.txt` is empty; a beat whose state only a scripted engine can provoke reports `blocked wave7-mock-engine` by name until Wave 7 lands (F.07 K31/K32/K37, F.11 L2/L3, O2.01b L14/L16/L20)
- unmapped ids: none
