#!/usr/bin/env bash
# The clock check of this repo's shell tests: no shell test waits on, bounds by or measures the real clock.
# It scans, in the repository at --root (default: this checkout), every tracked or untracked, not ignored, file outside plugins/ that
# is a shell test (`*_test.sh`), a shell file under a `tests/` folder (the fakes, fixtures and helpers the
# tests source or run), the repo root's other shell-test naming `scripts/test-*.sh`, the harness family
# `scripts/shtest*.sh`, the fresh-install test `scripts/fresh-install-test.sh` or a gate fixture
# `infra/fence/gate-fixtures/*.sh` — all but scripts/shtest-wait.sh, the
# one wait door, which holds the only real-clock pause by being the door. There is no list of exceptions.
# Comment lines are skipped; a word a test must hold as its subject lives in a testdata file (no `.sh`,
# no `#!`), which is no shell file and so is never scanned.
#   bash scripts/shtest-clock-check.sh [--root DIR]
# Prints one `{file}:{line} {spelling}` per site, then `CHECK shtest-clock PASS|FAIL {n} sites in {m} of {N} files`.
# Exit 0 PASS, 1 FAIL, 2 ERROR: could not look (no git, no repository, no file in scope, awk missing),
# printed as an `ERROR shtest-clock …` line, never as zero sites. The spellings: scripts/shtest-clock-check.tsv.
set -uo pipefail
root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
own_checkout=1 # this checkout is listed through pfm/scripts/repo-git.sh, which reads a worktree's index inside the fence
while [ $# -gt 0 ]; do
  case "$1" in
    --root) root="${2:?--root needs a directory}"; own_checkout=0; shift 2 ;;
    *) echo "usage: shtest-clock-check.sh [--root DIR]" >&2; exit 2 ;;
  esac
done
door=scripts/shtest-wait.sh
table="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)/shtest-clock-check.tsv"
[ -r "$table" ] || { echo "ERROR shtest-clock could not look: the spelling table $table is unreadable"; exit 2; }
err() { echo "ERROR shtest-clock $*"; exit 2; }
command -v git >/dev/null 2>&1 || err "could not look: git is missing"
command -v awk >/dev/null 2>&1 || err "could not look: awk is missing"
if [ "$own_checkout" = 1 ]; then
  # shellcheck source=pfm/scripts/repo-git.sh
  . "$root/pfm/scripts/repo-git.sh" || err "could not look: $root/pfm/scripts/repo-git.sh did not load"
  listing="$(cd "$root" && repo_git ls-files --cached --others --exclude-standard 2>&1)"
else
  listing="$(git -C "$root" ls-files --cached --others --exclude-standard 2>&1)"
fi || err "could not look: git ls-files in $root failed: ${listing%%$'\n'*}"

files=()
while IFS= read -r f; do
  [ -n "$f" ] || continue
  case "$f" in plugins/*) continue ;; esac
  [ "$f" != "$door" ] || continue
  [ -f "$root/$f" ] || continue # a tracked path deleted in the working tree
  case "$f" in
    *_test.sh | scripts/shtest*.sh | scripts/test-*.sh | scripts/fresh-install-test.sh | infra/fence/gate-fixtures/*.sh)
      files+=("$f"); continue ;;
  esac
  case "/$f" in
    */tests/*)
      case "$f" in
        *.sh | *.bash) files+=("$f") ;;
        *) IFS= read -r first < "$root/$f" 2>/dev/null || first=''
           case "$first" in '#!'*sh*) files+=("$f") ;; esac ;;
      esac ;;
  esac
done <<< "$listing"
[ "${#files[@]}" -gt 0 ] || err "could not look: no shell test file in scope under $root"

# The banned spellings, closed world, are the rows of the table beside this script (its first file for awk).
out="$(cd "$root" && awk -F '\t' '
BEGIN { B = "(^|[^-_.A-Za-z0-9]|\\\\[nt])"; E = "([^-_.A-Za-z0-9]|$)"; files = ARGC - 2 }
FILENAME == ARGV[1] {
  if ($0 ~ /^#/ || $0 == "") next
  if (NF != 2) { printf "the spelling table row %d is not {spelling}<TAB>{regex}\n", FNR; exit 3 }
  r = $2; gsub(/<B>/, B, r); gsub(/<E>/, E, r)
  name[++n] = $1; re[n] = r; next
}
FNR == 1 && n == 0 { print "the spelling table holds no row"; exit 3 }
/^[[:space:]]*#/ { next }
{
  hit = 0
  for (i = 1; i <= n; i++) if ($0 ~ re[i]) { printf "%s:%d %s\n", FILENAME, FNR, name[i]; sites++; hit = 1 }
  if (hit && !(FILENAME in seen)) { seen[FILENAME] = 1; bad++ }
}
END { printf "TOTAL %d %d %d %d\n", sites + 0, bad + 0, files + 0, n + 0 }
' "$table" "${files[@]}" 2>&1)" || err "could not look: awk failed: ${out%%$'\n'*}"
total="${out##*$'\n'}"
case "$total" in TOTAL\ *) ;; *) err "could not look: the scan printed no total" ;; esac
read -r _ sites bad scanned rules <<< "$total"
[ "${rules:-0}" -gt 0 ] || err "could not look: the spelling table $table holds no row"
[ "$scanned" -eq "${#files[@]}" ] || err "could not look: scanned $scanned of ${#files[@]} files"
[ "$sites" -eq 0 ] || printf '%s\n' "${out%$'\n'*}"
if [ "$sites" -eq 0 ]; then
  echo "CHECK shtest-clock PASS 0 sites in 0 of $scanned files"
else
  echo "CHECK shtest-clock FAIL $sites sites in $bad of $scanned files"
  exit 1
fi
