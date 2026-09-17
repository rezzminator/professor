#!/usr/bin/env bash
# Fixture-driven tests for lanes/creds.sh — the linux credential path against a
# FAKE home and a docker stub: every seat reported by name, a missing one as
# `NO CREDENTIAL`, and — the load-bearing one — never a token byte on stdout or
# stderr. Nothing real is read: HOME points into the scratch jail.
#
#   bash infra/fence/lanes/tests/creds_test.sh
#   LANE_SUT_DIR=/tmp/mutated-lanes bash …/creds_test.sh   # red-first
set -uo pipefail

SUT_DIR="${LANE_SUT_DIR:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)}"
SUT="$SUT_DIR/creds.sh"
T="$(mktemp -d "${TMPDIR:-/tmp}/lane-creds-test.XXXXXX")"
trap 'rm -rf -- "$T"' EXIT

PASS=0 FAIL=0
ok() { printf 'PASS  %s\n' "$1"; PASS=$((PASS + 1)); }
bad() { printf 'FAIL  %s\n' "$1" >&2; shift; [ $# -gt 0 ] && printf '      %s\n' "$@" >&2; FAIL=$((FAIL + 1)); }
[ -f "$SUT" ] || { echo "creds_test: no creds.sh at $SUT" >&2; exit 2; }

TOKEN='sk-ant-oat01-LANE-FIXTURE-TOKEN-NEVER-PRINT'
# The literal ~ IS the expected value: creds.sh maps every seat onto the
# CONTAINER's home, which expands it there, not here.
# shellcheck disable=SC2088
CONTAINER_SEAT_2='~/.cc/2'
FAKE_HOME="$T/home"
mkdir -p "$FAKE_HOME/.cc/1" "$FAKE_HOME/.cc/2" "$FAKE_HOME/.codex" "$FAKE_HOME/.local/share/opencode"
printf '{"claudeAiOauth":{"accessToken":"%s","refreshToken":"%s"}}\n' "$TOKEN" "$TOKEN" >"$FAKE_HOME/.cc/1/.credentials.json"
# seat 2 deliberately has NO credential file; seat 3 has a logged-out one.
mkdir -p "$FAKE_HOME/.cc/3"
printf '{"claudeAiOauth":{"accessToken":""}}\n' >"$FAKE_HOME/.cc/3/.credentials.json"
printf '{"tokens":{"access_token":"%s"}}\n' "$TOKEN" >"$FAKE_HOME/.codex/auth.json"
printf '{"openai":{"type":"oauth","access":"%s"}}\n' "$TOKEN" >"$FAKE_HOME/.local/share/opencode/auth.json"

CONFIG="$T/pfm.config.json"
cat >"$CONFIG" <<JSON
{"version": 2,
 "accounts": [
   {"id": 1, "configDir": "$FAKE_HOME/.cc/1", "emoji": "🥇"},
   {"id": 2, "configDir": "$FAKE_HOME/.cc/2", "emoji": "🥈"},
   {"id": 3, "configDir": "$FAKE_HOME/.cc/3", "emoji": "🥉"}
 ],
 "codex": {"homes": [{"id": 1, "home": "$FAKE_HOME/.codex", "emoji": "🥇"}]}}
JSON

# docker stub: `inspect` succeeds, `exec -i` consumes stdin into the jail and
# prints the byte count the real one prints. It never echoes the body.
BIN="$T/bin"
mkdir -p "$BIN" "$T/staged"
cat >"$BIN/docker" <<'STUB'
#!/usr/bin/env bash
case "$1" in
  inspect) exit 0 ;;
  exec)
    body="$(cat)"
    printf '%s' "$body" >"$STUB_STAGE/$(date +%s%N).blob"
    printf '%s\n' "${#body}"
    ;;
  *) exit 0 ;;
esac
STUB
chmod +x "$BIN/docker"
export PATH="$BIN:$PATH" STUB_STAGE="$T/staged"

run_sut() {
  OUT="$(HOME="$FAKE_HOME" OPENCODE_AUTH="$FAKE_HOME/.local/share/opencode/auth.json" \
    bash "$SUT" "$@" 2>&1)"
  RC=$?
}

# ---- 1: --print-config maps every seat onto the container's ~/.cc/<id> -----

