# RR — Is Dock force-quit of a lingering app killing unrelated coalition processes a known macOS bug?
Question: Does Dock/LaunchServices force-quit of an app whose main process had already exited (Chrome, pid 69809 reused) killing an unrelated coalition (Herdr + coding agents) match a known, publicly reported macOS bug?

Verdict: RELATED-ONLY. Third-party developers have reported the same LaunchServices coalition sweep killing their processes. These are GitHub issues, not Apple-acknowledged bugs, and none gives an FB number. The closest case is a Herdr issue in which force-quitting a Chrome launched from a pane shut down the Herdr server ([herdr #2407](https://github.com/herdrdev/herdr/issues/2407)). Another report describes a force quit on the tile of an app whose main process had already exited ([roundhouse #665](https://github.com/getroundhouse/roundhouse/issues/665)).

## Map
- herdr #2407 (opened Aug 6 2026, macOS 27.0 betas 26A5388g to 26A5425a) ([link](https://github.com/herdrdev/herdr/issues/2407))
  - Logs: `_LSForceQuitApplication: appPID=62534 coalition=139998`, then "coalition pids to consider killing" and `killPID: pid=... hard=NO succeeded`.
  - In that report, Chrome had been launched from inside Herdr's process chain, so the two genuinely shared a coalition.
  - The Aug 22 occurrence logged `coalition=0` for Chrome but still listed the Herdr server as a kill candidate. That is an unexplained attribution anomaly.
  - The issue was closed "not planned" in a pre-0.9 cleanup, not for a technical reason. It cites no FB number.
  - How it differs from your case: there, Chrome came from Herdr, so the shared coalition was real. In yours, Chrome was unrelated and pid 69809 had likely been reused.
- roundhouse #665 (Nov 2026) ([link](https://github.com/getroundhouse/roundhouse/issues/665))
  - Force Quit on a Dock tile whose main `look` process had already exited at 17:51, and that LaunchServices kept alive "until its subordinate processes exit". At 18:08, "coalition pids to consider killing" listed 53 pids, including the hub, and each one received SIGTERM.
  - Stated mechanism: a process inherits its parent's coalition, only launchd can assign a new one, and `setsid` does not change it.
  - How it differs: the killed processes were descendants of the app's own coalition. The report does not discuss pid reuse.
- diri PR #693 (2026-10-04) ([link](https://github.com/cristicretu/diri/pull/693))
  - Force-quitting a Chrome Canary that an agent's MCP server had opened produced "coalition pids to consider killing: [167 pids]" and SIGTERMed every agent.
  - Workaround: each agent runs as a transient launchd job, described as "the only way into a fresh coalition without private entitlements".
  - The PR does not cover jetsam vs resource coalition ids.
- What these reports share
  - The Dock path goes through private `_LSForceQuitApplication`, which sweeps the app's coalition and spares foreground or visible apps and BTM-managed jobs (roundhouse #665).
  - `NSRunningApplication.forceTerminate()` does not do this sweep (diri #693), and scripted replays failed because the private path is entitlement-gated.

## Coverage
- Public reports (bug triage): partial. Three third-party reports found. No Apple acknowledgement, FB number or release note was found, and none of the listed forums was searched.
- Mechanism (OS internals): partial. Coalition inheritance and the LS sweep are described. Stale or reused pid attribution, and jetsam vs resource coalition ids, remain unexplained.
- Digging ended: no diggers spawned. This was a tier-1 pass run on the opening search and 3 fetches.

## Verification
All facts come from quotes in the three fetched pages. No separate verification round was run.

## Open rabbit holes
- undug: whether Dock resolves the coalition from a stale appPID/coalition id cached at launch. Your coalition 189852 held none of Chrome's helpers, which suggests a reused-pid or stale-coalition bug. No source covers this.
- undug: why herdr's `coalition=0` force quit still listed the server.
- undug: Apple Developer Forums, Apple Community, OpenRadar, HN/Reddit, macOS 26/27 release notes.
- undug: jetsam vs resource coalition ids.

Queries run: `"coalition pids to consider killing" Dock force quit`; `macOS force quit app from Dock killed terminal tmux processes unrelated` (generic results only).
