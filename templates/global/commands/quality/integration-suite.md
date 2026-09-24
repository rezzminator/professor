---
name: quality:integration-suite
description: Designs lanes over shared state — `/quality:integration-suite <project|subsystem>`, "design our integration tests", "refactor the e2e suite", before building any live or end-to-end harness. Returns a design document plus its landscape, beat and map files; edits no code. Source-tree layout → /quality:llm-codebase.
argument-hint: <project|subsystem>
---

# Integration Suite

Every capability is inventoried from code and mapped to a step that asserts it, and the steps are ordered so features meet each other the way they do in production. This command writes the design and its skeleton files only.

## Contents

1. Inputs
2. Terms
3. Laws
4. Procedure — landscape, doors, research, shared state, lanes, crossings, map gate, activity log, harness, owner review
5. Output — the design document
6. Hand-off to the build and test hands
7. Seed evidence
8. Checks

## Inputs

- target: a project, or one subsystem of it
- the live tree, its entry-point registries (router, CLI tree, job registry, tool list) and its existing tests — read, never recalled
- the project's orientation file (CLAUDE.md or equivalent): its test tiers, isolation rules, and what a test run may never touch — absent, the three become Open rulings
- the owner, for the rulings in step 10

## Terms

- landscape: the flat, id-numbered list of everything a user, operator or integration can ask the system to do
- door: a place code touches the outside — clock, environment, filesystem, subprocess, socket, third-party API, LLM or other hosted model. The project's own data layer (its database, cache and queue broker, run locally) is part of the system in tiers A and B, not a door; tier U reaches it only in the packages that own persistence.
- seam: the interface plus its fake that stands at a door
- tier U: unit tests, every door behind a seam, parallel, seconds
- tier A: hermetic end-to-end — the real binary or server against mocks, no network beyond loopback, no credential
- tier B: live lanes — the real system against its real dependencies
- lane: one actor pursuing one kind of goal, depth-first, as an ordered list of beats
- beat: one step of a lane — an action plus the assertion of its result
- crossing: a piece of shared state that two lanes touch from different sides
- root: the installed, configured, seeded environment every run starts from

## Laws

1. U and A gate every commit; B gates every release and the close of each multi-commit piece of work. A commit gate holds no credential and calls no paid or live dependency. Where the platform allows, the commit gate runs with outbound network denied, so an unseamed door fails by name instead of reaching the real thing.
2. One scripted mock per external dependency family, speaking the real protocol from a scenario file. Its protocol shapes are golden files captured from the real dependency during tier B; a capture that differs from the golden is a red beat naming the drifted shape. A request the scenario does not script fails the test by name. Every tier-B beat that does not need the real dependency's judgement gets a tier-A twin against the mock, so the sequence's product-side logic gates every commit.
3. Tier B runs one environment per run, lanes in sequence, each lane inheriting the state earlier lanes built. Concurrency is a scripted beat (a storm, two writers), never a scheduling mode of the runner.
4. Any lane runs alone: it opens with a `need` prelude that creates its preconditions when absent and is a no-op when the sequence already built them. A solo lane is the working loop for that area; the full sequence is that close-and-release gate.
5. Lane order: state builders, then readers over the richest state, then destroyers. A lane with a destructive tail (uninstall, offboarding, purge) splits into an early half and a final half.
6. Every crossing is asserted by two lanes, and each heavy beat (one that loads, restarts, migrates or bulk-mutates shared state) is followed somewhere by a beat asserting that what another lane built still works.
7. A beat passes on its asserted result plus a clean activity log. Assertions read the system's own reports, stored state and rendered output; expected values are literals or fixtures — never an LLM's own claim; a beat that fails on LLM variance alone asserts the wrong thing.
8. A known issue is fixed. The known-gap list holds only what cannot be fixed in this repo (an upstream defect, absent hardware); each entry carries an owner and an expiry; an expired entry, an entry without an expiry, and a listed beat that passes are each red. Its healthy state is empty. A fix flips its beat from known to a real assertion and deletes the entry in the same commit.
9. Every check names its own broken state: a beat behind a failed precondition reports `blocked-by <beat>`; an enumerator that could not run exits non-zero with `DERIVE-FAILED`; an absent log fails the beat as `LOG-ABSENT`, never as a clean log. A failed beat stores raw evidence — the exact response or screen bytes and the log slice.
10. Wall budgets per lane and per sequence are pinned from the median of three green runs and ratchet down only; each recorded number carries the load it was measured under.
11. The product writes one structured activity log per environment root, levelled per environment (test and pre-release default to debug), with a redaction test for credentials, tokens, prompt bodies and personal or regulated data. Lane fixtures are synthetic.
12. Where the product includes LLM-driven steps, the release rehearsal runs on a weaker, non-frontier LLM at its highest reasoning setting: reaching the asserted end state there proves the instructions; a frontier pass proves less.
13. User-visible output — rendered screens, emitted commands, generated files — is pinned by golden files; a change to it ships with its golden in the same commit.
14. A flaky test is a defect with an owner, never a quarantine; a retry applies only to a test already ledgered as flaky, tracked per configuration; test order is shuffled in one scheduled leg, and a shuffle-only failure is a shared-state defect.

