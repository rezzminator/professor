#!/usr/bin/env bash
# stepprof.sh — run one gate step under the always-on step profiler.
#
#   source stepprof.sh; stepprof_run <out-dir> <name> <bound-s> -- <command...>
#   bash stepprof.sh <out-dir> <name> <bound-s> -- <command...>
#
# Sourced it only defines functions: no output, no shell option changed. The
# command is a program or a shell function of the caller; it runs in a
# background job that exports PFM_TEST_DEADLINE_EPOCH (start + bound, decimal
# epoch seconds) so the Go test watchdog fires before the bound does.
#
# Always writes <out-dir>/<name>.prof.tsv (key<TAB>value): bound, window
# epochs, wall, children CPU, trace mode, ERR-record count, VM pressure-stall
# deltas over the step window, the exit status and how the step ended.
# STEPPROF_TRACE picks what is recorded into <name>.xtrace (removed when it
# ends empty); every bash the step starts writes it, BASH_ENV being the only
# door (bash running as root ignores PS4 from the environment). A Go test
# process drops BASH_ENV (testjail.Run), so a shell started by a Go test writes
# no record:
#   err  (default) one record per failing command: `ERR rc=… <epoch> file:line
#        func: command` and its call stack. Bash fires no ERR inside an
#        `||`/`if` condition, so this catches the unexpected set -e death.
#   x    full xtrace with EPOCHREALTIME stamps; +25 % wall, so opt-in only
#   off  nothing
# Past <bound-s> the step's process tree is snapshotted into <name>.hang/ —
# state, wchan, schedstat, fds and the holders of every pipe or fifo it
# touches — then TERMed, KILLed after STEPPROF_GRACE_TICKS 0.1 s ticks (default
# 20), and the step ends 124 with ended=timeout.
# A pressure source that cannot be read is written as UNAVAILABLE, never 0.
# Exit status: the command's; 124 bound reached; 64 usage; 71 a file cannot be
# written. STEPPROF_PSI_DIR (default /proc/pressure) is the pressure test seam.

