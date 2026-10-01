# Tier B lanes — the hermetic end-to-end suite

**Home:** `infra/fence/lanes/`. **Coverage index:** `infra/fence/lanes/beats.md` (per-lane beats) + `infra/fence/lanes/map.tsv` (`name · lane · beat`, one row per pfm command and MCP tool).

Tier B drives the pfm binary against a mock engine that plays `claude`, `codex` and `opencode`. Every run has two invented Claude fixture seats plus Codex and OpenCode fixture homes, registered fixture credentials only, `--network none` and `GOPROXY=off`. It spends no model turn and works in Linux containers on any Docker host. A human runs it on demand, or a flight lander runs it when that flight's spec names the lanes. Neither CI nor the releaser GATE runs the lanes. The host-side release rehearsal is the one real-model run; it is outside every test suite.

## The shape

```
pfm-lane-root:<hash>          built once per template change (root.sh), never pushed
   └─► ONE offline container per run  the fleet's state accumulates lane after lane:
       O1 → E1 → E2 → E3 → F → M → A → O2
```

- **One container, lanes in sequence.** Every lane inherits the pfm state the lanes before it built: E1's named chat is still alive when F storms, when M restarts the daemon, when A rewrites the adopter's hooks. pfm is ONE state (one daemon, one `pfm.db` and one `pfm-cache.db`, one tmux server, one hook set) and the effect of each area on that shared state is where the bugs live.
- **Any lane runs alone.** `run.sh --lanes M` starts a fresh container from the same root and runs M only; its `need` prelude makes the preconditions the sequence would have made (a live chat, the daemon, a working directory) and is a no-op when they already exist. A solo lane is the dev/qa loop; the sequence checks accumulated state.
- **No `--parallel`, ever.** Concurrency is a scripted beat (`storm`, two writers, inject-during-busy), never a scheduling strategy: two lanes racing on one fleet would make every red row order-dependent.
- **Order is a design decision:** state builders first, readers over the richest state, destroyers last. Lane O is one area with two entry points — **O1** before any chat exists, **O2** the destructive tail that ends with `uninstall`.

## Run one lane

```bash
infra/fence/lanes/run.sh --lanes E1 --dry-run     # the plan: hash, image decision, beats, seats
infra/fence/lanes/run.sh --lanes E1                # solo, one Claude seat, from the root image
```

`--dry-run` creates no container and runs no beat; it prints the root decision, lane order, budgets and seats. A run prints `✓ / ✗ / known / blocked` per beat with the lane prefix and writes `/tmp/{project}/lanes/<stamp>/`:

| file | what it carries |
| --- | --- |
| `<lane>.log` | every beat line, plus a failed beat's raw pane bytes (`tmux capture-pane -e -p -S -`) and its activity-log slice |
| `<lane>.stream.log` | exactly what the lane printed, as it printed it |
| `timeline.tsv` | `lane · beat · t+s · verdict · dur_s · seat · detail` |
| `lanes.tsv` | `lane · wall_s · beats · failed · known · blocked` (the Wave 2 TSV shape) |
| `summary.md` | the header (mode, root image, order, seats), the table, the budget verdicts, the verdict |

## Run the sequence

```bash
infra/fence/lanes/run.sh                            # every WRITTEN lane, canonical order
infra/fence/lanes/run.sh --lanes O1,E1 --seats cc:1 # a slice of it, still in canonical order
infra/fence/lanes/run.sh --root rebuild             # force a fresh root image first
```

Lanes named on `--lanes` in any order run in canonical order. A lane that is not written yet is listed in `infra/fence/lanes/pending.txt`: naming it explicitly is refused (exit 2), and a bare `run.sh` drops it with a named `NOT WRITTEN` line rather than shrinking the sequence silently. The header names the mode (`solo` / `sequence`) and, in sequence mode, the lanes that ran before each lane.

