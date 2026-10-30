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
# shellcheck is the linter the plugins' scripts/gate.sh runs over their shell
# scripts. Pinned to the release those gates pin (gpt-gateway SHELLCHECK_VERSION):
# apt's older release reports different findings. Each tarball is verified
# against its recorded checksum before extracting.
ARG SHELLCHECK_VERSION=0.11.0
RUN case "${TARGETARCH}" in \
      amd64) sc_arch=x86_64; sc_sum=8c3be12b05d5c177a04c29e3c78ce89ac86f1595681cab149b65b97c4e227198 ;; \
      arm64) sc_arch=aarch64; sc_sum=12b331c1d2db6b9eb13cfca64306b1b157a86eb69db83023e261eaa7e7c14588 ;; \
      *) echo "unsupported build architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac \
 && curl -fsSL -o /tmp/shellcheck.tar.xz "https://github.com/koalaman/shellcheck/releases/download/v${SHELLCHECK_VERSION}/shellcheck-v${SHELLCHECK_VERSION}.linux.${sc_arch}.tar.xz" \
 && echo "${sc_sum}  /tmp/shellcheck.tar.xz" | sha256sum -c - \
 && tar -C /tmp -xJf /tmp/shellcheck.tar.xz \
 && install -m 0755 "/tmp/shellcheck-v${SHELLCHECK_VERSION}/shellcheck" /usr/local/bin/shellcheck \
 && rm -rf /tmp/shellcheck.tar.xz "/tmp/shellcheck-v${SHELLCHECK_VERSION}"
# rumdl is the Markdown linter .claude/scripts/test.sh runs on every named .md file, pinned to
# the release `pfm install` installs on a host (pfm/internal/installer/markdown_tool.go
# rumdlPinnedVersion): bump both together. The static musl build, verified against the
# release's recorded digest before extracting.
ARG RUMDL_VERSION=0.2.73
RUN case "${TARGETARCH}" in \
      amd64) rd_arch=x86_64; rd_sum=dc4aa9bf98f936f667039224a34fc2e26ddf6be3589bf59d63e4d6b038be4f5c ;; \
      arm64) rd_arch=aarch64; rd_sum=f51548adfc71c4982cb5cc239d847bcd4c884e6ac8dc67efd68efd3eea9753a4 ;; \
      *) echo "unsupported build architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac \
 && curl -fsSL -o /tmp/rumdl.tar.gz "https://github.com/rvben/rumdl/releases/download/v${RUMDL_VERSION}/rumdl-v${RUMDL_VERSION}-${rd_arch}-unknown-linux-musl.tar.gz" \
 && echo "${rd_sum}  /tmp/rumdl.tar.gz" | sha256sum -c - \
 && mkdir /tmp/rumdl && tar -C /tmp/rumdl -xzf /tmp/rumdl.tar.gz \
 && install -m 0755 "$(find /tmp/rumdl -type f -name rumdl | head -n 1)" /usr/local/bin/rumdl \
 && rm -rf /tmp/rumdl.tar.gz /tmp/rumdl \
 && rumdl --version | grep -F "${RUMDL_VERSION}"
# Node pinned to the mirror compilers' minimum supported release. Verify the selected
# architecture's tarball against the release checksum before extracting it.
ARG NODE_VERSION=22.13.1
RUN case "${TARGETARCH}" in amd64) node_arch=x64 ;; arm64) node_arch=arm64 ;; esac \
 && node_tar="node-v${NODE_VERSION}-linux-${node_arch}.tar.xz" \
 && curl -fsSLO "https://nodejs.org/dist/v${NODE_VERSION}/${node_tar}" \
 && curl -fsSLO "https://nodejs.org/dist/v${NODE_VERSION}/SHASUMS256.txt" \
 && grep " ${node_tar}$" SHASUMS256.txt | sha256sum -c - \
 && tar -C /usr/local --strip-components=1 -xJf "${node_tar}" \
 && rm -f "${node_tar}" SHASUMS256.txt
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
ARG HERDR_VERSION=0.9.5
ARG TARGETARCH
RUN case "${TARGETARCH}" in \
      amd64) herdr_arch=x86_64; herdr_sum=14d9e8bc8b4a9e0118d77c32266f76d461f09b40342eb23752960bd89f59929d ;; \
      arm64) herdr_arch=aarch64; herdr_sum=17d7536f2da50859523680ef279dffcac8e4a235f4e46fe8da6c2ec44141897c ;; \
      *) echo "unsupported Herdr architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac \
 && curl -fsSL -o /tmp/herdr "https://github.com/herdrdev/herdr/releases/download/v${HERDR_VERSION}/herdr-linux-${herdr_arch}" \
 && echo "${herdr_sum}  /tmp/herdr" | sha256sum -c - \
 && install -m 0755 /tmp/herdr /usr/local/bin/herdr \
 && rm /tmp/herdr \
 && herdr --version
