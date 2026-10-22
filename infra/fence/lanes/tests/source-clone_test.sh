#!/usr/bin/env bash
# The install fixture owns writable output and Git metadata, never the mounted
# checkout's .git file or pre-existing generated files.
set -euo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
SHTEST_TAG=source-clone-test
source "$HERE/../../../../scripts/shtest.sh"
shtest_isolate_host
SUT="$HERE/../../source-clone.sh"
mkdir -p "$T/source/pfm/harness-prompts/generated" "$T/source/.worktrees/other" "$T/source/plugins/example"
printf 'gitdir: /unmounted/host/plugin\n' >"$T/source/plugins/example/.git"
printf 'plugin source\n' >"$T/source/plugins/example/source.txt"
printf 'source part\n' >"$T/source/part.md"
printf 'gitdir: /unmounted/host/git\n' >"$T/source/.git"
printf 'stale output\n' >"$T/source/pfm/harness-prompts/generated/claude.md"
printf 'unrelated worktree\n' >"$T/source/.worktrees/other/file"
# Fence containers are root while the read-only host mount belongs to its user.
chown 12345:12345 "$T/source"
PFM_DEV_FENCE=1 bash "$SUT" "$T/source" "$T/clone"
if cmp -s "$T/source/part.md" "$T/clone/part.md" &&
  [ -L "$T/clone" ] && [ -d "$T/clone/.git" ] && [ ! -e "$T/clone/.worktrees" ] &&
  [ ! -e "$T/clone/plugins/example/.git" ] && cmp -s "$T/source/plugins/example/source.txt" "$T/clone/plugins/example/source.txt" &&
  [ ! -e "$T/clone/pfm/harness-prompts/generated/claude.md" ] &&
  git -C "$T/clone" rev-parse --verify HEAD >/dev/null; then
  ok 'source snapshot has its own Git history and no copied output'
else
  bad 'source snapshot is not independent'
fi
mkdir -p "$T/clone/pfm/harness-prompts/generated"
printf 'new output\n' >"$T/clone/pfm/harness-prompts/generated/claude.md"
PFM_DEV_FENCE=1 bash "$SUT" "$T/source" "$T/clone"
if grep -qx 'stale output' "$T/source/pfm/harness-prompts/generated/claude.md" &&
  grep -qx 'new output' "$T/clone/pfm/harness-prompts/generated/claude.md" &&
  grep -qx 'gitdir: /unmounted/host/git' "$T/source/.git"; then
  ok 'reusing the snapshot preserves generated files and leaves source untouched'
else
  bad 'snapshot reuse changed source or output'
fi
ln -s "$T/source" "$T/alias"
if PFM_DEV_FENCE=1 bash "$SUT" "$T/source" "$T/alias" >"$T/error" 2>&1; then
  bad 'accepted a symlink back to the source'
elif grep -q 'destination is a symlink' "$T/error"; then
  ok 'refuses a symlink destination by name'
else
  bad 'symlink refusal omitted the cause'
fi
# Sim bootstrap must surface snapshot failure through its named failure door,
# before install or the caller runs. Only the fixture mount and tools differ.
mkdir -p "$T/sim-source/infra/fence" "$T/sim-bin"
printf '0.1.0-alpha\n' >"$T/sim-source/VERSION"
cat >"$T/sim-source/infra/fence/engine-sync.sh" <<'SH'
engine_sync_build() { return 0; }
engine_sync_link_stable() { return 0; }
SH
printf '#!/usr/bin/env bash\necho "source-clone: fixture copy failed" >&2\nexit 42\n' >"$T/sim-source/infra/fence/source-clone.sh"
printf '#!/usr/bin/env bash\nexit 0\n' >"$T/sim-bin/go"
printf '#!/usr/bin/env bash\necho fixture-chrome\n' >"$T/sim-bin/google-chrome-stable"
chmod +x "$T/sim-bin/go" "$T/sim-bin/google-chrome-stable"
sed "s|/worktree|$T/sim-source|g" "$HERE/../../sim-entry.sh" >"$T/sim-entry.sh"
if HOME="$T/sim-home" PFM_CONFIG="$T/sim-config.json" PATH="$T/sim-bin:$PATH" \
  bash "$T/sim-entry.sh" touch "$T/caller-ran" >"$T/sim-error" 2>&1; then
  bad 'sim bootstrap accepted a failed source snapshot'
