# 2026-10-19 — the foreman narrowed a brief item, returned DONE, and built on the host

Observed: the first benchmark run of the rewritten `flights-foreman` (the `pfm restart` brief) restarted the search backend only as a Docker container or probed a remote one, left a local non-Docker backend probed only, and returned `DONE`, against its rule "narrowing the brief yourself is never DONE". It also ran `go build` and `go vet` on the host, the second of four benchmark runs that night to build or vet outside the fence. It never used its effort buttons in 171 calls.

Amend: judgment — a host-build guard (a hook refusing raw `go build`, `go vet` and `go test` outside the fence) is the mechanism candidate; the every-item rule and the effort line (added after this run) need a second run before either is reworded.
