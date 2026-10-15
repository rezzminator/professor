#!/usr/bin/env bash
# lib/isolate.sh: env allowlist render (L5), settings render (L6), installed copy off (D10).
set -uo pipefail
# shellcheck source=../testlib.sh
source "$(dirname "$0")/../testlib.sh"
lab_lib run auth isolate verdict
LAB_RUN=$T/run; mkdir -p "$LAB_RUN"
LAB_JQ_DIR=/opt/jq/bin
LAB_AUTH_JSON='{"claudeAiOauth":{"accessToken":"lab-canary-7f3e","expiresAt":1,"scopes":["a","b"],"subscriptionType":"max","rateLimitTier":"t"}}'
LAB_ENV=(CLAUDE_CODE_AUTO_COMPACT_WINDOW=100000)
want='CLAUDE_CODE_AUTO_COMPACT_WINDOW CLAUDE_CODE_DISABLE_CLAUDE_MDS CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC CLAUDE_CODE_ENABLE_FUNCTION_HOOKS CLAUDE_CODE_MANAGED_SETTINGS_PATH CLAUDE_CODE_OAUTH_SCOPES CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR CLAUDE_CODE_RATE_LIMIT_TIER CLAUDE_CODE_SUBSCRIPTION_TYPE CLAUDE_CODE_TMPDIR CLAUDE_CONFIG_DIR DISABLE_AUTOUPDATER ENABLE_CLAUDEAI_MCP_SERVERS HOME LANG PATH PLUGIN_LOG_DIR TEMP TERM TMP TMPDIR XDG_CACHE_HOME XDG_CONFIG_HOME XDG_DATA_HOME XDG_STATE_HOME'
expect_eq 'L5 allowlist keys' "$(lab_env_keys | tr '\n' ' ' | sed 's/ $//')" "$want"
lines=$(lab_env_lines)
expect_has 'HOME in the run' "$lines" "HOME=$LAB_RUN/home"
expect_has 'config dir in the run' "$lines" "CLAUDE_CONFIG_DIR=$LAB_RUN/config"
expect_has 'managed path in the run' "$lines" "CLAUDE_CODE_MANAGED_SETTINGS_PATH=$LAB_RUN/managed"
expect_has 'plugin logs in the run' "$lines" "PLUGIN_LOG_DIR=$LAB_RUN/logs"
expect_has 'PATH ends with the jq dir' "$lines" 'PATH=/usr/bin:/bin:/usr/sbin:/sbin:/opt/jq/bin'
expect_lacks 'no token in the env' "$lines" lab-canary-7f3e

# L6: settings render
store=$T/store; mkdir -p "$store/plugins"
printf '{"version":2,"plugins":{"sub-agent-compact@sub-agent-compact":[],"sub-agent-compact@sub-agent-compact-dev":[],"cache-live-control@cache-live-control":[]}}' > "$store/plugins/installed_plugins.json"
mapfile -t ids < <(lab_installed_ids "$store" sub-agent-compact)
expect_eq 'live ids of the plugin under test' "${ids[*]}" 'sub-agent-compact@sub-agent-compact sub-agent-compact@sub-agent-compact-dev'
LAB_OPTIONS='{"forwardHandoffTool":true}'; LAB_ALLOW=(Read mcp__sub-agent-compact__look_back); LAB_DENY=(Bash Grep); LAB_LAB=$LAB_DIR
lab_settings_render sub-agent-compact "${ids[@]}"
s=$LAB_RUN/settings.json
expect_eq 'enabledPlugins off' "$(jq -c .enabledPlugins "$s")" '{"sub-agent-compact@sub-agent-compact":false,"sub-agent-compact@sub-agent-compact-dev":false}'
expect_eq 'options under name@inline' "$(jq -c '.pluginConfigs["sub-agent-compact@inline"].options.forwardHandoffTool' "$s")" true
expect_eq 'dontAsk' "$(jq -r .permissions.defaultMode "$s")" dontAsk
expect_eq 'allow list exactly the scenario tools' "$(jq -c .permissions.allow "$s")" '["Read","mcp__sub-agent-compact__look_back"]'
expect_eq 'deny list exactly the scenario denials' "$(jq -c .permissions.deny "$s")" '["Bash","Grep"]'
expect_eq 'six mark hooks' "$(jq -r '.hooks | to_entries[] | "\(.key)=\(.value[0].hooks[0].command)"' "$s" | sed "s|$LAB_DIR|LAB|g; s|$LAB_RUN|RUN|g" | sort | tr '\n' ' ')" \
  "PreCompact=LAB_MARKS='RUN/marks.jsonl' 'LAB/hooks/mark.sh' PreCompact SessionEnd=LAB_MARKS='RUN/marks.jsonl' 'LAB/hooks/mark.sh' SessionEnd SessionStart=LAB_MARKS='RUN/marks.jsonl' 'LAB/hooks/mark.sh' SessionStart Stop=LAB_MARKS='RUN/marks.jsonl' 'LAB/hooks/mark.sh' Stop SubagentStop=LAB_MARKS='RUN/marks.jsonl' 'LAB/hooks/mark.sh' SubagentStop UserPromptSubmit=LAB_MARKS='RUN/marks.jsonl' 'LAB/hooks/mark.sh' UserPromptSubmit "