**The root image.** `root.sh` builds from the `pfm-dev` fence image in seven steps: (1) `provision.sh tools` builds pfm and `pfm/cmd/mock-engine`, linked as all three engine CLIs; (2) `provision.sh seats` writes two fixture seats and fixture Codex/OpenCode homes; (3) `provision.sh install` runs `pfm install --yes` and creates invented local projects; (4) `adopt.sh` scripts `pfm init` and `pfm init --render` for the invented express project; (5) `pfm ls --plain` confirms an empty fleet; (6) `cred-scan.sh` refuses any unregistered credential under `/root`, `/tmp` or `/home`, reporting its path without its value; (7) `docker commit` makes the local `pfm-lane-root:<hash>` image. A failed step commits no image and leaves diagnostics at `/tmp/{project}/lanes/root-failures/<hash>-<stamp>.txt`. A commit that loses its containerd lease is committed again, up to three attempts, each lost attempt leaving its own diagnostics file. Fence housekeeping skips its dangling-image prune while any `pfm-lane-build-*` container runs, from any checkout.

`<hash>` covers the content of the root's build inputs listed by `HASH_PATHS` in `root.sh`: the pfm product and templates, `VERSION`, `docs/SETUP.md`, `docs/PLACEHOLDERS.md`, the pre-push hook, the fence image files, and `root.sh`, `container.sh`, `provision.sh`, `adopt.sh`, `cred-scan.sh`, `fixtures/` and `scenarios/`. The hash is the same whether that content is dirty, staged or committed; untracked build inputs count too. Test files, testdata and the e2e tree are excluded. A lane-script, `lib.sh` or registry edit reuses the root. A matching image is printed as `REUSE`; the root stays local and `root.sh` refuses a registry tag.

**Seats.** `--seats cc:1` names the primary Claude seat for the run, not the roster. Both fixture seats always exist; `SEAT` names the first requested `cc:` seat and `ALT` or `SPARE` names the other.

## The fake engine

`provision.sh` stages `scenarios/default.json` as the default scenario. The mock engine chooses its Claude, Codex or OpenCode behavior from its invoked name and records panes and transcripts that pfm reads. A beat can script one turn with `mock_steps '<json>'`: the inline steps include `turn`, `hold`, `tool_call`, `compact` and `crash`. `wait_prompt <chat> <needle> <seconds>` proves pfm's transcript reader saw the submitted prompt; `wait_last` can then prove the scripted reply. See `infra/fence/lanes/scenarios/` and `lib.sh` for the fixtures and helpers. Assertions judge pfm's report, pane, transcript or state, never the mock's answer alone.

## Read a red row

1. Find it in `timeline.tsv`: `E1 · E1.09-reload-while-busy · t+412 · fail · 63 · cc:1 · L32 X35 X36 L34 · <why>`. The `t+` stamp is the position in the lane, so a sequence red row can be replayed in order.
2. Open `<lane>.log` at that beat: the assertion's own words, then the raw pane bytes (escapes included) as they were when the beat failed, then the beat's slice of the activity log.
3. Reproduce it alone: `run.sh --lanes E1` — the lane's `need` prelude rebuilds what the sequence had built, so a solo run reaches the same beat.

Four verdicts, and no fifth:

- `✓` the assertion held.
- `✗` it did not. The lane keeps going to its end, so one report carries every failure.
- `known` the beat is an entry in `infra/fence/lanes/known-gaps.yml`: counted apart, does not fail the run. **A listed beat that PASSES is a red row** (`known-gap now passes — remove the entry`), an entry with no `expires:` or past it makes the run red before a single lane starts, and an `arch:`-scoped entry is a gap only on that architecture.
- `blocked` a declared precondition failed — a beat before it (`blocked-by <beat>`), a named capability such as O2.01b's `clock-door`, or the chat itself: a beat that declared `target_live <chat>` and finds no live row for it is blocked in seconds, `blocked-by` the beat that last saw that chat alive. Never `✗` for someone else's failure, and never silence.

