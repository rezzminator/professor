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
#
# The checks are independent, so they run as concurrent background jobs and print
# in the order they always have: C1 … C23, C25, C26, then C24 (its own script). A job
# that ends without leaving its CHECK line is reported as ERROR for that check,
# never read as PASS.
set -uo pipefail
# The committed baselines are in byte order; sort, comm and grep must agree with
# them whatever locale the caller's shell carries.
export LC_ALL=C
PFM="${PFM:-$(cd "$(dirname "$0")/.." && pwd)}"
SCRIPTS="$(cd "$(dirname "$0")" && pwd)"
BASE="$PFM/.arch"
CEIL_SRC="${CEIL_SRC:-800}"; CEIL_TEST="${CEIL_TEST:-1000}"
# A baselined over-ceiling file may carry CEIL_SLACK lines of move churn (an
# import line gained when a symbol it calls moved packages); logic growth fails.
CEIL_SLACK="${CEIL_SLACK:-5}"
MODE="${1:-check}"
case "$MODE" in check|--measure) ;; *) echo "usage: arch-check.sh [--measure]" >&2; exit 2 ;; esac
rc=0
# say prints one CHECK line. In this shell it also raises rc, the exit status; a
# job's say cannot reach this rc, so the end of the script folds rc from the lines
# the jobs printed.
say() { printf 'CHECK %-22s %-7s %s\n' "$1" "$2" "$3"; case $2 in FAIL) [ "$rc" -lt 1 ] && rc=1 ;; ERROR) rc=2 ;; esac; }

cd "$PFM" || { say setup ERROR "cannot cd $PFM"; exit 2; }
# $L holds the file lists every check reads, written here once before any check
# starts; each check runs as a job with a scratch directory $T of its own under it.
L=$(mktemp -d) || { say setup ERROR "mktemp failed"; exit 2; }
trap 'rm -rf "$L"' EXIT
# shellcheck source=repo-git.sh
source "$SCRIPTS/repo-git.sh" || { say setup ERROR "cannot source $SCRIPTS/repo-git.sh"; exit 2; }
# The file lists include untracked files (a wave's new package exists before its
# commit) and exclude deleted ones (a wave's removed file is gone before its commit).
repo_git ls-files -co --exclude-standard '*.go' | while read -r f; do [ -f "$f" ] && echo "$f"; done | sort -u > "$L/all.list"
grep -v '_test\.go$' "$L/all.list" > "$L/src.list"
grep '_test\.go$' "$L/all.list" > "$L/test.list"
[ -s "$L/src.list" ] || { say setup ERROR "no Go sources listed under $PFM — the enumerator did not run"; exit 2; }
grep '^cmd/pfm/' "$L/src.list" > "$L/cmd.list"

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
chk_C1() {
  wc -l $(cat "$L/src.list") | awk -v ceil="$CEIL_SRC" '$2 != "total" && $1 > ceil {print $2" "$1}' > "$T/c1"
  ratchet_counts C1-ceiling-src ceiling-src "$T/c1" "$CEIL_SLACK"
}

chk_C2() {
  if [ -s "$L/test.list" ]; then
    wc -l $(cat "$L/test.list") | awk -v ceil="$CEIL_TEST" '$2 != "total" && $1 > ceil {print $2" "$1}' > "$T/c2"
  else
    : > "$T/c2"
  fi
  ratchet_counts C2-ceiling-test ceiling-test "$T/c2" "$CEIL_SLACK"
}

# C3 cmd/pfm is dispatch: its non-test line total may not exceed .arch/cmd-budget.txt.
chk_C3() {
  n=$(xargs cat < "$L/cmd.list" | wc -l | tr -d ' ')
  if [ ! -s "$L/cmd.list" ]; then say C3-cmd-budget ERROR "no cmd/pfm sources listed — the enumerator did not run"
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
}

# C4 primitives inside cmd/pfm (exec, SQL, raw fs writes) — each belongs to a package.
chk_C4() {
  if [ ! -s "$L/cmd.list" ]; then say C4-cmd-primitives ERROR "no cmd/pfm sources listed — the enumerator did not run"
  elif g "$T/raw" "$L/cmd.list" -nE 'exec\.Command|sql\.Open\(|os\.(WriteFile|Rename)\('; then count_by_file "$T/raw" > "$T/c4"; ratchet_counts C4-cmd-primitives cmd-primitives "$T/c4"
  else say C4-cmd-primitives ERROR "grep could not read cmd/pfm sources"; fi
}