run_sut --print-config --config "$CONFIG"
if [ "$RC" -eq 0 ] &&
  [ "$(printf '%s' "$OUT" | jq -r '.accounts | length')" = 3 ] &&
  [ "$(printf '%s' "$OUT" | jq -r '.accounts[1].configDir')" = "$CONTAINER_SEAT_2" ] &&
  [ "$(printf '%s' "$OUT" | jq -r '.mcp.servers.chat.enabled')" = true ]; then
  ok "--print-config: the host roster re-homed on ~/.cc/<id>, both MCP servers enabled"
else
  bad "print-config" "rc=$RC" "$OUT"
fi

# ---- 2: --accounts narrows the roster -------------------------------------

run_sut --print-config --config "$CONFIG" --accounts 1
if [ "$RC" -eq 0 ] && [ "$(printf '%s' "$OUT" | jq -r '.accounts | length')" = 1 ]; then
  ok "--accounts 1 narrows the container roster to one seat"
else
  bad "accounts filter" "rc=$RC" "$OUT"
fi

# ---- 3: staging — each seat by name, and NOT ONE token byte printed -------

run_sut --container fake --config "$CONFIG"
if [ "$RC" -eq 0 ] &&
  printf '%s' "$OUT" | grep -q 'seat 1 (🥇) staged' &&
  printf '%s' "$OUT" | grep -q 'seat 2 (🥈): NO CREDENTIAL' &&
  printf '%s' "$OUT" | grep -q 'seat 3 (🥉): NO CREDENTIAL' &&
  printf '%s' "$OUT" | grep -q 'creds: 3 staged · 2 NO CREDENTIAL · Claude seats staged: 1'; then
  ok "staging: seat 1 staged, seat 2 (absent) and seat 3 (logged out) each NO CREDENTIAL by name"
else
  bad "per-seat report" "rc=$RC" "$OUT"
fi
if printf '%s' "$OUT" | grep -qF "$TOKEN"; then
  bad "TOKEN LEAK" "the fixture token appeared in creds.sh output:" "$OUT"
else
  ok "no token on stdout or stderr (the fixture token appears nowhere in the output)"
fi
if [ "$(grep -lF "$TOKEN" "$T/staged"/*.blob 2>/dev/null | wc -l | tr -d ' ')" -ge 2 ]; then
  ok "the credential bodies did travel (2+ blobs reached the container over stdin)"
else
  bad "staging path" "no credential blob reached the docker stub: $(ls "$T/staged")"
fi

# ---- 4: not one seat staged → exit 1, named ------------------------------

mv "$FAKE_HOME/.cc/1/.credentials.json" "$T/away.json"
run_sut --container fake --config "$CONFIG"
if [ "$RC" -eq 1 ] && printf '%s' "$OUT" | grep -q 'not one Claude seat could be staged'; then
  ok "zero seats staged: exit 1 with the named reason, never an empty success"
else
  bad "zero seats" "rc=$RC" "$OUT"
fi
mv "$T/away.json" "$FAKE_HOME/.cc/1/.credentials.json"

# ---- 5: a missing Codex / OpenCode auth is named, not fatal --------------

mv "$FAKE_HOME/.codex/auth.json" "$T/codex-away.json"
mv "$FAKE_HOME/.local/share/opencode/auth.json" "$T/oc-away.json"
run_sut --container fake --config "$CONFIG"
if [ "$RC" -eq 0 ] &&
  printf '%s' "$OUT" | grep -q 'codex home: NO CREDENTIAL' &&
  printf '%s' "$OUT" | grep -q 'lane E2 cannot run' &&
  printf '%s' "$OUT" | grep -q 'opencode: NO CREDENTIAL' &&
  printf '%s' "$OUT" | grep -q 'lane E3 cannot run'; then
  ok "a missing Codex/OpenCode auth names the lane it disables and does not fail the staging"
else
  bad "engine auth absence" "rc=$RC" "$OUT"
fi
mv "$T/codex-away.json" "$FAKE_HOME/.codex/auth.json"
mv "$T/oc-away.json" "$FAKE_HOME/.local/share/opencode/auth.json"

# ---- 6: an unreadable config is exit 2 BEFORE anything is copied ---------

run_sut --container fake --config "$T/no-such-config.json"
if [ "$RC" -eq 2 ] && printf '%s' "$OUT" | grep -q 'not found'; then
  ok "a missing host config exits 2 by name before a single copy"
else
  bad "missing config" "rc=$RC" "$OUT"
fi

printf '\n%d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
