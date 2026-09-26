---
name: quality:integration-suite
description: Designs every test tier — its validity law, and the live tier's lanes over shared state; `/quality:integration-suite <project|subsystem>`, "design our test suite", "refactor the e2e suite", before building any test harness. Returns a design document with its ledgers and checks script; edits no code. Source-tree layout → /quality:llm-codebase.
argument-hint: <project|subsystem>
---

# Integration Suite

One procedure for any stack: a CLI, a web service, a UI, a daemon, a library with a unit tier only, a product with LLM-driven steps. Each rule below is one executable line; its reasoning, the failure it prevents and its evidence live in the design, read from disk at each decision point:

```bash
DESIGN="$(cd "$(dirname "$(readlink -f "$HOME/.claude/commands/quality/integration-suite.md")")/../../../../docs/design/integration-suite" && pwd)"
```

Run it first. When it fails, or `$DESIGN` lacks `integration-suite.md`, `laws.md` or `validity.md`, stop and report `DESIGN-UNREACHABLE <the directory the cd tried>`; never proceed from memory. A pointer `$DESIGN/<file> § <heading>` is an order: read that section before the decision it governs. Law N's section is `$DESIGN/laws.md § Law N` for 1–18 and `$DESIGN/validity.md § Law N` for 19–31 (the heading `Law N — …`); read it wherever a step names Law N, and before any judgement its line leaves open. Where this command and a section differ, the section governs and the reply names the difference. Before step 1, read `$DESIGN/integration-suite.md § The six ideas` and `$DESIGN/integration-suite.md § The shape`.

Sections: Inputs · Terms · Names and ids · Laws · Refused shapes · Files · Procedure · Output and build order · Checks · Where enforcement ends · Hand-off.

## Inputs

Read `$DESIGN/integration-suite.md § Inputs`.

- target: a project's test suite, or one subsystem of it (the same procedure over a narrower landscape). A project with no live tier (a library, a tool with no shared state, an owner who asks for none) runs every step; the live-tier parts answer `none — <reason>`.
- live tree: the product's entry-point registries, build configuration, settings schema (none: the key set the environment-reader seam's call sites name) and existing tests, read from code, never recalled.
- orientation file: the project's test tiers, isolation rules and what a test run may never touch; absent, each of the three is an Open ruling.
- ratified references: facts, specs, contracts and rulings, indexed in `references.tsv`; none, and every ruled row's source is an Open ruling.
- own identifiers: repository names, issue-tracker hosts and internal domains, listed in the checks script for `gaps`.
- owner: the step 12 rulings, and the review of the owner-owned paths (Where enforcement ends).

## Terms

Read `$DESIGN/integration-suite.md § Terms, and why each exists` before applying a term to an unclear case.

- unit tier: every door behind a seam, parallel, seconds; hermetic tier: the real binary or server as a separate process against scripted mocks, no network beyond loopback; live tier: the real system against its real dependencies.
- door: a contact with the outside world, as the door spelling set spells it; seam: the one interface in front of a door, with a fake. The project's own data layer (database, cache, queue broker, run locally) is part of the system at the hermetic and live tiers; the unit tier reaches it only in the packages that own persistence.
- landscape: the closed list of product capabilities in `landscape.md`, the coverage denominator.
- lane: one actor pursuing one kind of goal, depth-first; its files match the lane-file pattern the checks script declares.
- beat: one action plus the assertion of its result, the unit of verdict (✓, ✗, `known`, `blocked`, `skip-because`); a beat with several arms is several beats.
- mapped row: a beat, or a unit or hermetic test the map names (`tier-unit:<test>`, `tier-hermetic:<test>`); every mapped row has a `beats.md` row.
- crossing: shared state two lanes touch from different sides.
- root: the installed, configured, seeded environment every run starts from, snapshotted and keyed by a hash of its inputs; checkpoint: the environment after one lane of a sequence run, snapshotted and keyed like the root.
- carry: the ids and credentials a beat created or was handed.
- run ledger: the runner's record of one run (step 11); runs store: `runs/` beside the ledgers for a local run (`hash`-tagged, landed with the change), the CI artifact store keyed by subject for a CI job. A run a check reads after its landing (one a ledger file names, a lane's latest green `solo`) lives under `runs/`; any other landed ledger may be pruned.
- coverage context: the product lines executed under one test's id, as the coverage tool attributes them per test.
- canonical ledger: the ledger of a full sequence run (`mode: sequence`, every lane) on the merge subject; with no live tier, the blocking commands' ledgers on the merge subject.
- landing: one change's merge into the main branch, identified by its merge subject.
- hash subject: every tracked path minus the `hash`-tagged globs of `exclusions.txt`; merge subject: the version-control system's staged-tree hash over the hash subject for the change being merged; dirty hash: the tracked diff plus every untracked, non-ignored file under the subject.
- signature: the enclosing symbol plus the normalised statement, independent of the file; every exemption anchor is a signature, every ratchet baseline a multiset of them.
- path façade: the one product module that resolves every environment path (home, configuration, data, log) from the environment root.
- spelling set: one file per language under `scripts/suite-checks/` listing the call and pattern spellings one check searches for over its scope: the assertion, door, refused-shape, refused-name, violating-action, exclusion and locator sets. Locator position is an argument the locator set names (a selector, a key, an id), never the value asserted.
- rendered-from set: the non-code assets the build loads (message and translation files, templates).
- bite: the product change that turns a test red, named in `breaks-if:`, watched once, recorded in `bites.tsv`; source: the independent origin of an expected value.
- fakes directory: the one test-tree directory, named in the seam plan, holding every fake.
- schedule window: the longest period between two runs of the scheduled leg, and between two close runs, declared in the Tiers section and counted from the landing that installed the suite (the first landing whose tree holds `scripts/suite-checks.sh`); no check fails on it before one window has passed.

## Names and ids

Read `$DESIGN/integration-suite.md § Names and ids` before naming anything.

Every id is kebab-case domain words, the same string on every surface (file stem, runner argument, ledger column, log field, map cell), with no single letter, prime, date, bug id, wave or team name. A rename is one mapping applied everywhere, accepted when the old name greps empty repo-wide and one run is green.

| Id | Pattern | Example |
| --- | --- | --- |
| landscape id | `<area>.<capability>`, at line start | `invoice.export-pdf` |
| lane key | `<domain-words>`, three characters or more, never starting `tier-` | `billing-cycle` |
| beat id | `<lane>/<slug>`, the slug stating the action | `billing-cycle/export-invoice` |
| tier row | `tier-unit:<test name>` or `tier-hermetic:<test name>` | `tier-unit:cursor-rejects-foreign-sort` |
| test file stem, title | domain words clear of the refused-name spellings | `cursor-paging.test`, "a foreign-sort cursor is refused" |
| run id | `<lane or sequence>-<subject short hash>-<start time>` | assigned by the runner; `names` does not read it |

The refused-name spellings, which `names` fails: a bug id, a date, a wave or team word, `line N`, a code segment of one or two letters plus digits outside `name-allowlist.txt` (`s3`, `v2`), a numeric segment in an id, a key or a stem, and in a title an ordinal (`#N`, a leading `N.`, `case N`, `scenario N`). Beats carry no ordinal: the lane file's order is the run order. A property word in a slug or title (durable, recovers, idempotent, exclusive, retries) needs a violating action in the test body: restart, kill, replay, concurrent, expire, revoke, repeat, or a scripted fault (fail, error, drop, disconnect, timeout, 5xx).

## Laws

Each line is the rule; the section it ends with holds why.

### Where each tier gates

1. The unit and hermetic tiers gate every commit; the live tier gates each release and the close of each multi-commit piece of work. The commit gate holds no credential, calls no paid or live dependency, and runs with outbound network denied where the platform allows. `$DESIGN/laws.md § Law 1`
2. Each external dependency family gets one scripted mock speaking the real protocol from a scenario file, its shapes goldens captured from the real dependency during the live tier (a differing capture is a red beat); an unscripted request fails the test by name. Every internal fake (a store, a queue, a registry, a renderer) lives in the fakes directory and passes the real implementation's contract cases, run against both, producing every state a test needs and none the real one cannot. No test replaces a product module in place with an inline stand-in. `$DESIGN/laws.md § Law 2`
3. A live beat asserts only what the real dependency alone shows: each product-side assertion in it has a twin at the lowest tier that can observe it (`twin:<row>`), or the row is `live-only`. A hermetic row exists only for what a unit test cannot observe: a process boundary, real input and output, a boot path, the assembled artifact. Above the unit tier, one success and one refusal row per operation besides `crossing:` rows; input variations are table-driven unit cases. Every hermetic and live row states `live-because:`. `$DESIGN/laws.md § Law 3`
4. The hermetic and live tiers boot the production entry point from the build output as a separate process, with production's module resolution and configuration path (`boot: process`). An in-process boot (`boot: in-process`) is admitted only with a self-test asserting single instances of every stateful library the product loads and a behaviour probe identical in both modes, both passing on the merge subject. `$DESIGN/laws.md § Law 4`
5. Where the product has LLM-driven steps, the release rehearsal runs at release on a weaker, non-frontier LLM at its highest reasoning setting and passes on the asserted end state, never the model's claim; its ledger's `command:` names the model and the setting, an Open ruling until the owner rules both. `$DESIGN/laws.md § Law 5`