# C5 one tmux runner: outside internal/tmux/, a file that builds its own tmux
# invocation — resolves the tmux binary or assembles the -S socket argv itself.
chk_C5() {
  grep -v '^internal/tmux/' "$L/src.list" > "$T/notmux.list"
  if g "$T/raw" "$T/notmux.list" -lE 'deps\.Executable\("tmux"\)|\[\]string\{"-S", '; then cp "$T/raw" "$T/c5"; ratchet C5-tmux-runner tmux-runners "$T/c5"
  else say C5-tmux-runner ERROR "grep could not read sources"; fi
}

# C6 one atomic writer: outside internal/atomicfile/, a file naming an atomic-write
# helper or opening a scratch file itself (os.CreateTemp). The rename is NOT
# required: a scratch file that never reaches os.Rename is still a hand-rolled
# writer (headless/run had three such copies the old both-patterns rule missed).
chk_C6() {
  grep -v '^internal/atomicfile/' "$L/src.list" > "$T/noatomic.list"
  if g "$T/c6" "$T/noatomic.list" -lE '^func (writeAtomic|WriteAtomic|atomicWrite|AtomicWrite|writeFileAtomic|WriteFileAtomic)\(' &&
     g "$T/temps" "$T/noatomic.list" -l 'os\.CreateTemp('; then
    cat "$T/temps" >> "$T/c6"; ratchet C6-atomic-write atomic-writers "$T/c6"
  else say C6-atomic-write ERROR "grep could not read sources"; fi
}

# C7 one SQLite opener: sql.Open outside internal/sqlitedb/.
chk_C7() {
  grep -v '^internal/sqlitedb/' "$L/src.list" > "$T/nosql.list"
  if g "$T/raw" "$T/nosql.list" -nE 'sql\.Open\('; then count_by_file "$T/raw" > "$T/c7"; ratchet_counts C7-sql-open sql-openers "$T/c7"
  else say C7-sql-open ERROR "grep could not read sources"; fi
}

# C8 negation-named directories.
chk_C8() {
  if find internal cmd -type d \( -iname '*util*' -o -name helpers -o -name common -o -name misc -o -name shared \) > "$T/c8"; then ratchet C8-negation-dirs negation-dirs "$T/c8"
  else say C8-negation-dirs ERROR "find failed under internal/ cmd/"; fi
}

# C9 every package states what it owns in a `// Package` doc comment.
chk_C9() {
  awk '{sub(/\/[^/]*$/, ""); print}' "$L/src.list" | sort -u > "$T/pkgdirs"
  if g "$T/docs.list" "$L/src.list" -l '^// Package '; then
    awk '{sub(/\/[^/]*$/, ""); print}' "$T/docs.list" | sort -u > "$T/docdirs"
    comm -23 "$T/pkgdirs" "$T/docdirs" > "$T/c9"
    ratchet C9-package-doc no-package-doc "$T/c9"
  else
    say C9-package-doc ERROR "grep could not read sources"
  fi
}

# C10 MCP reaches chat verbs through typed calls, never argv into package main.
chk_C10() {
  grep '^internal/mcpserv/' "$L/src.list" > "$T/mcp.list"
  if [ ! -s "$T/mcp.list" ]; then say C10-mcp-argv ERROR "no internal/mcpserv sources listed"
  elif g "$T/raw" "$T/mcp.list" -nE 'backend\.dispatch\(|cliAction\('; then count_by_file "$T/raw" > "$T/c10"; ratchet_counts C10-mcp-argv mcp-argv-calls "$T/c10"
  else say C10-mcp-argv ERROR "grep could not read internal/mcpserv"; fi
}

# C11 one name per database file: "fleet.db" spelled in Go source.
chk_C11() {
  if g "$T/raw" "$L/src.list" -n '"fleet\.db"'; then count_by_file "$T/raw" > "$T/c11"; ratchet_counts C11-db-names fleet-db-spellings "$T/c11"
  else say C11-db-names ERROR "grep could not read sources"; fi
}

