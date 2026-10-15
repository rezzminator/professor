# 2026-10-15 — `check-pfm.sh` GAPs on indirect Go modules the checks never build

Observed: in the engine-sync-tidy phase-1 gate, `.claude/scripts/check-pfm.sh` printed `GAP install (pfm is missing: Go modules …)`, exit 3, inside the fence on a fresh box, and the lander reported that GAP as its one residual FAIL. Its install probe is `GOPROXY=off go -C pfm mod download`, which needs the whole module graph; the `pfm-dev-gomod` volume held every module the checks build but lacked indirect modules none of them needs. One `.claude/scripts/dev.sh iso install pfm` filled the volume and the same `check-pfm.sh` run passed.

Amend: .claude/scripts/check-pfm.sh#install probe (`# install:`) — probe only the modules the named checks build (the `go list -deps -test` set of the packages it vets and lints), so a module no check needs never GAPs the run; the GAP line keeps naming `dev.sh iso install pfm` for a module a check does need.
