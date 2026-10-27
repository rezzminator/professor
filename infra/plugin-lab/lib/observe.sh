# shellcheck shell=bash
# lib/observe.sh: what the lab reads. Plugin witness: the plugin's level and
# decision logs. Engine witness: the transcript, the marks, the debug file, the
# process env. Every could-not-look prints its named ERROR line on stdout and
# returns 2, so a caller captures it as evidence; never an empty PASS.

witness_match() { # <plugin rows JSON> <boundary preTokens JSON>: two-witness at ±5% of the boundary (D17, L15)
  local out
  if ! out=$(jq -rn --argjson r "$1" --argjson b "$2" '
    def tok: (.tokens // .before // error("plugin row without tokens or before: \(tojson)"));
    def dist($x; $y): if $x > $y then $x - $y else $y - $x end;
    ($r | map(tok)) as $rt
    | if ($rt | length) == 0 and ($b | length) > 0 then $b[] | "FAIL boundary without plugin row preTokens=\(.)"
      elif ($b | length) == 0 and ($rt | length) > 0 then $rt[] | "FAIL plugin row without boundary tokens=\(.)"
      else
        (reduce $rt[] as $t ({used: ($b | map(false)), miss: []};
          (first(range(0; $b | length) as $i | select((.used[$i] | not) and 100 * dist($t; $b[$i]) <= 5 * $b[$i]) | $i) // null) as $k
          | if $k == null then .miss += [$t] else .used[$k] = true end)) as $s
        | [range(0; $b | length) | select($s.used[.] | not) | $b[.]] as $ub
        | if ($s.miss | length) == 0 and ($ub | length) == 0 then "PASS \($rt | length) matched"
          else ($s.miss[] | "FAIL unmatched plugin row tokens=\(.)"), ($ub[] | "FAIL unmatched boundary preTokens=\(.)") end
      end' 2>&1); then
    printf 'ERROR witness-unreadable %s\n' "${out%%$'\n'*}"
    return 2
  fi
  printf '%s\n' "$out"
  [[ $out == PASS* ]]
}

lab_jsonl_check() { # <file>: the first line that is not a JSON object, as ERROR transcript-unreadable
  local bad
  [[ -r $1 ]] || { printf 'ERROR transcript-unreadable %s:0\n' "$1"; return 2; }
  bad=$(jq -nR 'first(foreach inputs as $l (0; . + 1; . as $n
    | select($l != "" and ((try ($l | fromjson) catch null) | type) != "object") | $n))' "$1")
  [[ -z $bad ]] || { printf 'ERROR transcript-unreadable %s:%s\n' "$1" "$bad"; return 2; }
}

transcript_boundaries() { # <transcript>: JSON array, per compact_boundary the context of the last reply before it
  # (the tokens its compaction ask carries; preTokens adds the tool results read since), else its preTokens
  lab_jsonl_check "$1" || return 2
  jq -sc 'reduce .[] as $e ({last: null, out: []};
    if $e.type == "assistant" and $e.message.usage != null then
      .last = ($e.message.usage | (.input_tokens // 0) + (.cache_read_input_tokens // 0) + (.cache_creation_input_tokens // 0))
    elif $e.type == "system" and $e.subtype == "compact_boundary" then .out += [.last // $e.compactMetadata.preTokens] | .last = null
    else . end) | .out' "$1"
}

lab_rows() { # <jsonl file> <jq filter>: matching rows; a missing file is LOG-ABSENT
  local out
  [[ -f $1 ]] || { printf 'ERROR LOG-ABSENT %s\n' "$1"; return 2; }
  out=$(jq -rc "$2" "$1" 2>&1) || { printf 'ERROR unreadable %s: %s\n' "$1" "${out%%$'\n'*}"; return 2; }
  [[ -z $out ]] || printf '%s\n' "$out"
}

lab_decision_file() { # LAB_DECISIONS with {run} and {sid} expanded, else the default per-session log
  local f=${LAB_DECISIONS:-'{run}/logs/{plugin}/decisions/{sid}.jsonl'}
  f=${f//\{run\}/$LAB_RUN}; f=${f//\{plugin\}/$LAB_PLUGIN}
  printf '%s\n' "${f//\{sid\}/${LAB_SID:-unknown-session}}"
}

decision_rows() { lab_rows "$(lab_decision_file)" "$1"; } # <jq filter>

level_rows() { lab_rows "$LAB_RUN/logs/$LAB_PLUGIN/$1.jsonl" "$2"; } # <level> <jq filter>

lab_bypass_lines() { # <debug file>: the lines naming a hook bypass
  [[ -f $1 ]] || { printf 'ERROR could-not-look\n'; return 2; }
  grep -F 'bypassed by' "$1"
  (( $? <= 1 )) || { printf 'ERROR could-not-look\n'; return 2; }
  return 0
}

lab_ps_env_keys() { # <ps command line> <argv prefix>: env key names after the argv (macOS ps -E)
  [[ $1 == "$2"* ]] || { printf 'ERROR could-not-look ps line does not start with the claude argv\n'; return 2; }
  grep -oE '(^| )[A-Za-z_][A-Za-z0-9_]*=' <<< "${1#"$2"}" | sed 's/^ //; s/=$//' | LC_ALL=C sort -u
}

lab_proc_env_keys() { tr '\0' '\n' < "/proc/$1/environ" | sed 's/=.*//' | LC_ALL=C sort -u; } # <pid> (Linux)

lab_env_observe() { # the claude process's env key names, observed from outside the process
  if [[ -r /proc/$LAB_PID/environ ]]; then lab_proc_env_keys "$LAB_PID"; return; fi
  lab_ps_env_keys "$(ps -E -ww -o command= -p "$LAB_PID" 2>&1)" "$(lab_argv | tr '\n' ' ' | sed 's/ $//')"
}

lab_rows_verdict() { # <want: has|lacks> <min> <label> <rows or ERROR>: shared verdict of the row verbs
  local n=0
  if [[ $4 == ERROR* ]]; then LAB_EV=${4#ERROR }; return 2; fi
  [[ -n $4 ]] && n=$(wc -l <<< "$4" | tr -d ' ')
  if [[ $1 == has ]]; then
    LAB_EV="$n rows $3"
    (( n >= $2 ))
  else
    LAB_EV="$n rows $3${4:+: ${4%%$'\n'*}}"
    (( n == 0 ))
  fi
}

decision_has() { lab_rows_verdict has "${2:-1}" "$1" "$(decision_rows "$1")"; } # <jq select> [min]
decision_lacks() { lab_rows_verdict lacks 0 "$1" "$(decision_rows "$1")"; } # <jq select>
level_has() { lab_rows_verdict has "${3:-1}" "$1 $2" "$(level_rows "$1" "$2")"; } # <level> <jq select> [min]
level_lacks() { # <level> <jq select>: the logger writes a level file at its first row, so none in a log folder holding rows holds no row
  local d=$LAB_RUN/logs/$LAB_PLUGIN
  if [[ ! -e $d/$1.jsonl && -d $d && -n $(find "$d" -type f -name '*.jsonl' -size +0 2>/dev/null) ]]; then
    LAB_EV="0 rows $1 $2 (no $1.jsonl in $d, which holds rows)"; return 0
  fi
  lab_rows_verdict lacks 0 "$1 $2" "$(level_rows "$1" "$2")"
}
jsonl_has() { lab_rows_verdict has 1 "$1 $2" "$(lab_rows "${1//\{run\}/$LAB_RUN}" "$2")"; } # <file with {run}> <jq select>
transcript_has() { lab_jsonl_check "${LAB_TRANSCRIPT:-}" >/dev/null || { LAB_EV="transcript-unreadable ${LAB_TRANSCRIPT:-}"; return 2; }; lab_rows_verdict has 1 "$1" "$(lab_rows "$LAB_TRANSCRIPT" "$1")"; }
agent_transcript_has() { lab_rows_verdict has 1 "$1" "$(lab_rows "${LAB_AGENT_TRANSCRIPT:-none}" "$1")"; } # <jq select>
mark_has() { lab_rows_verdict has 1 "$1" "$(lab_rows "$LAB_RUN/marks.jsonl" "$1")"; } # <jq select>

lab_rows_count() { # <label> <-le|-ge|-eq> <n> <rows or ERROR>: shared verdict of the count verbs
  local n=0
  if [[ $4 == ERROR* ]]; then LAB_EV=${4#ERROR }; return 2; fi
  [[ -n $4 ]] && n=$(wc -l <<< "$4" | tr -d ' ')
  LAB_EV="$n rows $1 (want $2 $3)"
  test "$n" "$2" "$3"
}

decision_count() { lab_rows_count "$1" "$2" "$3" "$(decision_rows "$1")"; } # <jq select> <-le|-ge|-eq> <n>
level_count() { lab_rows_count "$1 $2" "$3" "$4" "$(level_rows "$1" "$2")"; } # <level> <jq select> <-le|-ge|-eq> <n>

decision_first() { # <event>: line 1 of the decision log carries that event; an empty or unparseable line 1 is could-not-look
  local f first
  f=$(lab_decision_file)
  [[ -f $f ]] || { LAB_EV="LOG-ABSENT $f"; return 2; }
  [[ -s $f ]] || { LAB_EV="could-not-look $f is empty"; return 2; }
  first=$(head -n 1 "$f" | jq -r '.event // empty' 2>&1) || { LAB_EV="unreadable $f line 1: ${first%%$'\n'*}"; return 2; }
  [[ -n $first ]] || { LAB_EV="could-not-look $f line 1 has no event"; return 2; }
  LAB_EV="line 1 event $first (want $1)"
  [[ $first == "$1" ]]
}

decision_precondition() { # <jq select>: absent is an ERROR precondition, never a FAIL
  decision_has "$1" && return 0
  [[ $? == 2 ]] && return 2
  LAB_EV="precondition $LAB_EV"
  return 2
}

level_precondition() { # <level> <jq select> [min]: fewer rows is an ERROR precondition, never a FAIL
  level_has "$@" && return 0
  [[ $? == 2 ]] && return 2
  LAB_EV="precondition $LAB_EV"
  return 2
}

decision_prefix_save() { # remembers the decision log's byte length and sha256
  local f; f=$(lab_decision_file)
  [[ -f $f ]] || { LAB_EV="LOG-ABSENT $f"; return 2; }
  LAB_PREFIX_LEN=$(wc -c < "$f" | tr -d ' ')
  LAB_PREFIX_SHA=$(lab_sha256 "$(head -c "$LAB_PREFIX_LEN" "$f")")
  LAB_EV="prefix $LAB_PREFIX_LEN bytes"
}

decision_prefix_kept() { # the saved prefix is unchanged (append-only) and no {file}.tmp is left
  local f; f=$(lab_decision_file)
  [[ -f $f && -n ${LAB_PREFIX_LEN:-} ]] || { LAB_EV="could-not-look $f or no saved prefix"; return 2; }
  [[ -e $f.tmp ]] && { LAB_EV="leftover $f.tmp"; return 1; }
  LAB_EV="prefix $LAB_PREFIX_LEN bytes"
  [[ $(lab_sha256 "$(head -c "$LAB_PREFIX_LEN" "$f")") == "$LAB_PREFIX_SHA" ]] || { LAB_EV="rewritten: the first $LAB_PREFIX_LEN bytes changed"; return 1; }
}

file_absent() { local p=${1//\{run\}/$LAB_RUN}; LAB_EV="$p"; [[ ! -e $p ]] || { LAB_EV="present $p"; return 1; }; } # <path with {run}>

lab_last_reply() { # the main transcript's last assistant text
  jq -rs '[.[] | select(.type == "assistant" and (.isSidechain | not)) | .message.content[]? | select(.type == "text") | .text] | last // ""' "$LAB_TRANSCRIPT" 2>/dev/null
}

reply_has() { # <text>
  local r
  r=$(lab_last_reply) || { LAB_EV="transcript-unreadable ${LAB_TRANSCRIPT:-}"; return 2; }
  LAB_EV="last reply: ${r:0:120}"
  [[ $r == *"$1"* ]]
}

pane_lacks() { # <text>: no pane text contains it
  local f; f=$(lab_pane_save "${LAB_BEAT:-pane}") || { LAB_EV="could-not-look pane capture failed $f"; return 2; }
  [[ -s $f ]] || { LAB_EV="could-not-look pane $f"; return 2; }
  LAB_EV="$f"
  ! grep -qF -- "$1" "$f" || { LAB_EV="pane holds '$1' $f"; return 1; }
}

lab_context() { # the main context size: last main assistant usage, input plus cache
  jq -rs '[.[] | select(.type == "assistant" and (.isSidechain | not)) | .message.usage // empty
    | (.input_tokens // 0) + (.cache_creation_input_tokens // 0) + (.cache_read_input_tokens // 0)] | last // 0' "${LAB_TRANSCRIPT:-}" 2>/dev/null
}

lab_read_seen() { # <corpus name>: a main Read tool_use took corpus/{name}.txt whole; else ERROR fill-not-read, naming
  # the tools that named the file (a model answering by `tail` fills nothing: the lab could not induce the fill)
  local out
  out=$(jq -nrR --arg f "/corpus/$1.txt" '[inputs | fromjson? // empty | select(.type == "assistant" and (.isSidechain | not))
      | .message.content[]? | objects | select(.type == "tool_use" and (.input | tostring | contains($f)))] as $t
    | if any($t[]; .name == "Read" and ((.input.file_path // "") | endswith($f)) and .input.offset == null and .input.limit == null)
      then "read" else $t | map(if .name == "Read" then "Read(part)" else .name end) | unique | join(",") end' \
    "${LAB_TRANSCRIPT:-/dev/null}" 2>/dev/null) || { LAB_EV="fill-not-read $1: transcript unreadable ${LAB_TRANSCRIPT:-}"; return 2; }
  [[ $out == read ]] && return 0
  LAB_EV="fill-not-read $1: ${out:-no tool named it}"
  return 2
}

lab_read_turn() { # <corpus name>: one turn reading corpus/{name}.txt whole (a model asked only for the last word
  # reads the tail by offset once it knows the layout), proven by lab_read_seen
  turn "Read corpus/$1.txt whole with the Read tool, no offset or limit, and reply with its last word." 600 || return $?
  lab_read_seen "$1"
}

read_turns() { # <prefix> <from> <to> [stop at main context N]: one read turn per corpus file; each reply names its last word
  local i w rc ctx
  for (( i = 10#$2; i <= 10#$3; i++ )); do
    w=$(printf '%s-%02d' "$1" "$i")
    lab_read_turn "$w" || return $?
    reply_has "$w-END" || { LAB_EV="$w: $LAB_EV"; return 1; }
    ctx=$(lab_context) || { LAB_EV="transcript-unreadable ${LAB_TRANSCRIPT:-}"; return 2; }
    if [[ -n ${4:-} ]] && (( ctx >= $4 )); then LAB_EV="read to $w, context $ctx"; return 0; fi
  done
  LAB_EV="read $1-$2..$3, context ${ctx:-0}"
  [[ -z ${4:-} ]] && return 0
  LAB_EV="context ${ctx:-0} below $4 after $1-$3"
  return 1
}

fill_until_boundary() { # <prefix> <from> <to>: read turns until a main compact_boundary; GAP when the declared bypass is not induced
  local i w b rc
  (( 10#$2 <= 10#$3 )) || { LAB_EV="fill range $1-$2..$3 is empty"; return 2; }
  for (( i = 10#$2; i <= 10#$3; i++ )); do
    w=$(printf '%s-%02d' "$1" "$i")
    lab_read_turn "$w" || return $?
    b=$(transcript_boundaries "$LAB_TRANSCRIPT") || { LAB_EV=${b#ERROR }; return 2; }
    [[ $b != '[]' ]] && break
  done
  # unseated, the plugin answers the ask itself, so a declared bypass never induced is a GAP, boundary or none
  if [[ -n ${LAB_EXPECT_BYPASS[*]:-} ]]; then
    grep -qF 'bypassed by' "$LAB_RUN/debug.txt" 2>/dev/null; rc=$?
    (( rc <= 1 )) || { LAB_EV="could-not-look $LAB_RUN/debug.txt"; return 2; }
    if (( rc == 1 )); then
      LAB_EV="not-induced ${LAB_GAP_IF_NOT_INDUCED:-no bypass line in debug.txt}" LAB_FATAL=1
      return 3
    fi
  fi
  [[ $b != '[]' ]] || { LAB_EV="no compact_boundary after $1-$3"; return 1; }
  LAB_EV="boundary at $b"
  return 0
}

bypass_seated() { # <builtin plugin id>: the plugin carrying the induced bypass was seated at boot; its "not seated" line is a GAP
  local f=$LAB_RUN/debug.txt line
  [[ -f $f ]] || { LAB_EV="could-not-look $f"; return 2; }
  line=$(grep -F "$1 not seated" "$f" | head -n 1)
  if [[ -n $line ]]; then
    LAB_EV="not-induced ${LAB_GAP_IF_NOT_INDUCED:-$1 not seated}: ${line#*\[DEBUG\] }" LAB_FATAL=1
    return 3
  fi
  line=$(grep -F 'plugin.register: ' "$f" | grep -F ", $1)" | head -n 1)
  [[ -n $line ]] || { LAB_EV="seat-unknown $1 in $f"; return 2; }
  LAB_EV="seated: ${line#*\[DEBUG\] }"
}

witness_compactions() { # [--agent TYPE]: plugin compaction rows matched to transcript boundaries (D17)
  local rc t=${LAB_TRANSCRIPT:-} compacted='((.event == "compact" and .decision != "skip" and .decision != "foreign") or .event == "unseen-compaction")' filter b rows out
  filter="select($compacted and .party == \"main\")"
  if [[ ${1:-} == --agent ]]; then
    t=${LAB_AGENT_TRANSCRIPT:-}
    filter="select($compacted and .party == \"agent:${LAB_AGENT_ID:-none}\")"
  fi
  b=$(transcript_boundaries "$t") || { LAB_EV=${b#ERROR }; return 2; }
  rows=$(decision_rows "$filter") || { LAB_EV=${rows#ERROR }; return 2; }
  rows=$(printf '%s\n' "$rows" | jq -sc 'map(select(. != null))')
  [[ $b == '[]' && $rows == '[]' ]] && { LAB_EV='no compaction on either side'; return 1; }
  out=$(witness_match "$rows" "$b"); rc=$?
  LAB_EV=$(tr '\n' ';' <<< "$out" | sed 's/;$//; s/;/; /g')
  return "$rc"
}
