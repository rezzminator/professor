#!/usr/bin/env bash
# setup.sh — runs INSIDE the demo fence container (up.sh drives it) in two phases:
#   tools    pfm built from the mounted checkout with the release stamp, the REAL
#            Claude Code and Codex from npm, Starship.
#   install  pfm install from the checkout (chat MCP, statusline, hooks, the
#            professor system prompt, the harvester), Claude Code's first-run
#            state per seat, the demo projects (/work/demo is a `pfm init`
#            Professor project — its guard hook is the "rules that bite" demo),
#            and the headless kit.
# Run from /tmp: /worktree's .git file points at a host path the container cannot
# resolve, so any git command there fails.
#
# BROKEN STATE: any failing step exits non-zero with its own message (set -e).
# `install` requires the seats' credentials to be in place already (creds.sh):
# pfm's config validation refuses a Codex home without auth.json, and the
# harness would boot into a login screen instead of a chat.
set -euo pipefail
export PATH="$HOME/.local/bin:$PATH"
SRC=/worktree
HERE="$SRC/infra/demo"
CONFIG="$HOME/.config/pfm/pfm.config.json"
PROJECTS=(atlas lumen orbit harvester)
phase="${1:-}"

expand() { case "$1" in "~"*) echo "$HOME${1#\~}";; *) echo "$1";; esac; }

case "$phase" in
tools)
  mkdir -p "$HOME/.local/bin"
  (cd "$SRC/pfm" && go build -ldflags "-X main.version=$(cat ../VERSION)" -o "$HOME/.local/bin/pfm" ./cmd/pfm)
  command -v claude >/dev/null && command -v codex >/dev/null || npm install -g @anthropic-ai/claude-code @openai/codex >/dev/null
  command -v opencode >/dev/null || npm install -g opencode-ai >/dev/null
  command -v starship >/dev/null || curl -fsSL https://starship.rs/install.sh | sh -s -- -y >/dev/null
  # The VS Code workspace opens its terminals in /work/express; the directory
  # exists from the first minute so a terminal opens before the Express step
  # has cloned into it (git clone accepts an empty directory).
  mkdir -p /work/express
  echo "tools: pfm $(pfm version 2>/dev/null | head -1) · claude $(claude --version) · codex $(codex --version) · opencode $(opencode --version) · $(starship --version | head -1)"
  ;;
