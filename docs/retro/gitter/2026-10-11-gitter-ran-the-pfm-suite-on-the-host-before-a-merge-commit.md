# 2026-10-11 — gitter ran the pfm suite on the host before a merge commit

Observed: landing the watch-footprint flight, gitter confirmed the gate by running pfm tests on macOS outside the fence; the frozen-tmux-server ladder rung and some install/doctor tests failed there while the fence gate was green, leaving an unexplained host-only red in the return.

Amend: .claude/agents/gitter.md#COMMIT hard gate — "confirm it yourself where it is cheap" names how: read the gate log or rerun through `.claude/scripts/dev.sh iso`, never `go test` on the host (root CLAUDE.md § Testing).
