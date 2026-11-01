#!/usr/bin/env bash
# Sourced. fence_local_env <main-checkout> <caller> exports this machine's
# fence network overrides from <main-checkout>/infra/fence/.env — gitignored,
# one file for the main checkout and every linked worktree:
#   PFM_GOPROXY       the Go module proxy the running fence's Go fetches use,
#                     runtime only (a network that intercepts proxy.golang.org's TLS)
#   PFM_NPM_REGISTRY  the npm mirror (a network that blocks registry.npmjs.org)
# One KEY=value per line; blank lines and `#` comments are skipped. A variable
# already set in the environment wins over the file. No file exports nothing.
# BROKEN STATE: an unreadable file, a line that is not KEY=value or a key
# outside the two prints `{caller}: infra/fence/.env …` naming the line and
# returns 1 — a typo never builds an image silently without its mirror.

fence_local_env() {
  local file="$1/infra/fence/.env" caller="$2" line key value n=0
  [[ -e "$file" ]] || return 0
  [[ -r "$file" ]] || { echo "$caller: infra/fence/.env unreadable: $file" >&2; return 1; }
  while IFS= read -r line || [[ -n "$line" ]]; do
    n=$((n + 1))
    case "$line" in '' | '#'*) continue ;; esac
    if [[ "$line" != *=* ]]; then
      echo "$caller: infra/fence/.env line $n is not KEY=value: $file" >&2; return 1
    fi
    key="${line%%=*}" value="${line#*=}"
    case "$key" in
      PFM_GOPROXY | PFM_NPM_REGISTRY) ;;
      *) echo "$caller: infra/fence/.env line $n: unknown key $key (known: PFM_GOPROXY, PFM_NPM_REGISTRY): $file" >&2; return 1 ;;
    esac
    [[ -n "${!key+x}" ]] || export "$key=$value"
  done <"$file"
}
