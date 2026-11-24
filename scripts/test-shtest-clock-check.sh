#!/usr/bin/env bash
# Self-test of scripts/shtest-clock-check.sh, the clock check of this repo's shell tests, against a
# fixture repository: each banned spelling planted alone in a scanned file fails it, named exactly
# (scripts/testdata/shtest-clock-check/lines.tsv holds the planted lines, as the spellings are the
# subject); each clean line passes; the scope takes shell tests, tests/ shell files, the root's
# scripts/test-*.sh, the harness, the fresh-install test and the gate fixtures, untracked ones too; never plugins/, the door, pfm/scripts/test-*.sh
# (tools with _test.sh twins), an ignored file or a non-shell file; "could not look" is an ERROR, rc 2.
set -uo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SHTEST_TAG=shtest-clock-check-test
# shellcheck source=scripts/shtest.sh
source "$ROOT/scripts/shtest.sh"
shtest_isolate_host
CHECK="$ROOT/scripts/shtest-clock-check.sh"
LINES="$ROOT/scripts/testdata/shtest-clock-check/lines.tsv"
repo="$T/repo"
git init -q "$repo" || { echo "test-shtest-clock-check: git init failed" >&2; exit 2; }

track() { # track <path> <line>: writes and stages one file of the fixture repository
  mkdir -p "$repo/$(dirname -- "$1")" && printf '%s\n' "$2" > "$repo/$1" && git -C "$repo" add -- "$1"
}
untrack() { git -C "$repo" rm -q --cached -- "$1" && rm -f -- "$repo/$1"; }
check() { # check <name> <expected rc> <expected output>
  local out rc
  out="$(bash "$CHECK" --root "$repo" 2>&1)"; rc=$?
  if [ "$rc" = "$2" ] && [ "$out" = "$3" ]; then ok "$1"
  else bad "$1" "expected rc $2:" "$3" "actual rc $rc:" "$out" "check: $CHECK --root $repo"; fi
}

# Could not look: no file in scope, and a directory that is no repository.
check 'an empty repository is ERROR, never zero sites' 2 "ERROR shtest-clock could not look: no shell test file in scope under $repo"
mkdir -p "$T/norepo"
out="$(bash "$CHECK" --root "$T/norepo" 2>&1)"; rc=$?
case "$out" in
  "ERROR shtest-clock could not look: git ls-files in $T/norepo failed: "*) [ "$rc" = 2 ] && ok 'a directory outside git is ERROR rc 2' || bad 'a directory outside git is ERROR rc 2' "rc $rc" "$out" ;;
  *) bad 'a directory outside git is ERROR rc 2' "expected: ERROR shtest-clock could not look: git ls-files in $T/norepo failed: …" "actual rc $rc: $out" ;;
esac

# The spelling table beside the check: missing, empty or malformed, it is ERROR rc 2.
mkdir -p "$T/lone"; cp "$CHECK" "$T/lone/shtest-clock-check.sh"
track infra/fence/tests/clean_test.sh 'echo clean'
table_case() { # table_case <name> <expected output>: runs the lone copy of the check against the fixture repository
  local out rc
  out="$(bash "$T/lone/shtest-clock-check.sh" --root "$repo" 2>&1)"; rc=$?
  if [ "$rc" = 2 ] && [ "$out" = "$2" ]; then ok "$1"; else bad "$1" "expected rc 2: $2" "actual rc $rc: $out"; fi
}
table_case 'a missing spelling table is ERROR' "ERROR shtest-clock could not look: the spelling table $T/lone/shtest-clock-check.tsv is unreadable"
printf '# only a comment\n' > "$T/lone/shtest-clock-check.tsv"
table_case 'a spelling table with no row is ERROR' 'ERROR shtest-clock could not look: awk failed: the spelling table holds no row'
printf 'sleep without a regex\n' > "$T/lone/shtest-clock-check.tsv"
table_case 'a spelling table row without its regex is ERROR' 'ERROR shtest-clock could not look: awk failed: the spelling table row 1 is not {spelling}<TAB>{regex}'

# Each planted line alone in one scanned file: a banned spelling fails, named; a clean line passes.
rows=0
while IFS=$'\t' read -r spelling line; do
  case "$spelling" in '#'* | '') continue ;; esac
  rows=$((rows + 1))
  track infra/fence/tests/site_test.sh "$line"
  if [ "$spelling" = - ]; then
    check "clean line passes: $line" 0 'CHECK shtest-clock PASS 0 sites in 0 of 2 files'
  else
    check "banned $spelling fails: $line" 1 "infra/fence/tests/site_test.sh:1 $spelling
CHECK shtest-clock FAIL 1 sites in 1 of 2 files"
  fi
done < "$LINES"
[ "$rows" -eq 30 ] && ok 'every planted row ran (30)' || bad 'every planted row ran (30)' "expected 30 rows from $LINES" "actual $rows"
untrack infra/fence/tests/site_test.sh

# Scope: what is scanned beside what is not, each holding the same banned line.
banned="$(awk -F '\t' '$1 == "sleep" { print $2; exit }' "$LINES")" # the planted sleep row, the same line in every file
track scripts/shtest.sh "$banned"
track infra/plugin-lab/tests/fakes/claude "#!/usr/bin/env bash"$'\n'"$banned"
track pfm/scripts/gate_test.sh "$banned"
track plugins/other/x_test.sh "$banned"
track scripts/shtest-wait.sh "$banned"
track infra/fence/tests/notes.txt "$banned"
track scripts/deploy.sh "$banned"
track scripts/test-format.sh "$banned"
track pfm/scripts/test-shard.sh "$banned"
track scripts/fresh-install-test.sh "$banned"
track infra/fence/gate-fixtures/shell-hang.sh "$banned"
track infra/fence/gate-fixtures/notes.txt "$banned"
printf '%s\n' "$banned" > "$repo/infra/fence/tests/untracked_test.sh"
printf 'ignored_test.sh\n' > "$repo/.git/info/exclude"; printf '%s\n' "$banned" > "$repo/ignored_test.sh"
out="$(bash "$CHECK" --root "$repo" 2>&1)"; rc=$?
want='CHECK shtest-clock FAIL 7 sites in 7 of 8 files
infra/fence/gate-fixtures/shell-hang.sh:1 sleep
infra/fence/tests/untracked_test.sh:1 sleep
infra/plugin-lab/tests/fakes/claude:2 sleep
pfm/scripts/gate_test.sh:1 sleep
scripts/fresh-install-test.sh:1 sleep
scripts/shtest.sh:1 sleep
scripts/test-format.sh:1 sleep'
sorted="$(printf '%s\n' "$out" | LC_ALL=C sort)"
if [ "$rc" = 1 ] && [ "$sorted" = "$want" ]; then ok 'scope: the seven scanned files are named, the seven others are not'
else bad 'scope: the seven scanned files are named, the seven others are not' "expected rc 1 (sorted):" "$want" "actual rc $rc:" "$out"; fi

shtest_end
