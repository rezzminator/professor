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
    for engine in claude codex opencode; do ln -sf /usr/local/libexec/pfm-lanes/mock-engine "/usr/local/bin/$engine"; done
    cp "$HERE/scenarios/default.json" "$HOME/.local/share/pfm-lanes/default.json"
    ;;
  seats)
    umask 077
    mkdir -p "$(dirname "$PFM_CONFIG")" "$HOME/.codex" "$HOME/.local/share/opencode"
    cat >"$PFM_CONFIG" <<'JSON'
{"version":2,"accounts":[{"id":1,"configDir":"~/.cc/1","emoji":"🥇"},{"id":2,"configDir":"~/.cc/2","emoji":"🥈"}],"codex":{"homes":[{"id":1,"home":"~/.codex","emoji":"🥇"}]},"claude":{"systemPrompt":"professor"},"mcp":{"servers":{"chat":{"enabled":true},"harvester":{"enabled":true}}}}
JSON
    trust="$(printf '%s\n' atlas lumen orbit harvester express | jq -R '{key: ("/work/" + .), value: {hasTrustDialogAccepted: true}}' | jq -s from_entries)"
    mkdir -p "$HOME/.cc/1/themes"
    for id in 1 2; do
      dir="$HOME/.cc/$id"; mkdir -p "$dir"
      case "$id" in 1) theme=professor-gold ;; 2) theme=professor-silver ;; esac
      jq -n --argjson trust "$trust" --arg email "seat$id@lane.invalid" \
        '{hasCompletedOnboarding: true, oauthAccount: {emailAddress: $email}, projects: $trust}' >"$dir/.claude.json"
      jq -n --arg t "custom:$theme" \
        '{skipDangerousModePermissionPrompt: true, skipAutoPermissionPrompt: true, skipWorkflowUsageWarning: true, theme: $t, tui: "fullscreen", effortLevel: "low", feedbackDrafts: "off", attribution: {commit: "", pr: "", sessionUrl: false}}' >"$dir/settings.json"
      cp "$HERE/fixtures/claude-seat-$id.json" "$dir/.credentials.json"
      [ "$id" = 1 ] || [ -L "$dir/themes" ] || ln -s "$HOME/.cc/1/themes" "$dir/themes"
    done
    cp "$HERE/fixtures/codex-auth.json" "$HOME/.codex/auth.json"
    cp "$HERE/fixtures/opencode-auth.json" "$HOME/.local/share/opencode/auth.json"
    ;;
  install)
    [ -e "$HOME/.professor" ] || ln -s /worktree "$HOME/.professor"
    mkdir -p "$HOME/.config/opencode"
    printf '{"model":"openai/lane-fixture"}\n' >"$HOME/.config/opencode/opencode.jsonc"
    (cd /worktree && pfm install --yes)
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
