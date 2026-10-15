# shellcheck shell=bash
# lib/auth.sh: sign-in by the eval-sandbox route (D1). The keychain item (or Linux
# credentials file) is read read-only behind the LAB_SECURITY seam; only the access
# token ever leaves it, through a FIFO, never argv, env or a file. The run's own
# keychain item is the only one ever deleted (L13).

lab_auth_service() { # <config dir>: the keychain service Claude Code uses for it
  if [[ $1 == "$HOME/.claude" ]]; then printf 'Claude Code-credentials\n'
  else printf 'Claude Code-credentials-%s\n' "$(lab_sha256 "$1" | cut -c1-8)"; fi
}

lab_auth_keychain() { [[ -n ${LAB_SECURITY:-} || $(uname -s) == Darwin ]]; }

lab_auth_read() { # <config dir>: sets LAB_AUTH_JSON, the credentials document
  local svc
  svc=$(lab_auth_service "$1")
  if lab_auth_keychain; then
    LAB_AUTH_JSON=$("${LAB_SECURITY:-security}" find-generic-password -s "$svc" -w 2>/dev/null) \
      || { printf 'ERROR auth-not-found service=%s\n' "$svc" >&2; return 2; }
  else
    LAB_AUTH_JSON=$(cat "$1/.credentials.json" 2>/dev/null) \
      || { printf 'ERROR auth-not-found file=%s/.credentials.json\n' "$1" >&2; return 2; }
  fi
  printf '%s' "$LAB_AUTH_JSON" | jq -e '.claudeAiOauth.accessToken | type == "string"' >/dev/null 2>&1 \
    || { printf 'ERROR auth-unreadable no claudeAiOauth.accessToken in service=%s\n' "$svc" >&2; return 2; }
}

lab_auth_check() { # <wall cap s>: needs expiresAt - now >= wall cap + 10 min
  local exp now left need
  exp=$(printf '%s' "$LAB_AUTH_JSON" | jq -r '.claudeAiOauth.expiresAt // empty')
  [[ $exp =~ ^[0-9]+$ ]] || { printf 'ERROR auth-unreadable expiresAt=%s\n' "$exp" >&2; return 2; }
  now=$(( $(date +%s) * 1000 ))
  left=$(( (exp - now + 30000) / 60000 ))
  need=$(( ($1 + 59) / 60 + 10 ))
  if (( left < need )); then
    printf 'ERROR auth-expiring %d min left, need %d\n' "$left" "$need" >&2
    return 2
  fi
  printf 'auth: %d min left\n' "$left"
}

lab_auth_env() { # the four auth keys of D1, one KEY=value per line; never the token
  printf 'CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR=3\n'
  printf '%s' "$LAB_AUTH_JSON" | jq -r '.claudeAiOauth
    | "CLAUDE_CODE_OAUTH_SCOPES=\((.scopes // []) | join(" "))",
      "CLAUDE_CODE_SUBSCRIPTION_TYPE=\(.subscriptionType // "")",
      "CLAUDE_CODE_RATE_LIMIT_TIER=\(.rateLimitTier // "")"'
}

lab_auth_handoff() { # <fifo>: a background writer hands the access token to whoever opens the FIFO
  local token
  [[ -p $1 ]] || mkfifo -m 600 "$1" || { printf 'ERROR auth-handoff mkfifo %s\n' "$1" >&2; return 2; }
  token=$(printf '%s' "$LAB_AUTH_JSON" | jq -r '.claudeAiOauth.accessToken')
  ( printf '%s' "$token" > "$1" ) &
  LAB_WRITER_PID=$!
  token=''
}

lab_auth_handoff_wait() { # the writer finishes once the launcher opened fd 3; else it is killed behind its identity
  local i limit=$(( ${LAB_HANDOFF_S:-30} * 10 ))
  for (( i = 0; i < limit; i++ )); do
    kill -0 "$LAB_WRITER_PID" 2>/dev/null || return 0
    sleep 0.1
  done
  ps -o args= -p "$LAB_WRITER_PID" 2>/dev/null | grep -qF -- "${LAB_SELF:-lab}" && kill "$LAB_WRITER_PID" 2>/dev/null
  wait "$LAB_WRITER_PID" 2>/dev/null
  printf 'ERROR auth-handoff\n' >&2
  return 2
}

lab_keychain_clean() { # <run>: deletes the keychain item the run created under its own config hash, only that one
  local svc rc
  svc=$(lab_auth_service "$1/config")
  if ! lab_auth_keychain; then
    [[ -e $1/config/.credentials.json ]] || { printf 'PASS none created\n'; return 0; }
    rm -f "$1/config/.credentials.json" && { printf 'PASS created and deleted\n'; return 0; }
    printf 'ERROR keychain-clean %s/config/.credentials.json\n' "$1"; return 2
  fi
  "${LAB_SECURITY:-security}" find-generic-password -s "$svc" >/dev/null 2>&1; rc=$?
  case $rc in
    44) printf 'PASS none created\n'; return 0 ;;
    0) ;;
    *) printf 'ERROR keychain-clean find rc=%d service=%s\n' "$rc" "$svc"; return 2 ;;
  esac
  if "${LAB_SECURITY:-security}" delete-generic-password -s "$svc" >/dev/null 2>&1; then
    "${LAB_SECURITY:-security}" find-generic-password -s "$svc" >/dev/null 2>&1; rc=$?
    [[ $rc == 44 ]] && { printf 'PASS created and deleted\n'; return 0; }
  fi
  printf 'ERROR keychain-clean\n'
  return 2
}