## Procedure

### 1. Landscape — closed-world, from code

Enumerate every surface the project has: entry points (CLI commands and flags, routes, UI screens and actions, webhooks, scheduled jobs, queue consumers) · integrations (each third-party API, LLM call, mail, storage, identity provider) · lifecycle (install, migrations, config, flags, upgrade from the previous release, uninstall) · identity (roles, tenants, permission boundaries, credential expiry) · data lifecycle per core entity (create, read, update, export, archive, delete) · operations (health, admin, repair) · the tests that exist per tier.

One collection pass per surface (parallel sub-agents where the runtime has them, else in sequence), each returning rows with `file:line` and naming what it could not read. Merge into `landscape.md`, one line per item:

```
<id> · <item> · name:<the name the registry dumps, or —> · needs:<what the environment must provide to exercise it — a dependency, a credential, a platform; none> · today:<U|A|B joined by +, or NONE> · <source file:line>
```

Ids are `<AREA><nn>` (`AUTH03`, `BILL12`) — an uppercase area prefix then a number, at line start. State the `NONE` count — the size of the hole.

### 2. Doors

Write the door spelling set for the language first — the call spellings of clock, environment, process, network, filesystem outside the scratch root, each third-party SDK — and use that one set for the trace and for the ratchet. Trace the whole tree, one row per door: `<package> · <door kind> · <spelling> · seam:<name|NONE> · real-in-tests:<test file|—> · <file:line>`, then a per-package summary `package · doors · seamed · NONE`. The result sets tier U's seam plan — one clock, one process runner, one environment reader, one client interface per external service, each with a fake — and a count ratchet over bare doors outside the seam packages that only shrinks; every door left bare is listed with its reason.

Choose the ten environment edge cases that bite this project and specify each as a fixture function — its name, what it builds, the package that owns the behaviour and the first test that will use it. Candidates for a tool installed on a host: missing or read-only home · symlinked config dir · case-folding filesystem · missing optional tool or an old version · no service manager · bare locale or terminal · paths with spaces and non-ASCII · stale artifacts from a crash. For a service: a dependency down or slow at start · a webhook delivered twice or out of order · clock skew and a timezone or DST boundary · a half-applied migration · a rate limit or 5xx from a third party mid-flow · a suspended tenant or one with no plan · a payload at the size limit · a job retried after a partial write. For both: expired or absent credential · two concurrent writers. A fixture that cannot bite in some environment (a read-only directory under root) skips by name there.

### 3. Research — two passes, in parallel with steps 1–2

- Neighbours: the 8–12 projects closest in shape (same runtime problem, same kind of installer, same protocol, same UI medium). For each read the CI configuration, the contributing and testing docs, and the regression post-mortems. Extract: tiers and what each fakes · whether the merge gate touches a live or paid dependency · OS matrix · serialisation of shared-resource suites · snapshot rules for user-visible output · flaky-test policy · release and canary channel.
- Literature: hermetic and ephemeral environments · flaky-test economics · test-size taxonomy and budgets · release-chain staging · known-gap ledgers that stay honest · the domain's own hard part (a terminal, payments, regulated data, an LLM in the loop) · auditing the suite itself (diff-based mutation testing, production probers).

Each pass returns a cited report ending in patterns to adopt, patterns to avoid, and open questions; a source it could not fetch is named unverified, never summarised from memory. Fold every adopted finding into the design as a decision with its source, in a Prior art section per tier. A question the sources cannot answer for this machine or this codebase (a parallelism sweet spot, a wall budget) is marked `measure here` and becomes a measurement task. Without a fetch tool the step reports `RESEARCH-NOT-RUN — no fetch tool`, § Seed evidence stands in, and every pattern adopted from it is marked unverified.

### 4. Shared state

