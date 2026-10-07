#!/usr/bin/env bash
set -uo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SHTEST_TAG=leak-check-test
source "$ROOT/scripts/shtest.sh"
mkdir -p "$T/bin" "$T/repo/.githooks" "$T/repo/fixtures/ignore"

cat > "$T/terms" <<'EOF'
syntheticsecret
!ignore-path fixtures/ignore/*
!ignore-token syntheticsecret-public
EOF
cat > "$T/bin/git" <<'EOF'
#!/usr/bin/env bash
if [[ "$*" == 'rev-parse --show-toplevel' ]]; then
  printf '%s\n' "$TEST_REPO"
elif [[ "${1:-}" == 'diff' ]]; then
  cat "$GIT_DIFF_FILE"
else
  exit 3
fi
EOF
cat > "$T/bin/grep" <<'EOF'
#!/usr/bin/env bash
if [[ -n "${GREP_FAIL_STDIN:-}" && "$*" == *syntheticsecret* ]]; then
  input=$(cat)
  if [[ -n "$input" && " $* " == *" $GREP_FAIL_STDIN "* ]]; then exit 2; fi
  exec /usr/bin/grep "$@" <<<"$input"
fi
for arg in "$@"; do
  if [[ -n "${GREP_FAIL_FILE:-}" && "$arg" == "$GREP_FAIL_FILE" ]]; then
    exit 2
  fi
  if [[ -n "${GREP_FAIL_SUFFIX:-}" && "$arg" == *"$GREP_FAIL_SUFFIX" ]]; then
    exit 2
  fi
done
# GREP_NOFOLD_LOCALE names a caller locale whose -i misses a case pair, as GNU grep 3.11 does on a real host:
# GREP_NOFOLD_SCOPE=ascii stands for tr_TR.UTF-8 (I and i are no pair), nonascii for zh_CN.GB18030 (a two-byte
# letter reads as one character, but É and é are no pair). Under it -i is dropped from every grep whose
# arguments are all ASCII (ascii) or hold a non-ASCII byte (nonascii).
if [[ -n "${GREP_NOFOLD_LOCALE:-}" && "${LC_ALL:-}" == "$GREP_NOFOLD_LOCALE" ]]; then
  nonascii=0
  for arg in "$@"; do
    if printf '%s' "$arg" | LC_ALL=C /usr/bin/grep -q '[^ -~]'; then nonascii=1; fi
  done
  if [[ "$GREP_NOFOLD_SCOPE" == ascii && "$nonascii" == 0 || "$GREP_NOFOLD_SCOPE" == nonascii && "$nonascii" == 1 ]]; then
    args=()
    for arg in "$@"; do
      [[ "$arg" =~ ^-[A-Za-z]+$ ]] && arg="${arg//i/}"
      [[ "$arg" == - ]] || args+=("$arg")
    done
    set -- "${args[@]}"
  fi
fi
if [[ -n "${GREP_FORCE_C:-}" ]]; then
  LC_ALL=C exec /usr/bin/grep "$@"
fi
exec /usr/bin/grep "$@"
EOF
chmod +x "$T/bin/git" "$T/bin/grep"

# leak_run <leak-check args>: LC_ALL=C.UTF-8 is the fence image's own locale (LEAK_LC_ALL names another
# caller locale), and stdin is /dev/null so a grep that reads a file name as an option fails instead of hanging.
# TERMS names a second terms file; GREP_FAIL_FILE and GREP_FAIL_SUFFIX drive the grep stub, and GREP_FORCE_C
# makes the stub run every grep under LC_ALL=C (no UTF-8 locale reaches it); GREP_NOFOLD_LOCALE and
# GREP_NOFOLD_SCOPE make it drop -i under one caller locale.
leak_run() {
  local terms="${TERMS:-$T/terms}"
  env -u PFM_DEV_REPO_GIT_DIR -u PFM_DEV_REPO_WORK_TREE \
    TEST_REPO="$T/repo" GREP_FAIL_FILE="${GREP_FAIL_FILE:-}" GREP_FAIL_SUFFIX="${GREP_FAIL_SUFFIX:-}" \
    GREP_FAIL_STDIN="${GREP_FAIL_STDIN:-}" GREP_FORCE_C="${GREP_FORCE_C:-}" GREP_NOFOLD_LOCALE="${GREP_NOFOLD_LOCALE:-}" GREP_NOFOLD_SCOPE="${GREP_NOFOLD_SCOPE:-}" \
    GIT_DIFF_FILE="$T/diff" LEAK_TERMS="$terms" LC_ALL="${LEAK_LC_ALL:-C.UTF-8}" \
    PATH="$T/bin:$PATH" \
    bash "$ROOT/scripts/leak-check.sh" "$@" > "$T/out" 2> "$T/err" < /dev/null
  rc=$?
  out="$(cat "$T/out")"
  err="$(cat "$T/err")"
}
run_check() { leak_run --files "$@"; }
run_range() { leak_run --range A B; }

printf 'ordinary fixture 1\n' > "$T/repo/clean-1.txt"

printf 'syntheticsecret\n' > "$T/repo/hit.txt"
printf 'syntheticsecret again\n' > "$T/repo/hit-two.txt"
run_check hit-two.txt hit.txt
if [[ "$rc" -eq 1 && "$out" == $'LEAK hit-two.txt: syntheticsecret again\nLEAK hit.txt: syntheticsecret' && "$err" == *'leak-check: FAILED — 2 leak line(s)'* ]]; then
  ok 'hits preserve input order, exact lines, and failure count'
else
  bad 'hits preserve input order, exact lines, and failure count' "rc=$rc; out=$out; err=$err"
fi

printf '/%s/test\n' home > "$T/repo/benign.txt"
run_check benign.txt
if [[ "$rc" -eq 0 && "$out" == 'leak-check: clean' && "$err" == *'1 benign-token line(s) suppressed'* ]]; then
  ok 'benign token is suppressed and counted'
else
  bad 'benign token is suppressed and counted' "rc=$rc; out=$out; err=$err"
fi

printf 'syntheticsecret-public\nsyntheticsecret-public syntheticsecret\n' > "$T/repo/ignored.txt"
run_check ignored.txt
if [[ "$rc" -eq 1 && "$out" == 'LEAK ignored.txt: syntheticsecret-public syntheticsecret' && "$err" == *'1 benign-token line(s) suppressed'* ]]; then
  ok 'ignore token suppresses its own substring but keeps a separate hit'
else
  bad 'ignore token suppresses its own substring but keeps a separate hit' "rc=$rc; out=$out; err=$err"
fi

printf 'syntheticsecret\n' > "$T/repo/LICENSE"
printf 'syntheticsecret\n' > "$T/repo/.githooks/guard"
printf 'syntheticsecret\n' > "$T/repo/fixtures/ignore/skip.txt"
run_check LICENSE .githooks/guard fixtures/ignore/skip.txt clean-1.txt
if [[ "$rc" -eq 0 && "$out" == 'leak-check: clean' && "$err" == *'scanned 1 file(s) (3 excluded by design, 0 not a regular file)'* ]]; then
  ok 'license, hook, and configured path are excluded and counted'
else
  bad 'license, hook, and configured path are excluded and counted' "rc=$rc; out=$out; err=$err"
fi

run_check missing.txt hit.txt
if [[ "$rc" -eq 1 && "$out" == 'LEAK hit.txt: syntheticsecret' && "$err" == *'NOT-SCANNED missing.txt: not a regular file (deleted or misnamed) — examined by nothing, counted as clean by nothing'* ]]; then
  ok 'missing path is named while a regular file is scanned'
else
  bad 'missing path is named while a regular file is scanned' "rc=$rc; out=$out; err=$err"
fi

run_check missing.txt
if [[ "$rc" -eq 1 && "$out" == 'SCAN-ERROR: --files named 1 path(s), 0 excluded by design, and every one of the remaining 1 was NOT a regular file — nothing was examined, refusing to report clean' ]]; then
  ok 'an all-missing input refuses a clean verdict'
else
  bad 'an all-missing input refuses a clean verdict' "rc=$rc; out=$out; err=$err"
fi

printf 'ordinary fixture\n' > "$T/repo/bad.txt"
GREP_FAIL_FILE=bad.txt run_check hit.txt bad.txt
if [[ "$rc" -eq 1 && "$out" == $'LEAK hit.txt: syntheticsecret\nSCAN-ERROR bad.txt: leak-check could NOT read this file (grep rc=2) — treated as FAILURE, never as clean' ]]; then
  ok 'unreadable file is named once and other hits survive'
else
  bad 'unreadable file is named once and other hits survive' "rc=$rc; out=$out; err=$err"
fi

run_check clean-1.txt
if [[ "$rc" -eq 0 && "$out" == 'leak-check: clean' && "$err" == *'scanned 1 file(s) (0 excluded by design, 0 not a regular file)'* ]]; then
  ok 'clean input preserves the verdict and count'
else
  bad 'clean input preserves the verdict and count' "rc=$rc; out=$out; err=$err"
fi

printf 'syntheticsecret: detail\n' > "$T/repo/odd:name.txt"
run_check odd:name.txt
if [[ "$rc" -eq 1 && "$out" == 'LEAK odd:name.txt: syntheticsecret: detail' ]]; then
  ok 'a colon in the file name survives the hit parser'
else
  bad 'a colon in the file name survives the hit parser' "rc=$rc; out=$out; err=$err"
fi

# ---- binary and undecodable files are scanned as text -------------------------
# The suite is itself leak-scanned, so the address and the home path are assembled here.
printf 'caf\351 syntheticsecret\n' > "$T/repo/latin1.txt"
run_check latin1.txt
if [[ "$rc" -eq 1 && "$out" == "LEAK latin1.txt: caf"$'\xe9'" syntheticsecret" ]]; then
  ok 'an undecodable file is scanned and its hit keeps the raw byte'
else
  bad 'an undecodable file is scanned and its hit keeps the raw byte' "rc=$rc; out=$out; err=$err"
fi

printf 'head\0syntheticsecret\n' > "$T/repo/nul.bin"
run_check nul.bin hit.txt
first="${out%%$'\n'*}"
second="${out#*$'\n'}"
if [[ "$rc" -eq 1 && "$first" == 'LEAK nul.bin: '* && "$second" == 'LEAK hit.txt: syntheticsecret' ]]; then
  ok 'a binary file beside a text hit keeps both hit lines paired with their files'
else
  bad 'a binary file beside a text hit keeps both hit lines paired with their files' "rc=$rc; out=$out; err=$err"
fi

gm_addr="$(printf 'someone@%s.com' gmail)"
printf '%s\n' "$gm_addr" > "$T/repo/gm.txt"
run_check gm.txt
if [[ "$rc" -eq 1 && "$out" == "LEAK gm.txt: $gm_addr" && "$err" == *'+ 4 structural pattern(s)'* ]]; then
  ok 'a personal mailbox is a structural hit'
else
  bad 'a personal mailbox is a structural hit' "rc=$rc; out=$out; err=$err"
fi

# ---- a terms file the gate cannot compile is refused in both modes ------------
printf 'diff --git a/r.txt b/r.txt\n--- a/r.txt\n+++ b/r.txt\n@@ -0,0 +1 @@\n+syntheticsecret\n' > "$T/diff"
printf 'syntheticsecret\n!ignore-token bad#token\n' > "$T/terms-token"
for mode in files range; do
  case "$mode" in
    files) TERMS="$T/terms-token" run_check hit.txt ;;
    range) TERMS="$T/terms-token" run_range ;;
  esac
  if [[ "$rc" -eq 1 && -z "$out" && "$err" == *"$T/terms-token"* && "$err" == *'bad#token'* ]]; then
    ok "an unusable ignore token refuses a --$mode scan"
  else
    bad "an unusable ignore token refuses a --$mode scan" "rc=$rc; out=$out; err=$err"
  fi
done

printf 'syntheticsecret\n!ignore-token bad#token\n!ignore-path fixtures/ignore/*\n' > "$T/terms-order"
TERMS="$T/terms-order" run_check hit.txt
if [[ "$rc" -eq 1 && "$err" == *'1 configured path ignore(s)'* && "$err" == *"$T/terms-order"* ]]; then
  ok 'the configured path ignore notice prints beside a refusal'
else
  bad 'the configured path ignore notice prints beside a refusal' "rc=$rc; out=$out; err=$err"
fi

printf 'syntheticsecret\nbad(\n' > "$T/terms-bad"
for mode in files range; do
  case "$mode" in
    files) TERMS="$T/terms-bad" run_check hit.txt ;;
    range) TERMS="$T/terms-bad" run_range ;;
  esac
  if [[ "$rc" -eq 1 && -z "$out" && "$err" == *"$T/terms-bad"* ]]; then
    ok "an uncompilable term refuses a --$mode scan"
  else
    bad "an uncompilable term refuses a --$mode scan" "rc=$rc; out=$out; err=$err"
  fi
done

# ---- every judging failure names its source file ------------------------------
printf 'syntheticsecret one\nsyntheticsecret two\n' > "$T/repo/multi.txt"
GREP_FAIL_SUFFIX=.normalized run_check hit.txt multi.txt
if [[ "$rc" -eq 1 && "$(wc -l <<<"$out")" -eq 2 && "$out" != *"$T"* ]] &&
  grep -q '^SCAN-ERROR hit\.txt:' <<<"$out" && grep -q '^SCAN-ERROR multi\.txt:' <<<"$out"; then
  ok 'a judging failure names each source file once, never a temp path'
else
  bad 'a judging failure names each source file once, never a temp path' "rc=$rc; out=$out; err=$err"
fi

printf 'syntheticsecret\n' > "$T/repo/-i"
GREP_FAIL_FILE=bad.txt run_check bad.txt -i
if [[ "$rc" -eq 1 ]] && grep -q '^SCAN-ERROR bad\.txt:' <<<"$out" && grep -Fxq 'LEAK -i: syntheticsecret' <<<"$out"; then
  ok 'an option-shaped file name is read as a file by the per-file fallback'
else
  bad 'an option-shaped file name is read as a file by the per-file fallback' "rc=$rc; out=$out; err=$err"
fi

# ---- a term matches multibyte letters under a UTF-8 locale ---------------------
# Every fixture is written as octal escapes so this suite stays ASCII: \303\251 is the lowercase
# e-acute, \303\211 its capital, \303\266 the lowercase o-umlaut, \303\226 the capital one.
printf 'syntheticsecret\njos\303\251\n\303\251t\303\251\n' > "$T/terms-utf8"

printf 'JOS\303\211 here\n' > "$T/repo/upper.txt"
upper_text="$(printf 'JOS\303\211 here')"
printf '\303\211T\303\251\n' > "$T/repo/mixed.txt"
mixed_text="$(printf '\303\211T\303\251')"
printf 'diff --git a/r.txt b/r.txt\n--- a/r.txt\n+++ b/r.txt\n@@ -0,0 +1 @@\n+JOS\303\211 here\n' > "$T/diff"
for form in upper-files mixed-files upper-range; do
  case "$form" in
    upper-files) TERMS="$T/terms-utf8" run_check upper.txt; expected="LEAK upper.txt: $upper_text" ;;
    mixed-files) TERMS="$T/terms-utf8" run_check mixed.txt; expected="LEAK mixed.txt: $mixed_text" ;;
    upper-range) TERMS="$T/terms-utf8" run_range; expected="LEAK r.txt: $upper_text" ;;
  esac
  if [[ "$rc" -eq 1 && "$out" == "$expected" ]]; then
    ok "$form: a non-ASCII case form hits once"
  else
    bad "$form: a non-ASCII case form hits once" "rc=$rc; out=$out; err=$err"
  fi
done

printf 'syntheticsecret\nkn.del\n' > "$T/terms-dot"
printf 'syntheticsecret\nkn[\303\266o]del\n' > "$T/terms-bracket"
printf 'Kn\303\226del here\n' > "$T/repo/kn.txt"
kn_text="$(printf 'Kn\303\226del here')"

printf 'diff --git a/r.txt b/r.txt\n--- a/r.txt\n+++ b/r.txt\n@@ -0,0 +1 @@\n+Kn\303\226del here\n' > "$T/diff"
for pattern_mode in dot-files bracket-files bracket-range; do
  case "$pattern_mode" in
    dot-files) TERMS="$T/terms-dot" run_check kn.txt; expected="LEAK kn.txt: $kn_text" ;;
    bracket-files) TERMS="$T/terms-bracket" run_check kn.txt; expected="LEAK kn.txt: $kn_text" ;;
    bracket-range) TERMS="$T/terms-bracket" run_range; expected="LEAK r.txt: $kn_text" ;;
  esac
  if [[ "$rc" -eq 1 && "$out" == "$expected" ]]; then
    ok "$pattern_mode: a pattern matches a multibyte letter"
  else
    bad "$pattern_mode: a pattern matches a multibyte letter" "rc=$rc; out=$out; err=$err"
  fi
done

LEAK_LC_ALL=C TERMS="$T/terms-dot" run_check kn.txt
if [[ "$rc" -eq 1 && "$out" == "LEAK kn.txt: $kn_text" ]]; then
  ok 'a caller under the C locale still matches multibyte letters'
else
  bad 'a caller under the C locale still matches multibyte letters' "rc=$rc; out=$out; err=$err"
fi

GREP_FORCE_C=1 run_check hit.txt
if [[ "$rc" -eq 1 && -z "$out" && "$err" == *'no UTF-8 locale'* ]]; then
  ok 'no UTF-8 locale for grep refuses the scan'
else
  bad 'no UTF-8 locale for grep refuses the scan' "rc=$rc; out=$out; err=$err"
fi

# A caller's UTF-8-reading locale whose -i misses a case pair is passed over for C.UTF-8 (the stub's C.utf8
# stands for it): tr_TR.UTF-8 folds no ASCII I/i, zh_CN.GB18030 reads a two-byte letter as one character
# but folds no É/é.
printf 'SYNTHETICSECRET here\n' > "$T/repo/upper-ascii.txt"
for scope in ascii nonascii; do
  case "$scope" in
    ascii) file=upper-ascii.txt; terms_file="$T/terms"; expected='LEAK upper-ascii.txt: SYNTHETICSECRET here' ;;
    nonascii) file=upper.txt; terms_file="$T/terms-utf8"; expected="LEAK upper.txt: $upper_text" ;;
  esac
  GREP_NOFOLD_LOCALE=C.utf8 GREP_NOFOLD_SCOPE="$scope" LEAK_LC_ALL=C.utf8 TERMS="$terms_file" run_check "$file"
  if [[ "$rc" -eq 1 && "$out" == "$expected" ]]; then
    ok "$scope: a caller locale that folds no case pair is passed over and the term hits once"
  else
    bad "$scope: a caller locale that folds no case pair is passed over and the term hits once" "rc=$rc; out=$out; err=$err"
  fi
done

printf 'diff --git a/r.txt b/r.txt\n--- a/r.txt\n+++ b/r.txt\n@@ -0,0 +1 @@\n+caf\351 syntheticsecret\n' > "$T/diff"
run_range
if [[ "$rc" -eq 1 && "$out" == "LEAK r.txt: caf"$'\xe9'" syntheticsecret" ]]; then
  ok 'an undecodable line in a --range diff is scanned and its hit reported'
else
  bad 'an undecodable line in a --range diff is scanned and its hit reported' "rc=$rc; out=$out; err=$err"
fi

# ---- a pushed binary file is scanned as text ----------------------------------
# A real repository, as the pre-push hook sees it: git diffs a NUL-byte file as
# "Binary files … differ" unless told to treat it as text, and that line holds no hit.
pushed="$T/pushed"
shtest_isolate_host
pgit() { git -C "$pushed" -c user.name=t -c user.email=t@t -c commit.gpgsign=false -c core.hooksPath=/dev/null "$@"; }
git init -q "$pushed" && printf 'ordinary\n' > "$pushed/base.txt" && pgit add base.txt && pgit commit -q -m base
printf 'head\0syntheticsecret\n' > "$pushed/blob.bin"
pgit add blob.bin && pgit commit -q -m blob
# pushed_range: leak-check --range over the pushed repository's last commit, as the pre-push hook runs it.
pushed_range() {
  out="$(PFM_DEV_REPO_GIT_DIR="$pushed/.git" PFM_DEV_REPO_WORK_TREE="$pushed" LEAK_TERMS="$T/terms" LC_ALL=C.UTF-8 \
    bash "$ROOT/scripts/leak-check.sh" --range HEAD~1 HEAD 2> "$T/err" < /dev/null)"
  rc=$?
  err="$(cat "$T/err")"
}
pushed_range
if [[ "$rc" -eq 1 && "$out" == 'LEAK blob.bin: '*syntheticsecret* && "$out" != *$'\n'* ]]; then
  ok 'a NUL-byte file in a pushed range is scanned and its hit reported'
else
  bad 'a NUL-byte file in a pushed range is scanned and its hit reported' "rc=$rc; out=$out; err=$err"
fi

# An added line that starts with "++" reaches the -U0 diff as "+++ …", the shape of a file header. Inside a
# hunk it is content: it is judged, and the lines after it stay credited to their own file.
printf '++ syntheticsecret\nordinary\n' > "$pushed/plus.txt"
pgit add plus.txt && pgit commit -q -m plus
pushed_range
if [[ "$rc" -eq 1 && "$out" == 'LEAK plus.txt: ++ syntheticsecret' ]]; then
  ok 'an added line starting with "++" in a pushed range is judged as content, never read as a file header'
else
  bad 'an added line starting with "++" in a pushed range is judged as content, never read as a file header' "rc=$rc; out=$out; err=$err"
fi

# Runtime matcher/judge failures are distinct from no match or suppression.
printf 'diff --git a/r.txt b/r.txt\n--- a/r.txt\n+++ b/r.txt\n@@ -0,0 +1 @@\n+syntheticsecret\n' > "$T/diff"
for mode in staged range; do
  for stage in -niE -qiE; do
    if [[ "$mode" == staged ]]; then GREP_FAIL_STDIN="$stage" leak_run
    else GREP_FAIL_STDIN="$stage" run_range; fi
    if [[ "$rc" -ne 0 && "$out$err" == *'SCAN-ERROR r.txt:'* && "$out" != *'leak-check: clean'* ]]; then
      ok "$mode: runtime $stage failure refuses clean"
    else
      bad "$mode: runtime $stage failure refuses clean" "rc=$rc; out=$out; err=$err"
    fi
  done
done

shtest_end
