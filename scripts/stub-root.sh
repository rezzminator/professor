#!/usr/bin/env bash
# The stub fake root scripts/test-test-pfm.sh and scripts/test-check-pfm.sh share: sourced after
# scripts/shtest.sh with NAME set. Builds $R (a fake repository named $NAME, so its logs land under
# /tmp/$NAME, removed at exit by shtest_clean_also), $BIN (tool stubs that record argv to $REC and exit as $RULES says),
# and a stub .claude/scripts/dev.sh whose `iso run` executes its command string with PFM_DEV_FENCE=1.
# A `go test -v` call no rule matched prints one `--- PASS` line, as a real run of a test does; a
# matching rule prints only its own output. The cleanup is shtest_clean_also, which keeps shtest.sh's PID
# guard: a command forked from the suite and TERMed before its exec never deletes the suite's scratch.
# BROKEN STATE: an unset NAME, T or SHTEST_PID stops the sourcing suite under set -u before any assertion.
R="$T/$NAME"
BIN="$T/bin"
REC="$T/rec"
RULES="$T/rules"
STUBLIB="$T/stublib.sh"
shtest_clean_also "/tmp/$NAME"
mkdir -p "$BIN" "$R/.claude/scripts"
: >"$REC"; : >"$REC.env"; : >"$RULES"

# --- stubs: every tool the SUT calls records its argv to $REC and exits as $RULES tells it ---------
cat >"$STUBLIB" <<'EOF'
stub_record() { local n="$1"; shift; { printf '%s' "$n"; printf ' %q' "$@"; printf '\n'; } >>"$REC"; }
stub_rule() { # sets RULE_RC and RULE_MATCHED; prints the matching rule's output
  local name="$1" joined t sub rc out; shift; joined="$*"
  RULE_RC=0; RULE_MATCHED=0
  [ -f "$RULES" ] || return 0
  while IFS=$'\001' read -r t sub rc out; do
    [ "$t" = "$name" ] || continue
    case "$joined" in *"$sub"*)
      RULE_RC="$rc"; RULE_MATCHED=1; [ -z "$out" ] || printf '%b\n' "$out"; return 0 ;;
    esac
  done <"$RULES"
}
EOF
{ printf '#!%s\n' "$BASH"; cat <<'EOF'; } >"$BIN/.stub"
source "$STUBLIB"
n="${0##*/}"
stub_record "$n" "$@"
[ "$n" != go ] || printf 'GOFLAGS=%s GOPROXY=%s %s\n' "${GOFLAGS-}" "${GOPROXY-}" "$*" >>"$REC.env"
stub_rule "$n" "$@"
if [ "$n" = go ] && [ "$RULE_MATCHED" = 0 ]; then
  case " $* " in *' test -v '*) echo '--- PASS: TestStub (0.00s)' ;; esac
fi
exit "$RULE_RC"
EOF
{ printf '#!%s\n' "$BASH"; cat <<'EOF'; } >"$R/.claude/scripts/dev.sh"
source "$STUBLIB"
stub_record dev.sh "$@"
stub_rule dev.sh "$@"
if [ "$RULE_MATCHED" = 1 ]; then exit "$RULE_RC"; fi
if [ "${1:-} ${2:-}" = "iso run" ]; then
  cd "$(dirname "$0")/../.." && PFM_DEV_FENCE=1 exec bash -c "${*:3}"
fi
echo "stub dev.sh: $*"
EOF
chmod +x "$BIN/.stub" "$R/.claude/scripts/dev.sh"
