#!/usr/bin/env bash
# Fixture-driven tests for lanes/root.sh's build window: housekeeping runs with
# the root's own hash and the cache volumes are ensured before the build; the
# EXIT trap alone releases the build container and the base pin, exactly once,
# on success, on a failed step, and on INT/TERM/HUP, whose exit codes are
# 130/143/129. docker, provisioning and housekeeping are stubs in a throwaway git
# repo, so no image is built and no container is started.
#
#   bash infra/fence/lanes/tests/root_test.sh
#   ROOT_SUT=/tmp/old/root.sh bash …/root_test.sh   # red-first
#
# BROKEN STATE: a missing SUT exits 2 before any assertion.
set -uo pipefail

LANES="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUT="${ROOT_SUT:-$LANES/root.sh}"
SHTEST_TAG=lane-root-test
# shellcheck source=/dev/null
source "$LANES/../../../scripts/shtest.sh"
[ -f "$SUT" ] || { echo "root_test: no root.sh at $SUT" >&2; exit 2; }

R="$T/repo-$BASHPID"
FAIL_DIR="/tmp/$(basename "$R")/lanes/root-failures"
# One EXIT trap for both scratch roots: a second trap would replace shtest's.
trap 'rm -rf -- "$T" "/tmp/$(basename "$R")" "/tmp/$(basename "$R")-wt"' EXIT
mkdir -p "$R/infra/fence/lanes"
cp "$SUT" "$R/infra/fence/lanes/root.sh"
cp "$LANES/container.sh" "$R/infra/fence/lanes/container.sh"
cp "$LANES/../fence-env.sh" "$R/infra/fence/fence-env.sh"
cp "$LANES/../image-key.sh" "$R/infra/fence/image-key.sh"
cp "$LANES/../mkdir-lock.sh" "$R/infra/fence/mkdir-lock.sh"
printf 'FROM scratch\n' >"$R/infra/fence/pfm-dev.Dockerfile"
printf 'services: {}\n' >"$R/infra/fence/docker-compose.yml"
shtest_compose_config "$R/infra/fence" >"$T/compose-config.yml"
export STUB_COMPOSE_CONFIG="$T/compose-config.yml"
cat >"$R/infra/fence/housekeeping.sh" <<'EOF'
fence_housekeeping() { echo "fence_housekeeping $*" >>"$STUB_DOCKER_LOG"; }
fence_volumes_ensure() { echo "fence_volumes_ensure" >>"$STUB_DOCKER_LOG"; }
EOF
for script in provision adopt cred-scan; do printf '#!/bin/bash\nexit 0\n' >"$R/infra/fence/lanes/$script.sh"; done
(cd "$R" && git init -q && git add -A && git -c user.email=t@t -c user.name=t commit -qm fixture)