# C12 pfm/CLAUDE.md cites only what exists: packages, *.md docs, PFM_* variables something reads.
chk_C12() {
  if [ ! -f CLAUDE.md ]; then say C12-claude-pointers ERROR "pfm/CLAUDE.md missing"
  else
    : > "$T/c12"
    for p in $(grep -oE '^\| `[a-z/]+/`' CLAUDE.md | tr -d '|` '; grep -oE '`[a-z/]+/`' CLAUDE.md | grep -vE '^`(cmd|internal|testdata|e2e)' | tr -d '`'); do
      # A generated directory the repo root's .gitignore names (`/tmp/`) resolves before any build made it.
      [ -d "internal/$p" ] || [ -d "$p" ] || [ -d "../$p" ] \
        || grep -qxE "/?${p%/}/?" ../.gitignore 2>/dev/null || echo "$p" >> "$T/c12"
    done
    for f in $(grep -oE '`?[A-Z][A-Z_]+\.md`?' CLAUDE.md | tr -d '`' | sort -u); do [ -e "$f" ] || [ -e "../$f" ] || echo "$f" >> "$T/c12"; done
    # A PFM_* name counts as read only when production code uses it beyond
    # declaring it: the literal on a line that is not `name = "PFM_X"`, or the
    # declared constant's name on some other line. A constant only tests set is
    # a dead knob, not a read.
    grep -hE '"PFM_[A-Z_]+"' $(cat "$L/src.list") > "$T/uses-all" || true
    grep -oE '^[[:space:]]*(const[[:space:]]+)?[A-Za-z_][A-Za-z0-9_]*' "$T/uses-all" | awk '{print $NF}' | sort -u > "$T/use-names"
    : > "$T/name-uses"
    if [ -s "$T/use-names" ]; then
      grep -hwE "$(paste -sd '|' "$T/use-names")" $(cat "$L/src.list") > "$T/name-uses" || true
    fi
    grep -oE 'PFM_[A-Z_]+' CLAUDE.md | sort -u > "$T/vars"
    awk '
      FILENAME == ARGV[1] { vars[++count]=$0; next }
      FILENAME == ARGV[2] {
        for (i=1; i<=count; i++) {
          v=vars[i]
          if (index($0, "\"" v "\"") == 0) continue
          decl="^[[:space:]]*(const[[:space:]]+)?[A-Za-z_][A-Za-z0-9_]*[[:space:]]*(string[[:space:]]*)?=[[:space:]]*\"" v "\""
          if ($0 !~ decl) { read[v]=1; continue }
          match($0, /^[[:space:]]*(const[[:space:]]+)?[A-Za-z_][A-Za-z0-9_]*/)
          name=substr($0, RSTART, RLENGTH)
          sub(/^.*[[:space:]]/, "", name)
          names[v SUBSEP name]=1
        }
        next
      }
      {
        for (key in names) {
          split(key, parts, SUBSEP)
          v=parts[1]; name=parts[2]
          if (index($0, "\"" v "\"") == 0 && $0 ~ ("(^|[^[:alnum:]_])" name "([^[:alnum:]_]|$)")) read[v]=1
        }
      }
      END { for (i=1; i<=count; i++) { v=vars[i]; if (!read[v]) print v } }
    ' "$T/vars" "$T/uses-all" "$T/name-uses" >> "$T/c12"
    ratchet C12-claude-pointers claude-dangling "$T/c12"
  fi
}

# C13 tests mirror sources: every x.go has x_test.go.
chk_C13() {
  while read -r s; do [ -e "${s%.go}_test.go" ] || echo "$s"; done < "$L/src.list" > "$T/c13"
  ratchet C13-test-mirror untested-sources "$T/c13"
}

# C14 every dispatched top-level command appears in usage (structural once a command table lands).
chk_C14() {
  cases=$(awk '/^func run\(/,/^}/' cmd/pfm/main.go | grep -oE 'case "[a-z-]+"' | grep -oE '"[a-z-]+"' | tr -d '"' | grep -vE '^(help|version|internal)$')
  if [ -z "$cases" ]; then say C14-usage-parity ERROR "no case labels parsed from cmd/pfm/main.go run()"
  else
    usage="$(awk '/^func printUsage/,/^}/' cmd/pfm/main.go)"
    : > "$T/c14"; for c in $cases; do grep -qE "\"  $c " <<<"$usage" || echo "$c" >> "$T/c14"; done
    ratchet C14-usage-parity usage-missing "$T/c14"
  fi
}

