# Wave 4 Tier B — beat map

Per-lane ordered beat list, DFS order as `docs/dev/trains/testing-foundation/waves/4-integration-dfs/spec.md` gives it. Each beat names what it asserts, the seat(s) it spends (`cc:N | cx | oc | none`), and the landscape ids (`docs/dev/testing/landscape.md`) it covers. A beat marked `known-gap` reports `✗ known-gap` and does not fail the suite — see `infra/fence/lanes/known-gaps.yml`. Items whose `needs` column names `systemd/launchd` get a beat that asserts the named advisory in the container (no systemd/launchd host) rather than a skip; those beats are folded into the doctor/daemon-unit beats above by lane.

### Lane R — root

The root image build. Its own beats carry no landscape ids — they build the shared `pfm-lane-root:<hash>` image every other lane forks from, not a tested behavior surface.

- `R.01-build-image` · builds `pfm-dev` fresh, `pfm` compiled from the tree · spends none · (none)
- `R.02-install-yes` · runs `pfm install --yes` with every configured seat · spends none · (none)
- `R.03-creds` · copies credentials in (`lanes/creds.sh`, linux path — darwin has no Tier B) · spends none · (none)
- `R.04-express-init` · clones express, runs `pfm init`, install interview run by a real Claude chat (`adopt.sh`) · spends cc:1 · (none)
- `R.05-launch-empty` · launches `pfm` once and confirms an empty fleet · spends cc:1 · (none)
- `R.06-commit` · `docker commit` → `pfm-lane-root:<hash>` (hash covers `pfm/**`, `templates/**`, `docs/SETUP.md`, `infra/fence/**`) · spends none · (none)

### Lane E1 — Claude

- `E1.01-open-seat1` · opens one cc chat on seat 1 (spawn ceremony, label converges) · spends cc:1 · K1
- `E1.02-statusline-theme` · statusline (seat glyph/model/effort/context%) and theme `custom:professor-*` render · spends cc:1 · T31,T33,T35
- `E1.03-reload-account` · `/reload --account 2` reboots in place on the new seat · spends cc:1 · C50
- `E1.04-reload-model-effort` · `/reload --model`/`--effort` reboot in place · spends cc:1 · C51,C52
- `E1.05-reload-1h` · `/reload --1h on|off` toggles the cache window · spends cc:1 · C53,K26
- `E1.06-reload-new` · `/reload --new`/`--new --hide` spawns a fresh session variant · spends cc:1 · C54,C55,L33
- `E1.07-reload-then` · `/reload --then "<steer>"` queues a follow-up after the reboot · spends cc:1 · C56,X39
- `E1.08-reload-sock` · `/reload --sock <own>` reboots against the caller's own socket · spends cc:1 · C57
- `E1.09-reload-while-busy` · `/reload` typed while busy asserts the on-pane hold notice, then the reboot · spends cc:1 · L32,X35,X36,L34
- `E1.10-reload-credential` · `/reload --account` onto an expired/absent-credential seat refuses on the pane · spends cc:1 · K23
- `E1.11-role-rearm` · role re-arm crumb re-applies the `--role` constitution after reload/self-compact · spends cc:1 · K22,L38
- `E1.12-status` · `status` reports working/idle, still `working` under a live background sub-agent · spends cc:1 · C22,C23,C24,C25,C26,C27,L5
- `E1.13-last-read-stream` · `last`/`read`/`stream` read the transcript from outside · spends cc:1 · C28,C29,C30,C31
- `E1.14-capture-keys` · `capture`/`keys` drive the pane directly · spends cc:1 · C41,C42
- `E1.15-ask` · `ask` waits for a fresh assistant turn · spends cc:1 · C39
- `E1.16-inject` · `inject` delivers (busy → queued; menu open → refused by name) · spends cc:1 · C32,C33,C34,C35,C36,C37,L27,L28,L29,L30,L31
- `E1.17-watch` · `watch --idle-after` fires on the idle transition · spends cc:1 · C40
- `E1.18-name` · `name` converges the label in tmux + statusline together · spends cc:1 · C44,C45,K12
- `E1.19-kill-unkill` · `kill`/`unkill` toggle the chat's reachability · spends cc:1 · C46,C48,X28
- `E1.20-self-compact` · `self-compact --then` leaves a receipt on the pane · spends cc:1 · C38,L35,L37
- `E1.21-exit-close` · SessionEnd hooks close the pane without stranding it mid-reload · spends cc:1 · X27,X25
- `E1.22-handoff` · `/handoff` carries the file to a new chat and hides the old one · spends cc:1 · T38,C61
- `E1.23-launcher` · the managed Claude launcher entry starts the pane · spends cc:1 · X20,X31,X38
- `E1.24-exit-contract` · the shared headless-verb exit contract holds across the matrix · spends cc:1 · C66
- `E1.25-end` · `end` kills the whole tmux server, ending the lane · spends cc:1 · C49

