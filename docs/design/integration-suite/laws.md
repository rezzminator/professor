# The laws — tiers, shape, verdict

This file covers Laws 1–18 of [`/quality:integration-suite`](../../../templates/global/commands/quality/integration-suite.md), one section each: where each tier gates, the shape of the live suite, and what a verdict means. [validity.md](validity.md) covers Laws 19–31: how the suite stays honest over time, and the shapes a test may never take. Every section gives the rule, the failure it prevents, why it takes this shape and not a nearby one, and how it is checked; a section with published evidence names it. The laws hold for any project in any language; the examples are the kinds of systems the command designs for, not any one codebase.

## Contents

- [How the laws fit together](#how-the-laws-fit-together)
- [Law 1 — the commit gate is hermetic](#law-1--the-commit-gate-is-hermetic)
- [Law 2 — one scripted mock per dependency family, and every fake passes the real contract](#law-2--one-scripted-mock-per-dependency-family-and-every-fake-passes-the-real-contract)
- [Law 3 — each tier asserts only what the tier below cannot observe](#law-3--each-tier-asserts-only-what-the-tier-below-cannot-observe)
- [Law 4 — the hermetic and live tiers run the product as production runs it](#law-4--the-hermetic-and-live-tiers-run-the-product-as-production-runs-it)
- [Law 5 — the release rehearsal runs on a weaker model](#law-5--the-release-rehearsal-runs-on-a-weaker-model)
- [Law 6 — one environment, lanes in sequence](#law-6--one-environment-lanes-in-sequence)
- [Law 7 — every lane is proven on the sequence's state and alone](#law-7--every-lane-is-proven-on-the-sequences-state-and-alone)
- [Law 8 — builders, readers, destroyers](#law-8--builders-readers-destroyers)
- [Law 9 — every crossing asserted twice](#law-9--every-crossing-asserted-twice)
- [Law 10 — every read is scoped to what the test owns](#law-10--every-read-is-scoped-to-what-the-test-owns)
- [Law 11 — the beat verdict](#law-11--the-beat-verdict)
- [Law 12 — the oracle is independent and ratified](#law-12--the-oracle-is-independent-and-ratified)
- [Law 13 — every test is watched biting](#law-13--every-test-is-watched-biting)
- [Law 14 — coverage is observed, never declared](#law-14--coverage-is-observed-never-declared)
- [Law 15 — every check names its broken state](#law-15--every-check-names-its-broken-state)
- [Law 16 — observation neither disturbs the system nor waits on the clock](#law-16--observation-neither-disturbs-the-system-nor-waits-on-the-clock)
- [Law 17 — one activity log per environment root](#law-17--one-activity-log-per-environment-root)
- [Law 18 — meaning-bearing output is pinned from an independent source](#law-18--meaning-bearing-output-is-pinned-from-an-independent-source)
- [Sources](#sources)

## How the laws fit together

The laws serve the six ideas in [integration-suite.md § The six ideas](integration-suite.md#the-six-ideas), and they group into five concerns:

| Concern | Laws | The question the group answers |
| --- | --- | --- |
| Where each tier gates | 1–5 | What may run on every commit, how the fast tiers stay faithful to the real dependencies, and what each tier is for |
| The shape of the live suite | 6–10 | How one run reproduces production's accumulated state and still stays debuggable |
| What a verdict means | 11–18 | When a test is green, where its expected value comes from, and how a red, an absence or a broken check reports itself |
| Keeping it honest over time | 19–24 ([validity.md](validity.md)) | How the ledgers, the budgets and the gates stay true after the designers have left |
| What a test may not be | 25–31 ([validity.md](validity.md)) | The shapes that look like tests and test nothing, refused by rule |

Three tensions run through the whole set:

- **Realism against speed.** The live tier is slow. Laws 1, 3, 7 and 20 keep it off the commit path and inside a budget, and Law 2 moves as much of its logic as possible into the fast tiers.
- **Accumulated state against reproducibility.** Law 6 deliberately lets state pile up. Laws 7, 8, 10 and 15 keep every red row replayable and attributable anyway.
- **A test written to pass against a test written to fail.** Whoever writes a test — a person under deadline or a model asked for green — is pulled toward assertions that cannot go red. Laws 12, 13, 14 and 15 make the pull visible: an oracle needs a source, a test needs a witnessed failure, coverage needs an observed invocation under the test's own id, and a check needs a named broken state. What the checks cannot see, and what only the owner's review can hold, is stated once in [integration-suite.md § The threat model](integration-suite.md#the-threat-model).

## Law 1 — the commit gate is hermetic

**Rule:** The unit and hermetic tiers gate every commit. The live tier gates every release and the close of each multi-commit piece of work. The commit gate holds no credential and calls no paid or live dependency. Where the platform allows, it runs with outbound network denied.

**Prevents.** Three failures of a commit gate:

- **A gate that goes red for reasons unrelated to the change:** a third party's outage, a rate limit, a model's variance. People learn to rerun such a gate or route around it, and from then on it gates nothing.
- **A gate that holds credentials.** Every contributor's job and every fork then carries a secret.
- **A mock that silently isn't one.** A unit test that happens to reach the network passes on a connected machine and hides a dependency nobody declared.

**Why this shape.** The split follows cost against feedback latency. The commit gate must be fast and reliable, because it runs most often and it blocks the author. The live tier can tolerate latency, because the work has already landed when it runs. The live tier also gates the close of each multi-commit piece of work, not only the release, so a crossing defect surfaces at the close of the work that caused it, not weeks later in a release that bundles many merges. Network denial turns the seam law of the Doors step from a promise into a check: an unseamed door fails by name instead of reaching the real thing.

**Checked by.** The socket-refusal self-test of the commit-gate job is a line of `self-tests.txt`, so `self-tests` requires it in the census and passing on the merge subject; `collected` proves the gate runs what is on disk.

**Evidence.** *Software Engineering at Google* ch. 23 stages the chain as a fast, hermetic presubmit, then postsubmit, then a release candidate at rising fidelity. Bazel defines hermeticity so that touching an undeclared dependency fails loudly.

## Law 2 — one scripted mock per dependency family, and every fake passes the real contract

**Rule:** Each external dependency family gets one scripted mock, which speaks the real protocol from a scenario file. The mock's shapes are golden files captured from the real dependency during the live tier, and a capture that differs from the golden is a red beat. A request the scenario does not script fails the test by name. Every fake of an internal component — a store, a queue, a registry, a renderer — lives in the fakes directory and passes the same contract cases as the real implementation, run against both; a fake may not return a state the real component cannot produce, and it must be able to produce every state a test needs. A test never replaces a product module in place with an inline stand-in: that is a fake with no contract.

**Prevents.** Five failures:

- **Mock proliferation.** Each hand-written fake imitates a different, partial version of the dependency, and each drifts its own way. One mock per family keeps the protocol knowledge in one place.
- **A mock of a dependency that no longer exists.** The real API moves on while the fake keeps answering the old way, so the hermetic tier stays green against a protocol nobody speaks any more.
- **The silent default.** An unscripted request answered with an empty or default value hides every new call the product starts making.
- **An impossible state pinned as behaviour.** A fake that returns null where the real component throws, or an empty registry where the real one is never empty, lets a test pass against a branch production can never take, and a fake that cannot emit an invalid frame or persist a row leaves the branch that handles it untested. An inline replacement of a product module — a registry answering empty, a renderer reduced to its props — is the same fake without even a file to hold it to a contract.
- **A fake that drifts from the real thing under refactoring.** Without a contract suite that runs against both, the fake's behaviour is whatever its last editor remembered.

**Why this shape.**

- **Real protocol, not a stub.** The mock speaks the real protocol (a process, an HTTP server) rather than being a function stub, because the hermetic tier runs the real binary. The product's own client code has to be exercised.
- **Golden shapes captured during the live tier.** The real dependency is being exercised there anyway, so the live tier audits the mock at no extra cost. This also answers the question the record-and-replay literature leaves open: how to detect that a recording has gone stale.
- **One contract suite for the fake and the real thing, and one directory for the fakes.** The same cases run against both, so the fake is proven equivalent on every state a test relies on, and a new state the real component gains is a red fake until the fake learns it. The directory is what lets a check enumerate the fakes. The inline refusal covers every product module, because which modules hold a state a stand-in can misrepresent is a judgement no check can make, and the stand-ins that exist are grandfathered by the ratchet's baseline.

**Checked by.** `collected`: every fake in the fakes directory has a contract suite collected against both implementations; `self-tests`: each mock's unscripted-request self-test is a line of `self-tests.txt`; `refused-shapes`: the language's module-mocking spelling applied to a product module.

**Evidence.** The record-and-replay pattern for external APIs is to record once, replay deterministically, and fail on anything unrecorded. Every source that describes it leaves staleness detection unexplained.

## Law 3 — each tier asserts only what the tier below cannot observe

**Rule:** A live beat asserts only what the real dependency alone can show; every product-side assertion in it has a twin at the lowest tier that can observe it, named in the map (`twin:<row>`), or the live row is marked `live-only`. A hermetic row exists only for what a unit test cannot observe: a process boundary, real input and output, a boot path, the assembled artifact. One row per mechanism — at each tier above the unit tier, one success row and one refusal row per operation besides Law 9's crossing rows; its input variations are table-driven unit cases. Every hermetic and live row states `live-because:` — the observation no lower tier can make.

**Prevents.** Two failures:

- **Product logic gated only at release.** The live tier runs rarely, so without a twin the product-side logic of a live beat is checked only when the live tier runs.
- **Slow tiers spent on cheap proofs.** A fresh process boot, or a wait on a queue's visibility window, per input variation of a pure function or a guard buys in minutes what a unit test buys in milliseconds — whether the variations share one landscape id or are inventoried as five refusal rows of one operation. The suite grows slow without growing more certain, and the slow beats carry the most flake surface.

**Why this shape.** Each tier costs more than the one below; the rule spends each tier only on what it alone can see. The `live-because:` field makes the designer name that observation, so a row whose reason is "another input" has nowhere to hide, and the one-success-one-refusal bound per operation is what a check can count where "same reason" is a judgement. The twin is at the lowest tier that can observe the assertion, not always the hermetic one, because a twin above the cheapest observing tier is itself a slow tier spent on a cheap proof. `live-only` exists so a beat whose whole point is the real dependency does not have to invent a twin.

**Checked by.** `oracle`: a hermetic or live row without `live-because:`; `map-dup`: more than one success or one refusal row per operation at a tier above the unit tier besides `crossing:` rows, a `twin:` at the same tier or naming no row, a live row with neither `twin:` nor `live-only`.

## Law 4 — the hermetic and live tiers run the product as production runs it

**Rule:** The hermetic and live tiers boot the production entry point from the build output as a separate process, with production's module resolution and configuration path, and the ledger header records `boot: process`. An in-process boot inside the test runner records `boot: in-process` and is admitted only with a self-test that asserts single instances of every stateful library the product loads, and one behaviour probe whose result is identical in both modes, both passing on the merge subject.

**Prevents.** A product that behaves differently under test than in production, with tests written to the difference. A test runner's module transform can load one library twice, so a value created by one copy is rejected by the other; a runtime flag or an instrumentation hook can turn a controlled error into a crash. Beats are then shaped to expect the artifact — an audit row missing, an error answered as a server fault — and production changes are proposed to "fix" what only the test runtime does.

**Why this shape.** The cheapest proof that the product runs as shipped is to run what ships. The escape clause exists because some systems can only be driven in-process; it costs a self-test that names the exact way in-process boot can diverge, so the divergence is caught the day it appears rather than read out of a red beat months later. Recording the boot mode in the header is what lets a check tie the ledger to the self-tests, and the product records' `pid` is what lets the check disprove the header.

**Checked by.** `tree`: a canonical ledger with `boot: in-process` is admitted only when the single-instance and two-mode tests passed on the merge subject, and a `boot: process` ledger whose product records carry its `runner_pid` is refused; `self-tests`: both tests are lines of `self-tests.txt`.

## Law 5 — the release rehearsal runs on a weaker model

**Rule:** Where the product includes LLM-driven steps, the release rehearsal runs on a weaker, non-frontier LLM at its highest reasoning setting. Reaching the asserted end state there proves the instructions; a frontier pass proves less.

**Prevents.** Shipping instructions that only a frontier model can follow. A product with LLM-driven steps ships instructions (prompts, templates, generated guidance) that other people's models will execute, and those people run a range of models. A frontier model makes up for vague instructions by inferring the intent. When it passes, that shows the model is good, not that the instructions are.

**Why this shape.** A test is worth only as much as its sensitivity: the instrument has to fail when the thing under test is bad. Here the thing under test is the instructions, so the instrument is the model least able to compensate for them.

- **The highest reasoning setting.** A failure then means the instructions are unclear, not that the model was starved of effort.
- **The pass is the asserted end state, never the model's claim** (Law 11).
- **It runs at release, not per commit,** because it spends real model turns and credentials (Law 1).

**Checked by.** The rehearsal's ledger header names the model and the reasoning setting under `command:`; the owner's ruling on both sits under Open rulings until made.

## Law 6 — one environment, lanes in sequence

**Rule:** The live tier runs one environment per run, with lanes in sequence, and each lane inherits the state the earlier lanes built. Concurrency is a scripted beat (a storm, two writers), never a scheduling mode of the sequence. A runner mode that executes lane files concurrently exists only when the design declares it, and then: it admits only lane files whose crossing table shares no store with each other; every shared resource (database, schema, queues, captured mail, carried state, logs, scratch names, identities, addresses, environment keys) carries a per-child namespace derived mechanically from the lane key; a self-test runs two children with identical inputs and asserts zero cross-reads; a store with no correlation column is never asserted by delta across children; and its ledgers are labelled `mode: parallel:<n>`, which is never canonical. The sequence remains the canonical run.

**Prevents.** Two failures:

- **Testing each area in a vacuum.** In production a system is one state: one database, one set of daemons, one configuration, one set of sessions. What one area does to the state another area built is where the defects live, and a lane that starts from a fresh environment can never see what an earlier lane left behind.
- **Speed bought with unowned sharing.** Under time pressure a build parallelises anyway. Without a declared isolation contract the children share carry files, mint the same addresses, read each other's captured mail and count each other's rows, and the races surface only under load, as reds nobody can replay.

**Why this shape.** Two alternatives are rejected:

- **An environment per lane, lanes run in parallel.** The sequence would then take as long as its slowest lane instead of the sum of all of them. But every lane would test its area in a vacuum, dismissing the whole class of cross-area defects that the live tier exists to catch. The cost of this law is that a sequence takes the sum of its lanes; Law 20 designs that cost before the build, and Law 7's checkpoints keep the working loop at the cost of one lane.
- **Parallel lanes inside one environment.** Two lanes racing on one state would make every red row depend on the interleaving, so no red could be replayed, and a red nobody can replay is a red nobody fixes. Concurrency is still tested, but as a beat: a storm, two writers, an operation landing mid-flow. A beat fixes what races against what, and it can be replayed.

The declared parallel mode is the third way: it is admitted only where the crossing table proves two lane files never meet, and it is priced by an isolation contract and a collision self-test, so the sharing is owned instead of accidental. Labelling its ledgers is what keeps it out of the close, and the rows' timestamps are what disprove a mislabelled one. The hermetic-environment literature holds that a fresh environment per test reduces flakiness. That holds, and the unit and hermetic tiers buy exactly that property. The live tier has the opposite job: reproducing production's accumulation. The flakiness that accumulation brings is contained elsewhere: by Law 7 (checkpoint and solo replay), by Law 15 (a dead precondition blocks and never cascades), and by the timeline position on every red row.

**Checked by.** `self-tests`: a declared parallel mode, or a store-sharing tier run with a concurrency option, requires the collision test in the census and passing; `tree`: a `parallel:<n>` ledger offered as canonical, or a `sequence` ledger whose rows' `t+s` intervals overlap across lanes, is refused.

**Evidence.** Neighbour projects serialise the suites that contend for a shared resource rather than hoping parallel runs stay independent. One of them forces its shared-resource scenario suite to a single job.

## Law 7 — every lane is proven on the sequence's state and alone

**Rule:** Every lane opens with a `need` prelude that creates its preconditions when they are absent and is a no-op when they exist. A sequence run snapshots the environment after each lane as a checkpoint, keyed by the root hash and the lane files up to that lane. The working loop for a lane restores the checkpoint before it and runs the lane on the sequence's state. A lane is done only when it is green from its checkpoint, green alone from a fresh root, and green in the next full sequence. A lane joins the sequence the day it is written. The runner keeps going: after a red beat the rest of that lane reports `blocked-by`, and the next lane's prelude runs in create mode and reports that lane's own first fault.

**Prevents.** Three failures:

- **Lanes that meet each other's state for the first time at close.** A lane proven only alone builds a private world in its prelude; in the sequence the prelude skips, the lane meets what its predecessors actually left, and every mismatch appears one full run at a time, serially, because the first red stopped everything behind it.
- **Hidden order dependence.** A lane that works only after its predecessors ran has an undeclared precondition. The solo run from a fresh root exposes it; the prelude declares it. It is the live tier's order probe: lane order is canonical and beats carry state, so the live tier is never shuffled (Law 21).
- **An unreplayable red.** A red row from the sequence must be reproducible without another full sequence, or diagnosing it costs an hour per defect.

**Why this shape.** The checkpoint gives the sequence's state at the price of one lane, so the working loop sees the same state the close will see. The solo run from a fresh root proves the prelude, so the lane can still be replayed on its own. Keep-going makes one sequence report every lane's first fault instead of one; a lane behind a failed predecessor still attempts its own work, because the prelude creates what the predecessor did not. The prelude is idempotent: a fixture that always rebuilt would, from a checkpoint, replace what earlier lanes left, and that would erase the crossing the sequence exists to test. The two modes also give a differential diagnosis for free: a lane that passes alone and fails on its checkpoint has met a crossing defect, which is exactly the class Law 6 exists for. A solo run is owed again only when the lane's files change, because the prelude is lane code and the product it calls runs in the sequence at every close.

**Checked by.** `tree`: for every lane a green `sequence` ledger on the merge subject and a green `solo` ledger since its lane files last changed; `self-tests`: the keep-going run — a planted red in the first lane, the second lane's own verdicts in the ledger, exit 1 — is a line of `self-tests.txt`.

## Law 8 — builders, readers, destroyers

**Rule:** Lanes run in this order: state builders, then readers over the richest state, then destroyers. A lane with a destructive tail (uninstall, offboarding, purge) splits into an early half and a final half.

**Prevents.** Two failures:

- **Readers asserting over thin state.** A list with one row hides the sorting, paging and rendering defects that many rows expose.
- **Destroyers erasing what later lanes need.** The later lanes are then forced either to rebuild, which costs time, or to run on nothing, which leaves them blind.

**Why this shape.** A reader proves the most when it reads the most. The split lane exists because one area can have work that must come first and work that must come last. An operations area checks install idempotence and credentials before any user state exists, and it uninstalls, which tears the environment down, only at the very end. Each lane's position is written into the design with its reason, because an unexplained order gets reshuffled by the next maintainer.

**Checked by.** The runner sorts into canonical order whatever order was given; the design's lane table carries a reason per position, and a lane without one is an Open ruling.

## Law 9 — every crossing asserted twice

**Rule:** Two lanes assert every crossing. Every heavy beat, meaning one that loads, restarts, migrates or bulk-mutates shared state, is followed somewhere by a beat asserting that what another lane built still works. A probe-list item becomes a crossing beat only when the landscape carries the capability it probes, and a landscape row exists only when the built product reports its operation or its `evidence:` names the product code that writes the output.

**Prevents.** Blind spots, and beats for behaviour the product does not have. A lane sees a store from one side only, as its writer or as its reader, so a regression in how the other side interprets that store hides in the lane's blind spot. A heavy beat can also pass its own assertion while silently breaking a neighbour: the migration succeeds, but the report another lane built no longer renders; the restart succeeds, but the session another lane opened loses its next request. And a probe applied without asking whether the product has the behaviour — leader election, message durability across a restart — produces a beat whose name promises a protection the product never offered; inventing the landscape row to justify it is the same beat with paperwork, and a reviewer's reading of the `evidence:` row is what catches it.

**Why this shape.** Two is the smallest number that sees both sides; a third lane adds cost without adding a side. The after-effect beat is written in a fixed form: after `<lane X's heaviest beat>`, `<what lane W built>` still `<does its job>`. The form makes the designer name the victim, not only the heavy operation. Overlap is planned, never left to chance, because unplanned overlap covers whatever happened to be convenient. The landscape clause ties every crossing beat to an inventoried capability, so the probe list is a menu, not an order, and the bidirectional names check ties every inventoried capability to the built product.

**Checked by.** `map-dup`: an id whose `crossing:` rows all lie in one lane; `map-ids` and `map-names` run in both directions, so an invented operation is red; `names` refuses a property word in a slug whose test body carries no violating action.

**Evidence.** An expired credential is the canonical crossing. A health check sees it, and so does a request that uses it, and the two surfaces must agree that it is an error, not an absence.

## Law 10 — every read is scoped to what the test owns

**Rule:** Every read a test makes of a store that another test writes carries a predicate on an id the test created or carries. A count, a "first row", a "the only session" or an "unchanged table" over such a store is refused, at every tier whose tests share a store. Shared identities (an administrative account, a tenant, a default role) are provisioned once by the root build and read from the carry, never asserted as "mine".

**Prevents.** Closed-world assertions that are true only alone on an empty store. In the sequence, beside a concurrent child, or under a unit runner's parallel workers over one local store, the store holds what other tests built: the count is off by their rows, the first row is theirs, the administrative account is the one the boot lane seeded. The test then fails for a reason that is not a defect, or passes because another test happened to leave the right shape.

**Why this shape.** Shared state is the point of Law 6, and a shared local store is the point of parallel workers; the scoped read is what makes a shared store assertable at any tier. A predicate on an owned id proves the same thing an exact count proved on an empty store, and keeps proving it as the store fills. The Tiers section names which tiers share a store, so the check knows where to look.

**Checked by.** `refused-shapes`, the Unscoped read row: a store read without a predicate on a carried id, an unfiltered count or length, or a first-row read over a whole table, in every test tree of a tier that shares a store.

## Law 11 — the beat verdict

**Rule:** A beat passes on its asserted result plus a clean activity log. Assertions read the system's own reports, stored state and rendered output, and each names the landscape id it reads. A beat that made no assertion on a mapped id cannot pass for that id. Expected values come from a source outside the code under test (Law 12), never from an LLM's own claim. A beat that fails on LLM variance alone is asserting the wrong thing.

**Prevents.** Four failures:

- **A green beat with a hidden error.** The result looks right, but the product logged an error on the way: a retry that masked a failure, or a fallback that degraded the answer. That kind of degradation is invisible to a result-only assertion.
- **A green beat that asserted nothing, or asserted one thing for three.** An action ran, nothing threw, and the runner counted a pass; or a beat mapped to three capabilities drove all three and asserted one value.
- **The judge being the thing judged.** When a model says "done", the claim is itself the thing under test.
- **A beat that flakes by design.** Asserting on a model's words makes the suite fail on variance, which teaches everyone to rerun it.

**Why this shape.** The clean-log clause turns every beat into two checks for the price of one: the behaviour it asserts, plus the absence of any failure it did not expect. An expected error is declared per beat (`expect-log <pattern>`), so the clean-log rule stays strict rather than becoming a judgement call. Assertions read the system's own reports because those reports are what the user reads; asserting internal state alone proves a store, not a surface. The `assert` verb names the id, so the ledger counts assertions per mapped id and "one assertion for three capabilities" is a verdict state, not an oversight.

**Checked by.** `observed`: `NO-ASSERTION` on any mapped id with no `assert` naming it in that row's ledger.

**Evidence.** A published post-mortem of an AI coding tool describes three regressions that shipped together because user reports were "challenging to distinguish from normal variation". Its remedy is a deterministic, named signal.

## Law 12 — the oracle is independent and ratified

**Rule:** The expected side of every assertion holds a literal, a fixture or golden path, a registry key, or a name the test bound from a fixture, a seed or the carry: never a name or a call that resolves to a product module — a product type's constructor applied to literals alone counts as a literal — and never a value read from a file the product renders from (the rendered-from set of the Doors step); a product name may appear only in locator position. Every mapped row names its `source:`. For a ruled row (`ruled:yes` in the landscape) the source is a `references.tsv` row, never a bare literal or a fixture that merely holds the guess, and a disagreement between the test and the product means the product is wrong, never the test: production changes under a ruled row only with a cited reference and an owner ruling in the same commit. An assertion reads the specific value that differs between correct and broken behaviour — the code, the actor, the destination, the persisted row — never existence, truthiness, a literal on the actual side, "any value", a length alone, a disjunction of accepted values, an exception's class without its code, or a status or exit code as the row's only assertion. An assertion that cannot read such a value carries `weak-oracle:<class>` with a class from the owner's `unobservable.txt` — any other reason is ratcheted by signature — and `weak-oracle:` never appears on a ruled row. A count is derived from what the test seeded, never typed as a literal. Where a product artifact must agree with another product artifact — a store's schema with the code's definitions, two mirrors of one registry — the row's source is `drift:<artifact>` and it carries a bite on each side. For LLM output, the oracle is the parsed structure, the recorded response replayed at the hermetic tier, and a value assertion on the deterministic post-processing.

**Prevents.** Four failures:

- **The tautological oracle.** A test that compares the product's output with a value read from the same module or data file the product uses, from a sibling module that builds the same value, or from an expression re-implementing the product's formula, moves both sides on every change and can never fail. A golden the beat wrote itself is the same shape.
- **The guessed oracle.** An author — a person or a model — writes the expected value from a reading of the code or a hunch, parks it in a fixture, and when the product disagrees the reflex is to change the product; a ruled behaviour is edited to satisfy a guess.
- **The weak oracle.** An existence check passes on an empty string; a count passes whichever rows are present; a disjunction passes on either code; "any string" passes on the wrong one; a literal asserted against itself passes on anything; a status code alone passes a redirect to the wrong place and a receipt without its actor. Each was written to be green.
- **The drifting literal count.** A typed count cannot say what changed, and drifts against its twin in another file.

**Why this shape.** Independence is what lets the test disagree with the product; the grammar of the expected side — literal, path, key, or a name the test itself bound — is what a check can read, where "the module under test" is a judgement, and admitting the test's own bindings is what lets a seeded count and a carried id be asserted at all; a constructor over literals alone packages them rather than computing them, and the bite catches one that normalises them. Ratification is what settles the disagreement: a fact the owner ruled, a spec line, a contract line, a captured golden the owner reviewed, indexed in a file read at the merge base, so a reference written in the commit that cites it cannot pass (§ The threat model); whether the literal matches the reference is the reviewer's reading, because prose facts rarely contain the code they imply. A bare literal is accepted only outside the ruled set, because demanding a ratified citation for every cosmetic value trains authors to cite nothing; inside the set the citation is the point, and the set is a column with defaults by surface rather than a judgement per beat. The `drift:` source names the one legitimate product value on an expected side and makes its bite two-sided. The LLM clause exists because an LLM-driven product otherwise reaches for `weak-oracle:` everywhere. A listed class is not ratcheted because a generated id is a fact about the product, not a lapse, and a landing's wait for each would teach authors to drop the label.

**Checked by.** `oracle`: a mapped row without `source:`; a ruled row whose source is not a `references.tsv` row; in a mapped row's test, an expected side holding a name or call that resolves to a product module outside locator position (a constructor over literals alone is a literal), or a local name whose binding does, or an actual side that is a literal, a refused oracle spelling without `weak-oracle:` or with it on a ruled row, an expected-side string of three words or more equal to a rendered-from value that is not a ruled-copy key, or a literal count not derived from the seeded fixture. `refused-shapes`, the Weak oracle and Tautological oracle rows, ratchets the same spellings in every other test, and every `weak-oracle:` whose class `unobservable.txt` lacks. `bite`: a `drift:` row with fewer than two bite rows. The bite proof of Law 13 is the second guard: change the source value the product reads, and the test must go red.

## Law 13 — every test is watched biting

**Rule:** Every mapped row, every project-authored lint rule and every third-party rule a law relies on (a line of `third-party-rules.txt`), every check and every refused-shape spelling set carries a bite: `breaks-if:` names the product change that must turn it red, the build applies it once, watches the red and records `row⇥file_line⇥diff_hash⇥red_run_id` in `bites.tsv`. The mutation is one statement inside the guarded code; a diff that inserts an unconditional exit or throw, or removes more than one statement, is refused, and for a mapped row the mutation lies in the capability's file or the op's handler. Every other test is watched failing when it is written. A guard with an allow and a deny outcome is tested in both directions. A bound is tested at the boundary and one step either side. Negative fixtures differ from positive ones only in the guarded property. A name states a property (durable, recovers, idempotent, exclusive, retries) only when the body creates the condition that would violate it.

**Prevents.** Tests that cannot fail and names that lie. A guard tested only on its passing side stays green when the guard is deleted. A fixture whose alias always equals its name passes whichever field the code reads. A bound tested far past its limit never notices the limit move. A mutation that throws at the handler's entry turns any test red and proves nothing about the guard. A test named for durability that never restarts anything advertises a protection that does not exist, and the false coverage is worse than none because it is believed. A spelling set that matches nothing reads clean forever.

**Why this shape.** A test is an instrument, and an instrument is calibrated by showing it the thing it must detect. The watched red is the calibration; recording the diff and the run makes it verifiable rather than claimed, because the check can confirm the run exists, shows the row red, and ran on exactly that diff. The one-statement rule and the refused mutations remove the two lazy bites; the file rule ties the bite to the capability. The bite ledger is owed where the map, the project's own rules and the checks make a test load-bearing; for the rest, the watched failure at authoring is the executor's duty, because a bite row per unit test — or per third-party lint rule — is a cost teams would switch off. Both directions, the boundary and the minimal negative fixture are the three places a one-sided test hides, and whether a mutation was chosen to matter is a reviewer's reading (§ The threat model).

**Checked by.** `bite` at close: a mapped row, a project-authored lint rule, a listed third-party rule, a check or a refused shape with no bite row; a bite row whose red run does not exist, does not show the row ✗, or whose dirty hash differs from `diff_hash`; a mutation that inserts an unconditional exit or removes more than one statement; a mapped row's mutation outside the capability's file or the op's handler. `names`: a property word in a slug or title whose test body carries no violating action.

**Evidence.** Diff-based mutation testing is the one industrial answer found to whether a suite still tests anything; this law is its per-test, at-authoring form.

## Law 14 — coverage is observed, never declared

**Rule:** A capability counts as covered only when the canonical run observed a mapped row invoking it under the row's own injected id and asserting on it. The runner derives, per row, the operations recorded with that row's injected id (the activity log's `op` and `beat` fields, Law 17), the goldens it compared and the assertions it made by landscape id, and writes them to the run ledger. The close gate reads the ledger, never the map: a row mapped to an id with no operation record carrying the row's id is `CLAIMED-NOT-INVOKED`; an id no row invoked is `UNOBSERVED`; a mapped id with no assertion naming it is `NO-ASSERTION`. A `tier-unit:` or `tier-hermetic:` row is held to the same standard through its own tier's ledger — the buffer handler's records or the coverage context reaching the capability's file at unit, the log slice at hermetic — and in addition the named test must exist and be collected by the blocking command (`TIER-UNCOLLECTED`), have passed on the merge subject (`TIER-NOT-RUN`), and have a bite row whose mutation lies in the capability's file or the op's handler (`TIER-NO-BITE`). A time-driven job is covered by a beat that triggers it through an operator entry point carrying the id, or by a unit row. The map is the plan; the ledger is the proof, at every tier.

**Prevents.** Coverage credited by declaration or by neighbourhood. A map that is checked by set membership is satisfied by listing an id: a beat lists three operations and drives one; a registry name is moved into a beat's metadata by static reading and never touched; a covered set derived from the same registry the gate compares against covers a capability the instant it is registered. A slice attributed by log position credits a beat with a background consumer's or a scheduled job's operation that happened to land in its window. A beat that drives three capabilities and asserts one value is green for all three. Naming any passing unit test in a map row is the same fault one tier down.

**Why this shape.** Set membership answers "was it listed"; the ledger answers "did it run, under this row's id, and was it asserted". The activity log already records every entry point with its operation name, so the observation costs nothing extra — the seams wrapped for Law 17 pay a second time here; carrying the injected id through queues and jobs is the one product-side obligation, and it is the same correlation field production needs for its own tracing. A job the clock fires carries no id, so it is reached through the entry point that schedules it. A capability that leaves no operation record (a rendered screen, a generated file) declares the golden that evidences it, and the ledger records the golden compared. A unit test that reaches the capability below the point that records its operation — every unit test of a library, which has no log — proves its row by the coverage tool's per-test context instead, which every coverage tool already produces. The declared map survives, because the design must be complete on paper before a lane exists (`map-ids`, `map-beats`), but it never counts as coverage. A tier row needs the bite row on top of the observed invocation, because a unit test can reach a capability's file through a seam it exercises incidentally; the mutation inside the capability's own code is what proves the test guards that capability and not a neighbour.

**Checked by.** `observed` at close, over the canonical ledger of the merge subject, the unit and hermetic ledgers, the collected census and `bites.tsv`; `map-names` derives the operation names from the built product's own registries, never from a package manifest, and in both directions.

## Law 15 — every check names its broken state

**Rule:** Every check, scanner, lint rule, registry reader, output filter, runner and CI step has three outcomes — pass, fail, and could-not-look — and could-not-look exits non-zero with its own name, never as clean. Each broken state has its own name: a beat behind a failed precondition reports `blocked-by <beat>`; an enumerator that could not run reports `DERIVE-FAILED`; an absent log fails the beat as `LOG-ABSENT`; an unreachable dependency is an `ERROR`, never a skip; a killed or crashed run is a failed run; a check whose input is a section or a tier the design marks `none` reports `PASS none:<section>` when the marker exists and no artifact of that section exists in the tree, and `ERROR NONE-AMBIGUOUS` when both exist; a check that runs on a schedule prints `PASS not-due` between legs and errors when the last leg is older than its window. The one admitted skip is `skip-because:<reason>` with a reason from the closed list in `skip-reasons.txt`, an owner-owned path — a condition the environment makes physically unobservable, such as a permission denial under a superuser — reported by name and counted apart from passes; an unreachable dependency, a missing credential, a slow test or a flaky one is never a skip — it is an `ERROR` or a defect. Every check, scanner, filter, project-authored lint rule, the runner and the beat library ships a self-test per could-not-look input — file absent, empty, unparseable, process killed, zero items scanned, a node the rule cannot read — whose expected verdict is the error, and a planted-violation self-test whose expected verdict is the failure, both watched before the code exists. A failed beat stores raw evidence: the exact response or screen bytes and the log slice.

**Prevents.** The coincidence detector: a check that reports the same verdict when the world is fine and when the check itself is broken. Such a check blesses the failure it exists to catch, and it gets believed because it looks exactly like success. Its recurring shapes: a runner whose exit code is dropped; a verdict captured with the failure swallowed; a scan that finds nothing because it read nothing; a rule that skips the node it cannot parse; a filter whose tail dropped the only red; a suite that skips itself when its dependency is down and reports the absence as a pass; a check whose spelling set matched nothing and has read clean ever since; a "nothing to check" green produced by a marker nobody compared with the tree; a scheduled check that never ran and therefore never failed.

**Why this shape.** Each named state closes one way a green can lie:

- **`blocked-by`.** Without it, one dead precondition cascades into a wall of red beats that buries the single cause, or the dependent beats get skipped in silence.
- **`DERIVE-FAILED`.** Every gate compares sets. A failed enumerator yields an empty set, an empty set leaves nothing unmapped, and "nothing unmapped" reads as PASS. An empty enumeration is never a verdict until the enumerator is proven to have run.
- **`LOG-ABSENT`.** A missing log contains zero error records, which makes it the cleanest possible log. Without this state, breaking the logging would satisfy Law 11.
- **`ERROR` for an unreachable dependency, and a closed skip list.** A skip reports absence as success, and the coverage vanishes silently on every machine where the stack is down. The admitted skip names a reason from the owner's list, which no commit can extend for itself, so a run's skipped count is a fact about the environment, never a hiding place: the condition it names cannot be observed there by anyone, whereas a down dependency, a missing credential or a slow test can be fixed.
- **`PASS none:<section>` and `NONE-AMBIGUOUS`.** "Nothing to check" is a claim about the tree, and a marker alone cannot make it; the marker and the tree must agree — and a small project with no live tier must be able to pass on its first commit, or the gate is switched off before it ever ran.
- **`PASS not-due`.** A scheduled check that stays silent between legs is indistinguishable from one that was never wired; printing its line every time, with the last leg's run id, is what lets the census see it.
- **A self-test per input class, and a planted violation.** A rule stated in prose is enforced at some of its doors and violated at the rest; the self-tests make each door a check, the planted violation proves the check can go red on a real finding, and "watched before the code exists" is the only proof that the self-test itself can fail.
- **Raw evidence.** A paraphrase ("the screen showed an error") loses the bytes that would diagnose it. Rerunning a long sequence to see the failure again is the most expensive way to read it.

**Checked by.** `self-tests`: every check, scanner, filter, project-authored lint rule, the runner and the beat library has an input-class line and a `planted` line in `self-tests.txt`, and the reviewer reads the input classes against the inputs each reads; `refused-shapes`, the Fail-open and Self-skip rows; `bite`: every refused-shape spelling set has a watched-red planted-violation corpus; `collected`: every `CHECK` line, `PASS not-due` and `PASS none:` included, in the blocking run's log.

**Evidence.** This is the framework's standing rule: every check names what its own broken state reports. The same rule shows up in neighbour practice, for example a terminal multiplexer whose end-to-end suite keeps a debug log of the raw pty bytes it received.

## Law 16 — observation neither disturbs the system nor waits on the clock

**Rule:** A probe never consumes what the product consumes: it reads a queue's attributes, not its messages; it reads stored state, not the work item. Every observation is scoped by beat id, entity id and log offset, through the shared beat library, which reads a child process's output only after the child exited or flushed and keys every reply by beat id. Every wait is a bounded poll on a named observable condition of the product; a poll whose condition reads the clock or a time-derived value is a sleep. The harness never sleeps on the wall clock, never waits with a literal delay, and fakes only the clock, never every timer. A time-derived credential (a one-time code, a token with an expiry) comes only from the clock-seam or tolerance-window helper, never from waiting for the next real interval. A shared helper is never re-implemented locally in a lane file.

**Prevents.** Three failures:

- **The observer stealing the event.** A probe that receives from a queue the product's consumer reads takes the message for the visibility window, the consumer never sees it, and an "every item processed" over the empty batch is vacuously true; the beat proves the message is gone, not that it was applied.
- **The observer satisfied by someone else.** A log wait that reads from byte zero matches a line an earlier beat wrote; an unscoped expectation is met by a sibling arm; a straggler reply from a timed-out beat satisfies the next.
- **Idle time and bimodal time.** A sleep to the next interval of a time-based code costs half a minute per actor per login, and a poll on "the step has changed" is that sleep with a different name; a fixed window goes red under load and green when idle; the idle server hides a real rate limit that appears the moment the sleep is removed; a process-wide fake of every timer freezes the product's own backoff.

**Why this shape.** The beat library is the one place that knows the beat's log offset and ids, so routing every observation through it makes scoping automatic instead of remembered, and keying replies by beat id is what stops a straggler. A named poll turns a wait into a check that can time out with a reason — provided the condition is the product's, not the clock's. Injecting the clock is the seam law of the Doors step applied to the harness's own actors, which the seam plan for product code does not reach on its own.

**Checked by.** `refused-shapes`, the Wall-clock wait and Consuming probe rows: a sleep, a literal-delay timer, an all-timers fake, a poll whose condition reads the clock or a time-derived value, a time-based-code generator outside the clock-seam or tolerance-window helper, a receive on a queue a product consumer reads, a log read outside the beat library, a local copy of a shared helper; `self-tests`: the beat library refusing a live child's output and a mismatched reply.

## Law 17 — one activity log per environment root

**Rule:** A product with a runtime writes one structured activity log per environment root, levelled per environment, with test and pre-release defaulting to debug. Every record carries the operation name (`op`) and the ids of the entities in play; the harness injects the beat id and run id into every request and process environment it starts, and the product carries them through its queues and jobs. At the unit tier a buffer handler captures the records per test, and the runner writes each test's `op` set to its ledger. A redaction test covers credentials, tokens, prompt bodies and personal or regulated data. Lane fixtures are synthetic.

**Prevents.** Two failures:

- **A failure that cannot be diagnosed from what the product wrote,** whether it shows up in a red lane, a field report or an early tester's bug.
- **Two environments sharing one log.** A beat's slice then carries another run's errors (a false red), or the beat's own errors land elsewhere (a false green).

**Why this shape.** Law 11's verdict, Law 14's observed coverage and Law 15's raw evidence all read this log, so the log is part of the test system, not a debugging aid. Each clause earns its place:

- **One log per environment root.** A test, a lane run and production never share a destination. The path façade that separates environments already provides this, so no second mechanism is needed.
- **The `op` and `beat` fields on every record.** They are what observed coverage joins on; without `op` the log can prove an error happened but not which capability ran, and without the propagated `beat` a background job's operation is credited to whichever beat was running.
- **A product with a runtime.** A library has no log of its own, and Law 14 proves its unit rows by coverage context instead; demanding a log module of a library would be a test-only product surface.
- **Debug by default in test and pre-release.** The log is the evidence, and evidence is cheapest when it is captured by default.
- **A redaction test.** At debug level the log dumps arguments, which is the likeliest path for a credential, a prompt or regulated data to leak. A leak at debug level is silent until a test fails on it.
- **Synthetic fixtures.** A lane can then never write real personal or regulated data into a log it keeps.
- **One structured record shape.** The harness has to parse each beat's slice.

**Checked by.** `observed` reads the `op` and `beat` fields; `LOG-ABSENT` (Law 15) fails a beat whose slice is missing; `self-tests`: the redaction test is a line of `self-tests.txt`; `refused-shapes`, the Second logging path row.

## Law 18 — meaning-bearing output is pinned from an independent source

**Rule:** Output whose bytes are the interface — an emitted command, a generated file, a rendered screen's structure — is pinned by golden files, and a change to it ships with its golden in the same commit. Copy that carries meaning — legal, compliance, safety, an AI disclosure, an attribution, and any wording the owner has ruled — is pinned per locale by a literal held in `ruled-copy.tsv`, an owner-owned path independent of the file the product renders from, in exactly one test per key and locale; the registry holds keys or the single line `none`, never silence. Ordinary copy is asserted structurally: the element, its key, its presence and position, never its wording; an expected-side string of three words or more equal to a rendered-from value that is not a registry key is a refused oracle. A golden is never generated from the file the product renders from, and never written by the test that compares it: the `golden` verb compares against the tracked blob, a snapshot matcher's store sits under the goldens directory with snapshot writing disabled in the blocking command, a write under the goldens directory from a test or lane file or a snapshot-update flag in a committed configuration is a refused shape, and the first capture is reviewed before it pins anything.

**Prevents.** Three failures:

- **A rendering regression that passes every functional assertion.** The data is right and the screen is broken.
- **Ruled copy drifting unguarded.** A disclaimer that bounds what the product claims to do, a consent label, an attribution line that says who spoke — each is a commitment, and a silent edit is a breach. Pinning it by looking it up in the translation file the product renders from is the tautological oracle of Law 12: the file changes, both sides move, the test stays green. An empty registry that passes is the same drift with a ledger, and a `none` anyone can write is the same drift with a word.
- **Pinning every sentence.** When every copy edit fails a test, the golden is updated without being read, and the reviewer stops reading golden diffs; the pin that matters is then updated as blindly as the rest. A snapshot written by its own first run, or refreshed by an update flag, is the same blind update with a matcher.

**Why this shape.** The registry is independent because changing the rendered file does not change it: the two can disagree, which is the whole property, and a key removed takes effect only from the next landing, which makes its `none` a ruling rather than an omission (§ The threat model). "Meaning-bearing" is the line because it is where a wording change is a change of commitment; everywhere else, structure is the contract and wording is design, and the refused literal — on the expected side only, three words or more, so a locator or a button label never trips it — is what keeps wording pins from growing back outside the registry. Per locale, because a ruled sentence is ruled in every language the product ships. Same commit, because a golden updated separately either blesses a change nobody reviewed or leaves the tree red between commits. Emitted commands and generated files count as interface because, for a tool that writes into its users' config directories, the generated file is what the user reads. The live tier can compare its goldens against the running system, auditing them the same way Law 2 audits the mock.

**Checked by.** `copy`: every registry key pinned in exactly one test per locale, `ERROR` on a registry that is empty without the line `none`; `oracle`: in a mapped row's test, an expected-side string of three words or more equal to a rendered-from value that is not a registry key; `refused-shapes`: the same string in every other test, a write under the goldens directory, a snapshot store outside it, a snapshot-update flag in a committed runner configuration or CI step.

**Evidence.** One neighbour CLI requires snapshot coverage for any change that affects user-visible UI. A terminal multiplexer diffs its real output against snapshots.

## Sources

The citations were gathered in two research reports: [the literature on hermetic, trustworthy test gates](../../../.professor/RR/hermetic-trustworthy-test-gates-2026-09-17.md) and [how neighbour developer tools test and gate releases](../../../.professor/RR/agent-fleet-testing-release-chain-2026-09-17.md). A finding those reports marked unverified stays unverified here. The table also serves [validity.md](validity.md).

| Source | Finding | Laws |
| --- | --- | --- |
| *Software Engineering at Google* ch. 23 | Presubmit (fast, hermetic) → postsubmit → release candidate at rising fidelity → production probers, which also audit whether the tests still matter | 1, 3 |
| Bazel, *Hermeticity* | Sandboxed execution plus declared inputs plus a controlled environment; a loud failure when an undeclared dependency is touched | 1, 29 |
| Google, hermetic ephemeral test environments (ICST 2023) | Flakiness "significantly reduced", unquantified; environment startup of 10–30 min, eased by pre-warmed pools | 6, 7 |
| Luo et al., *An Empirical Analysis of Flaky Tests* (FSE 2014) | Async wait 45 %, concurrency 20 %, order dependency 12 %; 24 % of fixes changed the code under test, 94 % of those real bugs | 16, 21 |
| Google Testing Blog, flaky tests (2016) | Reruns only for tests already marked flaky; flakiness tracked per configuration | 21 |
| A strict expected-failure marker (pytest `xfail(strict=True)`) | The one mechanism found that fails the build when a listed gap passes | 19 |
| Chromium `TestExpectations` | A bug link is required but there is no expiry; stale rows are moved by hand | 19 |
| Record-and-replay for external APIs | Record once, replay, fail on anything unrecorded; staleness detection unexplained by every source | 2, 12 |
| Martin Fowler, *Continuous Integration* | The ten-minute build is a guideline; no automated ratchet documented | 20 |
| Diff-based mutation testing at Google | The one industrial answer found to whether the suite itself is trustworthy | 13, step 4 |
| chezmoi testing guide | Four tiers for a tool that writes into a user's home: unit, virtual filesystem, the real binary under a redirected home, an operating-system sweep | tiers, step 3 |
| Codex CLI `AGENTS.md` | Any change to user-visible UI carries snapshot coverage | 18 |
| zellij `CONTRIBUTING.md` | The real binary in a container, output diffed against snapshots, a debug log of raw pty bytes | 15, 18 |
| mise contributing guide | Isolated config, data and state directories per test; an order-shuffle job | 21, 29 |
| Goose CI | The shared-resource scenario suite forced to a single job | 6 |
| Agent-runner CI configurations | No provider key in any merge gate read | 1 |
| A tmux-driving agent manager's CI | Cross-compiles six targets, tests on one, no terminal-layer tests: a gate that only builds | 25, step 4 |
| An editor extension's checkpoint issue trail | Users' `.git` directories renamed and nested across seven issues; surfaced by search, fix status unverified | step 6 |
| Anthropic, Claude Code quality post-mortem | Three regressions shipped because reports were "challenging to distinguish from normal variation"; remedies: soak periods, gradual rollout, per-model evals | 11, 20 |
| `modelcontextprotocol/conformance` | A public conformance suite: an independent judge beats self-written assertions, once validated | 12, seed evidence |
