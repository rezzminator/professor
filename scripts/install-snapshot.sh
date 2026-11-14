# install-snapshot.sh — the tree under test as the repository INSTALL.md clones, and INSTALL.md's
# marked blocks as the script that clones it. Sourced, Bash 3.2-safe, defines functions only.
#   . "{root}/scripts/install-snapshot.sh"
# Callers: scripts/fresh-install-test.sh (a fresh HOME) and infra/fence/journey/prepare.sh (the
# journey machine's owner).
#
# BROKEN STATE: each function returns 1 with its cause in install_snapshot_why, the caller's
# failure text; it never returns 0 over a partial mirror or script.

# install_snapshot_mirror ROOT MIRROR RELEASE: copies ROOT's tracked and untracked files (no
# submodule checkouts, as a plain clone has none) into the existing directory MIRROR, sets VERSION
# to RELEASE, commits and tags vRELEASE. ROOT is read through the caller's src_git function.
install_snapshot_mirror() {
  src_git ls-files -z -co --exclude-standard |
    (
      cd "$1" || exit 1
      while IFS= read -r -d '' path; do
        # A directory entry is a submodule checkout; a deleted file is gone.
        if [ -L "$path" ] || [ -f "$path" ]; then printf '%s\0' "$path"; fi
      done
    ) |
    (cd "$1" && tar -cf - --null -T -) |
    (cd "$2" && tar -xf -) || {
    install_snapshot_why="snapshot: cannot copy the working tree into $2"
    return 1
  }
  printf '%s\n' "$3" >"$2/VERSION"
  {
    _install_snapshot_git "$2" init -q &&
      _install_snapshot_git "$2" add -A -f &&
      _install_snapshot_git "$2" commit -q -m "release v$3" &&
      _install_snapshot_git "$2" tag "v$3"
  } || {
    install_snapshot_why="snapshot: cannot commit and tag v$3 in $2"
    return 1
  }
}

_install_snapshot_git() { # _install_snapshot_git MIRROR git-args...: the mirror's own identity
  _install_snapshot_dir="$1"
  shift
  git -C "$_install_snapshot_dir" -c user.name=fresh-install -c user.email=fresh-install@invalid \
    -c commit.gpgsign=false -c tag.gpgsign=false "$@"
}

# install_snapshot_script INSTALL_MD MIRROR OUT: writes OUT/install-blocks.sh, every INSTALL.md
# block marked `<!-- fresh-install` as written, and OUT/install.sh, the same with
# https://github.com/${REPO}.git swapped for file://MIRROR.
install_snapshot_script() {
  awk '
    /^<!-- fresh-install/ { marked = 1; next }
    marked && /^```bash$/ { inside = 1; marked = 0; blocks++; next }
    marked { print "MARKER WITHOUT A BASH BLOCK"; exit 1 }
    inside && /^```$/ { inside = 0; next }
    inside { print }
    END { if (blocks == 0) { print "NO MARKED BLOCK"; exit 1 } }
  ' "$1" >"$3/install-blocks.sh" || {
    install_snapshot_why="extract: $(tail -1 "$3/install-blocks.sh")"
    return 1
  }
  # shellcheck disable=SC2016 # the literal INSTALL.md writes
  _install_snapshot_url='https://github.com/${REPO}.git'
  grep -qF "$_install_snapshot_url" "$3/install-blocks.sh" || {
    install_snapshot_why="extract: the marked blocks never name $_install_snapshot_url"
    return 1
  }
  sed "s#https://github.com/\${REPO}.git#file://$2#g" "$3/install-blocks.sh" >"$3/install.sh"
  if grep -n 'github.com' "$3/install.sh"; then
    install_snapshot_why="extract: a marked block still reaches GitHub after the repository swap"
    return 1
  fi
}