# STUB_STEP1: what step 1's exec does — ok, fail (exit 7), or a signal name it
# sends to root.sh (its parent) before returning 0.
BIN="$T/bin"
mkdir -p "$BIN"
cat >"$BIN/docker" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$STUB_DOCKER_LOG"
case "$1" in
  compose)
    if [[ "$*" == *' config '* ]]; then
      cat "$STUB_COMPOSE_CONFIG"
    fi ;;
  image)
    if [ "${2:-}" = inspect ] && [ "${3:-}" = --format ]; then
      if [ "${STUB_PROBE_FAIL:-0}" = 1 ]; then echo 'fixture image probe error' >&2; exit 6; fi
      echo 'sha256:fixture-base-id'; exit 0
    fi
    [ -e "${STUB_ROOT_PRESENT:-/nonexistent}" ] && exit 0
    exit 1 ;; # root image inspect: no root for this hash yet
  inspect)
    if [ "${STUB_PROBE_FAIL:-0}" = 1 ]; then echo 'fixture inspect probe error' >&2; exit 6; fi
    echo 'fixture-build-container-id' ;;
  info)
    if [ "${STUB_PROBE_FAIL:-0}" = 1 ] && [ "${2:-}" = --format ]; then echo 'fixture info probe error' >&2; exit 6; fi
    case "$*" in
      *ServerVersion*) echo 'fixture-server-version' ;;
      *Driver*) echo 'fixture-storage-driver' ;;
    esac ;;
  ps)
    if [ "${STUB_PROBE_FAIL:-0}" = 1 ]; then echo 'fixture ps probe error' >&2; exit 6; fi
    echo 'fixture-fence-id fixture-fence-name fixture-created-at' ;;
  run) if [ "${STUB_RUN_FAIL:-0}" = 1 ]; then echo 'fixture run failure' >&2; exit 9; fi ;;
  commit)
    count="$(cat "$STUB_COMMIT_COUNT" 2>/dev/null || echo 0)"
    count=$((count + 1)); printf '%s\n' "$count" >"$STUB_COMMIT_COUNT"
    if [ "${STUB_COMMIT_FAIL:-0}" = 1 ]; then echo 'fixture commit daemon error' >&2; exit 17; fi
    if [ "$count" -le "${STUB_COMMIT_LOSSES:-0}" ]; then
      case "${STUB_COMMIT_MODE:-}" in
        create|near-create)
          if [ "$STUB_COMMIT_MODE" = create ]; then reason='no such file or directory'; else reason='input/output error'; fi
          echo "Error response from daemon: failed to export layer: CreateDiff: mount callback failed on /var/lib/desktop-containerd/daemon/tmpmounts/containerd-mount3073836047: mount callback failed on /var/lib/desktop-containerd/daemon/tmpmounts/containerd-mount2491854757: failed to commit: rename /var/lib/desktop-containerd/daemon/io.containerd.content.v1.content/ingest/326d610ace0d011d9a568ef39c8380be31c458b4acfa5c0b1aa501863f59e1e8/data /var/lib/desktop-containerd/daemon/io.containerd.content.v1.content/blobs/sha256/9d53aef03c0266b814e656a1361946cc1737cbab752e32c7ab1e9b7f4a5b6f00: $reason" >&2 ;;
        lchown)
          echo 'Error response from daemon: failed to apply diff: failed to Lchown "/var/lib/desktop-containerd/daemon/io.containerd.snapshotter.v1.overlayfs/snapshots/27301/fs/root/.local/state/pfm/harvest-python/models/hf/hub/models--docling-project--docling-models/blobs/2a7d6c924b3cd12fb99a09280ca9c33a89c5d60b93253617d2e088c1a40374d9" for UID 0, GID 0: lchown /var/lib/desktop-containerd/daemon/io.containerd.snapshotter.v1.overlayfs/snapshots/27301/fs/root/.local/state/pfm/harvest-python/models/hf/hub/models--docling-project--docling-models/blobs/2a7d6c924b3cd12fb99a09280ca9c33a89c5d60b93253617d2e088c1a40374d9: no such file or directory' >&2 ;;
        near-open) echo 'Error response from daemon: open /x: no such file or directory' >&2 ;;
      esac
      exit 1
    fi ;;
  exec)
    case "$*" in
      *"provision.sh tools"*)
        case "${STUB_STEP1:-ok}" in
          ok) ;;
          fail) echo 'fixture exec daemon error' >&2; exit 7 ;;
          *) kill -"$STUB_STEP1" "$PPID" ;;
        esac ;;
      *"cred-scan.sh"*)
        if [ "${STUB_REFUSE:-0}" = 1 ]; then echo 'cred-scan: ✗ CREDENTIAL-REFUSED /root/.credentials.json — not a registered fixture'; exit 1; fi ;;
      *"pfm ls --plain"*)
        if [ "${STUB_NONEMPTY:-0}" = 1 ]; then echo '● FIXTURE chat'; else echo 'no chats'; fi ;;
      *) cat >/dev/null 2>&1 || true ;;
    esac ;;
esac
exit 0
STUB
chmod +x "$BIN/docker"
export PATH="$BIN:$PATH" STUB_DOCKER_LOG="$T/docker.log" STUB_COMMIT_COUNT="$T/commit.count" STUB_CONTEXT="$R/infra/fence"

run_root() { # run_root STEP1 — sets RC and OUT
  : >"$STUB_DOCKER_LOG"
  rm -f "$STUB_COMMIT_COUNT"
  OUT="$(STUB_STEP1="$1" bash "$R/infra/fence/lanes/root.sh" --rebuild 2>&1 </dev/null)"
  RC=$?
  HASH="$(bash "$R/infra/fence/lanes/root.sh" --print-hash 2>/dev/null)"
}
build_name() { sed -n 's/^run -d --init --name \([^ ]*\) .*/\1/p' "$STUB_DOCKER_LOG"; }
pin_name() { sed -n 's/^tag professor-pfm-dev \([^ ]*\)$/\1/p' "$STUB_DOCKER_LOG"; }
released_once() { # only the names made by this invocation are removed
  local build pin
  build="$(build_name)"; pin="$(pin_name)"
  [ -n "$build" ] && [ -n "$pin" ] &&
    [ "$(grep -cx "rmi $pin" "$STUB_DOCKER_LOG")" -eq 1 ] &&
    [ "$(grep -cx "rm -f $build" "$STUB_DOCKER_LOG")" -eq 1 ] &&
    [ "$(grep -c '^rm -f ' "$STUB_DOCKER_LOG")" -eq 1 ] &&
    [ "$(grep -c '^rmi ' "$STUB_DOCKER_LOG")" -eq 1 ]
}
same_container_retry() {
  [ "$(grep '^commit ' "$STUB_DOCKER_LOG" | sort -u | wc -l)" -eq 1 ] &&
    awk '/^commit / { n++; if (n == 2) exit (other ? 1 : 0); next } n == 1 { other = 1 }' "$STUB_DOCKER_LOG"
}

