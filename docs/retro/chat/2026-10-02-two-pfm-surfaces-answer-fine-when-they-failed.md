# 2026-10-02 — two pfm surfaces answer "fine" when they failed

Observed: `pfm chat new` returned `the chat drew nothing within 30s` (exit 1) for ttl-n-5m-1 and ttl-s-1h-2, both running normally; `--settle 90` did not change the 30s check. `.claude/scripts/dev.sh iso run bash -c 'cd pfm && go test …'` printed `go: cannot find main module` and then `all steps passed`.

Amend: pfm chat new — the draw check honours `settle` and confirms the pane before reporting a failed launch; .claude/scripts/dev.sh#iso run — exit and report the command's own status, never "all steps passed" after it failed.
