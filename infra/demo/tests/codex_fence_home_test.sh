#!/usr/bin/env bash
# .claude/scripts/dev.sh must loop infra/demo/tests/*_test.sh.
# The demo must refuse a host Codex login before it touches Docker.
set -uo pipefail
SHTEST_TAG=codex-fence-home-test
source "$(dirname -- "${BASH_SOURCE[0]}")/../../../scripts/shtest.sh"
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
FAKE_HOME="$T/home"
mkdir -p "$FAKE_HOME/.codex" "$T/bin"
printf '{"tokens":{"access_token":"HOST-ONLY-TOKEN"}}\n' >"$FAKE_HOME/.codex/auth.json"
cat >"$T/bin/docker" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$DOCKER_CALLS"
exit 0
STUB
chmod +x "$T/bin/docker"
OUT="$(HOME="$FAKE_HOME" PATH="$T/bin:$PATH" DOCKER_CALLS="$T/docker.calls" \
  bash "$ROOT/infra/demo/up.sh" --no-fleet --no-verify 2>&1)"
RC=$?
if [ "$RC" -ne 0 ] &&
  printf '%s' "$OUT" | grep -qF 'codex home: BLOCKED — no fence login at ~/.local/state/pfm/codex-fence/auth.json; create it once: CODEX_HOME=~/.local/state/pfm/codex-fence codex login --device-auth' &&
  [ ! -e "$T/docker.calls" ]; then
  ok "up.sh refuses the host-only Codex login before Docker is called"
else
  bad "host-only Codex login" "rc=$RC" "$OUT" "docker calls: $(cat "$T/docker.calls" 2>/dev/null)"
fi
shtest_end
