#!/usr/bin/env bash
# Fixture-driven tests for scripts/unit-proof.sh's filter and verdict: each trace record the spec
# names is judged allowed or a reach row of its kind, a started program's own records drop after its
# exec row, a record the filter cannot read and an allow row without a reason are errors, the
# verdict's three outcomes print their line and exit 0, 1 or 2, the exec wrapper runs a binary as one
# traced process or as the parts its split table names, and the run's go test takes its package
# concurrency from PFM_FENCE_TEST_P.
#   bash pfm/scripts/unit-proof_test.sh
set -uo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUT="${SUT:-$ROOT/scripts/unit-proof.sh}"
SHTEST_TAG=pfm-unit-proof-test
# shellcheck source=/dev/null
source "$ROOT/../scripts/shtest.sh"
eq() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected: $2" "actual:   $3"; fi; }

# A checkout of two tracked files, its allow file, and one run's out directory.
CO=$T/co
mkdir -p "$CO/pfm/internal/x" "$CO/pfm/.arch" "$T/goroot" "$T/gowork/b001"
printf 'package x\n' > "$CO/pfm/internal/x/x.go"
printf '/.dockerenv\tr\tthe fence container marker\n{checkout}/pfm/internal/z\tr\ta directory the allow row names\n{checkout}/pfm/internal/gone*\tabsent\ta probe that finds nothing\n' > "$CO/pfm/.arch/unit-proof-allow.txt"
# The system temp root the zones name: the fixture's records under it are the run's own scratch.
SYSTMP=/tmp
make_out() {
  mkdir -p "$1/profile"
  printf 'pfm/internal/x/x.go\0pfm/.arch/unit-proof-allow.txt\0' > "$1/files.z"
  printf 'checkout\t%s\ngoroot\t%s\nsystmp\t%s\nfiles\t%s\n' "$CO" "$T/goroot" "$SYSTMP" "$1/files.z" > "$1/zones.tsv"
  printf 'linux\n' > "$1/goos"
}
OUT=$T/out
make_out "$OUT"
BIN=$T/gowork/b001/x.test
TR=$T/trace/x
mkdir -p "$TR"
cat > "$TR/t.100" <<EOF
execve("$BIN", ["$BIN", "-test.v"], 0x1 /* 3 vars */) = 0
openat(AT_FDCWD<$SYSTMP>, "/etc/ld.so.cache", O_RDONLY|O_CLOEXEC) = 3</etc/ld.so.cache>
openat(AT_FDCWD, "$T/gowork/b001/importcfg", O_RDONLY|O_CLOEXEC) = 3
mkdirat(AT_FDCWD</w/pfm/internal/x>, "$SYSTMP/j41", 0700) = 0
openat(AT_FDCWD, "$SYSTMP/j41/u7/XDG_STATE_HOME/a", O_WRONLY|O_CREAT, 0600) = 5<$SYSTMP/j41/u7/XDG_STATE_HOME/a>
openat(AT_FDCWD, "$SYSTMP/cc-sid/9", O_RDONLY) = 4<$SYSTMP/cc-sid/9>
openat(AT_FDCWD, "$SYSTMP/j41/u7/T", O_RDONLY|O_DIRECTORY|O_CLOEXEC) = 11<$SYSTMP/j41/u7/T>
unlinkat(11, "dddddddddddd", AT_REMOVEDIR) = 0
openat(AT_FDCWD, "$CO/pfm/internal/x/x.go", O_RDONLY|O_CLOEXEC) = 7
openat(AT_FDCWD, "$CO/pfm.config.json", O_RDONLY|O_CLOEXEC) = 6
openat(AT_FDCWD, "/root/.cc/3/.credentials.json", O_RDONLY) = -1 ENOENT (No such file or directory)
openat(AT_FDCWD, "/dev/null", O_WRONLY|O_CLOEXEC) = 8
openat(AT_FDCWD<$CO/pfm/internal/x>, "[vdso]", O_RDONLY|O_CLOEXEC) = -1 ENOENT (No such file or directory)
openat(AT_FDCWD<$CO/pfm/internal/x>, "[vsyscall]", O_RDONLY|O_CLOEXEC) = -1 ENOENT (No such file or directory)
openat(AT_FDCWD<$CO/pfm/internal/z>, "", O_RDONLY|O_CLOEXEC) = -1 ENOENT (No such file or directory)
openat(AT_FDCWD<$CO/pfm/internal/x>, "[other]", O_RDONLY|O_CLOEXEC) = -1 ENOENT (No such file or directory)
newfstatat(AT_FDCWD, "/.dockerenv", 0x1, 0) = 0
connect(7, {sa_family=AF_UNIX, sun_path="$SYSTMP/tmux-1000/default"}, 26) = 0
connect(8, {sa_family=AF_INET, sin_port=htons(8080), sin_addr=inet_addr("127.0.0.1")}, 16) = 0
utimensat(AT_FDCWD</w>, "/proc/self/fd/5", NULL, 0) = 0
openat(AT_FDCWD, "/dev/ptmx", O_RDWR|O_CLOEXEC) = 9</dev/pts/ptmx>
openat(AT_FDCWD, "/dev/pts/0", O_RDWR|O_NOCTTY|O_CLOEXEC) = 10</dev/pts/0>
openat(AT_FDCWD, "/dev/tty", O_RDWR|O_CLOEXEC) = -1 ENXIO (No such device or address)
chdir("$SYSTMP/j41/u7/T/001") = 0
mkdirat(AT_FDCWD, "dddd", 0700) = 0
newfstatat(AT_FDCWD, "../../dddd", 0x1, AT_SYMLINK_NOFOLLOW) = 0
clone(child_stack=NULL, flags=CLONE_CHILD_CLEARTID|CLONE_CHILD_SETTID|SIGCHLD, child_tidptr=0x1) = 101
clone(child_stack=NULL, flags=CLONE_CHILD_CLEARTID|CLONE_CHILD_SETTID|SIGCHLD, child_tidptr=0x1) = 103
EOF
for pid in 101 103; do
  cat > "$TR/t.$pid" <<'EOF'
