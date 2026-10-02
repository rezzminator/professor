#!/usr/bin/env bash
# sampler.sh — record the resources of a whole gate run into resources.tsv.
#
#   sampler.sh run --out <resources.tsv> [--parent <pid>]
#
# One row per SAMPLER_INTERVAL_S (default 0.5) until it is sent TERM or INT, or
# until --parent's process is gone (checked every interval); the stop writes one
# final row and exits 0. Tab-separated, header first, columns and sources:
#   epoch_s      the epoch at start + the rise of /proc/uptime, so a wall-clock
#                step never reorders rows; uptime_s is /proc/uptime field 1
#   cg_cpu_s     cgroup cpu.stat usage_usec           vm_busy_s  /proc/stat `cpu `
#                fields 1,2,3,6,7,8 over CLK_TCK      cpus       its cpuN rows
#   cg_io_{read,write}_mb  io.stat rbytes / wbytes summed over devices
#   cg_mem_mb, cg_mem_peak_mb, cg_swap_mb  memory.current / memory.peak /
#                memory.swap.current, in MiB
#   cg_psi_{cpu_some,io_some,io_full,mem_some,mem_full}_s  the cgroup's
#   vm_psi_{…}_s the same five from /proc/pressure/{cpu,io,memory}
#                total= of each pressure line, in seconds
#   spin_us      wall µs of a bash arithmetic loop of SAMPLER_SPIN_ITERS (default
#                10000) iterations, run at each row: its slowdown against the
#                least seen is how test-contention.sh tells a starved VM
#   load1        /proc/loadavg field 1
# The cgroup counters are read under SAMPLER_CGROUP_DIR (default /sys/fs/cgroup),
# the VM's under SAMPLER_PROC_DIR (default /proc). A sample starts no process: every
# read is a bash builtin, and the wait is `read -t` slices on a descriptor that
# never delivers, so a stop is felt within 0.1 s.
#
# A source that cannot be read is NA in that column's every row — never 0 — and
# its reason is written once to the .err file beside --out (resources.tsv →
# resources.err, otherwise <out>.err) as `<column><TAB><reason>`. Each row is
# flushed as written, so a reader sees rows while the sampler runs.
# Exit status: 0 stopped; 64 usage; 2 --out cannot be written, a knob is not a
# number, bash lacks $EPOCHREALTIME or /proc/uptime cannot be read at start;
# 1 a row could not be written or /proc/uptime was lost mid-run.

