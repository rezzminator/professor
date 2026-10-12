#!/usr/bin/env bash
# Exercise the adopter Stop/PostToolUse door .claude/scripts/codex-sync.sh (and its project-tier twin) through
# every branch, with a stub engine-sync: an unknown stage, the stage forwarded to the binary, a set ENGINE_SYNC_BIN
# that is not an executable regular file (refused by name, never replaced by the default binary), the default binary
# unavailable at Stop with and without a pending edit (engine-sync's dirty flag, read in shell at the path
# plugins/engine-sync derives: {state}/dirty/{project dir name without a leading dot}-{first 16 hex of the sha256
# of the physical project root}), and the flag path under each state-directory variable.
#   bash scripts/test-codex-sync.sh <path to the hook>
# The hook is a Stop hook: a nonzero exit shows the user an error the model never sees, so every branch exits 0
# and speaks through a JSON systemMessage or not at all.
# BROKEN STATE: a missing hook, jq or python3 exits 2 and says so, never a pass; a silent hook, a message naming a
# retained flag that does not exist, a lost flag or a nonzero exit each fail with the expectation violated.
set -uo pipefail
HOOK="${1:?usage: test-codex-sync.sh PATH_TO_HOOK}"
[[ -f "$HOOK" ]] || { printf 'codex-sync test: hook not found: %s\n' "$HOOK" >&2; exit 2; }
HOOK="$(cd -- "$(dirname -- "$HOOK")" && pwd -P)/$(basename -- "$HOOK")"
for tool in jq python3; do
  command -v "$tool" > /dev/null 2>&1 || { printf 'codex-sync test: %s is absent — the hook could not be exercised\n' "$tool" >&2; exit 2; }
done
SHTEST_TAG=codex-sync
# shellcheck source=scripts/shtest.sh
source "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)/shtest.sh"

JQ_DIR="$(dirname -- "$(command -v jq)")"
PY_DIR="$(dirname -- "$(command -v python3)")"
HOME_DIR="$T/home"
BIN_DIR="$T/bin"
CALLS="$T/calls"
mkdir -p "$HOME_DIR" "$BIN_DIR"

# The stub engine-sync records its arguments and exits with $STUB_EXIT (default 0).
cat > "$BIN_DIR/engine-sync" <<'STUB'
#!/bin/sh
printf '%s\n' "$*" >> "$CALLS"
exit "${STUB_EXIT:-0}"
STUB
chmod +x "$BIN_DIR/engine-sync"

# new_project <dir name>: a project root holding a CLAUDE.md; prints its physical path.
new_project() {
  mkdir -p "$T/projects/$1" && : > "$T/projects/$1/CLAUDE.md"
  python3 -c 'import os,sys; print(os.path.realpath(sys.argv[1]))' "$T/projects/$1"
}
# flag_path <physical root> <state dir>: engine-sync's dirty flag for the root.
flag_path() {
  python3 -c '
import hashlib, os, sys
root, state = sys.argv[1], sys.argv[2]
name = os.path.basename(root)
name = name[1:] if name.startswith(".") else name
print(os.path.join(state, "dirty", name + "-" + hashlib.sha256(root.encode()).hexdigest()[:16]))' "$1" "$2"
}
set_flag() { mkdir -p "$(dirname -- "$1")" && printf '%s\n' "$2" > "$1"; }

# run_hook <stage> <project root> [VAR=value...]: out and rc of one run in a clean environment, cwd in the project.
run_hook() {
  local stage="$1" root="$2"; shift 2
  out="$(cd "$root" && env -i HOME="$HOME_DIR" PATH="$JQ_DIR:$PY_DIR:/usr/bin:/bin" CALLS="$CALLS" CLAUDE_PROJECT_DIR="$root" "$@" \
    bash "$HOOK" "$stage" 2>&1 < /dev/null)"
  rc=$?
}
message() { jq -er '.systemMessage' <<<"$out" 2> /dev/null; }
expect() { # expect <name> <condition: true|false> [detail...]
  local name="$1" cond="$2"; shift 2
  if [[ "$cond" == true ]]; then ok "$name"; else bad "$name" "rc=$rc" "out=$out" "$@"; fi
}

