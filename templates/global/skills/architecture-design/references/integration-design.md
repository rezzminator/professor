# Integration Design

Design a project's integration suite as lanes over one shared state: every capability inventoried from code, every capability mapped to a step that asserts it, the steps ordered so features meet each other the way they do in production. The output is a design document plus the file skeletons a build hand fills; this reference edits no code.

## Contents

1. Inputs
2. Terms
3. Laws
4. Procedure — landscape, doors, research, shared state, lanes, crossings, map gate, harness, owner review
5. Output — the design document
6. Hand-off to the build and test hands
7. Seed evidence
8. Checks

## Inputs

- target: a project, or one subsystem of it
- the live tree, its entry-point registries (router, CLI tree, job registry, tool list) and its existing tests — read, never recalled
- the project's CLAUDE.md: its test tiers, isolation rules, and what a test run may never touch
- the owner, for the rulings in step 9

## Terms

- landscape: the flat, id-numbered list of everything a user, operator or integration can ask the system to do
- door: a place code touches the outside — clock, environment, filesystem, subprocess, socket, third-party API, model
- seam: the interface plus its fake that stands at a door
- tier U: unit tests, every door behind a seam, parallel, seconds
- tier A: hermetic end-to-end — the real binary or server against mocks, no network, no credential
- tier B: live lanes — the real system against its real dependencies
- lane: one actor pursuing one kind of goal, depth-first, as an ordered list of beats
- beat: one step of a lane — an action plus the assertion of its result
- crossing: a piece of shared state that two lanes touch from different sides
- root: the installed, configured, seeded environment every run starts from

## Laws

1. U and A gate every commit; B gates wave close and release. A commit gate holds no credential and calls no paid or live dependency.
2. One scripted mock per external dependency family, speaking the real protocol from a scenario file. Its protocol shapes are golden files captured from the real dependency during tier B; a capture that differs from the golden is a red beat naming the drifted shape.
3. Tier B runs one environment per run, lanes in sequence, each lane inheriting the state earlier lanes built. Concurrency is a scripted beat (a storm, two writers), never a scheduling mode of the runner.
4. Any lane runs alone: it opens with a `need` prelude that creates its preconditions when absent and is a no-op when the sequence already built them. A solo lane is the working loop for that area; the full sequence is the close and release gate.
5. Lane order: state builders, then readers over the richest state, then destroyers. A lane with a destructive tail (uninstall, offboarding, purge) splits into an early half and a final half.
6. Every crossing is asserted by two lanes, and each heavy beat is followed somewhere by a beat asserting that what another lane built still works.
7. A beat passes on its asserted result plus a clean activity log. Assertions read the system's own reports, stored state and rendered output; expected values are literals or fixtures.
8. A known issue is fixed. The known-gap list holds only what cannot be fixed in this repo (an upstream defect, absent hardware); each entry carries an owner and an expiry; an expired entry, an entry without an expiry, and a listed beat that passes are each red. Its healthy state is empty.
9. Every check names its own broken state: a beat behind a failed precondition reports `blocked-by <beat>`; an enumerator that could not run exits non-zero with `DERIVE-FAILED`; an absent log reports `ABSENT`. A failed beat stores raw evidence — the exact response or screen bytes and the log slice.
10. Wall budgets per lane and per sequence are pinned from the median of three green runs and ratchet down only; each recorded number carries the load it was measured under.
11. The product writes one structured activity log per environment root, levelled per environment (test and pre-release default to debug), with a redaction test for credentials, tokens, prompt bodies and personal or regulated data. Lane fixtures are synthetic.
12. Where the product includes model-driven steps, the release rehearsal runs on a weaker, non-frontier model at high effort: reaching the asserted end state there proves the instructions; a frontier pass proves less.

## Procedure

### 1. Landscape — closed-world, from code

Enumerate every surface the project has: entry points (CLI commands and flags, routes, UI screens and actions, webhooks, scheduled jobs, queue consumers) · integrations (each third-party API, model call, mail, storage, identity provider) · lifecycle (install, migrations, config, flags, upgrade from the previous release, uninstall) · identity (roles, tenants, permission boundaries, credential expiry) · data lifecycle per core entity (create, read, update, export, archive, delete) · operations (health, admin, repair) · the tests that exist per tier.

One collection pass per surface, in parallel, each returning rows with `file:line` and naming what it could not read. Merge into `landscape.md`, one line per item:

```
<id> · <item> · needs:<deps> · today:<U|A|B|NONE> · <source file:line>
```

Ids carry an area prefix. State the `NONE` count — the size of the hole.

### 2. Doors

Trace the whole tree for every door, per package: which sit behind a seam, which tests still reach the real thing. The result sets tier U's seam plan — one clock, one process runner, one environment reader, one client interface per external service, each with a fake — and a ratchet that counts bare doors outside the seam packages and only shrinks.

