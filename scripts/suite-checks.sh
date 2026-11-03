#!/bin/sh
# CHECK name PASS|FAIL|ERROR; exit 0 clean, 1 findings, 2 could not inspect.
set -eu
HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
exec python3 "$HERE/suite-checks/checks.py" "$@"
