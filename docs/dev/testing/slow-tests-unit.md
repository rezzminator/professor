# Slow unit tests

## Contents

- [Test records](#test-records)

The durations below are historical observations under contention at `8bfb6cdc`. Source dependencies and dispositions describe the current test bodies. Tier placement is a decision for this train; it does not claim a move is complete.

## Test records

### TestEveryEgressGoesThroughTheGateway

**Source:** [pfm/internal/harvest/gateway_chokepoint_test.go](../../../pfm/internal/harvest/gateway_chokepoint_test.go) line 182 · **Observed:** 43.94s · **Class:** AST/source filesystem

os.ReadDir("."); parser.ParseFile for every production .go; scanGatewayEgress.

U — package source enumeration and type-aware AST checking are local guard work; no HTTP is issued.

### TestStressOpenCodeIndexSurvivesHostileStore

**Source:** [pfm/internal/index/opencode_stress_test.go](../../../pfm/internal/index/opencode_stress_test.go) line 168 · **Observed:** 18.06s · **Class:** SQLite/temp filesystem

seedOpenCodeStress creates SQLite WAL and 300 hostile rows; ReadOpenCodeSessions reads twice.

U — file-backed SQLite and parsing stay inside the package; no process/network seam.

### TestStressOpenCodeReadWhileWriterActive

**Source:** [pfm/internal/index/opencode_stress_test.go](../../../pfm/internal/index/opencode_stress_test.go) line 278 · **Observed:** 16.76s · **Class:** SQLite concurrency/timer

seedOpenCodeStress; SQLite WAL writer performs 2,000 transactions; time.Sleep(2ms) per round; five reads.

mock seam — the read/write unit contract can retain SQLite while replacing fixed pacing with a channel/clock handoff.

### TestStressOpenCodeMirrorConcurrentPasses

**Source:** [pfm/internal/index/opencode_stress_test.go](../../../pfm/internal/index/opencode_stress_test.go) line 227 · **Observed:** 12.70s · **Class:** SQLite concurrency

seedOpenCodeStress; store.Open temp DB; three goroutines call syncOpenCodeMirror; reads mirrored rows.

U — both databases are temp/local and the concurrency seam is in-process.

### TestDeliverThenRequiresTheTailNeedleWhenTheBaselineCaptureFailed

**Source:** [pfm/internal/reload/reload_then_proof_test.go](../../../pfm/internal/reload/reload_then_proof_test.go) line 226 · **Observed:** 8.65s · **Class:** injected fake seams

fakeReloadTmux; fakeReloadProc; clock.NewFake; driveFakeClock; deliverThen.

U — no process or real clock; reload proof is already seam-driven.

### TestDeliverThenRefusesAStalePlaceholderLeftInScrollback

**Source:** [pfm/internal/reload/reload_then_proof_test.go](../../../pfm/internal/reload/reload_then_proof_test.go) line 148 · **Observed:** 8.38s · **Class:** injected fake seams

stalePlaceholderScrollbackTmux; fakeReloadProc; clock.NewFake; driveFakeClock; deliverThen.

U — local fake tmux/process and fake clock isolate the proof.

### TestRunReturnsAnErrorAndSavesTheSentinelWhenSubmitIsNeverConfirmed

**Source:** [pfm/internal/reload/reload_then_proof_test.go](../../../pfm/internal/reload/reload_then_proof_test.go) line 307 · **Observed:** 6.65s · **Class:** injected fake seams/filesystem

stuckSubmitTmux; fakeReloadProc; clock.NewFake; Run; sentinel file read.

U — no real process; fake reload and process interfaces expose the error path.

### TestUIStress

**Source:** [pfm/internal/ui/stress_test.go](../../../pfm/internal/ui/stress_test.go) line 16 · **Observed:** 4.51s · **Class:** CPU/memory

largeSnapshot; NewModel/View; 1,000 frames; applyKey; 10,000 random keys and 100 refreshes.

U — pure in-process model/render stress with no external process or network.

### TestGatewayEgressEnumeratorIgnoresLookalikeMethods

**Source:** [pfm/internal/harvest/gateway_chokepoint_test.go](../../../pfm/internal/harvest/gateway_chokepoint_test.go) line 294 · **Observed:** 4.49s · **Class:** AST/type check

inline fixture string; parser.ParseFile; scanGatewayEgress type-checks http.Header/url.Values methods.

U — local parser/type checker only; net/http names in the fixture do not issue network calls.

### TestGatewayEgressEnumeratorCatchesEveryShape

**Source:** [pfm/internal/harvest/gateway_chokepoint_test.go](../../../pfm/internal/harvest/gateway_chokepoint_test.go) line 238 · **Observed:** 3.81s · **Class:** AST/type check

inline HTTP-call fixture; parser.ParseFile; scanGatewayEgress type-check; compares 12 shapes.

U — parser/type-check CPU, not mocked network latency and not an HTTP request.

### TestAsyncCallerRefreshStormPreservesCursorAndGoroutines

**Source:** [pfm/internal/picker/pipeline_test.go](../../../pfm/internal/picker/pipeline_test.go) line 429 · **Observed:** 3.16s · **Class:** SQLite/goroutines

jailTest/InstalledHome writes temp artifacts; store.Open/Batch 256 rows; 100 streamFleetRefreshesWith refreshes.

U — temp files and in-process refresh/index seams; no child process is launched.

### TestReadOpenCodeSessionsRejectsMalformedNativeJSONAndShapes

**Source:** [pfm/internal/index/opencode_test.go](../../../pfm/internal/index/opencode_test.go) line 287 · **Observed:** 2.37s · **Class:** SQLite/JSON parsing

seedOpenCodeStore; SQLite mutations via sql.Open/Exec; ReadOpenCodeSessions validation.

U — temp SQLite and parser validation are local package seams.

### TestAwaitAnswersTheLatestQuestion

**Source:** [pfm/internal/headless/converse_test.go](../../../pfm/internal/headless/converse_test.go) line 335 · **Observed:** 2.11s · **Class:** timer/file concurrency

newConversation file transcript; entryProgressGate; goroutine writes; Await uses real poll timers and a 2 s settle.

mock seam — inject wait/clock boundary so latest-question ordering stays U without wall-clock settle time.

### TestApplyNeverDeletesANoncanonicalProjection

**Source:** [pfm/internal/heal/heal_test.go](../../../pfm/internal/heal/heal_test.go) line 573 · **Observed:** 1.74s · **Class:** filesystem/state

newCodexJail; temp projection files/cursors; heal.New.Run(Apply); row/backup checks.

U — all state is temp and runner receives a local clock; no process/network.

### TestRetireRenamedGlobalAgentsDeletesOnlyTheInstallersOwnFrrLeftover

**Source:** [pfm/internal/installer/installer_test.go](../../../pfm/internal/installer/installer_test.go) line 1234 · **Observed:** 1.41s · **Class:** filesystem/fake runner

temp home; symlink/regular/frontmatter fixtures; Run(ModeApply, fakeRunner); Lstat/read checks.

U — installer runner is fake and all paths are temporary local files.

### TestRetireOrphanGlobalCommandsPrunesOnlyItsOwnDanglingLinks

**Source:** [pfm/internal/installer/global_command_retire_test.go](../../../pfm/internal/installer/global_command_retire_test.go) line 20 · **Observed:** 1.27s · **Class:** filesystem/fake runner

temp blueprint/home; symlink and regular-file fixtures; Run(..., fakeRunner); Lstat/link checks.

U — no external command or network; retirement is isolated in temp registries.

### TestWaveStatsWrittenByRealFetch

**Source:** [pfm/internal/harvest/wave_parity_test.go](../../../pfm/internal/harvest/wave_parity_test.go) line 152 · **Observed:** 1.20s · **Class:** HTTP mock/filesystem

mustNew; injected roundTripFunc returns 404; h.Fetch; stats file check.

U — “real fetch” means production fetch path, while HTTP transport is mocked.

### TestV4MigrationAddsCxNameProvenanceIdempotently

**Source:** [pfm/internal/store/queries_test.go](../../../pfm/internal/store/queries_test.go) line 377 · **Observed:** 1.10s · **Class:** SQLite/migrations

setStoreTestJail; temp SQLite schema v1-v3 and row; openTestStore migration/reopen; query checks.

U — local temp database and SQL migration path, with no external process.

### TestEnsureOpenCodeSessionsAssistantCountAddsColumnIdempotently

**Source:** [pfm/internal/store/queries_test.go](../../../pfm/internal/store/queries_test.go) line 500 · **Observed:** 1.05s · **Class:** SQLite/migrations

setStoreTestJail; temp SQLite schema v8; openTestStore ALTER/reopen; row round-trip.

U — local database migration and idempotence remain unit-level.

### TestBibliographicLandingCycleStopsAfterOneHop

**Source:** [pfm/internal/harvest/content_test.go](../../../pfm/internal/harvest/content_test.go) line 11 · **Observed:** 1.00s · **Class:** HTTP mock/redirect logic

withPublicDNSForProviderTest only injects DNS; roundTripFunc fakes A/B HTTP cycle; legacyConverterFunc; h.Fetch.

U — no real DNS or HTTP request occurs; both clients and converter are injected.
