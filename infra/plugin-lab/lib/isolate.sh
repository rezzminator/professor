# shellcheck shell=bash
# lib/isolate.sh: the run's isolation (D5, D10, D11, D14): the exact env allowlist,
# settings.json with the installed copy off and the lab hooks, managed settings,
# the .claude.json seed and the launcher that hands fd 3 to claude under env -i.

lab_options() { LAB_OPTIONS=$1; } # scenario header: the options JSON of a plugin that declares options; sub-agent-compact reads none (its SUB_AGENT_COMPACT_* variables go in LAB_ENV) and vital/options-ignored fails a run that sets any

lab_env_lines() { # the D5 allowlist, the four auth keys and the scenario's LAB_ENV, one KEY=value per line
  local r=$LAB_RUN e
  printf '%s\n' "HOME=$r/home" "XDG_CONFIG_HOME=$r/home/.config" "XDG_DATA_HOME=$r/home/.local/share" \
    "XDG_CACHE_HOME=$r/home/.cache" "XDG_STATE_HOME=$r/home/.local/state" \
    "TMPDIR=$r/tmp" "TMP=$r/tmp" "TEMP=$r/tmp" "CLAUDE_CODE_TMPDIR=$r/tmp" \
    "CLAUDE_CONFIG_DIR=$r/config" "CLAUDE_CODE_MANAGED_SETTINGS_PATH=$r/managed" \
    CLAUDE_CODE_DISABLE_CLAUDE_MDS=1 CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 \
    ENABLE_CLAUDEAI_MCP_SERVERS=false DISABLE_AUTOUPDATER=1 \
    "PLUGIN_LOG_DIR=$r/logs" CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1 \
    TERM=xterm-256color LANG=en_US.UTF-8 "PATH=/usr/bin:/bin:/usr/sbin:/sbin:${LAB_JQ_DIR:-/usr/bin}"
  lab_auth_env
  for e in "${LAB_ENV[@]}"; do printf '%s\n' "${e//\{run\}/$r}"; done
}

lab_env_keys() { lab_env_lines | sed 's/=.*//' | LC_ALL=C sort -u; }

lab_installed_ids() { # <store> <plugin name>: every installed id of that plugin in the live store
  local f=$1/plugins/installed_plugins.json out
  [[ -e $f ]] || return 0
  if ! out=$(jq -r --arg n "$2" '.plugins | if type == "object" then keys[] | select(split("@")[0] == $n)
      else error("plugins is \(type), want an object keyed name@marketplace") end' "$f" 2>&1); then
    printf 'ERROR could-not-look %s: %s\n' "$f" "$out" >&2
    return 2
  fi
  [[ -z $out ]] || printf '%s\n' "$out"
}

lab_settings_render() { # <plugin name> <live id>...: {run}/settings.json
  local name=$1; shift
  LAB_OPTIONS=${LAB_OPTIONS//\{run\}/$LAB_RUN}
  jq -n --arg name "$name" --arg run "$LAB_RUN" --arg lab "$LAB_LAB" --argjson options "${LAB_OPTIONS:-{\}}" \
    --argjson allow "$(jq -n '$ARGS.positional | map(select(length > 0))' --args "${LAB_ALLOW[@]}")" \
    --argjson deny "$(jq -n '$ARGS.positional | map(select(length > 0))' --args "${LAB_DENY[@]}")" \
    '{enabledPlugins: ($ARGS.positional | map({(.): false}) | add // {}),
      pluginConfigs: {("\($name)@inline"): {options: $options}},
      permissions: {defaultMode: "dontAsk", allow: $allow, deny: $deny},
      hooks: (reduce ("SessionStart", "UserPromptSubmit", "Stop", "SubagentStop", "PreCompact", "SessionEnd") as $e
        ({}; .[$e] = [{hooks: [{type: "command", command: "LAB_MARKS=\("\($run)/marks.jsonl" | @sh) \("\($lab)/hooks/mark.sh" | @sh) \($e)"}]}]))}' \
    --args "$@" > "$LAB_RUN/settings.json"
}

lab_managed_render() { # [json]: {run}/managed/managed-settings.json, default {"prependPlugins":[]}
  local m='{"prependPlugins":[]}'
  [[ -n ${1:-} ]] && m=$1
  mkdir -p "$LAB_RUN/managed" && jq -n --argjson m "$m" '$m' > "$LAB_RUN/managed/managed-settings.json"
}

lab_seed_config() { # {run}/config/.claude.json: onboarding done, the work dir trusted under both spellings
  local w=$LAB_RUN/work real
  mkdir -p "$LAB_RUN/config" "$w" || return 2
  real=$(cd "$w" && pwd -P)
  jq -n --arg w "$w" --arg r "$real" '{hasCompletedOnboarding: true, theme: "dark",
      projects: ([$w, $r] | unique | map({(.): {hasTrustDialogAccepted: true, hasCompletedProjectOnboarding: true}}) | add)}' \
    > "$LAB_RUN/config/.claude.json"
}

