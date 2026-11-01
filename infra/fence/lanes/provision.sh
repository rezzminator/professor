#!/usr/bin/env bash
# Build the lane tools and stage only invented, registered fixture seats; `held`
# provisions lane HELD's machine on the root (lanes/root.sh --machine held).
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
    for engine in claude codex opencode; do ln -sf /usr/local/libexec/pfm-lanes/mock-engine "/usr/local/bin/$engine"; done
    cp "$HERE/scenarios/default.json" "$HOME/.local/share/pfm-lanes/default.json"
    ;;
  seats)
    umask 077
    mkdir -p "$(dirname "$PFM_CONFIG")" "$HOME/.codex" "$HOME/.local/share/opencode"
    cat >"$PFM_CONFIG" <<'JSON'
{"version":2,"accounts":[{"id":1,"configDir":"~/.cc/1","emoji":"🥇"},{"id":2,"configDir":"~/.cc/2","emoji":"🥈"}],"codex":{"homes":[{"id":1,"home":"~/.codex","emoji":"🥇"}]},"claude":{"systemPrompt":"professor"},"mcp":{"servers":{"chat":{"enabled":true}}}}
JSON
    trust="$(printf '%s\n' atlas lumen orbit express held | jq -R '{key: ("/work/" + .), value: {hasTrustDialogAccepted: true}}' | jq -s from_entries)"
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
    for project in atlas lumen orbit; do
      mkdir -p "/work/$project"
      if [ ! -d "/work/$project/.git" ]; then
        (cd "/work/$project" && git init -q && git commit -q --allow-empty -m init && git checkout -q -b develop)
      fi
    done
    printf '{"$schema":"https://opencode.ai/tui.json","theme":"tokyonight"}\n' >"$HOME/.config/opencode/tui.json"
    ;;
  held)
    # Lane HELD's machine, as root on the lane root (lanes/root.sh --machine held):
    # systemd with a lingering user lane, whose Herdr opens every default-shell
    # pane through herdr-keep (built into /herdr-keep-target) and herdr-keep's
    # inner shell through pfm's wrapper — the owner's configuration.
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -q
    apt-get install -y -q --no-install-recommends systemd systemd-sysv dbus dbus-user-session libpam-systemd
    rm -rf /var/lib/apt/lists/*
    # Ubuntu's image user holds uid 1000.
    if id -u ubuntu >/dev/null 2>&1; then userdel -r ubuntu; fi
    useradd -m -d /srv/lane -u 1000 -s /usr/bin/zsh lane
    # The root build's own install leaves its scratch in /tmp and a 0700
    # /etc/claude-code; the owner's host has neither, so lane meets what the owner meets.
    find /tmp -mindepth 1 -maxdepth 1 -user root -exec rm -rf {} +
    if [ -d /etc/claude-code ]; then find /etc/claude-code -type d -exec chmod 0755 {} +; fi
    mkdir -p /var/lib/systemd/linger
    touch /var/lib/systemd/linger/lane
    mkdir -p /work/held
    git -C /work/held init -q -b main
    git -C /work/held -c user.name=demo -c user.email=demo@example.invalid commit -q --allow-empty -m init
    git -C /work/held checkout -q -b develop
    chown -R lane:lane /work
    lane=/srv/lane plugin=/srv/lane/.local/share/herdr-keep/plugin
    mkdir -p "$lane/.local/bin" "$plugin/bin" "$lane/.local/share/pfm-lanes"
    # herdr-keep reads Herdr's config only from a private directory, as Herdr itself makes it.
    install -d -m 0700 "$lane/.config/herdr"
    install -m 0755 "$HOME/.local/bin/pfm" "$lane/.local/bin/pfm"
    install -m 0644 /herdr-keep/herdr-plugin.toml "$plugin/herdr-plugin.toml"
    install -m 0755 "${HELD_KEEP_BIN:?lanes/root.sh names the herdr-keep binary for this key}" "$plugin/bin/herdr-keep"
    ln -sfn "$plugin/bin/herdr-keep" "$lane/.local/bin/herdr-keep"
    cp "$HERE/scenarios/default.json" "$lane/.local/share/pfm-lanes/default.json"
    cat >"$lane/.config/herdr/config.toml" <<'TOML'
onboarding = false

[update]
version_check = false
manifest_check = false

[terminal]
default_shell = "/srv/lane/.local/bin/herdr-keep"
shell_mode = "non_login"

[session]
resume_agents_on_restore = false

[ui.toast]
delivery = "off"

[ui.sound]
enabled = false
TOML
    install -d -m 0700 "$lane/.config/herdr-keep"
    (umask 077 && printf '%s\n' '{"shell":"/srv/lane/.local/share/pfm/install/bin/pfm-herdr-shell","args":["-l"]}' >"$lane/.config/herdr-keep/config.json")
    . /worktree/infra/fence/engine-sync.sh
    HOME="$lane" XDG_DATA_HOME="" ENGINE_SYNC_HOME="" engine_sync_link_stable "${ENGINE_SYNC_BIN:?lanes/container.sh sets ENGINE_SYNC_BIN}"
    chown -R lane:lane "$lane"
    ;;
  *) echo 'usage: provision.sh tools|seats|install|held' >&2; exit 2 ;;
esac