???( <unfinished ...>
execve("/opt/herdr/bin/herdr", ["herdr", "integration", "install", "claude"], 0x1 /* 3 vars */) = 0
openat(AT_FDCWD, "/etc/herdr.conf", O_RDONLY) = 3
EOF
done
printf '???( <detached ...>\n' > "$TR/t.104"
printf 'clone(child_stack=0x1, flags=CLONE_VM|CLONE_SETTLS\nclone(child_stack=0x1, flags=CLONE_VM|CLONE_THREAD <unfinished ...>\n' > "$TR/t.105"
bash "$SUT" --filter "$OUT" internal/x "$TR" "$BIN" > "$T/filter.out" 2>&1
rc=$?
eq 'filter: a readable trace exits 0' 0 "$rc"
eq 'filter: each record is allowed or one reach row of its kind; a started program is its exec row alone' \
  "$(printf '%s\n' \
    "exec	herdr integration install claude	2" \
    "read	/root/.cc/3/.credentials.json	1" \
    "read	$SYSTMP/cc-sid/9	1" \
    "read	{checkout}/pfm.config.json	1" \
    "read	{checkout}/pfm/internal/x/[other]	1" \
    "socket	$SYSTMP/tmux-1000/default	1" \
    "write	/dev/tty	1")" \
  "$(cut -f1-3 "$OUT/reach/internal%x.tsv")"

# A cwd strace cannot name is the binary's own only when every absolute chdir stayed in its own
# tree; a pseudo-terminal slave is its own only after it opened /dev/ptmx.
mkdir -p "$T/trace/cwd"
cat > "$T/trace/cwd/t.300" <<'EOF'
chdir("/srv/x") = 0
newfstatat(AT_FDCWD, "../y", 0x1, AT_SYMLINK_NOFOLLOW) = 0
openat(AT_FDCWD, "/dev/pts/3", O_RDWR) = 4</dev/pts/3>
EOF
make_out "$T/cwdout"
bash "$SUT" --filter "$T/cwdout" internal/cwd "$T/trace/cwd" "$BIN" > "$T/cwd.out" 2>&1
eq 'filter: an unnamed cwd outside the binary own tree and a foreign pseudo-terminal are reach rows' \
  "$(printf '%s\n' "read	/srv/x	1" "read	{checkout}/pfm/internal/y	1" "write	/dev/pts/3	1")" \
  "$(cut -f1-3 "$T/cwdout/reach/internal%cwd.tsv")"
eq 'filter: a row samples the record that made it' \
  'execve("/opt/herdr/bin/herdr", ["herdr", "integration", "install", "claude"], 0x1 /* 3 vars */) = 0' \
  "$(head -n 1 "$OUT/reach/internal%x.tsv" | cut -f4)"

# An fd strace prints bare (its path past PATH_MAX) is named by the open that returned it, in any
# thread's file; a relative chdir names nothing; only a testjail-made directory under the system temp
# directory is the run's; an `absent` allow row holds only while its path does not exist.
mkdir -p "$T/trace/chain"
printf 'x\n' > "$CO/pfm/internal/gone2"
cat > "$T/trace/chain/t.400" <<EOF
mkdirat(AT_FDCWD, "$SYSTMP/j42", 0700) = 0
openat(AT_FDCWD, "$SYSTMP/j42", O_RDONLY|O_DIRECTORY) = 3<$SYSTMP/j42>
openat(3<$SYSTMP/j42>, "dddd", O_RDONLY|O_DIRECTORY) = 319
openat(5</srv>, "dddd", O_RDONLY|O_DIRECTORY) = 330
chdir("dddd") = 0
mkdirat(AT_FDCWD, "$SYSTMP/tmux-1000", 0700) = 0
connect(7, {sa_family=AF_UNIX, sun_path="$SYSTMP/tmux-1000/default"}, 26) = 0
newfstatat(AT_FDCWD, "$CO/pfm/internal/gone1", 0x1, 0) = -1 ENOENT (No such file or directory)
openat(AT_FDCWD, "$CO/pfm/internal/gone2", O_RDONLY) = 3</gone2>
EOF
cat > "$T/trace/chain/t.401" <<'EOF'
openat(319, "eeee", O_RDONLY|O_DIRECTORY) = 320
unlinkat(320, "f", 0) = 0
unlinkat(319, "eeee", AT_REMOVEDIR) = 0
unlinkat(330, "x", 0) = 0
EOF
make_out "$T/chainout"
bash "$SUT" --filter "$T/chainout" internal/chain "$T/trace/chain" "$BIN" > "$T/chain.out" 2>&1
rc=$?
eq 'filter: a bare fd is named by its open in any thread; a relative chdir is no reach; a foreign temp directory and an existing absent-row path are' \
  "0|$(printf '%s\n' "read	/srv/dddd	1" "read	{checkout}/pfm/internal/gone2	1" "socket	$SYSTMP/tmux-1000/default	1" "write	/srv/dddd/x	1" "write	$SYSTMP/tmux-1000	1")" \
  "$rc|$(cut -f1-3 "$T/chainout/reach/internal%chain.tsv")"

mkdir -p "$T/trace/bad"
printf 'garbage(\nunlinkat(12, "dddd", 0) = 0\n' > "$T/trace/bad/t.200"
bash "$SUT" --filter "$OUT" internal/bad "$T/trace/bad" "$BIN" > "$T/bad.out" 2>&1
rc=$?
eq 'filter: a record it cannot read, or a relative path under a dirfd never named, is never dropped' \
  "1|ValueError: unparsed 2 records, first $T/trace/bad/t.200:1: garbage(" "$rc|$(tail -n 1 "$T/bad.out")"

BADCO=$T/badco
mkdir -p "$BADCO/pfm/.arch"
printf '/x\tr\t\n' > "$BADCO/pfm/.arch/unit-proof-allow.txt"
make_out "$T/badout"
sed -i "s#^checkout\t.*#checkout\t$BADCO#" "$T/badout/zones.tsv"
bash "$SUT" --filter "$T/badout" internal/x "$TR" "$BIN" > "$T/allow.out" 2>&1
rc=$?
eq 'filter: an allow row without a reason is an error' \
  "1|ValueError: allow row without a reason or mode, $BADCO/pfm/.arch/unit-proof-allow.txt:1: /x	r	" \
  "$rc|$(tail -n 1 "$T/allow.out")"

# The verdict over the filtered binary above: a refused start, a missing-tool skip, a pass and a tripwire.
M=github.com/rezzminator/professor/pfm
cat > "$OUT/tests.json" <<EOF
{"Action":"run","Package":"$M/internal/x","Test":"TestPasses"}
{"Action":"pass","Package":"$M/internal/x","Test":"TestPasses"}
{"Action":"run","Package":"$M/internal/x","Test":"TestStarts"}
{"Action":"output","Package":"$M/internal/x","Test":"TestStarts/one","Output":"    x_test.go:5: UNIT-NET REFUSED start: /usr/bin/git status — fix it\n"}
{"Action":"fail","Package":"$M/internal/x","Test":"TestStarts/one"}
{"Action":"fail","Package":"$M/internal/x","Test":"TestStarts"}
{"Action":"run","Package":"$M/internal/x","Test":"TestSkips"}
{"Action":"output","Package":"$M/internal/x","Test":"TestSkips","Output":"    x_test.go:9: git is not installed\n"}
{"Action":"skip","Package":"$M/internal/x","Test":"TestSkips"}
{"Action":"fail","Package":"$M/internal/x"}
EOF
printf 'git\tstatus\n' > "$OUT/tripwire.log"
bash "$SUT" --verdict "$OUT" > "$T/verdict.out" 2>&1
rc=$?
eq 'verdict FAIL: exit 1 and the line counts tests and reach rows by kind' \
  "1|UNIT-PROOF FAIL · tests 1 pass 1 fail 1 skip · reach 8 rows (1 exec · 4 read · 1 write · 1 socket · 1 tripwire) · syscalls traced · $OUT" \
  "$rc|$(tail -n 1 "$T/verdict.out")"
eq 'verdict FAIL: the reach rows print first' 'exec herdr integration install claude ×2 (internal/x)' "$(head -n 1 "$T/verdict.out")"
eq 'verdict FAIL: each failing test prints by package and name with its first output line, before the summary' \
  "$(printf '%s\n' '--- FAIL internal/x TestStarts' '    x_test.go:5: UNIT-NET REFUSED start: /usr/bin/git status — fix it')" \
  "$(sed -n '/^--- FAIL/,$p' "$T/verdict.out" | sed '$d')"
eq 'verdict: causes.tsv names each failing or skipping top-level test, subtests rolled up, then totals' \
  "$(printf '%s\n' \
    "skip:git is not installed	internal/x	TestSkips	x_test.go:9: git is not installed" \
    "start:git	internal/x	TestStarts	x_test.go:5: UNIT-NET REFUSED start: /usr/bin/git status — fix it" \
    "skip:git is not installed	1" \
    "start:git	1")" \
  "$(cat "$OUT/causes.tsv")"
eq 'verdict: reach.tsv is package, kind, target, count, sample; a tripwire row has no package' \
  "$(printf '%s\n' 'internal/x	exec	herdr integration install claude	2' '-	tripwire	git status	1')" \
  "$(awk -F'\t' '$2 == "exec" || $2 == "tripwire"' "$OUT/reach.tsv" | cut -f1-4)"

PASSOUT=$T/pass
make_out "$PASSOUT"
mkdir -p "$PASSOUT/reach"
: > "$PASSOUT/reach/internal%x.tsv"
printf '%s\n' "{\"Action\":\"pass\",\"Package\":\"$M/internal/x\",\"Test\":\"TestPasses\"}" > "$PASSOUT/tests.json"
bash "$SUT" --verdict "$PASSOUT" > "$T/pass.out" 2>&1
rc=$?
eq 'verdict PASS: exit 0, no test failing, no reach row' \
  "0|UNIT-PROOF PASS · tests 1 pass 0 fail 0 skip · reach 0 rows (0 exec · 0 read · 0 write · 0 socket · 0 tripwire) · syscalls traced · $PASSOUT" \
  "$rc|$(cat "$T/pass.out")"

# Each FAIL trigger alone fails the run: one reach row, or one skip naming a missing tool.
REACHOUT=$T/reachonly
make_out "$REACHOUT"
mkdir -p "$REACHOUT/reach"
printf 'read\t/tmp/cc-sid/9\t1\topenat(…)\n' > "$REACHOUT/reach/internal%x.tsv"
cp "$PASSOUT/tests.json" "$REACHOUT/tests.json"
bash "$SUT" --verdict "$REACHOUT" > "$T/reachonly.out" 2>&1
eq 'verdict FAIL: one reach row alone fails the run' \
  "1|UNIT-PROOF FAIL · tests 1 pass 0 fail 0 skip · reach 1 rows (0 exec · 1 read · 0 write · 0 socket · 0 tripwire) · syscalls traced · $REACHOUT" \
  "$?|$(tail -n 1 "$T/reachonly.out")"
SKIPOUT=$T/skiponly
make_out "$SKIPOUT"
mkdir -p "$SKIPOUT/reach"
: > "$SKIPOUT/reach/internal%x.tsv"
printf '%s\n' "{\"Action\":\"output\",\"Package\":\"$M/internal/x\",\"Test\":\"TestSkips\",\"Output\":\"    x_test.go:9: tmux is not installed\\n\"}" \
  "{\"Action\":\"skip\",\"Package\":\"$M/internal/x\",\"Test\":\"TestSkips\"}" > "$SKIPOUT/tests.json"
bash "$SUT" --verdict "$SKIPOUT" > "$T/skiponly.out" 2>&1
eq 'verdict FAIL: a skip naming a missing tool alone fails the run' \
  "1|UNIT-PROOF FAIL · tests 0 pass 0 fail 1 skip · reach 0 rows (0 exec · 0 read · 0 write · 0 socket · 0 tripwire) · syscalls traced · $SKIPOUT" \
  "$?|$(tail -n 1 "$T/skiponly.out")"
# A binary's filter error (an unparsed record) reaches the verdict as ERROR, exit 2, naming its package.
ERROUT=$T/errout
make_out "$ERROUT"
mkdir -p "$ERROUT/reach" "$ERROUT/errors"
: > "$ERROUT/reach/internal%x.tsv"
cp "$PASSOUT/tests.json" "$ERROUT/tests.json"
printf 'internal/bad: unparsed 2 records, first t.200:1: garbage(\n' > "$ERROUT/errors/internal%bad"
bash "$SUT" --verdict "$ERROUT" > "$T/errout.out" 2>&1
rc=$?
eq 'verdict ERROR: a filter error names its package and exits 2' \
  "2|$(printf 'UNIT-PROOF ERROR internal/bad: unparsed 2 records, first t.200:1: garbage(\nUNIT-PROOF ERROR · tests 1 pass 0 fail 0 skip · reach 0 rows (0 exec · 0 read · 0 write · 0 socket · 0 tripwire) · syscalls traced · %s' "$ERROUT")" \
  "$rc|$(cat "$T/errout.out")"

BLIND=$T/blind
make_out "$BLIND"
cp "$PASSOUT/tests.json" "$BLIND/tests.json"
bash "$SUT" --verdict "$BLIND" > "$T/blind.out" 2>&1
rc=$?
eq 'verdict ERROR: a traced run with no trace file could not look, exit 2' \
  "$(printf '2|UNIT-PROOF ERROR no trace file: %s/reach holds no filtered binary\nUNIT-PROOF ERROR · tests 1 pass 0 fail 0 skip · reach 0 rows (0 exec · 0 read · 0 write · 0 socket · 0 tripwire) · syscalls traced · %s' "$BLIND" "$BLIND")" \
  "$rc|$(cat "$T/blind.out")"

NOEVENT=$T/noevent
make_out "$NOEVENT"
mkdir -p "$NOEVENT/reach"
: > "$NOEVENT/tests.json"
bash "$SUT" --verdict "$NOEVENT" > "$T/noevent.out" 2>&1
rc=$?
eq 'verdict ERROR: no test event, exit 2' \
  "2|UNIT-PROOF ERROR no test event in $NOEVENT/tests.json" "$rc|$(head -n 1 "$T/noevent.out")"

# The exec wrapper on Linux, through a strace and a test binary of this suite's own: a package the
# split table names runs as that many traced parts, one that it does not as one traced process.
mkdir -p "$T/fakebin" "$T/pk"
cat > "$T/fakebin/strace" <<'EOF'
#!/bin/bash
# Writes the traced binary's exec record and one foreign read where -o names, then runs the binary.
while [ $# -gt 0 ]; do
  case "$1" in
    -o) trace="$2"; shift 2 ;;
    -e | -s) shift 2 ;;
    -*) shift ;;
    *) break ;;
  esac