Choose the ten host or environment edge cases that bite this project and build each as a fixture function used by at least one test in the package owning the behaviour. Candidates: missing or read-only home · symlinked config dir · case-folding filesystem · missing optional tool or an old version · no service manager · bare locale or terminal · paths with spaces and non-ASCII · expired or absent credential · stale artifacts from a crash · two concurrent writers.

### 3. Research — two passes, in parallel with steps 1–2

- Neighbours: the 8–12 projects closest in shape (same runtime problem, same kind of installer, same protocol, same UI medium). For each read the CI configuration, the contributing and testing docs, and the regression post-mortems. Extract: tiers and what each fakes · whether the merge gate touches a live or paid dependency · OS matrix · serialisation of shared-resource suites · snapshot rules for user-visible output · flaky-test policy · release and canary channel.
- Literature: hermetic and ephemeral environments · flaky-test economics · test-size taxonomy and budgets · release-chain staging · known-gap ledgers that stay honest · the domain's own hard part (a terminal, payments, regulated data, a model in the loop).

Each pass returns a cited report ending in patterns to adopt, patterns to avoid, and open questions; a source it could not fetch is named unverified, never summarised from memory. Fold every adopted finding into the design as a decision with its source, in a Prior art section per tier. A question the sources cannot answer for this machine or this codebase (a parallelism sweet spot, a wall budget) is marked `measure here` and becomes a measurement task.

### 4. Shared state

List every store features meet in: tables, caches, queues, filesystem layout, daemons, per-tenant config, sessions. For each: its writers, its readers, what can corrupt it.

### 5. Lanes

Cut lanes by actor journey, never by module — five to nine: one per primary role's core journey · one per integration or protocol surface · one for tenant or admin operations · one for onboarding and upgrade · one for operations and the destructive tail. Inside a lane go depth-first, carrying state forward: the record created in beat 2 is the one edited in beat 5 and exported in beat 9. Order the lanes by law 5 and write each position's reason. Give every lane its `need` prelude.

`beats.md`, one line per beat:

```
<lane>.<nn>-<slug> · <what it asserts> · spends:<quota, paid calls, seats> · <landscape ids>
```

### 6. Crossings

