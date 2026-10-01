#!/usr/bin/env bash
# Sourced by dev.sh. Registration is ordered; each command runs in its own shell.

steps_reset() {
  STEPS_NAMES=()
  STEPS_COMMANDS=()
  STEPS_HEAVY=()
  STEPS_PHASE=()
  STEPS_PHASE_ID=0
}

steps_add() { # steps_add <name> <command> [args...]
  local name="${1:-}" old quoted
  if [[ ! "$name" =~ ^[a-z0-9._-]+$ ]] || (( $# < 2 )); then
    printf 'steps_add: invalid step name or missing command: %s\n' "$name" >&2
    return 1
  fi
  for old in "${STEPS_NAMES[@]:-}"; do
    if [[ "$old" == "$name" ]]; then
      printf 'steps_add: duplicate step name: %s\n' "$name" >&2
      return 1
    fi
  done
  shift
  printf -v quoted '%q ' "$@"
  STEPS_NAMES+=("$name")
  STEPS_COMMANDS+=("$quoted")
  STEPS_HEAVY+=(0)
  STEPS_PHASE+=("$STEPS_PHASE_ID")
}

steps_add_heavy() { # steps_add_heavy <name> <command> [args...]
  steps_add "$@" || return 1
  STEPS_HEAVY[$((${#STEPS_HEAVY[@]} - 1))]=1
}

steps_barrier() {
  STEPS_PHASE_ID=$((STEPS_PHASE_ID + 1))
}

steps_seconds() { # start and end as EPOCHREALTIME values
  awk -v a="$1" -v b="$2" 'BEGIN { d=b-a; if (d<0) d=0; printf "%.1f", d }'
}

steps_jobs() {
  local jobs="${STEPS_JOBS:-}"
  if [[ -z "$jobs" ]]; then
    jobs="$(nproc 2>/dev/null)" || jobs=""
    [[ "$jobs" =~ ^[1-9][0-9]*$ ]] || jobs="$(getconf _NPROCESSORS_ONLN 2>/dev/null)" || jobs=""
    [[ "$jobs" =~ ^[1-9][0-9]*$ ]] || jobs=4
  fi
  if [[ ! "$jobs" =~ ^[1-9][0-9]*$ ]]; then
    printf 'steps_run: STEPS_JOBS must be a positive integer: %s\n' "$jobs" >&2
    return 1
  fi
  printf '%s\n' "$jobs"
}

steps_heavy_jobs() {
  local jobs="${STEPS_HEAVY_JOBS:-2}"
  if [[ ! "$jobs" =~ ^[1-9][0-9]*$ ]]; then
    printf 'steps_run: STEPS_HEAVY_JOBS must be a positive integer: %s\n' "$jobs" >&2
    return 1
  fi
  printf '%s\n' "$jobs"
}

steps_run() { # steps_run <run-dir>
  local run="$1" jobs heavy_jobs i n="${#STEPS_NAMES[@]}" active=0 heavy_active=0 completed=0 phase=0 red=0 first="" last=""
  local name log meta command start finish elapsed pid rc failures done_pid row pick
  local -a verdict=() seconds=() pids=() starts=() started=()
  local -A pid_index=()
  jobs="$(steps_jobs)" || return 1
  heavy_jobs="$(steps_heavy_jobs)" || return 1
  if ! mkdir -p "$run/steps"; then
    printf 'steps_run: cannot create step logs under %s\n' "$run" >&2
    return 1
  fi
  while (( completed < n )); do
    while (( active < jobs )); do
      pick=-1
      for (( i=0; i<n; i++ )); do
        [[ "${started[$i]:-}" ]] && continue
        (( ${STEPS_PHASE[$i]} == phase )) || continue
        if (( ${STEPS_HEAVY[$i]} == 0 || heavy_active < heavy_jobs )); then
          pick=$i
          break
        fi
      done
      (( pick >= 0 )) || break
      i=$pick
      started[$i]=1
      name="${STEPS_NAMES[$i]}"; log="$run/steps/$name.log"; meta="$run/steps/$name.failures"
      start="$EPOCHREALTIME"
      [[ -n "$first" ]] || first="$start"
      if ! : > "$log"; then
        printf 'steps_run: log not writable: %s\n' "$log" >&2
        verdict[$i]=NOT-RUN; seconds[$i]=0.0; red=1
        completed=$((completed + 1))
      else
        eval "set -- ${STEPS_COMMANDS[$i]}"
        command="$1"
        if ! type "$command" >/dev/null 2>&1; then
          printf 'steps_run: command not found: %s\n' "$command" > "$log"
          verdict[$i]=NOT-RUN; seconds[$i]=0.0; red=1
          completed=$((completed + 1))
        else
          (
            set +e
            FAILURES=0
            "$@" > "$log" 2>&1
            rc=$?
            printf '%s\n' "$FAILURES" > "$meta"
            exit "$rc"
          ) &
          pid=$!
          pids+=("$pid"); starts[$i]="$start"; pid_index[$pid]="$i"
          active=$((active + 1))
          if (( ${STEPS_HEAVY[$i]} )); then heavy_active=$((heavy_active + 1)); fi
        fi
      fi
      last="$EPOCHREALTIME"
    done
    if (( active > 0 )); then
      done_pid=""
      if wait -n -p done_pid "${pids[@]}" 2>/dev/null; then rc=0; else rc=$?; fi
      if [[ -z "${done_pid:-}" ]]; then
        # Bash may have reaped a child before wait -n sees it; wait by PID
        # still returns that child's status.
        done_pid="${pids[0]}"
        if wait "$done_pid"; then rc=0; else rc=$?; fi
      fi
      row="${pid_index[$done_pid]}"
      finish="$EPOCHREALTIME"; last="$finish"
      elapsed="$(steps_seconds "${starts[$row]}" "$finish")"
      seconds[$row]="$elapsed"
      failures="$(cat "$run/steps/${STEPS_NAMES[$row]}.failures" 2>/dev/null)" || failures=""
      if (( rc > 128 )); then verdict[$row]=NOT-RUN; red=1
      elif (( rc != 0 )) || [[ ! "$failures" =~ ^[0-9]+$ ]] || (( failures > 0 )); then verdict[$row]=FAIL; red=1
      else verdict[$row]=PASS
      fi
      local -a remaining=()
      for pid in "${pids[@]}"; do [[ "$pid" == "$done_pid" ]] || remaining+=("$pid"); done
      pids=("${remaining[@]}")
      active=$((active - 1))
      if (( ${STEPS_HEAVY[$row]} )); then heavy_active=$((heavy_active - 1)); fi
      completed=$((completed + 1))
    else
      phase=$((phase + 1))
    fi
  done
  [[ -n "$first" ]] || first="$EPOCHREALTIME"
  [[ -n "$last" ]] || last="$first"
  if ! {
    printf 'step\tverdict\tseconds\n'
    for (( i=0; i<n; i++ )); do
      printf '%s\t%s\t%s\n' "${STEPS_NAMES[$i]}" "${verdict[$i]}" "${seconds[$i]}"
    done
    if (( red )); then row=FAIL; else row=PASS; fi
    printf 'WALL\t%s\t%s\n' "$row" "$(steps_seconds "$first" "$last")"
  } > "$run/gate.tsv"; then
    printf 'steps_run: cannot write table: %s/gate.tsv\n' "$run" >&2
    return 1
  fi
  printf 'step\tverdict\tseconds\n'
  for (( i=0; i<n; i++ )); do
    printf '%s\t%s\t%s' "${STEPS_NAMES[$i]}" "${verdict[$i]}" "${seconds[$i]}"
    [[ "${verdict[$i]}" == PASS ]] || printf '\t%s/steps/%s.log' "$run" "${STEPS_NAMES[$i]}"
    printf '\n'
  done
  if (( red )); then row=FAIL; else row=PASS; fi
  printf 'WALL\t%s\t%s\n' "$row" "$(steps_seconds "$first" "$last")"
  for (( i=0; i<n; i++ )); do
    [[ "${verdict[$i]}" == PASS ]] || continue
    log="$run/steps/${STEPS_NAMES[$i]}.log"
    while IFS= read -r warning; do
      printf 'WARN %s: %s — %s\n' "${STEPS_NAMES[$i]}" "$warning" "$log"
    done < <(grep '^GATE-WARN ' "$log" || true)
  done
  (( red == 0 ))
}