done
printf 'execve("%s", ["%s"], 0x1 /* 0 vars */) = 0\nopenat(AT_FDCWD, "/srv/part", O_RDONLY) = -1 ENOENT (No such file or directory)\n' "$1" "$1" > "$trace.$$"
exec "$@"
EOF
SPLITBIN=$T/gowork/b001/split.test
cat > "$SPLITBIN" <<'EOF'
#!/bin/bash
# Five tests: -test.list names them, -test.run runs the ones its list names. {this}.lose names a test
# it never starts; {this}.fail names one that fails and ends the process there; {this}.list replaces
# the list run's output with its first line and exit status.
names='TestA TestB TestC TestD TestE'
run="|${names// /|}|"
for arg in "$@"; do
  case "$arg" in
    -test.list=*)
      if [ -f "$0.list" ]; then read -r out code < "$0.list"; echo "$out"; exit "$code"; fi
      printf '%s\n' $names 'a line that is no test name'; exit 0 ;;
    -test.run=*) run="${arg#-test.run=^(}"; run="|${run%)\$}|" ;;
  esac
done
lose=; [ ! -f "$0.lose" ] || read -r lose < "$0.lose"
fail=; [ ! -f "$0.fail" ] || read -r fail < "$0.fail"
for name in $names; do
  case "$run" in *"|$name|"*) ;; *) continue ;; esac
  [ "$name" != "$lose" ] || continue
  printf '\026=== RUN   %s\n' "$name"
  if [ "$name" = "$fail" ]; then printf '\026--- FAIL: %s (0.00s)\nFAIL\n' "$name"; exit 1; fi
  printf '\026--- PASS: %s (0.00s)\n' "$name"
