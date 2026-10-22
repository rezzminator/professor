#!/usr/bin/env bash
# Build the lane tools and stage only invented, registered fixture seats.
set -euo pipefail
[ "${PFM_DEV_FENCE:-}" = 1 ] || { echo 'provision: requires the fence' >&2; exit 1; }
: "${PFM_CONFIG:?PFM_CONFIG is required in the container}"
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
export PATH="$HOME/.local/bin:$PATH"
trap 'rc=$?; echo "provision: ${1:-unknown} failed at line $LINENO (exit $rc)" >&2; exit "$rc"' ERR
case "${1:-}" in
  tools)
    mkdir -p "$HOME/.local/bin" /usr/local/libexec/pfm-lanes "$HOME/.local/share/pfm-lanes"
    cd /worktree/pfm
    GOTOOLCHAIN=local go build -ldflags "-X main.version=$(cat ../VERSION)" -o "$HOME/.local/bin/pfm" ./cmd/pfm
    GOTOOLCHAIN=local go build -o /usr/local/libexec/pfm-lanes/mock-engine ./cmd/mock-engine
    # The Codex/OpenCode compiler pfm execs, from the source lanes/container.sh mounts at /engine-sync.
    . /worktree/infra/fence/engine-sync.sh
    engine_sync_build "${ENGINE_SYNC_BIN:?lanes/container.sh sets ENGINE_SYNC_BIN}"
    # No Claude session runs here, so no plugin wrapper makes the stable link pfm install's PATH link needs.
    engine_sync_link_stable "$ENGINE_SYNC_BIN"
    # The lane activity sweep judges both binaries through one fixture log.
    plugin_log="${XDG_STATE_HOME:-$HOME/.local/state}/harvester/logs/harvester.jsonl"
    fleet_log="${PFM_HOME:-$HOME/.local/state/pfm}/log/pfm.jsonl"
    mkdir -p "$(dirname "$plugin_log")" "$(dirname "$fleet_log")"
    ln -sf "$fleet_log" "$plugin_log"
    for engine in claude codex opencode; do ln -sf /usr/local/libexec/pfm-lanes/mock-engine "/usr/local/bin/$engine"; done
    cp "$HERE/scenarios/default.json" "$HOME/.local/share/pfm-lanes/default.json"
    ;;
  seats)
    umask 077
    mkdir -p "$(dirname "$PFM_CONFIG")" "$HOME/.codex" "$HOME/.local/share/opencode"
    cat >"$PFM_CONFIG" <<'JSON'
{"version":2,"accounts":[{"id":1,"configDir":"~/.cc/1","emoji":"🥇"},{"id":2,"configDir":"~/.cc/2","emoji":"🥈"}],"codex":{"homes":[{"id":1,"home":"~/.codex","emoji":"🥇"}]},"claude":{"systemPrompt":"professor"},"mcp":{"servers":{"chat":{"enabled":true},"harvester":{"enabled":true}}}}
JSON
    mkdir -p "$HOME/.config/harvester"
    printf '{}\n' >"$HOME/.config/harvester/harvester.config.json"
    trust="$(printf '%s\n' atlas lumen orbit harvester express | jq -R '{key: ("/work/" + .), value: {hasTrustDialogAccepted: true}}' | jq -s from_entries)"
    for id in 1 2; do
      dir="$HOME/.cc/$id"; mkdir -p "$dir"
      jq -n --argjson trust "$trust" --arg email "seat$id@lane.invalid" \
        '{hasCompletedOnboarding: true, oauthAccount: {emailAddress: $email}, projects: $trust}' >"$dir/.claude.json"
      cp "$HERE/fixtures/claude-seat-$id.json" "$dir/.credentials.json"
    done
    cp "$HERE/fixtures/codex-auth.json" "$HOME/.codex/auth.json"
    cp "$HERE/fixtures/opencode-auth.json" "$HOME/.local/share/opencode/auth.json"
    ;;
  install)
    # The mounted worktree stays read-only; install owns generated prompt files
    # inside this container-local clone.
    bash /worktree/infra/fence/source-clone.sh /worktree "$HOME/.professor"
    mkdir -p "$HOME/.config/opencode"
    printf '{"model":"openai/lane-fixture"}\n' >"$HOME/.config/opencode/opencode.jsonc"
    (cd "$HOME/.professor" && pfm install --yes)
    [ "$(command -v engine-sync)" = "$HOME/.local/bin/engine-sync" ] ||
      { echo "provision: engine-sync not on PATH after pfm install (got: $(command -v engine-sync || echo none))" >&2; exit 1; }
    settings_tmp="$(mktemp)"
    jq --arg t "custom:tokyo-night" '. + {skipDangerousModePermissionPrompt: true, skipAutoPermissionPrompt: true, skipWorkflowUsageWarning: true, theme: $t, tui: "fullscreen", effortLevel: "low", feedbackDrafts: "off", attribution: {commit: "", pr: "", sessionUrl: false}}' "$HOME/.claude/settings.json" >"$settings_tmp"
    cat "$settings_tmp" >"$HOME/.claude/settings.json"
    rm -f "$settings_tmp"
    git config --global user.name demo
    git config --global user.email demo@example.invalid
    git config --global init.defaultBranch main
    for project in atlas lumen orbit harvester; do
      mkdir -p "/work/$project"
      if [ ! -d "/work/$project/.git" ]; then
        (cd "/work/$project" && git init -q && git commit -q --allow-empty -m init && git checkout -q -b develop)
      fi
    done
    printf '{"$schema":"https://opencode.ai/tui.json","theme":"tokyonight"}\n' >"$HOME/.config/opencode/tui.json"
    ;;
  *) echo 'usage: provision.sh tools|seats|install' >&2; exit 2 ;;
esac
