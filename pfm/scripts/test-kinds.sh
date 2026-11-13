#!/usr/bin/env bash
# test-kinds.sh — counts pfm's unit-bundle test functions by the worst thing each one touches,
# the measure of the unit-isolation program (a unit test may use memory and its own temp
# directory, nothing else; every other test's decision moves to the journey or the installer suite).
#
#   scripts/test-kinds.sh [--list] [pfm-dir]      pfm-dir defaults to this script's module root
#
# Scope: every `func Test…(… *testing.T)` and `func Fuzz…(… *testing.F)` in a *_test.go file under
# pfm-dir, outside journey/, e2e/, testdata/ and dot directories. TestMain is not a test.
# A test's kind is the worst marker its own body or any test-file function of its directory it
# names (by identifier, transitively) carries, comment lines skipped. A test in a jail file,
# `{name}_jail_test.go` or `{name}_jail_{os}_test.go`, is real: a test kept on a real resource until a
# fake or the journey can play it, its file's count ratcheted by arch-check C33; any other function of
# a jail file marks the tests naming it tmux:
#   tmux         exec of "tmux", a "new-session" argument, or a function of a jail file
#   process      exec.Command, os.StartProcess, syscall exec, an executable written to run
#                (testjail.WriteExecutable and its fakes), a built binary, a PTY, mockengine
#   home-sqlite  SQLite's own file: testjail.DatabaseFiles, or a database built with sql.Open; any
#                other open goes through the SQLite door, which serves it from memory in a test process
#   temp-dir     t.TempDir(), os.MkdirTemp, os.CreateTemp, testjail.ShortRoot / CreateShortRoot, a
#                testjail home built in one (Fleet, FleetEnv, InstalledHome, CleanHome), or an
#                executable written never to run (testjail.WriteExecutableNeverRun)
#   nothing      none of the above: memory only
# A method is reached only through a selector (`x.Name`), a func by its bare name or a selector; a name
# inside a string literal reaches nothing. Markers are read from source, so the kind is what the test
# text reaches, an estimate both ways: a production call that reaches a real dependency on its own, or
# a closure held in a `var (…)` block, is not seen (too low); a method of the same name on another
# receiver is (too high).
#
# Output (rc 0), one line:
#   KINDS unit {n} (nothing {n} · temp-dir {n}) · real {n} · move {n} (home-sqlite {n} · process {n} · tmux {n}) · total {n}
# --list: one row per test instead, `{kind}\t{dir}\t{file}\t{func}`, dir relative to pfm-dir.
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
KINDS = ['nothing', 'temp-dir', 'home-sqlite', 'process', 'tmux', 'real']
MARKERS = [
    (4, re.compile(r'exec\.Command(?:Context)?\((?:[^,()]*,\s*)?"tmux"|"new-session"')),
    (3, re.compile(r'exec\.Command(?:Context)?\(|os\.StartProcess\(|syscall\.(?:Exec|ForkExec)\('
                   r'|testjail\.(?:WriteExecutable|PFMBinary|MockEngineBinary|GoBuild|PTYCommand|HungTmux'
                   r'|FakeEngineSync|WriteLoggedOut\w*)\b|\bmockengine\.')),
    (2, re.compile(r'testjail\.DatabaseFiles\b|\bsql\.Open\(')),
    (1, re.compile(r'\.TempDir\(\)|os\.(?:MkdirTemp|CreateTemp)\(|testjail\.(?:ShortRoot|CreateShortRoot|Fleet|FleetEnv|InstalledHome|CleanHome'
                   r'|WriteExecutableNeverRun)\b')),
]
# A test in a jail file, `{name}_jail_test.go` or `{name}_jail_{os}_test.go`, is real.
JAIL = re.compile(r'_jail(?:_(?:linux|darwin))?_test\.go$')
# A top-level func or method (its key `.{name}`, reached only through a selector), or a package-level
# `var {name} = func(` closure, read as a function.
FUNC = re.compile(r'^(?:func (\([^)]*\)\s*)?([A-Za-z_]\w*)|var ([A-Za-z_]\w*) = func\b)')
TEST = re.compile(r'^func ((?:Test|Fuzz)\w*)\(\w+ \*testing\.[TF]\)')
WORD = re.compile(r'(\.?)\b([A-Za-z_]\w*)\b')
STRING = re.compile(r'"(?:[^"\\\n]|\\.)*"|`[^`]*`')
SKIP_DIRS = {'journey', 'e2e', 'testdata'}