**A dead chat costs seconds, not minutes.** Every wait (`wait_last`, `wait_for`) abandons a beat's declared live chat the moment its row dies and says which happened — `timed out after Ns` or `has no live row` are different findings. A lane waits on the state it needs with `wait_for`, `tui_wait` or a row reader; a check made immediately after a wait or a `pfm` verb waits on the exact state it asserts, never on poll slack or a fixed sleep. Every `sleep` left in a lane script is a poll step, a window that must elapse, or a planted payload, named in the comment above it. After the first blocked beat the lane spends its ONE `lane_reopen` command to bring the chat back, `need`-style and never in a loop; whether it came back is a named line in the lane log. Where a reboot takes the NAME off the live session — `/reload --new` leaves the label with the id it replaced and auto-names the reborn session from its steer — a beat follows the chat by its tmux SOCKET instead (`anchor_socket <socket>`, column 11 of `pfm ls --tsv`, unchanged across the reboot) and renames it back with `pfm chat name <id> <name>`.

A beat also fails on a dirty activity log: `lib.sh` snapshots `<pfm home>/log/pfm.jsonl` before each beat and fails it on any `"level":"error"` record in its own slice that no `expect-log <pattern>` declared.

## Budgets

`infra/fence/lanes/budgets.yml` carries pinned seconds for every lane and the sequence, measured as the median of three green sequence runs and judged at ×1.25 in solo and sequence runs. A new lane starts with `unpinned`: run.sh records its wall and says `unpinned — recorded, not judged`. A lane with no row at all is red (`UNBUDGETED`). Ratchet pinned values DOWN only, the rule `pfm/.testtiming.yml` already follows. The targets are a root build ≤ 15 min and the full sequence ≤ 5 min with the root cached.

```bash
infra/fence/lanes/run.sh --check-budget E1 700   # the verdict for a recorded wall, by hand
```

## Extend it — a new command or MCP tool lands with its beat, in the same commit

1. Add its beat to `infra/fence/lanes/beats.md` under its lane, naming what it asserts and the seat it spends.
2. Add the `name · lane · beat` row to `infra/fence/lanes/map.tsv`: `name` is the command as typed (`pfm chat new`) or the MCP tool's name (`chat_ls`).
3. Write the beat in `infra/fence/lanes/<lane>.sh` using `lib.sh`: `beat <id>`, `spends <seat>`, `target <chat>` (or `target_live <chat>` when the beat cannot assert anything without that chat alive), then exactly one of `pass` / `fail` / `known` / `blocked`. Script its prompt with `mock_steps` and verify delivery with `wait_prompt`; assert from pfm's own report or the pane.
4. Run the gate: `infra/fence/lanes/check-map.sh --pfm <a pfm built from this tree>`. It fails on a map row whose beat no lane carries; on any command in `pfm --help` or MCP tool the map does not carry (derived from the binary); and on a row naming a command or tool pfm does not serve (a verb missing from the help tree is asked of pfm's dispatcher). If it cannot build or drive pfm it prints `DERIVE-FAILED: <why>` and exits 2; it never reports clean for a check it could not run.

```bash
# the gate, with a pfm built inside the fence (the worktree mount is read-only)
.claude/scripts/dev.sh iso run "cd pfm && go build -o /tmp/pfm ./cmd/pfm && cd /worktree && bash infra/fence/lanes/check-map.sh --pfm /tmp/pfm"
```

The harness has its own tests — plain bash, no docker, no model:

```bash
for t in infra/fence/lanes/tests/*_test.sh; do bash "$t" || break; done
```

They cover the beat library's verdicts and the known-gap red rows, the runner's plan/order/mode/budget verdicts, `cred-scan_test.sh`'s registered-fixture credential gate, the stdio exchange helper shared by check-map and E2, and the map gate's findings. Run these tests inside the fence through `.claude/scripts/dev.sh iso run`.
