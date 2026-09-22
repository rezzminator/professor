# Tier B lanes — the live DFS suite: one root, one container, lanes in sequence

**Home:** `infra/fence/lanes/`. **Coverage index:** `infra/fence/lanes/beats.md` (per-lane beats) + `infra/fence/lanes/map.tsv` (`landscape-id · lane · beat`) over `docs/dev/testing/landscape.md`.

Tier B drives the REAL binary against REAL engines: real Claude Code, Codex and OpenCode processes, real tmux panes, real seats, real model turns. It is the release gate and the on-demand suite, never a per-commit one — Tier U (unit) and Tier A (e2e, jailed) run at every commit. Tier B runs on linux containers only; darwin has no Tier B.

## The shape

```
pfm-lane-root:<hash>          built once per template change (root.sh), never pushed
   └─► ONE container per run  the fleet's state accumulates lane after lane:
       O1 → E1 → E2 → E3 → F → M → A → O2
```

- **One container, lanes in sequence.** Every lane inherits the pfm state the lanes before it built: E1's named chat is still alive when F storms, when M restarts the daemon, when A rewrites the adopter's hooks. pfm is ONE state (one daemon, one `fleet.db`, one tmux server, one hook set) and the effect of each area on that shared state is where the bugs live.
- **Any lane runs alone.** `run.sh --lanes M` starts a fresh container from the same root and runs M only; its `need` prelude makes the preconditions the sequence would have made (a live chat, the daemon, a working directory) and is a no-op when they already exist. A solo lane is the dev/qa loop; the sequence is the wave-close and release gate.
- **No `--parallel`, ever.** Concurrency is a scripted beat (`storm`, two writers, inject-during-busy), never a scheduling strategy: two lanes racing on one fleet would make every red row order-dependent.
- **Order is a design decision:** state builders first, readers over the richest state, destroyers last. Lane O is one area with two entry points — **O1** before any chat exists, **O2** the destructive tail that ends with `uninstall`.

## Run one lane

```bash
infra/fence/lanes/run.sh --lanes E1 --dry-run     # the plan: hash, image decision, beats, seats
infra/fence/lanes/run.sh --lanes E1                # solo, one Claude seat, from the root image
```

`--dry-run` executes nothing — no container, no model turn — and is the cheap way to see what a run would cost. A run prints `✓ / ✗ / known / blocked` per beat with the lane prefix and writes `/tmp/{project}/lanes/<stamp>/`:

| file | what it carries |
| --- | --- |
| `<lane>.log` | every beat line, plus a failed beat's raw pane bytes (`tmux capture-pane -e -p -S -`) and its activity-log slice |
| `<lane>.stream.log` | exactly what the lane printed, as it printed it |
| `timeline.tsv` | `lane · beat · t+s · verdict · dur_s · seat · landscape_ids · detail` |
| `lanes.tsv` | `lane · wall_s · beats · failed · known · blocked` (the Wave 2 TSV shape) |
| `summary.md` | the header (mode, root image, order, seats), the table, the budget verdicts, the verdict |

## Run the sequence

```bash
infra/fence/lanes/run.sh                            # every WRITTEN lane, canonical order
infra/fence/lanes/run.sh --lanes O1,E1 --seats cc:1 # a slice of it, still in canonical order
infra/fence/lanes/run.sh --root rebuild             # force a fresh root image first
```

Lanes named on `--lanes` in any order run in canonical order. A lane that is not written yet is listed in `infra/fence/lanes/pending.txt`: naming it explicitly is refused (exit 2), and a bare `run.sh` drops it with a named `NOT WRITTEN` line rather than shrinking the sequence silently. The header names the mode (`solo` / `sequence`) and, in sequence mode, the lanes that ran before each lane.

**The root image.** `root.sh` builds it from the `pfm-dev` fence image: pfm compiled from the mounted tree, the real engines installed (`infra/demo/setup.sh tools`), the seats' credentials staged (`lanes/creds.sh`), `pfm install --yes` plus the seats' first-run state (`infra/demo/setup.sh install`), express adopted by a real Claude chat (`infra/demo/adopt.sh`, ~10–15 min, one seat), then `docker commit`. `<hash>` covers the tracked content of `pfm/**`, `templates/**`, `docs/SETUP.md`, `infra/fence/**` plus the worktree's dirty diff, so an uncommitted edit changes the hash and can never be served by a stale image. Same hash → the image is reused, printed by name. It carries real seat tokens: it is local only, `root.sh` never pushes and refuses a `--tag` that names a registry. `root.sh --no-adopt` builds without a single model turn (no interview, no OpenCode liveness probe) — the way to prove the build path for free.

**Seats.** `--seats cc:1` is not a hint: it is the container's whole Claude roster. `run.sh` hands the run's `cc:` seats to `root.sh --accounts`, the roster is a root-hash input (so a one-seat image is never reused for a two-seat run), and `lanes/creds.sh` drops any seat this host cannot log in — by name, in both modes. A lane therefore never reads a second seat out of the config and finds nothing staged for it; with one seat the account-switch beats report `blocked`, not `✗`.

**Credentials.** `lanes/creds.sh` reads the seats from pfm's own config and stages each one at the container's `~/.cc/<id>`: on darwin through the Keychain (`infra/demo/creds.sh` is the reader), on linux by copying each seat's `.credentials.json` plus the Codex and OpenCode auth files. Every seat is reported by name; a missing one is `seat 2 (🥈): NO CREDENTIAL — <why>`, never an empty success, and no credential body is ever printed. A copied seat shares the host's refresh token — the first refresh inside the container rotates it, so the host seat may need a re-login; that is the accepted cost of a reusable root.

