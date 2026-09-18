#!/usr/bin/env bash
# Fixture-driven tests for lanes/check-map.sh — the three map findings
# (UNMAPPED-ID, MISSING-BEAT, PENDING-STALE / NOT WRITTEN), the machine-read
# landscape gate (LANDSCAPE-FORMATTABLE, the byte-stability run, its named
# NOT-PERFORMED line) and the one thing a gate must never do: report clean for a
# check it could not run (DERIVE-FAILED).
# Runs against a COPY of the lanes directory and a tiny fixture landscape.
#
#   bash infra/fence/lanes/tests/check-map_test.sh
#   LANE_SUT_DIR=/tmp/mutated-lanes bash …/check-map_test.sh   # red-first
set -uo pipefail

SUT_DIR="${LANE_SUT_DIR:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)}"
T="$(mktemp -d "${TMPDIR:-/tmp}/lane-checkmap-test.XXXXXX")"
trap 'rm -rf -- "$T"' EXIT

PASS=0 FAIL=0
ok() { printf 'PASS  %s\n' "$1"; PASS=$((PASS + 1)); }
bad() { printf 'FAIL  %s\n' "$1" >&2; shift; [ $# -gt 0 ] && printf '      %s\n' "$@" >&2; FAIL=$((FAIL + 1)); }

LANES="$T/lanes"
mkdir -p "$LANES"
cp "$SUT_DIR"/*.sh "$SUT_DIR"/*.yml "$SUT_DIR"/pending.txt "$LANES/" 2>/dev/null
# The copy brings every REAL lane script along (uppercase names: E1.sh, F.sh…);
# this suite's lanes are the two fixtures written below and nothing else, so a
# lane landing in the real directory can never flip a pending-lane case here.
find "$LANES" -maxdepth 1 -name '[A-Z]*.sh' -delete
SUT="$LANES/check-map.sh"
[ -f "$SUT" ] || { echo "check-map_test: no check-map.sh at $SUT" >&2; exit 2; }

LAND="$T/landscape.md"
cat >"$LAND" <<'MD'
<!-- rumdl-disable -->
# fixture landscape
Z1 · `pfm chat status <target>` base · needs:none · today:U · fixture:1 · lane(s):E1
Z2 · `pfm doctor` fixture row · needs:none · today:U · fixture:2 · lane(s):O1
Z3 · `chat_ls` fixture tool · needs:none · today:U · fixture:3 · lane(s):E1
MD

map() { printf 'landscape_id\tlane\tbeat\n%b' "$1" >"$LANES/map.tsv"; }

# Fixture lanes: E1 carries a beat, O1 is a written lane with one beat.
cat >"$LANES/E1.sh" <<'LANE'
#!/usr/bin/env bash
beat E1.01-fixture Z1
LANE
cat >"$LANES/O1.sh" <<'LANE'
#!/usr/bin/env bash
beat O1.01-fixture Z2
LANE
printf 'F\n' >"$LANES/pending.txt"

# PATH with no pfm at all, so the derive cannot run unless a test provides one.
BIN="$T/bin"
mkdir -p "$BIN"
for tool in bash awk sed grep sort uniq head tail cut tr wc find mktemp rm cat printf jq basename dirname expr date cp cmp diff; do
  real="$(command -v "$tool" 2>/dev/null)" || continue
  ln -sf "$real" "$BIN/$tool"
done

run_sut() { OUT="$(env PATH="$BIN" LANE_LANDSCAPE="$LAND" bash "$SUT" "$@" 2>&1)"; RC=$?; }

# ---- 1: a clean fixture map, derive skipped, names that it was skipped ----

map 'Z1\tE1\tE1.01-fixture\nZ2\tO1\tO1.01-fixture\nZ3\tE1\tE1.01-fixture\n'
run_sut --no-derive
if [ "$RC" -eq 0 ] &&
  printf '%s' "$OUT" | grep -q '3/3 landscape ids mapped' &&
  printf '%s' "$OUT" | grep -q 'lane E1: written · 1/1 mapped beats present' &&
  printf '%s' "$OUT" | grep -q 'derive: NOT RUN (--no-derive)'; then
  ok "clean map + --no-derive: exit 0, and the skipped derive is NAMED, not implied clean"
else
  bad "clean map" "rc=$RC" "$OUT"
fi

# ---- 1a: the landscape without its machine-read marker is red -------------
# (Wave 8 item 8: a formatter reflowed the 429 id rows into 27 paragraphs)

UNMARKED="$T/unmarked.md"
tail -n +2 "$LAND" >"$UNMARKED"
OUT="$(env PATH="$BIN" LANE_LANDSCAPE="$UNMARKED" bash "$SUT" --no-derive 2>&1)"; RC=$?
if [ "$RC" -eq 1 ] &&
  printf '%s' "$OUT" | grep -q "LANDSCAPE-FORMATTABLE: line 1 of unmarked.md is not '<!-- rumdl-disable -->'" &&
  ! printf '%s' "$OUT" | grep -q '^check-map: clean'; then
  ok "LANDSCAPE-FORMATTABLE: a landscape whose line 1 is not the rumdl-disable marker is red, never clean"
else
  bad "unmarked landscape" "rc=$RC" "$OUT"
fi

# ---- 1b: marker present, rumdl absent → the formatter half is NAMED not run --

run_sut --no-derive
if [ "$RC" -eq 0 ] &&
  printf '%s' "$OUT" | grep -q 'landscape machine-read: marker on line 1; rumdl not on PATH — the formatter run itself was NOT PERFORMED'; then
  ok "marker present, no rumdl: exit 0 and the skipped formatter run is NAMED, never implied"
else
  bad "marker without rumdl" "rc=$RC" "$OUT"
fi

# ---- 1c: marker present, rumdl present → a copy is formatted and compared --

if real_rumdl="$(command -v rumdl 2>/dev/null)"; then
  ln -sf "$real_rumdl" "$BIN/rumdl"
  run_sut --no-derive
  if [ "$RC" -eq 0 ] &&
    printf '%s' "$OUT" | grep -q 'landscape machine-read: marker on line 1, byte-stable under rumdl fmt'; then
    ok "marker present, rumdl present: the copy is byte-stable and the line says the run happened"
  else
    bad "marker with rumdl" "rc=$RC" "$OUT"
  fi
  rm -f "$BIN/rumdl"
else
  printf 'SKIP  rumdl is not on this host — the byte-stability run (1c) was NOT exercised\n'
fi

# ---- 2: an unmapped landscape id is a finding, exit 1 ---------------------

map 'Z1\tE1\tE1.01-fixture\nZ2\tO1\tO1.01-fixture\n'
run_sut --no-derive
if [ "$RC" -eq 1 ] && printf '%s' "$OUT" | grep -q 'UNMAPPED-ID: Z3 has no row in map.tsv' &&
  printf '%s' "$OUT" | grep -q '2/3 landscape ids mapped'; then
  ok "UNMAPPED-ID: the id with no row is named and the count says 2/3"
else
  bad "unmapped id" "rc=$RC" "$OUT"
fi

# ---- 3: a mapped beat that no written lane carries ----------------------

map 'Z1\tE1\tE1.01-fixture\nZ2\tO1\tO1.01-fixture\nZ3\tE1\tE1.99-ghost\n'
run_sut --no-derive
if [ "$RC" -eq 1 ] && printf '%s' "$OUT" | grep -q "MISSING-BEAT: E1.99-ghost is mapped to lane E1 but E1.sh has no 'beat E1.99-ghost' line"; then
  ok "MISSING-BEAT: a mapped beat absent from its written lane is named"
else
  bad "missing beat" "rc=$RC" "$OUT"
fi

# ---- 4: a pending lane is a NAMED line, not a silent hole --------------

map 'Z1\tE1\tE1.01-fixture\nZ2\tO1\tO1.01-fixture\nZ3\tF\tF.01-later\n'
run_sut --no-derive
if [ "$RC" -eq 0 ] && printf '%s' "$OUT" | grep -q 'lane F: NOT WRITTEN (1 beats pending) — declared in pending.txt' &&
  printf '%s' "$OUT" | grep -q 'pending lanes: 1 — this list must reach 0'; then
  ok "a pending lane is reported NOT WRITTEN with its beat count, exit still 0 while it is declared"
else
  bad "pending lane" "rc=$RC" "$OUT"
fi

# ---- 5: a pending list that has rotted is red -------------------------

printf 'F\nE1\n' >"$LANES/pending.txt"
run_sut --no-derive
if [ "$RC" -eq 1 ] && printf '%s' "$OUT" | grep -q 'PENDING-STALE'; then
  ok "PENDING-STALE: pending.txt naming a lane whose script exists is red"
else
  bad "pending stale" "rc=$RC" "$OUT"
fi
printf 'F\n' >"$LANES/pending.txt"

# ---- 6: a lane in the map that is neither written nor declared --------

map 'Z1\tE1\tE1.01-fixture\nZ2\tO1\tO1.01-fixture\nZ3\tQ9\tQ9.01-nowhere\n'
run_sut --no-derive
if [ "$RC" -eq 1 ] && printf '%s' "$OUT" | grep -q 'UNDECLARED-LANE: Q9'; then
  ok "UNDECLARED-LANE: a mapped lane with no script and no pending line is red"
else
  bad "undeclared lane" "rc=$RC" "$OUT"
fi

# ---- 7: no pfm binary → DERIVE-FAILED, exit 2, never 'clean' ----------

map 'Z1\tE1\tE1.01-fixture\nZ2\tO1\tO1.01-fixture\nZ3\tE1\tE1.01-fixture\n'
run_sut
if [ "$RC" -eq 2 ] &&
  printf '%s' "$OUT" | grep -q 'DERIVE-FAILED: no pfm binary to ask' &&
  printf '%s' "$OUT" | grep -q 'this is not a clean verdict' &&
  ! printf '%s' "$OUT" | grep -q '^check-map: clean'; then
  ok "DERIVE-FAILED: no pfm binary exits 2, names the reason, and refuses to say clean"
else
  bad "derive failed" "rc=$RC" "$OUT"
fi

# ---- 8: a pfm whose help tree cannot be read is DERIVE-FAILED too ------

cat >"$BIN/pfm" <<'STUB'
#!/usr/bin/env bash
exit 1
STUB
chmod +x "$BIN/pfm"
run_sut
if [ "$RC" -eq 2 ] && printf '%s' "$OUT" | grep -q 'DERIVE-FAILED: pfm --help produced'; then
  ok "DERIVE-FAILED: a binary that answers nothing to --help is named, not counted as zero commands"
else
  bad "derive unreadable help" "rc=$RC" "$OUT"
fi
rm -f "$BIN/pfm"

# ---- 9: a landscape the gate cannot read is exit 2, not 'every id mapped' --

run_sut --no-derive
saved_rc="$RC"
OUT="$(env PATH="$BIN" LANE_LANDSCAPE="$T/no-such-landscape.md" bash "$SUT" --no-derive 2>&1)"
RC=$?
if [ "$RC" -eq 2 ] && printf '%s' "$OUT" | grep -q 'LANDSCAPE-UNREADABLE'; then
  ok "an unreadable landscape is LANDSCAPE-UNREADABLE (exit 2), never an empty clean sweep"
else
  bad "unreadable landscape" "rc=$RC (map run was $saved_rc)" "$OUT"
fi

# ---- 10: UNMAPPED-BEAT — direction (b), script → map.tsv ------------------
# A beat added to a WRITTEN lane script with real landscape ids but no
# map.tsv row must be caught even though direction (a) (map.tsv → script)
# never walks it — the gate must see the missing row, not just its mirror.

beats() { printf '%b' "$1" >"$LANES/beats.md"; }
printf 'beat O1.02-codeonly Z9\n' >>"$LANES/O1.sh"
map 'Z1\tE1\tE1.01-fixture\nZ2\tO1\tO1.01-fixture\nZ3\tE1\tE1.01-fixture\n'
beats '- `O1.02-codeonly` · fixture code-only beat with REAL ids · spends none · Z9\n'
run_sut --no-derive
if [ "$RC" -eq 1 ] && printf '%s' "$OUT" | grep -q "UNMAPPED-BEAT: O1.02-codeonly is in O1.sh with no row in map.tsv"; then
  ok "UNMAPPED-BEAT: a coded beat with real landscape ids and no map row is caught (direction b)"
else
  bad "unmapped beat (real ids)" "rc=$RC" "$OUT"
fi

# ---- 11: the SAME shape, but beats.md marks it (none) — stays clean -------
# The six benign code-only beats today (A.11-guard-hook etc.) all carry
# (none) in beats.md; direction (b) must leave them alone.

beats '- `O1.02-codeonly` · fixture code-only beat, deliberately unmapped · spends none · (none)\n'
run_sut --no-derive
if [ "$RC" -eq 0 ] && ! printf '%s' "$OUT" | grep -q 'UNMAPPED-BEAT'; then
  ok "a coded beat beats.md marks (none) is left alone by direction b — the six benign ids stay green"
else
  bad "unmapped beat marked none" "rc=$RC" "$OUT"
fi

printf '\n%d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