### The shape of the live suite

6. The live tier runs one environment per run, lanes in sequence, each inheriting the state earlier lanes built; concurrency is a scripted beat (a storm, two writers), never a scheduling mode. A parallel mode exists only when the design declares it, and then admits only lane files whose crossings share no store, derives from the lane key a per-child namespace for every shared resource (database, schema, queues, captured mail, carry, logs, scratch names, identities, addresses, environment keys), carries a collision self-test (two children, identical inputs, zero cross-reads), asserts no delta across children on a store without a correlation column, and labels its ledgers `mode: parallel:<n>`, never canonical. `$DESIGN/laws.md § Law 6`
7. Every lane opens with a `need` prelude that creates its preconditions when absent and is a no-op when they exist. A sequence run checkpoints after each lane (step 11), and a lane's working loop restores its checkpoint. A lane is done when green from its checkpoint, green alone from a fresh root and green in the next full sequence; it joins the sequence the day it is written, and a solo run is owed again only when its files change. The runner keeps going: after a red beat the rest of that lane reports `blocked-by`, and the next lane's prelude runs in create mode and reports that lane's own first fault. `$DESIGN/laws.md § Law 7`
8. Lanes run state builders, then readers over the richest state, then destroyers; a lane with a destructive tail (uninstall, offboarding, purge) splits into an early half and a final half. The lane table gives each position its reason; a lane without one is an Open ruling. `$DESIGN/laws.md § Law 8`
9. Two lanes assert every crossing. Every heavy beat (one that loads, restarts, migrates or bulk-mutates shared state) is followed by an after-effect beat: after `<lane X's heaviest beat>`, `<what lane W built>` still `<does its job>`. A probe becomes a beat only when the landscape carries the capability it probes, and a landscape row exists only when the built product reports its operation or its `evidence:` names the code that writes the output. `$DESIGN/laws.md § Law 9`
10. Every read of a store another test writes carries a predicate on an id the test created or carries (no count, first row, "the only" row or unchanged-table assertion over such a store), at every tier whose tests share a store. Shared identities (an administrative account, a tenant, a default role) are provisioned once by the root build and read from the carry, never asserted as the test's own. `$DESIGN/laws.md § Law 10`

### What a verdict means

