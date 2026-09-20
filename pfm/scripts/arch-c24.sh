#!/usr/bin/env bash
# C24-unwrapped-door — the activity log's production census.
# Invoked by arch-check.sh with the same MODE (check | --measure); it prints
# exactly one `CHECK C24-unwrapped-door PASS|FAIL|ERROR|MEASURE <detail>`
# line and exits 0 PASS · 1 FAIL · 2 ERROR, the contract arch-check.sh folds
# into its own status.
#
# Three families of door a call site could open WITHOUT the logger, counted
# per file in non-test Go outside internal/testjail and internal/hostfixture:
#   http  an `http.Client{` construction or `http.DefaultClient` outside
#         internal/obs/httpout.go, unless the line hands it to
#         obs.WrapClient/obs.RoundTripper (the wrapper IS the door)
#   sql   a `database/sql` import outside the db access layers
#         internal/{sqlitedb,fleetdb,store,index}
#   proc  an exec.Command/exec.CommandContext/exec.LookPath call outside the
#         runner (internal/deps/runner.go), the tmux terminal (internal/tmux/)
#         and spawn's platform scope launchers (service_scope_*.go), plus a
#         tmux.Command( call outside internal/tmux/ — the constructor that
#         returns a bare *exec.Cmd and cannot see completion; tmux.Exec is
#         the door.
# Baseline .arch/unwrapped-door.txt: lines `<family>:<file> <count>`, byte
# order, and it only shrinks — a key it lacks or a count above it is FAIL
# naming the row; `--measure` lowers counts to today's and drops fixed rows,
# never adding one. An enumerator that could not run (no sources listed, grep
# unreadable, baseline missing in check mode) is ERROR, never PASS.
set -uo pipefail
export LC_ALL=C
PFM="${PFM:-$(cd "$(dirname "$0")/.." && pwd)}"
SCRIPTS="$(cd "$(dirname "$0")" && pwd)"
BASE="$PFM/.arch"
NAME=unwrapped-door
ID=C24-unwrapped-door
MODE="${1:-check}"
case "$MODE" in check|--measure) ;; *) echo "usage: arch-c24.sh [--measure]" >&2; exit 2 ;; esac

say() { printf 'CHECK %-22s %-7s %s\n' "$ID" "$1" "$2"; }

cd "$PFM" || { say ERROR "cannot cd $PFM"; exit 2; }
T=$(mktemp -d) || { say ERROR "mktemp failed"; exit 2; }
trap 'rm -rf "$T"' EXIT

# Same enumerator arch-check.sh uses: the worktree's own index, untracked
# files included, deleted ones excluded; the fence hands the mounted git dir
# over through PFM_DEV_REPO_GIT_DIR/PFM_DEV_REPO_WORK_TREE.
#
# internal/mockengine and cmd/mock-engine are test-only fake engines, never
# linked into the pfm binary — the same C22 exemption (arch-check.sh § C22)
# applies here: the mock IS a host (exec, files, a fake sqlite-backed store),
# so its doors are its purpose, not a leak this census tracks.
# shellcheck source=repo-git.sh
source "$SCRIPTS/repo-git.sh" || { say ERROR "cannot source $SCRIPTS/repo-git.sh"; exit 2; }
repo_git ls-files -co --exclude-standard '*.go' 2>/dev/null \
  | while read -r f; do [ -f "$f" ] && echo "$f"; done \
  | grep -v '_test\.go$' \
  | grep -vE '^internal/(testjail|hostfixture|mockengine)/|^cmd/mock-engine/' | sort -u > "$T/src.list"
[ -s "$T/src.list" ] || { say ERROR "no Go sources listed under $PFM — the enumerator did not run"; exit 2; }