List every store features meet in: tables, caches, queues, filesystem layout, daemons, per-tenant config, sessions · state a third party holds (a payment sandbox, an identity tenant, a bucket). For each: its writers, its readers, what can corrupt it.

### 5. Lanes

Cut lanes by actor journey, never by module — five to nine: one per primary role's core journey · one per integration or protocol surface · one for tenant or admin operations · one for onboarding and upgrade · one for operations and the destructive tail. When the count passes nine, fold the thinnest integration lane into the role journey that uses it — the map gate, not the lane count, guarantees coverage. Every destructive operation on user state (uninstall, delete, purge, a migration rollback) gets a beat for its refusal or no-op path — a foreign file kept, a second run changing nothing — beside its success path. Inside a lane go depth-first, carrying state forward: the record created in beat 2 is the one edited in beat 5 and exported in beat 9. Order the lanes by law 5 and write each position's reason. Give every lane its `need` prelude.

`beats.md`, one line per beat:

```
<lane>.<nn>-<slug> · do:<the action> · assert:<the literal result, and the report, store or screen it is read from> · spends:<quota, paid calls, licensed accounts> · <landscape ids>
```

Lane ids are uppercase letters and digits (`U1`, `PAY`).

### 6. Crossings

For each store from step 4, every writer–reader pair living in different lanes is a candidate; keep those where a regression would otherwise hide in one lane's blind spot. Add the after-effect beats of law 6 in the form: after `<lane X's heaviest beat>`, `<what lane W built>` still `<does its job>`. Probe list: one entity touched by two roles · an operation landing while another actor is mid-flow · an expired or absent credential seen at two surfaces (absence versus error) · a config change under live sessions · two writers under two load shapes · a dependency restart with work in flight · a delete while something still references the target. Record each as `crossing · lanes · why twice` and map its landscape ids to both beats.

### 7. Map gate

`map.tsv` — tab-separated, header `landscape_id⇥lane⇥beat` (⇥ = tab), the beat column holding the full beat id — covers every id. An id no live lane can reach maps to lane `A` or `U` with the covering test's name as its beat, and is listed under Not covered with its reason. The gate fails on an unmapped id, on a mapped beat that neither a lane file nor `pending.txt` defines, and on any route, command, job or tool name the built system reports that no landscape `name:` field carries (derived by dumping the router, the help tree, the job registry, the tool list). Beats of lanes still to be written sit in `pending.txt`, one beat id per line, which shrinks to empty by close. A new capability lands with its landscape row, its map row and its beat in one commit.

### 8. Activity log

Design the log Law 11 requires. Destination: one JSON-lines stream per environment root, resolved through the path façade so a test, a lane run and production never share one; a product of several processes writes one record shape to one destination the harness can read — a shared file, or the aggregator queried by a run id the harness injects. Record: `ts level msg op pid version`, the ids of the entities in play, `dur_ms`, `err`. Coverage: every entry point (argument shape, exit or status, duration), every door's seam (the real runner, client and clock wrap once — a few choke points, never hundreds of call sites), every state-machine transition, every destructive decision with its reason. Existing log calls move onto it, and a count ratchet holds direct prints and ad-hoc loggers outside the log module. Tier U gets a buffer handler and asserts on records. Name the level per environment, the rotation rule, the redaction test, and the reader (a filter by time, level, entity and operation).

### 9. Harness contract

The harness is written in whatever drives the real system best — the project's test runner with a browser or HTTP driver for a service, shell for a CLI; the verbs, line formats and exit codes below are the contract in any language.

- Beat library, one implementation sourced by every lane, demo and smoke script: `beat <id> <landscape-ids…>` · `pass` · `fail <why>` · `known <gap-id>` · `blocked <by>` · `need <name> <check> <make>` · `spends <resource>` · `expect-log <pattern>`. Around each beat: record the log position before (a byte offset for one file; the beat id injected into every request or process environment where several processes write), scan the beat's slice after; an `error` record no `expect-log` declared fails the beat, and a missing log fails it as `LOG-ABSENT`.
- Runner: `run [--lanes A,B] [--root reuse|rebuild] [--dry-run]` (`--dry-run` prints the lanes and beats in run order, exits 0) — one environment, canonical lane order whatever order was given; streams `✓` · `✗` · `known` · `blocked-by <beat>` lines with the lane prefix; a lane runs to its end after a red beat; writes per-lane logs, `timeline.tsv` (`lane · beat · t+s`), `summary.md`, and a `lane · wall_s · beats · failed · known · blocked` table; the header names the mode (`solo` or `sequence`) and the lanes that ran before; exit 1 on a failure outside the known-gap list, an unmapped id, or a budget breach, each named.
- Root: built once, snapshotted (container image, database template, VM snapshot), keyed by a hash of everything that shapes it — source, migrations, seeds, templates; an equal hash reuses the snapshot. A root holding secrets is never pushed to a shared registry.
- Run isolation: its own home and config root, database or schema, ports; third-party state is stamped with the run id and deleted by that id in the final lane; credentials are injected at run time from the operator's keychain or a secret store scoped to the tier-B job — never committed, never present in a commit-gate job.
- Known-gap file: one entry per beat — `beat · landscape_id · why (the upstream defect or absent hardware, linked) · owner · expires (ISO date) · when (optional platform condition)`. A listed beat reports `known` and counts apart; Law 8's three red states are the runner's.
- Self-tests for the harness, each failure path watched failing before its code exists.

### 10. Owner review

Present the research findings and the draft design one plain line each, with the decision each implies. The owner rules; every ruling moves from Open rulings into the law or section it changes. Rulings that usually surface: real dependency versus mock per tier · shared versus isolated state · known-gap policy · logging framework (prefer the language's standard structured logger) · which LLM and reasoning setting run the release rehearsal · which numbers are measured here · which gate runs the sequence (every close, or release only). In a run with no owner present, every item stays under Open rulings, recommendation first, and the document is marked `DRAFT — unruled`.

## Output — the design document

Written to the path the request names; absent one, `<the project's design-doc directory, else docs/design>/<target>-integration-suite/design.md`, stated in the reply. `landscape.md`, `beats.md`, `map.tsv`, `pending.txt` and an empty known-gap file are written beside it — the skeletons the build moves into the lanes directory; this command edits no code and no test. Sections, in this order:

