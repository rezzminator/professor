# RR — How do teams keep unit + integration suites for CLI/daemon/agent-orchestration tools fast, hermetic and trustworthy as the release gate?

Question: How do engineering teams keep unit + integration test suites for CLI/daemon/agent-orchestration tools fast, hermetic and trustworthy as the release gate — papers, research, engineering blogs and long-form writing on: mocking host/process/network/clock dependencies at seams (fakes vs mocks, fake clocks, scripted process runners); test parallelism sweet spots and timing budgets/ratchets that keep suites from becoming the development bottleneck; hermetic end-to-end tests of a real binary in a jailed HOME vs live/'realistic' integration lanes on fresh machines/containers; flaky-test economics and quarantine policies; known-gap/xfail ledgers that stay honest; release-chain gates (what runs at commit, at merge, pre-release) in projects that ship developer tools that touch tmux/terminals, multiple accounts/credentials, and external AI APIs.

---

## Answer

The literature converges on one shape: **hermeticity is bought at the seam, not at the boundary** — fake the clock, jail HOME/env, bundle every dependency into an ephemeral sandbox, and the flake rate collapses; then stage the chain so the fast hermetic tier gates every commit and the slow, high-fidelity, network-touching tier runs after the merge. The two things practitioners most want numbers for — a parallelism sweet spot and an automated suite-duration ratchet — are **not documented anywhere I could reach in two rounds**; the ten-minute build is a guideline, not a mechanism, and even Chromium's famous expectations ledger enforces a bug link but has **no expiry rule at all**.

---

## Load-bearing findings

### 1. Flake economics: the numbers, and why blanket quarantine is a trap

