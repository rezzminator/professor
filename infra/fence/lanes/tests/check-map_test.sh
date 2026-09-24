#!/usr/bin/env bash
# Fixture-driven tests for lanes/check-map.sh — map.tsv is `name · lane · beat`.
# Check 1 (MISSING-BEAT, pending lanes, PENDING-STALE, UNDECLARED-LANE), the
# map's own broken states (MAP-UNREADABLE, MALFORMED-ROW), the derive against a
# stub pfm (UNMAPPED-COMMAND, UNMAPPED-TOOL, STALE-NAME, a hidden verb pfm's
# dispatcher still knows) and the one thing a gate must never do: report clean
# for a check it could not run (DERIVE-FAILED).
# Runs against a COPY of the lanes directory.
#
#   bash infra/fence/lanes/tests/check-map_test.sh
#   LANE_SUT_DIR=/tmp/mutated-lanes bash …/check-map_test.sh   # red-first
set -uo pipefail

SUT_DIR="${LANE_SUT_DIR:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)}"
SHTEST_TAG=lane-checkmap-test
# shellcheck source=/dev/null
source "$(dirname -- "${BASH_SOURCE[0]}")/../../../../scripts/shtest.sh"

LANES="$T/lanes"
mkdir -p "$LANES"
cp "$SUT_DIR"/*.sh "$SUT_DIR"/*.yml "$SUT_DIR"/pending.txt "$LANES/" 2>/dev/null
# The copy brings every REAL lane script along (uppercase names: E1.sh, F.sh…);
# this suite's lanes are the two fixtures written below and nothing else, so a
# lane landing in the real directory can never flip a pending-lane case here.
find "$LANES" -maxdepth 1 -name '[A-Z]*.sh' -delete
SUT="$LANES/check-map.sh"
[ -f "$SUT" ] || { echo "check-map_test: no check-map.sh at $SUT" >&2; exit 2; }

map() { printf 'name\tlane\tbeat\n%b' "$1" >"$LANES/map.tsv"; }
CLEAN='pfm alpha\tE1\tE1.01-fixture\npfm chat new\tO1\tO1.01-fixture\nchat_ls\tE1\tE1.01-fixture\n'

# Fixture lanes: E1 and O1 are written lanes with one beat each.
printf '#!/usr/bin/env bash\nbeat E1.01-fixture\n' >"$LANES/E1.sh"
printf '#!/usr/bin/env bash\nbeat O1.01-fixture\n' >"$LANES/O1.sh"
printf 'F\n' >"$LANES/pending.txt"

# PATH with no pfm at all, so the derive cannot run unless a test provides one.
BIN="$T/bin"
mkdir -p "$BIN"
for tool in bash awk sed grep sort uniq head tail cut tr wc find mktemp rm cat printf jq basename dirname expr date cp cmp diff env sleep timeout; do
  real="$(command -v "$tool" 2>/dev/null)" || continue
  ln -sf "$real" "$BIN/$tool"
done

run_sut() { OUT="$(env PATH="$BIN" bash "$SUT" "$@" 2>&1)"; RC=$?; }

# ---- 1: a clean fixture map, derive skipped, names that it was skipped ----

map "$CLEAN"
run_sut --no-derive
if [ "$RC" -eq 0 ] &&
  printf '%s' "$OUT" | grep -q '3 map rows' &&
  printf '%s' "$OUT" | grep -q 'lane E1: written · 1/1 mapped beats present' &&
  printf '%s' "$OUT" | grep -q 'derive: NOT RUN (--no-derive)'; then
  ok "clean map + --no-derive: exit 0, and the skipped derive is NAMED, not implied clean"
else
  bad "clean map" "rc=$RC" "$OUT"
fi

# ---- 2: a mapped beat that no written lane carries ----------------------

map 'pfm alpha\tE1\tE1.01-fixture\nchat_ls\tE1\tE1.99-ghost\n'
run_sut --no-derive
if [ "$RC" -eq 1 ] && printf '%s' "$OUT" | grep -q "MISSING-BEAT: E1.99-ghost is mapped to lane E1 but E1.sh has no 'beat E1.99-ghost' line"; then
  ok "MISSING-BEAT: a mapped beat absent from its written lane is named"
else
  bad "missing beat" "rc=$RC" "$OUT"
fi

# ---- 3: a pending lane is a NAMED line, not a silent hole --------------

map "${CLEAN}pfm beta\tF\tF.01-later\n"
run_sut --no-derive
if [ "$RC" -eq 0 ] && printf '%s' "$OUT" | grep -q 'lane F: NOT WRITTEN (1 beats pending) — declared in pending.txt' &&
  printf '%s' "$OUT" | grep -q 'pending lanes: 1 — this list must reach 0'; then
  ok "a pending lane is reported NOT WRITTEN with its beat count, exit still 0 while it is declared"
else
  bad "pending lane" "rc=$RC" "$OUT"
fi

# ---- 4: a pending list that has rotted is red -------------------------

printf 'F\nE1\n' >"$LANES/pending.txt"
run_sut --no-derive
if [ "$RC" -eq 1 ] && printf '%s' "$OUT" | grep -q 'PENDING-STALE'; then
  ok "PENDING-STALE: pending.txt naming a lane whose script exists is red"
else
  bad "pending stale" "rc=$RC" "$OUT"
fi
printf 'F\n' >"$LANES/pending.txt"

# ---- 5: a lane in the map that is neither written nor declared --------

map "${CLEAN}pfm beta\tQ9\tQ9.01-nowhere\n"
run_sut --no-derive
if [ "$RC" -eq 1 ] && printf '%s' "$OUT" | grep -q 'UNDECLARED-LANE: Q9'; then
  ok "UNDECLARED-LANE: a mapped lane with no script and no pending line is red"
else
  bad "undeclared lane" "rc=$RC" "$OUT"
fi

# ---- 6: a map the gate cannot read is exit 2, never an empty clean sweep --

rm -f "$LANES/map.tsv"
run_sut --no-derive
missing_rc="$RC" missing_out="$OUT"
printf 'landscape_id\tlane\tbeat\nZ1\tE1\tE1.01-fixture\n' >"$LANES/map.tsv"
run_sut --no-derive
oldhdr_rc="$RC" oldhdr_out="$OUT"
map ''
run_sut --no-derive
if [ "$missing_rc" -eq 2 ] && printf '%s' "$missing_out" | grep -q 'MAP-UNREADABLE — .*map.tsv does not exist' &&
  [ "$oldhdr_rc" -eq 2 ] && printf '%s' "$oldhdr_out" | grep -q "MAP-UNREADABLE — line 1 of map.tsv is not the 'name<TAB>lane<TAB>beat' header" &&
  [ "$RC" -eq 2 ] && printf '%s' "$OUT" | grep -q 'MAP-UNREADABLE — map.tsv has no' &&
  ! printf '%s\n%s\n%s' "$missing_out" "$oldhdr_out" "$OUT" | grep -q '^check-map: clean'; then
  ok "MAP-UNREADABLE: a missing map, a wrong header and a header-only map each exit 2, never clean"
else
  bad "unreadable map" "missing rc=$missing_rc, old header rc=$oldhdr_rc, empty rc=$RC" "$missing_out
$oldhdr_out
$OUT"
fi

# ---- 7: a row that is not three fields is named by line ----------------

map "${CLEAN}pfm beta E1\n"
run_sut --no-derive
if [ "$RC" -eq 1 ] && printf '%s' "$OUT" | grep -q 'MALFORMED-ROW: line 5 of map.tsv is not three tab-separated fields'; then
  ok "MALFORMED-ROW: a row without three tab-separated fields is named by its line"
else
  bad "malformed row" "rc=$RC" "$OUT"
fi

# ---- 8: no pfm binary → DERIVE-FAILED, exit 2, never 'clean' ----------

map "$CLEAN"
run_sut
if [ "$RC" -eq 2 ] &&
  printf '%s' "$OUT" | grep -q 'DERIVE-FAILED: no pfm binary to ask' &&
  printf '%s' "$OUT" | grep -q 'this is not a clean verdict' &&
  ! printf '%s' "$OUT" | grep -q '^check-map: clean'; then
  ok "DERIVE-FAILED: no pfm binary exits 2, names the reason, and refuses to say clean"
else
  bad "derive failed" "rc=$RC" "$OUT"
fi

# ---- 9: a pfm whose help tree cannot be read is DERIVE-FAILED too ------

printf '#!/usr/bin/env bash\nexit 1\n' >"$BIN/pfm"
chmod +x "$BIN/pfm"
run_sut
if [ "$RC" -eq 2 ] && printf '%s' "$OUT" | grep -q 'DERIVE-FAILED: pfm --help produced'; then
  ok "DERIVE-FAILED: a binary that answers nothing to --help is named, not counted as zero commands"
else
  bad "derive unreadable help" "rc=$RC" "$OUT"
fi

# ---- 10: the derive against a stub pfm ----------------------------------
# The stub serves five top-level commands, five chat subcommands, one tool per
# MCP server, a hidden `chat secret` and `internal hook-x` its dispatcher
# knows, and answers `unknown command` for everything else — the real pfm's
# answer for a verb it does not have.

cat >"$BIN/pfm" <<'STUB'
#!/usr/bin/env bash
shift 2 # --config PATH
case "$*" in
  "--help") printf 'usage: pfm <command>\n  alpha\n  beta\n  chat\n  internal\n  mcp\n' ;;
  "chat --help") printf 'usage: pfm chat <command>\n  new\n  ls\n  inject\n  status\n  kill/unkill\n' ;;
  "mcp chat serve") echo '{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"chat_ls"}]}}' ;;
  "mcp harvester serve") echo '{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"read"}]}}' ;;
  "chat secret --help") echo 'usage: pfm chat secret <x>'; exit 2 ;;
  "internal hook-x --help") exit 0 ;;
  chat\ *) echo "pfm chat: unknown command \"$2\""; exit 2 ;;
  internal\ *) echo "pfm internal: unknown subcommand \"$2\""; exit 1 ;;
  *) echo "pfm: unknown command \"$1\""; exit 2 ;;
esac
STUB
chmod +x "$BIN/pfm"
FULL='pfm alpha\tE1\tE1.01-fixture\npfm beta\tE1\tE1.01-fixture\npfm internal hook-x\tE1\tE1.01-fixture\npfm mcp\tE1\tE1.01-fixture\npfm chat new\tO1\tO1.01-fixture\npfm chat ls\tO1\tO1.01-fixture\npfm chat inject\tO1\tO1.01-fixture\npfm chat status\tO1\tO1.01-fixture\npfm chat kill\tO1\tO1.01-fixture\npfm chat unkill\tO1\tO1.01-fixture\npfm chat secret\tO1\tO1.01-fixture\nchat_ls\tE1\tE1.01-fixture\nread\tE1\tE1.01-fixture\n'

map "$FULL"
run_sut
if [ "$RC" -eq 0 ] &&
  printf '%s' "$OUT" | grep -q 'derived commands: 5 top-level + 6 chat subcommands · 0 unmapped' &&
  printf '%s' "$OUT" | grep -q '0 stale · 2 judged by pfm.s dispatcher' &&
  printf '%s' "$OUT" | grep -q '^check-map: clean — .*no stale row'; then
  ok "derive clean: every command and tool mapped, and the hidden verbs pass on the dispatcher's word"
else
  bad "derive clean" "rc=$RC" "$OUT"
fi

map "$(printf '%b' "$FULL" | grep -v '^pfm beta')
"
run_sut
if [ "$RC" -eq 1 ] && printf '%s' "$OUT" | grep -q 'UNMAPPED-COMMAND: pfm beta — no row in map.tsv names it'; then
  ok "UNMAPPED-COMMAND: a command in pfm's help tree with no row is named"
else
  bad "unmapped command" "rc=$RC" "$OUT"
fi

map "$(printf '%b' "$FULL" | grep -v '^read')
"
run_sut
if [ "$RC" -eq 1 ] && printf '%s' "$OUT" | grep -q 'UNMAPPED-TOOL: harvester/read — no row in map.tsv names it'; then
  ok "UNMAPPED-TOOL: a served MCP tool with no row is named with its server"
else
  bad "unmapped tool" "rc=$RC" "$OUT"
fi

map "${FULL}pfm storm\tE1\tE1.01-fixture\npfm chat gone\tO1\tO1.01-fixture\npfm internal gone\tE1\tE1.01-fixture\nsearch_gone\tE1\tE1.01-fixture\n"
run_sut
if [ "$RC" -eq 1 ] &&
  printf '%s' "$OUT" | grep -q 'STALE-NAME: pfm storm — a map.tsv row names a command or tool pfm does not serve' &&
  printf '%s' "$OUT" | grep -q 'STALE-NAME: pfm chat gone' &&
  printf '%s' "$OUT" | grep -q 'STALE-NAME: pfm internal gone' &&
  printf '%s' "$OUT" | grep -q 'STALE-NAME: search_gone' &&
  printf '%s' "$OUT" | grep -q '4 stale · 2 judged'; then
  ok "STALE-NAME: a row for a verb or tool pfm answers 'unknown' to is red, top-level, chat, internal and tool alike"
else
  bad "stale name" "rc=$RC" "$OUT"
fi

shtest_end