# g <out> <list> <grep args...>: grep -n over a list; rc 2 when grep could not
# read or the list is empty — an unreadable tree never passes as a clean one.
g() { local out=$1 list=$2; shift 2; [ -s "$list" ] || return 2; grep "$@" $(cat "$list") > "$out"; [ $? -le 1 ] || return 2; }
count_by_file() { cut -d: -f1 "$1" | sort | uniq -c | awk -v fam="$2" '{print fam":"$2" "$1}'; }

: > "$T/cur"
# http
grep -v '^internal/obs/httpout\.go$' "$T/src.list" > "$T/http.list"
if g "$T/raw" "$T/http.list" -nE '&?http\.Client\{|http\.DefaultClient'; then
  grep -vE 'obs\.(WrapClient|RoundTripper)\(' "$T/raw" > "$T/http.raw"
  count_by_file "$T/http.raw" http >> "$T/cur"
else say ERROR "grep could not read sources for the http family"; exit 2; fi
# sql
grep -vE '^internal/(sqlitedb|fleetdb|store|index)/' "$T/src.list" > "$T/sql.list"
if g "$T/raw" "$T/sql.list" -n '"database/sql"'; then count_by_file "$T/raw" sql >> "$T/cur"
else say ERROR "grep could not read sources for the sql family"; exit 2; fi
# proc
grep -vE '^internal/deps/runner\.go$|^internal/tmux/|^internal/spawn/service_scope_[a-z]+\.go$' "$T/src.list" > "$T/proc.list"
if g "$T/raw" "$T/proc.list" -nE 'exec\.(Command|CommandContext|LookPath)\('; then count_by_file "$T/raw" proc >> "$T/cur"
else say ERROR "grep could not read sources for the proc family"; exit 2; fi
grep -v '^internal/tmux/' "$T/src.list" > "$T/tmuxcmd.list"
if g "$T/raw" "$T/tmuxcmd.list" -nE 'tmux\.Command\('; then count_by_file "$T/raw" proc >> "$T/cur"
else say ERROR "grep could not read sources for the tmux family"; exit 2; fi
# a file may carry both exec and tmux.Command rows: fold them into one key
awk '{c[$1]+=$2} END {for (k in c) print k" "c[k]}' "$T/cur" | sort -u > "$T/counts"

if [ "$MODE" = --measure ]; then
  mkdir -p "$BASE"
  if [ -f "$BASE/$NAME.txt" ]; then
    awk 'FILENAME==ARGV[1] {base[$1]=$2; next} ($1 in base) {print $1" "($2<base[$1] ? $2 : base[$1])}' "$BASE/$NAME.txt" "$T/counts" | sort -u > "$T/measured"
    mv "$T/measured" "$BASE/$NAME.txt"
  else cp "$T/counts" "$BASE/$NAME.txt"; fi
  say MEASURE "$(awk '{s+=$2} END {print s+0}' "$BASE/$NAME.txt") doors in $(wc -l < "$BASE/$NAME.txt" | tr -d ' ') files -> .arch/$NAME.txt"
  exit 0
fi
[ -f "$BASE/$NAME.txt" ] || { say ERROR "baseline .arch/$NAME.txt missing — cannot tell new from old (run arch-check.sh --measure once the doors are wired)"; exit 2; }
over=$(awk 'FILENAME==ARGV[1] {base[$1]=$2; next} !($1 in base) {print $1" (new "$2")"; next} $2>base[$1] {print $1" ("base[$1]"->"$2")"}' "$BASE/$NAME.txt" "$T/counts")
if [ -n "$over" ]; then say FAIL "$(echo "$over" | tr '\n' ' ')"; exit 1; fi
now=$(awk '{s+=$2} END {print s+0}' "$T/counts"); was=$(awk '{s+=$2} END {print s+0}' "$BASE/$NAME.txt")
note=""; [ "$now" -lt "$was" ] && note="; baseline $was — run --measure to lock the shrink"
say PASS "$now doors in $(wc -l < "$T/counts" | tr -d ' ') files, none above baseline$note"
exit 0
