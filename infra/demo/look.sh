#!/usr/bin/env bash
# look.sh — runs INSIDE the demo fence container after setup.sh install: the
# presenter's own terminal layer, so the fence looks like the machines the room
# has seen — the Sonar palette on tmux's status bar, the same prompt, the same
# zsh (zinit plugins, fzf, zoxide, eza, bat), the same CLI colours.
#
# It expects the host's theme directory at ~/.config/code-theme (up.sh copies
# it from the presenter's Mac: palette.tmux, tmux-theme.conf, shell-theme.zsh,
# starship.toml, bat/sonar.tmTheme). With no theme directory the shell and tools
# are still set up and the theme steps are reported as SKIPPED by name.
# Idempotent: every step converges. Live tmux servers (one per chat) are
# re-sourced at the end so running chats get the bar without a restart.
#
# BROKEN STATE: a tool that will not install is named and the exit is 1; a
# missing theme file is named as SKIPPED (not an error: a host without the
# theme is a valid host); the closing line lists what is wired.
set -uo pipefail
export PATH="$HOME/.local/bin:$PATH"
THEME="$HOME/.config/code-theme"
failed=0
say() { printf 'look: %s\n' "$*"; }

# 1. Tools: bat, zoxide from apt; fzf and eza from their GitHub releases — Ubuntu
#    24.04's fzf (0.44) predates `fzf --zsh` and eza is not packaged at all.
need=()
command -v zoxide >/dev/null || need+=("zoxide")
command -v bat >/dev/null || command -v batcat >/dev/null || need+=("bat")
if [ "${#need[@]}" -gt 0 ]; then
  apt-get update -qq >/dev/null 2>&1
  apt-get install -y -qq --no-install-recommends "${need[@]}" >/dev/null 2>&1 || { say "apt could not install: ${need[*]}"; failed=1; }
fi
# Ubuntu ships bat as batcat; the theme and BAT_THEME expect `bat`.
if ! command -v bat >/dev/null && command -v batcat >/dev/null; then ln -sf "$(command -v batcat)" "$HOME/.local/bin/bat"; fi
if ! fzf --zsh >/dev/null 2>&1; then
  case "$(uname -m)" in aarch64|arm64) fa=linux_arm64 ;; *) fa=linux_amd64 ;; esac
  fv="$(curl -fsSL https://api.github.com/repos/junegunn/fzf/releases/latest | jq -r .tag_name | sed 's/^v//')"
  if [ -n "$fv" ] && curl -fsSL "https://github.com/junegunn/fzf/releases/download/v${fv}/fzf-${fv}-${fa}.tar.gz" | tar -xz -C "$HOME/.local/bin" fzf 2>/dev/null; then say "fzf $fv installed ($fa)"; else say "fzf release download failed ($fa) — the shell skips fzf keybindings"; fi
fi
if ! command -v eza >/dev/null; then
  case "$(uname -m)" in aarch64|arm64) tri=aarch64-unknown-linux-gnu ;; *) tri=x86_64-unknown-linux-gnu ;; esac
  if curl -fsSL "https://github.com/eza-community/eza/releases/latest/download/eza_${tri}.tar.gz" | tar -xz -C "$HOME/.local/bin" ./eza 2>/dev/null; then say "eza installed ($tri)"; else say "eza release download failed ($tri) — ls stays plain"; fi
fi

# 2. zinit — the plugin manager the devbox shell uses (cloned once).
ZINIT_HOME="$HOME/.local/share/zinit/zinit.git"
if [ ! -f "$ZINIT_HOME/zinit.zsh" ]; then
  mkdir -p "$(dirname "$ZINIT_HOME")"
  git clone -q --depth=1 https://github.com/zdharma-continuum/zinit "$ZINIT_HOME" || { say "zinit clone failed"; failed=1; }
fi

# 3. ~/.zshrc — the devbox shell, minus what a fence has no use for (mise, direnv,
#    the version pin). pfm's own shim line is preserved verbatim: pfm install owns it.
shim="$(grep -F 'pfm.zsh' "$HOME/.zshrc" 2>/dev/null | head -1)"
[ -n "$shim" ] || shim='[[ -r "$HOME/.local/share/pfm/install/shim/pfm.zsh" ]] && source "$HOME/.local/share/pfm/install/shim/pfm.zsh"'
cat > "$HOME/.zshrc" <<ZRC
# ~/.zshrc — the demo fence's shell (infra/demo/look.sh writes this file; edit there).
export PATH="\$HOME/.local/bin:\$PATH"
export IS_SANDBOX=1   # root fence: Claude Code refuses the bypass flag under root without it
alias e="exit"

# ── Zinit + plugins (turbo-loaded) ──
ZINIT_HOME="\$HOME/.local/share/zinit/zinit.git"
if [[ -f \$ZINIT_HOME/zinit.zsh ]]; then
  source "\$ZINIT_HOME/zinit.zsh"
  autoload -Uz _zinit
  (( \${+_comps} )) && _comps[zinit]=_zinit
  zinit wait lucid light-mode for \\
    atinit"zicompinit; zicdreplay" \\
      zdharma-continuum/fast-syntax-highlighting \\
    atload"_zsh_autosuggest_start" \\
      zsh-users/zsh-autosuggestions \\
    blockf atpull"zinit creinstall -q ." \\
      zsh-users/zsh-completions \\
    atload"bindkey '^[[A' history-substring-search-up; bindkey '^[[B' history-substring-search-down" \\
      zsh-users/zsh-history-substring-search \\
    Aloxaf/fzf-tab