# The three number helpers return through the caller's variable name, so their
# own locals carry a leading underscore: a local named like the caller's variable
# would swallow the result.
stepprof_us() { # stepprof_us <var> <decimal seconds> → integer microseconds in <var>
  local _v=${2/,/.} _int _frac=
  _int=${_v%%.*}
  [[ $_v == *.* ]] && _frac=${_v#*.}
  _frac=${_frac}000000
  printf -v "$1" '%d' $((10#${_int:-0} * 1000000 + 10#${_frac:0:6}))
}

stepprof_fmt() { # stepprof_fmt <var> <microseconds> <3|6> → seconds with that many decimals
  local _us=$2 _sign=
  ((_us < 0)) && { _sign=-; _us=$((-_us)); }
  if (($3 == 3)); then
    printf -v "$1" '%s%d.%03d' "$_sign" $((_us / 1000000)) $((_us % 1000000 / 1000))
  else
    printf -v "$1" '%s%d.%06d' "$_sign" $((_us / 1000000)) $((_us % 1000000))
  fi
}

stepprof_times_us() { # stepprof_times_us <var> <a bash `times` field, 1m2.345s> → microseconds
  local _v=${2%s} _sec
  stepprof_us _sec "${_v#*m}"
  printf -v "$1" '%d' $((10#${_v%%m*} * 60000000 + _sec))
}

stepprof_psi_snapshot() { # prints <res>_<kind><TAB>total µs per pressure line, or one UNAVAILABLE line
  local dir=${STEPPROF_PSI_DIR:-/proc/pressure} r kind tot line lines=
  for r in cpu io memory; do
    [[ -r $dir/$r ]] || { printf 'psi\tUNAVAILABLE %s\n' "$dir/$r"; return; }
    while read -r kind _ _ _ tot; do
      [[ $tot =~ ^total=[0-9]+$ ]] || { printf 'psi\tUNAVAILABLE unparsable line in %s\n' "$dir/$r"; return; }
      printf -v line '%s_%s\t%s\n' "$r" "$kind" "${tot#total=}"
      lines+=$line
    done < "$dir/$r"
  done
  [[ -n $lines ]] || { printf 'psi\tUNAVAILABLE no pressure lines under %s\n' "$dir"; return; }
  printf '%s' "$lines"
}

stepprof_descendants() { # pid → itself and every descendant pid, one per line
  local root=$1 f pid ppid rest queue i=0
  local -A kids=()
  for f in /proc/[0-9]*/stat; do
    read -r pid _ rest < "$f" 2>/dev/null || continue
    rest=${rest##*) }          # drop "(comm) " which may hold spaces
    read -r _ ppid _ <<< "$rest"
    kids[$ppid]+="$pid "
  done
  queue=("$root")
  while ((i < ${#queue[@]})); do
    pid=${queue[i]}; ((i++))
    echo "$pid"
    for f in ${kids[$pid]:-}; do queue+=("$f"); done
  done
}

stepprof_alive() { # 0 when any of the pids still runs; a zombie is dead
  local p st
  for p in "$@"; do
    { st=$(<"/proc/$p/stat"); } 2>/dev/null || continue
    st=${st##*) }
    [[ ${st%% *} == [ZX] ]] || return 0
  done
  return 1
}

stepprof_snapshot_tree() { # stepprof_snapshot_tree <dir> <bound> <root pid> → <dir>/tree.txt
  local dir=$1 bound=$2 pid fd target st comm cwd arg cand p t links
  local -a argv
  local -A fifos=() waiters=() holders=() seen=()
  mkdir -p "$dir" || { echo "stepprof: cannot create $dir" >&2; return 1; }
  {
    echo "# process tree of the step at $(date -u +%FT%TZ), bound ${bound}s"
    for pid in $(stepprof_descendants "$3"); do
      [[ -d /proc/$pid ]] || continue
      st=$(sed 's/^[0-9]* (\(.*\)) \(.\) .*/\2 \1/' "/proc/$pid/stat" 2>/dev/null)
      comm=; read -r comm < "/proc/$pid/comm" 2>/dev/null
      printf '\npid %s  state %s  wchan %s  schedstat %s\n' "$pid" "$st" \
        "$(cat "/proc/$pid/wchan" 2>/dev/null)" "$(cat "/proc/$pid/schedstat" 2>/dev/null)"
      mapfile -d '' -t argv < "/proc/$pid/cmdline" 2>/dev/null
      printf '  cmd: %s\n' "$(printf '%s ' "${argv[@]}" | cut -c1-300)"
      cwd=$(readlink "/proc/$pid/cwd" 2>/dev/null)
      for arg in "${argv[@]}"; do # a fifo named on the command line: a blocked open() holds no fd yet
        cand=$arg
        [[ $arg == /* ]] || cand=$cwd/$arg
        if [[ -p $cand ]]; then
          cand=$(readlink -f -- "$cand")
          fifos[$cand]=1
          waiters[$cand]+=" $pid(${comm:-?})"
        fi
      done
      for fd in /proc/"$pid"/fd/*; do
        target=$(readlink "$fd" 2>/dev/null) || continue
        printf '  fd %s → %s\n' "${fd##*/}" "$target"
        [[ $target == pipe:* || -p $target ]] && fifos[$target]=1
      done
    done
    echo
    echo "# holders of every pipe/fifo the tree touches (any process)"
    if ((${#fifos[@]})); then
      links=$(find /proc/[0-9]*/fd -maxdepth 1 -type l -printf '%p\t%l\n' 2>/dev/null) # one pass over every fd link
      [[ -n $links ]] || echo "# holder scan FAILED: find listed no fd link, so the lines below prove nothing"
      while IFS=$'\t' read -r p t; do
        [[ -n $t && -n ${fifos[$t]+x} ]] || continue # an empty target: the fd closed between the listing and readlink
        pid=${p#/proc/}; pid=${pid%%/*}
        [[ -n ${seen[$t:$pid]+x} ]] && continue
        seen[$t:$pid]=1
        comm=; read -r comm < "/proc/$pid/comm" 2>/dev/null
        holders[$t]+=" $pid(${comm:-?})"
      done <<< "$links"
      for target in "${!fifos[@]}"; do
        printf '%s held by:%s\n' "$target" "${holders[$target]:- nobody (no process holds it open)}"
        [[ -z ${waiters[$target]:-} ]] || printf '%s awaited by (named on its command line):%s\n' "$target" "${waiters[$target]}"
      done
    fi
  } > "$dir/tree.txt" 2>&1
}

stepprof_main() { # the body of stepprof_run; exits, so it runs in its own subshell
  set +e; trap - ERR
  if (($# < 5)) || [[ $4 != -- ]]; then
    echo "usage: stepprof_run|stepprof.sh <out-dir> <name> <bound-s> -- <command...>" >&2
    exit 64
  fi
  local out=$1 name=$2 bound=$3
  shift 4
  [[ -n $name && $name != */* ]] || { echo "stepprof: step name must be non-empty and hold no '/': '$name'" >&2; exit 64; }
  [[ $bound =~ ^[0-9]+([.][0-9]+)?$ ]] || { echo "stepprof: bound must be decimal seconds, got '$bound'" >&2; exit 64; }
  local grace=${STEPPROF_GRACE_TICKS:-20} trace=${STEPPROF_TRACE:-err} trace_body
  [[ $grace =~ ^[0-9]+$ ]] || { echo "stepprof: STEPPROF_GRACE_TICKS must be a whole number, got '$grace'" >&2; exit 64; }
  case $trace in
    x)
      trace_body='BASH_XTRACEFD=199
PS4='"'"'+${EPOCHREALTIME} ${BASH_SOURCE[0]-}:${LINENO} ${FUNCNAME[0]-main}: '"'"'
set -x' ;;
    err) # frames of stepprof_* are the profiler's own: the step's exit status is not an ERR record;
      # every read is unset-safe (FUNCNAME is unset at a script's top level), so the trap never aborts a set -u step
      trace_body='set -E
trap '"'"'__sp_rc=$?; if [[ ${FUNCNAME[0]-} != stepprof_* ]]; then { printf "ERR rc=%s %s %s:%s %s: %s\n" "$__sp_rc" "$EPOCHREALTIME" "${BASH_SOURCE[0]-}" "$LINENO" "${FUNCNAME[0]-main}" "$BASH_COMMAND"; __sp_n=${FUNCNAME[@]+${#FUNCNAME[@]}}; for ((__sp_i = 1; __sp_i < ${__sp_n:-0}; __sp_i++)); do [[ ${FUNCNAME[__sp_i]-} == stepprof_* ]] && break; printf "  at %s:%s %s\n" "${BASH_SOURCE[__sp_i]-}" "${BASH_LINENO[__sp_i-1]-}" "${FUNCNAME[__sp_i]-}"; done; } >&199; fi'"'"' ERR' ;;
    off) trace_body=: ;;
    *) echo "stepprof: STEPPROF_TRACE must be err, x or off" >&2; exit 64 ;;
  esac
  mkdir -p "$out" || { echo "stepprof: cannot create $out" >&2; exit 71; }
  local prof="$out/$name.prof.tsv" xtrace="$out/$name.xtrace" env_file="$out/$name.bash_env" hang="$out/$name.hang"
  rm -rf "$xtrace" "$hang" # a rerun into the same directory starts clean
  {
    [[ $trace == off ]] || printf 'exec 199>>%q\n' "$xtrace"
    printf '%s\n' "$trace_body"
  } > "$env_file" || { echo "stepprof: cannot write $env_file" >&2; exit 71; }

  local psi0 psi1 start finish deadline_us deadline bound_us pid sleeper first wrc rc ended=exit tree tick p
  psi0=$(stepprof_psi_snapshot)
  start=${EPOCHREALTIME/,/.}
  stepprof_us bound_us "$bound"
  stepprof_us deadline_us "$start"
  stepprof_fmt deadline $((deadline_us + bound_us)) 6
  ( # the step: sources the env file first so the ERR trap covers a shell function's own body
    export BASH_ENV=$env_file PFM_TEST_DEADLINE_EPOCH=$deadline
    # shellcheck disable=SC1090 # the env file is generated above, per step
    source "$env_file"
    # `builtin eval`, not a plain call: a caller that tests stepprof_run (`if`, `||`)
    # would otherwise silence the ERR trap and errexit inside the step's own function body
    builtin eval '"$@"'
    exit $?
  ) &
  pid=$!
  sleep "$bound" &
  sleeper=$!
  wait -n -p first "$pid" "$sleeper"
  wrc=$?
  if [[ ${first:-} == "$sleeper" ]] && kill -0 "$pid" 2>/dev/null; then
    ended=timeout
    stepprof_snapshot_tree "$hang" "$bound" "$pid"
    tree=$(stepprof_descendants "$pid")
    kill -TERM $tree 2>/dev/null
    for ((tick = 0; tick < grace; tick++)); do # 0.1 s ticks
      stepprof_alive $tree || break
      sleep 0.1
    done
    tree+=$'\n'$(stepprof_descendants "$pid") # whatever forked during the grace
    kill -KILL $tree 2>/dev/null
    for ((tick = 0; tick < 10; tick++)); do
      stepprof_alive $tree || break
      sleep 0.1
    done
    if stepprof_alive $tree; then
      echo "# still alive after KILL: $(for p in $tree; do stepprof_alive "$p" && printf '%s ' "$p"; done)" >> "$hang/tree.txt"
    fi
    wait "$pid" 2>/dev/null
    rc=124
  else
    kill "$sleeper" 2>/dev/null
    if [[ ${first:-} == "$pid" ]]; then
      rc=$wrc
    else
      wait "$pid"
      rc=$?
    fi
  fi
  finish=${EPOCHREALTIME/,/.}
  wait "$sleeper" 2>/dev/null
  psi1=$(stepprof_psi_snapshot)

  local lines cu cs wall_s start_us finish_us cpu_us cpu_s k v d err_records=0
  local -A before=()
  times > "$out/$name.times" # builtin in this subshell: children = the step
  mapfile -t lines < "$out/$name.times"
  read -r cu cs <<< "${lines[1]-}"
  stepprof_us start_us "$start"
  stepprof_us finish_us "$finish"
  stepprof_fmt wall_s $((finish_us - start_us)) 6
  if [[ $cu =~ ^[0-9]+m[0-9]+[.,][0-9]+s$ && $cs =~ ^[0-9]+m[0-9]+[.,][0-9]+s$ ]]; then
    stepprof_times_us cpu_us "$cu"
    stepprof_times_us v "$cs"
    stepprof_fmt cpu_s $((cpu_us + v)) 3
  else
    cpu_s="NA unparsable times line '${lines[1]-}'"
  fi
  if [[ -s $xtrace ]]; then
    err_records=$(grep -c '^ERR ' "$xtrace")
    (($? <= 1)) || err_records="NA grep failed on $xtrace"
  else
    rm -f "$xtrace"
  fi
  {
    printf 'step\t%s\nended\t%s\nrc\t%s\nbound_s\t%s\n' "$name" "$ended" "$rc" "$bound"
    printf 'start_epoch\t%s\nfinish_epoch\t%s\nwall_s\t%s\n' "$start" "$finish" "$wall_s"
    printf 'children_cpu\t%s user %s sys\ncpu_s\t%s\n' "$cu" "$cs" "$cpu_s"
    printf 'trace\t%s\nerr_records\t%s\n' "$trace" "$err_records"
    if [[ $psi0 == psi$'\t'* ]]; then
      printf '%s\n' "$psi0"
    elif [[ $psi1 == psi$'\t'* ]]; then
      printf '%s\n' "$psi1"
    else
      while IFS=$'\t' read -r k v; do before[$k]=$v; done <<< "$psi0"
      while IFS=$'\t' read -r k v; do
        if [[ -n ${before[$k]+x} ]]; then
          stepprof_fmt d $((v - before[$k])) 6
          printf 'psi_%s_s\t%s\n' "$k" "$d"
        else
          printf 'psi_%s_s\tUNAVAILABLE absent at step start\n' "$k"
        fi
      done <<< "$psi1"
    fi
  } > "$prof" || { echo "stepprof: cannot write $prof" >&2; rm -f "$env_file" "$out/$name.times"; exit 71; }
  rm -f "$env_file" "$out/$name.times"
  exit "$rc"
}

# stepprof_run runs in its own subshell: `times` there counts only this step's
# children, whatever shell the caller is (steps.sh calls it in a per-step one).
stepprof_run() { (stepprof_main "$@"); }

if [[ ${BASH_SOURCE[0]-} == "$0" ]]; then
  set -u
  stepprof_run "$@"
  exit $?
fi
