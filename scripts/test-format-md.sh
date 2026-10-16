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
readiness_only=0
if [[ ${1:-} == --readiness-only ]]; then readiness_only=1; shift; fi
HOOK=${1:-"$(cd "$(dirname "$0")/.." && pwd)/.claude/scripts/format-md.sh"}
[[ -f "$HOOK" ]] || { printf 'test-format-md: hook not found: %s\n' "$HOOK" >&2; exit 2; }
command -v jq >/dev/null 2>&1 || { echo "test-format-md: jq missing — the hook cannot read its input" >&2; exit 2; }
SHTEST_TAG=format-md
# shellcheck source=scripts/shtest.sh
source "$(dirname "$0")/shtest.sh"
shtest_isolate_host
unset CLAUDE_PROJECT_DIR

R="$(cd "$T" && pwd -P)/repo"
mkdir -p "$R/docs/dev" "$R/src" "$T/bin"
git -C "$R" init -q
# A repo-root .rumdl.toml marks a project the hook formats; the stub rumdl never reads it.
printf '[global]\n' > "$R/.rumdl.toml"
printf '# T\n' > "$R/docs/dev/a.md"
printf '# T\n' > "$R/src/b.md"
# The stdin check replays the committed version; other checks replay the written file.
cat > "$T/bin/rumdl" <<'STUB'
#!/usr/bin/env bash
case "$1" in
  fmt)
    if [[ -n ${STUB_FMT_GATE:-} ]]; then
      printf 'ready\n' > "$STUB_FMT_GATE/ready"
      while [[ ! -f "$STUB_FMT_GATE/release" ]]; do sleep 0.1; done
    fi
    exit "${STUB_FMT_RC:-0}" ;;
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

if [[ $readiness_only == 0 ]]; then
# A repository without .rumdl.toml is not a project the hook formats: a nested one
# (a plugin submodule) is left alone, silent; the session's own one says so and exits 0.
N="$R/plugins/p"
mkdir -p "$N" && git -C "$N" init -q && printf '# T\n' > "$N/CLAUDE.md" ||
  { echo 'test-format-md: nested fixture setup failed' >&2; exit 2; }
long=$'CLAUDE.md:1:81: [MD013] Line length 120 exceeds 80 characters\n'
STUB_CHECK_RC=1 STUB_CHECK_OUT="$long" CLAUDE_PROJECT_DIR="$R" hook "$N/CLAUDE.md"
if [[ $RC == 0 && -z $ERR ]]; then ok "a nested repository without .rumdl.toml is left alone, silent"
else bad "a nested repository without .rumdl.toml" "rc $RC (want 0)" "stderr: ${ERR:-<empty>}"; fi
Q="$R/plugins/q"
mkdir -p "$Q" && git -C "$Q" init -q && printf '[global]\n' > "$Q/.rumdl.toml" && printf '# T\n' > "$Q/CLAUDE.md" ||
  { echo 'test-format-md: nested-policy fixture setup failed' >&2; exit 2; }
STUB_CHECK_RC=1 STUB_CHECK_OUT='CLAUDE.md:1:1: [MD041] x' CLAUDE_PROJECT_DIR="$R" hook "$Q/CLAUDE.md"
if [[ $RC == 2 && $ERR == *"UNFIXED CLAUDE.md:1:1 MD041"* ]]; then ok "a nested repository with its own .rumdl.toml is formatted, paths relative to its root"
else bad "a nested repository with its own .rumdl.toml" "rc $RC (want 2)" "stderr: ${ERR:-<empty>}"; fi
P="$(cd "$T" && pwd -P)/nopolicy"
mkdir -p "$P" && git -C "$P" init -q && printf '# T\n' > "$P/CLAUDE.md" && ln -s "$P" "$T/plink" ||
  { echo 'test-format-md: no-policy fixture setup failed' >&2; exit 2; }
# The session's own project is told under any spelling of CLAUDE_PROJECT_DIR; an unresolvable one is told too.
told="format-md: no .rumdl.toml at $P — CLAUDE.md left unformatted"
for row in "$P|the project's own repository" "$T/plink|a symlinked spelling of the project" \
  "$T|a project directory outside any repository" "-|an unset project directory"; do
  pd=${row%%|*}
  if [[ $pd == - ]]; then STUB_CHECK_RC=1 STUB_CHECK_OUT="$long" hook "$P/CLAUDE.md"
  else STUB_CHECK_RC=1 STUB_CHECK_OUT="$long" CLAUDE_PROJECT_DIR="$pd" hook "$P/CLAUDE.md"; fi
  if [[ $RC == 0 && $ERR == "$told"* && $ERR != *$'\n'* ]]; then ok "${row#*|} without .rumdl.toml exits 0 with one stderr line naming it"
  else bad "${row#*|} without .rumdl.toml" "rc $RC (want 0)" "stderr: ${ERR:-<empty>}"; fi
done
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