fi

# ── History ──
HISTFILE=~/.zsh_history
HISTSIZE=50000
SAVEHIST=50000
setopt SHARE_HISTORY HIST_IGNORE_DUPS HIST_IGNORE_SPACE HIST_REDUCE_BLANKS

# ── fzf · zoxide · starship ──
fzf --zsh >/dev/null 2>&1 && source <(fzf --zsh)
command -v zoxide >/dev/null && eval "\$(zoxide init zsh)"
eval "\$(starship init zsh)"

# ── word-motion keys (Option/Ctrl + arrows, Ctrl+Backspace) ──
bindkey '^[[1;3C' forward-word
bindkey '^[[1;3D' backward-word
bindkey '^[[1;5C' forward-word
bindkey '^[[1;5D' backward-word
bindkey '^H'      backward-kill-word
bindkey '^[[3;5~' kill-word
bindkey '^[[3~'   delete-char
bindkey '^[[H'    beginning-of-line
bindkey '^[[F'    end-of-line

# ── the fleet: pfm's shell shim (pfm install owns this line) ──
$shim

# ── Sonar CLI colours (eza, bat, fzf) and the demo aliases ──
[[ -r "$THEME/shell-theme.zsh" ]] && source "$THEME/shell-theme.zsh"
source /worktree/infra/demo/aliases.zsh
ZRC

# 4. Theme: starship pinned to the devbox accent, tmux status bar, bat theme.
wired=(zsh tools)
if [ -d "$THEME" ]; then
  if [ -f "$THEME/starship.toml" ]; then
    sed -i 's/^palette = .*/palette = "devbox"/' "$THEME/starship.toml"
    rm -f "$HOME/.config/starship.toml"; ln -s "$THEME/starship.toml" "$HOME/.config/starship.toml"; wired+=(starship)
  else say "SKIPPED starship: $THEME/starship.toml missing"; fi
  if [ -f "$THEME/tmux-theme.conf" ] && [ -f "$THEME/palette.tmux" ]; then
    # The theme's unknown-host branch paints RED ("treat an unknown box as
    # production"); this box is the demo, so that branch becomes magenta "demo".
    python3 - "$THEME/tmux-theme.conf" <<'PY'
import sys,re
p=sys.argv[1]; s=open(p).read()
i=s.index('%else'); j=s.index('%endif',i)
block=s[i:j].replace('#{@acc_prod}','#{@acc_devbox}').replace('"#{host_short}"','"demo"')
open(p,'w').write(s[:i]+block+s[j:])
PY
    cat > "$HOME/.tmux.conf" <<'TMUXRC'
# ~/.tmux.conf — the demo fence (infra/demo/look.sh writes this file; edit there).
set -g default-terminal "tmux-256color"
set -ga terminal-overrides ",*256col*:Tc"
set -g mouse on
set -g history-limit 100000
set -g base-index 1
setw -g pane-base-index 1
set -g renumber-windows on
set -g escape-time 0
set -g focus-events on
setw -g mode-keys vi
# Forward the pane title (the chat's name) to the outer terminal title, so a VS
# Code tab with tabs.title "${sequence}" is named after the chat inside it.
# Skipped on pfm's own chat servers (cc-/cx-/ox- sockets): pfm sets their titles.
if -F '#{m/r:/(cc|cx|ox)-[^/]*$,#{socket_path}}' '' {
  set -g set-titles on
  set -g set-titles-string "#{s/^[^ ]* //:pane_title}"
}
bind r source-file ~/.tmux.conf \; display "tmux.conf reloaded ✓"
bind | split-window -h -c "#{pane_current_path}"
bind - split-window -v -c "#{pane_current_path}"
# Sonar status bar, panes, copy-mode — the presenter's theme (code-theme/).
source-file ~/.config/code-theme/tmux-theme.conf
TMUXRC
    wired+=(tmux)
  else say "SKIPPED tmux theme: tmux-theme.conf or palette.tmux missing in $THEME"; fi
  if [ -f "$THEME/bat/sonar.tmTheme" ] && command -v bat >/dev/null; then
    bt="$(bat --config-dir)/themes/sonar.tmTheme"; mkdir -p "$(dirname "$bt")"; cp "$THEME/bat/sonar.tmTheme" "$bt"; bat cache --build >/dev/null 2>&1 && wired+=(bat)
  else say "SKIPPED bat theme: sonar.tmTheme or bat missing"; fi
  [ -f "$THEME/shell-theme.zsh" ] && wired+=(cli-colours) || say "SKIPPED cli colours: shell-theme.zsh missing"
else
  say "SKIPPED theme: $THEME is absent (up.sh copies it from a host that has ~/.config/code-theme)"
fi

# 4b. root's login shell is zsh: VS Code, tmux and docker exec all fall back to
#     $SHELL, and a fresh fence's is bash.
chsh -s /usr/bin/zsh root >/dev/null 2>&1 || usermod -s /usr/bin/zsh root 2>/dev/null || say "could not set root's login shell to zsh"

# 5. Every running tmux server — one per chat — takes the bar now.
n=0
for sock in /tmp/tmux-"$(id -u)"/*; do
  [ -S "$sock" ] || continue
  tmux -S "$sock" source-file "$HOME/.tmux.conf" >/dev/null 2>&1 && n=$((n + 1))
done
say "wired: ${wired[*]} · live tmux servers re-sourced: $n"
[ "$failed" -eq 0 ]
