#!/usr/bin/env bash
# Self-test for scripts/install-snapshot.sh: a fixture repository with a tracked, an untracked, an
# ignored and a deleted file becomes a mirror holding exactly the tree a clone would, tagged at its
# release; INSTALL.md's marked blocks become a script cloning that mirror; each refusal names its cause.
set -uo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SHTEST_TAG=install-snapshot-test
source "$ROOT/scripts/shtest.sh"
shtest_isolate_host
source "$ROOT/scripts/install-snapshot.sh"

same() { # same LABEL WANT GOT
  if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "want [$2]" "got  [$3]"; fi
}

src="$T/src" mirror="$T/mirror"
mkdir -p "$src" "$mirror"
git -C "$src" init -q && git -C "$src" config user.email t@invalid && git -C "$src" config user.name t
printf 'tracked\n' >"$src/tracked.txt"; printf 'gone\n' >"$src/deleted.txt"; printf 'ignored.txt\n' >"$src/.gitignore"
printf '0.1.0-alpha\n' >"$src/VERSION"
git -C "$src" add -A && git -C "$src" commit -q -m fixture
rm "$src/deleted.txt"; printf 'untracked\n' >"$src/untracked.txt"; printf 'no\n' >"$src/ignored.txt"
src_git() { git -C "$src" "$@"; }

install_snapshot_mirror "$src" "$mirror" 9.8.7; rc=$?
same 'the mirror is made' 0 "$rc"
same 'the mirror holds the tracked and untracked files, never the deleted or ignored one' \
  '.gitignore tracked.txt untracked.txt VERSION' "$(git -C "$mirror" ls-files | sort -f | tr '\n' ' ' | sed 's/ $//')"
same 'VERSION reads the release' 9.8.7 "$(cat "$mirror/VERSION")"
same 'the release tag names the mirror head' "$(git -C "$mirror" rev-parse HEAD)" "$(git -C "$mirror" rev-parse 'v9.8.7^{commit}')"

install_snapshot_why=''
install_snapshot_mirror "$src" "$T/absent" 9.8.7 2>/dev/null; rc=$?
same 'a missing mirror directory is refused' "1|snapshot: cannot copy the working tree into $T/absent" "$rc|$install_snapshot_why"

md="$T/INSTALL.md"
cat >"$md" <<'MD'
Prose.
<!-- fresh-install -->
```bash
git clone "https://github.com/${REPO}.git" "$HOME/.professor"
```
Unmarked:
```bash
echo never
```
MD
install_snapshot_script "$md" "$mirror" "$T"; rc=$?
same 'the marked block becomes a script cloning the mirror' \
  "0|git clone \"file://$mirror\" \"\$HOME/.professor\"" "$rc|$(cat "$T/install.sh")"

printf 'Prose only.\n' >"$md"
install_snapshot_script "$md" "$mirror" "$T"; rc=$?
same 'no marked block is refused' '1|extract: NO MARKED BLOCK' "$rc|$install_snapshot_why"

printf '<!-- fresh-install -->\n```bash\ngit clone "https://github.com/${REPO}.git"\ncurl https://github.com/x\n```\n' >"$md"
install_snapshot_script "$md" "$mirror" "$T" >/dev/null; rc=$?
same 'a block still reaching GitHub is refused' \
  '1|extract: a marked block still reaches GitHub after the repository swap' "$rc|$install_snapshot_why"

shtest_end