# 1 — success: the image is the last stdout line, cleanup ran once
run_root ok
if [ "$RC" -eq 0 ] && [ "$(tail -1 <<<"$OUT")" = "pfm-lane-root:$HASH" ] && released_once &&
  [ "$(grep -c '^root: step [1-7]/7' <<<"$OUT")" -eq 7 ]; then
  ok "success: the root is committed and the EXIT trap alone releases the build, once"
else bad "success" "rc=$RC" "$OUT" "$(cat "$STUB_DOCKER_LOG")"; fi

# 2 — housekeeping gets the root's own hash, and the caches are ensured, before the build
first_build="$(grep -n '^compose ' "$STUB_DOCKER_LOG" | head -1 | cut -d: -f1)"
hk_line="$(grep -nx "fence_housekeeping $HASH" "$STUB_DOCKER_LOG" | head -1 | cut -d: -f1)"
ens_line="$(grep -nx 'fence_volumes_ensure' "$STUB_DOCKER_LOG" | head -1 | cut -d: -f1)"
if [ -n "$hk_line" ] && [ -n "$ens_line" ] && [ -n "$first_build" ] && [ "$hk_line" -lt "$first_build" ] && [ "$ens_line" -lt "$first_build" ]; then
  ok "housekeeping runs with the root's hash, and the caches are ensured, before the base build"
else bad "housekeeping before build" "$(cat "$STUB_DOCKER_LOG")"; fi
if grep -q -- '-v pfm-lane-harvest-cache:/root/.local/state/pfm/harvest-python/cache -v pfm-lane-uv-cache:/root/.cache/uv -e UV_CACHE_DIR=/root/.cache/uv -e UV_LINK_MODE=copy' "$STUB_DOCKER_LOG"; then
  ok "root build mounts only the harvest download and uv caches"
else bad "root build cache mounts" "$(grep '^run ' "$STUB_DOCKER_LOG")"; fi

# Two builds of one hash own distinct names; neither removes an older container.
first_build="$(build_name)"; first_pin="$(pin_name)"
run_root ok
second_build="$(build_name)"; second_pin="$(pin_name)"
if [ "$RC" -eq 0 ] && [ "$first_build" != "$second_build" ] && [ "$first_pin" != "$second_pin" ] &&
  [[ "$first_build" =~ ^pfm-lane-build-$HASH-[a-z0-9]+$ ]] &&
  [[ "$first_pin" =~ ^pfm-lane-base:$HASH-[a-z0-9]+$ ]] &&
  released_once && ! grep -qF "rm -f $first_build" "$STUB_DOCKER_LOG" &&
  ! grep -qF "rmi $first_pin" "$STUB_DOCKER_LOG"; then
  ok "two builds of one hash use distinct owned names and leave the other's names alone"
else bad "two-build ownership" "first=$first_build $first_pin second=$second_build $second_pin" "$(cat "$STUB_DOCKER_LOG")"; fi
run_line="$(grep -n '^run -d ' "$STUB_DOCKER_LOG" | head -1 | cut -d: -f1)"
rm_line="$(grep -n '^rm -f ' "$STUB_DOCKER_LOG" | head -1 | cut -d: -f1)"
if [ -n "$run_line" ] && [ -n "$rm_line" ] && [ "$rm_line" -gt "$run_line" ]; then
  ok "a build performs no blind pre-start container deletion"
else bad "blind pre-delete" "$(cat "$STUB_DOCKER_LOG")"; fi

