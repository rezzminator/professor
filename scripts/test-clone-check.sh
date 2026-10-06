#!/usr/bin/env bash
# Exercise the clone ratchet with a controlled installed tool and scan results.
set -uo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
TOOLS_SCRIPT="${TOOLS_SCRIPT:-$ROOT/infra/fence/tools.sh}"
# shellcheck source=../infra/fence/tools.env
source "$ROOT/infra/fence/tools.env"
SHTEST_TAG=clone-check
# shellcheck source=scripts/shtest.sh
source "$ROOT/scripts/shtest.sh"
mkdir -p "$T/bin" "$T/path" "$T/absent"

# The old npx route makes the absent-tool case red before clone-check is fixed.
cat > "$T/path/npx" <<'EOF'
#!/usr/bin/env bash
echo 'Found 17 clones'
EOF
chmod +x "$T/path/npx"
cat > "$T/bin/jscpd" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == --version ]]; then
  echo "$JSCPD_STUB_VERSION"
  exit 0
fi
echo scanned > "$JSCPD_SCAN_MARKER"
case "${JSCPD_STUB_MODE:-pass}" in
  pass) echo 'Found 17 clones' ;;
  new) echo 'Clone found in fixture [NEW]'; echo ' - new-left'; echo '   new-right'; echo 'ERROR: jscpd found 1 new clones'; exit 1 ;;
  late-new)
    for ((i = 0; i < 15; i++)); do
      echo 'Clone found in fixture'
      echo " - known-left-$i"
      echo "   known-right-$i"
    done
    echo 'Clone found in fixture [NEW]'
    echo ' - late-new-left'
    echo '   late-new-right'
    echo 'ERROR: jscpd found 1 new clones'
    exit 1
    ;;
  crash) echo 'fixture scan crashed' >&2; exit 3 ;;
esac
EOF
chmod +x "$T/bin/jscpd"

clone() {
  env PATH="$T/path:/usr/bin:/bin" TOOLS_BIN="$1" \
    JSCPD_STUB_VERSION="${2:-$JSCPD_VERSION}" JSCPD_STUB_MODE="${3:-pass}" \
    JSCPD_SCAN_MARKER="$T/scanned" bash "$ROOT/scripts/clone-check.sh" ${4:+"$4"} 2>&1
}

for scenario in missing-scan wrong-scan missing-resolve wrong-resolve; do
  case "$scenario" in
    missing-*) tool_bin="$T/absent"; version="$JSCPD_VERSION" ;;
    wrong-*) tool_bin="$T/bin"; version=9.9.9 ;;
  esac
  mode_flag=
  [[ "$scenario" != *-resolve ]] || mode_flag=--resolve
  rm -f "$T/scanned"
  out=$(clone "$tool_bin" "$version" pass "$mode_flag"); rc=$?
  if [[ $rc -eq 2 && "$out" == *jscpd* && ! -e "$T/scanned" ]]; then
    ok "$scenario: unavailable jscpd refuses to scan and returns 2"
  else
    bad "$scenario: unavailable jscpd refuses to scan and returns 2" "rc=$rc; $out"
  fi
done

rm -f "$T/scanned"
out=$(clone "$T/bin"); rc=$?
if [[ $rc -eq 0 && "$out" == 'CLONES PASS Found 17 clones, none new' && -e "$T/scanned" ]]; then
  ok 'installed jscpd scans and preserves the pass count'
else
  bad 'installed jscpd scans and preserves the pass count' "rc=$rc; $out"
fi

rm -f "$T/scanned"
out=$(clone "$T/bin" "$JSCPD_VERSION" new); rc=$?
if [[ $rc -eq 1 && "$out" == *'CLONES FAIL new clone(s) above'* && -e "$T/scanned" ]]; then
  ok 'a new clone fails the ratchet with return 1'
else
  bad 'a new clone fails the ratchet with return 1' "rc=$rc; $out"
fi

out=$(clone "$T/bin" "$JSCPD_VERSION" late-new); rc=$?
if [[ $rc -eq 1 && "$out" == *'late-new-left'* && "$out" == *'late-new-right'* ]]; then
  ok 'a new clone after the known listing names both files'
