#!/usr/bin/env bash
# up.sh — stands up the live-demo fence (see README.md): the dev-fence container
# on THIS checkout, real Claude Code + Codex + pfm inside, host seats copied in,
# the demo fleet spawned.
#
#   infra/demo/up.sh [--name NAME] [--accounts 1,2,3] [--login] [--no-fleet] [--no-verify] [--fresh]
#
# The last step is verify.sh — the deck exercised end to end inside the
# container (inject round trip, /reload, self-compact, storm, idle, headless,
# the Express install's Codex mirror) and judged from what pfm reports. That is
# the integration gate this fence exists for: a green up.sh is a container that
# carries the whole deck. --no-verify skips it (and --no-fleet implies it).
#
# --login: the container's Claude seats are NOT copied from the host — a copied seat
#   shares the host's refresh token, the first refresh rotates it and one side is
#   logged out (it took seat 2, then seat 1). Instead each seat logs in inside the
#   container: the build stops before the interview and prints the login line per
#   seat; run it in a container terminal, then re-run up.sh --login to continue.
#
# Presentation scripts, run inside afterwards: storm.sh (the cosmos storm),
# adopt.sh (Professor onto another real repo).
#
# Idempotent: a running container is reused (pass --fresh to rebuild it).
# Runs OUTSIDE the command sandbox: it talks to the Docker socket and the Keychain.
#
# BROKEN STATE: a missing tool is TOOLCHAIN-MISSING (exit 1) before anything
# starts; every in-container step exits non-zero with its own message; the last
# line is `pfm ls --plain` from inside the container — a listing with no live
# row means the fleet never came up.
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
HERE="$ROOT/infra/demo"
NAME=pfm-demo ACCOUNTS=1,2,3 FLEET=1 FRESH=0 LOGIN=0 VERIFY=1
while [ $# -gt 0 ]; do
  case "$1" in
    --name) NAME="$2"; shift 2 ;;
    --accounts) ACCOUNTS="$2"; shift 2 ;;
    --no-fleet) FLEET=0; shift ;;
    --no-verify) VERIFY=0; shift ;;
    --fresh) FRESH=1; shift ;;
    --login) LOGIN=1; shift ;;
    *) echo "usage: up.sh [--name NAME] [--accounts 1,2,3] [--login] [--no-fleet] [--no-verify] [--fresh]" >&2; exit 2 ;;
  esac
done

missing() { echo "demo: TOOLCHAIN-MISSING — $1" >&2; exit 1; }
command -v docker >/dev/null || missing "docker"
docker info >/dev/null 2>&1 || missing "the docker daemon is not reachable ('docker info' failed)"
command -v jq >/dev/null || missing "jq"
# The fleet plus a 6-chat storm fills a 7.7 GiB Docker VM (25
# harnesses, ~1900% CPU): under 12 GiB the storm is capped at 4 in the note,
# not silently. Docker Desktop → Settings → Resources → Memory.
mem_gib="$(( $(docker info --format '{{.MemTotal}}') / 1073741824 ))"
[ "$mem_gib" -ge 12 ] || echo "demo: NOTE — the Docker VM has ${mem_gib} GiB; the fleet plus a 6-chat storm needs ~12: raise Docker Desktop → Resources → Memory to 14 GB, or keep storm-up at 4"
HOST_CONFIG="${XDG_CONFIG_HOME:-$HOME/.config}/pfm/pfm.config.json"
[ -f "$HOST_CONFIG" ] || missing "host pfm config $HOST_CONFIG (the seats to mirror come from it)"