done
printf 'PASS\n'
EOF
chmod 0755 "$T/fakebin/strace" "$SPLITBIN"
PK="${T#/}/pk"
PKSLUG="${PK//\//%}"
# split_exec <out> <table>: the wrapper on the split binary from its package directory; its exit status.
split_exec() {
  make_out "$1"
  (cd "$T/pk" && PATH="$T/fakebin:$PATH" UNIT_PROOF_SHARDS="$2" bash "$SUT" --exec "$1" "$SPLITBIN" -test.v=test2json) > "$1.stdout" 2> "$1.stderr"
}
frames() { printf '=== RUN   %s\n--- PASS: %s (0.00s)\n' "$1" "$1"; }
split_exec "$T/split" "$PK=3"; rc=$?
eq 'exec, a split package: each listed test runs once, the parts in order, and the binary passes' \
  "0|$(frames TestA; frames TestD; echo PASS; frames TestB; frames TestE; echo PASS; frames TestC; echo PASS)" \
  "$rc|$(tr -d '\026' < "$T/split.stdout")"
eq 'exec, a split package: the list run and each part are filtered into their own reach file' \
  "$(for part in 1 2 3 list; do printf '%s#%s.tsv read\t/srv/part\t1\n' "$PKSLUG" "$part"; done)" \
  "$(cd "$T/split/reach" && for f in *; do printf '%s %s\n' "$f" "$(cut -f1-3 "$f")"; done)"
