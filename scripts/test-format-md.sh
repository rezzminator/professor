#!/usr/bin/env bash
# Regression for the format-md.sh PostToolUse hook: what rumdl cannot fix reaches
# the agent as exit 2 plus `UNFIXED <file>:<line>:<col> <rule> <message>` lines on
# stderr; a clean file exits 0 silent; a failed rumdl step exits 2 naming it; a
# file outside the owned paths is left alone. rumdl is a stub replaying its real
# `--output-format concise` shape, so the suite runs where rumdl is not installed.
#   bash scripts/test-format-md.sh [path/to/format-md.sh]
#
# BROKEN STATE: a missing hook or a missing jq exits 2 before any case.
set -uo pipefail
HOOK=${1:-"$(cd "$(dirname "$0")/.." && pwd)/.claude/scripts/format-md.sh"}
[[ -f "$HOOK" ]] || { printf 'test-format-md: hook not found: %s\n' "$HOOK" >&2; exit 2; }
command -v jq >/dev/null 2>&1 || { echo "test-format-md: jq missing — the hook cannot read its input" >&2; exit 2; }
SHTEST_TAG=format-md
# shellcheck source=scripts/shtest.sh
source "$(dirname "$0")/shtest.sh"
shtest_isolate_host

R="$(cd "$T" && pwd -P)/repo"
mkdir -p "$R/docs/dev" "$R/src" "$T/bin"
git -C "$R" init -q
printf '# T\n' > "$R/docs/dev/a.md"
printf '# T\n' > "$R/src/b.md"
# The stdin check replays the committed version; other checks replay the written file.
cat > "$T/bin/rumdl" <<'STUB'
#!/usr/bin/env bash
case "$1" in
  fmt) exit "${STUB_FMT_RC:-0}" ;;
  check)
    for arg in "$@"; do
      if [[ $arg == --stdin ]]; then
        cat >/dev/null
        printf '%s' "${STUB_COMMITTED_CHECK_OUT:-}"
        exit "${STUB_COMMITTED_CHECK_RC:-0}"
      fi
    done
    printf '%s' "${STUB_CHECK_OUT:-}"; exit "${STUB_CHECK_RC:-0}" ;;
esac
exit 0
STUB
chmod +x "$T/bin/rumdl"
export PATH="$T/bin:$PATH"

hook() { # hook <file>: RC and ERR of one hook run
  ERR=$(printf '{"session_id":"%s","tool_input":{"file_path":"%s"}}' "${HOOK_SESSION:-}" "$1" | bash "$HOOK" 2>&1 >/dev/null)
  RC=$?
}

STUB_CHECK_RC=0 hook "$R/docs/dev/a.md"
if [[ $RC == 0 && -z $ERR ]]; then ok "a clean file exits 0 with nothing on stderr"
else bad "a clean file" "rc $RC (want 0)" "stderr: ${ERR:-<empty>}"; fi

STUB_CHECK_RC=1 STUB_CHECK_OUT=$'docs/dev/a.md:8:1: [MD051] Link anchor \'#missing\' does not exist in document headings\ndocs/dev/a.md:12:3: [MD018] No space after hash on atx style heading\n\nIssues: Found 2 issues in 1 file (5ms)\n' hook "$R/docs/dev/a.md"
want=$'format-md: docs/dev/a.md formatted; 2 issue(s) rumdl cannot fix, left for you:\n  UNFIXED docs/dev/a.md:8:1 MD051 Link anchor \'#missing\' does not exist in document headings\n  UNFIXED docs/dev/a.md:12:3 MD018 No space after hash on atx style heading\nformat-md: fix them in docs/dev/a.md; re-check from the repo root: rumdl check docs/dev/a.md'
if [[ $RC == 2 && $ERR == "$want" ]]; then ok "unfixed issues exit 2 with one UNFIXED line each, the summary line dropped"
else bad "unfixed issues" "rc $RC (want 2)" "stderr:" "$ERR"; fi