elif grep -q 'sim: BOOTSTRAP-FAILED — writable source snapshot' "$T/sim-error" &&
  grep -q 'source-clone: fixture copy failed' "$T/sim-error" && [ ! -e "$T/caller-ran" ]; then
  ok 'sim names source snapshot failure and never runs the caller'
else
  bad 'sim snapshot failure omitted its bootstrap context or cause'
fi
# A healthy simulation uses the alpha installer to build/setup the local plugin.
printf '#!/usr/bin/env bash\nmkdir -p "$2"\n' >"$T/sim-source/infra/fence/source-clone.sh"
mkdir -p "$T/sim-home/.local/bin"
cat >"$T/sim-home/.local/bin/pfm" <<'SH'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$SIM_CALLS"
case "$*" in
  'mcp harvester enable') exit 0 ;;
  'install --yes --skip-engine codex --skip-themes')
    [ "${SIM_INSTALL_FAIL:-0}" = 0 ] || { echo 'fixture install failed' >&2; exit 42; }
    if [ -n "${SIM_EXPECT_CONFIG:-}" ]; then
      cmp -s "$PFM_CONFIG" "$SIM_EXPECT_CONFIG" || exit 1
    else
      jq -e '.version == 2 and .accounts == [] and .claude.plugins == []' "$PFM_CONFIG" >/dev/null 2>&1 || {
      echo 'sim: real harvester bootstrap must not depend on Claude plugin delivery' >&2
      exit 1
      }
    fi
    exit 0 ;;
  *) exit 2 ;;
esac
SH
printf '#!/usr/bin/env bash\nexit 0\n' >"$T/sim-home/.local/bin/engine-sync"
chmod +x "$T/sim-home/.local/bin/pfm" "$T/sim-home/.local/bin/engine-sync"
if HOME="$T/sim-home" SIM_CALLS="$T/sim.calls" PFM_CONFIG="$T/sim-config.json" \
  PATH="$T/sim-home/.local/bin:$T/sim-bin:$PATH" bash "$T/sim-entry.sh" touch "$T/caller-ran" >"$T/sim-healthy" 2>&1 &&
  [ -f "$T/caller-ran" ] &&
  [ "$(cat "$T/sim.calls")" = "$(printf 'mcp harvester enable\ninstall --yes --skip-engine codex --skip-themes')" ] &&
  grep -q 'browser-rung=on' "$T/sim-healthy"; then
  ok 'sim enables the standalone registration before installer provisioning and runs its caller'
else bad 'sim standalone plugin bootstrap' "$(cat "$T/sim-healthy")"; fi
printf '{"accounts":[{"id":7}],"claude":{"plugins":["fixture"]}}\n' >"$T/sim-config.json"
cp "$T/sim-config.json" "$T/expected-config.json"
if HOME="$T/sim-home" SIM_CALLS="$T/sim.calls" PFM_CONFIG="$T/sim-config.json" \
  SIM_EXPECT_CONFIG="$T/expected-config.json" PATH="$T/sim-home/.local/bin:$T/sim-bin:$PATH" \
  bash "$T/sim-entry.sh" true >"$T/sim-preserved" 2>&1 &&
  cmp -s "$T/sim-config.json" "$T/expected-config.json"; then
  ok 'sim preserves an existing account/plugin config instead of replacing it with its fresh fixture'
else bad 'sim existing config changed' "$(cat "$T/sim-preserved")"; fi
rm "$T/caller-ran"
if HOME="$T/sim-home" SIM_CALLS="$T/sim.calls" PFM_CONFIG="$T/sim-config.json" \
  SIM_INSTALL_FAIL=1 PATH="$T/sim-home/.local/bin:$T/sim-bin:$PATH" \
  bash "$T/sim-entry.sh" touch "$T/caller-ran" >"$T/sim-install-error" 2>&1; then
  bad 'sim accepted a failed actual installer step'
elif grep -q 'sim: BOOTSTRAP-FAILED — pfm install' "$T/sim-install-error" &&
  grep -q 'fixture install failed' "$T/sim-install-error" && [ ! -e "$T/caller-ran" ]; then
  ok 'sim still refuses its caller on a named installer failure'
else bad 'sim installer failure lost its cause or ran the caller' "$(cat "$T/sim-install-error")"; fi
shtest_end
