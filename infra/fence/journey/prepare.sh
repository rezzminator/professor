#!/usr/bin/env bash
# prepare.sh — readies a disposable machine's owner for the journey suite, on Linux (the journey
# machine, run by run.sh) and macOS (the CI runner itself). Run as the owner from the tree under test:
#   bash infra/fence/journey/prepare.sh {tree}        HERDR_SHIELD_BIN={built herdr-shield} required
# It builds the fake engine and puts claude, codex and opencode on the owner's PATH as it, writes the
# owner's shell, Herdr and herdr-shield configuration, snapshots the tree into the repository INSTALL.md
# clones (~/.local/share/journey/mirror) with the script that clones it (~/.local/share/journey/install.sh),
# and leaves a file of the owner's own in ~/.claude. It installs nothing of pfm: the suite's first step does.
# Bash 3.2 and BSD-tool safe; every OS difference goes through scripts/platform.sh.
# BROKEN STATE: `prepare: FAIL {step}: {cause}` and exit 1; refuses outright off a disposable machine.
set -euo pipefail
fail() { echo "prepare: FAIL $*" >&2; exit 1; }
root=$(cd "${1:?usage: prepare.sh TREE}" && pwd -P) || fail "setup: cannot resolve the tree $1"
# shellcheck source=scripts/platform.sh
. "$root/scripts/platform.sh" || fail "setup: cannot source scripts/platform.sh"
platform_in_fence || fail "setup: refusing: this is not a disposable machine (PFM_DEV_FENCE=1 in a container, or a GitHub-hosted runner)"
shield_bin="${HERDR_SHIELD_BIN:?HERDR_SHIELD_BIN names the built herdr-shield}"
[ -x "$shield_bin" ] || fail "setup: HERDR_SHIELD_BIN=$shield_bin is not executable"
for tool in git go tar zsh tmux herdr sqlite3; do
  command -v "$tool" >/dev/null 2>&1 || fail "setup: $tool is not on PATH"
done

j="$HOME/.local/share/journey"
rm -rf "$HOME/.local/share/journey"
mkdir -p "$j/bin" "$j/mirror" "$HOME/.local/bin" "$HOME/.claude" || fail "setup: cannot create $j"

# 1. The fake engine, on PATH after ~/.local/bin, where pfm's own claude launcher lands at install.
GOTOOLCHAIN=local go -C "$root/pfm" build -o "$j/bin/mock-engine" ./cmd/mock-engine ||
  fail "engines: cannot build the fake engine"
for engine in claude codex opencode; do ln -sfn "$j/bin/mock-engine" "$j/bin/$engine"; done

# 2. engine-sync at its stable path: INSTALL.md has a Claude session produce it, and none runs here.
# shellcheck source=infra/fence/engine-sync.sh
. "$root/infra/fence/engine-sync.sh"
if [ -n "${ENGINE_SYNC_BIN:-}" ]; then
  engine_sync="$ENGINE_SYNC_BIN"
else
  ENGINE_SYNC_FENCE_SRC="$root/plugins/engine-sync" engine_sync_build "$j/bin/engine-sync" ||
    fail "engine-sync: cannot build the binary"
  engine_sync="$j/bin/engine-sync"
fi
engine_sync_link_stable "$engine_sync" || fail "engine-sync: cannot link the stable path"

# 3. The owner's shell: PATH and a prompt the suite knows. pfm install appends its own lines.
go_bin=$(dirname "$(command -v go)")
cat >"$HOME/.zshenv" <<ZSH
export PATH="\$HOME/.local/bin:$j/bin:$go_bin:\$PATH"
ZSH
cat >"$HOME/.zshrc" <<'ZSH'
PROMPT='owner %# '
unsetopt BEEP
ZSH

# 4. Herdr opening every pane through herdr-shield, whose inner shell is pfm's wrapper: the owner's setup.
plugin="$HOME/.local/share/herdr-shield/plugin"
mkdir -p "$plugin/bin" || fail "herdr: cannot create $plugin"
install -m 0644 "$root/plugins/herdr-shield/herdr-plugin.toml" "$plugin/herdr-plugin.toml" ||
  fail "herdr: cannot install herdr-plugin.toml from $root/plugins/herdr-shield"
install -m 0755 "$shield_bin" "$plugin/bin/herdr-shield" || fail "herdr: cannot install $shield_bin"
ln -sfn "$plugin/bin/herdr-shield" "$HOME/.local/bin/herdr-shield"
# herdr-shield reads Herdr's config only from a private directory, as Herdr itself makes it.
install -d -m 0700 "$HOME/.config/herdr" "$HOME/.config/herdr-shield" || fail "herdr: cannot create the config directories"
cat >"$HOME/.config/herdr/config.toml" <<TOML
onboarding = false

[update]
version_check = false
manifest_check = false

[terminal]
default_shell = "$HOME/.local/bin/herdr-shield"
shell_mode = "non_login"

[session]
resume_agents_on_restore = false

[ui.toast]
delivery = "off"

[ui.sound]
enabled = false
TOML
(umask 077 && printf '{"shell":"%s","args":["-l"]}\n' "$HOME/.local/share/pfm/install/bin/pfm-herdr-shell" \
  >"$HOME/.config/herdr-shield/config.json") || fail "herdr: cannot write herdr-shield's config"

# 5. The repository INSTALL.md clones, and the script that clones it.
src_git() { git -C "$root" -c safe.directory='*' "$@"; }
# shellcheck source=scripts/install-snapshot.sh
. "$root/scripts/install-snapshot.sh"
version=$(cat "$root/VERSION") || fail "snapshot: cannot read $root/VERSION"
install_snapshot_mirror "$root" "$j/mirror" "${version%%-*}" || fail "$install_snapshot_why"
install_snapshot_script "$root/INSTALL.md" "$j/mirror" "$j" || fail "$install_snapshot_why"

# 6. A file the owner had before pfm: the uninstall leaves it as it was.
printf 'the owner wrote this before pfm was installed\n' >"$HOME/.claude/owner-keep.txt"
echo "prepare: PASS"