# 1. The fence, exactly as dev.sh iso mounts it.
git_common="$(git -C "$ROOT" rev-parse --git-common-dir)"
[[ "$git_common" == /* ]] || git_common="$ROOT/$git_common"
git_common="$(cd "$git_common" && pwd -P)"
git_dir="$(cd "$(git -C "$ROOT" rev-parse --absolute-git-dir)" && pwd -P)"
case "$git_dir" in
  "$git_common") git_dir_rel="." ;;
  "$git_common"/*) git_dir_rel="${git_dir#"$git_common"/}" ;;
  *) echo "demo: git dir $git_dir is outside common dir $git_common" >&2; exit 1 ;;
esac
export PFM_DEV_WORKTREE="$ROOT" PFM_DEV_GIT_COMMON="$git_common" PFM_DEV_GIT_DIR_REL="$git_dir_rel"
if [ "$FRESH" -eq 1 ] || ! docker ps --format '{{.Names}}' | grep -qx "$NAME"; then
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  docker compose -f "$ROOT/infra/docker-compose.yml" run -d --build --name "$NAME" pfm-dev sleep infinity >/dev/null
  echo "demo: container $NAME started"
else
  echo "demo: reusing running container $NAME"
fi

# 2. Toolchain inside: pfm from the checkout, real Claude Code + Codex, Starship.
docker exec -w /tmp "$NAME" bash /worktree/infra/demo/setup.sh tools

# 3. The container's pfm config mirrors the selected host seats (same config
#    dirs relative to the container HOME, same emoji) plus one Codex home.
container_config="$(jq -c --arg ids "$ACCOUNTS" '
  ($ids | split(",") | map(tonumber)) as $want
  | {version: 2,
     accounts: [.accounts[] | select(.id as $id | $want | index($id)) | {id, configDir, emoji}],
     codex: {homes: [{id: 1, home: "~/.codex", emoji: "🥇"}]},
     claude: {systemPrompt: "professor"},
     mcp: {servers: {chat: {enabled: true}}}}' "$HOST_CONFIG")"
n_acct="$(jq '.accounts | length' <<<"$container_config")"
[ "$n_acct" -gt 0 ] || { echo "demo: none of the accounts $ACCOUNTS exist in $HOST_CONFIG" >&2; exit 1; }
docker exec -i "$NAME" sh -c 'mkdir -p /root/.config/pfm && cat > /root/.config/pfm/pfm.config.json' <<<"$container_config"
echo "demo: config written with $n_acct Claude seat(s) + 1 Codex home"

# 4. Credentials: Keychain → container, seat by seat; ~/.codex/auth.json and OpenCode's
#    ChatGPT auth.json → container.
seat_args=()
if [ "$LOGIN" -eq 0 ]; then
  while IFS=$'\t' read -r host_dir cont_dir; do
    seat_args+=(--seat "$host_dir=$cont_dir")
  done < <(jq -r '.accounts[] | "\(.configDir)\t\(.configDir)"' <<<"$container_config" | sed "s#^~#$HOME#")
fi
bash "$HERE/creds.sh" --container "$NAME" ${seat_args[@]+"${seat_args[@]}"} --codex "$HOME/.codex=~/.codex" \
  --opencode "$HOME/.local/share/opencode/auth.json=~/.local/share/opencode/auth.json"

# 4b. The presenter's terminal layer: ~/.config/code-theme (starship, tmux
#     palette + bar, CLI colours, bat theme) mirrored into the container, and
#     the Dev Containers attach config so VS Code installs the workspace-side
#     extensions (vim, errorlens, gitlens, go, …) into the container's server.
#     Both are optional: a host without them gets look.sh's SKIPPED lines.
if [ -d "$HOME/.config/code-theme" ]; then
  docker exec "$NAME" rm -rf /root/.config/code-theme
  docker cp "$HOME/.config/code-theme" "$NAME:/root/.config/code-theme"
  echo "demo: code-theme copied from the host"
fi
attach_dir="$HOME/Library/Application Support/Code/User/globalStorage/ms-vscode-remote.remote-containers"
if [ -d "$attach_dir" ]; then
  mkdir -p "$attach_dir/nameConfigs" && cp "$HERE/vscode-attach.json" "$attach_dir/nameConfigs/$NAME.json"
  echo "demo: VS Code attach config written for $NAME (extensions install on the next attach)"
fi

# 5. pfm install and the fleet's projects; then Professor installed on a real repo
#    (adopt.sh: clone → pfm init → a real chat runs the interview); then the fleet.
docker exec -w /tmp "$NAME" bash /worktree/infra/demo/setup.sh install
docker exec -w /tmp "$NAME" bash /worktree/infra/demo/look.sh
# 5b. Which seats answer. Every requested seat stays configured (the picker cycles
#     them all); the ones that answer are recorded in the container for adopt.sh,
#     fleet.sh and storm.sh, a seat that does not answer prints the exact login
#     line — run it in a container terminal (URL on the Mac, code pasted back) and
#     re-run this script; it gets its own tokens, nothing shared with the host.
#     No seat answering at all stops the build here, by name.
live=()
while IFS=$'\t' read -r id dir emoji; do
  if docker exec -e "CLAUDE_CONFIG_DIR=/root/${dir#\~/}" -e IS_SANDBOX=1 -w /tmp "$NAME" \
      timeout 90 claude -p "reply with the single word ok" --model haiku 2>/dev/null | grep -qi ok; then
    echo "demo: seat $emoji ($dir) answers"; live+=("$id")
  else
    echo "demo: seat $emoji ($dir) is NOT logged in inside the container — in a container terminal run:  CLAUDE_CONFIG_DIR=/root/${dir#\~/} claude   then /login, then re-run this script" >&2
  fi
done < <(jq -r '.accounts[] | "\(.id)\t\(.configDir)\t\(.emoji)"' <<<"$container_config")
[ "${#live[@]}" -gt 0 ] || { echo "demo: no Claude seat answers inside the container — log one in (lines above), then re-run" >&2; exit 1; }
docker exec -i "$NAME" sh -c 'mkdir -p /root/.local/state/pfm && cat > /root/.local/state/pfm/demo-seats-live' <<<"${live[*]}"
echo "demo: seats in use: ${live[*]}"
docker exec -w /tmp "$NAME" bash /worktree/infra/demo/adopt.sh
if [ "$FLEET" -eq 1 ]; then docker exec -w /tmp "$NAME" bash /worktree/infra/demo/fleet.sh; fi
# 6. The gate: every deck beat exercised for real and judged from pfm's own
#    reports (verify.sh header). Its closing line is the build's verdict.
if [ "$FLEET" -eq 1 ] && [ "$VERIFY" -eq 1 ]; then
  docker exec -w /tmp "$NAME" bash /worktree/infra/demo/verify.sh || { echo "demo: verify.sh FAILED (lines above) — the container is up but does not carry the whole deck" >&2; exit 1; }
fi
echo "demo: enter with  docker exec -it -w /work/express -e TERM=xterm-256color -e COLORTERM=truecolor $NAME zsh -i"
