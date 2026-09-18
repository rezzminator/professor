#!/usr/bin/env bash
# pfm architecture ratchet — `make arch` (part of `make gate`). Design and the
# meaning of every check: docs/dev/pfm-architecture.md § Checks.
#
# Every check prints exactly one line: CHECK <id> PASS|FAIL|ERROR <detail>.
#   PASS  = the enumerator ran and found nothing beyond the committed baseline
#   FAIL  = a violation the baseline does not list, or a count above its baseline
#   ERROR = the enumerator could not run (baseline missing, grep could not read,
#           nothing parsed where something must parse) — never PASS
# Exit status: 0 all PASS · 1 any FAIL · 2 any ERROR.
#
# Baselines live in pfm/.arch/ and only ever shrink. `--measure` locks in what a
# wave fixed and nothing else: with a baseline present it keeps only entries
# still violating and lowers counts to today's, never adding an entry or
# raising a count. A genuinely new exception is a hand edit to .arch/, visible
# in review. With no baseline, --measure writes today's tree as the first one.
set -uo pipefail
# The committed baselines are in byte order; sort, comm and grep must agree with
# them whatever locale the caller's shell carries.
export LC_ALL=C
PFM="${PFM:-$(cd "$(dirname "$0")/.." && pwd)}"
BASE="$PFM/.arch"
CEIL_SRC="${CEIL_SRC:-800}"; CEIL_TEST="${CEIL_TEST:-1000}"
# A baselined over-ceiling file may carry CEIL_SLACK lines of move churn (an
# import line gained when a symbol it calls moved packages); logic growth fails.
CEIL_SLACK="${CEIL_SLACK:-5}"
MODE="${1:-check}"
case "$MODE" in check|--measure) ;; *) echo "usage: arch-check.sh [--measure]" >&2; exit 2 ;; esac
rc=0
say() { printf 'CHECK %-22s %-7s %s\n' "$1" "$2" "$3"; case $2 in FAIL) [ "$rc" -lt 1 ] && rc=1 ;; ERROR) rc=2 ;; esac; }

cd "$PFM" || { say setup ERROR "cannot cd $PFM"; exit 2; }
T=$(mktemp -d) || { say setup ERROR "mktemp failed"; exit 2; }
trap 'rm -rf "$T"' EXIT
# repo_git reads the worktree's own index. Inside the dev fence a linked
# worktree's .git names a host path the container cannot see, so dev.sh iso
# hands over the mounted git dir and work tree instead.
repo_git() {
  if [[ -n "${PFM_DEV_REPO_GIT_DIR:-}" && -n "${PFM_DEV_REPO_WORK_TREE:-}" ]]; then
    git --git-dir="$PFM_DEV_REPO_GIT_DIR" --work-tree="$PFM_DEV_REPO_WORK_TREE" \
      -c safe.directory="$PFM_DEV_REPO_WORK_TREE" "$@"
  else
    git "$@"
  fi
}
# The file lists include untracked files (a wave's new package exists before its
# commit) and exclude deleted ones (a wave's removed file is gone before its commit).
repo_git ls-files -co --exclude-standard '*.go' | while read -r f; do [ -f "$f" ] && echo "$f"; done | sort -u > "$T/all.list"
grep -v '_test\.go$' "$T/all.list" > "$T/src.list"
grep '_test\.go$' "$T/all.list" > "$T/test.list"
[ -s "$T/src.list" ] || { say setup ERROR "no Go sources listed under $PFM — the enumerator did not run"; exit 2; }

# g <out> <list> <grep args...>: grep over a file list; returns 2 when grep could
# not read (rc ≥ 2), so an unreadable tree never passes as a clean one.
# An EMPTY list is never "clean": grep with no file operands would read stdin
# and hang the gate, so it is reported as an enumerator that could not run.
g() { local out=$1 list=$2; shift 2; [ -s "$list" ] || return 2; grep "$@" $(cat "$list") > "$out"; [ $? -le 1 ] || return 2; }