11. A beat passes on its asserted result plus a clean activity log; an expected error is declared per beat (`expect-log <pattern>`). Assertions read the system's own reports, stored state and rendered output, each naming the landscape id it reads; a beat with no assertion on a mapped id cannot pass for that id. No expected value comes from an LLM's own claim; a beat that fails on LLM variance alone is asserting the wrong thing. `$DESIGN/laws.md § Law 11`
12. The expected side holds a literal, a fixture or golden path, a registry key, or a name the test bound from a fixture, a seed or the carry; never a name or call resolving to a product module (a product type's constructor over literals alone is a literal) or a value read from the rendered-from set; a product name appears only in locator position. Every mapped row names `source:`; a ruled row's is a `references.tsv` row, and when test and product disagree the product is wrong: production under a ruled row changes only with a cited reference and an owner ruling in the same commit. Assert the specific value that differs between correct and broken behaviour (the code, the actor, the destination, the persisted row), never existence, truthiness, a literal on the actual side, "any value", a length alone, a disjunction of accepted values, an exception's class without its code, or a status or exit code as the row's only assertion; an assertion that cannot read such a value carries `weak-oracle:<class>` with a class from `unobservable.txt` (any other class is ratcheted by signature), and never on a ruled row. A count derives from what the test seeded. Two product artifacts that must agree (a store's schema and the code's definitions, two mirrors of one registry) make a `source:drift:<artifact>` row with a bite on each side. LLM output is asserted by its parsed structure, its recorded response replayed at the hermetic tier, and the values of its deterministic post-processing. `$DESIGN/laws.md § Law 12`
13. Every mapped row, project-authored lint rule, rule in `third-party-rules.txt`, check and refused-shape spelling set carries a bite: `breaks-if:` names the product change, and the build applies it once, watches the red and records it in `bites.tsv`. The mutation is one statement inside the guarded code (no inserted unconditional exit or throw, no more than one statement removed), and for a mapped row it lies in the capability's file or the op's handler. Every other test is watched failing when written. A guard with an allow and a deny outcome is tested in both directions, a bound at the boundary and one step either side; negative fixtures differ from positive ones only in the guarded property; a name states a property only when the body creates the condition that would violate it. `$DESIGN/laws.md § Law 13`
14. A capability is covered only when the canonical run observed a mapped row invoking it under the row's own injected id and asserting on it. The runner writes per row the operations recorded with that id (the log's `op` and `beat`), the goldens compared and the assertions per landscape id, and the close gate reads that ledger, never the map. A tier row is proven through its tier's ledger (the buffer handler's records or the coverage context reaching the capability's file at unit, the log slice at hermetic), and its test is collected by the blocking command, passed on the merge subject and bitten inside the capability's file or the op's handler. A time-driven job is covered by a beat triggering it through an operator entry point carrying the id, or by a unit row. `$DESIGN/laws.md § Law 14`
15. Every check, scanner, lint rule, registry reader, output filter, runner and CI step has three outcomes (pass, fail, could-not-look), and could-not-look exits non-zero under its own name: `blocked-by <beat>` behind a failed precondition, `DERIVE-FAILED` for an enumerator that could not run, `LOG-ABSENT` for a beat whose log slice is missing, `ERROR` for an unreachable dependency, a failed run for a killed or crashed one, `PASS none:<section>` or `ERROR NONE-AMBIGUOUS` (Output and build order), and `PASS not-due` between scheduled legs, `ERROR` once the last leg is older than its window. The one skip is `skip-because:<reason>` with a reason from `skip-reasons.txt`, for a condition the environment makes physically unobservable, reported by name and counted apart from passes; an unreachable dependency, a missing credential, a slow or a flaky test is an `ERROR` or a defect. Every check, scanner, filter, project-authored lint rule, the runner and the beat library ship one self-test per could-not-look input (file absent, empty, unparseable, process killed, zero items scanned, a node the rule cannot read) expecting the error, and one planted-violation self-test expecting the failure, both watched before the code exists. A failed beat stores raw evidence: the exact response or screen bytes and the log slice. `$DESIGN/laws.md § Law 15`
16. A probe reads, never consumes: a queue's attributes and stored state, never the message or the work item. Every observation goes through the beat library, scoped by beat id, entity id and log offset; the library reads a child process's output only after the child exited or flushed, and keys every reply by beat id. Every wait is a bounded poll on a named, observable product condition, never on the clock or a time-derived value; the harness never sleeps or waits a literal delay, fakes only the clock, never every timer, and takes a time-derived credential only from the clock-seam or tolerance-window helper. A lane file never re-implements a shared helper. `$DESIGN/laws.md § Law 16`
17. A product with a runtime writes one structured activity log per environment root, levelled per environment, test and pre-release defaulting to debug. Every record carries `op` and the ids of the entities in play; the harness injects the beat id and run id into every request and process environment it starts, and the product carries them through its queues and jobs. At the unit tier a buffer handler captures each test's records and the runner writes its `op` set to the ledger. A redaction test covers credentials, tokens, prompt bodies and personal or regulated data. Lane fixtures are synthetic. `$DESIGN/laws.md § Law 17`
18. Output whose bytes are the interface (an emitted command, a generated file, a rendered screen's structure) is pinned by goldens, changed in the output's commit. Meaning-bearing copy (legal, compliance, safety, an AI disclosure, an attribution, any owner-ruled wording) is pinned per locale by a literal in `ruled-copy.tsv`, in exactly one test per key and locale; the registry holds keys or the single line `none`. Ordinary copy is asserted structurally (the element, its key, its presence and position), never by wording. A golden is never generated from the rendered-from set nor written by the test comparing it: `golden` compares against the tracked blob, a snapshot store sits under the goldens directory with snapshot writing disabled in the blocking command, and a first capture is reviewed before it pins anything. `$DESIGN/laws.md § Law 18`

### Keeping it honest over time

19. `gaps.tsv` holds only what this repository cannot fix (an upstream defect or absent hardware), each entry with an external reference in `why` (an upstream issue link or `hardware:<identifier>`, never a repository file or an own identifier), an owner from `roster.txt` and an expiry. The healthy ledger is empty, and a listed row that passes is red. A fix flips its row to a real assertion and deletes the entry in the same commit. A `known` mark excuses one arm's verdict. Every exemption or allow list anchors on a signature, never a line number. `$DESIGN/validity.md § Law 19`
20. Before the build, the design states the target wall for the full sequence, each lane and each tier, the scheduling, and the per-change policy (the lanes touching the changed files from their checkpoints, then the touched lane alone; the full sequence at each close and at release); every beat and hermetic row carries `cost_ms:`. The runner prints a wall breakdown per row and per lane with the idle gaps between rows. Speed is measured against the target after the first two lanes, before a third is written. At close each lane, the sequence and each tier is pinned in `budgets.tsv` at the maximum of at least three named green canonical runs on one root hash (a tier key's on one subject) plus the headroom the Speed section declares, the sequence's pin within its target; a budget ratchets down only, save that a commit adding rows to a key may raise it by at most the added rows' own maximum `wall_ms` in the named runs. A per-row number is a hang guard and never fails a passing run. `$DESIGN/validity.md § Law 20`
21. A flaky test is a defect with an owner, never a quarantine: the runner records every flake (a row that failed and passed on one merge subject, dirty hash and root hash) in `flakes.tsv`, and close fails while the run ledgers show one. A retry or rerun setting covers only tests named in `flakes.tsv`, every attempt is its own ledger row, and flakiness is tracked per configuration. The unit and hermetic tiers run in shuffled order in one scheduled leg; a shuffle-only failure is a shared-state defect. The live tier is never shuffled. `$DESIGN/validity.md § Law 21`
22. Every run ledger records the subject hash and the dirty hash, both at start and at end; a gate or a merge accepts only a ledger the `tree` row passes; a bite's red run is verified against its own diff hash; a report older than the tree's latest change is not evidence. `$DESIGN/validity.md § Law 22`
23. The design names the exact blocking command per tier, and every ledger header records the `command:` that produced it. Every file matching a test-name pattern anywhere in the repository, outside the `scope` exclusions, is collected by the blocking run exactly once; the blocking run's log shows every `CHECK` line; every CI step running a check blocks. A lint rule's wiring is proven only by a bite whose red run's `command:` is the blocking command. `$DESIGN/validity.md § Law 23`
24. A test is deleted only in the commit that installs its verified `retired.tsv` rows, one per assertion, derived by the assertion spelling set from its text at the merge base. A disposition is `successor:<file:line | row id>` (existing, passed in the named run on the merge subject, with the same actual-side subject and the same expected literal or code), `dropped:<refused shape>` (matching that shape's spelling) or `ruling:<ref_id>` (in `references.tsv`). Kept tests get no row. The old suite is a list of claims to verify, never a source of fixtures or expected values. `$DESIGN/validity.md § Law 24`

### What a test may not be

25. The landscape holds only behaviour a user, an operator or an integration invokes on the running or installed product. Package-manager scripts that build, lint, type-check, format, test, generate or export, CI structure and step names, container build order, lockfiles, toolchain and dependency versions and the developer's own gates are excluded by rule; developer tooling runs as its own CI step. A configuration invariant that matters lives in one policy lint over the configuration files, each rule citing a `references.tsv` row, asserting a property (a key present or absent, a pattern absent) rather than a line's text, and carrying a planted violation. A version pin lives in the lockfile and a dependency policy. Operation names come from the built product's registries, never a package manifest, compared in both directions. `$DESIGN/validity.md § Law 25`
26. Beats and tests exercise the migration runner (ledger, checksum, ordering, a half-applied step, a rollback, the second run that changes nothing) with fabricated migrations in the scratch-migration directory only. The resulting schema is asserted by introspecting a freshly migrated store against the code's own definitions, as a `source:drift:` row. The upgrade row runs the previous release. A data backfill is proven once at authoring against a synthetic, production-shaped fixture and not kept; one needing production data is the owner's data-handling decision. No test names a shipped migration directory or file, parses migration text, migrates to a named version, seeds rows between two migration runs, or restores a store dump or snapshot outside the root build and the scratch-migration directory. `$DESIGN/validity.md § Law 26`
27. A test never opens a product-tree file (source, configuration, prompt, build output, or any text form of product code) as its subject; a behaviour test imports and runs the code. A source invariant is a lint rule with one planted violation per rule, run by the blocking command. An absence is asserted only by such a rule tagged security or privacy with a `references.tsv` row behind the tag; every other absence goes untested. A file the product wrote into the run root is output, not source, and a product whose output is configuration or prompt text pins that output under Law 18. Lint-rule fixtures and the planted corpora are `scope`-tagged. `$DESIGN/validity.md § Law 27`
28. A landscape id maps to at most one row per tier, except `crossing:` rows (Law 9's two sides, spanning two lanes or more) and a `twin:<row>` naming a row at another tier (Law 3). Every (row, id) pair in `beats.md` is a map row. Tests differing only in a literal are one table-driven test. `clones` runs a token-based clone detector over the test tree in this suite's own checks script, under the test tree's own baseline; test files are not exempt from it. `$DESIGN/validity.md § Law 28`
29. The suite assembles its configuration from the run root and yields the same result under any ambient configuration. In one scheduled leg, and on every commit whose diff against the merge base touches the settings or test-configuration paths the Tiers section declares, the unit tier runs twice, under the base configuration and under one derived from the settings schema (none: the environment-reader seam's key set) with every key perturbed to a valid alternative and every flag toggled, with identical results. A test file resolves no settings at import or collection time. Every runner builds the product's environment only from the run root and the `need` values; a run-root key set in the ambient configuration is an error. `$DESIGN/validity.md § Law 29`
30. Fakes, stubs and no-op implementations live only in the fakes directory. Production code never selects a double from a configuration value (a test flag, a test store name, a mock switch) and never substitutes a no-op for a missing configuration key: it refuses to boot. One hermetic boot with every required key blank asserts a non-zero exit whose refusal names every key; the per-key cases are unit tests over the configuration loader. Production source, contracts, schema comments and CI files never name a test file, a beat id or a test-tree path, except as an argument of a declared blocking command in a CI file. `$DESIGN/validity.md § Law 30`
31. Lane and test files obey the size ceiling the design declares (`size-ceiling`) at authoring, checked by `size` in this suite's own checks script under the test tree's own baseline and never warned; a lane file splits by journey segment. Fixture files carry no absolute host path, no captured process id and no personal or regulated data. A log-record shape lives only in the synthetic-log fixture directory, its records carrying the reserved run id `synthetic`; a captured run log is never a fixture. `$DESIGN/validity.md § Law 31`

## Refused shapes

Read `$DESIGN/validity.md § The refused shapes` before writing any refused-shape spelling set; its table holds what each set searches for. Each shape is one spelling set per language, written in step 3 with a planted-violation corpus of three syntactic forms per language, and ratcheted by `refused-shapes` over its scope as a signature-multiset baseline.

| Shape | Law | Scope |
| --- | --- | --- |
| Fail-open verdict | 15, 21 | checks, scanners, lint rules, filters, runner configurations, CI files |
| Weak oracle | 12 | every test tree |
| Tautological oracle | 12, 18 | every test tree |
| Unscoped read | 10 | every test tree of a tier that shares a store |
| Wall-clock wait | 16 | every test tree and the harness |
| Consuming probe | 16 | lane files and the harness |
| Toolchain as product | 25 | lane files, every test tree |
| Shipped migration content | 26 | every test tree outside the scratch-migration directory |
| Lint-as-test | 27 | every test tree |
| Tombstone | 27 | every test tree |
| Self-skip | 15, 29 | every test tree |
| Double in production | 30 | production source, every test tree |
| Test cited by production | 30 | production source, contracts, CI files |
| Second logging path | 17 | production source of a product with a runtime |
| Line-anchored exemption | 19 | exemption and allow lists |
| Machine in a fixture | 31 | fixture directories |

## Files

Every file is tab-separated (`⇥` is a tab) or one entry per line, and machine-read by the checks.

| File | Shape |
| --- | --- |
| `landscape.md` | one row per capability, step 1 |
| `retired.tsv` | `file⇥assertion_signature⇥disposition⇥run_id`, the disposition empty until the build fills it |
| `beats.md` | one row per mapped row, step 6 |
| `map.tsv` | header `landscape_id⇥row⇥reason`, step 8 |
| `pending.txt` | one mapped row id per line; empty at close |
| `references.tsv` | `ref_id⇥path⇥locator`, one row per ratified fact, spec line, contract line or ruling; the locator a heading, key or anchor inside `path` |
| `roster.txt` | one owner identifier per line |
| `skip-reasons.txt` | the owner's closed list of skip reasons, one per line |
| `unobservable.txt` | one class of value no test can know before the run per line (a generated id, a timestamp, an empty body) |
| `self-tests.txt` | `subject⇥case⇥test`: the subject a check, scanner, filter, lint rule, `runner`, `beat-library` or the component a law's self-test covers; the case an input class, `planted` or `law`; the test as the census names it |
| `exclusions.txt` | `glob⇥tag`: `hash` for paths the subject omits (the ledgers, the documentation), `scope` for paths no check collects or scans (the planted-violation corpora, lint-rule fixtures, the scratch-migration directory, the synthetic-log fixture directory, vendored code) |
| `ruled-copy.tsv` | `key⇥locale⇥literal⇥ruling`, the ruling a `references.tsv` ref_id; or the single line `none` |
| `gaps.tsv` | `row⇥landscape_id⇥why⇥owner⇥expires`, written empty |
| `flakes.tsv` | `row⇥tree⇥dirty⇥root⇥first_seen⇥owner⇥expires`, written empty |
| `bites.tsv` | `row⇥file_line⇥diff_hash⇥red_run_id`, the diff kept under `runs/` by its hash, two rows for a `drift:` row; written empty |
| `budgets.tsv` | `key⇥budget_ms⇥run_ids⇥load`, the key a lane, `tier-unit` or `tier-hermetic`; written empty |
| `runs/` | run ledgers, one file per run id (step 11), and the bite diffs |
| `scripts/suite-checks/` | the spelling sets as `<set>.<language>`, each refused shape's planted-violation corpus, the signature baselines as `<check>.baseline`, `third-party-rules.txt` (`rule⇥law`, one relied-on third-party rule per line), `name-allowlist.txt` (one admitted code segment per line) |

## Procedure

Steps 1–3 run in order with step 4 beside them, then steps 5–12 in order. Each step opens with its section; read it before starting the step.

### 1. Landscape

Read `$DESIGN/integration-suite.md § 1. Landscape` and `$DESIGN/validity.md § Law 25`.

Enumerate every capability closed-world into `landscape.md` from the built product's registries (router, CLI tree, job registry, tool list; for a library, its exported interface), never from docs. The surfaces: entry points a user, operator or integration invokes (routes, UI actions, a CLI product's commands, webhooks, scheduled jobs, queue consumers), integrations, lifecycle (install, the migration mechanism as one row, configuration, flags, upgrade from the previous release, uninstall), identity, money, consent, safety, the data lifecycle per core entity, operations, and the existing tests per tier.

- Run one collection pass per surface; each pass names what it could not read, and an empty pass is `DERIVE-FAILED` until its enumerator is proven to have run.
- A guard is two rows, `kind:success` and `kind:refusal`; the product's error-code registry, where one exists, lists the refusals.
- `op:` is the operation name the registry dump reports and the activity log records (for a library, the exported name). A capability that leaves no operation record (a rendered screen, a generated file) declares `evidence:<golden path>`, its `file:line` naming the code that writes the output. A time-driven job maps to a beat triggering it through an operator entry point carrying the injected id, or to a unit row.
- `surface:` joins the row's surfaces with `+`. A row whose surfaces include identity, money, consent, safety or data-lifecycle is `ruled:yes`; `ruled:no` on one carries `ruling:<ref_id>`; the owner marks others in step 12.
- `needs:` names what the environment must provide (a dependency, a credential, a platform); `today:` names the tiers testing it, or `NONE`. Report the `NONE` count as the baseline.

Row: `<id> · <what is asked for> · surface:<…> · kind:<success|refusal> · op:<name> | evidence:<golden path> · ruled:<yes|no|no ruling:<ref_id>> · needs:<…|none> · today:<unit|hermetic|live joined by +, or NONE> · <file:line>`

### 2. Existing tests

Read `$DESIGN/integration-suite.md § 2. Existing tests` and `$DESIGN/validity.md § Law 24`.

Write the assertion spelling set first, one file per language. For every existing test the design retires, write one `retired.tsv` row per assertion, derived by that set from the test's text at the merge base, never from file names or titles; the signature is the assertion's signature with its expected literal or code and its actual-side subject, the disposition empty. Tests the design keeps get no row; an assertion whose signature reappears in a file the same commit adds is a move. An assertion a refused shape covers takes `dropped:<shape>`.

### 3. Doors

Read `$DESIGN/integration-suite.md § 3. Doors` and `$DESIGN/laws.md § Law 2`; before writing any refused-shape set, `$DESIGN/validity.md § The refused shapes`.

- Write the door spelling set and trace every door with it. The seam plan gives each mechanism one seam (one clock, one process runner, one environment reader, one client interface per external service), each with a fake in the fakes directory (Laws 2, 30); the harness's own actors use the clock seam (Law 16).
- The doors left bare form the `bare-doors` signature-multiset baseline, each door with its reason, its header carrying the owner's burn-down ceiling (Checks).
- Write the refused-shape, refused-name, violating-action, exclusion and locator spelling sets in the same form, and each refused shape's planted-violation corpus; `scope`-tag the corpora, the lint-rule fixtures, the scratch-migration directory, the synthetic-log fixture directory and vendored code in `exclusions.txt`.
- Read the rendered-from set from the build configuration; with no build manifest, list its paths as an owner ruling.
- Choose up to ten environment edge-case fixtures that bite this project, from menus such as a host tool's read-only home, symlinked configuration directory or case-folding filesystem; a service's webhook delivered twice, clock skew, half-applied migration or suspended tenant; either's expired credential or two concurrent writers. Never choose one for a behaviour the product lacks (Law 9).
- A fixture for a condition the environment makes physically unobservable (a permission denial under a superuser, a case-folding check on a case-sensitive filesystem) carries `skip-because:<reason>` (Law 15).

### 4. Research

Read `$DESIGN/integration-suite.md § 4. Research` and `$DESIGN/integration-suite.md § Seed evidence`.

Run two cited passes in parallel with steps 1–3: the 8–12 nearest neighbour projects (their CI configurations, testing docs and regression post-mortems), and the literature, including diff-based mutation testing and production probers as audits of the suite itself. Every adopted finding becomes a decision with its source in the Prior art section, per tier. A source that could not be fetched is named unverified. A question the sources cannot answer for this machine or codebase (always the parallelism sweet spot and the suite-time ratchet) is marked `measure here`, a measurement task, never a gate on the machine's load. The seed evidence is `$DESIGN/laws.md § Sources`: re-fetch each finding before citing it. Without a fetch tool, report `RESEARCH-NOT-RUN — no fetch tool`, and mark every pattern adopted from the seed evidence unverified.

### 5. Shared state

Read `$DESIGN/integration-suite.md § 5. Shared state` and `$DESIGN/laws.md § Law 10`. With no live tier, section 6 reads `none — <reason>`; the store-sharing tiers still go in the Tiers section.

List every store features meet in (tables, caches, queues, filesystem layout, daemons, per-tenant configuration, sessions), with its writers, its readers, what can corrupt it, and its correlation column or `none` (Law 6). Third-party state (a payment sandbox, an identity tenant, a bucket) is on the list, stamped with the run id and cleaned by the run. The Tiers section names which tiers' tests share a store (Law 10).

### 6. Lanes

Read `$DESIGN/integration-suite.md § 6. Lanes`, `$DESIGN/laws.md § Law 7` and `$DESIGN/laws.md § Law 8` before cutting; `$DESIGN/laws.md § Law 12` before writing any `source:`, `$DESIGN/laws.md § Law 13` before any `breaks-if:`, `$DESIGN/laws.md § Law 3` before any `live-because:`, and `$DESIGN/laws.md § Law 18` before any golden or copy pin. With no live tier, section 7 reads `none — <reason>`, and `beats.md` still holds a row for every unit and hermetic mapped row.

- Cut lanes by actor journey: one per primary role's core journey, one per integration or protocol surface, one for tenant or admin operations, one for onboarding and upgrade, one for operations and the destructive tail. Five to nine lanes; past nine, the thinnest integration lane folds into the journey that uses it.
- Every destructive operation gets a refusal or no-op beat (the foreign file it must keep, the second run that changes nothing) beside its success path.
- Lanes go depth-first and carry state forward: one entity created, edited and exported across beats. Every read is scoped to the carry (Law 10).
- Each lane position carries its reason (Law 8); each lane has its `need` prelude (Law 7).
- Every mapped row has a `beats.md` row. A row whose bite cannot be named is cut; a row whose `live-because:` is another input is a unit case (Law 3).

Beat or hermetic row: `<beat id | tier-hermetic:<test>> · do:<the action> · assert:<the literal result and the report, store or screen it is read from> · source:<ref_id | drift:<artifact> | fixture | golden | literal> · breaks-if:<the product change> · live-because:<the observation> · cost_ms:<estimate> · spends:<quota, paid calls, licensed accounts | none> · <landscape ids>`

Unit row: `tier-unit:<test> · source:<…> · breaks-if:<the product change> · <landscape ids>`

### 7. Crossings

Read `$DESIGN/integration-suite.md § 7. Crossings` and `$DESIGN/laws.md § Law 9`. With no live tier, section 8 reads `none — <reason>`.

For each store of step 5, every writer–reader pair living in different lanes is a candidate; keep the pairs where a regression would otherwise hide in one lane's blind spot. The probe list: one entity touched by two roles; an operation landing while another actor is mid-flow; an expired or absent credential seen at two surfaces (absence versus error); a configuration change under live sessions; two writers under two load shapes; a dependency restart with work in flight; a delete while something still references the target. A probe becomes a beat only under Law 9. Record each as `crossing · lanes · why twice`; a crossing whose second side cannot be named is dropped. The after-effect beats of Law 9 come from this table, and each crossing's landscape ids map to both beats with `crossing:`.

### 8. Map and observed coverage

Read `$DESIGN/integration-suite.md § 8. Map and observed coverage` and `$DESIGN/laws.md § Law 14`.

Write `map.tsv`: every landscape id to a mapped row, the row column holding a beat id or a `tier-unit:` / `tier-hermetic:` row, the reason column empty, `crossing:`, `twin:<row>` or `live-only`, a `crossing:` live row carrying its `twin:<row>` or `live-only` after a space. List the not-yet-defined rows in `pending.txt`. Write into the design the derivation commands that dump the operation names from the built product (its router, job registry, tool list, a CLI product's help tree), never from a package manifest. The plan half (`map-ids`, `map-beats`, `map-names`, `map-dup`, `landscape-scope`) runs from the first commit; the proof half (`observed`) runs at close over the canonical ledger. The harness injects each row's id into every request and process environment it starts; the build makes the product carry it through its queues and jobs as a correlation field (this command edits no code); only records carrying that id credit the row. A capability lands with its landscape row, its map row and its mapped row in one commit.

### 9. Activity log

Read `$DESIGN/integration-suite.md § 9. Activity log` and `$DESIGN/laws.md § Law 17`. With no runtime (a library, a UI component suite), section 10 reads `none — <reason>`, and unit rows prove invocation by coverage context.

Design the log of Law 17: one JSON-lines stream per environment root, resolved through the path façade; a product of several processes writes one record shape to one destination the harness reads (a shared file, or an aggregator queried by the injected run id). The record: `ts level msg op pid version`, the ids of the entities in play, `dur_ms`, `err`, and the injected `beat` and `run`. Wrap the seams of step 3 (the real runner, client and clock, one wrapper each), not the call sites. The Second logging path ratchet holds direct prints and ad-hoc loggers. Name the buffer handler, the redaction test, the level per environment, the rotation rule and the reader (a filter by time, level, entity, operation and beat).

### 10. Speed

Read `$DESIGN/integration-suite.md § 10. Speed` and `$DESIGN/validity.md § Law 20`; before declaring a parallel mode, `$DESIGN/laws.md § Law 6`.

Price the suite on paper, writing each item Law 20 lists; `cost_ms:` covers actor provisioning, process boots, waits and third-party round trips, summed per lane and along the critical path. A declared parallel mode carries its isolation table (Law 6). A sequence whose paper cost exceeds its target is re-cut here, before a lane exists.

### 11. Harness contract

Read `$DESIGN/integration-suite.md § 11. Harness contract`. Write the harness in whatever drives the real system best; this contract holds in any language.

- Beat library, sourced by every lane, demo (a subset view) and smoke script, with the verbs `beat <id> <landscape-ids…>` · `assert <landscape-id> <what> <expected> <source>` · `golden <path under the reviewed goldens directory>` · `pass` · `fail <why>` · `known <landscape-id>` · `blocked <by>` · `skip-because <reason from skip-reasons.txt>` · `need <name> <check> <make>` · `spends <resource>` · `expect-log <pattern>` · `poll <name> <condition> <timeout>`. `golden` refuses bytes that differ from the tracked blob; a snapshot matcher is a golden (Law 18). The library is the only log reader: it records the log position before each beat and scans the slice after it (Law 16).
- Runner: `run [--lanes …] [--from checkpoint:<lane>] [--root reuse|rebuild] [--dry-run]`: one environment, canonical lane order whatever order is given, keep-going (Law 7), streamed lines `✓` · `✗` · `known` · `blocked-by <beat>` · `skip-because <reason>` with the lane prefix; `--dry-run` prints the lanes and beats in run order and exits 0. A concurrency option, where the design declares a parallel mode, labels the ledger `mode: parallel:<n>`. Exit 1 on a failure outside `gaps.tsv`, an unmapped id or a budget breach, each named; exit 2 when the runner could not run.
- Run ledger, one file per run id in the runs store: `key: value` header lines, then tab-separated rows. The header: `command:` (the exact invocation), `mode: sequence | checkpoint:<lane> | solo | parallel:<n> | tier`, the lanes that ran before, `boot: process | in-process | none`, `runner_pid`, `order: canonical | shuffled:<seed>`, `config: base | perturbed`, the root hash, the `tree` and `dirty` hashes at start and at end, the start time and the load (the load average and core count at start). The rows: one per mapped row and attempt, `row⇥verdict⇥assertions-by-id⇥expected⇥ops⇥goldens⇥wall_ms⇥t+s` (`expected` holding the row's expected literals), and per lane `lane⇥wall_s⇥beats⇥failed⇥known⇥blocked⇥skipped`.
- Every tier's runner (through its reporter or plugin interface), the lint step and the checks script write the same header, with `mode: tier` below the live tier whatever the worker count and `boot: none` outside the hermetic and live tiers. Unit and hermetic rows are per test (the ops from the buffer handler's records and the coverage context at unit, from the test's log slice at hermetic); the lint step's and the script's rows are per rule, check or refused shape.
- Failure block, before the verdict line, one per failed row: the row id, the file and line, the assertion's expected and actual values with its source, the first error record in the row's window, and the absolute paths of the run directory, the ledger and the log. An output filter takes its verdict from the exit code and the ledger, never from a keyword or a tail; its fixture tests (a green run, a red run, a long red run, a killed run) assert that the failure block's fields survive byte-for-byte.
- Checkpoints: after each lane of a sequence run, snapshot the environment (a container image, a database template, a VM snapshot) keyed by the root hash and the lane files up to that lane; `--from checkpoint:<lane>` restores it. Third-party state is not in a checkpoint; the prelude recreates it. The root is snapshotted the same way, keyed by a hash of everything that shapes it (source, migrations, seeds, templates); an equal hash reuses it, and a root holding secrets is never pushed to a shared registry.
- Isolation: each run gets its own home, configuration root, database or schema, and ports, derived from the run root (Law 29). Third-party state is stamped with the run id, so the final lane deletes exactly what the run created.
- Credentials are injected at run time from a keychain or secret store scoped to the live-tier job, never committed and never present in a commit-gate job (Law 1).
- Self-tests, each watched failing before its code exists and listed in `self-tests.txt`: the commit-gate job refusing an outbound socket (Law 1); each mock failing an unscripted request by name, and each fake's contract suite run against fake and real (Law 2); the single-instance and two-mode tests of an in-process boot (Law 4); the collision test of a parallel mode or of a store-sharing tier run concurrently (Law 6); the keep-going run, a planted red in the first lane with the second lane's own verdicts in the ledger and exit 1 (Law 7); the redaction test (Law 17); the all-keys-blank boot (Law 30); the beat library refusing a live child's output and a mismatched reply (Law 16); the failure block of a planted red carrying `file:line`, expected, actual and absolute paths; for every check, scanner, filter and project-authored lint rule, one could-not-look self-test per input class and one planted-violation self-test (Law 15).

### 12. Owner review

Read `$DESIGN/integration-suite.md § 12. Owner review`.

The owner rules: a real dependency versus a mock, per tier; shared versus isolated state, and whether a parallel mode exists; the known-gap policy and the owner roster; the ruled ids, the ruled-copy keys (or `none`), the skip reasons and the unobservable-value classes; the ratified references in `references.tsv`, and the rendered-from paths where the build has no manifest; the burn-down pace of every ratchet baseline; the logging framework; which LLM and which reasoning setting run the release rehearsal; the speed targets and which numbers get measured here; which gate runs the sequence. Present each research finding and draft decision in one plain line; each ruling moves from Open rulings into the law or section it changes. With no owner present, every item stays under Open rulings, recommendation first, and the document is marked `DRAFT — unruled`.

## Output and build order

Read `$DESIGN/integration-suite.md § The output` before writing a section.

Write the design document to the path the request names, else `<the project's design-doc directory, else docs/design>/<target>-integration-suite/design.md`, and state the path in the reply. Beside it write every file of Files except the checks directory; at the target project's root write `scripts/suite-checks.sh` (Checks) and `scripts/suite-checks/`. Edit no product code and no test. Each section is a `## <n>. <name>` heading. Each value a check reads sits on its own line as `<name>: <value>` in its section, numbers as integers: in Tiers, the blocking command and boot mode per tier, the store-sharing tiers, the double-run trigger paths, the schedule window and where each blocking run's log is kept; in Doors and seams, the fakes directory, the scratch-migration directory and the synthetic-log fixture directory; in Speed, the targets and the budget headroom; in Harness contract, the goldens directory, `size-ceiling` and the clone detector's threshold.

The fifteen sections, in order:

1. Tiers: per tier what it asserts, what it fakes, the gate it guards, the exact blocking command (Law 23) and the boot mode (Law 4); the tiers that share a store (Law 10); the settings and test-configuration paths that trigger the double-run (Law 29); the scheduled leg and the schedule window, which also bounds the close cadence (Laws 21, 23, 29)
2. Landscape summary: counts per area, the `NONE` count, the exclusions applied, the ruled rows
3. Existing tests: `retired.tsv`'s row count and the dispositions expected
4. Doors and seams: the seam plan and the fakes directory, the fixtures, the bare-door baseline, the spelling sets, the rendered-from set, `exclusions.txt`
5. Prior art: adopted, avoided, `measure here`, each with its source
6. Shared state: stores, writers, readers, correlation columns
7. Lanes: the order with reasons, `beats.md`, the `need` preludes
8. Crossings: the table and the after-effect beats
9. Map and observed coverage: `map.tsv`, the derivation commands, `pending.txt`
10. Activity log: destinations, levels per environment, record fields, the buffer handler, the redaction test
11. Speed: targets per tier and, with a live tier, per sequence and lane; the budget headroom, the paper cost, the critical path, the per-change policy, any parallel mode
12. Harness contract: library, runner, ledger, checkpoints, root hash inputs, the ledger files, `self-tests.txt`
13. Build order: below
14. Not covered: every surface no tier reaches (a platform with no live tier, a flow needing absent hardware), each with its reason
15. Open rulings: every number or trade-off the owner decides

A section with nothing to design has the single line `none — <reason>` as its whole body; a tier with no tests is its Tiers line `<tier>: none — <reason>`. With no live tier, sections 6–8 read `none` and section 11 holds only the tier targets and the headroom; section 10 reads `none` only for a product with no runtime; every other section is produced in full. Before a check errors on an absent input, it reads the `none` marker of that input's section or tier, as a list that excuses (Where enforcement ends): with the marker and no artifact of that section in the tree (no lane runner declared and no file matching the lane-file pattern, no hermetic ledger) it prints `PASS none:<section>`; with the marker and an artifact, `ERROR NONE-AMBIGUOUS`.

Build order, by risk and dependency:

1. Run isolation and the ledger's tree binding.
2. The collected census and the check self-tests.
3. Wall-time budgets for the unit and hermetic tiers, before any conversion.
4. Seams, fakes with their contract suites, and fixtures; then the packages converted in batches with disjoint file sets.
5. The activity log, then the mocks.
6. The harness, proven with the first lane in sequence and alone before another lane is written.
7. The second lane, then speed measured against the target before a third.
8. Each remaining lane joining the sequence the day it is written, with its after-effect beats, its bites recorded and its budget pinned from three runs.
9. The retirement ledger verified, then the old suite deleted in that commit (Law 24).
10. Close: three green sequences on the merge subject, budgets pinned from them, no flake on the merge subject, `pending.txt` empty, the sequence wired into the release procedure.

With no live tier, steps 6–8 become the unit and hermetic conversions of step 4 with their bites recorded, and close is one green run of the blocking commands on the merge subject, three only in the landing that pins a tier budget.

## Checks

Read `$DESIGN/integration-suite.md § The checks` before adapting a row.

`scripts/suite-checks.sh` runs plain (every commit) or with `--close`. Each check prints one line, `CHECK <name> PASS|FAIL|ERROR <detail>`; the script exits 0 when clean, 1 on a finding, 2 when a check could not run: the three-state protocol of `/quality:llm-codebase` § Protocol. Each check proves both of its sets non-empty before comparing them. Every scope skips the `scope`-tagged paths of `exclusions.txt`. A design value a check reads (a Speed target, the headroom, the schedule window, `size-ceiling`) missing is that check's `ERROR`. Every ruling-bearing input is read as Where enforcement ends says.

Every ratchet baseline is a multiset of signatures, read as a list that excuses: a signature found more often than the baseline holds it fails even when the total fell, so a move keeps its count and a copy raises it; the baseline only shrinks. Its header carries the owner's burn-down ceiling as `# ceiling <YYYY-MM-DD> <count>` lines; the entry in force is the latest dated today or earlier, and the tighter of the two readings applies. A baseline above it fails, and a header with no entry in force is the ratchet's `ERROR`.

| Check | Mode | FAIL on | ERROR on |
| --- | --- | --- | --- |
| `map-ids` | both | a landscape id with no map row; a map row whose id is not in the landscape | `DERIVE-FAILED`: no ids read from the landscape or the map |
| `map-beats` | both | a mapped row no lane file, no test file and no `pending.txt` line defines; a `beats.md` row with no map row; at `--close`, a row still in `pending.txt` | `DERIVE-FAILED`: no rows read; `pending.txt` missing |
| `map-names` | both | an operation the built product reports that no landscape `op:` carries; a landscape `op:` the built product does not report; an `evidence:` row whose `file:line` does not exist | `DERIVE-FAILED`: the registry dump or the `op:` fields came back empty |
| `map-dup` | both | an id mapped twice at one tier without `crossing:`; above the unit tier, more than one success row or one refusal row per operation besides `crossing:` rows; an id whose `crossing:` rows all lie in one lane; a `twin:` naming a row at the same tier or no row; a live row with neither `twin:` nor `live-only`. It prints `DUPLICATE-CANDIDATES` (tests at one tier with equal operation sets and equal expected literals, read from the tier ledgers) without failing on them | `DERIVE-FAILED`: no map rows |
| `landscape-scope` | both | a landscape row whose `op:` matches the exclusion spellings; a row without `surface:`, `kind:` or `ruled:`; `ruled:no` on a default-ruled surface without `ruling:<ref_id>` | the exclusion spellings empty |
| `names` | both | beyond its baseline, an id, a lane key, a test file stem or a test title matching the refused-name spellings, or a property word in a slug or title whose test body carries no violating action; a lane key starting `tier-` | no ids or no test files read; the refused-name or violating-action spellings empty; `name-allowlist.txt` unreadable |
| `oracle` | both | a mapped row without `source:`; a ruled row whose `source:` is not a `references.tsv` row; a hermetic or live row without `live-because:`; in a mapped row's test (every other test is `refused-shapes`'), an expected side holding a name or call that resolves to a product module outside locator position, or a local name whose binding does (a constructor over literals alone is a literal), a literal actual side, a refused oracle spelling without `weak-oracle:` or with it on a ruled row, an expected-side string of three words or more equal to a rendered-from value that is not a `ruled-copy.tsv` key, or a literal count not derived from the seeded fixture; a policy-lint rule citing no `references.tsv` row | the refused-shape or locator spellings, `references.tsv` or the rendered-from set unreadable |
| `copy` | both | a `ruled-copy.tsv` key not pinned in exactly one test per locale | the registry unreadable, or empty without the line `none` |
| `refused-shapes` | both | a signature found more often than its baseline holds it, in the scope each shape's row names | a spelling set empty; the tree, `skip-reasons.txt` or `unobservable.txt` unreadable; zero files scanned; a shape with no planted-violation corpus |
| `gaps` | both | an entry expired, without `expires`, with a `why` lacking an external reference, naming an own identifier or a repository file, or with an owner off the roster | `gaps.tsv`, `roster.txt` or the own-identifier list unreadable |
| `budgets` | both | plain: the paper cost (`cost_ms` summed per lane and along the critical path) above the Speed target, with unpinned keys listed; `--close`: `BUDGET-UNPINNED`; `run_ids` naming fewer than three green canonical runs on one root hash, or for a tier key on one subject; the recomputed maximum plus headroom above the pin; the slowest run over twice the median; the pinned sequence budget above the Speed target; a pin raised by more than the added rows' own maximum `wall_ms` in the named runs | `budgets.tsv` or a named run unreadable |
| `collected` | both | a file matching a test-name pattern anywhere in the repository, outside `exclusions.txt`, that the blocking run's log does not show collected, or shows twice; a `CHECK` line of this table absent from the blocking run's log (the plain checks in the commit job; in plain mode, no close-job log within the schedule window carrying every `--close` line); a fake in the fakes directory with no contract suite collected against both implementations | the CI configuration or the blocking run's log unreadable; no CI job invokes `--close`; no test-name patterns |
| `self-tests` | both | a check, scanner, filter, project-authored lint rule, the runner or the beat library with no input-class line or no `planted` line in `self-tests.txt`; a `self-tests.txt` line absent from the census or not passing on the merge subject; a declared parallel mode, or a store-sharing tier run with a concurrency option, without a passing collision test; a filter fixture (green, red, long red, killed) misjudged or not preserving the failure block byte-for-byte | the census or `self-tests.txt` unreadable |
| `retired` | both | test files deleted since the merge base whose re-derived assertion signatures, minus those reappearing in files the same commit adds, are not set-equal to the ledger rows added since the merge base; a `successor:` that does not exist, did not pass in the named run on this subject, or whose signature differs in subject or in expected literal from the retired one; a `dropped:` whose shape spelling the retired assertion does not match; a `ruling:` not in `references.tsv` | the merge base unreadable; deletions with an empty ledger; the assertion spelling set empty |
| `env-twice` | both; `PASS not-due <run id>` between scheduled legs | the unit tier's results differ between the base configuration and the perturbed one of Law 29 | a leg missing; the last scheduled leg older than the schedule window; the key set or the trigger paths unreadable |
| `bare-doors` | both | a door signature found more often than its baseline holds it | the door spellings empty; the tree unreadable; zero files scanned |
| `clones` | both | a clone in the test tree beyond the test tree's baseline, found by the token-based clone detector this script runs | the detector absent, or it matched no files |
| `size` | both | a test or lane file above `size-ceiling` beyond the test tree's baseline | `size-ceiling` not declared |
| `tree` | `--close` | the canonical ledger's subject hash differs from the merge subject; its dirty hash is non-empty; its start and end hashes differ; `mode: parallel:<n>` offered as canonical, or a `sequence` ledger whose rows' `t+s` intervals overlap across lanes; `boot: in-process` without the single-instance and two-mode tests passing on the merge subject, or `boot: process` with product records carrying its `runner_pid`; a lane without a green `sequence` ledger, or without a green `solo` ledger since its lane files last changed | no ledger for this subject |
| `observed` | `--close` | `CLAIMED-NOT-INVOKED`: a row mapped to an id with no operation record carrying that row's injected id, and for a unit row no coverage context reaching the capability's file; `UNOBSERVED`: an id no row invoked; `NO-ASSERTION`: a mapped id with no `assert` naming it in that row; for a tier row also `TIER-UNCOLLECTED`, `TIER-NOT-RUN`, `TIER-NO-BITE` | no canonical ledger; the `op:` fields empty; a tier ledger missing for a tier the design declares |
| `bite` | `--close` | a mapped row, a project-authored lint rule, a rule in `third-party-rules.txt`, a check or a refused shape with no bite row, or a `drift:` row with fewer than two; a bite row whose red run does not exist, does not show the row ✗, or whose dirty hash differs from `diff_hash`; a mutation inserting an unconditional exit or removing more than one statement; a mapped row's mutation outside the capability's file or the op's handler; a lint rule's red run whose `command:` is not the blocking command | `bites.tsv` or `third-party-rules.txt` unreadable; a named run or its diff unreadable |
| `flakes` | `--close` | a row the run ledgers show failing and passing on the merge subject with one dirty hash and root, recomputed, never read from `flakes.tsv`; no shuffled-order ledger for the unit or hermetic tier within the schedule window; a retry or rerun setting in a runner configuration outside tests named in `flakes.tsv` | the ledgers unreadable |

The `--close` rows read the run ledger, not the map: during the build, pending rows and unproven bites are the expected state. A listed `gaps.tsv` row that passes is the runner's red, not this script's. A check that cannot be expressed as a script goes under Open rulings.

Copy the skeleton below to `scripts/suite-checks.sh` and adapt it; it carries the protocol, the merge-base readings, the ratchet, the `none` gate and the subject hashes, with one worked set check and one worked ratchet check:

```sh
#!/bin/sh
# suite-checks.sh [--close]: one CHECK line per row; exit 0 clean, 1 on a finding, 2 when a check could not run
set -u; export LC_ALL=C
MODE=${1:-plain}; case $MODE in plain|--close) ;; *) echo "usage: $0 [--close]" >&2; exit 2;; esac
# SLOT lines: set each to this tree's value; a slot left unset keeps its rows at ERROR, never PASS
S='SLOT: the design directory, holding design.md and the ledgers'
MAIN='SLOT: the main branch'
LNG='SLOT: the language suffix of the spelling sets'
SRC='SLOT: an extended regex matching the product source paths'
OWN_IDS='SLOT: repository names|issue-tracker hosts|internal domains'
DOC="$S/design.md"; K=scripts/suite-checks; rc=0; TAB=$(printf '\t')
say() { printf 'CHECK %s %s %s\n' "$1" "$2" "$3"; case $2 in FAIL) [ "$rc" -ge 1 ] || rc=1;; ERROR) rc=2;; esac; }
cd "$(git rev-parse --show-toplevel 2>/dev/null)" || { say setup ERROR "not inside a repository"; exit 2; }
T=$(mktemp -d) || { say setup ERROR "mktemp failed"; exit 2; }; trap 'rm -rf "$T"' EXIT
MB=$(git merge-base HEAD "$MAIN" 2>/dev/null) || { say setup ERROR "merge base unreadable"; exit 2; }

# at <path>: the merge-base copy; a path the merge base lacks is being installed, so the working tree
at() { if git cat-file -e "$MB:$1" 2>/dev/null; then git show "$MB:$1"; else cat "$1"; fi; }
# excuse <path>: a list that excuses, read as the multiset intersection of both readings ('#' lines dropped)
excuse() { [ -r "$1" ] || return 1; at "$1" | grep -v '^#' | sort > "$T/e1"; grep -v '^#' "$1" | sort > "$T/e2"; comm -12 "$T/e1" "$T/e2"; }
# oblige <path>: a list that obliges, read as the union of both readings
oblige() { git cat-file -e "$MB:$1" 2>/dev/null || [ -r "$1" ] || return 1; { at "$1"; cat "$1"; } 2>/dev/null | grep -v '^#' | sort -u; }
# lower <a> <b>: the tighter of two numbers
lower() { if [ "$1" -le "$2" ]; then echo "$1"; else echo "$2"; fi; }
# ceiling <baseline>: per reading, the '# ceiling <YYYY-MM-DD> <count>' line with the latest date not after today;
# the lower of the two readings (a merge base without one: the working tree's); returns 1 when none is in force
ceil_in() { awk -v d="$(date +%Y-%m-%d)" '$1=="#" && $2=="ceiling" && $3<=d && $3>=m {m=$3; c=$4} END {if (c=="") exit 1; print c+0}'; }
ceiling() { w=$(ceil_in < "$1") || return 1; a=$(at "$1" | ceil_in) || a=$w; lower "$a" "$w"; }
# value <name>: a '<name>: <integer>' line of design.md, the lower of both readings; returns 1 when missing
val_in() { awk -F': ' -v k="$1" '$1==k {print $2+0; f=1; exit} END {if (!f) exit 1}'; }
value() { w=$(val_in "$1" < "$DOC") || return 1; a=$(at "$DOC" | val_in "$1") || a=$w; lower "$a" "$w"; }
# ratchet <row> <baseline> <current signatures, one line per occurrence> <items scanned>
ratchet() { [ "$4" -gt 0 ] || { say "$1" ERROR "zero items scanned"; return; }
  c=$(ceiling "$2") || { say "$1" ERROR "baseline $2 unreadable or no ceiling in force"; return; }
  excuse "$2" > "$T/base" || { say "$1" ERROR "baseline $2 unreadable"; return; }
  sort "$3" > "$T/cur"; over=$(comm -13 "$T/base" "$T/cur" | tr '\n' ';'); nb=$(( $(wc -l < "$T/base") ))
  if [ -n "$over" ]; then say "$1" FAIL "beyond baseline: $over"
  elif [ "$nb" -gt "$c" ]; then say "$1" FAIL "baseline holds $nb, above ceiling $c"
  else say "$1" PASS "$4 scanned, $(( $(wc -l < "$T/cur") )) signatures, none beyond baseline"; fi; }

excuse "$S/exclusions.txt" > "$T/excl" || { say setup ERROR "exclusions.txt unreadable"; exit 2; }
# tagged <tag> / untagged <tag>: the paths on stdin inside / outside every glob exclusions.txt gives that tag
tagged() { while IFS= read -r f; do while IFS="$TAB" read -r g t; do [ "$t" = "$1" ] && case $f in $g) printf '%s\n' "$f"; break;; esac; done < "$T/excl"; done; }
untagged() { while IFS= read -r f; do hit=; while IFS="$TAB" read -r g t; do [ "$t" = "$1" ] && case $f in $g) hit=1; break;; esac; done < "$T/excl"; [ -n "$hit" ] || printf '%s\n' "$f"; done; }
# scoped: the tracked or addable files every scan reads, outside the scope-tagged globs
scoped() { git ls-files -co --exclude-standard | untagged scope | while IFS= read -r f; do [ ! -f "$f" ] || printf '%s\n' "$f"; done; }
# subject_tree: the staged-tree hash over the hash subject; subject_dirty: a hash of the unstaged diff and the
# untracked, non-ignored files under the subject, empty when there are none (the runner writes both at start and end)
subject_tree() { cp "$(git rev-parse --git-path index)" "$T/idx" && git ls-files | tagged hash | GIT_INDEX_FILE="$T/idx" git update-index --force-remove --stdin && GIT_INDEX_FILE="$T/idx" git write-tree; }
subject_dirty() { { git diff --name-only; git ls-files -o --exclude-standard; } | untagged hash | sort -u | while IFS= read -r f; do
  if [ -f "$f" ]; then printf '%s %s\n' "$(git hash-object -- "$f")" "$f"; else printf 'deleted %s\n' "$f"; fi; done > "$T/dirty"
  [ ! -s "$T/dirty" ] || git hash-object "$T/dirty"; }
# none_gate <row> <section or tier> '<artifact test>': call before a row errors on an absent input. The marker is a
# '## <n>. <section>' heading whose first body line starts 'none — ', or a '<tier>: none — ' line, present at the
# merge base and in the working tree. Marked: prints PASS none:<name> or ERROR NONE-AMBIGUOUS, returns 0. Unmarked: 1.
marked() { awk -v h="$1" 'f && NF {print; exit} $0 ~ "^## [0-9]+\\. " h "$" {f=1} index($0, h ": none — ") == 1 {print "none — "; exit}' | grep -q '^none — '; }
none_gate() { { at "$DOC" | marked "$2"; } && marked "$2" < "$DOC" || return 1
  if eval "$3"; then say "$1" ERROR "NONE-AMBIGUOUS: $2 marked none, an artifact exists"; else say "$1" PASS "none:$2"; fi; }

# every set check has this shape: derive both sets, ERROR when either is empty, then compare
chk_map_ids() { grep -oE '^[a-z][a-z0-9-]*\.[a-z][a-z0-9-]*' "$S/landscape.md" 2>/dev/null | sort -u > "$T/L"
  sed 1d "$S/map.tsv" 2>/dev/null | cut -f1 | sort -u > "$T/M"
  if [ ! -s "$T/L" ] || [ ! -s "$T/M" ]; then say map-ids ERROR "DERIVE-FAILED: no ids read from the landscape or the map"; return; fi
  d=$(comm -3 "$T/L" "$T/M" | tr -d '\t' | tr '\n' ' ')
  if [ -n "$d" ]; then say map-ids FAIL "unmatched: $d"; else say map-ids PASS "$(( $(wc -l < "$T/L") )) ids, each mapped"; fi; }
# every ratchet check has this shape: the spellings as obliged, the files scanned counted, signatures ratcheted
chk_bare_doors() { oblige "$K/doors.$LNG" > "$T/spell" && [ -s "$T/spell" ] || { say bare-doors ERROR "door spellings empty or unreadable"; return; }
  scoped | grep -E "$SRC" > "$T/src"; n=$(( $(wc -l < "$T/src") ))
  extract_doors < "$T/src" > "$T/doors" || { say bare-doors ERROR "the extractor could not read the tree"; return; }
  ratchet bare-doors "$K/bare-doors.baseline" "$T/doors" "$n"; }
# SLOT extract_doors: file names on stdin, the spellings in $T/spell; prints one signature (the enclosing symbol and
# the normalised statement) per match; exits 0 with no output when nothing matches, non-zero only when it could not read
extract_doors() { return 2; }

# run <row…>: each row runs its chk_<row> function; a row with none is an ERROR, never a silent pass
run() { for r; do f=chk_$(printf '%s' "$r" | tr - _); case $(command -v "$f") in "$f") "$f";; *) say "$r" ERROR "not adapted: no $f";; esac; done; }
run map-ids map-beats map-names map-dup landscape-scope names oracle copy refused-shapes gaps budgets collected self-tests retired env-twice bare-doors clones size
[ "$MODE" = plain ] || run tree observed bite flakes
exit "$rc"
```

- Set every `SLOT` line. A slot left as written keeps its rows at `ERROR`, and the unadapted skeleton exits 2.
- Write one `chk_<row>` function per row of the table, dashes as underscores; `run` reports a row with no function as `ERROR`, so the script never reads clean with a row missing.
- A set check follows `chk_map_ids`: derive both sets, report `ERROR` when either is empty, then compare. A ratchet check follows `chk_bare_doors`: read its spellings with `oblige`, count the files it scans, extract one signature per match, and call `ratchet`, which reports `ERROR` on zero items scanned and on a baseline with no ceiling in force.
- Read every ruling-bearing input through `excuse`, `oblige`, `ceiling` or `value`, as Where enforcement ends classes it.
- Before a row errors on an absent input, it calls `none_gate` with the section or tier that input belongs to and the test for that section's artifact, and stops when `none_gate` printed the verdict.
- The runner writes `subject_tree` and `subject_dirty` into each run ledger's header at the start and the end of the run; `tree` compares them with the merge subject.

## Where enforcement ends

Read `$DESIGN/integration-suite.md § The threat model` before any edit to a ruling-bearing input and before writing a check that reads one.

The checks defend against accident, laziness and the pull toward green by an author who does not forge: a field left empty, a value guessed, an assertion written to pass, a check that could not look, a list grown in the commit that needs it. A ruling, a reference or a run ledger forged and landed is out of scope.

A ruling-bearing input is a file or a design-document value a check trusts instead of verifying. Two lines hold them:

- Owner-owned paths: the design document, the checks script with its directory, and the ruling-bearing ledgers change only with the owner's review (a code-owners rule, or the version-control system's equivalent). This line is never the only one, since it is void where the author commits as the owner.
- The merge base: every check reads each ruling-bearing input at the merge base and in the working tree and applies the stricter reading: the intersection of a list that excuses, the union of a list that obliges, the tighter of two numbers; an unreadable merge base is the check's `ERROR`. A loosening therefore takes effect from the next landing and a tightening at once; an added obligation lands after the baseline entries that excuse what already violates it. The landing that installs an input reads it from the working tree.

| Read as | The ruling-bearing inputs |
| --- | --- |
| lists that excuse | `references.tsv`, `roster.txt`, `skip-reasons.txt`, `unobservable.txt`, `gaps.tsv`, `flakes.tsv` (its retry exemption), `exclusions.txt`, every signature baseline, `name-allowlist.txt`, the locator spellings, the design's `none` markers |
| lists that oblige | `self-tests.txt`, `ruled-copy.tsv`, `third-party-rules.txt`, the landscape's `ruled:yes` marks, every other spelling set and its planted-violation corpus, the test-name and lane-file patterns, the own-identifier list, and the design's blocking command per tier (a replaced one runs beside its successor for one landing), store-sharing tiers, rendered-from paths and double-run trigger paths |
| numbers | the design's Speed targets, budget headroom and schedule window, every baseline's burn-down ceiling, `size-ceiling` and the clone detector's threshold |

`budgets.tsv` and the flakes are recomputed from the run ledgers, so neither is ruling-bearing.

At every landing the suite reviewer (in a project installed from this blueprint, `flights-lander`; elsewhere, whoever the owner names) reads what no check can judge, and a landing without that reading is unreviewed:

- every loosening of a ruling-bearing input;
- a sample of the recorded bite mutations, re-run, and for each guard both directions and the boundary plus and minus one;
- every ruled-row oracle against its cited `references.tsv` row, and every product change under a ruled row's `file:line` against the same;
- the landscape for every guard's refusal row (the error-code registry as the list, where one exists), every added row's `surface:`, and every `evidence:` row's `file:line` for the code that writes the output;
- every policy-lint rule, for a property asserted rather than a line's text pinned;
- every `twin:` and `live-only` reason, every added `weak-oracle:` and `skip-because:` against its class or reason, each subject's input-class lines in `self-tests.txt` against the inputs it reads, and the `DUPLICATE-CANDIDATES` list.

## Hand-off

Read `$DESIGN/integration-suite.md § The hand-off` and `$DESIGN/integration-suite.md § Where it sits`.

The design is reviewed before anything is built: unmapped capabilities, crossings asserted from one side only, rows with no nameable bite, oracles with no source, checks whose broken state would read as PASS. The laws reach every tier's tests through three carriers the design writes: the checks script, the skeleton ledgers, and the rows the project's testing manual must carry: under its Lanes and registries section the per-change duty (the landscape row, the map row, the mapped row and its bite in one commit; the sequence at every close where a live tier exists), under its What not to test section the refused shapes. A rule that lives only in a file the project may not hold is not in force. The `clones` and `size` rows run in this suite's own checks script on the test tree's own baselines, so no neighbour command's gate needs to change for them.

In a project installed from this blueprint, the flights cast builds and keeps the suite: `flights-speccer`'s reconcile phase performs the review, and the build goes through `/flights:spec`; a flight executor builds the harness, the mocks and the lanes from the task files Build order produces, recording each row's bite; `flights-lander` runs the blocking commands of the unit and hermetic tiers, sweeps the refused shapes and performs the reviewer's reading of Where enforcement ends on every landing, and where a live tier exists runs the touched lanes from their checkpoints while it fixes, the touched lane alone, and the sequence at the gate's open (the baseline a red at close is attributed against) and at its close. A defect a test or a lane exposes is the lander's to fix. Elsewhere the owner assigns the review, the build and the runs: Law 7 is the run policy, Law 20 the per-change policy, and Laws 11–15 the verdict rules at every tier.

Decide nothing this command governs before reading its `$DESIGN` section.