# C15 every `pfm internal` entry appears in its usage line (structural once hooks.Table lands).
chk_C15() {
  iv=$(awk '/^func runInternal\(/,/^}/' cmd/pfm/main.go | grep -oE 'args\[0\] (==|!=) "[a-z-]+"' | grep -oE '"[a-z-]+"' | tr -d '"' | sort -u)
  line=$(grep -oE 'usage: pfm internal [a-z-]+(\|[a-z-]+)+' cmd/pfm/main.go | head -1)
  if [ -z "$iv" ]; then say C15-internal-usage ERROR "no entries parsed from cmd/pfm/main.go runInternal()"
  elif [ -z "$line" ]; then say C15-internal-usage ERROR "no multi-entry 'usage: pfm internal a|b' line in cmd/pfm/main.go"
  else
    : > "$T/c15"; for v in $iv; do grep -qE "(^|[ |])$v([|]|$)" <<<"$line" || echo "$v" >> "$T/c15"; done
    ratchet C15-internal-usage internal-usage-missing "$T/c15"
  fi
}

# C16 PFM_* environment reads stay inside internal/paths — spelled as a literal
# or through any constant that holds a "PFM_*" name (paths.EnvHome, a local
# fooEnv), since a read through a name is still a read.
chk_C16() {
  grep -v '^internal/paths/' "$L/src.list" > "$T/nopaths.list"
  if g "$T/decl" "$L/src.list" -ohE '\b[A-Za-z_][A-Za-z0-9_]*[[:space:]]*(string[[:space:]]*)?=[[:space:]]*"PFM_[A-Z0-9_]+"'; then
    names=$(grep -oE '^[A-Za-z_][A-Za-z0-9_]*' "$T/decl" | sort -u | paste -sd'|' -)
    envread='(Getenv|LookupEnv)\("PFM_'; [ -n "$names" ] && envread="$envread|(Getenv|LookupEnv)\\(([A-Za-z_]+\\.)?($names)\\)"
    if g "$T/raw" "$T/nopaths.list" -nE "$envread"; then count_by_file "$T/raw" > "$T/c16"; ratchet_counts C16-env-outside-paths env-outside-paths "$T/c16"
    else say C16-env-outside-paths ERROR "grep could not read sources"; fi
  else say C16-env-outside-paths ERROR "grep could not read the PFM_* name declarations"; fi
}

# C17 one free function per name: the same unexported free-function name in two
# files is a twin waiting to diverge (clipRunes x5, isLive/IsLive with opposite
# answers). Case-folded so IsLive and isLive collide. Methods are excluded (a
# String() per type is the language), as are _linux/_darwin pairs, which define
# one identifier twice BY DESIGN (pfm/CLAUDE.md § one binary, two kernels).
chk_C17() {
  grep -vE '_(linux|darwin)\.go$' "$L/src.list" > "$T/nokernel.list"
  if g "$T/raw" "$T/nokernel.list" -nE '^func [A-Za-z_][A-Za-z0-9_]*\('; then
    awk -F: '{ match($3, /^func [A-Za-z_][A-Za-z0-9_]*/); n=tolower(substr($3, 6, RLENGTH-5)); if (n!="main" && n!="init") print n" "$1 }' "$T/raw" \
      | sort -u | awk '{files[$1]=files[$1]" "$2; c[$1]++} END {for (n in c) if (c[n]>1) print n":"files[n]}' | sort > "$T/c17"
    ratchet C17-dup-functions dup-functions "$T/c17"
  else say C17-dup-functions ERROR "grep could not read function declarations"; fi
}

# C18 one spelling per engine: OpenCode is the brand; Opencode / Oc* / oc* are
# drift, and GPT is an undeclared synonym for Codex. Count per file, only shrinks.
chk_C18() {
  if g "$T/raw" "$L/all.list" -nE 'Opencode|\b[oO]c[A-Z][A-Za-z]+|GPT'; then count_by_file "$T/raw" > "$T/c18"; ratchet_counts C18-engine-spellings engine-spellings "$T/c18"
  else say C18-engine-spellings ERROR "grep could not read sources"; fi
}

