#!/usr/bin/env bash
# Gate fixture (PFM_GATE_FIXTURES=1, step fixture.shell-fail): a strict-mode script that dies two
# calls deep, so the step's ERR record names this file, the line and the call stack.
set -euo pipefail

innermost() { false; }
middle() { innermost; }
middle
