# platform.sh — the shell door for every OS-dependent operation: source it, then
# call platform_*; no script branches on the OS or names a one-OS tool itself
# (pfm/scripts/arch-check.sh counts the stragglers). POSIX sh, sourced, no side
# effects on source: it defines functions only.
#   . "$(dirname "${BASH_SOURCE[0]}")/{rel}/scripts/platform.sh" || exit 2
# Each function probes the tool, not the OS name: GNU first, then BSD, exactly
# as the copies it replaced did, so a Linux host that has shasum still runs sha256sum.
#
# BROKEN STATE: no tool at all is rc 127 and no output, so the caller prints its
# own text; a tool that runs and fails passes its own stderr and rc through. The
# door never swallows either into an empty success.

# platform_sha256 FILE...: sha256sum-format lines. 127, silent, with neither tool.
platform_sha256() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$@"
  elif command -v shasum >/dev/null 2>&1; then shasum -a 256 "$@"
  else return 127
  fi
}

# platform_sha256_tools JOIN: the two tools platform_sha256 probes, joined by
# JOIN (nor, or), for the caller's own missing-tool text; each caller keeps its words.
platform_sha256_tools() {
  printf 'sha256sum %s shasum' "$1"
}

# platform_mtime FILE: epoch seconds. The BSD attempt's stderr reaches the caller.
platform_mtime() {
  stat -c %Y -- "$1" 2>/dev/null || stat -f %m -- "$1"
}

# platform_owner_mode PATH: "{uid} {octal mode}". The BSD attempt's stderr reaches the caller.
platform_owner_mode() {
  stat -c '%u %a' -- "$1" 2>/dev/null || stat -f '%u %Lp' -- "$1"
}

# platform_os: darwin | linux | unknown.
platform_os() {
  case $(uname -s) in
    Darwin) echo darwin ;;
    Linux) echo linux ;;
    *) echo unknown ;;
  esac
}

# platform_arch: arm64 | amd64 | unknown.
platform_arch() {
  case $(uname -m) in
    arm64|aarch64) echo arm64 ;;
    x86_64|amd64) echo amd64 ;;
    *) echo unknown ;;
  esac
}

# platform_cpus: online CPU count; rc 1 and no output when neither tool exists.
platform_cpus() {
  if command -v nproc >/dev/null 2>&1; then nproc
  elif command -v getconf >/dev/null 2>&1; then getconf _NPROCESSORS_ONLN
  else return 1
  fi
}

# platform_service_env NAME VALUE: NAME=VALUE in the environment of every service the owner's service
# manager starts from now on (systemctl --user set-environment, else launchctl setenv). 127, silent,
# with neither tool.
platform_service_env() {
  if command -v systemctl >/dev/null 2>&1; then systemctl --user set-environment "$1=$2"
  elif command -v launchctl >/dev/null 2>&1; then launchctl setenv "$1" "$2"
  else return 127
  fi
}

# platform_in_fence [MARKER]: rc 0 on a machine a suite may run on and provisioning may write:
# the development fence (PFM_DEV_FENCE=1 inside a container, MARKER, default /.dockerenv, present)
# or a GitHub-hosted runner (GITHUB_ACTIONS=true, RUNNER_ENVIRONMENT=github-hosted), a throwaway
# machine. The variable alone never passes: a host shell exporting it once ran a suite against the
# live pfm.db. pfm/internal/testjail/fence.go (admitted) applies the same rule.
platform_in_fence() {
  { [ "${PFM_DEV_FENCE:-}" = 1 ] && [ -e "${1:-/.dockerenv}" ]; } ||
    { [ "${GITHUB_ACTIONS:-}" = true ] && [ "${RUNNER_ENVIRONMENT:-}" = github-hosted ]; }
}