# ratchet <id> <name> <current>: set ratchet — FAIL on any line the baseline lacks.
ratchet() {
  local id=$1 name=$2 cur=$3
  sort -u "$cur" -o "$cur"
  if [ "$MODE" = --measure ]; then
    if ! mkdir -p "$BASE"; then
      say "$id" ERROR "could not create .arch directory for $name"
      return
    fi
    if [ -f "$BASE/$name.txt" ]; then
      if ! comm -12 "$BASE/$name.txt" "$cur" > "$T/measured" ||
         ! mv "$T/measured" "$BASE/$name.txt"; then
        say "$id" ERROR "could not update .arch/$name.txt"
        return
      fi
    elif ! cp "$cur" "$BASE/$name.txt"; then
      say "$id" ERROR "could not write .arch/$name.txt"
      return
    fi
    say "$id" MEASURE "$(wc -l < "$BASE/$name.txt" | tr -d ' ') entries -> .arch/$name.txt"; return
  fi
  [ -f "$BASE/$name.txt" ] || { say "$id" ERROR "baseline .arch/$name.txt missing — cannot tell new from old"; return; }
  local new gone
  new=$(comm -13 "$BASE/$name.txt" "$cur"); gone=$(comm -23 "$BASE/$name.txt" "$cur" | wc -l | tr -d ' ')
  if [ -n "$new" ]; then say "$id" FAIL "new: $(echo "$new" | tr '\n' ' ')"; return; fi
  local note=""; [ "$gone" -gt 0 ] && note="; $gone fixed — run --measure to lock the shrink"
  say "$id" PASS "$(wc -l < "$cur" | tr -d ' ') baselined, 0 new$note"
}

# ratchet_counts <id> <name> <current> [slack]: lines "<key> <count>" — FAIL on a
# key the baseline lacks or a count above the baseline's (+ slack). A count that
# shrank passes.
ratchet_counts() {
  local id=$1 name=$2 cur=$3 slack=${4:-0}
  sort -u "$cur" -o "$cur"
  if [ "$MODE" = --measure ]; then
    if ! mkdir -p "$BASE"; then
      say "$id" ERROR "could not create .arch directory for $name"
      return
    fi
    if [ -f "$BASE/$name.txt" ]; then
      if ! awk 'FILENAME==ARGV[1] {base[$1]=$2; next} ($1 in base) {print $1" "($2<base[$1] ? $2 : base[$1])}' "$BASE/$name.txt" "$cur" | sort -u > "$T/measured" ||
         ! mv "$T/measured" "$BASE/$name.txt"; then
        say "$id" ERROR "could not update .arch/$name.txt"
        return
      fi
    elif ! cp "$cur" "$BASE/$name.txt"; then
      say "$id" ERROR "could not write .arch/$name.txt"
      return
    fi
    say "$id" MEASURE "$(awk '{s+=$2} END {print s+0}' "$BASE/$name.txt") in $(wc -l < "$BASE/$name.txt" | tr -d ' ') keys -> .arch/$name.txt"; return
  fi
  [ -f "$BASE/$name.txt" ] || { say "$id" ERROR "baseline .arch/$name.txt missing — cannot tell new from old"; return; }
  local over
  over=$(awk -v slack="$slack" 'FILENAME==ARGV[1] {base[$1]=$2; next} !($1 in base) {print $1" (new "$2")"; next} $2>base[$1]+slack {print $1" ("base[$1]"->"$2")"}' "$BASE/$name.txt" "$cur")
  if [ -n "$over" ]; then say "$id" FAIL "$(echo "$over" | tr '\n' ' ')"; return; fi
  local now was; now=$(awk '{s+=$2} END {print s+0}' "$cur"); was=$(awk '{s+=$2} END {print s+0}' "$BASE/$name.txt")
  local note=""; [ "$now" -lt "$was" ] && note="; baseline $was — run --measure to lock the shrink"
  say "$id" PASS "$now in $(wc -l < "$cur" | tr -d ' ') keys, none above baseline$note"
}

# count_by_file <raw grep -n output> → "<file> <count>"
count_by_file() { cut -d: -f1 "$1" | sort | uniq -c | awk '{print $2" "$1}'; }

# C1/C2 size ceilings: an over-ceiling file may not appear, and a listed one may not grow.
: > "$T/c1"; while read -r f; do n=$(wc -l < "$f"); [ "$n" -gt "$CEIL_SRC" ] && echo "$f $n"; done < "$T/src.list" > "$T/c1"
ratchet_counts C1-ceiling-src ceiling-src "$T/c1" "$CEIL_SLACK"
while read -r f; do n=$(wc -l < "$f"); [ "$n" -gt "$CEIL_TEST" ] && echo "$f $n"; done < "$T/test.list" > "$T/c2"
ratchet_counts C2-ceiling-test ceiling-test "$T/c2" "$CEIL_SLACK"