For each store from step 4, every writer–reader pair living in different lanes is a candidate; keep those where a regression would otherwise hide in one lane's blind spot. Add the after-effect beats of law 6 in the form: after `<lane X's heaviest beat>`, `<what lane W built>` still `<does its job>`. Probe list: one entity touched by two roles · an operation landing while another actor is mid-flow · an expired or absent credential seen at two surfaces (absence versus error) · a config change under live sessions · two writers under two load shapes · a dependency restart with work in flight · a delete while something still references the target. Record each as `crossing · lanes · why twice` and map its landscape ids to both beats.

### 7. Map gate

`map.tsv` — `landscape_id · lane · beat` — covers every id. The gate fails on an unmapped id, on a mapped beat no lane file defines, and on any route, command, job or tool name the built system reports that the map lacks (derived by dumping the router, the help tree, the job registry, the tool list). Lanes still to be written sit in `pending.txt`, which shrinks to empty. A new capability lands with its landscape row and its beat in one commit.

### 8. Harness contract

- Beat library, one implementation sourced by every lane, demo and smoke script: `beat <id> <landscape-ids…>` · `pass` · `fail <why>` · `known <gap-id>` · `blocked <by>` · `need <name> <check> <make>` · `spends <resource>` · `expect-log <pattern>`. Around each beat: record the activity log's offset before, scan the slice after; an `error` record no `expect-log` declared fails the beat.
- Runner: `run [--lanes A,B] [--root reuse|rebuild] [--dry-run]` — one environment, canonical lane order whatever order was given; streams `✓ ✗ known blocked` lines with the lane prefix; writes per-lane logs, `timeline.tsv` (`lane · beat · t+s`), `summary.md`, and a `lane · wall_s · beats · failed · known · blocked` table; the header names the mode (`solo` or `sequence`) and the lanes that ran before; exit 1 on a failure outside the known-gap list, an unmapped id, or a budget breach, each named.
- Root: built once, snapshotted (container image, database template, VM snapshot), keyed by a hash of everything that shapes it — source, migrations, seeds, templates; an equal hash reuses the snapshot. A root holding secrets stays local.
- Run isolation: its own home and config root, database or schema, ports; credentials copied in at run time from the operator's machine, never from the repository or CI.
- Self-tests for the harness, each failure path watched failing before its code exists.

### 9. Owner review

Present the research findings and the draft design one plain line each, with the decision each implies. The owner rules; every ruling moves from Open rulings into the law or section it changes. Rulings that usually surface: real dependency versus mock per tier · shared versus isolated state · known-gap policy · logging framework (prefer the language's standard structured logger) · which model and effort run the release rehearsal · which numbers are measured here.

## Output — the design document

Sections, in this order, at the path the brief names:

1. Tiers — what each asserts, what each fakes, which gate it guards
2. Landscape summary — counts per area, the `NONE` count; `landscape.md` beside the document
3. Doors and seams — the seam plan, the ten fixtures, the bare-door ratchet baseline
4. Prior art — adopted, avoided, `measure here`, each with its source
5. Shared state — stores, writers, readers
6. Lanes — order with reasons, `beats.md`, the `need` preludes
7. Crossings — the table and the after-effect beats
8. Map gate — `map.tsv`, the derivation commands, `pending.txt`
9. Harness contract — library, runner, root hash inputs, budgets, known-gap file shape
10. Activity log — destinations, levels per environment, record fields, the redaction test
11. Build order — isolation layout · timing ratchet · seams, fakes and fixtures, then package batches with disjoint file sets · activity log · mocks · harness with the first lane solo, then in sequence · remaining lanes with their after-effect beats · known gaps closed · close: three green sequences, budgets pinned, the sequence wired into the release procedure
12. Open rulings — every number or trade-off the owner decides

## Hand-off to the build and test hands

The design document goes through SKILL.md § Hand-off. In a project installed from this blueprint, the suite is then built and kept by the per-project agents under `{project}/.claude/agents/`:

- `developer.md` builds the harness, the mocks and the lanes from the wave specs of Build order.
- `qa.md` runs them: § Scope maps onto lanes — TARGETED runs the solo lanes owning the touched area, FULL and POST-MERGE run the sequence; Step 6 compliance checks and § QA fix chain apply to a defect a lane exposes.
- `docs/commands/build/references/qa-commons.md` §§ Test validity, Run verdicts, Integration lanes carry the rules both hands share with this reference; a project without the blueprint applies Laws 7–9 directly.

## Seed evidence

Findings from the first application of this procedure; a starting point for step 3, re-verified per domain.

- Agent-runner CLIs surveyed (three of them) run no provider key in their merge gate; live-model runs never block a merge.
- The closest published analogue of a tool that writes into a user's home tests in four tiers: unit, in-memory filesystem, the real binary under a redirected home, then an operating-system sweep. Host-edge fixtures buy the last tier's coverage at unit speed.
- One terminal multiplexer drives its real binary end to end and keeps raw terminal bytes for a failed run; a structurally similar session manager ships no tests for its terminal layer and cross-compiles as its only gate — a gate that is green whether or not the feature works.
- Flaky-test study over a large CI fleet: async wait 45 %, concurrency 20 %, order dependency 12 %; 24 % of flaky-test fixes changed the code under test, nearly all real defects — a flaky row is a defect ticket with an owner, and test order is shuffled in one scheduled leg.
- The one known-gap mechanism that stays honest fails the build when a listed gap passes; a ledger requiring only a bug link accumulates stale rows.
- Release-chain staging in the literature: presubmit (fast, hermetic) → postsubmit → release candidate at rising fidelity → production probers, which also audit whether the tests still matter.
- A published vendor post-mortem: three regressions shipped together because user reports were indistinguishable from normal variation — gates emit named, deterministic failures, and a number is trusted after a soak.
- Tool-protocol servers have a public conformance client; an independent implementation of the spec judges better than self-written assertions — validate it before adopting.
- No source quantifies a parallelism sweet spot or a suite-time ratchet; both are measured on the project's own machine, never on a machine running other builds.

## Checks

```sh
L=<lanes dir>; D=<landscape.md>
comm -23 <(grep -oE '^[A-Z]+[0-9]+' $D | sort -u) <(cut -f1 $L/map.tsv | tail -n +2 | sort -u)      # unmapped ids
comm -23 <(cut -f3 $L/map.tsv | tail -n +2 | sort -u) <(grep -ohE '[A-Z0-9]+\.[0-9]+-[a-z0-9-]+' $L/*.sh $L/pending-beats.txt | sort -u)   # mapped beat defined nowhere
<dump routes|help tree|jobs|tools> | sort -u | comm -23 - <(<names carried by the map> | sort -u)   # capability the map lacks
awk '/expires:/{n++} /beat:/{b++} END{exit !(n==b)}' $L/known-gaps.yml                              # every gap entry has an expiry
grep -c . $L/pending.txt                                                                            # lanes still unwritten → 0 at close
grep -rnE '<bare door spellings>' <src> | grep -v '<seam packages>' | wc -l                          # bare-door ratchet
```

Adapt the id pattern, the derivation commands and the door spellings to the tree; a check that cannot be expressed as shell goes under Open rulings.
