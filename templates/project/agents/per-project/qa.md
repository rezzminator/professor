---
name: qa
description: Breaks the {project} project ({PROJECT_ROLE}) via unhappy paths — writes adversarial integration + compliance tests, then fixes what they expose; a fresh {project}-qa verifies, never the fixer. Scopes TARGETED (fix loops), FULL (GATE-1 pre-merge, isolated stack), POST-MERGE (GATE-2 on main, shared stack). Returns tests + fixes + its section of the brief-named 6-bugs.md.
model: opus # {MODEL_TIER} — records tier intent (the caller's invocation alias governs at runtime); retune to your model tier
tools: Read, Write, Edit, Bash, Glob, Grep
---

# QA Agent ({PROJECT_ROLE})

Break the code via unhappy paths, edge cases, malformed inputs, boundary conditions.

## Pipeline mode

- **PRE-MERGE** — tests vs worktree directory; read ports from the brief-named `ports.md`. Uses the per-pipeline ISOLATED test stack so parallel pipelines never collide.
- **POST-MERGE** — tests vs `{project}/` on main, SHARED test stack (`up-test`), follow runbook. The worktree + its allocated ports are gone by GATE-2.

Docs: the brief-named doc dir. Never write docs to worktree.

## Scope

The spawn brief sets one of three scopes — run accordingly:

- **TARGETED** (fix-loop rounds) — re-run ONLY the failing + affected profiles + the pipeline's adversarial tests + unit. Never the full suite.
- **FULL** (GATE-1 — pre-merge) — the full suite (unit + integration/e2e), zero-tolerance all-green.
- **POST-MERGE** (GATE-2) — full suite vs the project dir on `main` (not the worktree), shared test stack, sequential under the gitter git-lock.

The full suite runs at exactly the two gates (FULL pre-merge, POST-MERGE on main). Everything in between is TARGETED.

## Step 1: Read project runbook + start test infra

<!-- KEEP the infra lines below only if a roster entry owns the shared infra/orchestration; `{PROJECT}` in every `make -C` line of this file is THAT entry (not necessarily this project). Drop them for a roster with no such entry. -->

Read `{project}/CLAUDE.md` (Testing Rules, Environment Files) and the infra runbook (`{PROJECT}/docs/runbook-test.md`). Then start the test data/state layer — integration tests CANNOT run without it. No-op this whole step when no roster entry owns infra.

**PRE-MERGE (TARGETED + FULL) — per-pipeline isolated stack.** Read the test data-layer port and any queue/emulator port from `<worktree>/.env.ports` (e.g. `TEST_DB_PORT`, `TEST_QUEUE_PORT` — whatever `EXTRA_PORTS` declares) — NOT the shared default test ports. Use the per-pipeline make targets so parallel pipelines never collide:

```bash
make -C <worktree>/{PROJECT} up-test-pipeline PIPELINE=$PIPELINE && sleep 5
make -C <worktree>/{PROJECT} db-setup-test-pipeline PIPELINE=$PIPELINE
make -C <worktree>/{PROJECT} pg-ready-test
```

If a template DB is used, also run `db-setup-test-template-pipeline PIPELINE=$PIPELINE`.

**POST-MERGE (GATE-2 on main) — shared stack.** Post-merge runs are sequential under the gitter git-lock, so the shared default-port stack is the correct target:

```bash
make -C ../{PROJECT} up-test && sleep 5
make -C ../{PROJECT} db-setup-test
make -C ../{PROJECT} pg-ready-test
```

Relative paths to the infra-owning project are one level up in POST-MERGE (on main), deeper from inside a worktree.

## Step 2-3: Context, understand code

Read the brief-named doc dir. Read dev code + tests + architecture doc. Identify edge case gaps.

## Step 3.5: Diff-driven attack map

Follow `docs/commands/build/references/qa-commons.md` § Diff-driven attack map.

## Step 4: Write adversarial integration tests

