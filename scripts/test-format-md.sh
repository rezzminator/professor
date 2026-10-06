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

R="$(cd "$T" && pwd -P)/repo"
mkdir -p "$R/docs/dev" "$R/src" "$T/bin"
git -C "$R" init -q
printf '# T\n' > "$R/docs/dev/a.md"
printf '# T\n' > "$R/src/b.md"
# The stub answers `fmt` with STUB_FMT_RC and `check` with STUB_CHECK_RC and STUB_CHECK_OUT.
cat > "$T/bin/rumdl" <<'STUB'
#!/usr/bin/env bash
case "$1" in
  fmt) exit "${STUB_FMT_RC:-0}" ;;
  check) printf '%s' "${STUB_CHECK_OUT:-}"; exit "${STUB_CHECK_RC:-0}" ;;
esac
exit 0
STUB
chmod +x "$T/bin/rumdl"
export PATH="$T/bin:$PATH"

hook() { # hook <file>: RC and ERR of one hook run
  ERR=$(printf '{"tool_input":{"file_path":"%s"}}' "$1" | bash "$HOOK" 2>&1 >/dev/null)
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

# no rumdl on the hook's PATH: one stderr line naming the file and the fix, exit 0 — never a silent skip
mkdir -p "$T/nobin"
for c in bash jq git cat dirname; do ln -s "$(command -v "$c")" "$T/nobin/$c"; done
PATH="$T/nobin" hook "$R/docs/dev/a.md"
want='format-md: rumdl not found — docs/dev/a.md left unformatted (`pfm install` provisions it)'
if [[ $RC == 0 && $ERR == "$want" ]]; then ok "a missing rumdl exits 0 with the one stderr line naming the file"
else bad "a missing rumdl" "rc $RC (want 0)" "stderr: ${ERR:-<empty>}"; fi

shtest_end