1. Tiers — what each asserts, what each fakes, which gate it guards, which platforms it runs on
2. Landscape summary — counts per area, the `NONE` count; `landscape.md` beside the document
3. Doors and seams — the seam plan, the ten fixtures, the bare-door ratchet baseline
4. Prior art — adopted, avoided, `measure here`, each with its source
5. Shared state — stores, writers, readers
6. Lanes — order with reasons, `beats.md`, the `need` preludes
7. Crossings — the table and the after-effect beats
8. Map gate — `map.tsv`, the derivation commands, `pending.txt`
9. Harness contract — library, runner, root hash inputs, budgets, known-gap file shape
10. Activity log — destinations, levels per environment, record fields, the redaction test
11. Build order — run isolation (step 9) · wall-time budgets for tiers U and A (the Suite wall time class of /quality:llm-codebase) · seams, fakes and fixtures, then the packages converted in batches with disjoint file sets · activity log · mocks · harness with the first lane solo, then in sequence · remaining lanes with their after-effect beats · known gaps closed · a one-page runbook: run one lane, run the sequence, read a red row from the timeline, add a beat · close: three green sequences, budgets pinned, the sequence wired into the release procedure
12. Not covered — every surface no tier reaches (a platform with no live tier, a flow needing absent hardware), named with its reason
13. Open rulings — every number or trade-off the owner decides

## Hand-off to the build and test hands

The design document is reviewed before anything is built: unmapped capabilities, crossings asserted from one side only, checks whose broken state would read as PASS. In a project installed from this blueprint, `flights-speccer`'s reconcile phase performs that review, the build goes through `/flights:spec`, and the suite is built and kept by the flights agents:

- a flight executor builds the harness, the mocks and the lanes from the flight's task files of Build order.
- `flights-lander` runs them at the landing: the solo lanes owning the touched area while it fixes, the sequence at the gate's open and close; a defect a lane exposes is its to fix.
- The project's testing manual, § Lanes and registries, states the duty this design leaves on every change: the landscape row, the map row and the beat in the same commit.
- `docs/commands/build/references/qa-commons.md` §§ Test validity, Run verdicts, Integration lanes carry the rules both hands share with this command.

Elsewhere, the owner assigns the review, the build and the runs; Law 4 is the run policy (a solo lane while working, the sequence at close and release) and Laws 7–9 are the verdict rules.

## Seed evidence

Published findings to start step 3 from; each is re-fetched before it is cited.