### Lane E2 — Codex

- `E2.01-open-seat` · opens one cx chat, label/title converge on the Codex home · spends cx · K2
- `E2.02-statusline` · statusline renders the Codex usage segment · spends cx · T31,T32
- `E2.03-reload-matrix` · `/reload` matrix as far as Codex supports it (no UserPromptSubmit hook — the beat asserts the model-relay path) · spends cx · (none)
- `E2.04-recover` · `recover` rebuilds a chat from a rollout · spends cx · C43
- `E2.05-appendix-hook` · the appendix hook (`codex-appendix`) is present in the first turn · spends cx · X23
- `E2.06-mcp-http` · MCP over HTTP: `chat_*` tools are listed in the Codex session · spends cx · M34
- `E2.07-inject-ask-watch` · inject/ask/watch on the Codex home · spends cx · C32,C39,C40
- `E2.08-kill-self` · `chat kill self/me` alias incl. the tmux-less Codex tool-shell (`CODEX_THREAD_ID`) · spends cx · C47
- `E2.09-self-compact` · self-compact composes the Codex bare `/compact` form (held, not disproved) · spends cx · L36
- `E2.10-codex-launch` · the Codex-specific launcher entry starts the pane · spends cx · X24
- `E2.11-doctor-codex-pane` · `pfm doctor`'s `codex_pane` rows stay clean while the chat lives · spends cx · I63

### Lane E3 — OpenCode

- `E3.01-open-seat` · opens one oc chat, label/title/statusline converge on the OpenCode home (same shape as E1) · spends oc · K3,T31
- `E3.02-mcp-known-gap` · the MCP beat hits the confirmed-absent OpenCode wiring — reported `known-gap`, never a skip · spends oc · `known-gap` · M36
- `E3.03-everything-else` · everything else on the OpenCode home is asserted for real, reusing the shared CLI surface already proven in E1/F/O · spends oc · (none)

### Lane F — fleet

- `F.01-new-engine` · `chat new` across every engine: cc/cx/oc, resolved by flag/fallback/socket-prefix · spends cc:2 · C9,C10,C11,C12,K4,K5,K6,K7,K8,K9
- `F.02-new-label-role` · `chat new` dimension: `{name}:{group}` label / hidden `_KILL`/`_HIDE` / role / prompt-file · spends cc:2 · K12,K13,K14,K20,K21,C17,C18
- `F.03-new-account-1h-model` · `chat new` dimension: account / 1h / model-effort · spends cc:2 · C13,C14,C15,C16,K24,K25,K28
- `F.04-new-await-attach` · `chat new --await`/`--attach` mechanics · spends cc:2 · C19,C20,K15,K16
- `F.05-storm` · a storm (`storm.sh`) spans cc + cx + oc · spends cc:2+cx+oc · K18,K19
- `F.06-ls-rows` · `pfm ls` rows and kinds render for every chat · spends cc:2 · C1,C2,C3,C4,C5,C6,C7,C8,C21,C60
- `F.07-row-kinds` · every picker row kind renders (Live/Resume/New/Booting/Agent/ProfessorUpdate × cc/cx/oc) · spends none · K29,K30,K31,K32,K33,K34,K35,K36,K37,K38,K39,K40
- `F.08-tui-picker` · the TUI: picker entry, tab cycling, fuzzy-find, every Chats/Stats/Limits/Cosmos keybinding · spends none · T1,T2,T3,T4,T5,T6,T7,T8,T9,T10,T11,T12,T13,T14,T15,T16,T17,T18,T19,T20,T21,T22,T23,T24,T25,T26,T27
- `F.09-tui-golden` · golden/stress regression shapes hold against the live captured pane · spends none · T28,T29,T30,T36
- `F.10-concurrent-new` · N parallel `chat new` stress the fleetdb's atomic writers · spends cc:2 · I96
- `F.11-idle-states` · the idle-detection state machine is walked across chat kinds · spends none · L1,L2,L3,L4,L6,L7
- `F.12-idle-down-subagent` · idle-down must NOT take a chat with a running background sub-agent · spends cc:2 · L5
- `F.13-name-sync` · `name-sync` converges the tmux window name / label · spends none · L40,L41,L42
- `F.14-kill-storm` · kill-storm tears the storm down cleanly · spends cc:2+cx+oc · L39
- `F.15-addressing` · fleet-wide addressing/search: `find`, `save`, `history`, `resolve`, `modal` deny · spends none · C58,C59,C62,C63,C64
- `F.16-picker-plumbing` · picker plumbing: per-window pane opener, tmux-session shim · spends none · X18,X19
- `F.17-additional-k-coverage` · additional K-category items this lane also asserts (auto-reconciled) · spends none · K10,K11,K27