eq 'exec, a split package: the traces and the part outputs are removed and no error is left' \
  '|absent' "$(ls "$T/split/trace")|$([ -e "$T/split/errors" ] && echo present || echo absent)"
cp "$PASSOUT/tests.json" "$T/split/tests.json"
bash "$SUT" --verdict "$T/split" > "$T/split.verdict" 2>&1; rc=$?
eq 'verdict: the parts of a split package add up to one reach row under its name' \
  "1|read /srv/part ×4 ($PK)|UNIT-PROOF FAIL · tests 1 pass 0 fail 0 skip · reach 1 rows (0 exec · 1 read · 0 write · 0 socket · 0 tripwire) · syscalls traced · $T/split" \
  "$rc|$(head -n 1 "$T/split.verdict")|$(tail -n 1 "$T/split.verdict")"
split_exec "$T/whole" ''; rc=$?
eq 'exec, a package the table does not name: one traced process, one reach file' \
  "0|$(frames TestA; frames TestB; frames TestC; frames TestD; frames TestE; echo PASS)|$PKSLUG.tsv" \
  "$rc|$(tr -d '\026' < "$T/whole.stdout")|$(ls "$T/whole/reach")"
echo TestC > "$SPLITBIN.lose"
split_exec "$T/lost" "$PK=3"; rc=$?
eq 'exec, a split package: a listed test no part started is an error naming it' \
  "2|$PK: 1 listed test(s) ran in no part, first TestC" "$rc|$(cat "$T/lost/errors/$PKSLUG#lost" 2>&1)"