lab_argv() { # the claude argv, one word per line (the launcher and the ps passport share it)
  printf '%s\n' "$LAB_CLAUDE" --setting-sources project --settings "$LAB_RUN/settings.json" \
    --plugin-dir "$LAB_PLUGIN_DIR" --model "$LAB_MODEL" --debug-file "$LAB_RUN/debug.txt"
}

lab_launch_render() { # {run}/launch.sh: opens fd 3 from the FIFO, removes it, execs claude under env -i
  local f=$LAB_RUN/launch.sh line words=()
  mapfile -t words < <(lab_argv)
  {
    printf '#!/bin/bash\n# plugin-lab launcher, run %s: the token arrives on fd 3, never argv or env.\n' "${LAB_RUN_ID:-}"
    printf 'cd %q || exit 1\n' "$LAB_RUN/work"
    printf 'exec 3<%q || exit 1\n' "$LAB_RUN/sec/oauth.fifo"
    printf 'rm -f %q\n' "$LAB_RUN/sec/oauth.fifo"
    printf 'exec /usr/bin/env -i \\\n'
    while IFS= read -r line; do printf '  %q \\\n' "$line"; done < <(lab_env_lines)
    printf '  %s\n' "$(printf '%q ' "${words[@]}" | sed 's/ $//')"
  } > "$f" && chmod 700 "$f"
}

need_agent() { # <name> <model> <tools> [autoCompact map '{key: value, ...}']: a project agent in {run}/work/.claude/agents/
  # the map is written as nested lines: the plugin's frontmatter reader takes no flow map
  local d=$LAB_RUN/work/.claude/agents pair pairs=()
  if [[ -n ${4:-} ]]; then
    IFS=',' read -r -a pairs <<< "${4//[\{\}]/}"
  fi
  mkdir -p "$d" && {
    printf -- '---\nname: %s\ndescription: plugin-lab agent %s\nmodel: %s\ntools: %s\n' "$1" "$1" "$2" "$3"
    (( ${#pairs[@]} )) && printf 'autoCompact:\n'
    for pair in "${pairs[@]}"; do printf '  %s\n' "$(sed 's/^ *//; s/ *$//' <<< "$pair")"; done
    printf -- '---\n\nFollow the task you are given exactly, using only your tools. Reply briefly.\n'
  } > "$d/$1.md" || lab_need_fail "need_agent $1: cannot write $d/$1.md"
}

need_dir_mode() { # <path with {run}> <octal mode>: a pre-created directory, e.g. a refused log dir
  local p=${1//\{run\}/$LAB_RUN}
  mkdir -p "$p" && chmod "$2" "$p" || lab_need_fail "need_dir_mode $p $2: cannot create"
}

need_fixture_plugin() { # <fixture> <marketplace>: installs fixtures/<fixture> into {run}/config, zero tokens
  local src=$LAB_LAB/fixtures/$1 dst=$LAB_RUN/fixtures/$1 envl=() log=$LAB_RUN/fixture-$1.log
  mkdir -p "$LAB_RUN/fixtures" && cp -R "$src" "$dst" || { lab_need_fail "need_fixture_plugin $1: cannot copy $src"; return; }
  mapfile -t envl < <(lab_env_lines | grep -v '^CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR=')
  ( cd "$LAB_RUN/work" && /usr/bin/env -i "${envl[@]}" "$LAB_CLAUDE" plugin marketplace add "$dst" \
      && /usr/bin/env -i "${envl[@]}" "$LAB_CLAUDE" plugin install "$1@$2" ) > "$log" 2>&1 \
    || { lab_need_fail "need_fixture_plugin $1: claude plugin failed, see $log"; return; }
  # the install enables it in the user settings, which --setting-sources project never reads
  jq --arg id "$1@$2" '.enabledPlugins[$id] = true' "$LAB_RUN/settings.json" > "$LAB_RUN/settings.json.new" 2>> "$log" \
    && mv "$LAB_RUN/settings.json.new" "$LAB_RUN/settings.json" \
    || lab_need_fail "need_fixture_plugin $1: cannot enable $1@$2 in $LAB_RUN/settings.json, see $log"
}