# a run or lab path with a space still runs the mark hook with its marks file
mkdir -p "$T/lab x/hooks" "$T/run y"; printf '#!/bin/sh\nprintf "%%s|%%s\\n" "$LAB_MARKS" "$1"\n' > "$T/lab x/hooks/mark.sh"; chmod +x "$T/lab x/hooks/mark.sh"
(LAB_RUN="$T/run y" LAB_LAB="$T/lab x"; lab_settings_render sub-agent-compact "${ids[@]}"
  expect_eq 'a spaced path keeps the mark hook whole' "$(sh -c "$(jq -r '.hooks.SessionStart[0].hooks[0].command' "$LAB_RUN/settings.json")" 2>&1)" "$T/run y/marks.jsonl|SessionStart")
lab_managed_render
expect_eq 'managed default' "$(jq -c . "$LAB_RUN/managed/managed-settings.json")" '{"prependPlugins":[]}'
lab_managed_render '{"cleanupPeriodDays":36500}'
expect_eq 'managed override' "$(jq -c . "$LAB_RUN/managed/managed-settings.json")" '{"cleanupPeriodDays":36500}'
lab_seed_config
c=$LAB_RUN/config/.claude.json
expect_eq 'onboarding done' "$(jq -r .hasCompletedOnboarding "$c")" true
expect_eq 'work dir trusted' "$(jq -r --arg w "$LAB_RUN/work" '.projects[$w].hasTrustDialogAccepted' "$c")" true
LAB_CLAUDE=/opt/claude/bin/claude LAB_PLUGIN_DIR=$T/p LAB_MODEL=haiku
lab_launch_render
expect_eq 'launcher is executable' "$([[ -x $LAB_RUN/launch.sh ]] && echo yes)" yes
la=$(cat "$LAB_RUN/launch.sh")
expect_has 'launcher isolates with env -i' "$la" 'env -i'
expect_has 'launcher opens fd 3 from the fifo' "$la" "exec 3<$LAB_RUN/sec/oauth.fifo"
expect_has 'launcher flags' "$la" "--setting-sources project --settings $LAB_RUN/settings.json --plugin-dir $T/p --model haiku --debug-file $LAB_RUN/debug.txt"
expect_lacks 'no token in the launcher' "$la" lab-canary-7f3e
# scenario preludes: a project agent, a directory mode, a fixture plugin installed with zero tokens
LAB_SCENARIO=sc LAB_ROWS=(R7e) LAB_LAB=$LAB_DIR LAB_CLAUDE=$FAKES/claude; : > "$LAB_RUN/checks.tsv"; mkdir -p "$LAB_RUN/work"
need_agent lab-echo haiku 'Read' '{nudgeFrom: 15k, forceAt: 40k}'
expect_eq 'agent file, the autoCompact map nested (the plugin reads no flow map)' "$(cat "$LAB_RUN/work/.claude/agents/lab-echo.md")" "$(printf -- '---\nname: lab-echo\ndescription: plugin-lab agent lab-echo\nmodel: haiku\ntools: Read\nautoCompact:\n  nudgeFrom: 15k\n  forceAt: 40k\n---\n\nFollow the task you are given exactly, using only your tools. Reply briefly.')"
need_dir_mode '{run}/logs-ro' 500
expect_eq 'directory mode' "$(cd "$LAB_RUN/logs-ro" && find . -maxdepth 0 -perm 500 | wc -l | tr -d ' ')" 1
LAB_ENV=()
need_fixture_plugin lab-agents lab-fixtures; rc=$?
expect_eq 'fixture installed' "$rc $(cat "$LAB_RUN/fake/plugin-argv" 2>&1)" "0 $(printf 'plugin marketplace add %s\nplugin install lab-agents@lab-fixtures' "$LAB_RUN/fixtures/lab-agents")"
expect_eq 'fixture enabled in the run settings, since the user settings the install wrote are never read' "$(jq -c '.enabledPlugins["lab-agents@lab-fixtures"]' "$s")" true
expect_eq 'fixture copied' "$(cd "$LAB_RUN/fixtures/lab-agents" && find . -type f | LC_ALL=C sort | tr '\n' ' ')" './.claude-plugin/marketplace.json ./plugins/lab-agents/.claude-plugin/plugin.json ./plugins/lab-agents/agents/reader-forward.md '
rm -rf "$LAB_RUN/fixtures"
LAB_ENV=(FAKE_PLUGIN_RC=1)
need_fixture_plugin lab-agents lab-fixtures >/dev/null; rc=$?
LAB_ENV=()
expect_eq 'fixture install failure is a setup ERROR' "$rc $(cat "$LAB_RUN/checks.tsv")" "2 $(printf 'sc/setup\tR7e\tERROR\tneed_fixture_plugin lab-agents: claude plugin failed, see %s' "$LAB_RUN/fixture-lab-agents.log")"
finish