rm -f "$SPLITBIN.lose"
echo TestB > "$SPLITBIN.fail"
split_exec "$T/failed" "$PK=3"; rc=$?
eq 'exec, a split package: a failing part fails the binary, and the tests behind it are no second error' \
  "1|$(frames TestA; frames TestD; echo PASS; printf '=== RUN   TestB\n--- FAIL: TestB (0.00s)\nFAIL\n'; frames TestC; echo PASS)|absent" \
  "$rc|$(tr -d '\026' < "$T/failed.stdout")|$([ -e "$T/failed/errors" ] && echo present || echo absent)"
rm -f "$SPLITBIN.fail"
split_exec "$T/badtable" "$PK=many"; rc=$?
eq 'exec: a split table word that is no {package}={parts} is an error' \
  "2|UNIT_PROOF_SHARDS: $PK=many is no {package}={parts}" "$rc|$(cat "$T/badtable/errors/$PKSLUG" 2>&1)"
split_exec "$T/badword" "internal/other:3 $PK=3"; rc=$?
eq 'exec: a malformed split table word for another package is an error too' \
  "2|UNIT_PROOF_SHARDS: internal/other:3 is no {package}={parts}" "$rc|$(cat "$T/badword/errors/$PKSLUG" 2>&1)"
# list_case <out> <list output> <exit status>: the split binary's list run answering so; the run's status.
list_case() { echo "$2 $3" > "$SPLITBIN.list"; split_exec "$1" "$PK=3"; local rc=$?; rm -f "$SPLITBIN.list"; return "$rc"; }
list_case "$T/listfail" broken 1; rc=$?
eq 'exec, a split package: a list run that fails is an error naming its last line' \
  "2|$PK: its test list could not be read: broken" "$rc|$(cat "$T/listfail/errors/$PKSLUG#names" 2>&1)"