# C3 cmd/pfm is dispatch: its non-test line total may not exceed .arch/cmd-budget.txt.
grep '^cmd/pfm/' "$T/src.list" > "$T/cmd.list"
n=$(xargs cat < "$T/cmd.list" | wc -l | tr -d ' ')
if [ ! -s "$T/cmd.list" ]; then say C3-cmd-budget ERROR "no cmd/pfm sources listed — the enumerator did not run"
elif [ "$MODE" = --measure ]; then
  if ! mkdir -p "$BASE"; then
    say C3-cmd-budget ERROR "could not create .arch directory for cmd-budget"
  else
    budget=""
    budget_ok=1
    if [ -f "$BASE/cmd-budget.txt" ]; then
      if ! budget=$(cat "$BASE/cmd-budget.txt"); then
        say C3-cmd-budget ERROR "could not read .arch/cmd-budget.txt"
        budget_ok=0
      elif [ "$budget" -lt "$n" ]; then
        n=$budget
      fi
    fi
    if [ "$budget_ok" -eq 1 ]; then
      if ! printf '%s\n' "$n" > "$BASE/cmd-budget.txt"; then
        say C3-cmd-budget ERROR "could not write .arch/cmd-budget.txt"
      else
        say C3-cmd-budget MEASURE "budget $n lines -> .arch/cmd-budget.txt"
      fi
    fi
  fi
elif [ ! -f "$BASE/cmd-budget.txt" ]; then say C3-cmd-budget ERROR "baseline .arch/cmd-budget.txt missing"
elif [ "$n" -gt "$(cat "$BASE/cmd-budget.txt")" ]; then say C3-cmd-budget FAIL "cmd/pfm = $n > budget $(cat "$BASE/cmd-budget.txt")"
else say C3-cmd-budget PASS "cmd/pfm = $n <= budget $(cat "$BASE/cmd-budget.txt")"; fi

# C4 primitives inside cmd/pfm (exec, SQL, raw fs writes) — each belongs to a package.
if [ ! -s "$T/cmd.list" ]; then say C4-cmd-primitives ERROR "no cmd/pfm sources listed — the enumerator did not run"
elif g "$T/raw" "$T/cmd.list" -nE 'exec\.Command|sql\.Open\(|os\.(WriteFile|Rename)\('; then count_by_file "$T/raw" > "$T/c4"; ratchet_counts C4-cmd-primitives cmd-primitives "$T/c4"
else say C4-cmd-primitives ERROR "grep could not read cmd/pfm sources"; fi

# C5 one tmux runner: outside internal/tmux/, a file that builds its own tmux
# invocation — resolves the tmux binary or assembles the -S socket argv itself.
grep -v '^internal/tmux/' "$T/src.list" > "$T/notmux.list"
if g "$T/raw" "$T/notmux.list" -lE 'deps\.Executable\("tmux"\)|\[\]string\{"-S", '; then cp "$T/raw" "$T/c5"; ratchet C5-tmux-runner tmux-runners "$T/c5"
else say C5-tmux-runner ERROR "grep could not read sources"; fi

# C6 one atomic writer: outside internal/atomicfile/, a file naming an atomic-write
# helper or opening a scratch file itself (os.CreateTemp). The rename is NOT
# required: a scratch file that never reaches os.Rename is still a hand-rolled
# writer (headless/run had three such copies the old both-patterns rule missed).
grep -v '^internal/atomicfile/' "$T/src.list" > "$T/noatomic.list"
if g "$T/c6" "$T/noatomic.list" -lE '^func (writeAtomic|WriteAtomic|atomicWrite|AtomicWrite|writeFileAtomic|WriteFileAtomic)\(' &&
   g "$T/temps" "$T/noatomic.list" -l 'os\.CreateTemp('; then
  cat "$T/temps" >> "$T/c6"; ratchet C6-atomic-write atomic-writers "$T/c6"
else say C6-atomic-write ERROR "grep could not read sources"; fi

# C7 one SQLite opener: sql.Open outside internal/sqlitedb/.
grep -v '^internal/sqlitedb/' "$T/src.list" > "$T/nosql.list"
if g "$T/raw" "$T/nosql.list" -nE 'sql\.Open\('; then count_by_file "$T/raw" > "$T/c7"; ratchet_counts C7-sql-open sql-openers "$T/c7"
else say C7-sql-open ERROR "grep could not read sources"; fi

