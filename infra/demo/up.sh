#!/usr/bin/env bash
# up.sh — stands up the live-demo fence (see README.md): the dev-fence container
# on THIS checkout, real Claude Code + Codex + pfm inside, host seats copied in,
# the demo fleet spawned.
#
#   infra/demo/up.sh [--name NAME] [--accounts 1,2,3] [--no-fleet] [--fresh]
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
NAME=pfm-demo ACCOUNTS=1,2,3 FLEET=1 FRESH=0
while [ $# -gt 0 ]; do
  case "$1" in
    --name) NAME="$2"; shift 2 ;;
    --accounts) ACCOUNTS="$2"; shift 2 ;;
    --no-fleet) FLEET=0; shift ;;
    --fresh) FRESH=1; shift ;;
    *) echo "usage: up.sh [--name NAME] [--accounts 1,2,3] [--no-fleet] [--fresh]" >&2; exit 2 ;;
  esac
done

missing() { echo "demo: TOOLCHAIN-MISSING — $1" >&2; exit 1; }
command -v docker >/dev/null || missing "docker"
docker info >/dev/null 2>&1 || missing "the docker daemon is not reachable ('docker info' failed)"
command -v jq >/dev/null || missing "jq"
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
  bash "$ROOT/infra/prepare-fence-mounts.sh" "$ROOT"
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
while IFS=$'\t' read -r host_dir cont_dir; do
  seat_args+=(--seat "$host_dir=$cont_dir")
done < <(jq -r '.accounts[] | "\(.configDir)\t\(.configDir)"' <<<"$container_config" | sed "s#^~#$HOME#")
bash "$HERE/creds.sh" --container "$NAME" "${seat_args[@]}" --codex "$HOME/.codex=~/.codex" \
  --opencode "$HOME/.local/share/opencode/auth.json=~/.local/share/opencode/auth.json"

# 5. pfm install and the fleet's projects; then Professor installed on a real repo
#    (adopt.sh: clone → pfm init → a real chat runs the interview); then the fleet.
docker exec -w /tmp "$NAME" bash /worktree/infra/demo/setup.sh install
docker exec -w /tmp "$NAME" bash /worktree/infra/demo/adopt.sh
if [ "$FLEET" -eq 1 ]; then docker exec -w /tmp "$NAME" bash /worktree/infra/demo/fleet.sh; fi
echo "demo: enter with  docker exec -it -w /work/express -e TERM=xterm-256color -e COLORTERM=truecolor $NAME zsh -i"
