# pfm-dev — the ONE dev+e2e image: a fresh machine per run for building and
# testing Professor's compiled projects inside the fence
# (docs/dev/isolated-dev-foundation.md).
# The worktree is edited on the HOST; this container only builds and tests it.
FROM ubuntu:24.04
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl git jq zsh tmux python3 python3-yaml xz-utils \
 && rm -rf /var/lib/apt/lists/*
# Go pinned to pfm/go.mod — bump both together or the fence tests a different compiler.
ARG GO_VERSION=1.24.13
ARG TARGETARCH
RUN case "${TARGETARCH}" in amd64|arm64) ;; *) echo "unsupported build architecture: ${TARGETARCH}" >&2; exit 1 ;; esac \
 && curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${TARGETARCH}.tar.gz" | tar -C /usr/local -xz
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
WORKDIR /worktree