# The docs hub owns docs/_index.md, docs/standards.md and its clusters; any other
# docs/ root file is the adopter's own.
mkdir -p "$R/docs/architecture/data" "$R/docs/api" "$R/docs/map"
for owned in docs/_index.md docs/standards.md docs/architecture/data/flow.md docs/api/_index.md docs/map/_index.md; do
  printf '# T\n' > "$R/$owned"
  STUB_CHECK_RC=1 STUB_CHECK_OUT="$owned:1:1: [MD041] x" hook "$R/$owned"
  if [[ $RC == 2 && $ERR == *"UNFIXED $owned:1:1 MD041"* ]]; then ok "the hub file $owned is formatted"
  else bad "the hub file $owned" "rc $RC (want 2)" "stderr: ${ERR:-<empty>}"; fi
done
printf '# T\n' > "$R/docs/notes.md"
STUB_CHECK_RC=1 STUB_CHECK_OUT='docs/notes.md:1:1: [MD041] x' hook "$R/docs/notes.md"
if [[ $RC == 0 && -z $ERR ]]; then ok "a docs/ root file outside the hub is left alone"
else bad "a docs/ root file outside the hub" "rc $RC (want 0)" "stderr: ${ERR:-<empty>}"; fi

# The settings.json twin beside this hook runs it from the session's current directory: a worktree of the
# project with its own hook runs that one; a plugin submodule, a foreign repository holding its own hook, or a
# directory outside any repository runs the project's.
SETTINGS="$(dirname "$(dirname "$HOOK")")/settings.json"
SETTINGS_CMD=$(jq -er '[.hooks.PostToolUse[] | select(.matcher == "Edit|Write") | .hooks[] | .command | select(contains("format-md.sh"))] | first' "$SETTINGS") ||
  { echo "test-format-md: no format-md.sh PostToolUse command in $SETTINGS" >&2; exit 2; }
S="$(cd "$T" && pwd -P)/session"
W="$(cd "$T" && pwd -P)/worktree"
F="$(cd "$T" && pwd -P)/foreign"
N="$(cd "$T" && pwd -P)/plain"
# Only .rumdl.toml is committed: a.md stays new, so its replayed MD041 is the edit's own.
mkdir -p "$S/.claude/scripts" "$S/docs/dev" "$S/plugins/p" "$F/.claude/scripts" "$N" && git -C "$S" init -q &&
  printf '[global]\n' > "$S/.rumdl.toml" && git -C "$S" add .rumdl.toml &&
  git -C "$S" -c user.name=t -c user.email=t@t commit -qm fixture && git -C "$S" worktree add -q "$W" &&
  git -C "$S/plugins/p" init -q && git -C "$F" init -q && mkdir -p "$W/.claude/scripts" &&
  printf '# T\n' > "$S/docs/dev/a.md" && cp "$HOOK" "$S/.claude/scripts/format-md.sh" &&
  printf '#!/usr/bin/env bash\necho "worktree hook ran" >&2\n' > "$W/.claude/scripts/format-md.sh" &&
  printf '#!/usr/bin/env bash\necho "foreign hook ran" >&2\n' > "$F/.claude/scripts/format-md.sh" &&
  chmod +x "$S/.claude/scripts/format-md.sh" "$W/.claude/scripts/format-md.sh" "$F/.claude/scripts/format-md.sh" ||
  { echo 'test-format-md: settings-command fixture setup failed' >&2; exit 2; }
settings_hook() { # settings_hook <cwd> <file>: RC and ERR of the settings command run from <cwd>
  ERR=$(cd "$1" && printf '{"tool_input":{"file_path":"%s"}}' "$2" | CLAUDE_PROJECT_DIR="$S" bash -c "$SETTINGS_CMD" 2>&1 >/dev/null)
  RC=$?
}
settings_hook "$W" "$S/docs/dev/a.md"
if [[ $RC == 0 && $ERR == "worktree hook ran" ]]; then ok "the settings command run inside a worktree runs that worktree's hook"
else bad "the settings command inside a worktree" "rc $RC (want 0)" "stderr: ${ERR:-<empty>}"; fi
for cwd in "$S/plugins/p" "$F" "$N"; do
  where="a plugin submodule"
  [[ $cwd == "$F" ]] && where="a foreign repository holding its own hook"
  [[ $cwd == "$N" ]] && where="a directory outside any repository"
  STUB_CHECK_RC=1 STUB_CHECK_OUT='docs/dev/a.md:1:1: [MD041] x' settings_hook "$cwd" "$S/docs/dev/a.md"
  if [[ $RC == 2 && $ERR == *"UNFIXED docs/dev/a.md:1:1 MD041"* && $ERR != *"foreign hook ran"* ]]; then
    ok "the settings command run inside $where runs the project's hook"
  else bad "the settings command inside $where" "rc $RC (want 2)" "stderr: ${ERR:-<empty>}"; fi
done
fi