list_case "$T/listnone" no-test-here 0; rc=$?
eq 'exec, a split package: a list that names no test is an error' \
  "2|$PK: its binary lists no test to split" "$rc|$(cat "$T/listnone/errors/$PKSLUG#names" 2>&1)"

# The run's own go test, through a go and a git of this suite's own: go answers the two env reads and
# records a go test's arguments where GO_ARGV names, git lists one tracked file.
cat > "$T/fakebin/go" <<'EOF'
#!/bin/bash
case "$1 ${2:-}" in
  'env GOOS') echo linux ;;
  'env GOROOT') echo /nonexistent/goroot ;;
  'test '*) printf '%s\n' "$*" > "$GO_ARGV"; printf '{"Action":"pass","Package":"x","Elapsed":0}\n' ;;
  *) echo "fake go: unexpected $*" >&2; exit 2 ;;
esac
EOF
printf '#!/bin/bash\nprintf '"'"'pfm/go.mod\\0'"'"'\n' > "$T/fakebin/git"
chmod 0755 "$T/fakebin/go" "$T/fakebin/git"
TP="$(cd "$T" && pwd -P)"
# proof_run <out> <PFM_FENCE_TEST_P, empty for unset>: the whole run on the fakes; its exit status.
proof_run() {
  rm -f "$T/go.argv"
  (
    unset PFM_FENCE_TEST_P
    [ -z "$2" ] || export PFM_FENCE_TEST_P="$2"
    PATH="$T/fakebin:$PATH" GO_ARGV="$T/go.argv" bash "$SUT" --out "$1" > "$1.out" 2>&1
  )
}
proof_run "$T/runp" 3
eq 'run: go test takes -p from PFM_FENCE_TEST_P' \
  "test -count=1 -json -timeout 20m -p 3 -exec bash '$SUT' --exec '$TP/runp' ./..." "$(cat "$T/go.argv" 2>&1)"
proof_run "$T/rund" ''
eq 'run: without PFM_FENCE_TEST_P go test keeps its own package concurrency' \
  "test -count=1 -json -timeout 20m -exec bash '$SUT' --exec '$TP/rund' ./..." "$(cat "$T/go.argv" 2>&1)"
proof_run "$T/runbad" many; rc=$?
eq 'run: a PFM_FENCE_TEST_P that is no positive integer is an error before any test runs' \
  "2|UNIT-PROOF ERROR PFM_FENCE_TEST_P must be a positive integer (got 'many')|absent" \
  "$rc|$(cat "$T/runbad.out")|$([ -e "$T/go.argv" ] && echo present || echo absent)"

shtest_end
