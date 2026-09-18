#!/usr/bin/env bash
# adopt.sh — runs INSIDE the demo fence container: installs Professor on a real,
# well-known repository the natural way — clone, `pfm init`, then a REAL Claude
# chat follows docs/SETUP.md § Install interview with the answers given up
# front, so the room sees what Professor looks like on a project it already knows.
#
#   adopt.sh [URL=https://github.com/expressjs/express] [NAME=express] [PITCH] [TEST_CMD]
#
# The interview chat is named <NAME>_INSTALL and stays in the fleet: its
# transcript IS the install record. `--await --settle 60` blocks until the chat
# has been quiet a full minute (up to 25 min), so the caller sees the end
# state, not a booting row — pfm's exit code is the judge. An interview
# that died mid-way (a ↻ row instead of a ● one) is resumed in place and told
# to finish; one that left registered tokens unfilled is handed the file list
# once and given a second settle before the verdict.
#
# The install is finished only when the Codex mirror the interview said yes to
# is real: .claude/scripts/codex-sync.sh (the Stop hook's command) present,
# .codex compiled, `pfm codex build` + `pfm codex check` passing, and no
# registered token left in CLAUDE.md, AGENTS.md, .claude or .codex. Only then
# is the 'professor: install' commit written — the marker a re-run skips on.
#
# BROKEN STATE: a clone or `pfm init` failure exits with its own message; an
# interview that does not settle in 25 min names its last state; a token still
# unfilled after the second round is `TOKENS-LEFT: <n>` with the files (exit
# 1); a missing hook script or a failing codex build/check prints the stage's
# own output (exit 1). No marker commit is written on any of those.
set -euo pipefail
export PATH="$HOME/.local/bin:$PATH"
export IS_SANDBOX=1 # root fence: Claude Code refuses the bypass flag under root without it (setup.sh)
URL="${1:-https://github.com/expressjs/express}"
NAME="${2:-express}"
PITCH="${3:-Express is a fast, unopinionated, minimalist web framework for Node.js.}"
TEST_CMD="${4:-npm test}"
DIR="/work/$NAME"
"$(dirname "$0")/daemon.sh" # chat_* over HTTP for Codex rows; harmless when already up
if [ ! -d "$DIR/.git" ]; then
  git clone -q --depth 1 "$URL" "$DIR"
  (cd "$DIR" && git checkout -q -b develop && { [ -f package.json ] && npm install --silent >/dev/null 2>&1 || true; })
  echo "adopt: cloned $URL → $DIR"
fi
if [ ! -f "$DIR/.professor/baseline.json" ]; then
  (cd "$DIR" && pfm init . | tail -2)
fi
cd "$DIR"
CHAT="$(echo "$NAME" | tr a-z A-Z)_INSTALL"
SEAT="$(cut -d" " -f1 "$HOME/.local/state/pfm/demo-seats-live" 2>/dev/null || jq -r ".accounts[0].id" "$HOME/.config/pfm/pfm.config.json")"

# Settled = pfm's own verdict: `--await --settle 60` returns once the chat has
# been quiet for a full minute after its last word (exit 0, or 7 when another
# message reached it mid-wait — the state is what matters here, not whose
# question the last line answers); `chat watch --idle-after 60 --once` is the
# same rule for a chat that was already running. Exit 5 is "still working" at
# the 25-min bound, 3/4 a chat that died — each named, none waited on.
settled() { # settled <exit code> <what was waited on>
  case "$1" in
    0|7) return 0 ;;
    5) echo "adopt: $CHAT is still working after 25 min ($2) — attach it: pfm chat capture $CHAT" >&2; return 1 ;;
    3|4) echo "adopt: $CHAT died before finishing ($2) — its transcript: pfm chat last $CHAT" >&2; return 1 ;;
    *) echo "adopt: $2 failed with exit $1 (pfm's message above)" >&2; return 1 ;;
  esac
}
watch_idle() { # set -e is on: the status is captured, never allowed to abort the script unnamed
  local rc=0
  timeout 1500 pfm chat watch "$CHAT" --idle-after 60 --once >/dev/null || rc=$?
  [ "$rc" -eq 124 ] && rc=5 # timeout(1)'s own code → "still working at the bound"
  settled "$rc" "chat watch --idle-after 60"
}
# Only tokens the registry substitutes count: PLACEHOLDERS.md's closing
# "Runtime metavariables" section registers tokens that stay literal by design
# ({SCOPE}, {SEED_*}, …), and a shell ${VAR} was never a placeholder. The
# Codex keepers (.codex/config.toml, rules, skills) carry tokens too.
reg=/worktree/docs/PLACEHOLDERS.md
runtime="$(sed -n '/^## Runtime metavariables/,$p' "$reg" | grep -oE '\{[A-Z][A-Z0-9_]{3,}\}' | sort -u)"
registry="$(sed '/^## Runtime metavariables/,$d' "$reg" | grep -oE '\{[A-Z][A-Z0-9_]{3,}\}' | sort -u | grep -vxF "$runtime" || true)"
[ -n "$registry" ] || { echo "adopt: $reg names no substituted tokens — the sweep cannot run" >&2; exit 1; }
# grep exits 1 on "no file matched" — the healthy outcome — which pipefail would
# otherwise turn into a silent abort right before the good news.
sweep() { { grep -rIlF "$registry" CLAUDE.md AGENTS.md .claude .codex 2>/dev/null || true; } | sort; }