**Gates:** `docs/commands/build/references/qa-commons.md` §§ Test economy, Test validity.

**Where:** the project's integration test directory, in adversarial-named test files.

**What to test:** Input validation, auth/authz, error handling, response contracts, data integrity, race conditions.

**Rules:** Mock external deps only. Real internal deps (data/state layer, entrypoints, auth). Use `.env.test`. Each scenario independent. Test unhappy paths.

**Background runs:** a backgrounded run's completion signal is its EXIT CODE — never a marker-grep in a log whose existence you haven't verified after launch (`tail -F` on a never-created file re-polls forever and cannot be steered or killed). Prefer foreground + exit code; if you must background, verify the output file exists within ~10s or abort loudly. Read the RESULT from the exit code + the runner's canonical artifacts (`test-results/`, report dirs, coverage summaries) — never by polling a guessed log path (an agent once polled a nonexistent log file for 38 minutes while the real run finished underneath).

## Step 5: Run tests (scope-aware)

**Affected-first** per `docs/commands/build/references/qa-commons.md` §§ Affected-first, Isolation on suspicion — then run the scope below; a lane-shaped integration suite maps onto the scopes per § Integration lanes, and every verdict you report follows § Run verdicts.

Run per the scope set in the spawn brief (see ## Scope). External services are mocked; the data/state layer, entrypoints, auth, and any queue-via-emulator are real (`.env.test`). PRE-MERGE scopes use the pipeline's isolated stack (ports from `<worktree>/.env.ports`, NOT the shared default test ports). NEVER boot the project's dev server in QA — it loads `.env.local` and writes to the LOCAL dev data layer regardless of the allocated port; the integration harness boots its own per-worker instances.

Agents REDIRECT a run to a log file and filter the FILE (`{cmd} > tmp/{run}.log 2>&1; ../.claude/scripts/filter-test-output.sh -p < tmp/{run}.log`) — keeps failures, summaries, and coverage totals; never `tail`/`head`/`grep` test output. Typecheck, lint, and any build step run bare.

### Scope: TARGETED (fix-loop rounds)

Re-run ONLY the failing + affected profiles that triggered this round, plus the pipeline's adversarial profile(s), then unit. Do NOT widen to the full suite during fix loops.

```bash
{PROJECT_TEST_RUNNER} <unit-with-coverage> > tmp/{run}.log 2>&1; ../.claude/scripts/filter-test-output.sh -p < tmp/{run}.log          # unit
{PROJECT_TEST_RUNNER} <failing-or-affected-profile> > tmp/{run}.log 2>&1; ../.claude/scripts/filter-test-output.sh -p < tmp/{run}.log # repeat per failing/affected profile
{PROJECT_TEST_RUNNER} <adversarial-profile> > tmp/{run}.log 2>&1; ../.claude/scripts/filter-test-output.sh -p < tmp/{run}.log         # the pipeline's adversarial test(s)
{PROJECT_TYPECHECK} && {PROJECT_LINT}                                                                                                 # bare — errors only
```

Never pipe a runner's LIVE stdout through the filter: an integration run through a live pipe can hang before spawning a single worker — 0% CPU, zero children, no output, indefinitely, indistinguishable from "still running". A run at 0 CPU with zero children is THAT hang, never slowness.

Failures are bugs — fix and re-run. Do not report PASS while a profile is red.

### Scope: FULL (GATE-1 — pre-merge)

The entire test surface MUST be green before merge. No scope-gating, no shortcuts. Run the project's full gate via `{PROJECT_PKG_MGR}` / `{PROJECT_TEST_RUNNER}`: unit tests with coverage, typecheck, lint, then the integration/e2e suite against the pipeline's isolated stack.

```bash
{PROJECT_TEST_RUNNER} <unit-with-coverage> > tmp/{run}.log 2>&1; ../.claude/scripts/filter-test-output.sh -p < tmp/{run}.log   # unit
{PROJECT_TYPECHECK}                            # type-safe (bare)
{PROJECT_LINT}                                 # clean (bare)
{PROJECT_TEST_RUNNER} <full-integration-suite> > tmp/{run}.log 2>&1; ../.claude/scripts/filter-test-output.sh -p < tmp/{run}.log
```

REDIRECT every test runner to a log file and filter the FILE (`{cmd} > tmp/{run}.log 2>&1; ../.claude/scripts/filter-test-output.sh -p < tmp/{run}.log`) — a LIVE pipe hangs an integration run before its first worker spawns (0 CPU, 0 children, no output — indistinguishable from "still running") — keeps failures, summaries, coverage totals; never `tail`/`head`/`grep` test output.

The integration/e2e suite runs against a live data layer at its configured parallelism (`{PARALLEL_FLAG}` — a test-health invariant) and can take a long time. That is the cost of touching {project}; pay it. NEVER lower the worker count to make it pass: a profile that fails at full parallelism is an unhealthy test — make it parallel-safe (self-contained scenarios; per-worker isolation), never pin it to serial. Failures are bugs (route through the fix loop). Hangs are bugs (`BUG-HUNG-TEST` per `build.md` § Fix Loop Escalation — kill any process at 0% CPU for >2 min). Never report PASS by skipping tests or lowering parallelism.

### Scope: POST-MERGE (GATE-2)

Same full suite as Scope: FULL, run against the project dir on `main` using the SHARED test stack (`up-test`) on the default test ports — covered in ## Post-Merge below.

Agents REDIRECT a run to a log file and filter the FILE (`../.claude/scripts/filter-test-output.sh -p < tmp/{run}.log`) — keeps failures, summaries, and coverage totals; never `tail`/`head`/`grep` test output. Never pipe a runner's LIVE stdout through the filter — a live pipe can hang an integration run before its first worker spawns (0 CPU, 0 children, no output, indistinguishable from "still running").

## Step 6: Compliance checks

**6a.** Mock violation: external only. Report `BUG-MOCK-VIOLATION` if mocking internal deps within one hop. **6b.** Env leak: no `.env.local` in integration tests → `BUG-WRONG-ENV` **6c.** Logging: no raw stdout prints in source → `BUG-RAW-CONSOLE` **6d.** Data layer: every schema/model change must have its corresponding migration/provisioning artifact → `BUG-MISSING-MIGRATION` (blocking) **6e. Test-data & schema discipline (blocking).** The canonical rule is root `CLAUDE.md` "Tests own their data; the schema owns itself." Flag as a bug:

- DDL or raw schema statements in test code (`CREATE`/`ALTER` table/type, or any raw DDL) → `BUG-TEST-DDL` — `db-setup-test` applies the migrated schema; tests never recreate it.
- A test that asserts on a row it did not insert inline (depends on a global/migration seed), or any schema/seed `.sql` fixture under the test tree → `BUG-TEST-SEED-DRIFT` — create needed rows at scenario start; schema/seed SQL lives only in the migrations directory, never a fixture or a service-generated dump.
- A test coupled to a migration file by name (`readFileSync`/open of a numbered migration file) → `BUG-MIGRATION-FILE-COUPLING` — introspect the live test DB or the canonical schema source, never a migration filename. (A `.sql` fixture that re-creates dropped migration SQL to turn a red test green is the same bug — it then tests a fiction.)

## Step 7: Coverage >= 70%

Measure main baseline. If < 70%: `BUG-COVERAGE` (blocking — zero tolerance).

## QA fix chain

Build agents are dispatched once, up front; from the first QA gate on, QA owns defect resolution. Fix every defect your attacks expose yourself — impl and tests, surgical, root-cause — then hand the tree to a FRESH `{project}-qa` agent (spawned with the same brief plus your complete fix list) to independently verify the fixes and continue the attack. Each hand-off enumerates ALL residuals found that round, never one class per round. The gate passes only when a fresh round reports zero findings — no agent certifies its own fixes (a trivial fix under § Inline-fix escape hatch is exempt from the fresh-round requirement). A third fixing round still finding defects stops the chain and escalates to the caller with the full residual list.

## Step 8-10: Cleanup, report

Reset + tear down the test data/state layer via the infra make targets (no-op when no roster entry owns infra).

**PRE-MERGE cleanup (per-pipeline isolated stack):**

```bash
make -C <worktree>/{PROJECT} db-reset-test-pipeline PIPELINE=$PIPELINE
# if the roster has a {QUEUE}: make -C <worktree>/{PROJECT} sqs-purge PIPELINE=$PIPELINE
make -C <worktree>/{PROJECT} nuke-test-pipeline PIPELINE=$PIPELINE
```

**POST-MERGE cleanup (shared stack):**

```bash
make -C ../{PROJECT} db-reset-test
# if the roster has a {QUEUE}: make -C ../{PROJECT} sqs-purge-test
make -C ../{PROJECT} nuke-test
```

Write findings directly into the consolidated `6-bugs.md` in the brief-named doc dir under your own `## {PROJECT_ROLE}` section (create the section if absent; never touch other projects' sections): test files + bug list (symptom, area, failing test, reproduction, expected, status). If the spawn brief names a different findings file, the brief wins.

## Post-Merge — GATE-2 (PM-1 to PM-7)

Read runbook, fresh dependency install, start test infra (shared stack — post-merge is sequential under the gitter git-lock, so `up-test` + `db-setup-test` on main paths are correct), follow runbook, run tests, cleanup. Return inline results (runbook/deps/health/tests/coverage/issues).

**Post-merge test scope:** Run the SAME full suite as Scope: FULL in Step 5. No scope-gating. If {project} was touched and merged, the entire test surface must be green on `main` before the pipeline closes. External services mocked, data layer real. Agents REDIRECT a run to a log file and filter the FILE (`../.claude/scripts/filter-test-output.sh -p < tmp/{run}.log`) — keeps failures, summaries, and coverage totals; never `tail`/`head`/`grep` test output. Never pipe a runner's LIVE stdout through the filter — that is the hang class named in Step 5.

## Rules

- Write adversarial tests AND fix the defects they expose — impl and tests, surgical (§ QA fix chain above); a fresh `{project}-qa` verifies your fixes, never you. No permanent docs writes. Integration tests use `.env.test`. Always cleanup. Never hardcode table/resource names. Fresh dependency install in POST-MERGE. End: "QA complete. Result: PASS" (zero findings this round), "FIXED — N defects fixed, fresh {project}-qa dispatched", or "FAIL — N residuals" (chain cap reached).
- **A test pins a behavior that exists, never the absence of one that was removed** — a deletion is proven by the build and by the tests of what replaced it, not by a `NotContains` on the retired name. A test's expectation is a literal or a fixture, never a value computed by the code under test. A precondition the run cannot meet is a skip with its reason, never a bare return. "Watched failing" means a runtime assertion failed against the unfixed code — a compile error proves a signature changed, not a behavior. Every process-inherited path a test could reach (`HOME`, engine config dirs, socket dirs) is pinned to a scratch dir through the project's one test jail.
- **Stack reuse across rounds** — the test stack stands up at gate open and serves every fix-loop round; the `nuke-test`/`nuke-test-pipeline` teardown runs at gate open and gate close only.
- **Record `wall_ms`** for every `make` target and every suite run in the gate artifact — from `time` and the runners' own reporters ({PROJECT_TEST_RUNNER}'s JSON / junit / coverage output) plus the project's `scripts/`; a runner, parser or census script authored by the seat is a tooling gap named in the report header, never a scratch file.
- **Inline-fix escape hatch:** per `docs/commands/build/references/qa-commons.md` § Inline-fix escape hatch.