install)
  [ -f "$CONFIG" ] || { echo "setup: $CONFIG missing — up.sh writes it before this phase" >&2; exit 1; }
  # 1. Every seat needs a settings.json for pfm to wire, and a credential to be real.
  while read -r dir; do
    dir="$(expand "$dir")"; mkdir -p "$dir"
    [ -f "$dir/settings.json" ] || echo '{}' > "$dir/settings.json"
    [ -s "$dir/.credentials.json" ] || { echo "setup: NOTE — seat $dir has no .credentials.json — it logs in inside the container (up.sh --login) or creds.sh copies it; up.sh probes every seat before the interview" >&2; true; }
  done < <(jq -r '.accounts[].configDir' "$CONFIG")
  while read -r home; do
    home="$(expand "$home")"; mkdir -p "$home"
    [ -s "$home/auth.json" ] || { echo "setup: codex home $home has no auth.json — run creds.sh first" >&2; exit 1; }
  done < <(jq -r '.codex.homes[].home' "$CONFIG")
  [ -s "$HOME/.local/share/opencode/auth.json" ] || { echo "setup: OpenCode has no auth.json under ~/.local/share/opencode — run creds.sh --opencode first" >&2; exit 1; }
  #    Seats share one transcript store, as the host does (~/.cc/N/projects → ~/.claude/projects):
  #    /reload --account N resumes the SAME transcript under the new seat, and a seat
  #    with its own projects/ dir would resume nothing and die at birth.
  primary="$(expand "$(jq -r '.accounts[0].configDir' "$CONFIG")")"; mkdir -p "$primary/projects"
  while read -r dir; do
    dir="$(expand "$dir")"; [ "$dir" = "$primary" ] && continue
    [ -L "$dir/projects" ] || { rm -rf "$dir/projects"; ln -s "$primary/projects" "$dir/projects"; }
  done < <(jq -r '.accounts[].configDir' "$CONFIG")
  # 2. pfm install exactly as a user runs it from the clone, Claude Code themes included: the
  #    seats wear the professor palettes the presenter's machines wear.
  #    ~/.professor is where pfm expects the blueprint clone (pfm update check,
  #    the global fan-out); on a real host it IS the checkout, here it links to the mount.
  [ -e "$HOME/.professor" ] || ln -s "$SRC" "$HOME/.professor"
  #    The harvester's Python sidecar can refuse a platform (a pinned CUDA wheel on
  #    linux-arm64 did); the demo then installs without it and SAYS so — the deck's
  #    harvester section is an animation, the fleet does not depend on the sidecar.
  if ! (cd "$SRC" && pfm install --yes); then
    echo "setup: WARNING — pfm install with the harvester failed (see above); retrying with --skip-harvest: the harvester MCP is NOT available in this container" >&2
    (cd "$SRC" && pfm install --yes --skip-harvest)
  fi
  "$HERE/daemon.sh" # no init system in the fence: the MCP HTTP daemon runs from here
  # 3. Claude Code's first-run state: onboarding done, every demo project trusted, so no
  #    dialog stands between a spawn and a live chat. Merged, never overwritten — pfm
  #    install may already have written mcpServers into the same file.
  trust="$(printf '%s\n' "${PROJECTS[@]}" express | jq -R '{key: ("/work/" + .), value: {hasTrustDialogAccepted: true}}' | jq -s 'from_entries')"
  #    The bypass-permissions warning is a second first-run screen whose default is
  #    "No, exit" — a spawn's typed prompt dies in it. Accepting it once writes
  #    skipDangerousModePermissionPrompt into settings.json; that is what is seeded.
  #    The seat's Claude Code theme follows its medal (🥇 gold · 🥈 silver · 🥉 bronze):
  #    the professor-* overlays pfm install placed in the primary seat's themes/, which
  #    every other seat reaches through a symlink, as on the host.
  while IFS=$'\t' read -r dir emoji; do
    dir="$(expand "$dir")"; f="$dir/.claude.json"
    [ -s "$f" ] || echo '{}' > "$f"
    #    Claude Code's first run drops theme:"dark" into .claude.json, and that key
    #    beats settings.json's custom theme — dropped, as the presenter's host has it.
    jq --argjson trust "$trust" '. + {hasCompletedOnboarding: true} | del(.theme) | .projects = ((.projects // {}) + $trust)' "$f" > "$f.tmp" && mv "$f.tmp" "$f"
    case "$emoji" in 🥇) theme=professor-gold ;; 🥈) theme=professor-silver ;; 🥉) theme=professor-bronze ;; *) theme=tokyo-night ;; esac
    #    The presenter's own Claude Code look and habits: fullscreen TUI (the
    #    composer sits at the bottom, output above), low default effort, no
    #    attribution lines, no feedback drafts, no workflow-usage nag.
    jq --arg t "custom:$theme" '. + {skipDangerousModePermissionPrompt: true, skipAutoPermissionPrompt: true, skipWorkflowUsageWarning: true, theme: $t, tui: "fullscreen", effortLevel: "low", feedbackDrafts: "off", attribution: {commit: "", pr: "", sessionUrl: false}}' "$dir/settings.json" > "$dir/settings.json.tmp" && mv "$dir/settings.json.tmp" "$dir/settings.json"
    [ "$dir" = "$primary" ] || [ -e "$dir/themes" ] || ln -s "$primary/themes" "$dir/themes"
  done < <(jq -r '.accounts[] | "\(.configDir)\t\(.emoji)"' "$CONFIG")
  # 4. The shell: Starship's Catppuccin powerline after pfm's shim.
  [ -f "$HOME/.config/starship.toml" ] || starship preset catppuccin-powerline -o "$HOME/.config/starship.toml"
  grep -q 'starship init zsh' "$HOME/.zshrc" 2>/dev/null || echo 'eval "$(starship init zsh)"' >> "$HOME/.zshrc"
  # The fence runs as root, and Claude Code refuses --dangerously-skip-permissions
  # under root unless IS_SANDBOX=1 says the machine is disposable — which this one
  # is. Every spawn inherits it from the shell (or from the script that spawns).
  grep -q 'IS_SANDBOX' "$HOME/.zshrc" || echo 'export IS_SANDBOX=1' >> "$HOME/.zshrc"
  grep -q 'demo/aliases.zsh' "$HOME/.zshrc" || echo 'source /worktree/infra/demo/aliases.zsh' >> "$HOME/.zshrc"
  # 5. Projects: plain invented repos on develop for the fleet to live in.
  git config --global user.name demo
  git config --global user.email demo@example.invalid
  git config --global init.defaultBranch main
  for p in "${PROJECTS[@]}"; do
    mkdir -p "/work/$p"
    if [ ! -d "/work/$p/.git" ]; then
      (cd "/work/$p" && git init -q && git commit -q --allow-empty -m init && git checkout -q -b develop)
    fi
  done
  # 6. OpenCode: one ChatGPT-authenticated home; pfm sees it once opencode.db exists,
  #    which the first run below creates — and that run proves the copied auth is live.
  mkdir -p "$HOME/.config/opencode"
  cat > "$HOME/.config/opencode/opencode.jsonc" <<EOF
{
  "\$schema": "https://opencode.ai/config.json",
  "model": "openai/gpt-5.6-luna",
  "mcp": {
    "chat": { "type": "local", "command": ["$HOME/.local/bin/pfm", "mcp", "chat", "serve"], "enabled": true }
  }
}
EOF
  echo '{"$schema": "https://opencode.ai/tui.json", "theme": "tokyonight"}' > "$HOME/.config/opencode/tui.json"
  if [ ! -f "$HOME/.local/share/opencode/opencode.db" ]; then
    # The probe leaves one resumable OpenCode row under a name OpenCode invents
    # ("Ready Request" one day, "Ready instruction request" the next), so the
    # row to hide is found by difference, not by name.
    ox_rows() { "$HOME/.local/bin/pfm" ls --tsv 2>/dev/null | awk -F'\t' '$1 == "resume-opencode" {print $2}' | sort; }
    before="$(ox_rows)"
    (cd "/work/${PROJECTS[0]}" && timeout 180 opencode run "reply with one word: ready" | tail -1 | grep -qi ready) \
      || { echo "setup: OpenCode's first run did not answer 'ready' — its ChatGPT auth is not live" >&2; exit 1; }
    comm -13 <(printf '%s\n' "$before") <(ox_rows) | xargs -r -n1 "$HOME/.local/bin/pfm" chat kill >/dev/null
  fi
  mkdir -p "$HOME/headless" && cp "$HERE"/headless/* "$HOME/headless/"
  echo "install: $(jq -r '.accounts | length' "$CONFIG") Claude seats + Codex + OpenCode, $(ls /work | wc -l | tr -d ' ') projects, headless kit in ~/headless"
  ;;
*) echo "usage: setup.sh tools|install" >&2; exit 2 ;;
esac