# Idempotent by marker: the interview's own closing commit means the install is
# done. A ● <NAME>_INSTALL row means it is running — a re-run of up.sh never
# spawns a second interview, it waits for the first; a ↻ row is an interview
# that died mid-way — resumed in place (same transcript) and told to finish.
if git log --oneline 2>/dev/null | grep -q "professor: install"; then
  echo "adopt: $NAME already carries the 'professor: install' commit — interview skipped"
  exit 0
fi
kind="$(pfm ls --tsv 2>/dev/null | awk -F'\t' -v n="$CHAT" '$5 == n {print $1; exit}')"
case "$kind" in
live-*) echo "adopt: $CHAT is live — waiting for it to settle"; watch_idle || exit 1 ;;
resume-*)
  echo "adopt: $CHAT died mid-interview — resuming it in place"
  pfm chat open "$CHAT" </dev/null >/dev/null
  sleep 10
  pfm chat inject --allow-unsigned "$CHAT" "You were interrupted mid-interview. Continue from where your transcript stops and finish every remaining phase; end with one line: INSTALLED plus the count of files you changed." >/dev/null
  watch_idle || exit 1
  ;;
*)
  rc=0
  pfm chat new --name "$CHAT" --engine cc --account "$SEAT" --cwd "$DIR" --await --settle 60 --timeout 1500 \
    "pfm init has scaffolded Professor into this repository ($NAME). Follow /worktree/docs/SETUP.md § Install interview end to end, Phase 1 through Phase 3. Use these answers and do not ask them again: project identity: '$PITCH'; character: keep Professor; roster: single project, this repo, language from its package manifest; tech stack: as the repo shows; test command: '$TEST_CMD'; Tier B opt-ins: none; Codex dual-runtime: yes (the deck shows the compile hooks: .codex mirror, codex-sync.sh on PostToolUse and Stop, every {CODEX_*} token filled); sacred ground: none beyond the defaults; ports: none. Treat 'go' as already typed. Fill every registered token from /worktree/docs/PLACEHOLDERS.md in the scaffolded files, write .professor/manifest.json, run the smoke test, and close Phase 3 with 'pfm update check' reporting 'clean' — pin every interview-deployed file, ignore every declined template, as SETUP.md Phase 3 says. Finish with one line: INSTALLED plus the count of files you changed." >/dev/null || rc=$?
  settled "$rc" "chat new --await --settle 60" || exit 1
  ;;
esac
left="$(sweep)"
if [ -n "$left" ]; then
  # One second round: the chat gets the exact files, as a reviewer would hand them over.
  echo "adopt: $(wc -l <<<"$left" | tr -d ' ') file(s) still carry a registered token — handing the list back to $CHAT once"
  pfm chat inject --allow-unsigned "$CHAT" "These files still carry unfilled {TOKENS} from /worktree/docs/PLACEHOLDERS.md: $(tr '\n' ' ' <<<"$left"). Fill every one now, then reply with one line: TOKENS FILLED." >/dev/null
  sleep 20
  watch_idle || exit 1
  left="$(sweep)"
fi
n_left="$( [ -n "$left" ] && wc -l <<<"$left" | tr -d ' ' || echo 0)"
echo "adopt: $NAME interview settled · TOKENS-LEFT: $n_left file(s) with an unfilled token${left:+:}"
[ -z "$left" ] || { sed 's/^/adopt:   /' <<<"$left"; exit 1; }
# The Codex mirror the interview said yes to — the Stop hook runs
# .claude/scripts/codex-sync.sh sync, which runs pfm codex build + check; an
# install whose hook script is missing fails on the chat's first turn end.
[ -x .claude/scripts/codex-sync.sh ] || { echo "adopt: .claude/scripts/codex-sync.sh missing or not executable — the Stop hook names it; the Codex mirror was not installed" >&2; exit 1; }
grep -q 'codex-sync.sh' .claude/settings.json || { echo "adopt: .claude/settings.json wires no codex-sync.sh hook — the Codex mirror was not installed" >&2; exit 1; }
log=/tmp/adopt-codex.log
if ! { pfm codex build . && pfm codex check .; } >"$log" 2>&1; then
  echo "adopt: pfm codex build/check failed in $DIR — $log:" >&2; tail -20 "$log" >&2; exit 1
fi
echo "adopt: Codex mirror compiled — $(grep -E 'CODEX (BUILD|CHECK) PASS' "$log" | tr '\n' ' ')"
add_rc=0; add_stderr="$(git add -A 2>&1)" || add_rc=$?
if [ "$add_rc" -ne 0 ]; then
  add_stderr="$(printf '%s' "$add_stderr" | tr '\n' ' ')"
  echo "adopt: FAILED to add — ${add_stderr:-git add exit $add_rc with no error text}" >&2
  exit 1
fi
commit_rc=0; commit_stderr="$(git -c user.name=demo -c user.email=demo@example.invalid commit -q -m "professor: install" 2>&1)" || commit_rc=$?
if [ "$commit_rc" -ne 0 ]; then
  commit_stderr="$(printf '%s' "$commit_stderr" | tr '\n' ' ')"
  echo "adopt: FAILED to commit — ${commit_stderr:-git commit exit $commit_rc with no error text}" >&2
  exit 1
fi
echo "adopt: $NAME installed and committed ('professor: install')"
