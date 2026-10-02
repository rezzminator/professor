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
else
  exit 3
fi
EOF
cat > "$T/bin/grep" <<'EOF'
#!/usr/bin/env bash
for arg in "$@"; do
  if [[ -n "${GREP_FAIL_FILE:-}" && "$arg" == "$GREP_FAIL_FILE" ]]; then
    exit 2
  fi
done
exec /usr/bin/grep "$@"
EOF
chmod +x "$T/bin/git" "$T/bin/grep"

run_check() {
  local fail_file="${GREP_FAIL_FILE:-}"
  env -u PFM_DEV_REPO_GIT_DIR -u PFM_DEV_REPO_WORK_TREE \
    TEST_REPO="$T/repo" GREP_FAIL_FILE="$fail_file" \
    LEAK_TERMS="$T/terms" PATH="$T/bin:$PATH" \
    bash "$ROOT/scripts/leak-check.sh" --files "$@" > "$T/out" 2> "$T/err"
  rc=$?
  out="$(cat "$T/out")"
  err="$(cat "$T/err")"
}

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

shtest_end