# 3 — a failed step: exit 1, cleanup once
run_root fail
diag="$(sed -n 's/.*diagnostics: \([^ ]*\.txt\).*/\1/p' <<<"$OUT" | tail -1)"
if [ "$RC" -eq 1 ] && grep -q 'provision.sh tools failed (exit 7)' <<<"$OUT" && released_once &&
  [ -n "$diag" ] && [ -f "$diag" ] &&
  grep -q 'fixture exec daemon error' "$diag" &&
  grep -q 'fixture-build-container-id' "$diag" &&
  grep -q 'sha256:fixture-base-id' "$diag" &&
  grep -q 'fixture-server-version' "$diag" &&
  grep -q 'fixture-storage-driver' "$diag" &&
  grep -q 'fixture-fence-id' "$diag" &&
  grep -q "target image: pfm-lane-root:$HASH" "$diag"; then
  ok "a failed step exits 1 and the EXIT trap releases the build, once"
else bad "failed step" "rc=$RC" "$OUT" "$(cat "$STUB_DOCKER_LOG")"; fi

STUB_COMMIT_FAIL=1 run_root ok
diag="$(sed -n 's/.*diagnostics: \([^ ]*\.txt\).*/\1/p' <<<"$OUT" | tail -1)"
if [ "$RC" -eq 1 ] && grep -q 'docker commit failed (exit 17)' <<<"$OUT" &&
  [ "$(grep -c '^commit ' "$STUB_DOCKER_LOG")" -eq 1 ] &&
  [ -n "$diag" ] && [ -f "$diag" ] && grep -q 'fixture commit daemon error' "$diag" &&
  grep -q 'fixture commit daemon error' <<<"$OUT" && released_once; then
  ok "docker commit failure keeps the daemon error on disk before cleanup"
else bad "commit diagnostics" "rc=$RC" "$OUT" "$(cat "$STUB_DOCKER_LOG")"; fi

for mode in create lchown; do
  STUB_COMMIT_MODE="$mode" STUB_COMMIT_LOSSES=1 run_root ok
  diag="$(sed -n 's/.*diagnostics: \([^ ]*\.txt\).*/\1/p' <<<"$OUT" | head -1)"
  if [ "$RC" -eq 0 ] && [ "$(grep -c '^commit ' "$STUB_DOCKER_LOG")" -eq 2 ] &&
    same_container_retry &&
    [ "$(tail -1 <<<"$OUT")" = "pfm-lane-root:$HASH" ] &&
    grep -q 'root: commit attempt 1/3 lost its containerd lease — committing the same container again; diagnostics: ' <<<"$OUT" &&
    [ -f "$diag" ] && grep -q 'Error response from daemon:' "$diag" &&
    [[ "$diag" == *-attempt1.txt ]] && released_once; then
    ok "lease loss ($mode): the same container commits on attempt 2 with first-attempt diagnostics"
  else bad "lease loss ($mode)" "rc=$RC" "$OUT" "$(cat "$STUB_DOCKER_LOG")"; fi
done

STUB_COMMIT_MODE=create STUB_COMMIT_LOSSES=3 run_root ok
if [ "$RC" -eq 1 ] && [ "$(grep -c '^commit ' "$STUB_DOCKER_LOG")" -eq 3 ] &&
  [ "$(grep -c 'lost its containerd lease — committing the same container again' <<<"$OUT")" -eq 2 ] &&
  grep -q 'root: ✗ docker commit (attempt 3/3) failed (exit 1)' <<<"$OUT" &&
  grep -q -- '-attempt3.txt' <<<"$OUT" && released_once; then
  ok "lease loss on every attempt stops after three commits with last-attempt diagnostics"
else bad "lease retry bound" "rc=$RC" "$OUT" "$(cat "$STUB_DOCKER_LOG")"; fi

STUB_COMMIT_FAIL=1 run_root ok
if [ "$RC" -eq 1 ] && [ "$(grep -c '^commit ' "$STUB_DOCKER_LOG")" -eq 1 ] &&
  grep -q 'root: ✗ docker commit failed (exit 17)' <<<"$OUT" && released_once; then
  ok "other commit errors fail immediately without a retry"
else bad "other commit error retry" "rc=$RC" "$OUT" "$(cat "$STUB_DOCKER_LOG")"; fi

near_misses=0
for mode in near-create near-open; do
  STUB_COMMIT_MODE="$mode" STUB_COMMIT_LOSSES=1 run_root ok
  if [ "$RC" -eq 1 ] && [ "$(grep -c '^commit ' "$STUB_DOCKER_LOG")" -eq 1 ] && released_once; then
    near_misses=$((near_misses + 1))
  fi
done
if [ "$near_misses" -eq 2 ]; then ok "near-miss commit errors do not trigger a lease retry"
else bad "near-miss retry" "passed=$near_misses/2"; fi

