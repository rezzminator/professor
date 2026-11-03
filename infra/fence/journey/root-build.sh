#!/usr/bin/env bash
# Build OS layers and tools only in the run's fence namespace, with cache locks.
set -euo pipefail
DEST="$1" PREVIOUS="$2" HASH="$3"
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
CACHE=/pfm-timing/journey-roots
BUILD="$PFM_FENCE_DISK_DIR/journey-build"
ROOTFS="$BUILD/rootfs"
LOG="$DEST/build.log"
T0="$SECONDS"
mkdir -p "$BUILD" "$ROOTFS"
trap 'rc=$?; printf "journey-root: FAIL line=%s exit=%s log=%s\n" "$LINENO" "$rc" "$LOG" >&2; tail -n 35 "$LOG" >&2; exit "$rc"' ERR
exec 3>&1
exec >>"$LOG" 2>&1
printf 'journey-root: building %s\n' "$HASH" >&3
SYSTEM_PACKAGES=(systemd systemd-sysv dbus dbus-user-session libpam-systemd openssh-server python3-pyte)
OS_KEY="$(sha256sum /opt/pfm-tools/tools.env /worktree/infra/fence/pfm-dev.Dockerfile "$HERE/root-build.sh" | sha256sum | cut -c1-20)"
OS="$CACHE/os-$OS_KEY.tar"
if [ -s "$OS" ]; then
  tar -C "$ROOTFS" -xf "$OS"
else
  # Copy only the credential-free image filesystem, never a run HOME or source mount.
  tar -C / --exclude=usr/local/go --exclude=var/fence --exclude=var/cache/apt --exclude=var/lib/apt/lists \
    -cf - usr etc var bin sbin lib lib64 | tar -C "$ROOTFS" -xf -
  mkdir -p "$ROOTFS"/{dev,proc,sys,run,tmp,root,worktree,engine-sync,j,cache}
  chmod 1777 "$ROOTFS/tmp"
  APT="$CACHE/apt"
  mkdir -p "$APT/lists/partial" "$APT/archives/partial"
  APT_ARGS=(-o "Dir::State::lists=$APT/lists" -o "Dir::Cache::archives=$APT/archives" -o Debug::NoLocking=1)
  apt-get "${APT_ARGS[@]}" update
  apt-get "${APT_ARGS[@]}" --yes --download-only --no-install-recommends install "${SYSTEM_PACKAGES[@]}"
  for package in "$APT/archives/"*.deb; do dpkg-deb -x "$package" "$ROOTFS"; done
  if chroot "$ROOTFS" id -u ubuntu >/dev/null 2>&1; then chroot "$ROOTFS" userdel ubuntu; fi
  chroot "$ROOTFS" useradd -m -d /j/base/home -u 1000 -s /usr/bin/zsh journey
  if ! chroot "$ROOTFS" id -u sshd >/dev/null 2>&1; then
    chroot "$ROOTFS" useradd --system --no-create-home --home-dir /run/sshd --shell /usr/sbin/nologin sshd
  fi
  mkdir -p "$ROOTFS/var/lib/systemd/linger" "$ROOTFS/etc/systemd/system"
  ln -sfn /usr/lib/systemd/system/multi-user.target "$ROOTFS/etc/systemd/system/default.target"
  : >"$ROOTFS/etc/machine-id"
  tar -C "$ROOTFS" -cf "$OS.next" .
  mv "$OS.next" "$OS"
fi
TOOLS="$ROOTFS/usr/local/libexec/pfm-journey"
rm -f "$ROOTFS/var/lib/systemd/linger/journey"
cp -a /usr/local/go "$ROOTFS/usr/local/go"
mkdir -p "$ROOTFS/etc/claude-code/managed-settings.d"
printf '%s\n' '{"cleanupPeriodDays":36500,"prependPlugins":[]}' >"$ROOTFS/etc/claude-code/managed-settings.d/pfm.json"
mkdir -p "$TOOLS" "$ROOTFS/opt/pfm-journey/previous/source" "$ROOTFS/opt/pfm-journey/current/source"
git --git-dir="$PFM_DEV_REPO_GIT_DIR" --work-tree=/worktree archive "$PREVIOUS" |
  tar -C "$ROOTFS/opt/pfm-journey/previous/source" --exclude='.credentials.json' -xf -
tar -C /worktree --exclude=.git --exclude=.worktrees --exclude='*/target' --exclude='*/node_modules' --exclude='./docs' \
  --exclude='./.codex' --exclude='./.opencode' --exclude='./tmp' --exclude='.credentials.json' --exclude='.env' --exclude='settings.local.json' -cf - . |
  tar -C "$ROOTFS/opt/pfm-journey/current/source" -xf -
