# 2026-10-19 — fence runs die with exit 137 under heavy host load

Observed: at a 1-minute host load near 40, gate runs ended with exit 137 and "shared-run: the caller of run … is gone (heartbeat ended)"; the caller was alive, its heartbeat starved. Reruns at load under 15 passed.

Amend: infra/fence shared-run heartbeat — tolerate a starved heartbeat (longer grace, or a liveness check on the caller pid) before killing the run, and name load in the kill message.
