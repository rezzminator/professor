#!/usr/bin/env bash
# Install the pinned developer tools from infra/fence/tools.env with `go install`.
#   infra/fence/tools.sh [--bin DIR]   default DIR: $TOOLS_BIN, else <repo>/tmp/tools/<os>-<arch>/bin
# Prints one line per tool: TOOL <name> <version> INSTALLED|PRESENT <path>.
# BROKEN STATE: a missing tools.env, an unset version, a failed `go install`,
# or a binary that does not answer --version = a named line and a non-zero
# exit — never a silent partial install. `make tools` and the fence image
# both run this, so host and container carry byte-identical versions.
set -euo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
ENV_FILE="$HERE/tools.env"
[[ -f "$ENV_FILE" ]] || { echo "tools: BROKEN — $ENV_FILE missing" >&2; exit 2; }
# shellcheck source=tools.env
source "$ENV_FILE"
# Default keyed by OS/arch — the same rule pfm/Makefile resolves with, so a
# darwin host and the linux fence never see each other's binaries.
BIN="${TOOLS_BIN:-$HERE/../../tmp/tools/$(go env GOOS)-$(go env GOARCH)/bin}"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --bin) BIN="$2"; shift 2 ;;
    *) echo "usage: tools.sh [--bin DIR]" >&2; exit 2 ;;
  esac
done
mkdir -p "$BIN"
command -v go >/dev/null || { echo "tools: TOOLCHAIN-MISSING — go not on PATH" >&2; exit 1; }
# Build every tool with ONE explicit Go (tools.env TOOLS_GO): the formatters
# embed that Go's go/printer, and alignment differs across releases — with
# GOTOOLCHAIN=auto the host and the fence would each pick their own.
[[ -n "${TOOLS_GO:-}" ]] || { echo "tools: BROKEN — TOOLS_GO unset in tools.env" >&2; exit 2; }
export GOTOOLCHAIN="$TOOLS_GO"

# install <name> <module path> <version variable name>
install() {
  local name=$1 module=$2 var=$3 version="${!3:-}" state=INSTALLED
  [[ -n "$version" ]] || { echo "tools: BROKEN — $var unset in tools.env" >&2; exit 2; }
  if [[ -x "$BIN/$name" ]] && "$BIN/$name" --version 2>/dev/null | grep -q -- "${version#v}" \
     && go version -m "$BIN/$name" 2>/dev/null | head -1 | grep -q -- "$GOTOOLCHAIN"; then
    state=PRESENT
  else
    GOBIN="$BIN" GOFLAGS=-mod=mod go install "${module}@${version}" \
      || { echo "tools: FAILED — go install ${module}@${version}" >&2; exit 1; }
  fi
  "$BIN/$name" --version >/dev/null 2>&1 \
    || { echo "tools: BROKEN — $BIN/$name does not answer --version" >&2; exit 1; }
  printf 'TOOL %-18s %-9s %-9s %s (built with %s)\n' "$name" "$version" "$state" "$BIN/$name" "$GOTOOLCHAIN"
}

install golangci-lint    github.com/golangci/golangci-lint/v2/cmd/golangci-lint GOLANGCI_LINT_VERSION
install go-test-coverage github.com/vladopajic/go-test-coverage/v2              GO_TEST_COVERAGE_VERSION
