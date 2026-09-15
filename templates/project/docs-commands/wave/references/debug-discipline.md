# Debug Discipline — hangs, deadlocks, mystery failures

For invisible failure modes (hang, deadlock, "no output no error", intermittent, passes-alone-fails-in-suite, silent crash): **instrument, don't wait** — a 0%-CPU hang hangs forever; never re-run with `-v` hoping.

Symptom → meaning: ~0% CPU not exited = deadlock/blocked I/O, not slow · >2× expected runtime silent = hang · works-local-fails-CI = concurrency/env/isolation · passes-alone-fails-in-suite = shared state/fixture scope/DB residue · 1-in-N flake = race or external dep · silent crash no traceback = swallowed exception.

Five steps, in order:

**A — Confirm hang vs slow.** `ps aux | grep -E "pytest|node|python" | grep -v grep` — CPU `0.0` with elapsed growing = deadlock (kill: `kill -TERM <PID>; sleep 2; kill -0 <PID> 2>/dev/null && kill -KILL <PID>`); steady `>20%` = slow, profile instead; bouncing 0↔100% = blocked-I/O loop or retry storm, check logs.

**B — Hard wall-clock timeout BEFORE any re-run.** Python: pytest-timeout with `timeout = 120` + `timeout_method = "thread"` in pyproject (thread mode dumps the hung stack — the exact blocked line — and signal mode is unreliable for async). Shell: `timeout 60s <cmd>`. Node: `--testTimeout=60000` (Jest/Vitest/Playwright).

**C — Isolate the failing target with full capture:** `ENV_FILE=.env.test uv run pytest tests/…::test_x -v --tb=long -s 2>&1 | tee /tmp/debug.log` — isolation removes suite pollution, fixture-scope mismatch, and earlier tests holding DB locks as variables.

**D — Timing traces around every suspect await** when the timeout stack is ambiguous: `print(f"[T+{time.monotonic()-t0:.1f}s] {label}", flush=True)` before/after each — the await with no following trace is the deadlock. `flush=True` is load-bearing (Python buffers stdout off-TTY; prints otherwise arrive after death). Remove the traces once found.

**E — Query the layer below** (the trace says WHICH await; this says WHY):

- DB, while hung: `make -C {PROJECT} db-exec-test SQL="SELECT pid, state, wait_event, wait_event_type, query FROM pg_stat_activity WHERE state != 'idle';"` — `wait_event` reads: `ClientRead` = DB answered, client never read → protocol-level deadlock (classic: tz-aware datetime into `timestamp without time zone`) · `Lock`/`transactionid` = row lock, find the holder PID · `IO` = disk-bound · `null`+`active` = genuinely running, slow not dead.
- asyncio: `for task in asyncio.all_tasks(): task.print_stack()` (wire to SIGUSR1 to dump from outside).
- HTTP: `curl -v --max-time 10 <endpoint>` — curl hangs too = server; curl fast = client (query/fetch layer).
- Silent crash: grep swallowed exceptions — `grep -rn "except.*:\s*$\|except.*:\s*pass" {project}/src/ | grep -v test_` (the no-swallowed-exceptions law lives in root CLAUDE.md).
