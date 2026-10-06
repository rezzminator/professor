#!/usr/bin/env bash
set -uo pipefail

# Regression for the /pcm guard across repositories. The quality law is read
# once per session and stamped in the session's own project (guard-stamp.sh);
# an edit of another repo's CLAUDE.md must find that stamp there, or the gate
# never opens: the agent reads the law, is denied anyway, and follows a
# message that cannot help it. Also held: without any quality stamp the same
# edit is denied.
#
# When THIS script is broken rather than the hook: a setup step that cannot run
# exits 2 and says so, never a pass.
HOOK=${1:?usage: test-pfm-guard.sh PATH_TO_HOOK}
[[ -f "$HOOK" ]] || { printf 'pfm-guard regression: hook not found: %s\n' "$HOOK" >&2; exit 2; }
command -v jq >/dev/null 2>&1 || { printf 'pfm-guard regression: jq is absent — the hook could not be exercised\n' >&2; exit 2; }

ROOT=$(mktemp -d "${TMPDIR:-/tmp}/pfm-guard-regression.XXXXXX") || { echo 'pfm-guard regression: mktemp failed' >&2; exit 2; }
# Repo basenames become /tmp/<project>/guard namespaces: unique per run.
HOME_REPO="$ROOT/guard-home-$$"; TARGET="$ROOT/guard-target-$$"; SID="regression-$$"
H_GUARD="/tmp/$(basename "$HOME_REPO")/guard"; T_GUARD="/tmp/$(basename "$TARGET")/guard"
trap 'rm -rf "$ROOT" "${H_GUARD%/guard}" "${T_GUARD%/guard}"' EXIT
for r in "$HOME_REPO" "$TARGET"; do
  mkdir -p "$r" && git -C "$r" init -q || { echo "pfm-guard regression: git init $r failed" >&2; exit 2; }
done
mkdir -p "$H_GUARD" "$T_GUARD" || exit 2

run() { jq -cn --arg f "$TARGET/CLAUDE.md" --arg s "$SID" --arg c "$HOME_REPO" \
  '{tool_input:{file_path:$f},session_id:$s,cwd:$c}' | bash "$HOOK" >/dev/null 2>&1; }

fail=0
date +%s > "$H_GUARD/quality_loaded.$SID"; date +%s > "$T_GUARD/pfm_active.$SID"
run; rc=$?
[[ $rc -eq 0 ]] || { echo "FAIL: law read in the session's project, /pcm open on the target — the edit was denied (exit $rc)"; fail=1; }
rm -f "$H_GUARD/quality_loaded.$SID"
run; rc=$?
[[ $rc -eq 2 ]] || { echo "FAIL: no quality stamp anywhere — the edit was not denied (exit $rc)"; fail=1; }
printf '%s\n' "$(( $(date +%s) - 1600 ))" > "$T_GUARD/quality_loaded.$SID"
date +%s > "$H_GUARD/quality_loaded.$SID"; date +%s > "$T_GUARD/pfm_active.$SID"
run; rc=$?
[[ $rc -eq 0 ]] || { echo "FAIL: stale target law stamp, fresh session law stamp — the edit was denied (exit $rc)"; fail=1; }
[[ $fail -eq 0 ]] && echo "PASS pfm-guard: cross-repo law stamp opens the gate; an unread law keeps it shut"
exit $fail
