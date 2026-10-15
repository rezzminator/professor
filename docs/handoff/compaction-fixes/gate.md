# gate r1 — compaction-fixes

## Review tier

| repo | tier | sink hits (added prod lines + owned untracked) | changed lines | sandbox |
|---|---|---|---|---|
| professor | reviewer-pro | 89 (infra/plugin-lab shell) + 2 (checks.sh) | tracked +103/-73, untracked 49 files / 2687 lines | review-r1/professor/ |
| sub-agent-compact | reviewer-pro | 12 (fs/path/env in src + hook) | +1626/-326, prod +773 | review-r1/sub-agent-compact/ |
| effort-live-control | reviewer | 0 | +59/-3, prod 9 | review-r1/effort-live-control/ |
| cache-live-control | reviewer | 0 | +40/-2, prod 5 | review-r1/cache-live-control/ |

## Gate runs

- professor static: `check-templates.sh` over 93 flight files, all PASS (logs/gate-r1-static-professor.log)
- professor full: `dev.sh iso gate` — `all steps passed.` STEPS PASS 225.7, EGRESS PASS, RUN DIR /tmp/compaction-fixes/timing/run.VRAkeZ (logs/gate-r1-full-professor.log)
- pfm coverage floor: `dev.sh iso cover pfm` (logs/gate-r1-cover-pfm.log) — red: `--- FAIL: TestCodexRefusingAnEmptyNameIsReportedAsSuch` (startup-screen warning under coverage load; pfm/ untouched by the flight; same tree's gate pfm.unit PASS; isolated `-count=20` ok 12.4s) — rerun logs/gate-r1-cover-pfm-2.log: `Total test coverage: 85.4% (71622/83861)`, package 52% PASS, total 77% PASS, `all steps passed.`
- plugins full, fence as root (logs/gate-r1-full-plugins.log): sub-agent-compact 723 passed / 2 skipped, typecheck rc0, release-check FAIL badge 724 vs 723 (root-only skip difference); effort 548/2, typecheck rc0, release-check PASS; cache 254/1, typecheck rc0, release-check PASS. `claude` CLI absent in fence: validate:plugin and cache test:hooks not run there.
- plugins full, fence as uid 65534 = CI's non-root ubuntu (logs/gate-r1-full-plugins-nonroot.log): sub-agent-compact `Tests 724 passed | 1 skipped (725)`, release-check `PASS release 0.7.0-dev.23 ... README badge counts 724 passing tests`; effort `Tests 547 passed | 3 skipped (550)` PASS; cache `Tests 254 passed | 1 skipped (255)` PASS. Cache's macOS-only near-limit lone-surrogate test is green on Linux (root and non-root).

## Attack map

sub-agent-compact
- hooks say(row)/warnOnce/option-error/resolve-error: one level-log entry per warning (R7g) — no-attack: pinned by log-write-failure/forward-adapter tests (red-r7g log).
- hooks plog/pluginLogFailed/flushDecisions: both logs failing at once could loop failure rows — no-attack: plugin-log-failed maps to no level (pluginLogEvent undefined) and flushDecisions dedups by logFailures key; the chain terminates.
- hooks record+SkipFold+retainLogs+session.end drain: skip-heavy session evicts evidence / end rows lost — no-attack: forward-adapter 'the decision log through the adapter' + log.test SkipFold cases.
- hooks startOver (/clear, resume): stale handoff installs into a new conversation — no-attack: forward-adapter '/clear' case.
- hooks compact-unseen drop: stale handoff after unhandled compaction (R6) — no-attack: forward-adapter 'drops the stored handoff' (red-r6 log).
- hooks partyOf canUse / nudgeFor: party without forward_handoff nudged toward it (R7j) — no-attack: forward-adapter disallowedTools/tools cases (red-r7j).
- hooks limitOf/forcedPoint/rereadWindow: 600k main on a 200k model held past the window (R5) — no-attack: forward-adapter window-changed + clamp cases.
- hooks session.compact unanswered: prompt-too-long recovery ask skipped below limit (R5) — no-attack: decisions + forward-adapter unanswered cases.
- hooks forward recovery after reload (R7c): lost stored handoff — no-attack: forward-adapter reload case + forward-record recoverForward (red-r7c).
- hooks agent.spawn cwd (R7l): relative path rebuilt under main's cwd — no-attack: forward-adapter spawn case (red-r7l).
- hooks Haiku hands-off (R3): any nudge/hold/brief for a Haiku party — no-attack: decisions + forward-adapter + self-compact Haiku cases (red-batch1, red-r3).
- hooks turn.complete own compaction resets main — no-attack: unseen-compact.test case.
- log.ts DECISION_APPEND_SCRIPT/LOCK_FN: concurrent writers across a rotation lose rows; stale lock blocks — no-attack: log.test 'four writers', 'waits on a lock', 'breaks a lock'.
- log.ts RETENTION_SCRIPT: deletes a foreign file, the session's own log or a symlink target — no-attack: log.test retention case (only UUID-shaped names, self spared, `file()` refuses links).
- log.ts APPEND_SCRIPT chmod/lock/BSD wc: macOS level log never writes (R1), rotation race (R7h) — no-attack: log.test 'plugin log under BSD chmod' + 'holds the plugin log directory lock'.
- limits.ts OPTION_KEYS/nudgeAfterLimit/logFile fallback (R7f) — no-attack: decisions.test option cases.
- window.ts modelWindow Haiku 5 → 1M — no-attack: a Haiku party's window feeds nothing after R3 (decide passes `haiku` first).
- decide.ts unanswered — no-attack: decisions.test.
- agents.ts hasTool/names/AGENT_CACHE_MS/plugin dirs (R7e) — no-attack: agent-fs.test cases.
- agent-fs.ts read stat guard (R7d) — no-attack: agent-fs.test (red-r7den).
- frontmatter.ts frontmatterList — no-attack: agent-fs.test list shapes.
- look-back.ts attachments/bookkeeping/null entry/demote/no-boundary (R7a, R7k) — no-attack: look-back.test (red-r3-bookkeeping, red-r7ak).
- forward-record.ts forwardRoots/recoverForward (R7b, R7c) — no-attack: forward-record.test (red-r7b, red-r7c).
- parties.ts held/forget — no-attack: parties.test; docblock of compacted() sat above forget() — fixed (finding A1).
- register.ts haikuServed, forward-args forwardHoldText, decide HAIKU_HOLDS removed — RETIRE: their tests left with them (grep: no test names them).
- README/CHANGELOG/plugin.json/.gitignore — no-attack: docs; badge proven by release-check non-root.
effort-live-control
- plugin-log.ts chmod/dash-dir (R1) — no-attack: plugin-log.test BSD shim cases (r1-effort-red.log).
- hook flushPlugin record(plugin-log-failed): both logs failing could loop — attack E1: PluginLogQueue error only on the first failure of a streak; adapter test with run and fs.write both failing must end with bounded notices.
cache-live-control
- plugin-log.ts chmod/dash-dir (R1) — no-attack: plugin-log.test BSD shim cases (r1-cache-red.log); no decision log in this plugin (review T5).
- README badge 234 → 254 (non-root Linux count) — fixed.
professor
- templates/global|generated|project agents, .claude/agents (R7m) — no-attack: roster test 'never compact' + templates.mirrors/generated-render gate step (gate PASS).
- infra/fence/checks.sh plugin-lab step + its two tests — no-attack: checks_test/templates_steps_test in gate.
- infra/plugin-lab/** (49 files) — no-attack here: its self-tests + shellcheck are templates.plugin-lab (gate PASS 63.5s); live half not run by ruling.
- docs, manifest, jscpd baseline — no-attack: check-templates manifest/clone PASS.

## Findings

| project | source | area | failing test | reproduction | expected | status |
|---|---|---|---|---|---|---|
| cache-live-control | checks | README tests badge | release-check (badge not checked by this repo's script) | non-root fence run passes 254, badge said 234 | badge = 254 | fixed |
| sub-agent-compact | attack | src/parties.ts docblock | none (doc) | compacted()'s docblock sat above forget() | each docblock above its own method | fixed |
| effort-live-control | sweep | tests/plugin-log.test.ts BSD case | n/a | `expect(await exec(...)).toBeTruthy()` catches nothing (exec rejects on failure) | plain await | fixed |
| effort-live-control | review (F1 LOW) | README decision-log rows / Logs | n/a (doc) | README said decision logs unchanged; `plugin-log-failed` row unnamed | README names `plugin-log-failed` (`error`), once per failing streak | fixed |
| effort-live-control | review (F2 LOW) + attack | hooks session.end | tests/adapter.test.ts 'a failing plugin-log write also reaches the decision log…' (slowWrite fixture, no tick) | logs/gate-r1-red-effort-end-flush.log: `expected [] to have a length of 1 but got +0` | row on disk when session.end returns: `await rt.writing` | fixed (green: logs/gate-r1-green-effort-end-flush.log) |
| professor (pfm) | checks | pfm/internal/spawn TestCodexRefusingAnEmptyNameIsReportedAsSuch | spawn_test.go:486 under `iso cover pfm` load | logs/gate-r1-cover-pfm.log; isolated -count=20 ok; cover rerun green | rename-failure warning names Codex's refusal regardless of load | outside the flight (pfm/ untouched; timing flake) |
| sub-agent-compact | review (F1 HIGH) | hooks turn.complete / parties.turnEnded | forward-adapter 'holds the next ask again after an interrupt or an API error…' | held ask, Esc/API error, next ask passed `unanswered` | next turn's ask held again | fixed (logs/gate-r1-sac-f1.log red→green) |
| sub-agent-compact | review (F2 MED) | hooks session.end bound | forward-adapter 'starts main over within the end bound while the log append hangs…' | hung append at /clear: end never returned, startOver never ran | writes bounded by next.budget less 300 ms core reserve; startOver always; cut rows counted on screen; rows pinned to their session's path | fixed (logs/gate-r1-sac-f2.log red timeout→green) |
| sub-agent-compact | review (F3 MED) | log.ts RETENTION_SCRIPT ours() | log.test retention 'never the session's own or another file' (foreign `<uuid>.jsonl`) | foreign transcript-shaped file deleted | only files whose first line is a decision row | fixed (logs/gate-r1-sac-f3f6.log) |
| sub-agent-compact | review (F6 LOW) | retention age | log.test 'just past the age' | `-mtime +14` spared 14d+1h | `-mmin +days*1440`; README/CHANGELOG count wording | fixed (logs/gate-r1-sac-f3f6.log) |
| sub-agent-compact | review (F4,F7,F9 LOW) | README / plugin.json claims | n/a (doc) | logger-ready "per session"; save dir; retention-failed unnamed | per process; HOME state root; row named | fixed |
| sub-agent-compact | review (F5 LOW) | log.ts brief-error level | log.test level table | mapped error, said warning | warning in both | fixed (logs/gate-r1-sac-lows.log red) |
| sub-agent-compact | review (F8 LOW) | hooks previous path | forward-adapter 'chains from a record kept under the config directory…' | printed nonexistent HOME path | first root where it exists | fixed (logs/gate-r1-sac-f8.log) |
| sub-agent-compact | review (F10 LOW) | hooks rereadWindow | forward-adapter 'reads each model's window once…' | usage() per request under a router | per-model memo | fixed (logs/gate-r1-sac-lows.log red) |
| sub-agent-compact | review (F11 LOW) | hooks plog timeout | none | 5 s timeout < lock wait | 15 s | fixed; older-version unlocked rotation race: outside the flight (older releases) |
| sub-agent-compact | review (F12 LOW) | log.ts LOCK_FN dead-holder break | none | check-then-rm race between two waiters | atomic break | residual (needs a different lock primitive: design decision) |
| sub-agent-compact | review (F13 LOW) | hooks nudgeFor compact tool | forward-adapter 'toward the compact tool only when it has it' | nudged without the tool | no nudge | fixed (logs/gate-r1-sac-lows.log red) |
| sub-agent-compact | review (F14 LOW) | forward-record recoverForward | forward-record.test saved-to words / outside roots / `..` | first match model-chosen | last clause, inside forwardRoots | fixed (logs/gate-r1-sac-lows.log red) |

# gate r2 — compaction-fixes (lander af7877420207c454c)

## Round-1 professor review findings settled (review-r1/professor/review.md)

Red proof: logs/gate-r2-lab-redgreen.log — each fix re-broken alone in a fence copy of infra/plugin-lab (group g1..g12), green on the tree (g0: observe 42/0, meter 8/0, run 16/0, lab 99/0).

| finding | fix in tree | pinning test | red (re-break) |
|---|---|---|---|
| F1 ledger fails open on interrupt | lab: RUNNING row at cap before spend; lab_ledger_record swaps it for the final row | lab_test 'interrupted run leaves one ledger row' / 'counts at its cap' | g1: 3 FAIL |
| F2 kill check = binary path only | meter.sh lab_stop greps `$LAB_CLAUDE --setting-sources project --settings $LAB_RUN/settings.json `, refuses empty pid/path/run | lab_test 'live chat on the same binary survives lab stop' | g2: FAIL |
| F3 TMUX=/tmp/outer in lab_test | `TMUX=$T/outer` | scratch-path gate regex over the 38 untracked code files: 0 hits (checked by hand: the gate scans tracked files only) | n/a |
| F4 shared /tmp root unchecked | run.sh lab_root_safe (owner, no group/other write up to a sticky or root-owned dir), umask 077 | lab_test 'unsafe-root' | g4: 2 FAIL |
| F5 could-not-look read as FAIL/GAP | r2: observe.sh witness_match (row without tokens → ERROR witness-unreadable rc2), decision_first (empty / unparseable line 1 → rc2), reply_has + read_turns (unreadable transcript → rc2), fill_until_boundary (no debug.txt → rc2, never GAP) | observe_test 6 new cases; GAP case now with a debug.txt | g5: 6 FAIL |
| F6 slash beat drops meter rc | drive.sh slash `lab_after_wait; return $?` | r2: lab_test 'a slash over the cap is STOPPED' | g6: FAIL |
| F7 prune rm -rf any old dir | prune takes only `{scenario}-YYMMDD-HHMMSS[-n]` names | lab_test 'prune takes run folders only' | g7: FAIL |
| F8 bash 4.0–4.3 admitted | guard needs 4.4 | lab_test 'bash-4.3' | g8: 2 FAIL |
| F9 doc tsconfig / status-hash comment | plugin-lab.md Adopt step 2 + verdict.sh comment corrected | doc | n/a |
| F10 decoy never a candidate | decoy is `exec -a` lookalike of the claude argv | same as F2 | g2 |
| F11 meter ignores dropped calls | r1: noTimestamp; r2: badLines at the final (--final) read only — a poll may see a line mid-append; 49 real lab runs' meter.json: badLines 0, noTimestamp 0 | meter_test 'undated calls', 'malformed lines … final', 'a poll reads past a line mid-append' | g11: 2 FAIL |
| F12 --budget without --ledger | lab refuses `--budget` without `--ledger`; doc says so | lab_test 'budget without a ledger refused' | g12: FAIL |

## Gate runs r2

- plugins full, fence as uid 65534 (logs/gate-r2-full-plugins.log): sac `Tests 733 passed | 1 skipped (734)` typecheck rc0, release-check FAIL `README tests badge says 724, the run passes 733` (badge set last); effort `Tests 547 passed | 3 skipped (550)` PASS; cache `Tests 254 passed | 1 skipped (255)` PASS. claude CLI MISSING in fence.
- professor full: `dev.sh iso gate` — `all steps passed.` STEPS PASS 104.4, EGRESS PASS, RUN DIR /tmp/compaction-fixes/timing/run.I6wGoO, EXIT 0 (logs/gate-r2-full-professor.log)
- professor static: `check-templates.sh` over the 93 flight files (submodule pointers excluded: not files), all PASS (logs/gate-r2-static-professor.log)
- plugins static: `npm run validate:plugin` on host per worktree plugin, `✔ Validation passed` x3 (logs/gate-r2-validate-plugins.log); README marketplace-install-line notice is advisory and pre-existing
- pfm coverage floor: running → logs/gate-r2-cover-pfm.log

## Review tier r2

Sink grep (TRUST TRACER seat, adapted to TS/shell: fs/read/write/rename/unlink/mkdir/chmod/spawn/exec/env/path.join/rm/mv/find/mktemp/eval/$()/redirect/kill) over added production lines + owned untracked files.

| repo | tier | sink hits | changed lines | sandbox |
|---|---|---|---|---|
| professor | reviewer-pro | tracked 0 + untracked 214 (infra/plugin-lab shell) | tracked 47 files +103/-73 (incl. 3 submodule pointers), untracked 49 files / 2793 lines | review-r2/professor/ |
| sub-agent-compact | reviewer-pro | 42 | 30 files +1758/-340, prod 15 files +826/-206 | review-r2/sub-agent-compact/ |
| effort-live-control | reviewer-pro | 3 (chmod in APPEND script) | 6 files +70/-6, prod 2 files +11/-3 | review-r2/effort-live-control/ |
| cache-live-control | reviewer-pro | 3 (chmod in APPEND script) | 4 files +41/-3, prod 1 file +5/-2 | review-r2/cache-live-control/ |


## Gate runs r2 (closing)

- pfm coverage floor: `Total test coverage: 85.4% (71619/83861)`, all steps passed, EXIT 0 (logs/gate-r2-cover-pfm.log)
- plugins full after review fixes (logs/gate-r2-full-plugins-2.log): sac `Tests 736 passed | 1 skipped (737)` but `Errors 1 error` (EPIPE, tests/log.test.ts:255) → vitest rc=1; release-check `FAIL README tests badge says 724, the run passes 736`. Both fixed (rows C1, C2).
- plugins full, closing (logs/gate-r2-full-plugins-3.log): sac `Tests 736 passed | 1 skipped (737)` vitest rc=0 typecheck rc=0 `PASS release 0.7.0-dev.23: versions agree, CHANGELOG dated, README badge counts 736 passing tests`; effort `Tests 548 passed | 3 skipped (551)` rc=0 typecheck rc=0 `PASS release 0.3.0`; cache `Tests 254 passed | 1 skipped (255)` rc=0 `PASS release 0.6.0`; `all steps passed.`
- professor: closed by its last full run (logs/gate-r2-full-professor.log, `all steps passed.`); later lab fixes proven by their own tests + shellcheck in the fence (logs/gate-r2-lab-fixes2.log) and `check-templates.sh` over docs/dev/testing/{_index,plugin-lab}.md + infra/plugin-lab/** all PASS (logs/gate-r2-static-professor-2.log)
- plugins static closing: `validate:plugin` sac + effort `✔ Validation passed` (logs/gate-r2-validate-plugins-2.log)

## Attack map r2 (hunks changed in round 2)

| hunk | outcome |
|---|---|
| lab verdict.sh lab_ledger_record append-only + final row under lock | ATTACK: two lab runs sharing a --ledger finish together; a read-modify-write loses the other's row → budget under-counted. Pinned: verdict_test concurrent records/reserves (h1 red) |
| lab lab_ledger_reserve (lock, preflight, dup id, RUNNING row) | ATTACK: two runs pass the budget check before either reserves → both spend past the cap. Pinned: verdict_test concurrent reserves (h2 red); dup run id refused (h7 red) |
| run.sh lab_ledger_sum supersede by id, lock/unlock | ATTACK: RUNNING + final row of one run counted twice → false STOPPED. Pinned: run_test supersede (h1/h9 red) |
| observe.sh decision_first blank line 1 | ATTACK: blank first line read as "not start" FAIL instead of could-not-look. Pinned: observe_test rc2 (h3 red) |
| observe.sh lab_pane_save rc + pane_lacks rc2 | ATTACK: capture fails, empty pane reads as "lacks" PASS. Pinned: observe_test (h4 red) |
| observe.sh fill range from>to | ATTACK: reversed range loops zero times, reads GAP/PASS. Pinned: observe_test rc2 (h5 red) |
| verdict.sh passport_spend STOPPED --final | ATTACK: STOPPED run's spend read from a poll mid-append. Pinned: verdict_test (h6 red) |
| isolate.sh hook command @sh quoting | ATTACK: a run dir with a quote/space breaks the hook command. Pinned: isolate_test (h8 red) |
| lab_test comment (F10), plugin-lab.md ledger paragraph | no-attack: prose |
| sac turn.complete try/finally turnEnded | ATTACK: rejected turn.complete chain leaves the hold → next turn's ask passes `unanswered`. Pinned: forward-adapter.test.ts (s1 red) |
| sac levelRowsWriting + afterOrNever what | ATTACK: session end cut while level rows still writing, message names only decisions. Pinned: forward-adapter.test.ts (s2, s3 red) |
| sac README F1/F3, plugin.json F5 | no-attack: prose |
| effort notice() → logFallback | ATTACK: screen door throws while the decision-log write fails → session.end rejects. Pinned: adapter.test.ts 'session.end still ends when the decision-log write fails and the screen door throws' (logs/gate-r2-effort-f3-red.log / -green.log) |
| sac tests/log.test.ts runSh stdin EPIPE guard | no-attack: test harness; the race is a child exiting before reading stdin; red not reproducible on demand (timing) |
| README tests badges sac 736, effort 548 | no-attack: release-check pins sac; effort's release-check reads no tests badge |

## Validity sweep r2

Test files touched in r2: infra/plugin-lab/tests/{lab_test.sh,lib/run_test.sh,lib/verdict_test.sh,lib/observe_test.sh,lib/isolate_test.sh}, plugins sac tests/{forward-adapter.test.ts,log.test.ts}, effort tests/adapter.test.ts. Each new case calls the symbol it names and was watched red against a re-break (logs/gate-r2-review2-redgreen.log, gate-r2-sac-redgreen.log, gate-r2-effort-f3-red.log). Fix to the sweep: lab_test interrupted-run case shared the global ledger with other roots and became flaky once F7 refused duplicate ids — given its own ledger. No duplicate tests or catch-nothing assertions found among the r2 additions; nothing removed.

## Findings r2

| # | project | source | area | failing test | reproduction | expected | status |
|---|---|---|---|---|---|---|---|
| P-F1/F9 | professor | review | lab ledger rewrite race | verdict_test concurrent records | two runs record on one ledger at once | both rows kept | fixed |
| P-F2 | professor | review | lab budget check/reserve split | verdict_test concurrent reserves | two runs start under one cap | second refused | fixed |
| P-F3 | professor | review | observe decision_first blank line | observe_test | decisions file with blank line 1 | rc2 could-not-look | fixed |
| P-F4 | professor | review | lab_pane_save/pane_lacks | observe_test | capture fails | rc2, never PASS | fixed |
| P-F5 | professor | review | fill_until_boundary range | observe_test | from>to | rc2 | fixed |
| P-F6 | professor | review | passport_spend STOPPED | verdict_test | STOPPED run | --final read | fixed |
| P-F7 | professor | review | duplicate run id | verdict_test | same id reserved twice | refused | fixed |
| P-F8 | professor | review | isolate hook quoting | isolate_test | run dir with quote | hook runs | fixed |
| P-F10 | professor | review | lab_test comment claim | n/a | read | true comment | fixed |
| S-F1b | sub-agent-compact | review | turn.complete rejection keeps hold | forward-adapter.test.ts | next(e) rejects | hold cleared | fixed |
| S-F1a | sub-agent-compact | review | no-usage step inside a turn keeps hold → later ask passes `unanswered` | none | another hook answers a step without a request, then an automatic ask | held ask stays held | residual: telling a no-usage step from a prompt-too-long refusal needs a design decision on the engine's step contract |
| S-F2 | sub-agent-compact | review | session-end cut message / level rows | forward-adapter.test.ts | cut during level-row write | message names level rows | fixed |
| S-F3 | sub-agent-compact | review | retention in a shared logFile folder | n/a | doc | README says the folder is the plugin's own | fixed |
| S-F4 | sub-agent-compact | review | lock break check-then-rm (r1 F12) | none | two waiters break a dead holder's lock | one holder | residual: atomic break needs a design decision (row 80) |
| S-F5 | sub-agent-compact | review | plugin.json Haiku claim | n/a | doc | names refusal | fixed |
| E-F3 | effort-live-control | review | notice() unguarded ui.log | adapter.test.ts | log write fails + ui.log throws | session ends | fixed |
| E-F4 | effort-live-control | review | setup failure reaching decision log | n/a | read | — | no defect (dir refusal reaches it via flush out.error; flushPlugin cannot throw) |
| C1 | sub-agent-compact | checks | runSh stdin EPIPE (tests/log.test.ts) | 'passes over a log directory that does not exist' | full run: child exits before reading stdin | no unhandled error | fixed |
| C2 | sub-agent-compact + effort | checks | stale README tests badge | release-check | full run | badge = passed count | fixed |

# gate r3 — compaction-fixes (root rulings on r2 residuals S-F1a, S-F4)

## Gate runs r3

- sub-agent-compact full, fence as uid 65534 (logs/gate-r3-full-plugins.log): `Tests  740 passed | 1 skipped (741)` vitest rc=0, typecheck rc=0, `PASS release 0.7.0-dev.23: versions agree, CHANGELOG dated, README badge counts 740 passing tests`; effort `Tests  548 passed | 3 skipped (551)` PASS; cache `Tests  254 passed | 1 skipped (255)` PASS; `all steps passed.`; lock tests repeated x5 `Tests  5 passed | 76 skipped (81)` each.
- validate:plugin sac `✔ Validation passed` (logs/gate-r3-validate-sac.log).
- professor static: no professor file touched in r3 (all edits inside the sub-agent-compact submodule worktree); none run.

## Attack map r3

| hunk | outcome |
|---|---|
| hooks turn.step `else if (result.toolUses.length > 0) st.parties.answered(key)` + parties.answered | ATTACK: a hook answers a step without a request (usage null, tools asked) → the turn's next auto ask passes `unanswered` below the limit. Pinned: forward-adapter 'holds the next ask again after a step with no usage that asks for tools…' (red logs/gate-r3-f1a-red.log, green logs/gate-r3-f1a-green.log). Counter-pin R5: '…no usage and no tool uses (the failed request) as unanswered' |
| log.ts LOCK_FN claim-based break/release | ATTACK: waiter B stalls between reading the dead holder and breaking; A breaks and takes the lock; B removes A's lock and appends under it. Pinned: log.test 'keeps the lock a second waiter broke and took…' (cat PATH shim stalls B's second read of the dead token). ATTACK: holder outlived by an age break releases and removes its successor's lock. Pinned: log.test 'leaves the successor's lock…'. Both red on the r2 LOCK_FN (logs/gate-r3-f4-red.log), green (logs/gate-r3-f4-green.log: 81 passed) |
| README unanswered sentence, badge 740 | no-attack: prose; release-check pins the badge |

## Findings r3

| # | project | source | area | failing test | reproduction | expected | status |
|---|---|---|---|---|---|---|---|
| S-F1a | sub-agent-compact | review (ruled) | held ask after a no-usage step that asks for tools | forward-adapter 'holds the next ask again after a step with no usage that asks for tools answered the held ask within the turn' | held ask, step usage null + 1 tool use, auto ask | skip (held again) | fixed |
| S-F4 | sub-agent-compact | review (ruled) | LOCK_FN check-then-rm break; release rm -rf | log.test 'keeps the lock a second waiter broke and took…', 'leaves the successor's lock…' | B stalled before break while A takes the lock; age break then the old holder releases | A's / successor's lock kept, rows whole | fixed |
