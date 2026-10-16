# 2026-10-15 — staticcheck is absent from the fence image

Observed: the engine-sync-tidy phase-2 gate needed engine-sync's pinned `staticcheck` (`plugins/engine-sync/scripts/tool-versions.txt`: v0.8.1); the pfm-dev image does not carry it, and the fence is offline. The lander installed it in-container from the module cache: `GOBIN={scratch}/bin GOPROXY=file://$(go env GOMODCACHE)/cache/download GOSUMDB=off go install honnef.co/go/tools/cmd/staticcheck@v0.8.1`.

Amend: infra/fence/tools.env — pin `STATICCHECK_VERSION` at the version engine-sync's `scripts/tool-versions.txt` names and install it through `infra/fence/tools.sh`, so the image carries it.
