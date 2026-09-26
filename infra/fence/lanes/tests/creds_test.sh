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
SHTEST_TAG=lane-creds-test
# shellcheck source=/dev/null
source "$(dirname -- "${BASH_SOURCE[0]}")/../../../../scripts/shtest.sh"
[ -f "$SUT" ] || { echo "creds_test: no creds.sh at $SUT" >&2; exit 2; }

TOKEN='sk-ant-oat01-LANE-FIXTURE-TOKEN-NEVER-PRINT'
# The literal ~ IS the expected value: creds.sh maps every seat onto the
# CONTAINER's home, which expands it there, not here.
# shellcheck disable=SC2088
CONTAINER_SEAT_2='~/.cc/2'
# shellcheck disable=SC2088 # same rule for seat 1: the ~ belongs to the container
CONTAINER_SEAT_1='~/.cc/1'
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
# prints the byte count the real one prints — via `wc -c` on a FILE, not a
# `$(cat)` capture, which would strip stdin's trailing newline and undercount
# by exactly the bytes put()'s own size-vs-source comparison must catch. It
# never echoes the body. `DOCKER_TRUNCATE` (when set) drops the last byte
# BEFORE the size is read back, simulating a write that failed mid-copy —
# put() must then read a short count and fail the seat, never stage it.
BIN="$T/bin"
mkdir -p "$BIN" "$T/staged"
cat >"$BIN/docker" <<'STUB'
#!/usr/bin/env bash
case "$1" in
  inspect) exit 0 ;;
  exec)
    blob="$STUB_STAGE/$(date +%s%N).$$.blob"
    cat >"$blob"
    if [ -n "${DOCKER_TRUNCATE:-}" ]; then
      head -c -1 "$blob" >"$blob.trunc" 2>/dev/null && mv "$blob.trunc" "$blob"
    fi
    wc -c <"$blob" | tr -d ' '
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

# ---- 1: --print-config lists ONLY the seats that hold a credential --------
# Seat 2 has no credential file and seat 3 is logged out, so a container built
# from this roster must never offer them: a lane that reads two seats out of the
# config and can only drive one reports the difference as a product failure.

run_sut --print-config --config "$CONFIG"
roster="$(printf '%s' "$OUT" | grep '^{')"
if [ "$RC" -eq 0 ] &&
  [ "$(printf '%s' "$roster" | jq -r '.accounts | length')" = 1 ] &&
  [ "$(printf '%s' "$roster" | jq -r '.accounts[0].id')" = 1 ] &&
  [ "$(printf '%s' "$roster" | jq -r '.accounts[0].configDir')" = "$CONTAINER_SEAT_1" ] &&
  [ "$(printf '%s' "$roster" | jq -r '.mcp.servers.chat.enabled')" = true ] &&
  printf '%s' "$OUT" | grep -q 'seat 2 (🥈): NO CREDENTIAL' &&
  printf '%s' "$OUT" | grep -q 'seat 3 (🥉): NO CREDENTIAL' &&
  printf '%s' "$OUT" | grep -q 'dropped from the container roster'; then
  ok "--print-config: only credentialed seats re-homed on ~/.cc/<id>; each dropped seat NAMED"
else
  bad "print-config" "rc=$RC" "$OUT"
fi

# ---- 1b: a seat WITH a credential keeps its container path ----------------

printf '{"claudeAiOauth":{"accessToken":"%s"}}\n' "$TOKEN" >"$FAKE_HOME/.cc/2/.credentials.json"
run_sut --print-config --config "$CONFIG"
roster="$(printf '%s' "$OUT" | grep '^{')"
if [ "$RC" -eq 0 ] &&
  [ "$(printf '%s' "$roster" | jq -r '.accounts | length')" = 2 ] &&
  [ "$(printf '%s' "$roster" | jq -r '.accounts[1].configDir')" = "$CONTAINER_SEAT_2" ]; then
  ok "--print-config: a seat that IS logged in stays in the roster on ~/.cc/<id>"
else
  bad "print-config credentialed seat" "rc=$RC" "$OUT"
fi
rm -f "$FAKE_HOME/.cc/2/.credentials.json"

# ---- 2: --accounts narrows the roster -------------------------------------

run_sut --print-config --config "$CONFIG" --accounts 1
if [ "$RC" -eq 0 ] && [ "$(printf '%s' "$OUT" | grep '^{' | jq -r '.accounts | length')" = 1 ]; then
  ok "--accounts 1 narrows the container roster to one seat"
else
  bad "accounts filter" "rc=$RC" "$OUT"
fi

# ---- 2b: no requested seat holds a credential → exit 1, named -------------

run_sut --print-config --config "$CONFIG" --accounts 2,3
if [ "$RC" -eq 1 ] && printf '%s' "$OUT" | grep -q 'not one requested Claude seat holds a credential'; then
  ok "--print-config over seats that are all logged out exits 1 by name, never an empty roster"
else
  bad "print-config empty roster" "rc=$RC" "$OUT"
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

# ---- 3b: a write that lands short (quota, revoked perm) is NOT staged -----
# DOCKER_TRUNCATE makes the stub's read-back size one byte short of what was
# sent — put() must catch the mismatch and refuse the seat, never report it
# staged on a byte count that does not match the source.

run_sut_truncated() {
  OUT="$(HOME="$FAKE_HOME" OPENCODE_AUTH="$FAKE_HOME/.local/share/opencode/auth.json" DOCKER_TRUNCATE=1 \
    bash "$SUT" "$@" 2>&1)"
  RC=$?
}
run_sut_truncated --container fake --config "$CONFIG" --accounts 1
if printf '%s' "$OUT" | grep -q 'seat 1 (🥇): NO CREDENTIAL' &&
  printf '%s' "$OUT" | grep -q 'the copy into fake failed' &&
  ! printf '%s' "$OUT" | grep -q 'seat 1 (🥇) staged'; then
  ok "staging: a short write (truncated read-back size) is refused, never reported staged"
else
  bad "truncated write" "rc=$RC" "$OUT"
fi

# ---- 4: not one seat staged → exit 1, named ------------------------------

mv "$FAKE_HOME/.cc/1/.credentials.json" "$T/away.json"
run_sut --container fake --config "$CONFIG"
if [ "$RC" -eq 1 ] && printf '%s' "$OUT" | grep -q 'not one requested Claude seat holds a credential'; then
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

shtest_end
