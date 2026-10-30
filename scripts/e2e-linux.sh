#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

if ! command -v docker >/dev/null 2>&1; then
  echo "e2e-linux: docker is required" >&2
  exit 1
fi

docker run --rm \
  -e HOME=/tmp/pfm-e2e-home \
  -e PFM_DEV_FENCE=1 \
  -e PFM_E2E_SOURCE_REPO=/workspace \
  -v "${repo_root}:/workspace:ro" \
  -w /workspace \
  golang:1.27.1-bookworm@sha256:8d48e12ec56735e9358640898b9d9b9fcca110612ed8a5567438c0a1baa24e66 \
  bash -euo pipefail -c '
    apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends git tmux zsh python3
    mkdir -p "$HOME"
    go -C pfm test -tags e2e -p 1 ./e2e/...
  '