### Lane M — MCP

- `M.01-register-claude` · registration per engine: Claude stdio + HTTP wired from the files the installer wrote · spends cc:3 · M30,M31,M32,M33
- `M.02-register-codex` · registration per engine: Codex HTTP-only, fenced block preserves a foreign entry · spends cc:3 · M34,M35
- `M.03-register-opencode` · registration per engine: OpenCode — known gap · spends cc:3 · `known-gap` · M36
- `M.04-doctor-mcp` · `pfm doctor` MCP registration classification + daemon reachability + version-skew · spends cc:3 · M37,M38,M39
- `M.05-daemon-core` · daemon: single loopback port, health, restart on replaced binary (rebuild in-container, exit-75) · spends cc:3 · M40,M41,M42,M43,M44,M45,M46
- `M.06-daemon-units` · daemon service units: systemd live in the container, launchd = named advisory · spends cc:3 · M47,M48
- `M.07-stdio-transports` · stdio transports: chat ambient-identity, harvester non-ambient, malformed-frame parse error · spends cc:3 · M49,M50,M51
- `M.08-mcp-cli` · `pfm mcp` CLI surface: bare alias, `ls`, `enable`/`disable`, `serve` dispatch · spends cc:3 · M52,M53,M54,M55
- `M.09-chat-tools` · chat fleet server tools driven by a direct MCP client against the live daemon · spends cc:3 · M1,M2,M3,M4,M5,M6,M7,M8,M9,M10,M11,M12,M13,M14
- `M.10-chat-tools-gap` · chat fleet server tools with no dedicated test file: open/name/kill/unkill/save · spends cc:3 · M15,M16,M17,M18,M19,M20
- `M.11-harvester-tools` · harvester server tools driven against a real, small public document · spends cc:3 · M21,M22,M23,M24,M25,M26,M27,M28,M29
- `M.12-harvester-cache-gate` · harvest local cache + search-backend gating back the MCP tools · spends cc:3 · H10,H11
- `M.13-dropped-seat-roster` · a dropped seat's absence shows up in the daemon's own seat roster · spends cc:3 · I38
- `M.14-end-to-end` · one chat-driven call per server proves engine wiring end to end (Claude `chat_status` on itself + `fetch` a URL; Codex the same over HTTP) · spends cc:3+cx · (none)

### Lane A — adopter

- `A.01-scaffold` · the scaffold roster lands from `pfm init` (CLAUDE.md, settings, commands, agents, scripts, skills, epics, codex + docs mirrors) · spends cc:2 · P1,P2,P3,P4,P5,P6,P7,P8,P9,P10,P11,P32
- `A.02-phase2-never-deployed` · Phase-2-only scaffold is confirmed never auto-deployed by `pfm` · spends cc:2 · P12,P13,P14,P15
- `A.03-baseline-pin` · `.professor/baseline.json` pins the install · spends cc:2 · P16
- `A.04-update-check` · `pfm update check` is clean; every status class is exercised · spends cc:2 · P17,P18,P19,P20,P21,P22,P23
- `A.05-update-verbs` · `pfm update` verbs: pin / pin --all / pin --template / drop / ignore / ignore --undo · spends cc:2 · P24,P25,P26,P27,P28,P29
- `A.06-update-adopt` · `pfm update adopt` pins a pre-init install, incl. `--at REF` · spends cc:2 · P30,P31
- `A.07-self-update` · bare `pfm update` self-updates: rebuild, doctor before/after, rollback on failure · spends cc:2 · P33
- `A.08-codex-mirror` · `pfm codex build`/`pfm codex check` PASS · spends cc:2 · P34,P35
- `A.09-codex-agents` · `pfm codex agents` compiles the global agent mirror · spends cc:2 · P36
- `A.10-symlinked-blueprint` · a symlinked blueprint is reached correctly through `pfm update` (P10.1 class, update side) · spends cc:2 · I37
- `A.11-guard-hook` · the guard hook DENIES a real chat's Edit of `.claude/**` without the `/pcm` stamp and ALLOWS it with the stamp; the Stop hook recompiles `AGENTS.md` · spends cc:2 · (none)
- `A.12-dev-suite` · `/dev status|test` runs express's own suite · spends cc:2 · (none)
- `A.13-opencode-layer-gap` · the OpenCode compile layer for adopters is confirmed absent — known gap · spends cc:2 · `known-gap` · P37
- `A.14-release-notice` · the picker's cached release-notice refreshes · spends none · X41

