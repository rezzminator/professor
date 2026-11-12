#!/usr/bin/env bash
# prepare.sh — readies a disposable machine's owner for the journey suite, on Linux (the journey
# machine, run by run.sh) and macOS (the CI runner itself). Run as the owner from the tree under test:
#   bash infra/fence/journey/prepare.sh {tree}
# It builds the fake engine and puts claude, codex and opencode on the owner's PATH as it, writes the
# owner's shell and Herdr configuration, snapshots the tree into the repository INSTALL.md
# clones (~/.local/share/journey/mirror) with the script that clones it (~/.local/share/journey/install.sh),
# leaves a file of the owner's own in ~/.claude, a project at $HOME/work/demo and a second Claude account's
# directory at ~/.cc/2, and Codex and OpenCode logged in. It installs nothing of pfm: the suite's first step does.
# Bash 3.2 and BSD-tool safe; every OS difference goes through scripts/platform.sh.
# BROKEN STATE: `prepare: FAIL {step}: {cause}` and exit 1; refuses outright off a disposable machine.
set -euo pipefail
fail() { echo "prepare: FAIL $*" >&2; exit 1; }
root=$(cd "${1:?usage: prepare.sh TREE}" && pwd -P) || fail "setup: cannot resolve the tree $1"
# shellcheck source=scripts/platform.sh
. "$root/scripts/platform.sh" || fail "setup: cannot source scripts/platform.sh"
platform_in_fence || fail "setup: refusing: this is not a disposable machine (PFM_DEV_FENCE=1 in a container, or a GitHub-hosted runner)"
for tool in git go tar zsh tmux herdr sqlite3; do
  command -v "$tool" >/dev/null 2>&1 || fail "setup: $tool is not on PATH"
done
case "${SHELL:-}" in
  */zsh) ;;
  *) fail "setup: the owner's login shell is '${SHELL:-}', not zsh, the shell this script configures" ;;
esac
# The tree may belong to another user (CI's runner mounts its checkout into the machine), and git
# refuses to read a repository someone else owns: the two fixtures built here carry no VCS stamp.
export GOFLAGS="${GOFLAGS:+$GOFLAGS }-buildvcs=false"

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

# 4. Herdr: its config names no shell, so pfm's install wires its own wrapper, as on any owner's machine.
install -d -m 0700 "$HOME/.config/herdr" || fail "herdr: cannot create $HOME/.config/herdr"
cat >"$HOME/.config/herdr/config.toml" <<TOML
onboarding = false

[update]
version_check = false
manifest_check = false

[terminal]
shell_mode = "non_login"

[ui.toast]
delivery = "off"

[ui.sound]
enabled = false
TOML

# 5. The repository INSTALL.md clones, and the script that clones it.
src_git() { git -C "$root" -c safe.directory='*' "$@"; }
# shellcheck source=scripts/install-snapshot.sh
. "$root/scripts/install-snapshot.sh"
version=$(cat "$root/VERSION") || fail "snapshot: cannot read $root/VERSION"
install_snapshot_mirror "$root" "$j/mirror" "${version%%-*}" || fail "$install_snapshot_why"
install_snapshot_script "$root/INSTALL.md" "$j/mirror" "$j" || fail "$install_snapshot_why"

# 6. A file the owner had before pfm, and a member of the owner's own in Claude's settings: the
# uninstall leaves both as they were.
printf 'the owner wrote this before pfm was installed\n' >"$HOME/.claude/owner-keep.txt"
printf '{\n  "ownerKeep": "the owner set this before pfm was installed"\n}\n' >"$HOME/.claude/settings.json"

# 7. The owner's project, a git repository with one commit, and the config directory of a second Claude
# account, ready for the step that adds it to pfm's roster (the roster lives in the clone the install makes).
demo="$HOME/work/demo"
rm -rf "$HOME/work/demo"
mkdir -p "$demo" "$HOME/.cc/2" || fail "project: cannot create $demo and $HOME/.cc/2"
printf '# demo\n' >"$demo/README.md"
git -C "$demo" init -q && git -C "$demo" add README.md &&
  git -C "$demo" -c user.name=owner -c user.email=owner@journey.invalid commit -q -m "the demo project" ||
  fail "project: cannot commit $demo"
printf '{"hasCompletedOnboarding":true}\n' >"$HOME/.cc/2/.claude.json"

# 8. Codex and OpenCode logged in, as after the owner's `codex login` and `opencode auth login`, so pfm
# offers both engines; and the fake engine's scenario naming the pane texts Codex and OpenCode draw
# (internal/mockengine PaneShapes: a TUI without them is refused) and the rate limits Codex's app-server
# answers (pfm install's hook trust asks it), exported in every login.
install -d -m 0700 "$HOME/.codex" "$HOME/.local/share/opencode" || fail "engines: cannot create the Codex and OpenCode homes"
printf '{"tokens":{"access_token":"journey-codex-access","account_id":"journey-codex-account"}}\n' >"$HOME/.codex/auth.json"
printf '{"openai":{"type":"oauth","access":"journey-opencode","refresh":"journey-opencode","expires":4102444800000,"accountId":"journey-opencode"}}\n' \
  >"$HOME/.local/share/opencode/auth.json"
# The model catalogue Codex fetches on its first run, which pfm checks a --model against.
printf '{"fetched_at":"2026-11-12T00:00:00Z","models":[{"slug":"gpt-journey"}]}\n' >"$HOME/.codex/models_cache.json"
cat >"$j/scenario.json" <<'JSON'
{"pane":{"busy":"Working (1s · esc to interrupt) · esc interrupt","compacted":"Context compacted","composer":"FIXTURE-OPENCODE-PROMPT"},
 "rate_limits":{"primary":{"usedPercent":12,"windowDurationMins":300,"resetsAt":4102444800},"secondary":{"usedPercent":34,"windowDurationMins":10080,"resetsAt":4102444800},"planType":"plus"}}
JSON
printf 'export MOCK_ENGINE_SCENARIO="%s"\n' "$j/scenario.json" >>"$HOME/.zshenv"
echo "prepare: PASS"
