---
name: integration-suite:use
description: Designs every test tier — its validity law, and the live tier as one tree of owner steps; `/integration-suite:use <project|subsystem>`, "design our test suite", "refactor the e2e suite", before building any test harness. Returns a design document; edits no code. Source-tree layout → /llm-codebase:use.
argument-hint: <project|subsystem>
---

# Integration Suite

One procedure for any stack: a CLI, a web service, a UI, a daemon, a library with a unit tier only, a product with LLM-driven steps. The reasoning behind each rule lives in the design record, read from disk:

```bash
DESIGN="$(cd "$(dirname "$(readlink -f "$HOME/.claude/commands/integration-suite/use.md")")/../../../../docs/design/integration-suite" && pwd)"
```

Run it first. When it fails, or `$DESIGN` lacks `journey-tree.md`, `laws.md` or `validity.md`, stop and report `DESIGN-UNREACHABLE <the directory the cd tried>`. A pointer `$DESIGN/<file> § <heading>` ending a line holds that line's reasoning: read it before a judgement the line leaves open. Where a design section and this command differ, this command governs; `$DESIGN/journey-tree.md § What it replaced` lists the sections it supersedes. Before step 1, read `$DESIGN/journey-tree.md` whole.

## Inputs

- target: a project's test suite, or one subsystem of it (the same procedure over a narrower product). A project with no live tier (a library, a tool with no running state, an owner who asks for none) runs every step; the live-tier parts answer `none — <reason>`.
- live tree: the product's entry registries (router, command tree, job registry, tool list; a library's exported interface), build configuration, settings schema and existing tests, read from code, never from docs or memory.
- fix history: the version-control log and the issue tracker, the source of the escapes.
- orientation file: the project's test tiers, isolation rules and what a test run may never touch; absent, each of the three is an Open ruling.
- owner: the step 9 rulings.

## Terms