# C19 one environment namespace: CHAT_*, CC_* reads are pfm's own
# variables under a foreign prefix — a `grep PFM_` never finds them.
chk_C19() {
  if g "$T/raw" "$L/src.list" -nE '(Getenv|LookupEnv)\("(CHAT|CC)_'; then count_by_file "$T/raw" > "$T/c19"; ratchet_counts C19-env-namespace env-namespace "$T/c19"
  else say C19-env-namespace ERROR "grep could not read sources"; fi
}

# C20 one name for ~/.codex: CodexHome. codexRoot / CodexRoot / AccountHome
# name the same directory in 43 files a `grep CodexHome` misses.
chk_C20() {
  if g "$T/raw" "$L/src.list" -nE '\b[cC]odexRoot\b|\bAccountHome\b'; then count_by_file "$T/raw" > "$T/c20"; ratchet_counts C20-codex-home codex-home "$T/c20"
  else say C20-codex-home ERROR "grep could not read sources"; fi
}

# C21 one test jail: a test that hand-rolls its scratch root with
# os.MkdirTemp("/tmp", ...) instead of testjail.ShortRoot has its own copy of
# the jail, and the six copies already disagree on the DB path.
chk_C21() {
  grep -v '^internal/testjail/' "$L/test.list" > "$T/nojail.list"
  if g "$T/raw" "$T/nojail.list" -n 'os\.MkdirTemp("/tmp"'; then count_by_file "$T/raw" > "$T/c21"; ratchet_counts C21-test-jail test-jail-copies "$T/c21"
  else say C21-test-jail ERROR "grep could not read tests"; fi
}

# C22 host doors stay inside their four seam packages: a bare os.Getenv/
# LookupEnv/UserHomeDir/user.Current/exec.Command/exec.CommandContext/
# exec.LookPath/time.Now/Sleep/After/NewTimer/NewTicker/Tick/net.Dial/
# net.Listen in non-test code outside internal/{clock,deps,paths,tmux} is a
# door the unit-test law (§ Three seams item 4) has not seamed yet; the baseline
# only shrinks as later batches migrate a package onto clock.Clock,
# deps.Runner, tmux.Fake or paths.Env. internal/mockengine + cmd/mock-engine
# are the fifth seam: the mock IS a host (exec, env, files, clock) — the thing
# the other four fake — so its doors are its purpose, not a leak to migrate.
chk_C22() {
  grep -vE '^(internal/(clock|deps|paths|tmux|mockengine)|cmd/mock-engine)/' "$L/src.list" > "$T/noseam.list"
  if g "$T/raw" "$T/noseam.list" -nE 'os\.Getenv|LookupEnv|UserHomeDir|user\.Current|exec\.Command|exec\.CommandContext|exec\.LookPath|time\.Now|time\.Sleep|time\.After|time\.NewTimer|time\.NewTicker|time\.Tick|net\.Dial|net\.Listen'; then
    count_by_file "$T/raw" > "$T/c22"; ratchet_counts C22-host-doors host-doors "$T/c22"
  else say C22-host-doors ERROR "grep could not read sources"; fi
}

# C23 one activity log: a bare log.Print*/log.Fatal* or a hand-rolled
# fmt.Fprint*(os.Stderr in non-test code writes where nothing can read it back
# — no level, no fields, no destination a field report or a lane beat can
# attach. internal/obs IS the destination and cmd/pfm's stderr IS a verb's user-facing
# output, so both are outside the count; every other package moves onto
# obs.Logger/obs.Span as part B migrates it, and this baseline only shrinks.
chk_C23() {
  grep -vE '^(internal/obs/|cmd/pfm/)' "$L/src.list" > "$T/noobs.list"
  if g "$T/raw" "$T/noobs.list" -nHE '\blog\.(Print|Fatal)|fmt\.Fprint[a-zA-Z]*\(os\.Stderr'; then
    count_by_file "$T/raw" > "$T/c23"; ratchet_counts C23-bare-log bare-log "$T/c23"
  else say C23-bare-log ERROR "grep could not read sources"; fi
}

