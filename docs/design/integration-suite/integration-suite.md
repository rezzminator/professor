# /quality:integration-suite

`/quality:integration-suite <project|subsystem>` designs a project's whole test suite: its tiers, what every test at every tier may and may not be, the ledgers and checks that keep the suite honest, and — where the product has a live tier — its integration tests as lanes over one shared state. Every capability of the product is inventoried from code and mapped to a test that asserts it, the live beats are ordered so features meet each other the way they do in production, and coverage is proven by what a run observed, never by what a file declares. The command returns a design document plus its skeleton files and edits no code. It is stack-agnostic: the same procedure designs the suite for a CLI, a web service, a UI, a daemon, a library with a unit tier only, or a product with LLM-driven steps.

This file covers how the command works and why each part of it exists. [laws.md](laws.md) covers Laws 1–18 (tiers, shape, verdict) and [validity.md](validity.md) covers Laws 19–31 (honesty over time, what a test may not be), each with the reasoning and the sources it rests on. The command itself lives in [`templates/global/commands/quality/integration-suite.md`](../../../templates/global/commands/quality/integration-suite.md).

## Contents

- [What it is for](#what-it-is-for)
- [The six ideas](#the-six-ideas)
- [Inputs](#inputs)
- [The shape](#the-shape)
- [Terms, and why each exists](#terms-and-why-each-exists)
- [Names and ids](#names-and-ids)
- [The procedure, and why each step](#the-procedure-and-why-each-step)
- [The output](#the-output)
- [The checks](#the-checks)
- [The threat model](#the-threat-model)
- [Seed evidence](#seed-evidence)
- [The hand-off](#the-hand-off)
- [Where it sits](#where-it-sits)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)
- [Open items](#open-items)

## What it is for

Most projects organise their tests the way their code is organised: one test file per module, each one building a small world, asserting something, and tearing the world down. That proves the parts work. It cannot see the defects users actually meet, because those live where features share state:

- a record two roles edit;
- a config change landing under live sessions;
- a dependency restart with work in flight;
- a credential that expires between two surfaces that report it differently;
- a delete while something still references the target.

Every module test starts from a clean world, so the state one area leaves behind for another never exists in any test.

Line coverage does not reveal the gap. It measures the lines that ran, not the capabilities that were asserted, so a project can report high coverage without ever having walked one user journey end to end. A coverage map does not reveal it either, unless the map is checked against what a run actually did: a map checked by listing is satisfied by listing. The command designs the missing layer where the product has one, and with it the proof — observed, not declared — that the suite covers every capability the product has.

The command owns the whole suite, not only its live tier. The shapes that make a test worthless — an oracle read from the code under test, a test that cannot fail, a skip that reports absence as success, a check that reads clean when it could not look, a test deleted before its successor is verified — appear at every tier, and a project with only a unit tier meets every one of them. So the tiers, the refused shapes and their ratchets, the collected census, the bite ledger, the tier budgets, the configuration double-run and the retirement ledger are produced for every project; the lanes, crossings, checkpoints and the sequence's speed are the live-tier part, and a project with no live tier records each of those as `none — <reason>`.

The design keeps three promises:

- **Nothing ships that disappoints a user.** Every capability maps to a test that fails when that capability breaks, the test was watched failing once, and a run observed it invoking the capability and asserting on it.
- **Tests never become the bottleneck.** Nothing slow sits on the commit path, speed is designed before the first lane is written, and every suite's wall time can only fall.
- **A green means the tree being merged.** Every verdict names the tree it ran on, every check names its own broken state, and every check that exists is one the blocking gate runs.

## The six ideas

Every law and every step serves one of these.

1. **The defects live at the crossings.** The live tier is organised by actor journey over one accumulating state, and every place where two journeys share state is asserted from both sides. Laws 6–10; steps 5–7.
2. **Realism and speed pull against each other, so the tiers split the work, and speed is designed.** The hermetic unit and hermetic end-to-end tiers gate every commit. The live tier gates the close of each piece of work and each release. Each tier asserts only what the tier below cannot, the sequence's cost is priced on paper before the build, and the working loop runs one lane on the sequence's own state from a checkpoint. Laws 1–5, 7, 16 and 20; steps 4, 6 and 10.
3. **A verdict must be able to fail.** A test suite is an instrument. An instrument that reports "fine" when it is itself broken is worse than none, because it gets believed. So every test is watched failing once, every check names its own broken state, coverage is read from the run ledger and not from the map, a green is bound to the tree it ran on, and a check counts only when the blocking gate runs it. Laws 13, 14, 15, 22 and 23; steps 8 and 11; § The checks.
4. **The oracle is not the code.** Whoever writes a test is pulled toward an expected value that is green by construction: computed by the product, imported from the module under test, read from the file the product renders from, or guessed and then defended by editing production. Every expected value comes from a source independent of the code, ratified by the owner where the outcome is ruled, and a disagreement means the product is wrong. Laws 11, 12, 17 and 18; steps 6 and 9.
5. **Only the product is tested.** The landscape admits only behaviour a user, an operator or an integration invokes on the running product. The toolchain, the CI structure, shipped migration content, source text, absences and history are refused by rule, and a test retires only through a verified successor. Laws 24–27 and 30; steps 1 and 2.
6. **The suite is kept by agents, over time, under an owner.** Every artifact is machine-read, every rule that can be a script is one, every ledger has a red state, every recorded number moves in one direction only, and the files that carry a ruling are the owner's to change and loosen only from the next landing. A rule that lives only in prose ends up enforced at some of its doors and violated at the rest. Laws 19–21, 28, 29 and 31; steps 3, 8, 9, 11 and 12; § The threat model.

## Inputs

- **The target:** a project's test suite, or one subsystem of it. A subsystem gets the same procedure with a narrower landscape. A project with no live tier — a library, a tool with no shared state, an owner who asks for none — gets the same procedure: the live-tier steps answer `none — <reason>` and every other step runs in full.
- **The live tree:** the product's own entry-point registries, its build configuration, its settings schema (or, where none exists, the key set the environment-reader seam's call sites name) and its existing tests, read from code, never recalled. A design built from memory describes the project as someone remembers it.
- **The project's orientation file:** its test tiers, its isolation rules, and what a test run may never touch. These are the owner's constraints, not the designer's to invent. When the file is missing, each of the three becomes an Open ruling rather than a guess.
- **The project's ratified references:** its facts, specs, contracts and rulings, indexed in `references.tsv` (§ Terms), which is where Law 12's `source:` values resolve. When none exist, every ruled assertion's source is an Open ruling.
- **The project's own identifiers:** its repository names, its issue-tracker hosts and its internal domains, listed in the checks script so that `gaps` can tell an external reference from an internal one (Law 19).
- **The owner,** for the rulings in step 12 and as the reviewer of the ruling-bearing files (§ The threat model).

## The shape

```text
unit tier      every door behind a seam, parallel, seconds                 ┐ every commit:
hermetic tier  the real binary or server, as a separate process,          ┘ no credential,
               against scripted mocks, no network beyond loopback           no live dependency
live tier      the real system against its real dependencies               close of each piece of work, release

one live-tier run:
root (installed, configured, seeded; snapshotted, keyed by a hash of its inputs)
  └─► ONE environment, lanes in canonical order, state accumulating, a checkpoint after each lane:
      state builders ──► readers over the richest state ──► destroyers
      a lane's working loop restores the checkpoint before it; a lane also runs alone from a fresh root
      the runner keeps going after a red lane; every lane reports its own first fault
```

Coverage is not a percentage of lines and not a list of ids. It is a map from every landscape id to a test, proven by a run ledger that shows the test invoking the capability under its own injected id and asserting on that id. A script checks the map before any lane exists, and reads the ledger at close; it also reads the operation names from the built product, so the landscape cannot fall behind the code and the code cannot fall behind the landscape.

## Terms, and why each exists

- **landscape:** the denominator. Without a closed list of capabilities, a coverage claim describes the tests, not the product. It holds product behaviour only (Law 25).
- **door and seam:** the unit tier is fast and hermetic only where every contact with the outside world stands behind an interface that has a fake. The project's own data layer (its database, cache and queue broker, run locally) is part of the system in the hermetic and live tiers, because faking it there would test a data layer the product does not use. The unit tier reaches it only in the packages that own persistence.
- **unit, hermetic and live tiers:** three fidelities at three costs, each with the gate it can afford (Law 1), each asserting only what the tier below cannot (Law 3).
- **lane:** one actor pursuing one kind of goal, depth-first. The lane is the unit of realism. Lanes are cut by journey because a journey is how features meet; a lane cut by module rebuilds the unit tier at the price of the live tier. Lane files match the lane-file pattern the checks script declares.
- **beat:** an action plus the assertion of its result. The beat is the unit of verdict, the smallest thing that can be ✓, ✗, `known`, `blocked` or `skip-because`. A beat with several arms is several beats, so every verdict and every `known` mark is one arm's.
- **mapped row:** a beat, or a unit or hermetic test the map names (`tier-unit:<test>`, `tier-hermetic:<test>`). Every mapped row has a row in `beats.md`, so the oracle, bite and observed checks read every tier through one shape; a unit row carries only what a unit test can honestly state.
- **crossing:** a piece of shared state that two lanes touch from different sides. The crossing is the unit of integration risk.
- **root:** the installed, configured, seeded environment every run starts from. It is paid for once and reused by hash, so a lane's minutes go to the product instead of to setup.
- **checkpoint:** the environment as the sequence left it after a lane, snapshotted and keyed like the root. It is what makes one lane's working loop run on the sequence's state (Law 7).
- **carry:** the ids and credentials a beat created or was handed, which every read it makes is scoped to (Law 10).
- **run ledger and runs store:** the runner's record of a run — a header and one row per mapped row, in the shapes of step 11 — which every tier's runner writes to the runs store: a local run to `runs/` beside the ledgers, `hash`-tagged and landed with the change, and a CI job to the CI system's artifact store, keyed by subject so a rerun reads its predecessors. A run a check reads after its landing — one a ledger file names, a lane's latest green `solo` — lives under `runs/`, so it outlives any store's retention; any other ledger there may be pruned once landed. The ledger is the proof of coverage; the map is the plan (Law 14).
- **coverage context:** the set of product lines executed under one test's id, as the coverage tool attributes them per test. It is how a unit row proves invocation where no operation record carries its id.
- **canonical ledger:** the ledger of a full sequence run (`mode: sequence`, every lane) on the merge subject; with no live tier, the ledgers of the blocking commands on the merge subject.
- **landing:** one change's merge into the main branch, identified by its merge subject.
- **hash subject and merge subject:** the hash subject is every tracked path minus those `exclusions.txt` tags `hash` (the ledgers, the documentation); the merge subject is the version-control system's staged-tree hash over the hash subject for the change being merged (Law 22).
- **`exclusions.txt`:** one path glob per line with a tag — `hash` for paths the subject omits, `scope` for paths no check collects or scans (the planted-violation corpora, lint-rule fixtures, the scratch-migration directory, the synthetic-log fixtures, vendored code).
- **signature:** the enclosing symbol plus the normalised statement, independent of the file, so a rename or a split re-reds nothing. Every exemption anchor is a signature, and every ratchet baseline a multiset of them (§ The checks).
- **path façade:** the product's one module that resolves every environment path — home, configuration, data, log — from the environment root, so a test, a lane run and production never share a path.
- **spelling set:** a per-language list of the call and pattern spellings one check searches for, one file per language under the checks directory, applied to the paths the check's scope names. The door, assertion, refused-shape, refused-name, violating-action, exclusion and locator spellings are such sets; locator position is an argument the locator spelling set names — a selector, a key, an id — rather than the value asserted.
- **bite:** the product change that turns a test red, named in its row and watched once at authoring; recorded as the diff and the red run (Law 13).
- **source:** the independent origin of an expected value; for a ruled row, a row of `references.tsv` (Law 12).
- **`references.tsv` and the roster:** the ratified-reference index, `ref_id⇥path⇥locator`, one row per fact, spec line, contract line or ruling the owner has ratified; and `roster.txt`, one owner identifier per line. Both are ruling-bearing (§ The threat model).
- **fakes directory:** the one directory under the test tree, named in the seam plan, where every fake lives (Laws 2, 30).
- **schedule window:** the longest period between two runs of the scheduled leg, and between two close runs, declared in the Tiers section and counted from the landing that installed the suite, so no check can fail on it before one window has passed.
- **retirement ledger:** the per-assertion record that lets an existing test be deleted (Law 24).

## Names and ids

Every id is domain words in kebab-case. No single letters, primes, dates, bug ids, wave names or team names anywhere — not in an id (the runner-assigned run id excepted), a lane key, a test file stem or a test title: a code has to be decoded, and the decoding lives in one person's head or one document that drifts. The same string is the key on every surface — file stem, runner argument, ledger column, log field, map cell — so a rename is one mapping applied everywhere, accepted when the old name greps empty repo-wide and one run is green.

| Id | Pattern | Example |
| --- | --- | --- |
| landscape id | `<area>.<capability>`, at line start | `invoice.export-pdf`, `member.revoke-access` |
| lane key | `<domain-words>`, three characters or more, never starting `tier-` | `member-journey`, `billing-cycle`, `operator-tail` |
| beat id | `<lane>/<slug>` — the slug states the action | `billing-cycle/export-invoice` |
| tier row in the map | `tier-unit:<test name>` or `tier-hermetic:<test name>` | `tier-unit:cursor-rejects-foreign-sort` |
| test file stem and title | domain words; the refused-name spellings fail `names`: a bug id, a date, a wave or team word, `line N`, a code segment of one or two letters plus digits outside `name-allowlist.txt` (`s3`, `v2`), a numeric segment in an id, a key or a stem, and in a title only an ordinal (`#N`, a leading `N.`, `case N`, `scenario N`) | `cursor-paging.test`, "a foreign-sort cursor is refused" |
| run id | `<lane or sequence>-<subject short hash>-<start time>` | assigned by the runner |

The `tier-` prefix is reserved so a map row that names a unit or hermetic test can never be read as a lane. Beats carry no ordinal: the lane file's order is the run order, and the timeline records the position, so inserting a beat renumbers nothing. A property word in a slug or a title (durable, recovers, idempotent, exclusive, retries) is admitted only when the test body carries a violating action from the violating-action spelling set (restart, kill, replay, concurrent, expire, revoke, repeat, or a scripted fault: fail, error, drop, disconnect, timeout, 5xx): a name that promises a property the body never attacks is a false coverage report (Law 13).

## The procedure, and why each step

### 1. Landscape

The step enumerates every capability closed-world, from code, into `landscape.md`, one id-numbered row per capability. The surfaces are entry points a user, operator or integration invokes (routes, UI actions, CLI commands of a CLI product, webhooks, scheduled jobs, queue consumers), integrations, lifecycle (install, the migration mechanism as one row, config, flags, upgrade from the previous release, uninstall), identity, money, consent, safety, the data lifecycle per core entity, operations, and the tests that exist per tier.

- **From code, never from docs.** Docs describe intent and lag behind the code. The product's registries (router, CLI tree, job registry, tool list) are what actually ships.
- **Product behaviour only** (Law 25). Package-manager scripts, build, lint, type-check, format, test and codegen steps, CI structure, container build order, lockfiles and version pins are excluded by rule and never become rows. A service has no help tree; its manifest's script list is not one. The migration mechanism is one row (Law 26); a shipped migration is never a row, and the upgrade row builds its previous state by running the previous release, never by restoring a dump.
- **A guard is two rows.** A capability with a refusal path — a permission check, a validation, a tenancy boundary — is inventoried as its success and its refusal (`kind:`), because a refusal that is not a row is owed nothing (Law 14). The registries cannot enumerate refusals; the product's error-code registry, where it has one, is the reviewer's list (§ The threat model).
- **One collection pass per surface, and each pass names what it could not read.** A surface that silently returns nothing looks like a product with nothing there. An empty enumeration is a failure to look until the enumerator is proven to have run.
- **The `op:` column** holds the operation name the activity log records when this capability runs — the name the registry dumps. Observed coverage joins on it (Law 14). A capability that leaves no operation record (a rendered screen, a generated file) declares `evidence:<golden path>` instead, and its `file:line` names the product code that writes that output. A time-driven job is covered by a beat that triggers it through an operator entry point carrying the injected id, or by a unit row.
- **The `ruled:` column** marks the rows whose outcome is ruled. Rows whose `surface:` — the surfaces above the row belongs to, joined by `+` — includes identity, money, consent, safety or data-lifecycle are `ruled:yes` by default, and `ruled:no` on one of them carries `ruling:<ref_id>`; any other row the owner adds in step 12. The column is what `oracle` reads instead of guessing from an area name (Law 12).
- **The `needs:` column** tells the lane designer what the environment must provide (a dependency, a credential, a platform). It also exposes early what no tier can reach, which later becomes the Not covered section.
- **The `today:` column and the `NONE` count** measure the size of the hole. That is the baseline the design is judged against.
- **The row shape:** `<id> · <what is asked for> · surface:<…> · kind:<success|refusal> · op:<name> | evidence:<golden path> · ruled:<yes|no|no ruling:<ref_id>> · needs:<…|none> · today:<unit|hermetic|live joined by +, or NONE> · <file:line>`.

### 2. Existing tests

The step writes the assertion spelling set — the language's assertion call spellings, one file per language — and, for every existing test the design retires, one `retired.tsv` row per assertion, derived by that spelling set from the test's text, never from file names or titles: `file⇥assertion_signature⇥disposition⇥run_id`, the signature being § Terms' signature of the assertion with its expected literal or code and its subject (the call or field on the actual side), the disposition empty. The build fills each row (Law 24) before the old test can go. Tests the design keeps get no row, and an assertion whose signature reappears in a file the same commit adds is moved, not retired.

- **Assertion, not file.** A file name and a scenario title describe intent; the assertion is what guarded something.
- **The spelling set comes first,** because the check re-derives the same rows from the merge base at the deletion commit and requires set equality: a row pruned by hand is then a red row.
- **The ledger exists before the design of the lanes,** because a lane designer who can see every old assertion writes the successor rows on purpose, and the ledger is the loss check at the deletion commit.
- **The old suite is claims to verify, never fixtures or expected values.** Its fakes and literals are exactly the oracles Law 12 refuses.
- **Junk gets a name, not a port.** An old assertion that a refused shape (validity.md § The refused shapes) covers takes `dropped:<shape>`, and the check confirms the assertion matches that shape's spelling.

### 3. Doors

The step writes the door spelling set and traces every door with it. From that trace come the seam plan, a ratchet on bare doors, up to ten edge-case fixtures, and the refused-shape, refused-name, violating-action, exclusion and locator spelling sets.

- **Hermeticity is bought at the seam, not at the boundary.** Parallel unit tests are legal only where every door is seamed. A unit test that sleeps on the real clock or spawns a real process is slow and order-sensitive by construction.
- **One spelling set serves both the trace and the ratchet.** Two lists drift apart, and a spelling that the trace searched for but the ratchet omits is a door the gate never counts.
- **One seam per mechanism:** one clock, one process runner, one environment reader, one client interface per external service, each with a fake that lives in the fakes directory (Law 30) and passes the real thing's contract (Law 2). Five local clocks are five fakes that each advance time differently.
- **The harness's own actors use the same clock seam** (Law 16). A test actor that waits for the next real interval of a time-based code is a bare door in the harness, and a poll whose condition reads the clock is the same wait under another name.
- **The ratchet only shrinks, and it is a multiset of signatures read at the merge base** (§ The checks). A door is a signature, not a count, so neither a swap of one bare door for another nor a copy of one can hide, and a baseline grown in the commit that needs it takes effect only from the next landing (§ The threat model). Every door left bare carries a reason, so the remaining debt is named, not hidden; the owner's burn-down pace is a ceiling in the baseline's header, and the ratchet enforces it.
- **The other spelling sets are written here, in the same form,** one set per language, so the ratchets of validity.md § The refused shapes read one list beside the door list. Each refused-shape set carries a planted-violation corpus — three syntactic forms per language — that the `bite` check requires watched red, because a spelling set that matches nothing reads clean forever. The corpora, the lint-rule fixtures, the scratch-migration directory and the synthetic-log fixtures go into `exclusions.txt` as `scope`, so no check collects or scans a deliberate violation.
- **The rendered-from set** — the non-code assets the build loads (message and translation files, templates) — is read from the build configuration; where the build has no manifest, the design lists the paths as an owner ruling. Law 12's oracle check refuses an expected value read from it.
- **Up to ten fixtures, chosen for this project.** Environment edge cases are where software breaks on someone else's machine. A host tool breaks on a read-only home, a symlinked config dir or a case-folding filesystem. A service breaks on a webhook delivered twice, clock skew, a half-applied migration or a suspended tenant. Both kinds break on an expired credential or two concurrent writers. An operating-system sweep pays for this coverage with whole machines; a fixture function buys it at unit speed, and every package can reuse it. The candidate lists are menus, and the design picks the ones that bite this project — a fixture for a behaviour the product does not have is a beat for an absent feature (Law 9).
- **The one admitted skip.** A fixture for a condition the environment makes physically unobservable — a permission denial under a superuser, a case-folding check on a case-sensitive filesystem — carries `skip-because:<reason>` with a reason from the closed list in `skip-reasons.txt`, an owner-owned path, is reported by name and counted apart from passes; a silent pass there would be a coincidence, not evidence. Nothing else skips: an unreachable dependency, a missing credential, a slow test or a flaky one is never a skip — it is an `ERROR` or a defect (Law 15).

### 4. Research

Two cited passes run in parallel with steps 1–3, because their inputs are independent. One reads the 8–12 nearest neighbour projects; the other reads the literature.

- **Neighbours' CI configurations, testing docs and regression post-mortems are evidence** of what held and what broke under the same runtime problem. A design from first principles repeats their mistakes.
- **The literature pass includes auditing the suite itself.** Diff-based mutation testing and production probers answer whether the tests still test anything.
- **Every adopted finding becomes a decision with its source,** in a Prior art section per tier. A decision with a source can be reviewed; a decision from memory is a fluent guess.
- **A source that could not be fetched is named unverified,** never summarised from memory.
- **A question the sources cannot answer for this machine or this codebase is marked `measure here`** and becomes a measurement task. No published source quantifies a parallelism sweet spot or a suite-time ratchet, so both are measured on the project's own machine, never assumed — and never turned into a gate on the machine's load, which is a gate nobody can satisfy.
- **Without a fetch tool, the step reports `RESEARCH-NOT-RUN — no fetch tool`.** § Seed evidence stands in, and every pattern adopted from it is marked unverified. The missing research is visible, never absent.

### 5. Shared state

The step lists every store that features meet in, with its writers, its readers, and what can corrupt it. Crossings are cut from this list: the writers and readers are the raw material of step 7, and "what can corrupt it" names the beats. State a third party holds (a payment sandbox, an identity tenant, a bucket) is on the list because it outlives the run and must be stamped with the run id and cleaned by it. Every store carries its correlation column, or `none`: a store with none can never be asserted by delta across concurrent children (Law 6). The Tiers section names which tiers' tests share a store, because Law 10 binds every test of such a tier, not only beats.

### 6. Lanes

- **Lanes are cut by actor journey, never by module.** A lane per module repeats the unit tier at live-tier cost. The standard set is one lane per primary role's core journey, one per integration or protocol surface, one for tenant or admin operations, one for onboarding and upgrade, and one for operations and the destructive tail.
- **Five to nine lanes.** With fewer, a lane becomes a long monolith that is expensive to run alone. With more, each lane is too thin to build the state the sequence exists for. The count is a heuristic; the observed-coverage gate, not the lane count, guarantees coverage, so past nine the thinnest integration lane folds into the journey that uses it.
- **Every destructive operation gets a refusal or no-op beat beside its success path.** The dangerous paths of an uninstall, a delete, a purge or a migration rollback are the foreign file it must keep and the second run that must change nothing. A destructive operation tested only when it succeeds is tested where it is least dangerous. A guard with an allow and a deny outcome is asserted in both directions (Law 13).
- **Lanes go depth-first and carry state forward.** The record created in one beat, edited in a later one and exported in a later one still is one entity moving through its lifecycle, as a user sees it. A fresh fixture per beat would only retest the units.
- **Every read is scoped to the carry** (Law 10). Shared identities are the root's, read from the carry, never asserted as the beat's own.
- **Each lane's position carries a written reason.** Order is a design decision (Law 8), and an unexplained order gets reshuffled by the next maintainer.
- **Every lane gets a `need` prelude** (Law 7), and joins the sequence the day it is written.
- **Every beat row carries `assert:` with its `source:`, `breaks-if:`, `live-because:`, `cost_ms:` and `spends:`.** The source is where the expected value comes from — a `references.tsv` row for a ruled id, `drift:<artifact>` for the agreement of two product artifacts, a fixture, a golden or a literal elsewhere (Law 12); the bite names the product change that turns the row red, one statement inside the guarded code (Law 13); `live-because:` names what no lower tier can observe (Law 3); `cost_ms:` is the paper estimate `budgets` sums against the Speed target (Law 20); declared spend prices a run before it runs. A row whose bite cannot be named has no purpose; a row whose `live-because:` is "another input" is a unit case.
- **The row shapes,** in `beats.md`: a beat or hermetic row is `<beat id | tier-hermetic:<test>> · do:<the action> · assert:<the literal result and the report, store or screen it is read from> · source:<ref_id | drift:<artifact> | fixture | golden | literal> · breaks-if:<the product change> · live-because:<the observation> · cost_ms:<estimate> · spends:<quota, paid calls, licensed accounts | none> · <landscape ids>`; a unit row is `tier-unit:<test> · source:<…> · breaks-if:<the product change> · <landscape ids>`, because prose action and cost fields would only restate what the unit test already is.

### 7. Crossings

For each store from step 5, every writer–reader pair that lives in different lanes is a candidate. The design keeps the pairs where a regression would otherwise hide in one lane's blind spot.

The probe list names the classes that recur:

- one entity touched by two roles;
- an operation landing while another actor is mid-flow;
- an expired or absent credential seen at two surfaces (absence versus error);
- a config change under live sessions;
- two writers under two load shapes;
- a dependency restart with work in flight;
- a delete while something still references the target.

A probe becomes a beat only when the landscape carries the capability it probes (Law 9): a product without leader election gets no leader-election beat, and a product that does not promise durability across a restart gets no beat named for it. Each crossing is recorded as `crossing · lanes · why twice`. The `why twice` column forces the designer to name the second side; a crossing with no nameable second side is not a crossing. The after-effect beats of Law 9 come from the same table, and each crossing's landscape ids map to both beats with `crossing:` in the reason (Law 28).

### 8. Map and observed coverage

`map.tsv` maps every landscape id to a mapped row: header `landscape_id⇥row⇥reason`, the row column holding a beat id or a `tier-unit:` / `tier-hermetic:` row, the reason column empty or `crossing:`, `twin:<row>` or `live-only`, a `crossing:` row carrying its `twin:<row>` or `live-only` beside it. The map is the plan; the run ledger is the proof (Law 14). The gate has two halves:

| Half | Runs | Fails on | Closes |
| --- | --- | --- | --- |
| the plan | every commit, from the first | a landscape id with no map row, or a map row with no landscape id; a mapped row no lane file, no test file and no `pending.txt` line defines, or a `beats.md` row with no map row; an operation the built product reports that no landscape `op:` carries, or a landscape `op:` the built product does not report; an id mapped twice at one tier without `crossing:`, more than one success or one refusal row per operation at a tier above the unit tier besides `crossing:` rows, an id whose `crossing:` rows all lie in one lane, or a live row with no `twin:` and no `live-only`; a landscape row inside the exclusions | an untested capability on paper; a map that lies in either direction; a capability that shipped without being inventoried, or was invented; a duplicate, a mechanism booted per variation, or a missing twin; the toolchain as product |
| the proof | at close, over the canonical ledger of the merge subject | `CLAIMED-NOT-INVOKED`: a row mapped to an id with no operation record carrying that row's injected id, and for a unit row no coverage context reaching the capability's file; `UNOBSERVED`: an id no row invoked; `NO-ASSERTION`: a mapped id with no `assert` naming it in that row; for a tier row also `TIER-UNCOLLECTED`, `TIER-NOT-RUN`, `TIER-NO-BITE` | coverage credited by declaration, at any tier |

- **The operations are derived from the built product** by dumping its router, its job registry, its tool list, the help tree of a CLI product, a library's exported interface. The gate therefore does not depend on anyone remembering to update the landscape, it never reads a package manifest, and — because the join runs both ways — a landscape row nobody can invoke is red too.
- **A tier row is a mapped row like any other:** it sits in `beats.md` with its `source:` and `breaks-if:`, so the oracle and bite checks read it, and its ledger row comes from the unit or hermetic runner: the operations its records carried, and for a unit row its coverage context. A test named in the map proves nothing by being named.
- **Credit follows the injected id.** The harness injects the row's id into every request and process environment it starts, and the product carries it through its queues and jobs as a correlation field; an operation record without this row's id — a background consumer, a scheduled job, a sibling's traffic — credits nothing. A time-driven job is therefore reached through an operator entry point or a unit row (step 1).
- **`map-dup` also prints `DUPLICATE-CANDIDATES`** — tests at one tier with equal operation sets and equal expected literals, read from the tier ledgers — as the reviewer's list, without failing on them.
- **`pending.txt` lets the map be complete before the lanes exist:** one mapped row id per line. The plan half runs from the first commit, and the list shrinks to empty by close.
- **A new capability lands with its landscape row, its map row and its mapped row in one commit.** Otherwise the gate stays red for every commit in between, or the capability ships unmapped.

### 9. Activity log

Laws 11, 14, 15 and 17 read one log, and this step designs it for every product that has a runtime; a library or a UI component suite with none marks the section `none — <reason>` and proves its unit rows by coverage context (Law 14).

- **One JSON-lines stream per environment root,** resolved through the path façade, so a test, a lane run and production never share one. A product made of several processes writes one record shape to one destination the harness can read: a shared file, or the aggregator queried by a run id the harness injects.
- **The record:** `ts level msg op pid version`, the ids of the entities in play, `dur_ms`, `err`, and the `beat` and `run` the harness injects and the product propagates. `op` is what observed coverage joins on.
- **Wrap the seams, not the call sites.** The seams from step 3 pay a second time here: the real runner, client and clock each get one wrapper, so a few choke points cover every door instead of hundreds of call sites.
- **A signature ratchet on direct prints and ad-hoc loggers** stops a second logging path from growing back.
- **The unit tier asserts on log records through a buffer handler,** which makes the log a tested product surface rather than a debugging afterthought, and the runner writes each test's `op` set to the unit ledger.
- **The redaction test and the level per environment are named here,** with the rotation rule and the reader (a filter by time, level, entity, operation and beat).

### 10. Speed

The step prices the suite before the build (Law 20): the target wall for the full sequence, for each lane and for each tier; the `cost_ms:` per beat — actor provisioning, process boots, waits, third-party round trips — summed per lane and along the sequence's critical path by the `budgets` check; the scheduling, including whether a declared parallel mode exists and its isolation table (Law 6); the per-change run policy — the lanes touching the changed files from their checkpoints, then the touched lane alone, the full sequence at each close and at release; and the runner's wall breakdown. A sequence whose paper cost exceeds the target is re-cut here, before a lane exists, because after the build the only lever left is deleting beats.

### 11. Harness contract

The harness is written in whatever drives the real system best. The contract, which is its verbs, line formats, ledger and exit codes, holds in any language.

- **One beat library, sourced by every lane, demo and smoke script.** Verbs: `beat <id> <landscape-ids…>` · `assert <landscape-id> <what> <expected> <source>` · `golden <path under the reviewed goldens directory>` · `pass` · `fail <why>` · `known <landscape-id>` · `blocked <by>` · `skip-because <reason from skip-reasons.txt>` · `need <name> <check> <make>` · `spends <resource>` · `expect-log <pattern>` · `poll <name> <condition> <timeout>`. A demo becomes a subset view, never a second copy that drifts, and a lane file never re-implements a library helper. `assert` names the landscape id it reads, so the ledger can count assertions per id; `golden` compares against the tracked blob and refuses bytes that differ. A snapshot matcher is a golden: its store sits under the goldens directory, the blocking command runs with snapshot writing disabled, and a write under the goldens directory from a test or lane file, or a snapshot-update flag in a committed runner configuration or CI step, is a refused shape.
- **The log position is recorded before each beat, and the slice after it is scanned.** That attributes every error record to the beat that caused it. Coverage is read only from records carrying the beat's injected id (step 8). The library is the only log reader (Law 16), it reads a child process's output only after the child exited or flushed, and it keys every reply by beat id so a straggler from a timed-out beat can never satisfy the next.
- **The runner:** `run [--lanes …] [--from checkpoint:<lane>] [--root reuse|rebuild] [--dry-run]`. One environment, canonical lane order whatever order was given; it keeps going after a red lane (Law 7); it streams `✓` · `✗` · `known` · `blocked-by <beat>` · `skip-because <reason>` lines with the lane prefix; `--dry-run` prints the lanes and beats in run order and exits 0, so a run can be read and priced before it spends anything. A concurrency option, where the design declares a parallel mode, labels the ledger `mode: parallel:<n>`, which is never canonical.
- **The run ledger:** a header with `command:` (the exact invocation), `mode: sequence | checkpoint:<lane> | solo | parallel:<n> | tier`, the lanes that ran before, `boot: process | in-process | none` (Law 4), `runner_pid`, `order: canonical | shuffled:<seed>` (Law 21), `config: base | perturbed` (Law 29), the root hash, the `tree` and `dirty` hashes taken at start and again at end (Law 22), the start time and the load; then one row per mapped row and attempt, so a retried failure stays on record — `row · verdict · assertions-by-id · expected · ops · goldens · wall_ms · t+s`, `expected` holding the row's expected literals — and a per-lane table `lane · wall_s · beats · failed · known · blocked · skipped`. A `sequence` ledger whose rows' `t+s` intervals overlap across lanes, or a `boot: process` ledger whose product records carry its `runner_pid`, is refused by `tree`, because the ledger disproves its own header. Exit 1 on a failure outside the known-gap list, an unmapped id or a budget breach, each named; exit 2 when the runner could not run. Every tier's runner, the lint step and the checks script write the same header, with `mode: tier` below the live tier whatever the worker count and `boot: none` outside the hermetic and live tiers; the unit and hermetic rows are per test, with the ops read from the buffer handler's records and the coverage context at unit and from the test's log slice at hermetic; the lint step's and the script's rows are per rule, check or refused shape, so a bite on any of them names its red run.
- **The failure block.** Before the verdict line, one block per failed row: the row id, the file and line, the assertion's expected and actual values with its source, the first error record in the row's window, and the absolute paths of the run directory, the ledger and the log. Any output filter takes its verdict from the exit code and the ledger, never from a keyword, never from a tail, and its fixture tests (a green run, a red run, a long red run, a killed run) assert that the failure block's fields survive byte-for-byte.
- **Checkpoints:** after each lane of a sequence run the environment is snapshotted (container image, database template, VM snapshot) and keyed by the root hash and the lane files up to that lane; `--from checkpoint:<lane>` restores it. Third-party state is not in a checkpoint; the prelude recreates it.
- **The root is snapshotted the same way,** keyed by a hash of everything that shapes it: source, migrations, seeds, templates. An equal hash reuses the snapshot, so a changed input can never be served a stale root. A root that holds secrets is never pushed to a shared registry. A store dump or snapshot is restored only by the root build or inside the scratch-migration directory (Law 26).
- **Each run is isolated:** its own home, config root, database or schema, and ports, derived from the run root; the runner builds the product's environment only from the run root and the `need` values, so an ambient key never reaches the product (Law 29). A test run that can reach the operator's real environment is the most dangerous test there is. Third-party state is stamped with the run id, so the final lane deletes exactly what the run created. A declared parallel mode carries its per-child namespaces and its collision self-test (Law 6).
- **Credentials are injected at run time** from a keychain or a secret store scoped to the live-tier job. They are never committed and never present in a commit-gate job (Law 1). Time-derived credentials come only from the clock-seam or tolerance-window helper (Law 16).
- **Ledgers beside the design document,** tab-separated, each with a red state named in § The checks: `gaps.tsv` (`row⇥landscape_id⇥why⇥owner⇥expires`), `flakes.tsv`, the runner's owned record of each flake (`row⇥tree⇥dirty⇥root⇥first_seen⇥owner⇥expires`, Law 21), `bites.tsv` (`row⇥file_line⇥diff_hash⇥red_run_id`, the diff itself kept under `runs/` by that hash, two rows for a `drift:` row, Law 13), `budgets.tsv` (`key⇥budget_ms⇥run_ids⇥load`, the key a lane, `tier-unit` or `tier-hermetic`, Law 20), `ruled-copy.tsv` (`key⇥locale⇥literal⇥ruling`, the ruling a `references.tsv` ref_id, or the single line `none`, Law 18), `retired.tsv` (Law 24), `skip-reasons.txt`, `unobservable.txt` (one class of value no test can know before the run per line — a generated id, a timestamp, an empty body — Law 12), `self-tests.txt` (`subject⇥case⇥test`, one self-test per line: the subject a check, scanner, filter, lint rule, `runner`, `beat-library` or the component a law's self-test covers, the case an input class, `planted` or `law`), `exclusions.txt`, `references.tsv`, `roster.txt` and `runs/`. The `hash`-tagged paths are excluded from the subject, so writing a ledger never dirties the tree it describes; the ruling-bearing ones are read as § The threat model reads them.
- **The harness has self-tests, each watched failing before its code exists,** listed in `self-tests.txt` so `self-tests` can require each in the census: the commit-gate job refusing an outbound socket (Law 1); each mock failing an unscripted request by name, and each fake's contract suite run against fake and real (Law 2); the single-instance assertion and the two-mode probe of an in-process boot (Law 4); the collision test of a parallel mode or of a store-sharing tier run concurrently (Law 6); the keep-going run — a planted red in the first lane, the second lane's own verdicts in the ledger, exit 1 (Law 7); the redaction test (Law 17); one hermetic boot with every required configuration key blank, asserting the refusal names every key (Law 30); the beat library refusing a live child's output and a mismatched reply (Law 16); the failure block of a planted red carrying `file:line`, expected, actual and absolute paths; and for every check, scanner, filter and project-authored lint rule one could-not-look self-test per input class and one planted-violation self-test (Law 15).

### 12. Owner review

The trade-offs belong to the owner:

- a real dependency versus a mock, per tier;
- shared versus isolated state, and whether a parallel mode exists;
- the known-gap policy and the owner roster;
- the ruled ids, the ruled-copy keys (or `none`), the skip reasons and the unobservable-value classes;
- the ratified references in `references.tsv`, and the rendered-from paths where the build has no manifest;
- the burn-down pace of every ratchet baseline;
- the logging framework;
- which LLM and which reasoning setting run the release rehearsal;
- the speed targets and which numbers get measured here;
- which gate runs the sequence.

The designer presents each research finding and draft decision in one plain line, and every ruling moves from Open rulings into the law or section it changes. With no owner present, every item stays under Open rulings, recommendation first, and the document is marked `DRAFT — unruled`. A trade-off the designer settles silently is a decision nobody made.

## The output

The design document goes to the path the request names. With none named, it goes to `<the project's design-doc directory, else docs/design>/<target>-integration-suite/design.md`. Beside it the command writes `landscape.md`, `retired.tsv`, `beats.md`, `map.tsv`, `pending.txt`, `references.tsv`, `roster.txt`, `skip-reasons.txt`, `unobservable.txt`, `self-tests.txt`, `exclusions.txt`, `ruled-copy.tsv` with the owner's keys or `none`, the empty `gaps.tsv`, `flakes.tsv`, `bites.tsv` and `budgets.tsv`, `scripts/suite-checks.sh` adapted from the skeleton, and under the checks directory the spelling sets, their planted-violation corpora, `third-party-rules.txt` and `name-allowlist.txt`. It edits no code and no test.

- **The design is reviewed before anything is built.** Unmapped capabilities, crossings asserted from one side only, rows with no nameable bite, oracles with no source, and checks whose broken state would read as PASS are cheapest to catch on paper.
- **The skeleton files are the build's inputs, already in their machine-read shapes,** so the build starts with a gate that runs.

The fifteen sections run from what the tiers are to what the owner still has to decide. Tiers come first because every later section depends on what each tier fakes. Not covered and Open rulings come last because they collect what every earlier section could not settle:

1. Tiers — what each asserts, what each fakes, which gate it guards, the exact blocking command per tier (Law 23), the boot mode (Law 4), which tiers share a store (Law 10), the settings and test-configuration paths that trigger the double-run (Law 29), the scheduled leg and the schedule window, which also bounds the close cadence (Laws 21, 23, 29)
2. Landscape summary — counts per area, the `NONE` count, the exclusions applied, the ruled rows
3. Existing tests — the retirement ledger's row count and the dispositions expected
4. Doors and seams — the seam plan and the fakes directory, the fixtures, the bare-door baseline, the spelling sets, the rendered-from set, `exclusions.txt`
5. Prior art — adopted, avoided, `measure here`, each with its source
6. Shared state — stores, writers, readers, correlation columns
7. Lanes — order with reasons, `beats.md`, the `need` preludes
8. Crossings — the table and the after-effect beats
9. Map and observed coverage — `map.tsv`, the derivation commands, `pending.txt`
10. Activity log — destinations, levels per environment, record fields, the buffer handler, the redaction test
11. Speed — targets per tier and, with a live tier, per sequence and lane, the budget headroom, the paper cost, the critical path, the per-change policy, the parallel mode if any
12. Harness contract — library, runner, ledger, checkpoints, root hash inputs, the ledger files, `self-tests.txt`
13. Build order — below
14. Not covered — every surface no tier reaches (a platform with no live tier, a flow needing absent hardware), named with its reason
15. Open rulings — every number or trade-off the owner decides

**A section or a tier marked `none — <reason>`** is read by every check before it errors on an absent input: a check whose input is that section reports `PASS none:<section>` when the marker exists and no artifact of that section exists in the tree — no lane runner declared and no file matching the lane-file pattern, no hermetic ledger — and `ERROR NONE-AMBIGUOUS` when the marker and an artifact both exist. With no live tier, sections 6, 7 and 8 read `none`, `beats.md` still holding every unit and hermetic row, and section 11 holds only the tier targets and the headroom; section 10 reads `none` only for a product with no runtime; every other section is produced in full.

Build order is sequenced by risk and by dependency:

1. **Run isolation and the ledger's tree binding first.** Nothing else is safe to build until a run cannot reach the real environment, and nothing is evidence until it names its tree.
2. **The collected census and the check self-tests,** so the gate that will run everything is proven to run it, and proven to go red when it cannot look and when it should.
3. **Wall-time budgets for the unit and hermetic tiers, before any conversion,** so the conversion's effect is measured, not assumed.
4. **Seams, fakes with their contract suites, and fixtures, then the packages converted in batches with disjoint file sets,** so parallel hands never edit one file.
5. **Activity log, then mocks,** because the beat verdict (Law 11), observed coverage (Law 14) and the hermetic twins (Law 3) need them.
6. **The harness, proven with the first lane in sequence and alone, before the other lanes are written.** The first lane is what debugs the harness.
7. **The second lane, then speed measured against the target** (Law 20) before a third lane is written.
8. **Each remaining lane joining the sequence the day it is written** (Law 7), with its after-effect beats, its bites recorded, its budget pinned from three runs.
9. **The retirement ledger verified, then the old suite deleted in that commit** (Law 24).
10. **Close:** three green sequences on the merge subject, budgets pinned from them, no flake on the merge subject, `pending.txt` empty, and the sequence wired into the release procedure.

With no live tier, steps 6–8 are replaced by the unit and hermetic conversions of step 4 with their bites recorded, and close is one green run of the blocking commands on the merge subject — three only in the landing that pins a tier budget.

## The checks

The command ships `scripts/suite-checks.sh`, a POSIX shell skeleton with its per-language spelling sets and planted-violation corpora, `third-party-rules.txt` (one relied-on third-party rule per line, with its law) and `name-allowlist.txt` under `scripts/suite-checks/`, that the design adapts to the tree: the id patterns, the derivation commands, the test-name and lane-file patterns, the ledger paths. It runs in two modes, a plain check and `--close`. Each check prints one line, `CHECK <name> PASS|FAIL|ERROR <detail>`. The script exits `0` when everything is clean, `1` on a finding, and `2` when a check could not run. This is the three-state protocol of `/quality:llm-codebase` § Protocol. Every ratchet reads its baseline — a multiset of signatures, never a count — as § The threat model reads every ruling-bearing input: a signature found more often than the baseline holds it is red even when the total fell, so a move keeps its count and a copy raises it; the baseline only shrinks; and its header carries the owner's burn-down ceiling, a signature count per date, so a baseline above today's ceiling fails and a header without one is the ratchet's `ERROR`. A design value a check reads — a Speed target, the headroom, the schedule window — missing is that check's `ERROR`. Every scope skips the `scope`-tagged paths of `exclusions.txt`.

| Check | Mode | Fails on | Errors on |
| --- | --- | --- | --- |
| `map-ids` | both | a landscape id with no map row; a map row whose id is not in the landscape | `DERIVE-FAILED`: no ids read from the landscape or the map |
| `map-beats` | both | a mapped row no lane file, no test file and no `pending.txt` line defines; a `beats.md` row with no map row; at `--close`, a row still in `pending.txt` | `DERIVE-FAILED`: no rows read; `pending.txt` missing |
| `map-names` | both | an operation the built product reports that no landscape `op:` carries; a landscape `op:` the built product does not report; an `evidence:` row whose `file:line` does not exist | `DERIVE-FAILED`: the registry dump or the `op:` fields came back empty |
| `map-dup` | both | an id mapped twice at one tier with no `crossing:`; more than one success row or one refusal row per operation at a tier above the unit tier, besides `crossing:` rows; an id whose `crossing:` rows all lie in one lane; a `twin:` naming a row at the same tier or no row; a live row with neither `twin:` nor `live-only` — and it prints `DUPLICATE-CANDIDATES` from the tier ledgers without failing | `DERIVE-FAILED`: no map rows |
| `landscape-scope` | both | a landscape row whose `op:` matches the exclusion spellings; a row without `surface:`, `kind:` or `ruled:`; `ruled:no` on a default-ruled surface without `ruling:<ref_id>` | the exclusion spellings empty |
| `names` | both | beyond its baseline, an id, a lane key, a test file stem or a test title matching the refused-name spellings, or a property word in a slug or title whose test body carries no violating action; a lane key starting `tier-` | no ids or no test files read; the refused-name or violating-action spellings empty; `name-allowlist.txt` unreadable |
| `oracle` | both | a mapped row without `source:`; a ruled row whose `source:` is not a `references.tsv` row; a hermetic or live row without `live-because:`; in a mapped row's test — every other test is the `refused-shapes` ratchet's — an expected side holding a name or call that resolves to a product module outside locator position (a constructor over literals alone is a literal), or a local name whose binding does, or an actual side that is a literal, a refused oracle spelling without `weak-oracle:` or with it on a ruled row, an expected-side string of three words or more equal to a rendered-from value that is not a `ruled-copy.tsv` key, or a literal count not derived from the seeded fixture; a policy-lint rule citing no `references.tsv` row | the refused-shape or locator spellings, `references.tsv` or the rendered-from set unreadable |
| `copy` | both | a `ruled-copy.tsv` key not pinned in exactly one test per locale | the registry unreadable, or empty without the line `none` |
| `refused-shapes` | both | a signature found more often than its baseline holds it, in the scope each row of validity.md § The refused shapes names | a spelling set empty; the tree, `skip-reasons.txt` or `unobservable.txt` unreadable; a shape with no planted-violation corpus; zero files scanned |
| `gaps` | both | an entry expired, without `expires:`, with a `why` lacking an external reference, naming an own identifier or a repository file, or an owner off the roster | `gaps.tsv`, `roster.txt` or the own-identifier list unreadable |
| `budgets` | both | plain: the paper cost (`cost_ms` summed per lane and along the critical path) above the Speed target, with unpinned keys listed; `--close`: `BUDGET-UNPINNED`; `run_ids` naming fewer than three green canonical runs on one root hash, or for a tier key on one subject; the recomputed maximum plus headroom above the pin; the slowest run over twice the median; the pinned sequence budget above the Speed target; a pin raised by more than the added rows' own maximum `wall_ms` in the named runs | `budgets.tsv` or a named run unreadable |
| `collected` | both | a file matching a test-name pattern anywhere in the repository, outside `exclusions.txt`, that the blocking run's log does not show collected, or shows twice; a `CHECK` line of this table absent from the blocking run's log — the plain checks in the commit job, and in plain mode no close-job log within the schedule window carrying every `--close` line; a fake in the fakes directory with no contract suite collected against both implementations | the CI configuration or the blocking run's log unreadable; no CI job invokes `--close`; no test-name patterns |
| `self-tests` | both | a check, scanner, filter, project-authored lint rule, the runner or the beat library with no input-class line or no `planted` line in `self-tests.txt`; a line of `self-tests.txt` absent from the census or not passing on the merge subject; a declared parallel mode, or a store-sharing tier run with a concurrency option, without a passing collision test; a filter fixture (green, red, long red, killed) mis-judged or not preserving the failure block byte-for-byte | the census or `self-tests.txt` unreadable |
| `retired` | both | test files deleted since the merge base whose re-derived assertion signatures, minus those that reappear in files the same commit adds, are not set-equal to the ledger rows added since the merge base; a `successor:` that does not exist, did not pass in the named run on this subject, or whose signature differs in subject or in expected literal from the retired one; a `dropped:` whose shape spelling the retired assertion does not match; a `ruling:` not in `references.tsv` | the merge base unreadable; deletions with an empty ledger; the assertion spelling set empty |
| `env-twice` | both (`PASS not-due <run id>` between scheduled legs) | the unit tier's results differ between the base configuration and the configuration derived from the settings schema, or the seam's key set, with every key perturbed to a valid alternative and every flag toggled | a leg missing; the last scheduled leg older than the schedule window; the key set or the trigger paths unreadable |
| `bare-doors` | both | a door signature found more often than its baseline holds it | the door spellings empty; the tree unreadable; zero files scanned |
| `clones` | both | a clone in the test tree beyond its baseline, via the token-based clone detector this suite's checks script runs | the detector absent, or it matched no files |
| `size` | both | a test or lane file above the ceiling beyond the test tree's baseline | no `size-ceiling` declared in the design |
| `tree` | `--close` | the canonical ledger's subject hash differs from the merge subject; its dirty hash (tracked diff plus untracked, non-ignored files) is non-empty; its start and end hashes differ; `mode: parallel:<n>` offered as canonical, or a `sequence` ledger whose rows overlap across lanes; `boot: in-process` without the single-instance and two-mode tests passing on the merge subject, or `boot: process` with product records carrying its `runner_pid`; a lane without a green `sequence` ledger, or without a green `solo` ledger since its lane files last changed | no ledger for this subject |
| `observed` | `--close` | `CLAIMED-NOT-INVOKED`, `UNOBSERVED`, `NO-ASSERTION`; for a tier row `TIER-UNCOLLECTED`, `TIER-NOT-RUN`, `TIER-NO-BITE` | no canonical ledger; the `op:` fields empty; a tier ledger missing for a tier the design declares |
| `bite` | `--close` | a mapped row, a project-authored lint rule, a rule in `third-party-rules.txt`, a check or a refused shape with no bite row, or a `drift:` row with fewer than two; a bite row whose red run does not exist, does not show the row ✗, or whose dirty hash differs from `diff_hash`; a mutation that inserts an unconditional exit or removes more than one statement; a mutation outside the capability's file or the op's handler; a lint rule's red run whose `command:` is not the blocking command | `bites.tsv` or `third-party-rules.txt` unreadable; a named run or its diff unreadable |
| `flakes` | `--close` | a row the run ledgers show failing and passing on the merge subject with one dirty hash and root, recomputed and never read from `flakes.tsv`; no shuffled-order ledger for the unit or hermetic tier within the schedule window; a retry or rerun setting in a runner configuration outside tests named in `flakes.tsv` | the ledgers unreadable |

Twenty-two rows, and each pays for itself: it reads an artifact the design already produces, it names a state for "could not look", and removing it would let one refused shape or one lazy construction pass green.

- **`ERROR` is distinct from `FAIL` because a check that could not run is neither a finding nor a pass.** Every comparison in the script is a set difference, and an empty set leaves nothing unmapped, which would read as PASS. So each check first proves that both of its sets are non-empty, and each has a self-test per could-not-look input and a planted-violation self-test (Law 15).
- **The `--close` checks read the run ledger, not the map.** During the build, pending rows and unproven bites are the expected state; at close, they are unfinished work, and only a run can prove coverage.
- **A listed row that passes is the runner's red, not this script's.** Only a run can observe a pass.
- **A check that cannot be expressed as a script goes under Open rulings,** because a rule that is not scripted is not enforced.

## The threat model

The checks defend against accident, laziness and the pull toward green by an author who does not forge: a field left empty, a value guessed, an assertion written to pass, a check that could not look, a list grown in the commit that needs it. Forgery — a ruling, a reference or a run ledger written to deceive, and landed — is out of scope: no in-repository check can stop it, because the forger controls the file the check reads.

A ruling-bearing input is a file or a design-document value that a check trusts instead of verifying. Two lines hold them:

- **Owner-owned paths.** The design document, the checks script with its directory, and the ruling-bearing ledgers change only with the owner's review (a code-owners rule, or the version-control system's equivalent). This is the first line and never the only one: ownership is void where the author commits as the owner — a single maintainer, an agent fleet — which is the common case this design serves.
- **The merge base.** A check reads every ruling-bearing input at the merge base and in the working tree, and applies the stricter reading: the intersection of a list whose entries excuse, the union of a list whose entries oblige, the tighter of two numbers; an unreadable merge base is its `ERROR`. A loosening — an excusing entry added, an obliging entry removed, a number moved the lenient way — therefore takes effect from the next landing, never in the commit that relies on it; a tightening takes effect at once, so a fix still deletes its own gap in one commit, and a new obligation lands after the baseline entries that excuse what already violates it. The landing that installs an input reads it from the working tree, since the merge base holds nothing to compare.

| Read as | The ruling-bearing inputs |
| --- | --- |
| lists that excuse | `references.tsv`, `roster.txt`, `skip-reasons.txt`, `unobservable.txt`, `gaps.tsv`, `flakes.tsv` (its retry exemption), `exclusions.txt`, every signature baseline, `name-allowlist.txt`, the locator spellings, the design's `none` markers |
| lists that oblige | `self-tests.txt`, `ruled-copy.tsv`, `third-party-rules.txt`, the landscape's `ruled:yes` marks, every other spelling set and its planted-violation corpus, the test-name and lane-file patterns, the own-identifier list, and the design's blocking command per tier (a replaced one runs beside its successor for one landing), store-sharing tiers, rendered-from paths and double-run trigger paths |
| numbers | the design's Speed targets, budget headroom and schedule window, every baseline's burn-down ceiling, the size ceiling and the clone detector's threshold |

`budgets.tsv` and the flakes are not on the list, because neither is trusted: `budgets` recomputes every pin from the run ledgers it names, and `flakes` recomputes the flakes from the run ledgers on the merge subject.

What only a reading can judge is the suite reviewer's at every landing — in a project installed from this blueprint, the lander's; elsewhere, whoever the owner names — and a landing without it is unreviewed, not unchecked:

- Read every loosening of a ruling-bearing input, which takes effect at the next landing.
- Re-run a sample of the recorded bite mutations, and for each guard confirm both directions and the boundary plus and minus one.
- Read every ruled-row oracle against its cited `references.tsv` row, and every product change under a ruled row's `file:line` against the same.
- Read the landscape for the refusal row of every guard, with the product's error-code registry as the list where it has one, every new row's `surface:`, and every `evidence:` row's `file:line` for the code that writes the output.
- Read every policy-lint rule for a property asserted rather than a line's text pinned.
- Read every `twin:` and `live-only` reason, every new `weak-oracle:` and `skip-because:` against its class or reason, every subject's input-class lines in `self-tests.txt` against the inputs it reads, and the `DUPLICATE-CANDIDATES` list.

Four rules are prose by design and belong to no check: a lane joins the sequence the day it is written (the executor's), each lane's position carries a reason (an Open ruling until it does), every unmapped test is watched failing when written (the executor's), and the rehearsal's model and reasoning setting (the owner's).

## Seed evidence

The command points to [laws.md § Sources](laws.md#sources), the short list of published findings to start step 4 from. Each finding is re-fetched before it is cited. The list gives the research pass anchors, so it does not start blind, without letting a remembered claim pass as a cited one. When no fetch tool exists, the seed evidence stands in and is marked unverified. [laws.md § Sources](laws.md#sources) maps each source to the laws it supports.

## The hand-off

The design document is reviewed before anything is built. The laws reach every tier's tests through three carriers the design writes, never through a pointer to a file the project may not hold: the checks script, the skeleton ledgers, and the rows the project's testing manual must carry — under § Lanes and registries the per-change duty (the landscape row, the map row, the mapped row and its bite in the same commit; the sequence at every close where a live tier exists) and under § What not to test the refused shapes. A rule that only lives in a neighbour document is not in force.

In a project installed from this blueprint, the flights cast builds and keeps the suite:

- `flights-speccer`'s reconcile phase performs the review, and the build goes through `/flights:spec`.
- A flight executor builds the harness, the mocks and the lanes from the task files that Build order produces, and records each row's bite.
- `flights-lander` runs the blocking commands of the unit and hermetic tiers, sweeps the refused shapes and performs the § The threat model duties on every landing; where a live tier exists it also runs the touched lanes from their checkpoints while it fixes, the touched lane alone, and the sequence at the gate's open and at its close. The sequence at open gives a baseline, so a red at close can be attributed to the flight. A defect a test or a lane exposes is the lander's to fix.

Outside this blueprint, the owner assigns the review, the build and the runs. Law 7 is the run policy, Law 20 the per-change policy, and Laws 11–15 the verdict rules at every tier.

## Where it sits

| Neighbour | Owns | Shares with this command |
| --- | --- | --- |
| `/quality:llm-codebase` | the source-tree layout and its mechanical gates | the three-state protocol as it stands; this suite's `size`, `clones` and budget rows run in its own script |
| the project's testing manual | one project's test mechanics — where a test lives, run commands, environments, cleanup, concurrency, the local traps — and the per-change duty | this command carries the design law for every tier; the manual's § Lanes and registries carries the per-change duty and § What not to test the refused shapes, in the manual's own words for its stack |
| `qa-commons.md` | the generic rules every project's test gates share | nothing; its rules stand as they are and this command does not rely on them |

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The command | `templates/global/commands/quality/integration-suite.md` | Inputs, terms, names, the tier names unit / hermetic / live, laws, procedure, output, hand-off, seed evidence, checks, the threat model |
| Its neighbour | `templates/global/commands/quality/llm-codebase.md` | § Protocol, cited as it stands |
| The testing-manual template | `templates/project/commands/per-project/testing-manual.md` | § Tiers in the tier names unit / hermetic / live, § Lanes and registries, § What not to test |
| The testing-manual design | [`docs/design/flights/testing-manual.md`](../flights/testing-manual.md) | § What stays out names this command |
| Registries | `templates/refresh-map.json`, `docs/BLUEPRINT.md`, `docs/SETUP.md`, `docs/README.md` | The command's entry |
| This directory | `docs/design/integration-suite/` | How the command works and why its laws are shaped as they are |

## Open items

- **The testing-manual template's § Tiers** names its tiers Unit and Integration; § Surfaces that stay in sync holds it to the tier names unit / hermetic / live, which its § Lanes and registries and § What not to test already use.
