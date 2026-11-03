# pfm integration suite: the owner's journey tree

This is the current design of pfm's end-to-end suite: one stateful journey tree from the owner's real start through every branch of the day, plus the unit and hermetic rows the tree pushes down. It replaces `infra/fence/lanes/` and the map-only gate. The permanent home is `docs/design/pfm-integration-suite/`; `scripts/suite-checks.sh` and its support files live under `scripts/` at the repository root.

The ledgers beside this file are the authoritative capability landscape, dimensions, cuts, beat contracts, beat map, pending work, escapes and reconciliation, forecast, retired assertions, references, self-test cases, exclusions, roster, skip reasons, unobservable classes, ruled copy, gaps, flakes, bites, budgets, Codex check references, Codex beat-cost references, prior-art sources and excluded-tooling escapes. The design and ledgers describe current state.

## Contents

- [Tiers](#1-tiers)
- [Landscape summary](#2-landscape-summary)
- [Existing tests](#3-existing-tests)
- [Doors and seams](#4-doors-and-seams)
- [Prior art](#5-prior-art)
- [Shared state](#6-shared-state)
- [Lanes](#7-lanes)
- [Crossings](#8-crossings)
- [Map and observed coverage](#9-map-and-observed-coverage)
- [Activity log](#10-activity-log)
- [Speed](#11-speed)
- [Harness contract](#12-harness-contract)
- [Build order](#13-build-order)
- [Not covered](#14-not-covered)
- [Decisions](#15-decisions)

## 1. Tiers

**unit.** Asserts every product decision behind a seam: backend choice, placement (`placementOf`), engine dispatch, hook-to-status mapping, reload and kill guards, resolver tables, picker key handling, literal-input variations of any dimension's dispatch (Law 3). Fakes every door through the seams of section 4. Runs the package tests of `pfm/internal/**` and `pfm/cmd/pfm` (jail tests included), shuffled in the scheduled leg. Gates every commit (fence gate slice `pfm.unit`, CI `gate (unit)`, `macos (unit)`).

blocking-command-unit: PFM_GATE_ONLY='pfm\.unit' .claude/scripts/dev.sh iso gate

boot-unit: none

**hermetic.** Asserts what a unit test cannot observe: the built `pfm` binary as a separate process, its exit codes, files written into the run root, a boot path, an MCP stdio session, an upgrade from the previous release (`pfm/e2e`, testscript txtar). Fakes: every engine through its recorded mock, Herdr through its recorded socket mock, HTTP through recorded replies; tmux, zsh and SQLite run real (local, no network). Gates every commit.

blocking-command-hermetic: PFM_GATE_ONLY='pfm\.e2e' .claude/scripts/dev.sh iso gate

boot-hermetic: process

**live.** Asserts what the owner sees and what the machine stores, on a machine built to the owner's topology (section 12): real Herdr (pinned build), real herdr-shield (Linux), real tmux, real zsh with pfm's shim, real systemd user units (launchd on macOS), the installed release under test over the previous release; Claude, Codex and OpenCode through one recorded mock per engine family (Law 2), real only in the release leg. One environment per OS, lanes in sequence, keep-going (Law 7). Gates every landing: the lander runs the Linux sequence; CI runs both OSes on every pull request into `main` and on `workflow_dispatch`; the release leg runs real engines and re-records the mocks' goldens.

blocking-command-live: infra/fence/journey/run.sh --root reuse

boot-live: process

live-os: linux, macos

store-sharing-tiers: live Only the live tier shares stores across tests (the state database, the Herdr server, tmux servers, engine homes, the installed machine). Unit and hermetic tests each own a run root (`pfm/internal/testjail`, per-test HOME and state DB); a unit test reaching a shared tmux server is a jail defect.

double-run-paths: pfm/internal/config/, pfm/internal/paths/, pfm/internal/testjail/, .claude/scripts/test.sh, .claude/scripts/dev.sh, infra/fence/ A commit touching any of these runs the unit tier twice, base and perturbed configuration (Law 29).

scheduled-leg: weekly; the unit and hermetic tiers shuffled, the perturbed double run, the macOS live sequence on `develop`, a diff-scoped mutation audit of the week's landings (section 5).

schedule-window-days: 7

blocking-run-logs: the lander's ledgers under `docs/design/pfm-integration-suite/runs/` (landed with the change); CI ledgers as artifacts keyed by subject hash.

The gate that today only reads the map is replaced: `infra/fence/checks.sh` runs `scripts/suite-checks.sh` on every commit (the plan half), the lander runs the journey sequence and `scripts/suite-checks.sh --close` over its ledger at every landing, and CI's `ci` job needs `journey (linux)` and `journey (macos)` with `if: always()` and an explicit `result` check of each.

## 2. Landscape summary

`landscape.md` holds capabilities enumerated from the built product's registries (the cobra tree, the MCP tool table, the hooks pfm registers per engine, the picker keymap, the installer's units), one row per entry where an action has several (Law 33).

| Area | Rows | | Area | Rows |
| --- | --- | --- | --- | --- |
| chat | 71 | | hook | 10 |
| lifecycle | 16 | | status | 9 |
| picker | 13 | | config | 4 |
| ops | 12 | | mcp | 4 |
| report | 12 | | tmux | 2 |
| herdr | 11 | | daemon, headless, schedule, theme | 1 each |

- `today:` baseline: unit 133, unit+live 31, live 1, `NONE` 3 (`picker.open-herdr-tab`, `picker.auto-open-off`, `chat.new-typed`: the owner's two most common doors were never tested). The unit count is a proxy (the package holding the capability has tests); the live count reads the current `map.tsv`, which credits a command, never an observed invocation.
- Exclusions applied (Law 25): build, lint, gate, release tooling, the fence, CI structure and the framework's rule and template files; the excluded-tooling escape inventory is in `escapes-excluded.tsv` and those defects stay with their tools' own tests.
- Ruled rows (7, `ruled:yes`): `lifecycle.uninstall`, `lifecycle.uninstall-again` (data lifecycle: the owner's files), `chat.reload-account`, `chat.reload-account-refused` (identity: which account a chat spends), `chat.whoami-cli`, `chat.whoami-mcp`, `herdr.whoami-moved` (identity: which chat a caller is).
- Known product gaps remain in `gaps.tsv` with their journey beats: Codex typed `/reload`, OpenCode reload, typed `opencode` through the shim, OpenCode tmux birth, and Herdr plain-shell entry on Linux `herdr-shield` and macOS `herdr-native`. The Herdr rows await the owner's decision, report as gaps, and never become `blocked-by`.

## 3. Existing tests

`retired.tsv` records one disposition per assertion of the current lane files at the merge base, derived by `scripts/suite-checks/assertion.sh`. Dispositions use `successor:<beat id>` when the journey re-asserts the same subject and literal; `successor:tier-hermetic:<test>` or `tier-unit:<test>` when a literal-input or single-process check moves down a tier; `dropped:internal-entry-as-action` for assertions whose action was a `pfm internal` verb; and `dropped:weak-oracle` for existence and exit-code-only checks no successor can sharpen.

Disposition of the current suite:

| Current | Disposition | Successor |
| --- | --- | --- |
| R (root build) | keep, rewrite | the root of section 12 |
| O1 | rewrite | `machine-lifecycle`, `machine-restart`; generate, heal, MCP ls, usage-hook, model-cost, tokens, theme to hermetic rows |
| E1, E2, E3 | rewrite | the engine branches; `--then`, `--model` to unit, recover and ask to hermetic |
| F | rewrite | `chat-birth`, `storm-of-births`, `fleet-reader`; reap, name-sync, find to hermetic or unit; internal agent-open dropped |
| M | rewrite | `chat-mesh`; MCP stdio to `tier-hermetic:mcp-serve-answers-tools-list` |
| A | delete | hermetic rows (init, update, engine-sync) |
| H | rewrite | the Herdr branches; internal reload-run dropped |
| HELD | keep its systemd machine as the base of the new root; rewrite every beat | all live lanes |
| O2 | rewrite | `machine-lifecycle` final half; reap, archive, index, headless, log to hermetic; internal clear-name dropped |
| infra kept and rewritten to the contract | `root.sh`, `lib.sh`, `herdr.py`, `herdr.sh`, `provision.sh`, `container.sh`, `cred-scan.sh`, `run.sh`, `mcp-stdio.sh`, `fixtures/`, `scenarios/`, `tests/` | `infra/fence/journey/` |
| deleted | `check-map.sh`, `map.tsv`, `beats.md`, `pending.txt`, `known-gaps.yml`, `budgets.yml`, `adopt.sh`, `hook-command.sh` | `scripts/suite-checks.sh` and this directory's ledgers |

Kept without a row: every Go unit test and the `pfm/e2e` hermetic suite (they gain rows; none is retired).

## 4. Doors and seams

```text
fakes-directory: pfm/fakes
scratch-migration: pfm/fakes/migrations
synthetic-log: pfm/fakes/synthetic-log
```

Seam plan, one seam per mechanism, each fake in `pfm/fakes/` passing the real implementation's contract cases (Law 2):

| Door | Seam | Fake | Contract run against |
| --- | --- | --- | --- |
| process exec | `obs` runner (`pfm/internal/obs/runner.go`) | scripted runner | the real runner on `true`, `false`, a killed child |
| clock | `pfm/internal/clock` | stepped clock | the system clock |
| environment and paths | `pfm/internal/paths` (`Resolve`, `EnvOrFrom`, `HomeFrom`): the path façade | run-root env | the real env under a jail |
| Herdr socket | `pfm/internal/herdr` | recorded Herdr mock (scenario files, both directions: replies and plugin events) | real Herdr, pinned build |
| tmux | `pfm/internal/tmux` | none below hermetic; real tmux at hermetic and live | — |
| process table | `pfm/internal/gather` (procfs, darwin environ) | procfs fixture tree, including another user's process | the real table |
| service manager | `pfm/internal/servicemanager` | recorded systemctl/launchctl | real systemd user and launchd |
| engines | `pfm/internal/mockengine` moves to `pfm/fakes/mockengine` | One recorded mock per family fires every registered Claude, Codex, and OpenCode hook | Goldens are recorded from real engines in the release leg; an unscripted request fails by name |
| HTTP out | `obs` HTTP client (`httpout.go`) | recorded replies (prices, update check) | the real endpoints, release leg |
| state store | SQLite through `pfm/internal/store` | none: the project's own data layer runs real at every tier; unit reaches it only in the store packages | — |

`pfm/internal/hostfixture` moves to `pfm/fakes/hostfixture`; production code never selects a double from configuration (Law 30).

Edge-case fixtures (Law 3 menu, each a behaviour pfm has): an account `settings.json` that is a symlink into a shared file; an account `stats-cache.json` link the engine replaced with a file; a stopped (`SIGSTOP`) foreign tmux server; eight concurrent opens against one Herdr server; a live chat whose cwd was deleted; a worktree removed under a live chat; a process owned by another user (macOS environ `EINVAL`); a retired seat still naming a pane; a superuser run (`skip-because:superuser-bypasses-file-modes` for file-mode refusals); a case-insensitive project path on macOS (`skip-because:filesystem-is-case-sensitive` on Linux).

The bare-door and refused-shape baselines are generated from their source scans and carry dated burn-down ceilings; no baseline is accepted until its source scan passes in the fence. Baseline entries fingerprint statement bytes with SHA256 while retaining shape and source identity, so fixture transcripts and secrets are not copied; fingerprint encoding does not increase counts or ceilings. `forecast.tsv` preserves all 206 source cells and adds the required `beat` column. The current design has an unresolved scope conflict: MERGE requires every cell to map to a journey beat, while Law 25 excludes tooling from the product journey. See [decisions.md](decisions.md#forecast-scope-conflict) and [forecast-reconciliation.tsv](forecast-reconciliation.tsv); no tooling-only cell is claimed as product journey factor coverage.

Spelling sets written under `scripts/suite-checks/`: `assertion.go`, `assertion.sh`, `doors.go`, `refused-shape.go`, `refused-name.sh`, `violating-action.sh`, `exclusion.go`, `locator.sh`, `internal-entry.sh` (the Internal entry as action shape over lane files: any `pfm internal` in an action line), `name-allowlist.txt`, `third-party-rules.txt`. The planted-violation corpora and could-not-look inputs are indexed in `self-tests.txt`; each named check has both controls.

Rendered-from set (a test never reads an expected value from it): `pfm/internal/installer/assets/**` (shim, launchers, units, hook entries), `pfm/harness-prompts/**`, theme profiles under `templates/themes/`.

`exclusions.txt`: `docs/*` and this directory as `hash`; the corpora, `pfm/internal/lintrules/testdata/*`, the scratch-migration and synthetic-log directories and `plugins/*` (submodules with their own CI) as `scope`.

## 5. Prior art

Sources and their fetched status: `prior-art.tsv` (25 rows; unfetched sources are explicitly marked).

Adopted:

- Live tier: the real binary driven as a black box inside a container through a real terminal, compared to stored results (zellij CONTRIBUTING; tmux `regress/` scripts with `.result` files). The Linux root is the HELD systemd machine, rebuilt by hash.
- Environment: every script under a scrubbed environment built from the run root (tmux regress Makefile, `env -i`), each run its own HOME and TMPDIR (mise test-impl.yml).
- CI shape: per-OS jobs rather than a matrix, one required gate job over them (mise test.yml); `macos-15` lacks tmux, so a pinned `brew install` plus a version-print step (runner-images macos-15-arm64 readme).
- Per-run tmux socket directory under the run root, never bare `/tmp` (brindle issue 199; the `/tmp` reaping detail unverified, search snippet).
- Mutation as an audit of the suite, scoped to the diff (Petrovic et al., Practical Mutation Testing at Scale): the weekly leg mutates the week's landed hunks and lists survivors; bites (Law 13) stay the gate.
- Cut reasoning: most faults involve one or two parameters, some three or more (NIST combinatorial testing). Cuts here keep the full product of the dimensions that interact in code (terminal × engine, caller × callee at project place) and cut only where the dispatch has no branch, naming the three-way loss in each cut.
- Mocks fail an unmatched request and ask for re-recording (llm-vcr, unverified, search snippet): the mock contract of Law 2.

Avoided:

- Unit-only CI for a tmux-driving agent manager (claude-squad build.yml): the exact shape that let the owner's escapes through.
- Retries and quarantine of the macOS tmux flake (brindle issue 199): a flake is a defect (Law 21).

Measure here:

- macOS sequence wall relative to Linux (GitHub runner docs give no core or pty figures).
- Root build cold time and checkpoint snapshot and restore time on the HELD machine.
- The suite-time ratchet after three canonical runs; no parallel mode is declared, so no parallelism sweet spot is sought.
- Cost: a macOS minute is about 10.3× a Linux minute (GitHub Actions billing); one macOS sequence at 45 minutes is about 2.8 USD.

## 6. Shared state

| Store | Writers | Readers | Corrupted by | Correlation |
| --- | --- | --- | --- | --- |
| state DB (chats, seats, schedules) via the path façade | chat new, open, reload, name, kill, unkill, end; seat host and session hooks; MCP daemon; Herdr restore | picker, `chat ls`, `whoami`, `status`, `call`, MCP `ls`, `resolve` | two seats for one pane, a retired seat read as live, stale pane ids after restore | chat id |
| Herdr server (workspaces, tabs, labels, panes, agent status, indicators) | placement on open and move, seat host, indicator plugin, the owner's keys | gather, picker, `call` delivery, `whoami` | a tab in the wrong workspace, a label not following a rename, a stuck blocked | pane id joined to chat id through the seat |
| tmux servers (one fresh socket per chat, the owner's own session) | spawn, kill finisher, the owner | gather, `call`, picker | a stopped server blocking a read | socket path |
| engine homes (3 Claude accounts, 1 Codex home, OpenCode config and session store) | installer, engines, hooks | transcript, tokens, name-sync, statusline, doctor | a link replaced by a file, a hook missing per account | engine session id joined to chat id |
| installed machine (launchers, shim, units, Herdr and herdr-shield config) | install, update, heal, uninstall | doctor, the shim, Herdr | an owner setting overwritten | none: machine-global |
| MCP daemon (HTTP port, call index) | install, restart | every MCP action | an old daemon on the port, stale pane ids | caller and callee ids in each record |
| activity log (JSON lines) | every pfm process | the beat library | a record without `op`, `beat`, `run` | `beat`, `run`, chat id (build adds) |
| project tree (atlas and its nested places) | mock engines' file writes, the worktree-removal beat | placement, picker grouping | a removed worktree under a live chat | path |

Third-party state: none at landing (every paid dependency is a recorded mock). The release leg's real engines create remote conversations on the test accounts; the run stamps each first prompt with the run id and the final lane lists them (`ruling-dependency-per-tier`). The installed machine has no correlation column, so no parallel mode exists (Law 6) and every assertion on it reads a path the root inventory names.

## 7. Lanes

### The owner's journey

The owner's spec, verbatim from the brief, ref `spec-owner-journey`:

> Root: pfm executed in a terminal opens its pane. New chat, from the terminal AND from the picker menu. Fork: inside Herdr, or outside Herdr (tmux). In each branch assert it landed exactly as expected (Herdr: right workspace named for the project, tab label, pane; tmux: the window), then reload through EVERY owner door in the same branch: /reload typed into the pane (pfm's hook must catch it) AND the public `pfm chat reload` CLI. Never `pfm internal ...` as an action. Then chat MCP: chats create chats (a tmux chat spawns a background tmux chat, a Herdr chat spawns a Herdr pane) and target each other in every direction (tmux->Herdr, Herdr->Herdr, tmux->tmux, Herdr->tmux), continuing until every combination of his day-to-day work is walked. Engines: Claude, Codex and OpenCode are all a dimension of the tree (owner ruling); fake engines are fine if recorded from the real ones. Each state is the cause of the next: one stateful chain, forked into a tree.
>
> Add every other day-to-day branch you find in pfm's surface (permission prompt -> blocked badge -> answered -> unblocked, sub-agents, exit/kill/unkill, resume, nested projects and worktrees rolling up to their project workspace, herdr-shield's shell starting in ~, account switch, picker actions). Price the full product of dimensions; any cut is named in the design with its reason, never silent.

### Dimension inventory (`dimensions.tsv`)

| Dimension | Values | Dispatch |
| --- | --- | --- |
| os | linux, macos | `pfm/internal/gather/procfs.go:184` (and every build-tagged door) |
| terminal | herdr-shield, herdr-native, tmux | `pfm/internal/spawn/backend.go:92` (Herdr pane env, else tmux at `:115`); shield versus native at `pfm/internal/hookentry/herdr_seat.go:135` |
| place | project, subfolder, worktree, submodule, workbench, home | `pfm/internal/spawn/herdr_place.go:145` (`placementOf`; roll-up through `gitroot`, workbench owner, home labelled `~` and never absorbing) |
| engine | claude, codex, opencode | `pfm/internal/chat/open.go:319` |
| peer | the callee's terminal × place × engine (54 values), an ordered pair with the caller's branch | `pfm/internal/call/engine.go:499` (delivery by the target's backend) |
| shell-start | home, project | `pfm/internal/hookentry/seathost.go` and `pfm/internal/spawn/herdr_place.go:145`; exercised within Herdr births |
| workspace-state | named, misnamed-exclusive, mixed | `pfm/internal/spawn/herdr_place.go:145`; separate carried workspaces for each value |
| mcp-route | daemon-proxy, local-fallback | `pfm/internal/mcpserv/stdio.go:28`; both routes in `machine-restart` |
| recipient-state | idle, working, draft, modal, blocked | `pfm/internal/call/guards.go:117`; established through the recipient's pane in `chat-mesh` |

The first five dimensions define the branch-key prefix and ordered peer suffix; shell-start, workspace-state, mcp-route, and recipient-state are scoped modifiers walked in their named lanes, not extra positional `+` segments. `os` is the first branch-key segment, and each OS has an independent full root.

Not dimensions (literal inputs, unit tables under Law 3): `--backend` and `chat.backend`, `--model`, `--effort`, `--cache`, `--then`, `--new`, an account number, a sibling `*-worktrees` folder (the same `commondir` path as `worktree`).

### Observation bundle and process identity

Every beat records the minimum observation bundle: exact workspace/project label and id, tab label, pane or tmux window/pane identity, focus, and relevant pane text; every carried chat's picker row including engine, project, name, account, kind, state, killed/hidden, backend/address and badge; stored seat/session/lineage/cwd/account/launch recipe and lifecycle markers, transcript attribution, and touched schedule/reload records; the exact projection of a named foreign sentinel; and the run-attributable activity-log slice with no unexpected error. Inapplicable fields carry explicit `n/a:<reason>` values. Existing per-beat assertions remain additional checks.

A new-process assertion pairs old pid and start time with new pid and start time, and verifies the new process belongs to the intended terminal. PID inequality alone is insufficient.

### Merged journey modifiers

- `shell-start` forks Herdr births between home and project starts.
- `workspace-state` forks Claude Herdr births at project place across named, misnamed-exclusive, and mixed workspaces; each begins from a disjoint carried workspace.
- `mcp-route` walks all four backend-direction calls with the daemon stopped through local fallback, then repeats them after daemon recovery.
- `recipient-state` walks the four backend directions for Claude-to-Claude and Claude-to-Codex callers while the recipient is working, drafting, in a modal, or blocked. Each state is established in the recipient pane and the exact delivery or refusal is golden.
- `chat-reload` exercises typed `/reload`, picker ctrl+o, then public `pfm chat reload` resume, `--new` fresh, and `--new --hide` fresh-hidden in one branch. Resume preserves the session id; fresh changes it and leaves its predecessor visible; fresh-hidden hides the carried predecessor.
- `chat-mesh` carries chats born by typed engine command, CLI, and picker; each origin calls in every backend direction.
- `chat-ending` uses successive live-target cycles: pane exit then CLI unkill/open; CLI kill then MCP unkill/open; MCP kill then picker unhide/resume; picker kill then public resume; end then history resume, branch/recover, and archive.

### Journey tree

```
root(os) ── machine-lifecycle (early half, per OS)
  └─ terminal-open ── fork terminal ─┬─ herdr-shield (linux) / herdr-native (macos)
                                     └─ tmux
     └─ chat-birth ── fork place ── fork engine
        │   herdr: project, home × claude, codex, opencode; subfolder, worktree, submodule, workbench × claude
        │   tmux:  project × claude, codex, opencode
        └─ chat-reload → chat-attention → chat-mesh ── fork peer (every ordered pair of the six project leaves)
           └─ fleet-reader → machine-restart → chat-ending → machine-lifecycle (final half)
```

The following figures are the original base design's paper pricing, not the merged ledgers. Recompute merged counts and cost from the current ledgers; the merged total can exceed the original target.

Full product priced: 16,928 rows, 91,967 s (25.5 h) across both OSes; chat leaves 108, pair leaves 5,832. Kept: 742 rows, 5,000 s. Cut: 16,186 rows, 86,967 s. These base-only estimates are not the current merged cost:

| Ruling | Cut cost (s) | Reason |
| --- | --- | --- |
| ruling-cut-tmux-place | 25,305 | tmux has no placement dispatch; place changes only the folder |
| ruling-cut-owner-places | 1,186 | the owner ruled the submodule, workbench and home places repeat the project branch (176 beats) |
| ruling-cut-macos-shield | 15,261 | herdr-shield is Linux-only; on macOS it prints UNPROTECTED and runs a plain shell (native) |
| ruling-cut-linux-native | 15,261 | the shield/native branch has no OS branch; native is walked on macOS |
| ruling-cut-engine-place | 13,496 | `placementOf` has no engine branch; nested places walk Claude only |
| ruling-cut-place-depth | 10,290 | nested and home places walk only place-sensitive beats (births, reloads, resume, whoami, exits); 37 rows, one per beat |
| ruling-cut-peer-place | 5,400 | `call` branches on the target's backend, not its folder; pairs only at project place |
| fact-cli-call-engine-blind | 360 | CLI caller resolution reads the pane, not the engine; the CLI call runs from Claude callers |
| ruling-cut-escape-engines | 318 | escape beats whose fix touched one engine's path walk that engine |
| fact-herdr-seat-only | 318 | seat, label and indicator beats have no tmux path |
| ruling-cut-peer-os | 240 | a callee on the other OS's Herdr variant does not exist in this root |
| fact-ssh-outside-herdr | 240 | the over-ssh resume runs outside Herdr |
| fact-subagent-claude-only | 208 | sub-agent hooks exist for Claude only |
| fact-topology-one-codex-home | 112 | the owner has one Codex home and no OpenCode accounts |
| fact-clear-claude-only, tmux-no-placement, tmux-server-only, statusline-claude-only, self-kill-opencode-only, tmux-no-keybinding | 158 | the product has no such path for the cut value |

### Lane order, with reasons (Law 8)

| # | Lane | Role | Beats per OS | Why here |
| --- | --- | --- | --- | --- |
| 1 | machine-lifecycle (early) | builder | 5 | every later beat runs on the release installed over the previous one, with its live chat |
| 2 | terminal-open | builder | 5 | the owner's first act; the first beat reaching the terminal dispatch, so the fork sits here |
| 3 | chat-birth | builder | 67 | creates each branch's chats through picker, typed engine command and `pfm chat new`; forks place and engine; the eight-open storm |
| 4 | chat-reload | mutator | 46 | reloads the born chats through typed `/reload`, `pfm chat reload`, picker ctrl+o, `--account`; after-effect of birth |
| 5 | chat-attention | mutator | 41 | working, blocked, answered, permission, sub-agent on reloaded chats: proves reload kept the hooks |
| 6 | chat-mesh | builder | 62 | MCP children and every ordered call pair; needs every branch's chats live |
| 7 | fleet-reader | reader | 49 | picker, `ls`, `whoami`, `status`, transcript, statusline over the richest state |
| 8 | machine-restart | heavy | 4 | Herdr restart and restore, tmux server death, daemon restart, each with its after-effect |
| 9 | chat-ending | destroyer | 89 | typed `/e`, native `/exit`, `pfm chat end`, MCP exit, kill, unkill, resume, `/clear` |
| 10 | machine-lifecycle (final) | destroyer | 3 | doctor after the fleet ended, uninstall keeping the owner's files, the second uninstall |

Nine lane keys, ten positions; `machine-lifecycle.sh` holds both halves and the runner runs its final half last. Within a lane the run is branch-major: every branch's beats of a lane run before the next lane starts, so readers see all branches' chats.

### Entries (Law 33)

| Action | Entries walked in every branch that has them |
| --- | --- |
| open the picker | Herdr new-tab key (auto-open through herdr-shield or the wrapper), typed `pfm` |
| new chat | picker ✦ row and wizard, typed `claude`/`codex`/`opencode` through the shim, `pfm chat new`, MCP `new` (child) |
| reload | typed `/reload` caught by the hook, `pfm chat reload` run by the chat, picker ctrl+o; `--account` |
| rename | `pfm chat name`, MCP `name`, the engine's own rename |
| call | MCP `call`, `pfm chat call` |
| status | Herdr tab and badge, picker row, `pfm chat status`, MCP `status`, `last` |
| exit | typed `/e`, the engine's own exit, `pfm chat end`, MCP `exit` |
| kill, unkill | picker ctrl+x, `pfm chat kill` / `unkill`; MCP `kill` / `unkill` at hermetic |
| resume | picker Enter on an inactive row, `pfm chat open`, Herdr restore (`--restored`) |

A `pfm internal` verb appears only in a `need` prelude or an observation; `internal-entry.sh` fails any action line naming one.

### `beats.md` and what each beat asserts

Every beat asserts every effect it can observe (Law 11). A Herdr landing asserts the workspace named for the project, the tab label `<account glyph> <NAME>`, the pane cwd, the pane text, Herdr agent status and the absence of a pfm indicator beside the presence of the idle status, that no other workspace gained or lost a tab, the `pfm chat ls --json` row (backend, pane, workspace, engine, dir, state), the picker row and its group, and the activity-log op carrying the chat id. A tmux landing asserts the chat's own socket and window, the absence of any Herdr call, the `ls` row and the picker row. A status beat asserts the Herdr status, the `[W]`/`[B]` tag, the indicator, the picker status and `pfm chat status` together. A call asserts the callee's pane text once, its status cycle, the reply at the caller and that no other pane changed.

Example: `chat-birth/new-typed@linux+herdr-shield+project+claude` types `cd ~/work/atlas` then `claude` into a shell pane and asserts eleven effects; `chat-reload/reload-typed@...` asserts the hook's blocking line in the pane, the prompt never reaching the engine, the same chat id, pane, workspace and label, the resumed session id, idle status, one picker row, unchanged account, model and cache, and op `chat.reload` with door `typed`.

### `need` preludes (Law 7)

| Lane | Needs (created when absent, no-op when present) |
| --- | --- |
| machine-lifecycle | the root at its hash; the previous release installed with one live Claude chat in workspace `atlas` |
| terminal-open | release under test installed; Herdr server up with workspaces `~`, `atlas`, `beacon`, `cobalt`; the owner's tmux session |
| chat-birth | a picker-capable terminal per terminal branch |
| chat-reload | per chat leaf: the chat born by `new-typed` live (created by `pfm chat new` from the leaf's folder when absent) |
| chat-attention | per chat leaf: one idle live chat |
| chat-mesh | per project leaf: the chats born by `new-from-cli` and `new-from-picker` live |
| fleet-reader | the mesh's children and renamed chats, or one chat per leaf |
| machine-restart | at least one Herdr chat and one tmux chat live, the MCP daemon up |
| chat-ending | per leaf: a main chat, a scout child, a typed-born and a CLI-born chat |
| machine-lifecycle (final) | the release installed, no live chat |

### Product gaps walked to spec

Known product gaps are recorded in `gaps.tsv`: Codex typed reload has no hook (`pfm/internal/installer/codex_hooks.go:46`); OpenCode reload is refused (`pfm/cmd/pfm/chat_reload_command.go:94`); typed OpenCode has no shim; OpenCode tmux birth is refused in `chat_new_command.go`; and pfm offers no plain-shell entry in Herdr, where a new pane always arms the picker. The Herdr rows are Linux `herdr-shield` and macOS `herdr-native`, pending the owner's decision. Gap rows report as known and never as `blocked-by`.

## 8. Crossings

| Crossing | Lanes | Why twice |
| --- | --- | --- |
| `herdr.restore` | chat-birth/new-from-picker (Herdr branches) ↔ machine-restart/herdr-restart-restores | birth writes seats and labels; restore must reopen the same seats, not a second one |
| `chat.call-mcp` | chat-mesh/call-from-mcp ↔ machine-restart/after-restore-call-delivers | the call index built before a restart must deliver to the restored panes |
| `ops.doctor-after-ends` | chat-ending/exit-native ↔ machine-lifecycle/doctor-after-chats-ended | exits leave Herdr panes and records that doctor must read clean |
| `report.statusline` | chat-reload/reload-into-account ↔ fleet-reader/statusline-renders | the account switch must reach the statusline's theme and cost cell |
| `chat.ls-cli` | chat-birth/new-from-cli ↔ fleet-reader/ls-from-cli | birth writes the row; the reader joins it with live Herdr and tmux |
| `lifecycle.install` | machine-lifecycle/install-over-live-chat ↔ chat-birth/new-typed | the shim and launcher the install wrote must route the owner's typed command |

Each maps both beats with `crossing:` and `live-only`. After-effect beats (Law 9): `install-over-live-chat` → `live-chat-adopts-release`; `storm-of-births` asserts every earlier chat's label, pane text and status unchanged; each reload → the chat-attention beats on the same chat; `herdr-restart-restores` → `after-restore-call-delivers`; `tmux-server-gone` → the picker draws and other rows unchanged; `daemons-restart` → MCP `ls` answers with the carried rows; the worktree removal → `indicators-survive-worktree-removal`.

## 9. Map and observed coverage

`map.tsv`: 1726 rows over 170 landscape ids, one row per (id, branch) and per tier row, 6 crossings, no duplicate per tier and branch (Law 28). `pending.txt`: 1468 rows (every beat and tier row not yet written).

Derivation (registries, never a manifest; build step 2 adds the dump):

- `pfm help --ops`: every public verb, MCP tool, registered hook entry and picker action with its op name, generated from the cobra tree, the MCP tool table (`pfm/internal/mcpserv`), `installer.ExpectedHook` and the picker keymap.
- Cross-checks in both directions: `pfm help` (the verb tree) and MCP `tools/list` over `pfm mcp serve` stdio (`infra/fence/journey/mcp-stdio.sh`).

Plan half from the first commit: `map-ids`, `map-beats`, `map-names`, `map-dup`, `branches`, `landscape-scope`. Proof half at close: `observed` reads the canonical ledger; owner-action rows require an action record carrying their injected `beat` id and matching their assertions. The `action` field records that owner-visible action; `op` records diagnostics and never credits an action by itself. An engine-owned input row has no action record or ops, so it cannot borrow action credit. Its beat contract names the beat-local `engine-input.txt` receipt, expected and actual input, and `evidence:engine-input:/stats`; its engine-state assertion expects and observes `regular-file`, and a passed named golden proves the exact file bytes by hash. The original `log:` header (for example, `/j/TOKEN/out/RUN.activity.jsonl`) locates aggregate activity by basename under `runs/`. A relative `artifacts: RUN` header locates bundles and receipts under `runs/RUN/`, including `runs/RUN/beats/<row>`; when `artifacts` is absent, receipt lookup keeps the legacy `runs/beats/<row>` location. The receipt's original source remains relative to the original log parent, then is rebased under the artifact root. Absolute artifact paths, `..`, and resolved paths escaping `runs/` are rejected. The harness injects `PFM_BEAT` and `PFM_RUN` into every process it starts; the build makes pfm carry them through the MCP daemon, the reload worker, the kill finisher and the hook processes as log fields.

## 10. Activity log

- Destination: one JSON-lines stream per environment root, resolved by the path façade (`pfm/internal/paths`), written by `obs` (`slog`, `OpenRotatingLog`); every pfm process (CLI, hooks, MCP daemon, workers, timers) writes the one record shape to that file.
- Record: `ts level msg op pid version comp`, the chat ids in play (`chat`, `caller`, `callee`, `parent`), `door` for multi-entry ops, `dur_ms`, `err`, `beat`, `run`. Today verb records carry no `op`, `beat` or `run`; the build adds them at the seams (runner, Herdr client, tmux client, clock, HTTP), not at call sites.
- Levels: `debug` under the harness, `info` installed.
- Buffer handler: an in-memory `slog.Handler` behind `obs.Logger(ctx)` for unit rows, proving invocation by op.
- Redaction test: a record carrying a token-shaped value, an account path and a prompt body is written redacted (self-test `redaction`).
- Rotation: `OpenRotatingLog` keep and size limits unchanged; the beat library records the file offset before each beat and reads the slice after it, across a rotation.
- Reader: `pfm log` filtered by time, level, chat id, op and beat (`tier-hermetic:log-filters-records`).
- The Second logging path ratchet holds direct prints to stderr outside the CLI printers.

## 11. Speed

```text
target-sequence-ms: 2700000
budget-headroom-pct: 20
```

Unit and hermetic targets: measured at build step 3 from three runs, before any conversion (`ruling-speed-targets`).

Paper cost per OS (identical tree on both), seconds: machine-lifecycle 108, terminal-open 12, chat-birth 438, chat-reload 550, chat-attention 195, chat-mesh 324, fleet-reader 128, machine-restart 51, chat-ending 665, machine-lifecycle final 29; total 2,500 s (41.7 min), 371 rows. Unit costs come from measurement on the HELD machine: reload 12 s, kill 6 s, exit 9 s, about 2.7 s per simple beat; the current sequence's budget is 763 s for about 140 beats.

- Critical path: the sequence is serial within an OS; the two OSes run on separate machines in parallel, so wall is the slower OS. Linux: 41.7 min plus the root build on a hash miss (measure here). macOS: 42 to 63 min estimated (1.0 to 1.5× Linux, measure here).
- Per-change policy: a change runs the lanes whose files or product packages it touches from their checkpoints, then the touched lane alone from a fresh root when its file changed; the full sequence on Linux at every landing (lander); both OSes on every pull request into `main` and at release; the weekly leg on `develop`.
- Parallel mode: none. The installed machine, the Herdr server and the service units are machine-global with no correlation column (Law 6).

## 12. Harness contract

```text
goldens: infra/fence/journey/goldens
size-ceiling: 400
clone-threshold-tokens: 80
```

- Beat library `infra/fence/journey/lib.sh` (rewritten from `infra/fence/lanes/lib.sh`, with `herdr.py` for Herdr socket reads): `beat`, `assert`, `golden`, `pass`, `fail`, `known`, `blocked`, `skip-because`, `need`, `spends`, `expect-log`, `poll`. Keystrokes reach panes through the terminal (Herdr's pane input and tmux `send-keys`), the owner's path; the library is the only log reader.
- Runner `infra/fence/journey/run.sh [--lanes …] [--from checkpoint:<lane>] [--root reuse|rebuild] [--dry-run]`: canonical lane order, keep-going, streamed `✓ ✗ known blocked-by skip-because` lines with the lane prefix; exit 1 on a failure outside `gaps.tsv`, an unmapped id or a budget breach; exit 2 when it could not run.
- Lane files: `infra/fence/journey/lanes/<lane key>.sh`, split by journey segment past 400 lines.
- Ledger: one file per run id in `docs/design/pfm-integration-suite/runs/`, header `command mode lanes-before boot runner_pid order config root tree dirty (start and end) start load`, rows `row⇥verdict⇥assertions-by-id⇥expected⇥ops⇥goldens⇥wall_ms⇥t+s`, per-lane totals. The unit and hermetic runners, the lint step and the checks script write the same header (`mode: tier`).
- Failure block per failed row: row id, `file:line`, expected and actual with its source, the first error record in the row's window, absolute paths of the run directory, the ledger and the log.
- Checkpoints: after each lane, the HELD machine's filesystem snapshotted, keyed by the root hash plus the lane files up to that lane. Live processes (Herdr, tmux servers, engines, daemons) do not survive a snapshot: a restore restarts them and the next lane's prelude reopens its chats (`ruling-checkpoint-mechanism`).
- Isolation: each run its own HOME, state DB, Herdr socket, tmux socket directory, MCP port and unit names derived from the run root; the run root's keys set in the ambient environment are an error (Law 29). No credential reaches a landing job (Law 1).

### Root inventory

Read from the owner's Linux machine on 2026-11-02, written with invented names; counts from the machine (Law 34). Root hash inputs: this inventory, the previous release's tag and build, the Herdr build, herdr-shield's build, the tmux and zsh versions, `provision.sh`, the mock scenarios and goldens, the fixtures.

- One user, zsh login shell; pfm's zsh shim routes `claude` and `codex` through pfm's launchers and opens the picker when `PFM_AUTO_OPEN` is set.
- Herdr: `default_shell` herdr-shield, `shell_mode` non-login, agents not resumed on restore; herdr-shield's inner shell is pfm's wrapper with `-l`, which restores the login zsh and sets `PFM_AUTO_OPEN=pfm` for interactive shells. herdr-shield starts in home. pfm offers no plain-shell entry in Herdr; every available new-pane key arms the picker.
- Keybindings: new tab, new workspace and split open a pane with `PFM_AUTO_OPEN=pfm` through the owner's Herdr wrapper (it fixes the foreground client's size). Plain-shell entry is unavailable in Herdr; its owner decision is pending.
- Four workspaces open at once, named for their projects: `~` (identity cwd home), `atlas`, `beacon`, `cobalt`; most pane cwds home; unnamed tabs hold pickers and shells.
- Projects under `~/work/`: `atlas` (git; `docs/specs` subfolder; `.worktrees/flight-export` linked worktree; a sibling `atlas-worktrees/flight-review` worktree; `plugins/lantern` submodule; `bench/report-desk` workbench), `beacon`, `cobalt`; one scratch project under the run's `/tmp` root. The owner's machine has about 30 project folders; the root seeds 3 (`ruling-root-scale`).
- Engines: three Claude account dirs, each with its theme and a `settings.json` symlinked to one shared file; one Codex home; OpenCode configured with no accounts.
- `pfm.config.json`: bypass permission mode, 1h cache on, chat MCP enabled over HTTP on the run-root port, `ask.engine` codex, no backend key (auto).
- tmux: the default server with one owner session, for work outside Herdr and over ssh.
- macOS root: a `macos-15` runner, brew-pinned tmux, the pinned Herdr darwin build with native panes (herdr-shield is Linux-only), launchd agents; the same projects and accounts. The owner's Mac was not readable from here (`ruling-mac-root-inventory`).

`self-tests.txt` contains one could-not-look input and one planted violation per check, plus runner (keep-going with a planted red, killed run), beat-library refusal, output-filter, mock, redaction, all-keys-blank boot, and outbound-socket refusal tests. The forecast and internal-entry checks each have an unreadable-input test and a planted violation.

## 13. Build order

1. Run isolation (run-root HOME, state DB, sockets, ports, unit names) and the ledger header bound to the tree and dirty hashes.
2. `pfm help --ops`, the collected census, the checks script installed with its self-tests and the refused-shape corpora; `infra/fence/checks.sh` calls `scripts/suite-checks.sh`. This step is complete only after its census and planted self-tests pass in the fence and each plan check reports against the current ledgers.
3. Unit and hermetic wall budgets measured and pinned before any conversion.
4. Seams and fakes: `pfm/fakes/` created, `mockengine` and `hostfixture` moved, contract suites run against the real implementations; the edge-case fixtures; packages converted in batches with disjoint files.
5. The activity log fields (`op`, `beat`, `run`, ids, `door`), then the three engine mocks firing every hook, recorded from the real engines once.
6. The harness with `machine-lifecycle` (early half), green in sequence and alone.
7. `terminal-open`, then speed measured against `target-sequence-ms` before a third lane.
8. `chat-birth`, `chat-reload`, `chat-attention`, `chat-mesh`, `fleet-reader`, `machine-restart`, `chat-ending`, the final half, each joining the sequence the day it is written with its after-effect beats, bites and budget; each escape's assertion watched red on its reverted fix.
9. `retired.tsv` verified, then `infra/fence/lanes/` deleted in that commit.
10. Close: three green sequences per OS on the merge subject, budgets pinned, no flake, `pending.txt` empty, the sequence in the lander's gate, CI `journey (linux)` and `journey (macos)` under the `ci` job, and the release leg with real engines.

## 14. Not covered

- Windows: pfm does not run there.
- Real engines between releases: every landing walks recorded mocks; a shape the mock lacks escapes until the release leg re-records it (Law 2).
- The Herdr client's pixels (colours, blink rendering): the harness reads Herdr's socket state and pane text, not the GUI.
- The owner's Mac topology: unread from this host; the macOS root follows the runner image until ruled.
- Developer tooling (fence, framework rules and templates, release tooling, CI, plugins): outside the product landscape (Law 25); excluded escapes are listed in `escapes-excluded.tsv`.
- The cut combinations of `cuts.tsv`: each row carries its cost and ruling.

## 15. Decisions

The accepted current decisions, with exact reference anchors for the ledgers, are in [decisions.md](decisions.md). They cover placement, lifecycle, cuts, known product gaps, tier boundaries, OS runs, logging, cost policy, and checkpointing. Two inputs remain unresolved: the owner must name the release LLM model and reasoning setting per engine, and the owner's Mac topology inventory has not been read.

The 206 forecast cells are retained, but tooling-only cells expose a conflict between MERGE's all-cell mapping requirement and Law 25's product-only journey scope. See [decisions.md](decisions.md#forecast-scope-conflict) and [forecast-reconciliation.tsv](forecast-reconciliation.tsv). Step 2 remains incomplete: the prescribed fence and test entry points were rejected by the engine guard before execution. No check, baseline scan, planted self-test, or current-ledger verdict is claimed as passing.
