# shellcheck shell=bash
# lib/run.sh: run id and run folder (D6), the socket bound (L7), the ledger and the
# cross-run budget preflight (D9, L1), prune (L18). Errors print one named line on
# stderr and return 2; the lab entry decides to exit.

lab_sha256() { # <string>: hex sha256, either tool
  if command -v sha256sum >/dev/null 2>&1; then printf '%s' "$1" | sha256sum | cut -c1-64
  else printf '%s' "$1" | shasum -a 256 | cut -c1-64; fi
}

lab_project() { # the main checkout's directory name minus its leading dot, from a worktree too
  local repo=${LAB_REPO:-${LAB_LAB:-.}} common
  if ! common=$(git -C "$repo" rev-parse --path-format=absolute --git-common-dir 2>&1); then
    printf 'ERROR project: git rev-parse in %s: %s\n' "$repo" "$common" >&2
    return 2
  fi
  common=$(basename "$(dirname "$common")")
  printf '%s\n' "${common#.}"
}

lab_root() { # LAB_ROOT, else /tmp/{project}/lab
  local project
  if [[ -n ${LAB_ROOT:-} ]]; then printf '%s\n' "$LAB_ROOT"; return 0; fi
  project=$(lab_project) || return 2
  printf '/tmp/%s/lab\n' "$project"
}

lab_root_safe() { # <physical dir>: every folder from it up to a sticky or root-owned one is the invoker's (or root's) and open to nobody else
  local d=$1 me
  me=$(id -u)
  while [[ $d != / ]]; do
    if [[ -L $d || ! -d $d ]] || [[ -z $(find "$d" -maxdepth 0 \( -user "$me" -o -user 0 \) -print) ]]; then
      printf 'ERROR run-dir unsafe root %s: not a folder of uid %s or root\n' "$d" "$me" >&2
      return 2
    fi
    if [[ -n $(find "$d" -maxdepth 0 \( -perm -0020 -o -perm -0002 \) -print) ]]; then
      [[ -n $(find "$d" -maxdepth 0 -perm -1000 -print) ]] && return 0
      printf 'ERROR run-dir unsafe root %s: writable by group or others\n' "$d" >&2
      return 2
    fi
    [[ $me != 0 && -n $(find "$d" -maxdepth 0 -user 0 -print) ]] && return 0
    d=$(dirname "$d")
  done
}

lab_run_dir() { # <scenario>: makes the private run folder under a safe root, by its physical path; sets LAB_RUN and LAB_RUN_ID
  local root base id n=1 sock
  root=$(lab_root) || return 2
  if ! (umask 077 && mkdir -p "$root") || ! root=$(cd "$root" && pwd -P); then
    printf 'ERROR run-dir cannot create %s\n' "$root" >&2
    return 2
  fi
  lab_root_safe "$root" || return 2
  base="$1-$(date +%y%m%d-%H%M%S)"
  id=$base
  while [[ -e $root/$id ]]; do n=$((n + 1)); id="$base-$n"; done
  sock="$root/$id/t.sock"
  if (( ${#sock} > 100 )); then
    printf 'ERROR socket-path-long %d>100 %s\n' "${#sock}" "$sock" >&2
    return 2
  fi
  if ! mkdir -m 700 "$root/$id"; then
    printf 'ERROR run-dir cannot create %s\n' "$root/$id" >&2
    return 2
  fi
  LAB_RUN=$root/$id
  LAB_RUN_ID=$id
}

lab_ledger_sum() { # <ledger>: spend so far (%.2f): a run's final row in place of its RUNNING one, a RUNNING
  # row with no final row at its cap, an unknown spend (?) at its cap
  [[ -e $1 ]] || { printf '0.00\n'; return 0; }
  if ! awk -F '\t' 'NF >= 8 && $6 == "RUNNING" { open[$1]++; cap[$1] = $8; next }
    NF >= 8 { done[$1]++; s += ($7 == "?" ? $8 : $7) }
    END { for (id in open) if (open[id] > done[id]) s += (open[id] - done[id]) * cap[id]; printf "%.2f\n", s }' "$1"; then
    printf 'ERROR ledger unreadable %s\n' "$1" >&2
    return 2
  fi
}

lab_budget_preflight() { # <ledger> <cap> <budget>: refuses when spent + cap > budget
  local spent
  spent=$(lab_ledger_sum "$1") || return 2
  awk -v s="$spent" -v c="$2" -v b="$3" 'BEGIN {
    sc = int(s * 100 + 0.5); cc = int(c * 100 + 0.5); bc = int(b * 100 + 0.5)
    if (sc + cc > bc) { printf "ERROR budget-exhausted spent=%.2f cap=%.2f budget=%.2f\n", s, c, b > "/dev/stderr"; exit 2 }
    printf "budget: %.2f left of %.2f · cap %.2f\n", (bc - sc) / 100, b, c
  }'
}

lab_ledger_lock() { # <ledger>: one ledger writer at a time across runs ({ledger}.lock, mkdir is atomic); waits 10 s
  local l=$1 err i
  mkdir -p "$(dirname "$l")" 2>/dev/null || { printf 'ERROR ledger cannot create %s\n' "$(dirname "$l")" >&2; return 2; }
  for (( i = 0; i < 100; i++ )); do
    err=$(LC_ALL=C mkdir "$l.lock" 2>&1) && return 0
    # an EEXIST whose holder released before this recheck is retried, never an error
    [[ -d $l.lock || $err == *'File exists'* ]] || { printf 'ERROR ledger cannot lock %s.lock: %s\n' "$l" "${err%%$'\n'*}" >&2; return 2; }
    sleep 0.1
  done
  printf 'ERROR ledger locked %s.lock for over 10 s (remove it when no lab run is writing)\n' "$l" >&2
  return 2
}

lab_ledger_unlock() { rmdir "$1.lock" 2>/dev/null || true; } # <ledger>

lab_ledger_append() { # <ledger> <field>...: one tab-separated row
  local f=$1; shift
  if ! mkdir -p "$(dirname "$f")" 2>/dev/null || ! (IFS=$'\t'; printf '%s\n' "$*") >> "$f" 2>/dev/null; then
    printf 'ERROR ledger cannot append to %s\n' "$f" >&2
    return 2
  fi
}

lab_prune() { # <days>: deletes run folders (named {scenario}-YYMMDD-HHMMSS[-n]) under the root older than <days>, printing each; never follows a symlink
  local root d
  [[ $1 =~ ^[0-9]+$ ]] || { printf 'ERROR prune --days wants a whole number, got %s\n' "$1" >&2; return 2; }
  root=$(lab_root) || return 2
  [[ $root == /* && $root != / ]] || { printf 'ERROR prune refuses root %s\n' "$root" >&2; return 2; }
  [[ -d $root ]] || return 0
  while IFS= read -r d; do
    [[ ${d##*/} =~ -[0-9]{6}-[0-9]{6}(-[0-9]+)?$ ]] || continue
    rm -rf "$d" && printf '%s\n' "$d"
  done < <(find "$root" -mindepth 1 -maxdepth 1 -type d -mtime +"$1" -print)
}