else
  bad 'a new clone after the known listing names both files' "rc=$rc; $out"
fi

rm -f "$T/scanned"
out=$(clone "$T/bin" "$JSCPD_VERSION" pass --resolve); rc=$?
if [[ $rc -eq 0 && "$out" == "$T/bin/jscpd" && ! -e "$T/scanned" ]]; then
  ok '--resolve prints the pinned jscpd and does not scan'
else
  bad '--resolve prints the pinned jscpd and does not scan' "rc=$rc; $out"
fi

out=$(clone "$T/bin" "$JSCPD_VERSION" pass --bogus); rc=$?
if [[ $rc -eq 2 && "$out" == usage:* ]]; then
  ok 'an unknown mode prints the usage and returns 2'
else
  bad 'an unknown mode prints the usage and returns 2' "rc=$rc; $out"
fi

out=$(env TOOLS_BIN="$T/selected-bin" bash "$TOOLS_SCRIPT" --print-bin 2>&1); rc=$?
if [[ $rc -eq 0 && "$out" == "$T/selected-bin" ]]; then
  ok 'tools --print-bin resolves the configured bin'
else
  bad 'tools --print-bin resolves the configured bin' "rc=$rc; $out"
fi

out=$(env -u TOOLS_BIN PATH="$T/path:/usr/bin:/bin" bash "$TOOLS_SCRIPT" --print-bin 2>&1); rc=$?
if [[ $rc -ne 0 && "$out" == *'tools: TOOLCHAIN-MISSING — go not on PATH'* ]]; then
  ok 'tools --print-bin names a missing Go toolchain'
else
  bad 'tools --print-bin names a missing Go toolchain' "rc=$rc; $out"
fi

out=$(TOOLS_BIN=/usr/local/bin bash "$TOOLS_SCRIPT" 2>&1); rc=$?
tool_line=$(printf '%s\n' "$out" | grep '^TOOL jscpd' || true)
if [[ $rc -eq 0 && "$tool_line" == *"$JSCPD_VERSION"* && "$tool_line" == *PRESENT* ]]; then
  ok 'installed jscpd and identical lockfile report PRESENT'
else
  bad 'installed jscpd and identical lockfile report PRESENT' "rc=$rc; $out"
fi

mkdir -p "$T/tool-src"
cp "$TOOLS_SCRIPT" "$ROOT/infra/fence/tools.env" "$T/tool-src/"
cp -R "$ROOT/infra/fence/jscpd" "$T/tool-src/"
node -e 'const fs = require("fs"); const p = process.argv[1]; const lock = JSON.parse(fs.readFileSync(p)); lock.packages["node_modules/jscpd"].version = "9.9.9"; fs.writeFileSync(p, JSON.stringify(lock));' "$T/tool-src/jscpd/package-lock.json" \
  || { echo 'test-clone-check: lockfile mutation failed — harness did not start' >&2; exit 2; }
cat > "$T/path/npm" <<'EOF'
#!/usr/bin/env bash
echo called > "$JSCPD_NPM_MARKER"
exit 99
EOF
chmod +x "$T/path/npm"
out=$(env TOOLS_BIN=/usr/local/bin PATH="$T/path:$PATH" JSCPD_NPM_MARKER="$T/npm-called" bash "$T/tool-src/tools.sh" 2>&1); rc=$?
if [[ $rc -eq 2 && "$out" == *"jscpd 9.9.9; JSCPD_VERSION is $JSCPD_VERSION"* && ! -e "$T/npm-called" ]]; then
  ok 'lockfile version drift names both versions before npm ci'
else
  bad 'lockfile version drift names both versions before npm ci' "rc=$rc; $out"
fi

out=$(env TOOLS_BIN=/usr/local/bin PATH=/usr/local/go/bin:/usr/bin:/bin bash "$TOOLS_SCRIPT" 2>&1); rc=$?
if [[ $rc -eq 1 && "$out" == *'tools: TOOLCHAIN-MISSING — npm not on PATH'* ]]; then
  ok 'missing npm is a named toolchain failure'
else
  bad 'missing npm is a named toolchain failure' "rc=$rc; $out"
fi

shtest_end