STUB_PROBE_FAIL=1 run_root fail
diag="$(sed -n 's/.*diagnostics: \([^ ]*\.txt\).*/\1/p' <<<"$OUT" | tail -1)"
if [ "$RC" -eq 1 ] && [ -f "$diag" ] &&
  grep -q 'fixture inspect probe error' "$diag" &&
  grep -q 'fixture image probe error' "$diag" &&
  grep -q 'fixture info probe error' "$diag" &&
  grep -q 'fixture ps probe error' "$diag" && released_once; then
  ok "failed diagnostic probes write their own errors and preserve cleanup"
else bad "diagnostic probe errors" "rc=$RC" "$OUT"; fi

# An unwritable diagnostics destination is reported without changing the step's exit.
rm -rf "$FAIL_DIR"
mkdir -p "$(dirname "$FAIL_DIR")"
printf 'fixture obstruction\n' >"$FAIL_DIR"
run_root fail
if [ "$RC" -eq 1 ] && grep -q 'diagnostics could not be written:' <<<"$OUT" && released_once; then
  ok "a diagnostics write failure is named and leaves the step exit at 1"
else bad "unwritable diagnostics" "rc=$RC" "$OUT"; fi
rm -f "$FAIL_DIR"

# A failed container start is fatal (2), with the owned pin and name cleaned once.
STUB_RUN_FAIL=1 run_root ok
if [ "$RC" -eq 2 ] && grep -q 'the fence container would not start' <<<"$OUT" && released_once; then
  ok "fatal start failure cleans only this build's names once"
else bad "fatal cleanup" "rc=$RC" "$OUT"; fi

# 4 — signals: INT → 130, TERM → 143, HUP → 129, cleanup once each
for pair in INT:130 TERM:143 HUP:129; do
  run_root "${pair%%:*}"
  if [ "$RC" -eq "${pair#*:}" ] && released_once; then ok "SIG${pair%%:*} exits ${pair#*:} and releases the build, once"
  else bad "SIG${pair%%:*}" "rc=$RC want ${pair#*:}" "$(cat "$STUB_DOCKER_LOG")"; fi
done

# 5 — a contaminated root is never committed, and is always removed.
STUB_REFUSE=1 run_root ok
if [ "$RC" -eq 1 ] && grep -q '^root: ✗ .*CREDENTIAL-REFUSED /root/.credentials.json' <<<"$OUT" &&
  ! grep -q '^commit ' "$STUB_DOCKER_LOG" && released_once; then ok "credential refusal stops before commit and removes the build"
else bad "credential gate" "rc=$RC" "$OUT"; fi
STUB_NONEMPTY=1 run_root ok
if [ "$RC" -eq 1 ] && grep -q 'EMPTY fleet' <<<"$OUT" && ! grep -q '^commit ' "$STUB_DOCKER_LOG" && released_once; then ok "a populated fleet cannot become a root"
else bad "empty fleet gate" "rc=$RC" "$OUT"; fi