# C25 one test jail per test process: a package with tests whose TestMain does
# not reach testjail.Run runs its tests against the real HOME and the live fleet
# database, and its profile row is never written. A package is judged by the
# directory of its _test.go files (testdata trees are fixtures, not packages);
# internal/testjail IS the jail, so its TestMain calls Run(m) unqualified.
chk_C25() {
  grep -vE '(^|/)testdata/' "$L/test.list" > "$T/judged.list"
  if [ ! -s "$T/judged.list" ]; then
    say C25-testmain-jail PASS "0 test packages"
    return
  fi
  # One grep per question over every judged file, then the decision per directory
  # on the file lists: a directory is jailed when one of its files both defines
  # TestMain and calls the jail. The jail's own package (the directory
  # internal/testjail, not the trees under it) calls Run unqualified.
  grep -vE '^internal/testjail/[^/]+$' "$T/judged.list" > "$T/other.list"
  grep -E '^internal/testjail/[^/]+$' "$T/judged.list" > "$T/self.list"
  c25_err=0
  g "$T/mains" "$T/judged.list" -l 'func TestMain(' || c25_err=1
  : > "$T/calls"
  if [ -s "$T/other.list" ]; then
    g "$T/calls" "$T/other.list" -lE 'testjail\.Run\(' || c25_err=1
  fi
  if [ -s "$T/self.list" ]; then
    if g "$T/selfcalls" "$T/self.list" -lE '(^|[^[:alnum:]_.])Run\('; then cat "$T/selfcalls" >> "$T/calls"; else c25_err=1; fi
  fi
  if [ "$c25_err" -eq 1 ]; then say C25-testmain-jail ERROR "grep could not read tests"; return; fi
  sort -u "$T/mains" > "$T/mains.sorted"
  sort -u "$T/calls" > "$T/calls.sorted"
  comm -12 "$T/mains.sorted" "$T/calls.sorted" | sed 's|/[^/]*$||' | sort -u > "$T/jailed.dirs"
  sed 's|/[^/]*$||' "$T/judged.list" | sort -u > "$T/dirs"
  comm -23 "$T/dirs" "$T/jailed.dirs" > "$T/c25"
  ratchet C25-testmain-jail testmain-jail "$T/c25"
}

# C26 one executable writer in tests: a test that writes a file with an exec
# bit and then runs it races every fork of its process — a child forked while
# the file is open for writing keeps the write descriptor until its own exec
# closes it, and the run fails with ETXTBSY ("text file busy"). Every such write
# goes through testjail.WriteExecutable, which holds syscall.ForkLock's read
# side. Judged: every _test.go and every file of internal/testjail. Flagged: an
# os.WriteFile (a call spanning lines included) whose mode is a literal with the
# owner-exec bit, or not a literal at all (a mode the caller passes may carry
# one). Exempt: a write inside a function that takes syscall.ForkLock.RLock() —
# the helper itself and its twins in the packages testjail imports (deps,
# config), which cannot import it back.
chk_C26() {
  { cat "$L/test.list"; grep '^internal/testjail/' "$L/src.list"; } | sort -u > "$T/judged.list"
  [ -s "$T/judged.list" ] || { say C26-exec-write ERROR "no test files listed — the enumerator did not run"; return; }
  # A character scanner, not a line grep: it follows a call across lines and
  # skips parentheses and commas inside strings, runes and raw strings.
  # shellcheck disable=SC2016 # the awk program is single-quoted on purpose
  if ! awk '
    function judge(  m, d, v) {
      m = arg; if (m !~ /[^ \t]/) m = last
      gsub(/^[ \t]+|[ \t]+$/, "", m)
      if (locked) return
      if (m ~ /^0[oO]?[0-7_]+$/) { d = m; sub(/^0[oO]?/, "", d); gsub(/_/, "", d)
        if (length(d) >= 3 && substr(d, length(d) - 2, 1) ~ /[1357]/) print FILENAME ":" start; return }
      if (m ~ /^[1-9][0-9_]*$/) { v = m; gsub(/_/, "", v); if (int(v / 64) % 2 == 1) print FILENAME ":" start; return }
      print FILENAME ":" start
    }
    FNR == 1 { incall = 0; inraw = 0; locked = 0 }
    /^func / { locked = 0 }
    {
      line = $0; n = length(line); i = 1; instr = 0
      if (!inraw && index(line, "syscall.ForkLock.RLock()")) locked = 1
      while (i <= n) {
        c = substr(line, i, 1)
        if (inraw) { if (c == "`") inraw = 0; if (incall) arg = arg c; i++; continue }
        if (instr) {
          if (c == "\\") { if (incall) arg = arg substr(line, i, 2); i += 2; continue }
          if (c == q) instr = 0
          if (incall) arg = arg c; i++; continue
        }
        if (c == "/" && substr(line, i + 1, 1) == "/") break
        if (c == "\"" || c == "\047") { instr = 1; q = c; if (incall) arg = arg c; i++; continue }
        if (c == "`") { inraw = 1; if (incall) arg = arg c; i++; continue }
        if (!incall) {
          if (substr(line, i, 13) == "os.WriteFile(" && (i == 1 || substr(line, i - 1, 1) !~ /[A-Za-z0-9_.]/)) {
            incall = 1; depth = 1; arg = ""; last = ""; start = FNR; i += 13; continue
          }
          i++; continue
        }
        if (c == "(" || c == "[" || c == "{") depth++
        else if (c == ")" || c == "]" || c == "}") { depth--; if (depth == 0) { judge(); incall = 0; i++; continue } }
        else if (c == "," && depth == 1) { if (arg ~ /[^ \t]/) last = arg; arg = ""; i++; continue }
        arg = arg c; i++
      }
      if (incall) arg = arg " "
    }
  ' $(cat "$T/judged.list") > "$T/c26"; then
    say C26-exec-write ERROR "awk could not read the test files"; return
  fi
  ratchet C26-exec-write exec-writes "$T/c26"
}