# C8 negation-named directories.
if find internal cmd -type d \( -iname '*util*' -o -name helpers -o -name common -o -name misc -o -name shared \) > "$T/c8"; then ratchet C8-negation-dirs negation-dirs "$T/c8"
else say C8-negation-dirs ERROR "find failed under internal/ cmd/"; fi

# C9 every package states what it owns in a `// Package` doc comment.
: > "$T/c9"
for dir in $(xargs -n1 dirname < "$T/src.list" | sort -u); do
  grep -lq '^// Package ' $(grep "^$dir/[^/]*$" "$T/src.list") 2>/dev/null || echo "$dir" >> "$T/c9"
done
ratchet C9-package-doc no-package-doc "$T/c9"

# C10 MCP reaches chat verbs through typed calls, never argv into package main.
grep '^internal/mcpserv/' "$T/src.list" > "$T/mcp.list"
if [ ! -s "$T/mcp.list" ]; then say C10-mcp-argv ERROR "no internal/mcpserv sources listed"
elif g "$T/raw" "$T/mcp.list" -nE 'backend\.dispatch\(|cliAction\('; then count_by_file "$T/raw" > "$T/c10"; ratchet_counts C10-mcp-argv mcp-argv-calls "$T/c10"
else say C10-mcp-argv ERROR "grep could not read internal/mcpserv"; fi

# C11 one name per database file: "fleet.db" spelled in Go source.
if g "$T/raw" "$T/src.list" -n '"fleet\.db"'; then count_by_file "$T/raw" > "$T/c11"; ratchet_counts C11-db-names fleet-db-spellings "$T/c11"
else say C11-db-names ERROR "grep could not read sources"; fi

# C12 pfm/CLAUDE.md cites only what exists: packages, *.md docs, PFM_* variables something reads.
if [ ! -f CLAUDE.md ]; then say C12-claude-pointers ERROR "pfm/CLAUDE.md missing"
else
  : > "$T/c12"
  for p in $(grep -oE '^\| `[a-z/]+/`' CLAUDE.md | tr -d '|` '; grep -oE '`[a-z/]+/`' CLAUDE.md | grep -vE '^`(cmd|internal|testdata|e2e)' | tr -d '`'); do
    [ -d "internal/$p" ] || [ -d "$p" ] || echo "$p" >> "$T/c12"
  done
  for f in $(grep -oE '`?[A-Z][A-Z_]+\.md`?' CLAUDE.md | tr -d '`' | sort -u); do [ -e "$f" ] || [ -e "../$f" ] || echo "$f" >> "$T/c12"; done
  # A PFM_* name counts as read only when production code uses it beyond
  # declaring it: the literal on a line that is not `name = "PFM_X"`, or the
  # declared constant's name on some other line. A constant only tests set is
  # a dead knob, not a read.
  for v in $(grep -oE 'PFM_[A-Z_]+' CLAUDE.md | sort -u); do
    grep -h "\"$v\"" $(cat "$T/src.list") > "$T/uses"
    decl='^[[:space:]]*(const[[:space:]]+)?[A-Za-z_][A-Za-z0-9_]*[[:space:]]*(string[[:space:]]*)?=[[:space:]]*"'"$v"'"'
    if grep -vqE "$decl" "$T/uses"; then continue; fi
    read=""
    for name in $(grep -oE '^[[:space:]]*(const[[:space:]]+)?[A-Za-z_][A-Za-z0-9_]*' "$T/uses" | awk '{print $NF}' | sort -u); do
      grep -hw "$name" $(cat "$T/src.list") | grep -vq "\"$v\"" && { read=1; break; }
    done
    [ -n "$read" ] || echo "$v" >> "$T/c12"
  done
  ratchet C12-claude-pointers claude-dangling "$T/c12"
fi

# C13 tests mirror sources: every x.go has x_test.go.
while read -r s; do [ -e "${s%.go}_test.go" ] || echo "$s"; done < "$T/src.list" > "$T/c13"
ratchet C13-test-mirror untested-sources "$T/c13"