STUB_FMT_RC=3 hook "$R/docs/dev/a.md"
if [[ $RC == 2 && $ERR == "format-md: FAILED rumdl fmt on docs/dev/a.md"* ]]; then ok "a failed rumdl fmt exits 2 naming the step"
else bad "a failed rumdl fmt" "rc $RC (want 2)" "stderr: ${ERR:-<empty>}"; fi

STUB_CHECK_RC=2 STUB_CHECK_OUT='config error' hook "$R/docs/dev/a.md"
if [[ $RC == 2 && $ERR == "format-md: FAILED rumdl check on docs/dev/a.md (exit 2)"*"config error"* ]]; then ok "a failed rumdl check exits 2 naming the step and rumdl's words"
else bad "a failed rumdl check" "rc $RC (want 2)" "stderr: ${ERR:-<empty>}"; fi

STUB_CHECK_RC=1 STUB_CHECK_OUT='something rumdl never prints' hook "$R/docs/dev/a.md"
if [[ $RC == 2 && $ERR == *"in a shape this hook cannot read"* ]]; then ok "an issue report the hook cannot parse exits 2, never a silent pass"
else bad "an unreadable issue report" "rc $RC (want 2)" "stderr: ${ERR:-<empty>}"; fi

STUB_CHECK_RC=1 STUB_CHECK_OUT='src/b.md:1:1: [MD041] x' hook "$R/src/b.md"
if [[ $RC == 0 && -z $ERR ]]; then ok "a file outside the owned paths is left alone"
else bad "a file outside the owned paths" "rc $RC (want 0)" "stderr: ${ERR:-<empty>}"; fi

# A committed baseline belongs to a second repo; the cases above have no HEAD.
C="$(cd "$T" && pwd -P)/committed"
mkdir -p "$C/docs/dev" && git -C "$C" init -q &&
  printf '# T\n' > "$C/docs/dev/a.md" && git -C "$C" add docs/dev/a.md &&
  git -C "$C" -c user.name=t -c user.email=t@t commit -qm baseline ||
  { echo 'test-format-md: committed fixture setup failed' >&2; exit 2; }
standing=$'docs/dev/a.md:8:1: [MD051] Link anchor \'#missing\' does not exist in document headings\n'
shifted=$'docs/dev/a.md:9:1: [MD051] Link anchor \'#missing\' does not exist in document headings\n'
introduced=$'docs/dev/a.md:12:3: [MD018] No space after hash on atx style heading\n'

STUB_COMMITTED_CHECK_RC=1 STUB_COMMITTED_CHECK_OUT="$standing" \
  STUB_CHECK_RC=1 STUB_CHECK_OUT="$shifted" hook "$C/docs/dev/a.md"
if [[ $RC == 0 && -z $ERR ]]; then ok "standing issues at shifted positions exit 0 with nothing on stderr"
else bad "standing issues at shifted positions" "rc $RC (want 0)" "stderr: ${ERR:-<empty>}"; fi

STUB_COMMITTED_CHECK_RC=1 STUB_COMMITTED_CHECK_OUT="$standing" \
  STUB_CHECK_RC=1 STUB_CHECK_OUT="$shifted$introduced" hook "$C/docs/dev/a.md"
want=$'format-md: docs/dev/a.md formatted; 1 issue(s) rumdl cannot fix, left for you:\n  UNFIXED docs/dev/a.md:12:3 MD018 No space after hash on atx style heading\nformat-md: fix them in docs/dev/a.md; re-check from the repo root: rumdl check docs/dev/a.md'
if [[ $RC == 2 && $ERR == "$want" ]]; then ok "only the introduced issue reaches stderr, with its post-format position"
else bad "only the introduced issue" "rc $RC (want 2)" "stderr:" "$ERR"; fi

STUB_COMMITTED_CHECK_RC=1 STUB_COMMITTED_CHECK_OUT="$standing" \
  STUB_CHECK_RC=1 STUB_CHECK_OUT="$shifted${shifted/9:1/12:3}" hook "$C/docs/dev/a.md"