# 6 — the hash follows on-disk build content, never index or commit state.
H="$T/hash-repo"
mkdir -p "$H/infra/fence/lanes" "$H/infra/fence/jscpd" "$H/pfm/internal/alpha/testdata" "$H/pfm/testdata" "$H/pfm/e2e" "$H/pfm/internal/harvestpy/assets"
cp "$SUT" "$H/infra/fence/lanes/root.sh"
cp "$LANES/container.sh" "$H/infra/fence/lanes/container.sh"
cp "$LANES/../fence-env.sh" "$H/infra/fence/fence-env.sh"
cp "$LANES/../image-key.sh" "$H/infra/fence/image-key.sh"
cp "$LANES/../mkdir-lock.sh" "$H/infra/fence/mkdir-lock.sh"
cp "$R/infra/fence/housekeeping.sh" "$H/infra/fence/housekeeping.sh"
for script in provision adopt cred-scan; do cp "$R/infra/fence/lanes/$script.sh" "$H/infra/fence/lanes/$script.sh"; done
for file in E1.sh lib.sh beats.md; do printf 'fixture\n' >"$H/infra/fence/lanes/$file"; done
printf 'module example.test/pfm\n' >"$H/pfm/go.mod"
printf '{}\n' >"$H/infra/fence/jscpd/package-lock.json"
printf 'test\n' >"$H/pfm/internal/alpha/x_test.go"
printf 'fixture\n' >"$H/pfm/internal/alpha/testdata/input"
printf 'fixture\n' >"$H/pfm/testdata/input"
printf 'e2e\n' >"$H/pfm/e2e/example.go"
printf 'test\n' >"$H/pfm/internal/harvestpy/assets/example_test.py"
(cd "$H" && git init -q && git add -A && git -c user.email=t@t -c user.name=t commit -qm fixture)
base="$(bash "$H/infra/fence/lanes/root.sh" --print-hash)"
for file in infra/fence/lanes/E1.sh infra/fence/lanes/lib.sh infra/fence/lanes/beats.md infra/fence/lanes/Z.sh infra/fence/lanes/provision.sh infra/fence/jscpd/package-lock.json pfm/go.mod pfm/internal/alpha/x_test.go pfm/internal/alpha/testdata/input pfm/testdata/input pfm/e2e/example.go pfm/internal/harvestpy/assets/example_test.py; do
  printf 'edit\n' >>"$H/$file"
  changed="$(bash "$H/infra/fence/lanes/root.sh" --print-hash)"
  case "$file" in
    infra/fence/lanes/provision.sh|infra/fence/jscpd/package-lock.json|pfm/go.mod) expected=different ;;
    *) expected=equal ;;
  esac
  if { [ "$expected" = equal ] && [ "$changed" = "$base" ]; } ||
     { [ "$expected" = different ] && [ "$changed" != "$base" ]; }; then
    ok "hash $file is $expected after edit"
  else
    bad "hash $file should be $expected after edit" "base=$base" "changed=$changed"
  fi
  case "$file" in
    infra/fence/lanes/Z.sh) rm "$H/$file" ;;
    *) git -C "$H" checkout -- "$file" ;;
  esac
done
printf 'package alpha\n' >"$H/pfm/internal/alpha/alpha.go"
untracked="$(bash "$H/infra/fence/lanes/root.sh" --print-hash)"
if [ "$untracked" != "$base" ]; then ok "hash changes for an untracked non-test Go file"
else bad "hash ignores untracked build input" "base=$base untracked=$untracked"; fi
rm "$H/pfm/internal/alpha/alpha.go"
ln -s missing-target "$H/pfm/dangling"
dangling="$(bash "$H/infra/fence/lanes/root.sh" --print-hash 2>&1)"; RC=$?
ln -sfn other-target "$H/pfm/dangling"
retargeted="$(bash "$H/infra/fence/lanes/root.sh" --print-hash 2>&1)"
rm "$H/pfm/dangling"
if [ "$RC" -eq 0 ] && [ "$dangling" != "$base" ] && [ "$retargeted" != "$dangling" ]; then
  ok "an untracked dangling symlink hashes by its target text"
else bad "dangling symlink input" "rc=$RC base=$base dangling=$dangling retargeted=$retargeted"; fi
ln -s internal "$H/pfm/dirlink"
dirlink="$(bash "$H/infra/fence/lanes/root.sh" --print-hash 2>&1)"; RC=$?
rm "$H/pfm/dirlink"
if [ "$RC" -eq 0 ] && [ "$dirlink" != "$base" ]; then ok "an untracked symlink to a directory hashes by its target text"
else bad "directory symlink input" "rc=$RC base=$base dirlink=$dirlink"; fi
printf 'changed\n' >>"$H/pfm/go.mod"
dirty="$(bash "$H/infra/fence/lanes/root.sh" --print-hash)"
git -C "$H" add pfm/go.mod
staged="$(bash "$H/infra/fence/lanes/root.sh" --print-hash)"
git -C "$H" -c user.email=t@t -c user.name=t commit -qm changed
committed="$(bash "$H/infra/fence/lanes/root.sh" --print-hash)"
if [ "$dirty" != "$base" ] && [ "$dirty" = "$staged" ] && [ "$staged" = "$committed" ]; then
  ok "dirty, staged and committed build content has one hash"
