#!/usr/bin/env bash
# A failed pfm host-install restores the engine-sync link; a successful swap keeps the new writer.
# The Makefile runs with invented homes, repositories and executable fixtures inside the fence.
# BROKEN STATE: wrong live versions or a missing restoration diagnostic fails an assertion.
set -uo pipefail
SHTEST_TAG=host-install-transaction
source "$(dirname "$0")/../../scripts/shtest.sh"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
shtest_isolate_host
mkdir -p "$T/repo/pfm/scripts" "$T/bin" "$T/es/bin" "$T/es/versions/old" "$T/es/versions/new"
printf '1.0.0-alpha\n' > "$T/repo/VERSION"
printf '#!/bin/sh\nexit 0\n' > "$T/repo/pfm/scripts/install-downgrade-guard.sh"
cat > "$T/repo/pfm/scripts/install-engine-sync.sh" <<'ES'
#!/bin/bash
set -e
[ "${PFM_ES_RC:-0}" = 0 ] || exit "$PFM_ES_RC"
link="$ENGINE_SYNC_HOME/bin/engine-sync"
if [ -n "${ENGINE_SYNC_LINK_JOURNAL:-}" ]; then
  printf '%s\n' "$link" > "$ENGINE_SYNC_LINK_JOURNAL/path"
  cp -P "$link" "$ENGINE_SYNC_LINK_JOURNAL/previous"
  ln -s ../versions/new/engine-sync "$ENGINE_SYNC_LINK_JOURNAL/installed"
fi
ln -s ../versions/new/engine-sync "$link.next"
mv -f "$link.next" "$link"
ES
cat > "$T/bin/go" <<'GO'
#!/bin/bash
if [ "$1" = env ]; then echo fixture; exit 0; fi
out=''; prev=''
for arg; do [ "$prev" != -o ] || out=$arg; prev=$arg; done
[ "$PFM_BUILD_RC" = 0 ] || exit "$PFM_BUILD_RC"
cat > "$out" <<'PFM'
#!/bin/bash
if [ "$1" = --version ]; then echo 'pfm 1.0.0-alpha'; else exit "${PFM_CHECK_RC:-0}"; fi
PFM
chmod +x "$out"
GO
chmod +x "$T/bin/go"
export PATH="$T/bin:$PATH" ENGINE_SYNC_HOME="$T/es"
for row in 'engine-sync:0:0:13' 'build:7:0:0' 'check:0:11:0' 'success:0:0:0'; do
  IFS=: read -r label build_rc check_rc es_rc <<< "$row"
  rm -f "$T/es/bin/engine-sync"
  ln -s ../versions/old/engine-sync "$T/es/bin/engine-sync"
  out=$(PFM_ES_RC="$es_rc" PFM_BUILD_RC="$build_rc" PFM_CHECK_RC="$check_rc" make -s -f "$ROOT/Makefile" host-install     REPO="$T/repo" BIN="$HOME/bin/pfm" ENGINE_SYNC_SRC="$T/checkout" 2>&1); rc=$?
  link=$(readlink "$T/es/bin/engine-sync")
  if [ "$label" = engine-sync ]; then
    if [ "$rc" != 0 ] && [ "$link" = ../versions/old/engine-sync ]; then ok 'a refused engine-sync install stops the pfm swap';
    else bad 'refused engine-sync installation' "rc=$rc link=$link" "$out"; fi
  elif [ "$label" = success ]; then
    if [ "$rc" = 0 ] && [ "$link" = ../versions/new/engine-sync ]; then ok 'successful pfm swap keeps the new engine-sync link';
    else bad 'successful transaction' "rc=$rc link=$link" "$out"; fi
  else
    if [ "$rc" != 0 ] && [ "$link" = ../versions/old/engine-sync ] && [[ $out == *'restored engine-sync stable link'* ]]; then
      ok "a refused pfm $label restores the previous engine-sync stable link"
    else bad "refused pfm $label transaction" "rc=$rc link=$link" "$out"; fi
  fi
done
shtest_end