### Lane O — ops & host

- `O.01-install-idempotent` · a second `pfm install --yes` is idempotent (changed=0) · spends cc:1 · I96
- `O.02-dropped-seat` · a dropped seat loses exactly its owned hooks and ledger rows (P10.2) · spends cc:1 · I38
- `O.03-credential` · a seat with an expired/absent credential refuses by name at every surface (doctor, `chat new`, `/reload`) · spends cc:1 · K23
- `O.04-symlinked-home` · `~/.claude` as a symlink, and a blueprint reached through one (P10.1 class) · spends cc:1 · I37
- `O.05-doctor-pass` · `pfm doctor` every row green or a named advisory (no systemd/launchd in the container is the named one) · spends cc:1 · I41,I42,I43,I44,I45,I46,I47,I48,I49,I50,I51,I52,I53,I54,I55,I56,I57,I58,I59,I60,I61,I62,I64,I65,I66,I67,I68,I5,I6,I12,I13
- `O.06-heal` · `pfm heal` reports/rebuilds wedged Codex thread-history · spends cc:1 · X11
- `O.07-reap` · `pfm reap` classification + actions (dry-run default, --apply, --horizon, --busy-recent, --json) · spends cc:1 · L8,L9,L10,L11,L12,L13,L14,L15,L16,L17,L18,L19,L20,L21,L22,L23,L24,L25,L26
- `O.08-archive` · `pfm archive` (apply / subagents / restore / prune-orphans) · spends cc:1 · X6,X7,X8,X9,X10
- `O.09-index` · `pfm index` · spends cc:1 · X5
- `O.10-headless` · headless on cc and cx · spends cc:1 · X15,X16,X17,K17
- `O.11-harvester` · harvester: `pfm harvest` ask + sidecar provisioning (real on x86_64; arm64 = known gap) · spends cc:1 · `known-gap` · H1,H2,H3,H4,H5,H6,H7,H8,H9,H12
- `O.12-uninstall` · `uninstall` removes everything owned, keeps a foreign hook planted before install, leaves no ledger · spends cc:1 · I69,I70,I71,I72,I73,I74,I75,I76,I77,I78,I79,I80,I81,I82,I83,I84,I85,I86,I87,I88,I89,I97
- `O.13-host-assets` · host asset staging is present after install (launcher, overlays, skill/command links, themes, harness baseline) · spends cc:1 · I1,I2,I3,I4,I7,I8,I9,I10,I11,I14,I15,I16,I17,I18,I19,I20,I21,I22,I23
- `O.14-hooks-installed` · hooks are installed correctly per engine · spends cc:1 · I24,I25,I26,I27,I28,I29,I30,I31,I32,I33
- `O.15-seats` · seats configuration: implicit + explicit accounts, fanout, Codex homes, OpenCode home absence · spends cc:1 · I34,I35,I36,I39,I40
- `O.16-doc-vs-code` · doc-vs-code confirmed behaviors (settings-global merge, project hooks, git-bridge skill, themes, MCP) · spends cc:1 · I90,I91,I92,I93,I94,I95
- `O.17-top-level` · the top-level `pfm doctor` command contract (exit 0/1/2/3) · spends cc:1 · I98
- `O.18-misc-ops` · misc ops CLI: version, config, issues, whoami, usage-hook · spends cc:1 · X1,X2,X3,X4,X12,X13,X14,C65
- `O.19-internal-plumbing` · misc internal plumbing: launcher-repair, primary get/set, stale sweep, statusline alias, clear-kill, kill-exit, claude-version, explore-deny, epic-inject, title-renudge · spends cc:1 · X21,X22,X26,X29,X30,X32,X33,X34,X37,X40,T34,T37
- `O.20-additional-i-coverage` · additional I-category items this lane also asserts (auto-reconciled) · spends cc:1 · I63
- `O.21-additional-l-coverage` · additional L-category items this lane also asserts (auto-reconciled) · spends cc:1 · L32

## Coverage

- ids mapped / total: **429/429**
- ids mapped to ≥ 2 lanes: **14** — C32 (E1+E2), C39 (E1+E2), C40 (E1+E2), I37 (A+O), I38 (M+O), I63 (E2+O), I96 (F+O), K12 (E1+F), K23 (E1+O), L5 (E1+F), L32 (E1+O), M34 (E2+M), M36 (E3+M), T31 (E1+E2+E3)
- beats per lane: E1 25, E2 11, E3 3, F 17, M 14, A 14, O 21, R 6
- unmapped ids: none