CURRENT="$ROOTFS/opt/pfm-journey/current/source"
GOTOOLCHAIN=local go -C "$CURRENT/pfm" build -ldflags "-X main.version=$(cat "$CURRENT/VERSION")" -o "$TOOLS/pfm-under-test" ./cmd/pfm
GOTOOLCHAIN=local go -C "$ROOTFS/opt/pfm-journey/previous/source/pfm" build \
  -ldflags "-X main.version=$(cat "$ROOTFS/opt/pfm-journey/previous/source/VERSION")" -o "$TOOLS/pfm-previous" ./cmd/pfm
GOTOOLCHAIN=local go -C "$CURRENT/pfm" build -o "$TOOLS/mock-engine" ./cmd/mock-engine
. /worktree/infra/fence/engine-sync.sh
engine_sync_build "$TOOLS/engine-sync"
# Runtime installers build shipped Go plugins without network access.
while IFS= read -r manifest; do
  GOTOOLCHAIN=local GOMODCACHE="$CACHE/plugin-modules" go -C "$(dirname "$manifest")" mod download
done < <(find "$CURRENT/plugins" -name go.mod -type f)
cp -a "$CACHE/plugin-modules" "$ROOTFS/opt/pfm-journey/gomodcache"
chown -R 1000:1000 "$ROOTFS/opt/pfm-journey/gomodcache"
SKILL_REV="$(jq -er '.["god-speed"].revision' "$CURRENT/infra/fence/journey/fixtures/external-skills.json")"
SKILL_URL="$(python3 "$HERE/projects.py" skill-source "$CURRENT")"
if [ ! -d "$CACHE/god-speed.git" ]; then GIT_TERMINAL_PROMPT=0 git clone --mirror "$SKILL_URL" "$CACHE/god-speed.git"; fi
if ! git --git-dir="$CACHE/god-speed.git" cat-file -e "$SKILL_REV^{commit}"; then
  GIT_TERMINAL_PROMPT=0 git --git-dir="$CACHE/god-speed.git" fetch "$SKILL_URL" "$SKILL_REV"
fi
mkdir -p "$ROOTFS/opt/pfm-journey/external"
cp -a "$CACHE/god-speed.git" "$ROOTFS/opt/pfm-journey/external/god-speed.git"
git --git-dir="$ROOTFS/opt/pfm-journey/external/god-speed.git" update-ref refs/heads/journey "$SKILL_REV"
git --git-dir="$ROOTFS/opt/pfm-journey/external/god-speed.git" symbolic-ref HEAD refs/heads/journey
chown -R 1000:1000 "$ROOTFS/opt/pfm-journey/external/god-speed.git"
# Rust is pinned by the dependency's own toolchain file; downloads are build-only.
export RUSTUP_HOME="$CACHE/rustup" CARGO_HOME="$CACHE/cargo"
RUST_VERSION="$(sed -n 's/^channel = "\([^"]*\)"/\1/p' /worktree/plugins/herdr-shield/rust-toolchain.toml)"
if [ ! -x "$CARGO_HOME/bin/rustup" ]; then
  curl --fail --silent --show-error https://sh.rustup.rs -o "$BUILD/rustup.sh"
  sh "$BUILD/rustup.sh" -y --profile minimal --no-modify-path --default-toolchain "$RUST_VERSION"
fi
export PATH="$CARGO_HOME/bin:$PATH" CARGO_TARGET_DIR="$CACHE/shield-target"
cargo +"$RUST_VERSION" build --release --locked --manifest-path "$CURRENT/plugins/herdr-shield/Cargo.toml"
install -m 0755 "$CARGO_TARGET_DIR/release/herdr-shield" "$TOOLS/herdr-shield"
install -m 0644 "$CURRENT/plugins/herdr-shield/herdr-plugin.toml" "$TOOLS/herdr-plugin.toml"
for engine in claude codex opencode; do ln -sfn /usr/local/libexec/pfm-journey/mock-engine "$ROOTFS/usr/local/bin/$engine"; done
# No mounted repository metadata or runtime credentials enter the immutable image.
tar -C "$ROOTFS" -cf "$DEST/root.tar.next" .
mv "$DEST/root.tar.next" "$DEST/root.tar"
python3 - "$DEST/manifest.json" "$HASH" "$((SECONDS - T0))" <<'PY'
import json, pathlib, sys
pathlib.Path(sys.argv[1]).write_text(json.dumps({'root':sys.argv[2],'build_wall_s':int(sys.argv[3]),'os':'linux','boot':'systemd'}, sort_keys=True) + '\n')
PY
printf 'journey-root: PASS build wall_s=%s\n' "$((SECONDS - T0))" >&3
