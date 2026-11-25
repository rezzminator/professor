#!/usr/bin/env bash
# Sourced. fence_local_env <main-checkout> <caller> exports this machine's
# fence network overrides from <main-checkout>/infra/fence/.env — gitignored,
# one file for the main checkout and every linked worktree:
#   PFM_GOPROXY         the Go module proxy the running fence's Go fetches use,
#                       runtime only (a network that intercepts proxy.golang.org's TLS)
#   PFM_NPM_REGISTRY    the npm mirror (a network that blocks registry.npmjs.org)
#   PFM_APT_MIRROR      the Ubuntu apt mirror, a URL like https://mirror.example/ubuntu
#                       (a network that blocks archive.ubuntu.com)
#   PFM_EXTRA_CA_CERTS  an absolute path to a PEM file of one or more CA certificates
#                       the image trusts, like /etc/corp/root-ca.pem (a network whose
#                       TLS interception proxy re-signs HTTPS with a private root CA);
#                       its content, never its path, reaches the build as
#                       PFM_EXTRA_CA_PEM_B64, one line of base64, so the image key
#                       changes with the certificates
# One KEY=value per line; blank lines and `#` comments are skipped. A variable
# already set in the environment wins over the file. No file exports nothing.
# BROKEN STATE: an unreadable file, a line that is not KEY=value or a key
# outside the four prints `{caller}: infra/fence/.env …` naming the line and
# returns 1 — a typo never builds an image silently without its mirror. A
# PFM_EXTRA_CA_CERTS that is not absolute, not readable or holds no
# `-----BEGIN CERTIFICATE-----` prints `{caller}: infra/fence/.env …` (or
# `{caller}: PFM_EXTRA_CA_CERTS …` when the environment set it) and returns 1.

fence_local_env() {
  local file="$1/infra/fence/.env" caller="$2" line key value n=0 ca_where="$2: PFM_EXTRA_CA_CERTS" b64
  if [[ -e "$file" ]]; then
    [[ -r "$file" ]] || { echo "$caller: infra/fence/.env unreadable: $file" >&2; return 1; }
    while IFS= read -r line || [[ -n "$line" ]]; do
      n=$((n + 1))
      case "$line" in '' | '#'*) continue ;; esac
      if [[ "$line" != *=* ]]; then
        echo "$caller: infra/fence/.env line $n is not KEY=value: $file" >&2; return 1
      fi
      key="${line%%=*}" value="${line#*=}"
      case "$key" in
        PFM_GOPROXY | PFM_NPM_REGISTRY | PFM_APT_MIRROR | PFM_EXTRA_CA_CERTS) ;;
        *) echo "$caller: infra/fence/.env line $n: unknown key $key (known: PFM_GOPROXY, PFM_NPM_REGISTRY, PFM_APT_MIRROR, PFM_EXTRA_CA_CERTS): $file" >&2; return 1 ;;
      esac
      if [[ -z "${!key+x}" ]]; then
        export "$key=$value"
        [[ "$key" != PFM_EXTRA_CA_CERTS ]] || ca_where="$caller: infra/fence/.env line $n PFM_EXTRA_CA_CERTS"
      fi
    done <"$file"
  fi
  [[ -n "${PFM_EXTRA_CA_CERTS:-}" ]] || return 0
  local ca="$PFM_EXTRA_CA_CERTS"
  [[ "$ca" == /* ]] || { echo "$ca_where must be an absolute path: $ca" >&2; return 1; }
  [[ -f "$ca" && -r "$ca" ]] || { echo "$ca_where is not a readable file: $ca" >&2; return 1; }
  grep -q -- '-----BEGIN CERTIFICATE-----' "$ca" || { echo "$ca_where holds no -----BEGIN CERTIFICATE-----: $ca" >&2; return 1; }
  b64="$(base64 <"$ca" | tr -d '\n')" && [[ -n "$b64" ]] || { echo "$ca_where could not be base64-encoded: $ca" >&2; return 1; }
  export PFM_EXTRA_CA_PEM_B64="$b64"
}