if [[ ${BASH_SOURCE[0]} == */* ]]; then SAMPLER_HERE=${BASH_SOURCE[0]%/*}; else SAMPLER_HERE=.; fi
# shellcheck source=stepprof.sh
source "$SAMPLER_HERE/stepprof.sh" # stepprof_us, stepprof_fmt: the µs number helpers

SAMPLER_COLUMNS=(epoch_s uptime_s cg_cpu_s vm_busy_s cpus cg_io_read_mb cg_io_write_mb cg_mem_mb cg_mem_peak_mb cg_swap_mb
  cg_psi_cpu_some_s cg_psi_io_some_s cg_psi_io_full_s cg_psi_mem_some_s cg_psi_mem_full_s
  vm_psi_cpu_some_s vm_psi_io_some_s vm_psi_io_full_s vm_psi_mem_some_s vm_psi_mem_full_s spin_us load1)
declare -A SAMPLER_COL=() SAMPLER_SEEN=()
SAMPLER_STOP=0

sampler_usage() { echo "usage: sampler.sh run --out <resources.tsv> [--parent <pid>]" >&2; }

sampler_na() { # sampler_na <column> <reason>: the column is NA in this row; its reason is logged once
  SAMPLER_COL[$1]=NA
  [[ -n ${SAMPLER_SEEN[$1]:-} ]] && return 0
  SAMPLER_SEEN[$1]=1
  { printf '%s\t%s\n' "$1" "$2" >> "$SAMPLER_ERR"; } 2> /dev/null ||
    echo "sampler: cannot write $SAMPLER_ERR: $1 NA ($2)" >&2
}

sampler_mb() { # sampler_mb <var> <bytes> → MiB with 3 decimals
  local _milli=$(($2 * 1000 / 1048576))
  printf -v "$1" '%d.%03d' $((_milli / 1000)) $((_milli % 1000))
}

sampler_mem_file() { # sampler_mem_file <column> <file>: the file's one integer, bytes, as MiB in the column
  local line v
  if [[ ! -r $2 ]]; then sampler_na "$1" "$2: absent or unreadable"; return 0; fi
  { read -r line < "$2"; } 2> /dev/null || [[ -n ${line:-} ]] || { sampler_na "$1" "$2: empty or unreadable"; return 0; }
  v=${line%% *}
  [[ $v =~ ^[0-9]+$ ]] || { sampler_na "$1" "$2: not an integer: ${line:0:40}"; return 0; }
  sampler_mb "SAMPLER_COL[$1]" "$v"
}

sampler_cpu_stat() { # cg_cpu_s from the cgroup's cpu.stat usage_usec
  local f=$SAMPLER_CG/cpu.stat k v found=
  if [[ ! -r $f ]]; then sampler_na cg_cpu_s "$f: absent or unreadable"; return 0; fi
  while read -r k v; do
    [[ $k == usage_usec ]] && { found=$v; break; }
  done < "$f"
  [[ $found =~ ^[0-9]+$ ]] || { sampler_na cg_cpu_s "$f: no usage_usec line"; return 0; }
  stepprof_fmt "SAMPLER_COL[cg_cpu_s]" "$found" 6
}

sampler_io_stat() { # cg_io_read_mb / cg_io_write_mb: rbytes / wbytes summed over the devices of io.stat
  local f=$SAMPLER_CG/io.stat words w r=0 wr=0 bad=
  if [[ ! -r $f ]]; then
    sampler_na cg_io_read_mb "$f: absent or unreadable"
    sampler_na cg_io_write_mb "$f: absent or unreadable"
    return 0
  fi
  while read -r -a words; do
    for w in "${words[@]}"; do
      case $w in
        rbytes=*) [[ ${w#rbytes=} =~ ^[0-9]+$ ]] && r=$((r + ${w#rbytes=})) || bad=$w ;;
        wbytes=*) [[ ${w#wbytes=} =~ ^[0-9]+$ ]] && wr=$((wr + ${w#wbytes=})) || bad=$w ;;
      esac
    done
  done < "$f"
  if [[ -n $bad ]]; then
    sampler_na cg_io_read_mb "$f: malformed $bad"
    sampler_na cg_io_write_mb "$f: malformed $bad"
    return 0
  fi
  sampler_mb "SAMPLER_COL[cg_io_read_mb]" "$r"
  sampler_mb "SAMPLER_COL[cg_io_write_mb]" "$wr"
}

sampler_psi() { # sampler_psi <column> <pressure file> <some|full>: total= µs of that line, as seconds
  local f=$2 kind total found=
  if [[ ! -r $f ]]; then sampler_na "$1" "$f: absent or unreadable"; return 0; fi
  while read -r kind _ _ _ total; do
    [[ $kind == "$3" ]] && { found=${total#total=}; break; }
  done < "$f"
  [[ $found =~ ^[0-9]+$ ]] || { sampler_na "$1" "$f: no '$3' line with total="; return 0; }
  stepprof_fmt "SAMPLER_COL[$1]" "$found" 6
}

sampler_proc_stat() { # vm_busy_s from the aggregate `cpu ` line, cpus from the cpuN lines
  local f=$SAMPLER_PROC/stat name u n s irq sirq steal rest cpus=0 busy='' x
  if [[ ! -r $f ]]; then
    sampler_na vm_busy_s "$f: absent or unreadable"
    sampler_na cpus "$f: absent or unreadable"
    return 0
  fi
  while read -r name u n s _ _ irq sirq steal rest; do
    case $name in
      cpu)
        busy=ok
        for x in "$u" "$n" "$s" "$irq" "$sirq" "$steal"; do
          [[ $x =~ ^[0-9]+$ ]] || busy=
        done
        [[ -n $busy ]] && busy=$((u + n + s + irq + sirq + steal))
        ;;
      cpu[0-9]*) cpus=$((cpus + 1)) ;;
      *) break ;;
    esac
  done < "$f"
  if [[ -z $busy ]]; then
    sampler_na vm_busy_s "$f: no complete 'cpu ' line"
  elif [[ -z $SAMPLER_TCK ]]; then
    sampler_na vm_busy_s "getconf CLK_TCK gave no number"
  else
    stepprof_fmt "SAMPLER_COL[vm_busy_s]" $((busy * 1000000 / SAMPLER_TCK)) 6
  fi
  if ((cpus > 0)); then SAMPLER_COL[cpus]=$cpus; else sampler_na cpus "$f: no cpuN line"; fi
}

sampler_uptime() { # sampler_uptime <var>: /proc/uptime field 1 in µs; 1 when unreadable
  local line v
  { read -r line < "$SAMPLER_PROC/uptime"; } 2> /dev/null || [[ -n ${line:-} ]] || return 1
  v=${line%% *}
  [[ $v =~ ^[0-9]+(\.[0-9]+)?$ ]] || return 1
  stepprof_us "$1" "$v"
}

sampler_loadavg() {
  local f=$SAMPLER_PROC/loadavg line v
  if [[ ! -r $f ]]; then sampler_na load1 "$f: absent or unreadable"; return 0; fi
  { read -r line < "$f"; } 2> /dev/null || [[ -n ${line:-} ]] || { sampler_na load1 "$f: empty"; return 0; }
  v=${line%% *}
  [[ $v =~ ^[0-9]+(\.[0-9]+)?$ ]] || { sampler_na load1 "$f: not a number: ${line:0:40}"; return 0; }
  SAMPLER_COL[load1]=$v
}

sampler_spin() { # spin_us: wall µs of SAMPLER_SPIN_ITERS iterations of a bash arithmetic loop
  local i=0 start end
  start=${EPOCHREALTIME/[.,]/}
  while ((i < SAMPLER_SPIN_ITERS)); do ((++i)); done
  end=${EPOCHREALTIME/[.,]/}
  if ((end < start)); then
    sampler_na spin_us "the wall clock stepped back during the spin loop"
  else
    SAMPLER_COL[spin_us]=$((end - start))
  fi
}

sampler_row() { # one sample, written and flushed; 1 when the row cannot be written or the time base is lost
  local up c row=
  SAMPLER_COL=()
  if ! sampler_uptime up; then
    echo "sampler: $SAMPLER_PROC/uptime became unreadable — the time base is lost, stopping" >&2
    return 1
  fi
  stepprof_fmt "SAMPLER_COL[uptime_s]" "$up" 6
  stepprof_fmt "SAMPLER_COL[epoch_s]" $((SAMPLER_EPOCH0_US + up - SAMPLER_UP0_US)) 6
  sampler_cpu_stat
  sampler_proc_stat
  sampler_io_stat
  sampler_mem_file cg_mem_mb "$SAMPLER_CG/memory.current"
  sampler_mem_file cg_mem_peak_mb "$SAMPLER_CG/memory.peak"
  sampler_mem_file cg_swap_mb "$SAMPLER_CG/memory.swap.current"
  sampler_psi cg_psi_cpu_some_s "$SAMPLER_CG/cpu.pressure" some
  sampler_psi cg_psi_io_some_s "$SAMPLER_CG/io.pressure" some
  sampler_psi cg_psi_io_full_s "$SAMPLER_CG/io.pressure" full
  sampler_psi cg_psi_mem_some_s "$SAMPLER_CG/memory.pressure" some
  sampler_psi cg_psi_mem_full_s "$SAMPLER_CG/memory.pressure" full
  sampler_psi vm_psi_cpu_some_s "$SAMPLER_PROC/pressure/cpu" some
  sampler_psi vm_psi_io_some_s "$SAMPLER_PROC/pressure/io" some
  sampler_psi vm_psi_io_full_s "$SAMPLER_PROC/pressure/io" full
  sampler_psi vm_psi_mem_some_s "$SAMPLER_PROC/pressure/memory" some
  sampler_psi vm_psi_mem_full_s "$SAMPLER_PROC/pressure/memory" full
  sampler_loadavg
  sampler_spin
  for c in "${SAMPLER_COLUMNS[@]}"; do row+=${row:+$'\t'}${SAMPLER_COL[$c]}; done
  sampler_emit "$row"
}

sampler_emit() { # write one line to --out; 1 with a stderr line when that fails
  { printf '%s\n' "$1" >&"$SAMPLER_OUT_FD"; } 2> /dev/null && return 0
  echo "sampler: cannot write a row to $SAMPLER_OUT" >&2
  return 1
}

sampler_wait() { # wait SAMPLER_INTERVAL_US in 0.1 s `read -t` slices, early on a stop
  local left=$SAMPLER_INTERVAL_US slice t
  while ((left > 0 && !SAMPLER_STOP)); do
    slice=$((left > 100000 ? 100000 : left))
    printf -v t '0.%06d' "$slice"
    read -r -t "$t" -u "$SAMPLER_WAIT_FD" _ || true
    left=$((left - slice))
  done
}

sampler_parent_alive() { # the --parent process exists and is not a zombie
  local line state
  if [[ -r /proc/$SAMPLER_PARENT/stat ]]; then
    { read -r line < "/proc/$SAMPLER_PARENT/stat"; } 2> /dev/null || return 1
    state=${line##*) }
    [[ ${state:0:1} != Z ]]
    return
  fi
  kill -0 "$SAMPLER_PARENT" 2> /dev/null
}

sampler_run() {
  local interval=${SAMPLER_INTERVAL_S:-0.5} iters=${SAMPLER_SPIN_ITERS:-10000} dir c header=
  SAMPLER_OUT='' SAMPLER_PARENT=''
  while (($# > 0)); do
    case $1 in
      --out) [[ $# -ge 2 ]] || { sampler_usage; return 64; }; SAMPLER_OUT=$2; shift 2 ;;
      --parent) [[ $# -ge 2 ]] || { sampler_usage; return 64; }; SAMPLER_PARENT=$2; shift 2 ;;
      *) echo "sampler: unknown argument $1" >&2; sampler_usage; return 64 ;;
    esac
  done
  [[ -n $SAMPLER_OUT ]] || { echo "sampler: --out is required" >&2; sampler_usage; return 64; }
  if [[ -n $SAMPLER_PARENT && ! $SAMPLER_PARENT =~ ^[0-9]+$ ]]; then
    echo "sampler: --parent is not a pid: $SAMPLER_PARENT" >&2
    return 64
  fi
  if [[ $interval =~ ^[0-9]+(\.[0-9]+)?$ ]]; then stepprof_us SAMPLER_INTERVAL_US "$interval"; else SAMPLER_INTERVAL_US=0; fi
  if ((SAMPLER_INTERVAL_US <= 0)); then echo "sampler: SAMPLER_INTERVAL_S is not a positive number: $interval" >&2; return 2; fi
  if [[ ! $iters =~ ^[0-9]+$ ]] || ((iters <= 0)); then echo "sampler: SAMPLER_SPIN_ITERS is not a positive integer: $iters" >&2; return 2; fi
  SAMPLER_SPIN_ITERS=$iters
  SAMPLER_CG=${SAMPLER_CGROUP_DIR:-/sys/fs/cgroup}
  SAMPLER_PROC=${SAMPLER_PROC_DIR:-/proc}
  [[ -n ${EPOCHREALTIME:-} ]] || { echo "sampler: bash $BASH_VERSION has no \$EPOCHREALTIME (bash 5 is required)" >&2; return 2; }

  if [[ $SAMPLER_OUT == *.tsv ]]; then SAMPLER_ERR=${SAMPLER_OUT%.tsv}.err; else SAMPLER_ERR=$SAMPLER_OUT.err; fi
  if ! { exec {SAMPLER_OUT_FD}> "$SAMPLER_OUT"; } 2> /dev/null; then
    if [[ $SAMPLER_OUT == */* ]]; then dir=${SAMPLER_OUT%/*}; else dir=.; fi
    if [[ -d $dir ]]; then echo "sampler: --out $SAMPLER_OUT cannot be written" >&2; else echo "sampler: --out $SAMPLER_OUT cannot be written: directory $dir is missing" >&2; fi
    return 2
  fi
  rm -f -- "$SAMPLER_ERR" # a stale one of an earlier run; this run's is created by its first NA

  SAMPLER_TCK=$(getconf CLK_TCK 2> /dev/null)
  [[ $SAMPLER_TCK =~ ^[1-9][0-9]*$ ]] || SAMPLER_TCK=
  sampler_uptime SAMPLER_UP0_US || { echo "sampler: $SAMPLER_PROC/uptime cannot be read — no time base" >&2; return 2; }
  SAMPLER_EPOCH0_US=${EPOCHREALTIME/[.,]/}
  exec {SAMPLER_WAIT_FD}<> <(:) # a pipe whose write end this shell holds: a read on it times out, never delivers
  trap 'SAMPLER_STOP=1' TERM INT

  for c in "${SAMPLER_COLUMNS[@]}"; do header+=${header:+$'\t'}$c; done
  sampler_emit "$header" || return 1
  while :; do
    sampler_row || return 1
    ((SAMPLER_STOP)) && return 0
    sampler_wait
    if ((!SAMPLER_STOP)) && [[ -n $SAMPLER_PARENT ]] && ! sampler_parent_alive; then SAMPLER_STOP=1; fi
  done
}

case ${1:-} in
  run) shift; sampler_run "$@"; exit $? ;;
  *) sampler_usage; exit 64 ;;
esac