# C24 lives in its own script; its exit status (0 PASS · 1 FAIL · 2 ERROR) joins the
# maximum below, and the status file is how the job hands it over.
chk_C24() { bash "$PFM/scripts/arch-c24.sh" "$MODE"; echo $? > "$L/jobs/C24.status"; }

# Start every check; $L/jobs/<id>.out is a job's stdout, and a job that is killed
# or fails before its CHECK line leaves none. Baselines are one file per check, so
# a --measure job writes only its own.
mkdir -p "$L/jobs" || { say setup ERROR "cannot create $L/jobs"; exit 2; }
IDS=(); PIDS=()
job() { # job <CHECK id>: run chk_<the id up to its first dash> in the background
  local id=$1
  ( T="$L/jobs/$id.d"; mkdir -p "$T" || exit 3; "chk_${id%%-*}"; exit 0 ) > "$L/jobs/$id.out" &
  IDS+=("$id"); PIDS+=("$!")
}
for id in C1-ceiling-src C2-ceiling-test C3-cmd-budget C4-cmd-primitives C5-tmux-runner \
  C6-atomic-write C7-sql-open C8-negation-dirs C9-package-doc C10-mcp-argv C11-db-names \
  C12-claude-pointers C13-test-mirror C14-usage-parity C15-internal-usage \
  C16-env-outside-paths C17-dup-functions C18-engine-spellings C19-env-namespace \
  C20-codex-home C21-test-jail C22-host-doors C23-bare-log C25-testmain-jail C26-exec-write C24-unwrapped-door; do
  job "$id"
done

# Print in the order the jobs were started, folding rc from the printed lines
# (FAIL 1, ERROR 2, the maximum). A job whose output holds no CHECK line of its own
# id is an ERROR for that check, whatever else it printed.
for i in "${!IDS[@]}"; do
  id=${IDS[$i]}; out="$L/jobs/$id.out"
  wait "${PIDS[$i]}"; job_status=$?
  if awk -v id="$id" '$1 == "CHECK" && $2 == id && ($3 == "PASS" || $3 == "FAIL" || $3 == "ERROR" || $3 == "MEASURE") {found=1} END {exit !found}' "$out"; then
    cat "$out"
    awk '$1 == "CHECK" && $3 == "ERROR" {e=1} $1 == "CHECK" && $3 == "FAIL" {f=1} END {exit (e ? 2 : (f ? 1 : 0))}' "$out"; line_rc=$?
    [ "$line_rc" -gt "$rc" ] && rc=$line_rc
  else
    say "$id" ERROR "check job ended (status $job_status) without leaving its CHECK line"
  fi
done
if [ -f "$L/jobs/C24.status" ]; then read -r c24 < "$L/jobs/C24.status"; [ "$c24" -gt "$rc" ] && rc=$c24; fi
exit $rc