- Google TAP data reported in Luo et al., *An Empirical Analysis of Flaky Tests* (FSE 2014): **73K of 1.6M daily test failures were flaky — 4.56%** over 15 months. Root causes in their 161-commit sample cluster hard: **Async Wait 45% (74/161), Concurrency 20% (32/161), Test Order Dependency 12% (19/161)** ([fse14.pdf](https://mir.cs.illinois.edu/lamyaa/publications/fse14.pdf)).
- The same paper is the strongest argument against reflexive quarantine: **24% of "flaky test" fixes actually changed the code under test, and 94% of those were real bugs.** Quarantining by default silently discards genuine defect signal.
- Google's own [*Flaky Tests at Google and How We Mitigate Them*](https://testing.googleblog.com/2016/05/flaky-tests-at-google-and-how-we.html) is far less quantitative than its reputation: John Micco states Google had (as of 2016) **"nothing publishable"** on the dollar/time cost of flakiness. The mechanics it does give: **reruns fire only for tests already marked flaky**, and flakiness is tracked **per flag/configuration combination**, not per test.
- Google's 2017 follow-up, [*Where do our flaky tests come from?*](https://testing.googleblog.com/2017/04/where-do-our-flaky-tests-come-from.html), correlates flakiness with **test binary size** — notably *not* with parallel execution or shared-resource contention.

### 2. Hermetic + ephemeral environments: the mechanism, unquantified

- Google's CCIW 2023 / ICST talk [*How we use Hermetic, Ephemeral Test Environments at Google to reduce Test Flakiness*](https://conf.researchr.org/details/icst-2023/cciw-2023-papers/3/How-we-use-Hermetic-Ephemeral-Test-Environments-at-Google-to-reduce-Test-Flakiness) (companion write-up by Carlos Arguelles, [Medium](https://carloarg02.medium.com/how-we-use-hermetic-ephemeral-test-environments-at-google-to-reduce-test-flakiness-a87be42b37aa)) defines it precisely: **hermetic** = all dependencies bundled in one sandboxed container, no cross-network calls; **ephemeral** = SUT spun up per test, torn down after. Claimed effect: flakiness "significantly reduced" across Google — **with no published percentage.** Cost is real and named: **10–30 minutes of SUT startup** for complex dependency graphs, mitigated by pre-warmed SUT pools, telemetry-driven boot optimization, and record-replay to "fake hermeticism cheaply."
- Bazel's definition is the crisp one: hermeticity = **sandboxed execution + fully declared inputs + controlled (not inherited) environment variables**, which buys reproducibility, safe remote caching/execution, and a *loud failure the moment an undeclared dependency is touched* ([bazel.build/basics/hermeticity](https://bazel.build/basics/hermeticity)). Tweag's analysis adds that Bazel enforces this "to some extent... less strict about it than Nix," and that execlogs make non-determinism *detectable* rather than merely avoided ([tweag.io](https://www.tweag.io/blog/2022-09-15-hermetic-bazel/)).

### 3. Go practice for a real binary in a jailed HOME

- `rogpeppe/go-internal/testscript` (descended from `cmd/go`'s own script_test harness) gives each `.txtar` script a **fresh `$WORK` temp dir, `HOME=/no-home`, and `TMPDIR=$WORK/.tmp`**; `testscript.Main(m, commands)` registers your CLI so `exec cmd` runs it as a **real subprocess** without polluting the parent test binary ([pkg.go.dev](https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript)). Critically, it hermeticizes **env + workdir, not the OS** — the community pairs it with `mvdan.cc/dockexec` for container-level isolation ([golang-nuts thread](https://groups.google.com/g/golang-nuts/c/98SJJorDzhI/m/Gb1ZF4MGCwAJ)).
- `testing/synctest` (GOEXPERIMENT in Go 1.24) fakes **only the clock plus durable-blocking detection** inside a goroutine "bubble," advancing virtual time when every goroutine in the bubble is blocked; **real mutexes and external I/O (network, file) stay unvirtualized.** `synctest.Wait()` replaces sleep-polling because it blocks until the bubble is provably quiescent — the Go team frames this explicitly as dissolving the "fast *or* reliable" tradeoff of sleep-based tests ([go.dev/blog/synctest](https://go.dev/blog/synctest)).

### 4. Deterministic simulation as the extreme case

Antithesis's framing of DST: make clock, thread interleaving, system randomness and I/O deterministic so the same seed reproduces the same execution, and "execution can be rolled back and inspected at multiple points in time." Their own stated **non**-indications are the useful part for scoping: skip DST when retrofitting determinism into a live system is impractical, when there's no concurrency, when **external dependencies can't be mocked or controlled**, or when simulation infra cost exceeds the bug-prevention value ([antithesis.com docs](https://antithesis.com/docs/resources/deterministic_simulation_testing/)).

### 5. Speed: the real budgets, the real selection numbers

- **Test sizes are only partly enforced.** Google's small tests are restricted to **one thread / one process / one machine**; large tests carry a **default timeout of 15 minutes or 1 hour**, though some run hours or days — and the *Software Engineering at Google* "Larger Testing" chapter itself admits large tests **"suffer a lack of standardization"** ([abseil.io ch14](https://abseil.io/resources/swe-book/html/ch14.html)).
- **Test selection beats brute force, measurably.** Meta's *Predictive Test Selection*: naive build-dependency selection picks on the order of **10⁴ tests per change** against tens of thousands of changes/week; their learned classifier cuts testing infrastructure cost **by a factor of two** while still surfacing **>95% of individual test failures and >99.9% of faulty changes** ([arXiv 1810.05286](https://arxiv.org/pdf/1810.05286)).
- **Google TAP scale and the latency it still eats** (Memon et al., ICSE-SEIP 2017): **13K projects/day, 800K builds, 150M test runs/day**, ~1 commit/second, commits bundled into **milestones cut every ~45 minutes**, each covering up to **4.2 million tests**, with observed feedback delays **up to 9 hours**; only **63K of 5.5 million affected tests** ever failed, which is what motivates skipping always-passing tests ([research.google.com/pubs/archive/45861.pdf](https://research.google.com/pubs/archive/45861.pdf)).
- **The budget is a guideline, not a gate.** The XP/Continuous Delivery **ten-minute build** is confirmed on Fowler's [continuousIntegration.html](https://martinfowler.com/articles/continuousIntegration.html) ("the XP guideline of a ten minute build is perfectly within reason... most of our modern projects achieve this," vs. an hour-long build being "totally unreasonable"). **No automated ratchet mechanism was found** — see Open questions.

### 6. Release-chain staging — the canonical four tiers

*Software Engineering at Google* ch. 23 defines the chain purely as cost-vs-feedback-latency ([abseil.io ch23](https://abseil.io/resources/swe-book/html/ch23.html)):

| Tier | What runs | Why |
|---|---|---|
| **Presubmit** | fast, reliable, mostly unit tests | "waiting a long time to run every test during code submission can be severely disruptive"; a green presubmit gives ~95%+ confidence in the rest |
| **Postsubmit** | *all* potentially affected tests, including larger and slower ones | tolerates higher latency and some instability, because the change already landed |
| **Release candidate** | larger tests against the entire candidate | promoted through progressively higher-fidelity environments |
| **Production (probers)** | the same suite against the live system | verifies both the state of production **and the continued relevance of the tests** |

That last row is the underrated one: the production lane exists partly to audit the test suite, not only the system.

### 7. Driving a real terminal, and the AI-API seam

- **PTY:** Go's `creack/pty` gives `pty.Start(cmd)` to attach a spawned process to a real pseudo-terminal master — read its output, write simulated input, manage geometry via `pty.InheritSize`/SIGWINCH; it is the primitive under expect-style layers such as `go-expect` ([github.com/creack/pty](https://github.com/creack/pty)).
- **tmux tests itself from the outside:** its [`regress/`](https://github.com/tmux/tmux/tree/master/regress) directory is a shell-script suite (`input-*.sh`, `capture-pane-*.sh`, `control-client-*.sh`, `copy-mode-*.sh`) that drives a real tmux session and **diffs captured pane output against checked-in `.result` baselines** — capture-and-compare, not inline assertions. (Inferred from the visible file manifest; GitHub's directory viewer errored on file contents, so treat the per-file detail as partially unverified.)
- **External LLM APIs:** the working pattern is VCR-style — **record real interactions once, replay deterministically thereafter, and error on anything unrecorded**, with an "audit mode" to rebuild the allowlist baseline ([zylos.ai](https://zylos.ai/zh/research/2026-08-14-network-egress-assertion-hermetic-tests-agent-tooling/)). Every source that mentions cassettes leaves **staleness detection unexplained** — see Open questions.

### 8. Known-gap ledgers: one mechanism works, the famous one is weaker than its reputation

- **pytest `xfail(strict=True)` / `xfail_strict = true`** turns an unexpected pass (XPASS) into a **build failure** rather than silent success. This is the one enforcement pattern found that structurally prevents a known-gap entry from being forgotten ([pytest skipping/xfail docs](https://docs.pytest.org/en/stable/how-to/skipping.html)).
- **Chromium's TestExpectations requires a bug link but has no expiry.** Every line must carry a bug identifier — "Lines are expected to have one or more bug identifiers, and the linter will complain about lines missing them" (`crbug.com/12345` or `Bug(username)`) — but the docs specify **no expiry dates and no automatic removal**. The only staleness mechanism is a **manual `StaleTestExpectations` file** that platform-specific lines "many months" old get moved into for declutter: administrative reorganization, not an enforced bankruptcy rule ([chromium web_test_expectations.md](https://chromium.googlesource.com/chromium/src/+/main/docs/testing/web_test_expectations.md)).

### 9. Auditing the gate itself

If the question is "is the suite trustworthy," mutation testing is the only industrial answer found with numbers. Google runs it **diff-based** — mutating only changed code during code review — with **mutant filtering** (skipping uncovered and "arid" lines, capping mutants per line and per review) and **historical operator selection**, evaluated across **>24,000 developers on >1,000 projects** and producing "orders of magnitude fewer mutants" against a codebase of **2 billion LOC and 500M+ daily test executions** ([arXiv 2102.11378](https://arxiv.org/abs/2102.11378); see also [State of Mutation Testing at Google, ICSE-SEIP 2018](https://research.google.com/pubs/archive/46584.pdf)).

---

## Open questions (named gaps, not omissions)

1. **Parallelism sweet spots — fully unanswered after two rounds.** No source in either round produced a measured worker-count ROI curve, a diminishing-returns threshold, or evidence quantifying parallel execution as a *cause* of flakiness via shared-resource contention. The named anchor (Lam et al., *iDFlakies*, ICST 2019, `doi:10.1109/ICST.2019.00017`) **timed out** on harvester fetch and was not retried within budget. Google's own flaky-origins post is a confirmed dead end here — it correlates flakiness with binary size, never with parallelism.
2. **CI time ratchets — no mechanism found.** The ten-minute build exists as a guideline; **no automated script or gate that fails the build when wall-clock regresses past a high-water mark** was documented in any reachable source. Fowler's dedicated `bliki/TenMinuteBuild.html` page **404'd via WebFetch and timed out on harvester retry.**
3. **Multi-account / credential test doubles — unanswered in both rounds.** Neither the hermeticity sources nor abseil ch23, creack/pty, or tmux's regress suite address fake credential stores, per-test `$HOME` credential isolation, or test-only token fixtures. Concrete next query: *how do the `gh`, `aws-cli`, and `gcloud` test suites fake `~/.netrc`/keychain state?*
4. **The hermetic/ephemeral payoff is unquantified.** Google claims "significantly reduced" flakiness with no percentage, no before/after, and no cost-per-test figure against the stated 10–30 min SUT startup.
5. **`testing/synctest` GA status is unverified.** The fetched Go blog post is Go-1.24-era; whether Go 1.25 shipped it GA and whether the API changed (e.g. a `synctest.Test` entry point) was not confirmed from a fetched source.
6. **`clockwork` vs `benbjohnson/clock` vs synctest-backed drop-ins** rests on search snippets only — no library page was fetched, so treat the fake-clock library comparison as unverified.
7. **Microsoft/Meta quarantine and auto-retry policy numbers were not found.** Not surfaced, not fetched — unverified rather than absent.
8. **Cassette staleness for LLM APIs is the universal unexplained gap.** Every source describing record/replay stops before explaining how teams *detect* recorded-vs-live drift when a model version or API contract moves.
9. **The dollar cost of flakiness remains unpublished** as far as these sources go — Google said "nothing publishable" in 2016 and nothing later was found.
10. **Weak-evidence note:** the one empirical paper found specifically on testing LLM-powered systems ([arXiv 2508.00198](https://arxiv.org/abs/2508.00198)) draws on **99 student course-project reports**, not industry practice. Its themes (prompt sensitivity, uncertainty about correctness, blended manual/automated strategies) are directionally interesting but should not be cited as industrial evidence.

---

## Dispatch telemetry

6 diggers dispatched across 2 rounds, 6 reports received. Round 1 (4): Go hermetic CLI testing · flaky economics + ledgers · parallelism/budgets/selection · gate staging + hermeticity + external APIs. Round 2 (2, targeting the two named holes round 1 returned nothing on): gate staging + pty/credentials · duration budgets/ratchets/parallelism/Chromium expiry. Note: `Explore` is disabled in this install, so round 1 was re-dispatched to `general-purpose` on sonnet with fan-out forbidden; depth stayed at one hop.
