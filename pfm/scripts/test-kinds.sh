#!/usr/bin/env bash
# test-kinds.sh — counts pfm's test functions by where they run, read from their file's name: a unit
# test runs on any host inside the unit net (internal/testjail/unitnet.go), where it starts no process
# and looks up no host tool, and scripts/unit-proof.sh proves it reaches nothing outside its temp
# directory; a real test, in a jail file `{name}_jail_test.go` or `{name}_jail_{os}_test.go`, runs on a
# real resource in the fence or on a hosted runner, its file's count ratcheted by arch-check C33.
#
#   scripts/test-kinds.sh [--list] [pfm-dir]      pfm-dir defaults to this script's module root
#
# Scope: every `func Test…(… *testing.T)` and `func Fuzz…(… *testing.F)` in a *_test.go file under
# pfm-dir, outside journey/, e2e/, testdata/ and dot directories. TestMain is not a test.
#
# Output (rc 0), one line:
#   KINDS unit {n} · real {n} · total {n}
# --list: one row per test instead, `{unit|real}\t{dir}\t{file}\t{func}`, dir relative to pfm-dir.
# BROKEN STATE: an unreadable pfm-dir, no go.mod there, an unreadable test file, or no test
# function found at all prints `KINDS ERROR {why}` and exits 2 — "could not look" never reads as zero.
set -uo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
list=0
if [ "${1:-}" = --list ]; then list=1; shift; fi
root="${1:-$HERE/..}"
command -v python3 >/dev/null 2>&1 || { echo 'KINDS ERROR python3 not on PATH'; exit 2; }
exec python3 - "$root" "$list" <<'PY'
import os
import re
import sys

root, as_list = sys.argv[1], sys.argv[2] == '1'
# A test in a jail file, `{name}_jail_test.go` or `{name}_jail_{os}_test.go`, is real.
JAIL = re.compile(r'_jail(?:_(?:linux|darwin))?_test\.go$')
TEST = re.compile(r'^func ((?:Test|Fuzz)\w*)\(\w+ \*testing\.[TF]\)', re.M)
SKIP_DIRS = {'journey', 'e2e', 'testdata'}


def die(why):
    print(f'KINDS ERROR {why}')
    sys.exit(2)


if not os.path.isdir(root):
    die(f'pfm dir {root} is not a readable directory')
if not os.path.isfile(os.path.join(root, 'go.mod')):
    die(f'{root} has no go.mod — not a Go module root')

rows = []
for directory, dirs, files in os.walk(root, onerror=lambda exc: die(f'cannot list {exc.filename}: {exc.strerror}')):
    dirs[:] = sorted(d for d in dirs if not d.startswith('.') and d not in SKIP_DIRS
                     and not (directory == root and d == 'tmp'))
    rel = os.path.relpath(directory, root)
    for file in sorted(f for f in files if f.endswith('_test.go')):
        path = os.path.join(directory, file)
        try:
            with open(path, encoding='utf-8') as handle:
                text = handle.read()
        except (OSError, UnicodeError) as exc:
            die(f'cannot read {path}: {exc}')
        kind = 'real' if JAIL.search(file) else 'unit'
        rows.extend((kind, rel, file, name) for name in TEST.findall(text))

if not rows:
    die(f'no test function found under {root}')
if as_list:
    for row in rows:
        print('\t'.join(row))
    sys.exit(0)
real = sum(1 for row in rows if row[0] == 'real')
print(f'KINDS unit {len(rows) - real} · real {real} · total {len(rows)}')
PY
