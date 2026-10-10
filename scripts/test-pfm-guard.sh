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
# F8: parent creation must not evade repository/path classification.
run_path() { jq -cn --arg f "$1" --arg s "$SID" --arg c "$HOME_REPO" \
  '{tool_input:{file_path:$f},session_id:$s,cwd:$c}' | bash "$HOOK" >"$ROOT/decision" 2>"$ROOT/error"; }
rm -f "$H_GUARD/quality_loaded.$SID" "$T_GUARD/quality_loaded.$SID"
for path in "$TARGET/.claude/new/deep/agent.md" "$TARGET/child/new/CLAUDE.md"; do
  run_path "$path"; rc=$?
  [[ $rc -eq 2 ]] && jq -e '.hookSpecificOutput.permissionDecision == "deny"' "$ROOT/decision" >/dev/null || {
    echo "FAIL F8: nonexistent-parent guarded target was not denied: $path (exit $rc)"; fail=1;
  }
done
run_path "$TARGET/ordinary/new/source.go"; rc=$?
[[ $rc -eq 0 ]] || { echo "FAIL F8: unguarded nonexistent-parent source was denied (exit $rc)"; fail=1; }
run_path "child/new/CLAUDE.md"; rc=$?
[[ $rc -eq 2 ]] || { echo "FAIL F8: relative guarded target was not denied (exit $rc)"; fail=1; }
# Outside any repository (~/.claude plans, memory, settings) the guard has no say.
run_path "$ROOT/unregistered/.claude/plans/new.md"; rc=$?
[[ $rc -eq 0 ]] || { echo "FAIL F8: a .claude path outside any repository was denied (exit $rc)"; fail=1; }

# F8: a symlink alias of a guarded file is the guarded file, hop by hop; an alias of an
# unguarded file stays unguarded; a guarded location stays guarded whatever its link points at.
mkdir -p "$TARGET/.claude/agents" "$TARGET/ordinary" || exit 2
printf 'protected\n' > "$TARGET/.claude/agents/worker.md"; printf 'plain\n' > "$TARGET/ordinary/source.go"
ln -s "$TARGET/.claude/agents/worker.md" "$TARGET/notes.txt"
ln -s notes.txt "$TARGET/second-hop.txt"
ln -s .claude/agents/not-yet.md "$TARGET/dangling-alias.txt"
ln -s ordinary/source.go "$TARGET/plain-alias.txt"
mkdir -p "$ROOT/unregistered" && ln -s "$ROOT/unregistered/elsewhere.md" "$TARGET/.claude/outbound.md" || exit 2
for path in "$TARGET/notes.txt" "$TARGET/second-hop.txt" "$TARGET/dangling-alias.txt" "$TARGET/.claude/outbound.md"; do
  run_path "$path"; rc=$?
  [[ $rc -eq 2 ]] && jq -e '.hookSpecificOutput.permissionDecision == "deny"' "$ROOT/decision" >/dev/null || {
    echo "FAIL F8: a symlink path reaching a guarded location was not denied: $path (exit $rc)"; fail=1;
  }
done
run_path "$TARGET/plain-alias.txt"; rc=$?
[[ $rc -eq 0 ]] || { echo "FAIL F8: an alias of an unguarded file was denied (exit $rc)"; fail=1; }
# F8: on a case-insensitive filesystem (APFS's default) another spelling of a guarded name
# opens the guarded file itself, so the spelling never decides.
for path in "$TARGET/claude.md" "$TARGET/.Claude/settings.json" "$TARGET/child/Claude.md"; do
  run_path "$path"; rc=$?
  [[ $rc -eq 2 ]] && jq -e '.hookSpecificOutput.permissionDecision == "deny"' "$ROOT/decision" >/dev/null || {
    echo "FAIL F8: a differently-cased guarded path was not denied: $path (exit $rc)"; fail=1;
  }
done
ln -s loop-b "$TARGET/loop-a"; ln -s loop-a "$TARGET/loop-b"
run_path "$TARGET/loop-a"; rc=$?
[[ $rc -eq 2 ]] || { echo "FAIL F8: a symlink loop was not denied (exit $rc)"; fail=1; }

# F8: a Git error inside a checkout leaves its root unknown, never "outside any repository":
# a guarded path is denied, an unguarded one is not guarded, a path under no checkout is the
# outside-any-repository case above.
STUB="$ROOT/stub-bin"; mkdir -p "$STUB" || exit 2
printf '#!/usr/bin/env bash\necho "synthetic metadata discovery error" >&2\nexit 128\n' > "$STUB/git" && chmod +x "$STUB/git" || exit 2
for path in "$TARGET/.claude/agents/worker.md" "$TARGET/child/new/CLAUDE.md" "$TARGET/.claude/new/deep/agent.md"; do
  PATH="$STUB:$PATH" run_path "$path"; rc=$?
  [[ $rc -eq 2 ]] && jq -e '.hookSpecificOutput.permissionDecision == "deny"' "$ROOT/decision" >/dev/null || {
    echo "FAIL F8: a Git discovery error let a guarded target through: $path (exit $rc)"; fail=1;
  }
done
PATH="$STUB:$PATH" run_path "$TARGET/ordinary/source.go"; rc=$?
[[ $rc -eq 0 ]] || { echo "FAIL F8: a Git discovery error denied an unguarded source file (exit $rc)"; fail=1; }
PATH="$STUB:$PATH" run_path "$ROOT/unregistered/.claude/plans/new.md"; rc=$?
[[ $rc -eq 0 ]] || { echo "FAIL F8: a .claude path outside any repository was denied when Git errors (exit $rc)"; fail=1; }

# F15 is this framework clone's policy, not an adopter's generic templates folder.
run_path "$TARGET/templates/new/prompt.md"; rc=$?
case "$HOOK" in */.claude/scripts/pfm-guard.sh) local_default=1 ;; *) local_default=0 ;; esac
if [[ "${EXPECT_LOCAL_TEMPLATES:-$local_default}" == 1 ]]; then expected=2; else expected=0; fi
[[ $rc -eq $expected ]] || { echo "FAIL F15: local/adopter templates split expected $expected, got $rc"; fail=1; }

[[ $fail -eq 0 ]] && echo "PASS pfm-guard: cross-repo law stamp opens the gate; an unread law keeps it shut"
exit $fail