repeated=$'format-md: docs/dev/a.md formatted; 1 issue(s) rumdl cannot fix, left for you:\n  UNFIXED docs/dev/a.md:12:3 MD051 Link anchor \'#missing\' does not exist in document headings\nformat-md: fix them in docs/dev/a.md; re-check from the repo root: rumdl check docs/dev/a.md'
if [[ $RC == 2 && $ERR == "$repeated" ]]; then ok "a repeated rule and message consumes only one baseline occurrence"
else bad "a repeated rule and message" "rc $RC (want 2)" "stderr:" "$ERR"; fi

STUB_COMMITTED_CHECK_RC=2 STUB_COMMITTED_CHECK_OUT='config error' \
  STUB_CHECK_RC=0 hook "$C/docs/dev/a.md"
if [[ $RC == 2 && $ERR == "format-md: FAILED rumdl check of the committed docs/dev/a.md (exit 2)"*"config error"* ]]; then ok "a failed committed-version check exits 2 naming the file and exit"
else bad "a failed committed-version check" "rc $RC (want 2)" "stderr: ${ERR:-<empty>}"; fi

ln -s "$C" "$T/link" || { echo 'test-format-md: symlink fixture setup failed' >&2; exit 2; }
STUB_COMMITTED_CHECK_RC=0 STUB_CHECK_RC=1 STUB_CHECK_OUT="$introduced" hook "$T/link/docs/dev/a.md"
if [[ $RC == 2 && $ERR == "$want" ]]; then ok "a write through a symlinked directory reports the repo-relative issue"
else bad "a write through a symlinked directory" "rc $RC (want 2)" "stderr:" "$ERR"; fi

# no rumdl on the hook's PATH: stderr names the file, exit 0 — never a silent skip
mkdir -p "$T/nobin"
for c in bash jq git cat dirname; do ln -s "$(command -v "$c")" "$T/nobin/$c"; done
PATH="$T/nobin" hook "$R/docs/dev/a.md"
if [[ $RC == 0 && -n $ERR && $ERR == *docs/dev/a.md* ]]; then ok "a missing rumdl exits 0 with stderr naming the file"
else bad "a missing rumdl" "rc $RC (want 0)" "stderr: ${ERR:-<empty>}"; fi

# The shipped hook reports standing uncommitted diagnostics once per session.
if [[ "$HOOK" == */templates/project/scripts/format-md.sh ]]; then
  for cache_repo in "$R" "$C"; do
    STUB_COMMITTED_CHECK_RC=0 STUB_CHECK_RC=1 STUB_CHECK_OUT="$introduced" TMPDIR="$T/cache" HOOK_SESSION=repeat-test hook "$cache_repo/docs/dev/a.md"
    if [[ $RC == 2 && $ERR == *UNFIXED* ]]; then ok "first uncommitted diagnostic reports for $cache_repo"
    else bad "first uncommitted diagnostic reports" "rc=$RC; err=$ERR"; fi
    STUB_COMMITTED_CHECK_RC=0 STUB_CHECK_RC=1 STUB_CHECK_OUT="${introduced/12:3/13:3}" TMPDIR="$T/cache" HOOK_SESSION=repeat-test hook "$cache_repo/docs/dev/a.md"
    if [[ $RC == 0 && -z $ERR ]]; then ok "later unrelated write suppresses standing uncommitted diagnostics for $cache_repo"
    else bad "later unrelated write suppresses standing uncommitted diagnostics" "rc=$RC; err=$ERR"; fi
    STUB_COMMITTED_CHECK_RC=0 STUB_CHECK_RC=1 STUB_CHECK_OUT="$introduced$introduced" TMPDIR="$T/cache" HOOK_SESSION=repeat-test hook "$cache_repo/docs/dev/a.md"
    if [[ $RC == 2 && $ERR == *'1 issue(s)'* ]]; then ok "a later extra occurrence is reported for $cache_repo"
    else bad "a later extra occurrence is reported" "rc=$RC; err=$ERR"; fi
  done
fi
shtest_end