# C14 every dispatched top-level command appears in usage (structural once a command table lands).
cases=$(awk '/^func run\(/,/^}/' cmd/pfm/main.go | grep -oE 'case "[a-z-]+"' | grep -oE '"[a-z-]+"' | tr -d '"' | grep -vE '^(help|version|internal)$')
if [ -z "$cases" ]; then say C14-usage-parity ERROR "no case labels parsed from cmd/pfm/main.go run()"
else
  : > "$T/c14"; for c in $cases; do awk '/^func printUsage/,/^}/' cmd/pfm/main.go | grep -qE "\"  $c " || echo "$c" >> "$T/c14"; done
  ratchet C14-usage-parity usage-missing "$T/c14"
fi

# C15 every `pfm internal` entry appears in its usage line (structural once hooks.Table lands).
iv=$(awk '/^func runInternal\(/,/^}/' cmd/pfm/main.go | grep -oE 'args\[0\] (==|!=) "[a-z-]+"' | grep -oE '"[a-z-]+"' | tr -d '"' | sort -u)
line=$(grep -oE 'usage: pfm internal [a-z-]+(\|[a-z-]+)+' cmd/pfm/main.go | head -1)
if [ -z "$iv" ]; then say C15-internal-usage ERROR "no entries parsed from cmd/pfm/main.go runInternal()"
elif [ -z "$line" ]; then say C15-internal-usage ERROR "no multi-entry 'usage: pfm internal a|b' line in cmd/pfm/main.go"
else
  : > "$T/c15"; for v in $iv; do echo "$line" | grep -qE "(^|[ |])$v([|]|$)" || echo "$v" >> "$T/c15"; done
  ratchet C15-internal-usage internal-usage-missing "$T/c15"
fi

# C16 PFM_* environment reads stay inside internal/paths — spelled as a literal
# or through any constant that holds a "PFM_*" name (paths.EnvHome, a local
# fooEnv), since a read through a name is still a read.
grep -v '^internal/paths/' "$T/src.list" > "$T/nopaths.list"
if g "$T/decl" "$T/src.list" -ohE '\b[A-Za-z_][A-Za-z0-9_]*[[:space:]]*(string[[:space:]]*)?=[[:space:]]*"PFM_[A-Z0-9_]+"'; then
  names=$(grep -oE '^[A-Za-z_][A-Za-z0-9_]*' "$T/decl" | sort -u | paste -sd'|' -)
  envread='(Getenv|LookupEnv)\("PFM_'; [ -n "$names" ] && envread="$envread|(Getenv|LookupEnv)\\(([A-Za-z_]+\\.)?($names)\\)"
  if g "$T/raw" "$T/nopaths.list" -nE "$envread"; then count_by_file "$T/raw" > "$T/c16"; ratchet_counts C16-env-outside-paths env-outside-paths "$T/c16"
  else say C16-env-outside-paths ERROR "grep could not read sources"; fi
else say C16-env-outside-paths ERROR "grep could not read the PFM_* name declarations"; fi

# C17 one free function per name: the same unexported free-function name in two
# files is a twin waiting to diverge (clipRunes x5, isLive/IsLive with opposite
# answers). Case-folded so IsLive and isLive collide. Methods are excluded (a
# String() per type is the language), as are _linux/_darwin pairs, which define
# one identifier twice BY DESIGN (pfm/CLAUDE.md § one binary, two kernels).
grep -vE '_(linux|darwin)\.go$' "$T/src.list" > "$T/nokernel.list"
if g "$T/raw" "$T/nokernel.list" -nE '^func [A-Za-z_][A-Za-z0-9_]*\('; then
  awk -F: '{ match($3, /^func [A-Za-z_][A-Za-z0-9_]*/); n=tolower(substr($3, 6, RLENGTH-5)); if (n!="main" && n!="init") print n" "$1 }' "$T/raw" \
    | sort -u | awk '{files[$1]=files[$1]" "$2; c[$1]++} END {for (n in c) if (c[n]>1) print n":"files[n]}' | sort > "$T/c17"
  ratchet C17-dup-functions dup-functions "$T/c17"
else say C17-dup-functions ERROR "grep could not read function declarations"; fi

# C18 one spelling per engine: OpenCode is the brand; Opencode / Oc* / oc* are
# drift, and GPT is an undeclared synonym for Codex. Count per file, only shrinks.
if g "$T/raw" "$T/all.list" -nE 'Opencode|\b[oO]c[A-Z][A-Za-z]+|GPT'; then count_by_file "$T/raw" > "$T/c18"; ratchet_counts C18-engine-spellings engine-spellings "$T/c18"
else say C18-engine-spellings ERROR "grep could not read sources"; fi

