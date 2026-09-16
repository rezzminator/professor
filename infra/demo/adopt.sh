#!/usr/bin/env bash
# adopt.sh — runs INSIDE the demo fence container: installs Professor on a real,
# well-known repository the natural way — clone, `pfm init`, then a REAL Claude
# chat follows docs/SETUP.md § Install interview with the answers given up
# front, so the room sees what Professor looks like on a project it already knows.
#
#   adopt.sh [URL=https://github.com/expressjs/express] [NAME=express] [PITCH] [TEST_CMD]
#
# The interview chat is named <NAME>_INSTALL and stays in the fleet: its
# transcript IS the install record. `--await` blocks until it settles (up to
# 25 min), so the caller sees the end state, not a booting row.
#
# BROKEN STATE: a clone or `pfm init` failure exits with its own message; an
# interview that leaves a registered token unfilled is reported by the closing
# grep as `TOKENS-LEFT: <count>` — that is a partial install, not a finished one.
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
pfm chat new --name "$(echo "$NAME" | tr a-z A-Z)_INSTALL" --engine cc --account 1 --cwd "$DIR" --await --timeout 1500 \
  "pfm init has scaffolded Professor into this repository ($NAME). Follow /worktree/docs/SETUP.md § Install interview end to end, Phase 1 through Phase 3. Use these answers and do not ask them again: project identity: '$PITCH'; character: keep Professor; roster: single project, this repo, language from its package manifest; tech stack: as the repo shows; test command: '$TEST_CMD'; Tier B opt-ins: none; Codex dual-runtime: no; sacred ground: none beyond the defaults; ports: none. Treat 'go' as already typed. Fill every registered token from /worktree/docs/PLACEHOLDERS.md in the scaffolded files, write .professor/manifest.json, run the smoke test, and finish with one line: INSTALLED plus the count of files you changed." >/dev/null
# Only tokens the registry substitutes count: PLACEHOLDERS.md's closing
# "Runtime metavariables" section registers tokens that stay literal by design
# ({SCOPE}, {SEED_*}, …), and a shell ${VAR} was never a placeholder.
reg=/worktree/docs/PLACEHOLDERS.md
runtime="$(sed -n '/^## Runtime metavariables/,$p' "$reg" | grep -oE '\{[A-Z][A-Z0-9_]{3,}\}' | sort -u)"
registry="$(sed '/^## Runtime metavariables/,$d' "$reg" | grep -oE '\{[A-Z][A-Z0-9_]{3,}\}' | sort -u | grep -vxF "$runtime" || true)"
[ -n "$registry" ] || { echo "adopt: $reg names no substituted tokens — the sweep cannot run" >&2; exit 1; }
left="$(grep -rIlF "$registry" CLAUDE.md .claude 2>/dev/null | wc -l | tr -d ' ')"
echo "adopt: $NAME interview settled · TOKENS-LEFT: $left file(s) with an unfilled token"
git add -A >/dev/null 2>&1 && git -c user.name=demo -c user.email=demo@example.invalid commit -q -m "professor: install" 2>/dev/null || true