- unit tier: every door behind a seam, parallel, seconds; hermetic tier: the real binary or server as a separate process against scripted mocks, no network beyond loopback; live tier: the installed product on a machine, driven through the owner's entries.
- door: a contact with the outside world (clock, process, filesystem, network, environment); seam: the one interface in front of a door, its fake in the fakes directory, the one test-tree directory holding every fake.
- entry: one way the owner starts an action (keys typed into a real terminal, a click in a real browser, the public command line, an integration's own call); internal entry: one the owner never uses (a hidden subcommand, a direct call into product code).
- machine: the disposable host the live tier runs on (a container booting the real service manager, a virtual machine, a CI runner), set up as the owner's own.
- step: one owner action through an entry, what the owner sees after it, and the reads of the state underneath; one small file.
- tree: the one file saying which step runs after which; node: one place in it, named by its path from the root, its id and its run filter.
- chain: children run in order, each from the state the one before left; together node: a node whose children run concurrently; branch: one child of a together node with its whole subtree; join: the node after a together node, started once every branch finished; trunk: the chain of the owner's commonest day, root to leaf.
- escape: a defect a user met while the suite passed; the step that walked its path passed blind.
- watched red: the product statement a test guards, broken in a scratch copy, and the test seen failing on it with readable evidence; never committed.
- log gate: the product's activity-log records written while a step ran, read after it; an error record the step did not declare fails it.

## Names

Every name is kebab-case domain words, the same string on every surface (file stem, node name, run filter, log field), with no single letter, date, bug id, ordinal, wave or team word. A node name is unique among its siblings. A name stating a property (durable, recovers, idempotent, exclusive, retries) needs a body that creates the violating condition: restart, kill, replay, concurrent, expire, revoke, or a scripted fault. A resource a step creates (an account, a session, a workspace, a record) takes a run-unique name from the harness.

## Laws

Each line is the rule; the pointer it ends with holds why.

### Where each tier gates

- Gates: the unit and hermetic tiers gate every commit; the live tree runs on every pull request, on every operating system the Tiers section lists, each paid or credentialed dependency through its recorded mock or fake; with every dependency real it runs at each release. No gate below the release holds a credential or calls a paid or live dependency, and each runs with outbound network denied where the platform allows. `$DESIGN/laws.md § Law 1`
- Mocks: each external dependency family gets one scripted mock speaking its real protocol both ways (its replies, and the events and callbacks it sends into the product) from a scenario file, its shapes recorded from the real dependency at release and re-recorded when a defect traces to a shape it lacked; an unscripted request fails the test by name. Every internal fake lives in the fakes directory and passes the real implementation's contract cases, run against both. No test replaces a product module in place with an inline stand-in. `$DESIGN/laws.md § Law 2`
- Tier spend: each tier holds only what the tier below cannot observe: a hermetic test a process boundary, real input and output, a boot path or the assembled artifact; a live step the owner's path reaching a decision. A decision a unit test pins is not re-proved by a step; another literal input is a table-driven unit case. `$DESIGN/laws.md § Law 3`
- Boot: the hermetic tier boots the production entry point from the build output as a separate process, with production's module resolution and configuration path; the live tier installs the product the way the owner does. An in-process boot needs a self-test proving one instance of every stateful library and identical behaviour in both modes. `$DESIGN/laws.md § Law 4`
- LLM steps: where the product has LLM-driven steps, one end-to-end run at release uses a weaker, non-frontier model at its highest reasoning setting and passes on the asserted end state, never the model's claim; the model and the setting are owner rulings. `$DESIGN/laws.md § Law 5`

### The live tree

- Shape: the live tier is one tree file and one small file per step (20 to 60 lines). Read, the tree is the plan; run, it is the report, every node's verdict on one timeline. `$DESIGN/journey-tree.md § The shape`
- Fork or chain: steps that only share a starting point fork, as siblings or as branches; a step chains under another only when it needs what that one left. A failed step blocks its own descendants and nothing else: every other branch runs and reports.
- Ownership: branches of a together node run at once. Each owns what it creates under a run-unique name (its own session, server, account, workspace), its reads filter on what it created, and it stops what it started when its subtree ends, failed or not. Machine-wide checks, and steps that change the whole machine (install, restart, upgrade, uninstall), run only at the joins, alone. `$DESIGN/laws.md § Law 10`
- Entries: every action is the owner's own: typed into a real terminal running the owner's login shell with the multiplexer inside, clicked in a real browser, or run as the public command. A command run directly, or an internal entry, only reads state, seeds the machine or tears down; it never stands in for an action. `$DESIGN/laws.md § Law 33`
- Step: the owner's hands first, then the reads underneath. Act through the entry; wait for what the owner sees, a short literal the owner would recognise, never a pattern over layout; then read the state behind the screen (the product's own machine-readable listing, its database, the multiplexer's or browser's own state, the process table, the files it wrote) and assert exact values. The read is the second check, never a substitute for the first. Side effects are caught once, globally: the log gate after every step, and the consistency check (every live entity listed once, no orphan process, names equal across surfaces) at the end of each branch over what it owns and machine-wide at each join.
- Breadth: the tree grows from evidence only. An escape becomes an assertion on the step that walked past it, watched red on the unfixed code; where no step walked its path, the tree gains the step that does. A new feature adds a step. Combinations are never multiplied out. `$DESIGN/validity.md § Law 35`
- Not covered: a step the product or its fakes cannot play, and a defect this repository cannot fix (an upstream defect, absent hardware), are left out and named under Not covered with what each needs or its external reference: never skipped, never a placeholder node, never a known-gap list.
- Machine: the live tree runs on a machine it may spend, set up as the owner's own (login shell, multiplexer or browser, projects and nested projects, configuration, the launcher), with no network beyond what the design declares. The suite's entry point refuses to run anywhere else, exiting non-zero with one line, never a skip. Every container, directory and artifact is named for its run, so two runs never collide; the machine goes on a pass and stays on a failure. `$DESIGN/laws.md § Law 34`
- Speed: the whole tree, one branch, and the unit and hermetic tiers each carry a wall-time budget, measured on green runs (the tree's on the first green trunk, before another branch is written); the runner fails a run over its budget, printing measured and allowed seconds. `$DESIGN/journey-tree.md § Speed`

### What a verdict means

- Verdict: a test or step passes on its assertions plus a clean log slice; an error it causes on purpose is declared by the step. No expected value comes from an LLM's own claim. `$DESIGN/laws.md § Law 11`
- Oracle: the expected side holds a literal, a fixture, a golden, or a value the test created; never a call into product code or a value read from the product's rendered assets (message, translation and template files). Assert the value that differs between correct and broken behaviour (the code, the actor, the destination, the stored row), never existence, truthiness, any value, a length alone, a disjunction of accepted values, an exception's class without its code, or an exit status as the only assertion. A count derives from what the test created. Where a test and a ratified spec disagree, the product is wrong. Two product artifacts that must agree (a store's schema and the code's definitions) get one drift test. LLM output is asserted by its parsed structure, its recorded response replayed at the hermetic tier, and its deterministic post-processing. `$DESIGN/laws.md § Law 12`
- Watched red: every test and every step is watched red once, when written, on a one-statement break of the product code it guards (no inserted exit or throw); a guard with an allow and a deny outcome both ways, a bound at the boundary and one step either side. The change adding the test names the break. `$DESIGN/laws.md § Law 13`
- Three outcomes: every check, runner, filter and CI step has pass, fail and could-not-look, and could-not-look exits non-zero under its own name: an enumerator that read nothing, a missing log slice, an unreachable dependency, a killed run. No test skips itself; an unreachable dependency, a missing credential, or a slow or flaky test is an error or a defect. A CI job that `needs:` another runs `if: always()` and checks each needed job's result, since a skipped required check counts as passed. `$DESIGN/laws.md § Law 15`
- Evidence: a failed step keeps what the owner would need to see the failure: every terminal's screen as text and as raw bytes (a browser's screenshot and page), the log slice, the product's own listing and the process table, the screen also printed in the test log; a passing step keeps nothing.
- Observation: a probe reads, never consumes (a queue's attributes, never its message). Every wait is a bounded poll on a named, observable condition, its failure printing what it waited for, the bound and the last value seen; no fixed sleep anywhere in the harness or a step; a test fakes only the clock, never every timer. `$DESIGN/laws.md § Law 16`
- Scoped reads: every read of a store another test or branch writes filters on an id it created (no count, first row or "the only" row over shared state); shared identities are provisioned by the machine setup and read, never asserted as a test's own. `$DESIGN/laws.md § Law 10`
- Activity log: a product with a runtime writes one structured log per environment root, at debug under test; every record carries its operation and the ids in play, and the harness injects the run id and the node path into every process and request it starts, the product carrying them through its queues and jobs. One redaction test covers credentials, tokens, prompt bodies and personal data. `$DESIGN/laws.md § Law 17`
- Goldens: output whose bytes are the interface (an emitted command, a generated file, a rendered structure) is pinned by a golden, changed in the output's commit, never generated from the rendered assets nor written by the test comparing it, its first capture reviewed before it pins. Meaning-bearing copy (legal, safety, an AI disclosure, an attribution, owner-ruled wording) is pinned per locale by a literal in exactly one test. Other copy is asserted structurally (element, key, position), save on a terminal screen, where the text is the structure. `$DESIGN/laws.md § Law 18`
- Flakes: a flaky test is a defect with an owner, fixed, never quarantined or retried. The unit and hermetic tiers run shuffled in one scheduled leg; a failure seen only shuffled, or only while branches overlap, is a shared-state defect. `$DESIGN/validity.md § Law 21`
- Bound to the tree: a verdict counts only for the tree it ran on; a gate accepts only a run on the change it gates. `$DESIGN/validity.md § Law 22`

### What a test may not be

- Blocking command: the design names the exact blocking command per tier; every test file is collected by it exactly once, and every CI step running a check blocks. `$DESIGN/validity.md § Law 23`
- Retirement: an old suite is a list of claims to check, never a source of fixtures or expected values. Each claim guarding an escape lands as an assertion on the step or test that walks its path; the retiring change names every other claim it drops, and why. `$DESIGN/journey-tree.md § Retiring an old suite`
- Product only: tests cover behaviour a user, operator or integration invokes on the running or installed product. Build, lint, format and generate scripts, CI structure, the toolchain and dependency versions are not product: a configuration invariant lives in one policy lint, a version pin in the lockfile. `$DESIGN/validity.md § Law 25`
- Migrations: the migration mechanism is tested with fabricated migrations in the scratch-migration directory (ordering, a half-applied step, a rollback, the second run that changes nothing); the schema by introspecting a freshly migrated store against the code's own definitions; the upgrade from the previous release is a live step. No test names a shipped migration, parses migration text, migrates to a named version or restores a dump outside the machine setup. `$DESIGN/validity.md § Law 26`
- Source is no subject: a test never opens product source, configuration, prompts or build output as its subject; a source invariant is a lint rule with one planted violation; an absence is asserted only by such a rule, tagged security or privacy. A file the product wrote at run time is output (Goldens). `$DESIGN/validity.md § Law 27`
- One contract: one test per contract per tier; tests differing only in a literal are one table-driven test; a token-based clone detector runs over the test tree under its own baseline. `$DESIGN/validity.md § Law 28`
- Environment: the suite assembles its configuration from its own run root and gives one result under any ambient configuration. In one scheduled leg, and on every change touching the settings paths, the unit tier runs twice, under the base configuration and with every settings key perturbed to a valid alternative, with identical results. A test resolves no setting at import time. `$DESIGN/validity.md § Law 29`
- Doubles: fakes and stubs live only in the fakes directory. Production never selects a double from configuration and never substitutes a no-op for a missing key: it refuses to boot, one hermetic boot with every required key blank asserting a refusal that names each. Production source, contracts and CI files never name a test file or test path, save a CI file's blocking command. `$DESIGN/validity.md § Law 30`
- Fixtures: test, step and harness files stay under the size ceiling the design declares; fixtures carry no absolute host path, captured process id or personal data, and a captured run log is never a fixture. `$DESIGN/validity.md § Law 31`

## Refused shapes

Each shape is a rule in the project's own linter, or in one small script where the linter cannot express it, carrying one planted violation and run by the blocking command; violations already in the tree sit in a baseline that only shrinks, anchored on signatures (the enclosing symbol plus the normalised statement), never a line number. `$DESIGN/validity.md § The refused shapes` holds what each searches for.

| Shape | Law | Scope |
| --- | --- | --- |
| Fail-open verdict | Three outcomes, Flakes | checks, scanners, lint rules, filters, runner configurations, CI files |
| Weak oracle | Oracle | every test tree |
| Tautological oracle | Oracle, Goldens | every test tree |
| Unscoped read | Scoped reads, Ownership | test trees of a tier sharing a store; branch steps |
| Wall-clock wait | Observation | every test tree and the harness |
| Consuming probe | Observation | step files and the harness |
| Toolchain as product | Product only | every test tree |
| Shipped migration content | Migrations | every test tree outside the scratch-migration directory |
| Lint-as-test, tombstone | Source is no subject | every test tree |
| Self-skip | Three outcomes, Not covered | every test tree |
| Double in production, test cited by production | Doubles | production source, contracts, CI files |
| Second logging path | Activity log | production source of a product with a runtime |
| Line-anchored exemption | Refused shapes | baselines and allow lists |
| Machine in a fixture | Fixtures | fixture directories |
| Internal entry as action | Entries | step files |

## Layout

The live tier's files, named for the stack in the design; nothing else is written for it:

```text
harness/       product-agnostic: the tree walker, the step context, the terminal or browser driver; tested in the normal gate
<suite>/
  tree         the one tree file
  state        the value handed down the tree
  step_<name>  one step each
  reads        the state reads the steps share, the consistency check, the log gate
machine/       the machine definition and the script that builds it, boots it, runs the suite inside and collects the evidence
```

`escapes.tsv`, beside the design document, holds one escape per line: the defect as the user met it ⇥ the node path or test asserting it, empty while open.

## Procedure

Steps 1–3 run together, then 4–9 in order.

### 1. Read the product

Read `$DESIGN/journey-tree.md § Reading the product`.

- Entries: from the built product's registries, never docs, list every capability with the owner's entries into it, one collection pass per surface, each naming what it could not read; an empty pass is `DERIVE-FAILED` until its enumerator provably ran.
- Escapes: from the fix history, list into `escapes.tsv` every fixed defect a user met that no test caught, grouped by area and entry; the areas that break again are where the trunk goes first.
- Existing tests: per tier, what each holds and its blocking command; which are kept and which retire (Retirement).

### 2. Doors and seams

Read `$DESIGN/integration-suite.md § 3. Doors` and `$DESIGN/laws.md § Law 2`.

- Trace every door; the seam plan gives each mechanism one seam (one clock, one process runner, one environment reader, one client interface per external service), each with its fake in the fakes directory; the harness's own waits use the clock seam.
- The mocks per dependency family (Mocks); the rendered-from set (message, translation and template files) from the build configuration.
- Up to ten environment edge-case fixtures that bite this project (a read-only home, a symlinked configuration directory, a case-folding filesystem, a webhook delivered twice, clock skew, an expired credential, two concurrent writers), none for a behaviour the product lacks.

### 3. Research

Read `$DESIGN/integration-suite.md § 4. Research`.

Run two cited passes beside steps 1–2: the nearest neighbour projects (their CI configurations, testing docs and regression post-mortems) and the literature. Every adopted finding becomes a decision with its source under Prior art; a source that could not be fetched is named unverified; a question only this machine can answer (the concurrency sweet spot, the boot cost) is marked `measure here`. Without a fetch tool, report `RESEARCH-NOT-RUN — no fetch tool`.

### 4. The machine

Read `$DESIGN/laws.md § Law 34`. With no live tier, sections 5–7 read `none — <reason>`.

Define it per Machine: the owner's setup read from the owner's own machine, the dependencies (real, recorded mock or fake), the service manager when the product installs services, the network policy, the admission check, how an image is built once per hash of its inputs and never rebuilt by a product edit, and what is kept across runs (an image, a module cache) and what is removed after each.

### 5. The tree

Read `$DESIGN/journey-tree.md § The shape`.

- Draw the trunk first: the owner's commonest day from install to uninstall, through the entries the escapes cluster on.
- Fork and chain per Fork or chain; put concurrent work under together nodes, each branch owning its resources (Ownership); machine-wide steps and the machine-wide consistency check go at the joins.
- One row per step: `<node path> · types or clicks:<the exact keys or command> · sees:<the literal> · underneath:<each state read and its exact value> · escapes:<the escapes.tsv lines it asserts>`. A step played by several branches is one function working from the state handed down, never from names written into it.
- Define the consistency check over what the product shows at once: every live entity listed once, its process and its window present, no orphan process, no stale indicator, its name equal on every surface.

Drawn for an invented editor product, `═` marking branches that run together:

```text
install
├─ day                     together
│  ├═ cli ─ list ─ export
│  ├═ editor ─ open-project ─ edit ─ reload ─ close ─ reopen-from-history
│  ╚═ second-account ─ sign-in ─ switch ─ sign-out
├─ consistency             the whole machine after the day
├─ restart ─ sessions-return
└─ uninstall               the owner's own files untouched
```

### 6. Harness

Read `$DESIGN/journey-tree.md § The harness`. Write it in the product's own test language; this contract holds in any.

- Runner: the language's own test runner walks the tree: one node is one named subtest, a together node's children are parallel subtests, the runner's name filter runs one path with its ancestors and nothing beside, its parallelism flag is the concurrency knob, its machine-readable output the record. No scheduler and no ledger of its own.
- Tree: `do(name, step, children…)` and `together(name, step, children…)`; a step is `step(ctx, state) → state`, state passed by value, so a child gets its parent's output and a branch never sees a sibling's. The walker validates the tree before running (name shape, duplicate siblings); on a failed step it starts none of its children and prints `BLOCKED <n> steps under <path>: <names>`; at the end it prints the timeline (path, PASS, FAIL or BLOCKED, start offset, seconds) and `PASS <a> · FAIL <b> · BLOCKED <c>`, writes the timeline file and fails the run over its budget.
- Context: `terminal(name)` opened for the node's subtree and closed when it ends; `read(argv…)`, a command run as the owner to read state; `poll(what, within, condition)`; `unique(label)`; `expect(pattern)`, declaring an error the step causes; `artifact(name, bytes)`; `cleanup(fn)`, run when the subtree ends, failed or not, after its evidence is read. The suite fills an after-step hook with the log gate and the evidence; the harness knows nothing of the product.
- Terminal: a kernel pseudo-terminal running the owner's login shell as a session leader at a fixed size, read through a screen emulator that also answers the terminal's queries (device attributes, cursor reports); `type`, `press(keys…)`, `screen()`, `waitFor(text)`, `waitGone(text)`, `waitScreen(what, condition)`, `resize`. Its environment is a fresh login's, from an allowlist, so no test variable or multiplexer ancestry reaches it; its last raw output is kept; closing hangs up, waits bounded, then kills its process group. A browser entry: one real browser page per node, under the same contract.
- Self-tests in the normal gate, each property once with its exact value: serial order; a child gets its parent's state and a branch not its sibling's; together children overlap, proven by a latch both must reach, never a sleep; a join starts after every branch; a failed step blocks exactly its descendants and the count line is exact; a bad name and a duplicate sibling refused; the terminal against a plain shell (typed text, keys, the screen after cursor movement and a clear, a resize the child sees, a close leaving no child, a failed wait's message carrying the screen); the budget over, under and at the boundary.

### 7. Activity log

Read `$DESIGN/laws.md § Law 17`. With no runtime (a library, a UI component suite), section 8 reads `none — <reason>`.

Design the log of Activity log: one JSON-lines stream per environment root, one record shape and one destination for a product of several processes; the record `ts level msg op pid version`, the ids in play, `dur_ms`, `err`, the injected run id and node path. Wrap the seams of step 2, never the call sites. Name the level per environment, the redaction test, the rotation and the reader the log gate uses (a filter by time window, level, run and node).

### 8. Speed

Read `$DESIGN/journey-tree.md § Speed`.

Set the targets per Speed: the whole tree, one branch through the run filter, and each lower tier. Write the per-change policy: while working, the affected unit and hermetic tests and the touched branch through the run filter; the whole tree on every pull request. Price nothing on paper: the trunk's measured time sets the tree's budget, and a trunk over its target re-cuts the tree before another branch is written.

### 9. Owner review

The owner rules: a real dependency versus a mock or fake, per tier; the machine's setup against the owner's own machine; the operating systems the tree runs on; the trunk, and which escapes go first; the speed targets; the logging framework; which LLM and setting run the LLM-driven steps at release; the size ceiling. Present each research finding and draft decision in one plain line; each ruling moves from Open rulings into the section it changes. With no owner present, every item stays under Open rulings, recommendation first, and the document is marked `DRAFT — unruled`.

## Output and build order

Write the design document to the path the request names, else `<the project's design-doc directory, else docs/design>/<target>-integration-suite/design.md`, and state the path in the reply; beside it write `escapes.tsv`. Edit no product code and no test. Each section is a `## <n>. <name>` heading:

1. Tiers: per tier what it asserts, what it fakes, the gate it guards, its exact blocking command and boot; the operating systems the tree runs on; the settings paths that trigger the double run; the scheduled leg and the longest gap allowed between its runs
2. The product read: entries, the escapes by area, the existing tests kept and retiring
3. Doors and seams: the seam plan, the fakes directory, the mocks, the fixtures, the rendered-from set
4. Prior art: adopted, avoided, `measure here`, each with its source
5. The machine
6. The tree: the drawing, the step rows, the consistency check
7. Harness: the contract adapted to the stack, the layout, the self-tests
8. Activity log
9. Speed: the targets, the per-change policy
10. Build order: below
11. Not covered: every step and surface left out, with what it needs or its external reference
12. Open rulings

A section with nothing to design has the single line `none — <reason>` as its whole body; a tier with no tests is its Tiers line `<tier>: none — <reason>`.

Build order, by risk and dependency; on a live tier each phase ends on a run the owner can watch:

1. Run isolation, the blocking commands and the unit and hermetic budgets.
2. Seams, fakes with their contract suites, and fixtures; then the packages converted in batches with disjoint file sets.
3. The activity log, then the mocks.
4. The harness with its self-tests, then the machine.
5. The trunk, green end to end on every listed operating system and within its budget, then watched red three times: a break in two of its paths and the revert of a real past fix, each failing exactly the expected step with a readable screen, only its descendants blocked, the other branches passing.
6. The fork: the remaining branches, several builders at once, each owning one branch (its step files and its own lines of the tree file; the shared state and reads files changed only by adding). The builder of a step lands the assertions for the escapes it walks past. A branch joins when it is green alone three times through the run filter, every step watched red; the whole tree is done when green twice within its budget, its timeline showing the branches overlapping.
7. The tree wired into CI on every pull request and every listed operating system; the old suite retired in that change (Retirement).

With no live tier, steps 4–7 drop, and done is one green run of the blocking commands on the change.

## Checks

The refused-shape rules, plus these rows, run by the blocking command in the project's linter or one checks script, each with the three-state protocol of `/llm-codebase:use` § Protocol and each proving its inputs non-empty before comparing:

| Check | FAIL on | ERROR on |
| --- | --- | --- |
| `collected` | a test file the blocking command does not collect, or collects twice; a listed operating system with no CI job running the tree | the CI configuration or the blocking run's log unreadable |
| `names` | beyond its baseline, a name breaking Names, or a property word whose test body creates no violating condition | no names read |
| `clones` | a clone in the test tree beyond its baseline | the detector absent, or it matched no files |
| `size` | a test, step or harness file above the size ceiling beyond its baseline | the ceiling not declared |
| `escapes` | an `escapes.tsv` line naming a node path the tree lacks or a test that does not exist | `escapes.tsv` unreadable |
| `env-twice` | the unit tier's results differ between the base and the perturbed configuration | no scheduled leg within the gap the Tiers section allows |

## Where enforcement ends

Read `$DESIGN/integration-suite.md § The threat model`.

The checks defend against accident, laziness and the pull toward green by an author who does not forge. Every baseline, the size ceiling and the budgets are read at the merge base and in the working tree, the stricter reading applying, so a loosening takes effect only from the next landing. At every landing the suite reviewer (in a project installed from this blueprint, the flights lander; elsewhere whoever the owner names) reads what no check judges: every loosening; a sample of the named watched reds, re-run; each added step's screen literal and reads against what the owner sees and what lies underneath; each escape's step against the defect's path; a chain whose steps only share a starting point, which forks.

## Hand-off

The design is reviewed before anything is built: a trunk not on the owner's commonest day, a chain that should fork, a branch reading what it does not own, a step acting through an internal entry, an escape with no step, a check whose broken state would read as PASS. The project's testing manual carries the per-change duty under its Suite duties section (a feature lands with its watched-red step or test; an escape's fix lands with its assertion on the step that walked past it and its `escapes.tsv` line; the whole tree on every pull request where a live tier exists) and the refused shapes under What not to test; a rule only this command holds is not in force.

In a project installed from this blueprint, a flights foreman builds the suite in Build order, one per branch on the fork; the flights lander runs the blocking commands, the touched branch while it fixes and the whole tree in its full gate, and performs the reviewer's reading. Elsewhere the owner assigns the build, the runs and the review.

The suite's weight is part of its verdict: one tree of small owner steps, typed through the owner's own entries, running together and growing only from escapes.
