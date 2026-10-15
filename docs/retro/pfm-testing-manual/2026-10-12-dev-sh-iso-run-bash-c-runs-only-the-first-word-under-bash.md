# 2026-10-12 — `dev.sh iso run bash -c '…'` runs only the first word under bash

Observed: `iso run bash -c 'go -C pfm test …'` printed `go help`: the wrapper hands one shell string to the container, so `bash -c` received `go` alone. An earlier `iso run bash -c 'set -e; K=…; cd $K; python3 -m unittest …'` passed by coincidence — `bash -c set` was a no-op and the rest ran under the outer shell. The working form is one quoted string: `iso run 'go -C pfm test ./internal/{pkg}/'`.

Amend: .claude/commands/pfm-testing-manual.md — `iso run` takes one quoted shell string; never `bash -c`.