def die(why):
    print(f'KINDS ERROR {why}')
    sys.exit(2)


if not os.path.isdir(root):
    die(f'pfm dir {root} is not a readable directory')
if not os.path.isfile(os.path.join(root, 'go.mod')):
    die(f'{root} has no go.mod — not a Go module root')


def split_funcs(text):
    """Top-level gofmt'd functions: (name, signature line, body lines without comment lines)."""
    funcs, current = [], None
    for line in text.split('\n'):
        if current is None:
            match = FUNC.match(line)
            if not match:
                continue
            name = '.' + match.group(2) if match.group(1) else match.group(2) or match.group(3)
            current = (name, line, [line])
            if line.rstrip().endswith('}') and '{' in line:
                funcs.append(current)
                current = None
            continue
        if not line.lstrip().startswith('//'):
            current[2].append(line)
        if line == '}':
            funcs.append(current)
            current = None
    return funcs


def rank(body):
    for value, pattern in MARKERS:
        if pattern.search(body):
            return value
    return 0


rows = []
for directory, dirs, files in os.walk(root, onerror=lambda exc: die(f'cannot list {exc.filename}: {exc.strerror}')):
    dirs[:] = sorted(d for d in dirs if not d.startswith('.') and d not in SKIP_DIRS
                     and not (directory == root and d == 'tmp'))
    tests = sorted(f for f in files if f.endswith('_test.go'))
    if not tests:
        continue
    own, refs, found = {}, {}, []
    for file in tests:
        path = os.path.join(directory, file)
        try:
            with open(path, encoding='utf-8') as handle:
                text = handle.read()
        except (OSError, UnicodeError) as exc:
            die(f'cannot read {path}: {exc}')
        jail = bool(JAIL.search(file))
        for name, signature, body in split_funcs(text):
            joined = '\n'.join(body)
            value = 4 if jail else rank(joined)
            own[name] = max(own.get(name, 0), value)
            # A name inside a string literal calls nothing; `x.Name` may be a method or another file's func.
            refs.setdefault(name, set()).update(dot + word for dot, word in WORD.findall(STRING.sub('""', joined)))
            if TEST.match(signature):
                found.append((file, name, jail))
    names = set(own)
    worst = {}
    for file, name, _ in found:
        seen, stack, value = {name}, [name], 0
        while stack and value < 4:
            current = stack.pop()
            value = max(value, own.get(current, 0))
            for ref in refs.get(current, ()):
                for key in (ref, ref[1:]) if ref.startswith('.') else (ref,):
                    if key in names and key not in seen:
                        seen.add(key)
                        stack.append(key)
        worst[(file, name)] = value
    rel = os.path.relpath(directory, root)
    for file, name, jail in found:
        rows.append(('real' if jail else KINDS[worst[(file, name)]], rel, file, name))

if not rows:
    die(f'no test function found under {root}')
if as_list:
    for row in rows:
        print('\t'.join(row))
    sys.exit(0)
count = {kind: 0 for kind in KINDS}
for row in rows:
    count[row[0]] += 1
unit = count['nothing'] + count['temp-dir']
print(f"KINDS unit {unit} (nothing {count['nothing']} · temp-dir {count['temp-dir']}) · real {count['real']}"
      f" · move {len(rows) - unit - count['real']} (home-sqlite {count['home-sqlite']} · process {count['process']}"
      f" · tmux {count['tmux']}) · total {len(rows)}")
PY
