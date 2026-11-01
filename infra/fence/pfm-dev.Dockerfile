# pfm-dev — the ONE dev+e2e image: a fresh machine per run for building and
# testing Professor's compiled projects inside the fence
# (docs/dev/isolated-dev-foundation.md).
# The worktree is edited on the HOST; this container only builds and tests it.
# Two targets over one base: `pfm-sim` (the real-simulation fence: the base plus
# a real Google Chrome and a display) and `pfm-dev` (build, test, e2e). pfm-dev
# is the LAST stage, so an untargeted `docker build` of this file still
# produces pfm-dev, never the Chrome image.
FROM ubuntu:24.04@sha256:008173c23f95b170204355c12626cb5a965d779a7e1283b09e9cffbb1bf33ca3 AS pfm-base
# The converter sidecar's OCR stack (OpenCV) loads these system libraries on
# import, and `pfm install` stages its models on any fence image. gcc and
# libc6-dev are the C toolchain `go test -race` needs (the race runtime is
# cgo): CGO_ENABLED stays 0 below, so every build stays static, and the gate's
# race step (infra/fence/checks.sh checks_pfm_race) turns cgo on for itself.
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl git jq make zsh tmux python3 python3-yaml xz-utils rsync sqlite3 tcpdump iproute2 \
    libgl1 libglib2.0-0 libxcb1 \
    gcc libc6-dev \
 && rm -rf /var/lib/apt/lists/*
# Go pinned to pfm/go.mod — bump both together or the fence tests a different compiler.
ARG GO_VERSION=1.27.1
ARG TARGETARCH
RUN case "${TARGETARCH}" in amd64|arm64) ;; *) echo "unsupported build architecture: ${TARGETARCH}" >&2; exit 1 ;; esac \
 && curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${TARGETARCH}.tar.gz" | tar -C /usr/local -xz
# shellcheck, rumdl and Node: the pinned, checksum-verified installs CI's Linux gate jobs run too
# (infra/fence/pinned-tools.sh holds every version and digest).
COPY pinned-tools.sh /opt/pfm-tools/
RUN bash /opt/pfm-tools/pinned-tools.sh /usr/local shellcheck rumdl node
ENV HOME=/root \
    LANG=C.UTF-8 \
    PATH=/usr/local/go/bin:/root/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    CGO_ENABLED=0
# Pinned developer tools (infra/fence/tools.env) — the same versions `make tools`
# installs on the host, so a lint verdict is the same on both sides of the fence.
COPY tools.env tools.sh /opt/pfm-tools/
COPY jscpd/ /opt/pfm-tools/jscpd/
# A network that blocks registry.npmjs.org names its mirror in PFM_NPM_REGISTRY;
# npm rewrites the lockfile's registry.npmjs.org URLs to it, integrity unchanged.
ARG PFM_NPM_REGISTRY=
RUN if [ -n "$PFM_NPM_REGISTRY" ]; then export NPM_CONFIG_REGISTRY="$PFM_NPM_REGISTRY"; fi \
 && TOOLS_BIN=/usr/local/bin bash /opt/pfm-tools/tools.sh
WORKDIR /worktree
# Every image built from this file, by any builder (compose, a plain
# `docker build`, a lane root committed from a container of it), carries
# the fence label; fence housekeeping prunes dangling images by it
# (infra/fence/housekeeping.sh).
LABEL pfm.fence=1

# pfm-sim — the real-simulation fence: the base plus what a real
# desktop brings to live traffic — Google Chrome (patchright's `chrome` channel
# launches only Google Chrome; Chromium reports MISSING by design) and real
# fonts so pages render as they do for a person; the browser runs headless
# only, so the image carries no display. `dev.sh iso sim`
# is the entry point. Google's apt repo serves amd64 and arm64 and keeps only
# the current release, so Chrome is the one unpinned tool here: the image
# prints its version at build time and every sim run prints it in the proof line.
FROM pfm-base AS pfm-sim
RUN apt-get update && apt-get install -y --no-install-recommends \
    fonts-liberation fonts-noto-core fonts-noto-cjk fonts-noto-color-emoji \
 && install -d -m 0755 /etc/apt/keyrings \
 && curl -fsSL https://dl.google.com/linux/linux_signing_key.pub -o /etc/apt/keyrings/google-chrome.asc \
 && echo "deb [signed-by=/etc/apt/keyrings/google-chrome.asc] https://dl.google.com/linux/chrome/deb/ stable main" \
      > /etc/apt/sources.list.d/google-chrome.list \
 && apt-get update && apt-get install -y --no-install-recommends google-chrome-stable \
 && rm -rf /var/lib/apt/lists/* \
 && google-chrome-stable --version

# pfm-dev carries the real headless Herdr used by the native-seat lane.
FROM pfm-base AS pfm-dev
RUN bash /opt/pfm-tools/pinned-tools.sh /usr/local herdr