PROJ="$(new_project demo)"
DEFAULT_BIN="$HOME_DIR/.local/share/engine-sync/bin/engine-sync"
FLAG="$(flag_path "$PROJ" "$HOME_DIR/.local/state/engine-sync")"

# ---- the stage argument ---------------------------------------------------------------------------------------------
: > "$CALLS"
run_hook bogus "$PROJ" ENGINE_SYNC_BIN="$BIN_DIR/engine-sync"
expect 'an unknown stage exits 0 silently and never reaches the binary' "$([[ $rc -eq 0 && -z "$out" && ! -s "$CALLS" ]] && echo true || echo false)"

# ---- the happy paths forward to the binary --------------------------------------------------------------------------
for pair in 'mark:hook claude mark' 'sync:hook claude stop'; do
  stage="${pair%%:*}"; want="${pair#*:}"
  : > "$CALLS"
  run_hook "$stage" "$PROJ" ENGINE_SYNC_BIN="$BIN_DIR/engine-sync"
  expect "$stage forwards to ENGINE_SYNC_BIN as: $want" "$([[ $rc -eq 0 && "$(cat "$CALLS")" == "$want" ]] && echo true || echo false)" "calls=$(cat "$CALLS")"
done
: > "$CALLS"
run_hook sync "$PROJ" ENGINE_SYNC_BIN="$BIN_DIR/engine-sync" STUB_EXIT=7
expect "the binary's exit code is the hook's exit code (exec)" "$([[ $rc -eq 7 ]] && echo true || echo false)"
mkdir -p "$(dirname -- "$DEFAULT_BIN")" && cp "$BIN_DIR/engine-sync" "$DEFAULT_BIN"
: > "$CALLS"
run_hook sync "$PROJ"
expect 'with no ENGINE_SYNC_BIN the default binary under HOME is run' "$([[ $rc -eq 0 && "$(cat "$CALLS")" == 'hook claude stop' ]] && echo true || echo false)" "calls=$(cat "$CALLS")"
: > "$CALLS"
set_flag "$FLAG" "$PROJ"
run_hook sync "$PROJ" ENGINE_SYNC_HOME="$T/es-home"
rm -f "$FLAG"
expect 'an absolute ENGINE_SYNC_HOME replaces the default binary location (and the default one is not run)' "$([[ $rc -eq 0 && ! -s "$CALLS" && "$out" == *"$T/es-home/bin/engine-sync: no such file"* ]] && echo true || echo false)" "calls=$(cat "$CALLS")"
rm -f "$DEFAULT_BIN"

# ---- a set ENGINE_SYNC_BIN that is not an executable regular file -----------------------------------------------------
cp "$BIN_DIR/engine-sync" "$DEFAULT_BIN"   # a working default binary the refusal must not fall back to
printf '#!/bin/sh\n' > "$T/not-exec" && chmod 644 "$T/not-exec"
mkdir -p "$T/a-directory"
for bad_bin in "$T/not-exec" "$T/a-directory" "$T/missing-bin"; do
  : > "$CALLS"
  run_hook sync "$PROJ" ENGINE_SYNC_BIN="$bad_bin"
  msg="$(message)"
  expect "Stop with ENGINE_SYNC_BIN=${bad_bin#"$T"/} (not an executable file) refuses by name, exit 0, default binary untouched" \
    "$([[ $rc -eq 0 && "$msg" == *"ENGINE_SYNC_BIN=$bad_bin is not an executable file"* && ! -s "$CALLS" ]] && echo true || echo false)" "calls=$(cat "$CALLS")"