- A tool that writes into a user's home (chezmoi's testing guide) tests in four tiers — unit, in-memory filesystem, the real binary under a redirected home, an operating-system sweep; host-edge fixtures buy the last tier's coverage at unit speed.
- A gate that only builds (a cross-compile, a type check) is green whether or not the feature works; a suite that drives the real binary keeps the raw bytes of a failed run.
- Luo et al., An Empirical Analysis of Flaky Tests (FSE 2014): async wait 45 %, concurrency 20 %, order dependency 12 %; 24 % of flaky-test fixes changed the code under test, nearly all real defects.
- pytest `xfail(strict=True)` is the one known-gap mechanism that fails the build when a listed gap passes; a ledger requiring only a bug link (Chromium TestExpectations) accumulates stale rows.
- Software Engineering at Google, ch. 23: presubmit (fast, hermetic) → postsubmit → release candidate at rising fidelity → production probers, which also audit whether the tests still matter.
- Anthropic's Claude Code quality post-mortem: three regressions shipped together because user reports were indistinguishable from normal variation — gates emit named, deterministic failures, and a number is trusted after a soak.
- Where a protocol has a public conformance suite, an independent implementation judges better than self-written assertions — validate it before adopting.
- No source quantifies a parallelism sweet spot or a suite-time ratchet; both are measured on the project's own machine, never on a machine running other builds.

## Checks

```sh
set -uo pipefail; export LC_ALL=C
L=<lanes dir>; D=<landscape.md>; MODE=${1:-check}; rc=0
say() { printf 'CHECK %-10s %-5s %s\n' "$1" "$2" "$3"; case $2 in FAIL) [ $rc -ge 1 ] || rc=1;; ERROR) rc=2;; esac; return 0; }
verdict() { if [ -z "$2" ]; then say "$1" PASS "0 $3"; else say "$1" FAIL "$3: $(echo $2)"; fi; }
ids=$(grep -oE '^[A-Z]+[0-9]+' "$D" | sort -u);        mapped=$(tail -n +2 "$L/map.tsv" | cut -f1 | sort -u)
beats=$(tail -n +2 "$L/map.tsv" | cut -f3 | sort -u);  defined=$(grep -ohE '[A-Z][A-Z0-9]*\.[0-9]+-[a-z0-9-]+' <lane files> "$L/pending.txt" | sort -u)
names=$(<dump routes|help tree|jobs|tools> | sort -u); known=$(grep -oE 'name:[^ ]+' "$D" | cut -d: -f2- | grep -v '^—$' | sort -u)
if [ -z "$ids" ] || [ -z "$mapped" ]; then say map-ids ERROR "DERIVE-FAILED — no ids read from $D or $L/map.tsv"; else verdict map-ids "$(comm -23 <(echo "$ids") <(echo "$mapped"))" "unmapped ids"; fi
if [ -z "$beats" ] || [ -z "$defined" ]; then say map-beats ERROR "DERIVE-FAILED — no beats read from map.tsv or the lane files"; else verdict map-beats "$(comm -23 <(echo "$beats") <(echo "$defined"))" "mapped beats defined nowhere"; fi
if [ -z "$names" ] || [ -z "$known" ]; then say map-names ERROR "DERIVE-FAILED — the registry dump or the landscape name: fields came back empty"; else verdict map-names "$(comm -23 <(echo "$names") <(echo "$known"))" "capabilities the landscape lacks"; fi
if [ ! -r "$L/<known-gap file>" ]; then say gaps ERROR "<known-gap file> unreadable"
else verdict gaps "$(awk -v t="$(date +%F)" '/^ *- beat:/{if(n&&!e)bad=bad" "b"(no-expiry)"; b=$3; n++; e=0} /^ *expires:/{e=1; if($2<t)bad=bad" "b"(expired)"} END{if(n&&!e)bad=bad" "b"(no-expiry)"; print bad}' "$L/<known-gap file>")" "known-gap entries expired or without expiry"; fi
if [ ! -f "$L/pending.txt" ]; then say pending ERROR "$L/pending.txt missing"
elif [ "$MODE" = --close ] && [ -s "$L/pending.txt" ]; then say pending FAIL "$(grep -c . "$L/pending.txt") beats still pending at close"
else say pending PASS "$(grep -c . "$L/pending.txt") beats pending"; fi
# bare doors: the count ratchet of /quality:llm-codebase § Checks over '<bare door spellings>' outside '<seam packages>'
exit $rc
```

Every check follows the three-state protocol of /quality:llm-codebase § Protocol. The `gaps` expression reads a YAML list of `- beat:` entries with an `expires: YYYY-MM-DD` key; another file format swaps that one expression. Adapt the id pattern, the derivation commands and the door spellings to the tree; a check that cannot be expressed as a script goes under Open rulings.