# C19 one environment namespace: CHAT_*, CC_* reads are pfm's own
# variables under a foreign prefix — a `grep PFM_` never finds them.
if g "$T/raw" "$T/src.list" -nE '(Getenv|LookupEnv)\("(CHAT|CC)_'; then count_by_file "$T/raw" > "$T/c19"; ratchet_counts C19-env-namespace env-namespace "$T/c19"
else say C19-env-namespace ERROR "grep could not read sources"; fi

# C20 one name for ~/.codex: CodexHome. codexRoot / CodexRoot / AccountHome
# name the same directory in 43 files a `grep CodexHome` misses.
if g "$T/raw" "$T/src.list" -nE '\b[cC]odexRoot\b|\bAccountHome\b'; then count_by_file "$T/raw" > "$T/c20"; ratchet_counts C20-codex-home codex-home "$T/c20"
else say C20-codex-home ERROR "grep could not read sources"; fi

# C21 one test jail: a test that hand-rolls its scratch root with
# os.MkdirTemp("/tmp", ...) instead of testjail.ShortRoot has its own copy of
# the jail, and the six copies already disagree on the DB path.
grep -v '^internal/testjail/' "$T/test.list" > "$T/nojail.list"
if g "$T/raw" "$T/nojail.list" -n 'os\.MkdirTemp("/tmp"'; then count_by_file "$T/raw" > "$T/c21"; ratchet_counts C21-test-jail test-jail-copies "$T/c21"
else say C21-test-jail ERROR "grep could not read tests"; fi

# C22 host doors stay inside their four seam packages: a bare os.Getenv/
# LookupEnv/UserHomeDir/user.Current/exec.Command/exec.CommandContext/
# exec.LookPath/time.Now/Sleep/After/NewTimer/NewTicker/Tick/net.Dial/
# net.Listen in non-test code outside internal/{clock,deps,paths,tmux} is a
# door the unit-test-law wave (docs/dev/trains/testing-foundation/waves/
# 3-unit-law/spec.md § Three seams item 4) has not seamed yet; the baseline
# only shrinks as later batches migrate a package onto clock.Clock,
# deps.Runner, tmux.Fake or paths.Env. internal/mockengine + cmd/mock-engine
# are the fifth seam: the mock IS a host (exec, env, files, clock) — the thing
# the other four fake — so its doors are its purpose, not a leak to migrate.
grep -vE '^(internal/(clock|deps|paths|tmux|mockengine)|cmd/mock-engine)/' "$T/src.list" > "$T/noseam.list"
if g "$T/raw" "$T/noseam.list" -nE 'os\.Getenv|LookupEnv|UserHomeDir|user\.Current|exec\.Command|exec\.CommandContext|exec\.LookPath|time\.Now|time\.Sleep|time\.After|time\.NewTimer|time\.NewTicker|time\.Tick|net\.Dial|net\.Listen'; then
  count_by_file "$T/raw" > "$T/c22"; ratchet_counts C22-host-doors host-doors "$T/c22"
else say C22-host-doors ERROR "grep could not read sources"; fi

# C23 one activity log: a bare log.Print*/log.Fatal* or a hand-rolled
# fmt.Fprint*(os.Stderr in non-test code writes where nothing can read it back
# — no level, no fields, no destination a field report or a lane beat can
# attach (docs/dev/trains/testing-foundation/waves/6-activity-log/spec.md).
# internal/obs IS the destination and cmd/pfm's stderr IS a verb's user-facing
# output, so both are outside the count; every other package moves onto
# obs.Logger/obs.Span as part B migrates it, and this baseline only shrinks.
grep -vE '^(internal/obs/|cmd/pfm/)' "$T/src.list" > "$T/noobs.list"
if g "$T/raw" "$T/noobs.list" -nHE '\blog\.(Print|Fatal)|fmt\.Fprint[a-zA-Z]*\(os\.Stderr'; then
  count_by_file "$T/raw" > "$T/c23"; ratchet_counts C23-bare-log bare-log "$T/c23"
else say C23-bare-log ERROR "grep could not read sources"; fi

bash "$PFM/scripts/arch-c24.sh" "$MODE"; c24=$?; [ "$c24" -gt "$rc" ] && rc=$c24 # C24 lives in its own script
exit $rc
