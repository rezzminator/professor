# Concurrency sweep

No successful sweep has established a concurrency knee for this tree. The initial gate uses conservative `-p 1 -parallel 1` flags because the default-concurrency provisional capture failed under sustained shared-host load. This is a safe starting point, not a measured sweet spot.

The fenced sweep writes raw per-run data under `/tmp/{project}/timing/` and replaces this report only after complete, successful measurements. The host leg is excluded by this train's fence-only rule. See [timing.md](timing.md) for measurement prerequisites and failure semantics.

Re-run the sweep at W5 close. `--wait-quiet` records low-load evidence when the host settles, but sustained load warns and proceeds after at most five minutes instead of holding the train indefinitely.