done
: > "$CALLS"
run_hook mark "$PROJ" ENGINE_SYNC_BIN="$T/not-exec"
expect 'mark with a refused ENGINE_SYNC_BIN exits 0 silently' "$([[ $rc -eq 0 && -z "$out" && ! -s "$CALLS" ]] && echo true || echo false)"
run_hook sync "$PROJ" ENGINE_SYNC_BIN="$T/not-exec"
expect 'a refusal with no pending edit does not claim a retained flag' "$([[ "$(message)" != *'dirty flag retained'* ]] && echo true || echo false)"
set_flag "$FLAG" "$PROJ"
run_hook sync "$PROJ" ENGINE_SYNC_BIN="$T/not-exec"
expect 'a refusal with a pending edit says the dirty flag is retained, and it is' "$([[ "$(message)" == *'dirty flag retained'* && -f "$FLAG" ]] && echo true || echo false)"
rm -f "$FLAG" "$DEFAULT_BIN"

# ---- the default binary unavailable ---------------------------------------------------------------------------------
run_hook sync "$PROJ"
expect 'binary unavailable and no pending edit: Stop is silent, exit 0' "$([[ $rc -eq 0 && -z "$out" ]] && echo true || echo false)"
run_hook mark "$PROJ"
expect 'binary unavailable: mark exits 0 silently' "$([[ $rc -eq 0 && -z "$out" ]] && echo true || echo false)"
set_flag "$FLAG" "$PROJ"
run_hook sync "$PROJ"
msg="$(message)"
expect 'binary unavailable with a pending edit: exit 0, a systemMessage naming the missing binary and the retained flag' \
  "$([[ $rc -eq 0 && "$msg" == *'binary unavailable'* && "$msg" == *"$DEFAULT_BIN: no such file"* && "$msg" == *'dirty flag retained'* ]] && echo true || echo false)"
expect 'binary unavailable: the pending flag is left in place' "$([[ -f "$FLAG" ]] && echo true || echo false)"
mkdir -p "$(dirname -- "$DEFAULT_BIN")" && printf '#!/bin/sh\n' > "$DEFAULT_BIN" && chmod 644 "$DEFAULT_BIN"
run_hook sync "$PROJ"
expect 'a default binary that exists but is not executable is named as such' "$([[ "$(message)" == *"$DEFAULT_BIN: not executable"* ]] && echo true || echo false)"
rm -f "$DEFAULT_BIN" "$FLAG"

# ---- where the flag is looked for -----------------------------------------------------------------------------------
for variant in "ENGINE_SYNC_STATE|ENGINE_SYNC_STATE=$T/s1|$T/s1" "XDG_STATE_HOME|XDG_STATE_HOME=$T/s2|$T/s2/engine-sync"; do
  IFS='|' read -r label assign state <<<"$variant"
  vflag="$(flag_path "$PROJ" "$state")"
  run_hook sync "$PROJ" "$assign"
  silent="$([[ -z "$out" ]] && echo true || echo false)"
  set_flag "$vflag" "$PROJ"
  run_hook sync "$PROJ" "$assign"
  expect "$label names the state directory: silent without its flag, a warning with it" "$([[ "$silent" == true && "$(message)" == *'dirty flag retained'* ]] && echo true || echo false)"
  rm -f "$vflag"
done
set_flag "$FLAG" "$PROJ"
run_hook sync "$PROJ" ENGINE_SYNC_STATE=relative/state
expect 'a relative ENGINE_SYNC_STATE is ignored, as engine-sync ignores it' "$([[ "$(message)" == *'dirty flag retained'* ]] && echo true || echo false)"
rm -f "$FLAG"