else bad "hash depends on index or commit state" "base=$base dirty=$dirty staged=$staged committed=$committed"; fi
# 6b — the engine-sync source the root builds its binary from is a hash input; its tests are not.
ES="$T/es-src"
mkdir -p "$ES/cmd/engine-sync" "$ES/internal/x/testdata"
printf 'module example.test/engine-sync\n' >"$ES/go.mod"
printf 'package main\n' >"$ES/cmd/engine-sync/main.go"
printf 'package x\n' >"$ES/internal/x/x_test.go"
printf 'fixture\n' >"$ES/internal/x/testdata/input"
(cd "$ES" && git init -q && git add -A && git -c user.email=t@t -c user.name=t commit -qm fixture)
es_base="$(ENGINE_SYNC_SRC="$ES" bash "$H/infra/fence/lanes/root.sh" --print-hash)"
for file in cmd/engine-sync/main.go go.mod internal/x/x_test.go internal/x/testdata/input; do
  printf 'edit\n' >>"$ES/$file"
  changed="$(ENGINE_SYNC_SRC="$ES" bash "$H/infra/fence/lanes/root.sh" --print-hash)"
  case "$file" in *_test.go|*/testdata/*) expected=equal ;; *) expected=different ;; esac
  if { [ "$expected" = equal ] && [ "$changed" = "$es_base" ]; } ||
     { [ "$expected" = different ] && [ "$changed" != "$es_base" ]; }; then
    ok "hash engine-sync $file is $expected after edit"
  else bad "hash engine-sync $file should be $expected after edit" "base=$es_base" "changed=$changed"; fi
  git -C "$ES" checkout -- "$file"
done
printf 'package main\n' >"$ES/cmd/engine-sync/extra.go"
es_untracked="$(ENGINE_SYNC_SRC="$ES" bash "$H/infra/fence/lanes/root.sh" --print-hash)"
rm "$ES/cmd/engine-sync/extra.go"
if [ "$es_untracked" != "$es_base" ] && [ "$es_base" != "$base" ]; then
  ok "an untracked engine-sync source file and the source's presence both change the hash"
else bad "engine-sync source presence" "base=$base es_base=$es_base es_untracked=$es_untracked"; fi
# 6c — each plugin checkout pfm install copies into the image is a hash input, as the
# copy takes it: ignored files count, node_modules does not, a missing checkout is absent.
PC="$H/plugins/demo/plugins/demo"
pc_none="$(bash "$H/infra/fence/lanes/root.sh" --print-hash)"
mkdir -p "$H/plugins/demo"
pc_empty="$(bash "$H/infra/fence/lanes/root.sh" --print-hash)"
mkdir -p "$PC/.claude-plugin" "$PC/node_modules/dep"
printf '{"name":"demo"}\n' >"$PC/.claude-plugin/plugin.json"
pc_base="$(bash "$H/infra/fence/lanes/root.sh" --print-hash)"
printf 'dep\n' >"$PC/node_modules/dep/index.js"
pc_nm="$(bash "$H/infra/fence/lanes/root.sh" --print-hash)"
printf 'types/\n' >"$PC/.gitignore"
pc_gi="$(bash "$H/infra/fence/lanes/root.sh" --print-hash)"
mkdir -p "$PC/types"; printf 'generated\n' >"$PC/types/index.d.ts"
pc_ignored="$(bash "$H/infra/fence/lanes/root.sh" --print-hash)"
printf 'edit\n' >>"$PC/.claude-plugin/plugin.json"
pc_edit="$(bash "$H/infra/fence/lanes/root.sh" --print-hash)"
rm -rf "$H/plugins"
if [ "$pc_empty" != "$pc_none" ] && [ "$pc_base" != "$pc_empty" ] && [ "$pc_nm" = "$pc_base" ] &&
  [ "$pc_ignored" != "$pc_gi" ] && [ "$pc_edit" != "$pc_ignored" ]; then
  ok "a plugin checkout's copied files, ignored ones included, change the hash; node_modules does not; a missing checkout hashes as absent"
else bad "plugin checkout hash input" "none=$pc_none empty=$pc_empty base=$pc_base nm=$pc_nm gi=$pc_gi ignored=$pc_ignored edit=$pc_edit"; fi
HASH_BIN="$T/hash-bin"; mkdir -p "$HASH_BIN"
cat >"$HASH_BIN/git" <<'EOF'
#!/usr/bin/env bash
case "$*" in *ls-files*) echo 'listing refused (fixture)' >&2; exit 128 ;; esac
exec /usr/bin/git "$@"
EOF
chmod +x "$HASH_BIN/git"
OUT="$(PATH="$HASH_BIN:$PATH" bash "$H/infra/fence/lanes/root.sh" --print-hash 2>&1)"; RC=$?
if [ "$RC" -eq 2 ] && grep -q 'HASH-UNDERIVABLE.*listing refused' <<<"$OUT"; then ok "a failed git listing refuses image reuse"
else bad "failed git listing" "rc=$RC out=$OUT"; fi
for flag in --accounts --no-adopt; do
  case "$flag" in --accounts) set -- "$flag" 1 ;; *) set -- "$flag" ;; esac
  OUT="$(bash "$R/infra/fence/lanes/root.sh" "$@" 2>&1)"; RC=$?
  if [ "$RC" -eq 2 ] && grep -q '^usage: root.sh' <<<"$OUT"; then ok "unsupported root input $flag reports usage"
  else bad "invalid root input $flag" "rc=$RC" "$OUT"; fi
done
# One root build per hash at a time: a build holds /tmp/{project}/lanes/root-build-<hash>.lock over the
# inspect and the build, so two concurrent builds never race on one tag or the shared cache volumes. A
# waiter that finds the image once the lock is free reuses it; a dead holder's lock is broken.
HASH="$(bash "$R/infra/fence/lanes/root.sh" --print-hash 2>/dev/null)"
LK="/tmp/$(basename "$R")/lanes/root-build-$HASH.lock"
mkdir -p "$LK"
sleep 30 &
holder=$!
printf '%s\n' "$holder" >"$LK/pid"
: >"$STUB_DOCKER_LOG"
STUB_ROOT_PRESENT="$T/root.present" bash "$R/infra/fence/lanes/root.sh" >"$T/wait.out" 2>&1 </dev/null &
waiter=$!
for ((i = 0; i < 50; i++)); do grep -q 'waiting for the root build' "$T/wait.out" 2>/dev/null && break; sleep 0.1; done
waited="$(grep -c 'waiting for the root build' "$T/wait.out")"
: >"$T/root.present"
rm -rf "$LK"
wait "$waiter"; wrc=$?
ps -o args= -p "$holder" 2>/dev/null | grep -qF 'sleep 30' && kill "$holder" 2>/dev/null
if [ "$waited" -ge 1 ] && [ "$wrc" -eq 0 ] && grep -q "^root: reusing pfm-lane-root:$HASH" "$T/wait.out" &&
  ! grep -qE '^(run -d|commit) ' "$STUB_DOCKER_LOG" && [ ! -e "$LK" ]; then
  ok "a root build waits for a concurrent build of its hash, then reuses the image it made and leaves no lock"
else bad "concurrent root build" "waited=$waited rc=$wrc" "$(cat "$T/wait.out")" "$(cat "$STUB_DOCKER_LOG")"; fi
rm -f "$T/root.present"
mkdir -p "$LK"
printf '999999\n' >"$LK/pid"
run_root ok
if [ "$RC" -eq 0 ] && grep -q '^commit ' "$STUB_DOCKER_LOG" && [ ! -e "$LK" ]; then
  ok "a dead holder's root build lock is broken, the build runs and releases its lock"
else bad "dead holder lock" "rc=$RC" "$OUT"; fi
# The lock is the machine's, not the worktree's: a build from a linked worktree of the repo waits on the
# lock under the main checkout's /tmp/{project}/lanes, so two worktrees on one hash never build one tag at once.
WT="$R-wt"
git -C "$R" worktree add -q "$WT" 2>/dev/null || bad "cross-worktree lock: git worktree add failed" "$WT"
WHASH="$(bash "$WT/infra/fence/lanes/root.sh" --print-hash 2>/dev/null)"
LK="/tmp/$(basename "$R")/lanes/root-build-$WHASH.lock"
mkdir -p "$LK"
sleep 30 &
holder=$!
printf '%s\n' "$holder" >"$LK/pid"
: >"$STUB_DOCKER_LOG"
STUB_ROOT_PRESENT="$T/root.present" bash "$WT/infra/fence/lanes/root.sh" >"$T/wtwait.out" 2>&1 </dev/null &
waiter=$!
for ((i = 0; i < 50; i++)); do grep -q 'waiting for the root build' "$T/wtwait.out" 2>/dev/null && break; sleep 0.1; done
waited="$(grep -c "waiting for the root build of $WHASH held by pid $holder ($LK)" "$T/wtwait.out")"
: >"$T/root.present"
rm -rf "$LK"
wait "$waiter"; wrc=$?
ps -o args= -p "$holder" 2>/dev/null | grep -qF 'sleep 30' && kill "$holder" 2>/dev/null
rm -f "$T/root.present"
if [ -n "$WHASH" ] && [ "$waited" -ge 1 ] && [ "$wrc" -eq 0 ] && grep -q "^root: reusing pfm-lane-root:$WHASH" "$T/wtwait.out" && [ ! -e "$LK" ]; then
  ok "a root build from a linked worktree waits on the main checkout's lock for its hash, then reuses the image"
else bad "cross-worktree root build lock" "hash=$WHASH waited=$waited rc=$wrc" "$(cat "$T/wtwait.out")"; fi

shtest_end