# A committed baseline belongs to a second repo; the cases in $R above have no HEAD.
C="$(cd "$T" && pwd -P)/committed"
mkdir -p "$C/docs/dev" && git -C "$C" init -q && printf '[global]\n' > "$C/.rumdl.toml" &&
  printf '# T\n' > "$C/docs/dev/a.md" && git -C "$C" add docs/dev/a.md &&
  git -C "$C" -c user.name=t -c user.email=t@t commit -qm baseline ||
  { echo 'test-format-md: committed fixture setup failed' >&2; exit 2; }
standing=$'docs/dev/a.md:8:1: [MD051] Link anchor \'#missing\' does not exist in document headings\n'
shifted=$'docs/dev/a.md:9:1: [MD051] Link anchor \'#missing\' does not exist in document headings\n'
introduced=$'docs/dev/a.md:12:3: [MD018] No space after hash on atx style heading\n'

if [[ $readiness_only == 0 ]]; then
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

fi

# Both hooks report standing uncommitted diagnostics once per session.
gate="$T/overlap"
mkdir -p "$gate" || exit 2
printf '{"session_id":"overlap","tool_input":{"file_path":"%s"}}' "$C/docs/dev/a.md" |
  STUB_FMT_GATE="$gate" STUB_COMMITTED_CHECK_RC=0 STUB_CHECK_RC=1 STUB_CHECK_OUT="$introduced" TMPDIR="$T/cache" \
    timeout 15 bash "$HOOK" > "$gate/first.out" 2> "$gate/first.err" &
first_hook=$!
# The formatter seam parks the first actor inside its snapshot transaction.
for ((tick=0; tick<50; tick++)); do
  [[ ! -f "$gate/ready" ]] || break
  kill -0 "$first_hook" 2>/dev/null || break
  sleep 0.1
done
if [[ ! -f "$gate/ready" ]]; then
  if kill -0 "$first_hook" 2>/dev/null; then
    reason='timed out waiting for formatter'
    kill -TERM "$first_hook" 2>/dev/null || true
    wait "$first_hook" || true
  else
    wait "$first_hook"; first_rc=$?
    reason="hook exited before formatter readiness (exit $first_rc)"
  fi
  bad "formatter readiness FAILED — $reason" "$(cat "$gate/first.err")"
  shtest_end
  exit 1
fi
if [[ $readiness_only == 1 ]]; then
  printf 'release\n' > "$gate/release"
  wait "$first_hook"; first_rc=$?
  if [[ $first_rc == 0 || $first_rc == 2 ]]; then ok 'formatter readiness detected';
  else bad 'formatter failed after readiness' "exit $first_rc"; fi
  shtest_end
  exit
fi
ln -s a.md "$C/docs/dev/alias.md" || exit 2
STUB_COMMITTED_CHECK_RC=0 STUB_CHECK_RC=0 TMPDIR="$T/cache" HOOK_SESSION=overlap hook "$T/link/docs/dev/alias.md"
if [[ $RC == 2 && $ERR == *'diagnostic snapshot busy'* ]]; then ok "an overlapping alias hook refuses the active snapshot transaction"
else bad "an overlapping alias hook" "rc=$RC; err=$ERR"; fi
printf 'release\n' > "$gate/release"
wait "$first_hook"; first_rc=$?
if [[ $first_rc == 2 ]] && [[ $(cat "$gate/first.err") == *UNFIXED* ]]; then ok "the snapshot owner finishes with its diagnostic"
else bad "the snapshot owner" "rc=$first_rc"; fi
STUB_COMMITTED_CHECK_RC=0 STUB_CHECK_RC=1 STUB_CHECK_OUT="$introduced" TMPDIR="$T/cache" HOOK_SESSION=overlap hook "$C/docs/dev/a.md"
if [[ $RC == 0 && -z $ERR ]]; then ok "the next actor reads the completed snapshot"
else bad "the next actor reads the completed snapshot" "rc=$RC; err=$ERR"; fi
STUB_COMMITTED_CHECK_RC=0 STUB_CHECK_RC=2 STUB_CHECK_OUT='injected failure' TMPDIR="$T/cache" HOOK_SESSION=overlap hook "$C/docs/dev/a.md"
if [[ $RC == 2 && $ERR == *'FAILED rumdl check'* ]]; then ok "a failed check reports before releasing snapshot ownership"
else bad "a failed check releases ownership" "rc=$RC; err=$ERR"; fi
STUB_COMMITTED_CHECK_RC=0 STUB_CHECK_RC=0 TMPDIR="$T/cache" HOOK_SESSION=overlap hook "$C/docs/dev/a.md"
if [[ $RC == 0 && -z $ERR ]]; then ok "a clean actor can clear the snapshot after a failed actor"
else bad "a clean actor clears after failure" "rc=$RC; err=$ERR"; fi
STUB_COMMITTED_CHECK_RC=0 STUB_CHECK_RC=1 STUB_CHECK_OUT="$introduced" TMPDIR="$T/cache" HOOK_SESSION=overlap hook "$C/docs/dev/a.md"
if [[ $RC == 2 && $ERR == *UNFIXED* ]]; then ok "a diagnostic reintroduced after cleanup is reported"
else bad "a reintroduced diagnostic" "rc=$RC; err=$ERR"; fi
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
shtest_end
