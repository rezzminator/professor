# Tracing design

| Topic | File | Covers |
| --- | --- | --- |
| ledger | [ledger.md](ledger.md) | The design `tracer` and `mapper` share: the family, why a ledger, the script's commands, buckets, the row format and kinds, what verify checks, widening, absence, the lint, mechanisms that replaced rules, what was left out and why, the evidence, measuring a run, the surfaces in sync |
| tracer | [tracer.md](tracer.md) | One question answered from code: the run, who reads, which buckets are read, the report, the return, failure reports, the measured scoreboard |
| mapper | [mapper.md](mapper.md) | One target's whole area: the run, how it differs from tracer, probing the empty facets, the map, the return, failure reports, the measured scoreboard |