## Read a red row

1. Find it in `timeline.tsv`: `E1 · E1.09-reload-while-busy · t+412 · fail · 63 · cc:1 · L32 X35 X36 L34 · <why>`. The `t+` stamp is the position in the lane, so a sequence red row can be replayed in order.
2. Open `<lane>.log` at that beat: the assertion's own words, then the raw pane bytes (escapes included) as they were when the beat failed, then the beat's slice of the activity log.
3. Reproduce it alone: `run.sh --lanes E1` — the lane's `need` prelude rebuilds what the sequence had built, so a solo run reaches the same beat.

Four verdicts, and no fifth:

- `✓` the assertion held.
- `✗` it did not. The lane keeps going to its end, so one report carries every failure.
- `known` the beat is an entry in `infra/fence/lanes/known-gaps.yml`: counted apart, does not fail the run. **A listed beat that PASSES is a red row** (`known-gap now passes — remove the entry`), an entry with no `expires:` or past it makes the run red before a single lane starts, and an `arch:`-scoped entry is a gap only on that architecture.
- `blocked` a declared precondition failed — a beat before it (`blocked-by <beat>`), the run's own shape (`blocked-by seats cc:1 — no second seat in this run`), or the chat itself: a beat that declared `target_live <chat>` and finds no live row for it is blocked in seconds, `blocked-by` the beat that last saw that chat alive. Never `✗` for someone else's failure, and never silence.

**A dead chat costs seconds, not minutes.** Every wait (`wait_last`, `wait_for`) abandons a beat's declared live chat the moment its row dies and says which happened — `timed out after Ns` or `has no live row` are different findings. After the first blocked beat the lane spends its ONE `lane_reopen` command to bring the chat back, `need`-style and never in a loop; whether it came back is a named line in the lane log. Where a reboot takes the NAME off the live session — `/reload --new` leaves the label with the id it replaced and auto-names the reborn session from its steer — a beat follows the chat by its tmux SOCKET instead (`anchor_socket <socket>`, column 11 of `pfm ls --tsv`, unchanged across the reboot) and renames it back with `pfm chat name <id> <name>`.

A beat also fails on a dirty activity log: `lib.sh` snapshots `<pfm home>/log/pfm.jsonl` before each beat and fails it on any `"level":"error"` record in its own slice that no `expect-log <pattern>` declared. Until Wave 6 lands that file does not exist, and the run summary says so by name: `activity log: ABSENT (Wave 6 not landed) — log assertions not enforced`.

## Budgets

`infra/fence/lanes/budgets.yml` carries a row per lane plus the sequence, with tolerance ×1.25. Every row reads `unpinned` today: run.sh RECORDS the wall and says `unpinned — recorded, not judged` rather than judging it. A lane with no row at all is red (`UNBUDGETED`). Pin a number from the median of three green runs and ratchet DOWN only, the rule `pfm/.testtiming.yml` already follows. The spec's targets until then: root ≤ 15 min, a solo lane ≤ 12 min, the full sequence ≤ 75 min with the root cached.

```bash
infra/fence/lanes/run.sh --check-budget E1 700   # the verdict for a recorded wall, by hand
```

## Extend it — a landscape item lands with its beat, in the same commit

1. Add the item to `docs/dev/testing/landscape.md` with its `lane(s):` column. The file is machine-read: line 1 is `<!-- rumdl-disable -->`, the inline marker rumdl honours in `fmt` too, so neither the format-md hook nor a bare `rumdl fmt` reflows its id rows (`check-map.sh` fails `LANDSCAPE-FORMATTABLE` without it and, with rumdl on PATH, formats a copy and demands byte-identity).
2. Add its beat to `infra/fence/lanes/beats.md` under that lane, naming what it asserts, the seat it spends and its landscape ids.
3. Add the `landscape-id · lane · beat` row(s) to `infra/fence/lanes/map.tsv`.
4. Write the beat in `infra/fence/lanes/<lane>.sh` using only `lib.sh`: `beat <id> <landscape-ids…>`, `spends <seat>`, `target <chat>` (or `target_live <chat>` when the beat cannot assert anything without that chat alive), then exactly one of `pass` / `fail` / `known` / `blocked`. Assert from pfm's own report or the pane — never from what a model said.
5. Run the gate: `infra/fence/lanes/check-map.sh --pfm <a pfm built from this tree>`. It fails on an unmapped landscape id, on a mapped beat no lane carries, and — machine-derived from the binary — on any `pfm --help` command or MCP tool name the map does not carry. If it cannot build or drive pfm it prints `DERIVE-FAILED: <why>` and exits 2; it never reports clean for a check it could not run.

```bash
# the gate, with a pfm built inside the fence (the worktree mount is read-only)
.claude/scripts/dev.sh iso run "cd pfm && go build -o /tmp/pfm ./cmd/pfm && cd /worktree && bash infra/fence/lanes/check-map.sh --pfm /tmp/pfm"
```

The harness has its own tests — plain bash, no docker, no model:

```bash
for t in infra/fence/lanes/tests/*_test.sh; do bash "$t" || break; done
```

They cover the beat library's verdicts and the three known-gap red rows, the runner's plan/order/mode/budget verdicts, the credential staging (including "no token on stdout"), and the map gate's findings. Each failure path was watched red against a deliberately broken copy of the script it guards before it was trusted.