# ---- which project the flag belongs to ------------------------------------------------------------------------------
mkdir -p "$T/elsewhere/demo" && : > "$T/elsewhere/demo/CLAUDE.md"
SAME_NAME="$(python3 -c 'import os,sys; print(os.path.realpath(sys.argv[1]))' "$T/elsewhere/demo")"
set_flag "$(flag_path "$SAME_NAME" "$HOME_DIR/.local/state/engine-sync")" "$SAME_NAME"
run_hook sync "$PROJ"
expect 'another project of the same directory name does not trigger this project warning' "$([[ $rc -eq 0 && -z "$out" ]] && echo true || echo false)"
DOT="$(new_project .dotted)"
set_flag "$(flag_path "$DOT" "$HOME_DIR/.local/state/engine-sync")" "$DOT"
run_hook sync "$DOT"
expect 'a project whose directory name starts with a dot has its flag found under the name without the dot' "$([[ "$(message)" == *'dirty flag retained'* ]] && echo true || echo false)"
ln -s "$PROJ" "$T/link-to-demo"
set_flag "$FLAG" "$PROJ"
run_hook sync "$T/link-to-demo"
expect 'the flag is found through a symlinked project directory (physical root)' "$([[ "$(message)" == *'dirty flag retained'* ]] && echo true || echo false)"
sub="$PROJ/sub/dir"; mkdir -p "$sub"
out="$(cd "$sub" && env -i HOME="$HOME_DIR" PATH="$JQ_DIR:$PY_DIR:/usr/bin:/bin" CALLS="$CALLS" bash "$HOOK" sync 2>&1 < /dev/null)"; rc=$?
expect 'without CLAUDE_PROJECT_DIR the project is the nearest ancestor holding CLAUDE.md' "$([[ "$(message)" == *'dirty flag retained'* ]] && echo true || echo false)"
rm -f "$FLAG"

# ---- cannot tell ----------------------------------------------------------------------------------------------------
mkdir -p "$T/nopath-bin" && ln -s "$(command -v jq)" "$T/nopath-bin/jq"
out="$(cd "$PROJ" && env -i HOME="$HOME_DIR" PATH="$T/nopath-bin" CALLS="$CALLS" CLAUDE_PROJECT_DIR="$PROJ" /bin/bash "$HOOK" sync 2>&1 < /dev/null)"; rc=$?
expect 'when the flag cannot be looked up the warning says so instead of claiming a retained flag' \
  "$([[ $rc -eq 0 && "$(message)" == *'binary unavailable'* && "$(message)" == *'could not be determined'* && "$(message)" != *'dirty flag retained'* ]] && echo true || echo false)"

# ---- a failed flag lookup is not a missing flag ---------------------------------------------------------------------
# Only ENOENT and ENOTDIR mean no edit is pending. A lookup that fails otherwise (a permission error cannot be
# staged as root, so a symlink loop stands for it) or finds something that is not a regular file cannot tell.
rm -f "$DEFAULT_BIN"
STATE_DIR="$HOME_DIR/.local/state/engine-sync"
rm -rf "$STATE_DIR/dirty"
mkdir -p "$STATE_DIR" && ln -s dirty "$STATE_DIR/dirty"
run_hook sync "$PROJ"
expect 'a flag lookup that fails (symlink loop on the dirty directory) says whether an edit is pending could not be determined' \
  "$([[ $rc -eq 0 && "$(message)" == *'binary unavailable'* && "$(message)" == *'whether an edit is pending could not be determined'* && "$(message)" == *"$DEFAULT_BIN: no such file"* && "$(message)" != *'dirty flag retained'* ]] && echo true || echo false)"
run_hook sync "$PROJ" ENGINE_SYNC_BIN="$T/not-exec"
expect 'an invalid ENGINE_SYNC_BIN still warns by name when the flag lookup fails, claiming no retained flag' \
  "$([[ $rc -eq 0 && "$(message)" == *"ENGINE_SYNC_BIN=$T/not-exec is not an executable file"* && "$(message)" != *'dirty flag retained'* ]] && echo true || echo false)"
rm -f "$STATE_DIR/dirty"
mkdir -p "$FLAG"
run_hook sync "$PROJ"
expect 'a flag path that is not a regular file cannot be read as pending or absent: the warning says it could not be determined' \
  "$([[ $rc -eq 0 && "$(message)" == *'whether an edit is pending could not be determined'* && "$(message)" != *'dirty flag retained'* ]] && echo true || echo false)"
rm -rf "$STATE_DIR/dirty"
printf 'x' > "$STATE_DIR/dirty"
run_hook sync "$PROJ"
expect 'a state directory whose dirty path is a regular file holds no flag: silent' "$([[ $rc -eq 0 && -z "$out" ]] && echo true || echo false)"
rm -f "$STATE_DIR/dirty"

shtest_end
