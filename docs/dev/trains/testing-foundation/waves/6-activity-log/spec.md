# Wave 6 — the activity log: one structured log at every corner, per environment

**Why:** the engine has ~50 log calls, no central destination, no level switch, and only scattered one-off files (the reload worker's `reload-*.log`, a few installer probes). A failed lane step, a field report, or an alpha tester's bug cannot be diagnosed from what pfm writes today. **Law (the user's):** a lane step passes only on its asserted result AND a clean activity log.

## Framework

Go's standard `log/slog` — levels, structured fields, handlers per environment, no new dependency. One package, `pfm/internal/obs` (name checked against the tree before creation): `obs.Logger(ctx)` returns the process logger; `obs.With(ctx, "chat", id, "seat", n)` scopes it; `obs.Span(ctx, "reload.waitIdle")` logs entry, exit, duration and error of a unit of work.

## Destinations and environments

- One JSON-lines file per pfm home: `<paths home>/log/pfm.jsonl`, resolved through `paths` — so the fence, a test jail, a lane container and the live host each write their own file and can never mix (the environment separation IS the home jail; no second mechanism).
- Size-capped rotation written in-tree (keep N files of M MB; both in `pfm.config.json`), so no rotation library is added.
- Level by build and by switch: a `-alpha` `VERSION` defaults to `debug`, a release build to `info`; `PFM_LOG_LEVEL` and `pfm.config.json` `log.level` override; `PFM_LOG=stderr` mirrors to stderr for a foreground run. Tier U tests get a buffer handler from `obs.Test(t)` and assert on records.
- Every record carries: `ts`, `level`, `msg`, `cmd` (the pfm verb), `pid`, `version`, and where known `chat`, `seat`, `engine`, `sock`, `dur_ms`, `err`. Never a credential, a token, a prompt body, or a transcript line — a redaction test pins that.

## Coverage — every corner

Each `cmd/pfm` verb (entry, args shape, exit code, duration); every hook entry (`hookentry`: which hook, decision, reason); the MCP daemon (tool, target, result shape, duration); every tmux façade call (argv, exit, duration — at `debug`); every `deps.Runner` call (same); installer steps (each write/link/prune with its ledger row); reload/inject/self-compact state machines (each transition); reap/heal/kill decisions (what and why); usage hook fetches (seat, status, cache age); updatecheck. Existing `log.Printf` calls move onto it — one logging path, never two. The seams from Wave 3 are the choke points: `deps.RealRunner`, the tmux façade and `clock` make most of this one wrapper each, not 300 call sites.

## Reading it

`pfm log [--since 10m] [--level warn] [--chat X] [--cmd reload] [--follow]` — a filter over the JSONL; `pfm doctor` names the log path and its size. The lanes' `lib.sh` snapshots the file's offset before each beat and, after it, fails the beat on any `error` record not declared expected by that beat (`expect-log <pattern>`), attaching the beat's slice of the log to a red row.

## Proof

Red-first: a test per corner asserting the record exists with its fields (watched failing before the call is added); the redaction test; a level test per environment; `pfm log` filter tests; C22-style ratchet `C23-bare-log` counting `log.Print*`/`fmt.Fprint*(os.Stderr` outside `obs` and `cmd/pfm` user-facing output — baseline measured, only shrinks.
