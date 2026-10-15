# shellcheck shell=bash
# lib/meter.sh: spend through the LAB_METER seam (D9; default pfm tokens over the
# run's own projects root, so every session of the run counts), the cap, and the
# identity-checked stop (L10). A failed or unpriced meter never reads as $0.

lab_meter_read() { # [--final]: sets LAB_USD (%.2f); ERROR on a failed meter, unpriced or undated calls, read errors,
  # and, --final (the session ended, so no line is mid-append), malformed lines: each is spend the meter dropped
  local out=$LAB_RUN/meter.json err total final=false
  [[ ${1:-} == --final ]] && final=true
  if ! err=$("$LAB_METER" tokens --root "$LAB_RUN/config/projects" --out "$out" 2>&1 >/dev/null); then
    LAB_METER_EV="meter: ${err%%$'\n'*}"
    printf 'ERROR %s\n' "$LAB_METER_EV" >&2
    return 2
  fi
  total=$(jq -r --argjson final "$final" 'if (.scan.unpricedCalls // 0) > 0 then "unpriced calls \(.scan.unpricedCalls)"
    elif (.scan.readErrors // []) | length > 0 then "read errors \(.scan.readErrors | length)"
    elif (.scan.noTimestamp // 0) > 0 then "calls without a timestamp \(.scan.noTimestamp)"
    elif $final and (.scan.badLines // 0) > 0 then "malformed lines \(.scan.badLines)"
    elif (.total | type) != "number" then "no total"
    else .total end' "$out" 2>/dev/null)
  if [[ ! $total =~ ^[0-9.eE+-]+$ ]]; then
    LAB_METER_EV="meter ${total:-unreadable $out}"
    printf 'ERROR %s\n' "$LAB_METER_EV" >&2
    return 2
  fi
  LAB_USD=$(awk -v t="$total" 'BEGIN { printf "%.2f", t }')
}

lab_over_cap() { awk -v u="$LAB_USD" -v c="$LAB_CAP" 'BEGIN { exit !(u + 0 >= c + 0) }'; }

lab_meter_poll() { # 0 under the cap · 2 meter error (the run stops) · 5 STOPPED budget
  [[ -n ${LAB_STOPPED:-} ]] && return 5
  if ! lab_meter_read 2>/dev/null; then
    LAB_EV=$LAB_METER_EV LAB_FATAL=1
    lab_stop
    return 2
  fi
  if lab_over_cap; then
    LAB_STOPPED="budget $LAB_USD/$LAB_CAP" LAB_EV="budget $LAB_USD/$LAB_CAP" LAB_FATAL=1
    lab_stop
    return 5
  fi
}

lab_stop() { # kills claude only behind its identity check (this run's binary and its own settings file), then the private tmux server
  if [[ -n ${LAB_PID:-} && -n ${LAB_CLAUDE:-} && -n ${LAB_RUN:-} ]]; then
    ps -o args= -p "$LAB_PID" 2>/dev/null | grep -qF -- "$LAB_CLAUDE --setting-sources project --settings $LAB_RUN/settings.json " && kill "$LAB_PID" 2>/dev/null
  fi
  [[ -S $LAB_RUN/t.sock ]] && lab_tmux kill-server 2>/dev/null
  LAB_TORN=1
  return 0
}
