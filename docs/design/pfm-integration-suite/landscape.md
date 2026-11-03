# Capability landscape

## Contents

- [Capability rows](#capability-rows)

## Capability rows

Closed list of pfm's capabilities, from the built CLI tree (`pfm help` recursive), the installed engine launcher shims, the chat MCP `tools/list`, the hook registries (claudelaunch/hooks.go, installer/codex_hooks.go, workbench/opencode_herdr_plugin.mjs), the picker key map (ui/model.go) and the service units (installer/assets/systemd). `pfm internal *` verbs are internal entries and carry no row of their own. `op:` names the operation the activity log records once the build adds `op` (Law 17); today the log records `cmd` only.

```text
lifecycle.install · install a release over a machine with a live chat · surface:lifecycle · kind:success · entry:cli · op:lifecycle.install · ruled:no · needs:systemd-or-launchd · today:unit+live · pfm/internal/installer/herdr_shell.go:72

lifecycle.install-again · a second install changes nothing · surface:lifecycle · kind:refusal · entry:cli · op:lifecycle.install · ruled:no · needs:none · today:unit+live · pfm/internal/installer/live_chats.go:148

lifecycle.live-chat-upgrade · a chat started by the previous release runs the new hooks · surface:lifecycle · kind:success · entry:hook · op:hook.run · ruled:no · needs:none · today:unit · pfm/internal/hookentry/herdr_activity.go:281

lifecycle.uninstall · uninstall removes what pfm installed and keeps the owner's files · surface:lifecycle+data-lifecycle · kind:success · entry:cli · op:lifecycle.uninstall · ruled:yes · needs:none · today:unit+live · pfm/internal/installer/live_chats.go:148

lifecycle.uninstall-again · a second uninstall changes nothing · surface:lifecycle+data-lifecycle · kind:refusal · entry:cli · op:lifecycle.uninstall · ruled:yes · needs:none · today:unit+live · pfm/internal/installer/live_chats.go:148

lifecycle.restart · restart pfm's daemons · surface:operations · kind:success · entry:cli · op:lifecycle.restart · ruled:no · needs:none · today:live · pfm/internal/installer/assets/systemd/pfm-mcp.service:18

ops.doctor · doctor reports the installed machine's health · surface:operations · kind:success · entry:cli · op:ops.doctor · ruled:no · needs:none · today:unit+live · pfm/internal/doctor/herdr_shell.go:1

picker.open-bash · typing pfm from a bash login shell opens the picker · surface:terminal · kind:success · entry:typed-bash · op:picker.open · ruled:no · needs:none · today:live · pfm/cmd/pfm/main.go:156

picker.open-herdr-tab · a new Herdr tab opens the picker · surface:terminal · kind:success · entry:herdr-keybinding · op:picker.open · ruled:no · needs:none · today:NONE · pfm/internal/installer/assets/shim/pfm.zsh:112

picker.open-typed · typing pfm opens the picker · surface:terminal · kind:success · entry:typed · op:picker.open · ruled:no · needs:none · today:unit+live · pfm/cmd/pfm/main.go:158

picker.auto-open-off · a plain-shell entry keeps the picker closed; Herdr offers no such entry · surface:terminal · kind:refusal · entry:herdr-keybinding · op: · ruled:no · needs:none · today:NONE · pfm/internal/installer/assets/shim/pfm.zsh:112

picker.quit · quitting the picker leaves the shell · surface:terminal · kind:success · entry:picker-key · op:picker.quit · ruled:no · needs:none · today:unit · pfm/internal/ui/model.go:492

launcher.version · installed `claude --version` passes through · surface:launcher · kind:success · entry:shell · op:none · ruled:no · needs:none · today:unit+live · pfm/internal/hookentry/claude_launch.go:15 -> launch.go:128,169

chat.new-picker · start a chat from the picker's new-chat row · surface:chat · kind:success · entry:picker-key · op:chat.new · ruled:no · needs:none · today:unit · pfm/internal/ui/launchwizard.go:33

chat.new-typed · start a chat by typing the engine's command · surface:chat · kind:success · entry:typed · op:chat.new · ruled:no · needs:none · today:NONE · pfm/internal/installer/assets/shim/pfm.zsh:112

chat.new-cli · start a chat with pfm chat new · surface:chat · kind:success · entry:cli · op:chat.new · ruled:no · needs:none · today:unit+live · pfm/internal/chat/open.go:319

chat.new-mcp · a chat starts a child chat on its own backend · surface:chat · kind:success · entry:mcp · op:chat.new · ruled:no · needs:none · today:unit+live · pfm/internal/mcpserv/actions.go:129

herdr.place-from-foreign-workspace · a chat opened from another project's workspace lands in its own · surface:chat · kind:success · entry:typed · op:herdr.place · ruled:no · needs:none · today:unit · pfm/internal/spawn/herdr_place.go:145

chat.name-cli · rename a chat · surface:chat · kind:success · entry:cli · op:chat.name · ruled:no · needs:none · today:unit+live · pfm/internal/mcpserv/actions.go:371

chat.name-mcp · a chat renames itself · surface:chat · kind:success · entry:mcp · op:chat.name · ruled:no · needs:none · today:unit+live · pfm/internal/mcpserv/actions.go:371

chat.reload-typed · typed /reload reboots the chat in place · surface:chat · kind:success · entry:typed · op:chat.reload · ruled:no · needs:none · today:unit · pfm/internal/hookentry/reload_intercept.go:71

chat.reload-cli · pfm chat reload reboots the chat in place · surface:chat · kind:success · entry:cli · op:chat.reload · ruled:no · needs:none · today:unit+live · pfm/cmd/pfm/chat_reload_command.go:94

chat.reload-picker · the picker's reboot key reboots a live chat · surface:chat · kind:success · entry:picker-key · op:chat.reload · ruled:no · needs:none · today:unit · pfm/internal/picker/picker.go:627

chat.reload-account · reload a chat into another account · surface:chat+identity · kind:success · entry:cli · op:chat.account-switch · ruled:yes · needs:none · today:unit · pfm/cmd/pfm/chat_reload_command.go:94

chat.reload-account-refused · reload into an unknown account is refused · surface:chat+identity · kind:refusal · entry:cli · op:chat.reload · ruled:yes · needs:none · today:unit · pfm/cmd/pfm/chat_reload_command.go:94

status.turn · a turn shows working then idle · surface:status · kind:success · entry:hook · op:status.idle · ruled:no · needs:none · today:unit · pfm/internal/hookentry/herdr_activity.go:281

status.question-blocked · a question from the engine shows blocked · surface:status · kind:success · entry:hook · op:status.question · ruled:no · needs:none · today:unit · pfm/internal/hookentry/herdr_activity.go:281

status.answer-unblocks · answering lifts blocked · surface:status · kind:success · entry:typed · op:status.answer · ruled:no · needs:none · today:unit · pfm/internal/hookentry/herdr_activity.go:281

status.permission-blocked · a permission request shows blocked · surface:status · kind:success · entry:hook · op:status.permission · ruled:no · needs:none · today:unit · pfm/internal/hookentry/herdr_activity.go:281

status.permission-answered · allowing a permission lifts blocked · surface:status · kind:success · entry:typed · op:status.permission-answer · ruled:no · needs:none · today:unit · pfm/internal/hookentry/herdr_activity.go:281

status.subagent-hold · a running sub-agent holds working and its own blocked · surface:status · kind:success · entry:hook · op:status.subagent · ruled:no · needs:none · today:unit · pfm/internal/hookentry/herdr_hold.go:121

chat.call-mcp · a chat messages another chat · surface:chat · kind:success · entry:mcp · op:chat.call · ruled:no · needs:none · today:unit+live · pfm/internal/call/engine.go:499

chat.call-cli · a chat messages another with pfm chat call · surface:chat · kind:success · entry:cli · op:chat.call · ruled:no · needs:none · today:unit+live · pfm/internal/call/engine.go:499

chat.call-refused · a call to an unknown chat is refused · surface:chat · kind:refusal · entry:mcp · op:chat.call · ruled:no · needs:none · today:unit+live · pfm/internal/call/body.go:64

chat.keys-mcp · a chat sends keys to another · surface:chat · kind:success · entry:mcp · op:chat.keys · ruled:no · needs:none · today:unit+live · pfm/internal/mcpserv/actions.go:1

chat.capture-mcp · a chat reads another's pane · surface:chat · kind:success · entry:mcp · op:chat.capture · ruled:no · needs:none · today:unit+live · pfm/internal/mcpserv/actions.go:1

chat.ls-picker · the picker lists the fleet · surface:chat · kind:success · entry:typed · op:picker.open · ruled:no · needs:none · today:unit · pfm/internal/ui/model.go:1256

chat.ls-cli · pfm chat ls lists the fleet · surface:chat · kind:success · entry:cli · op:chat.ls · ruled:no · needs:none · today:unit+live · pfm/internal/mcpserv/types.go:20

chat.ls-mcp · a chat lists the fleet · surface:chat · kind:success · entry:mcp · op:chat.ls · ruled:no · needs:none · today:unit+live · pfm/internal/mcpserv/types.go:20

chat.whoami-cli · a chat asks who it is · surface:chat+identity · kind:success · entry:cli · op:chat.whoami · ruled:yes · needs:none · today:unit+live · pfm/internal/resolve/whoami.go:253

chat.whoami-mcp · a chat asks who it is over MCP · surface:chat+identity · kind:success · entry:mcp · op:chat.whoami · ruled:yes · needs:none · today:unit+live · pfm/internal/resolve/whoami.go:253

chat.status-cli · read a chat's status · surface:chat · kind:success · entry:cli · op:chat.status · ruled:no · needs:none · today:unit+live · pfm/internal/herdr/tab_status.go:70

chat.status-mcp · a chat reads another's status · surface:chat · kind:success · entry:mcp · op:chat.status · ruled:no · needs:none · today:unit+live · pfm/internal/herdr/tab_status.go:70

chat.last-mcp · a chat reads another's last answer · surface:chat · kind:success · entry:mcp · op:chat.last · ruled:no · needs:none · today:unit+live · pfm/internal/mcpserv/actions.go:1

report.transcript · show a chat's transcript · surface:report · kind:success · entry:cli · op:report.transcript · ruled:no · needs:none · today:unit+live · pfm/cmd/pfm/main.go:158

report.statusline · Claude's statusline shows account, model and cost · surface:report · kind:success · entry:integration · op:report.statusline · ruled:no · needs:none · today:unit+live · pfm/cmd/pfm/main.go:158

herdr.restore · a Herdr restart restores every chat · surface:chat · kind:success · entry:herdr-restore · op:chat.open · ruled:no · needs:none · today:unit · pfm/internal/hookentry/herdr_seat.go:316

tmux.server-stopped · a stopped tmux server leaves the picker usable · surface:chat · kind:refusal · entry:typed · op:picker.open · ruled:no · needs:none · today:unit · pfm/internal/gather/procfs.go:184

chat.exit-typed · typed /e ends the chat · surface:chat · kind:success · entry:typed · op:chat.exit-intercept · ruled:no · needs:none · today:unit · pfm/internal/hookentry/prompt_block.go:52

chat.exit-native · the engine's own exit ends the chat · surface:chat · kind:success · entry:typed · op:chat.exit-close · ruled:no · needs:none · today:unit · pfm/internal/hookentry/exit_close.go:51

chat.end-cli · pfm chat end ends a chat · surface:chat · kind:success · entry:cli · op:chat.end · ruled:no · needs:none · today:unit+live · pfm/internal/kill/manager.go:221

chat.exit-mcp · a chat ends itself over MCP · surface:chat · kind:success · entry:mcp · op:chat.exit · ruled:no · needs:none · today:unit+live · pfm/internal/kill/manager.go:221

chat.resume-picker · resume an ended chat from the picker · surface:chat · kind:success · entry:picker-key · op:chat.resume · ruled:no · needs:none · today:unit · pfm/internal/ui/launchwizard.go:302

chat.open-cli · resume an ended chat with pfm chat open · surface:chat · kind:success · entry:cli · op:chat.resume · ruled:no · needs:none · today:unit+live · pfm/cmd/pfm/chat_command.go:86

chat.kill-picker · hide a chat from the picker · surface:chat · kind:success · entry:picker-key · op:chat.kill · ruled:no · needs:none · today:unit · pfm/internal/ui/model.go:1097

chat.unkill-picker · unhide a chat from the picker · surface:chat · kind:success · entry:picker-key · op:chat.unkill · ruled:no · needs:none · today:unit · pfm/internal/ui/model.go:1097

chat.kill-cli · kill a chat · surface:chat · kind:success · entry:cli · op:chat.kill · ruled:no · needs:none · today:unit+live · pfm/internal/kill/finisher.go:160

chat.unkill-cli · unkill a chat · surface:chat · kind:success · entry:cli · op:chat.unkill · ruled:no · needs:none · today:unit+live · pfm/internal/kill/manager.go:221

chat.kill-self-refused · an OpenCode chat cannot kill itself · surface:chat · kind:refusal · entry:mcp · op:chat.kill · ruled:no · needs:none · today:unit+live · pfm/internal/kill/manager.go:237

chat.new-storm · eight chats opened at once each land whole · surface:chat · kind:success · entry:cli · op:chat.open · ruled:no · needs:none · today:unit · pfm/internal/spawn/backend.go:92

chat.clear · /clear starts a new conversation and hides the old · surface:chat · kind:success · entry:typed · op:chat.clear · ruled:no · needs:none · today:unit · pfm/internal/hookentry/clear_kill.go:92

chat.read-cli · pfm chat read · surface:chat · kind:success · entry:cli · op:chat.read-cli · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

chat.last-cli · pfm chat last · surface:chat · kind:success · entry:cli · op:chat.last-cli · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

chat.stream-cli · pfm chat stream · surface:chat · kind:success · entry:cli · op:chat.stream-cli · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

chat.ask-cli · pfm chat ask · surface:chat · kind:success · entry:cli · op:chat.ask-cli · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

chat.watch-cli · pfm chat watch · surface:chat · kind:success · entry:cli · op:chat.watch-cli · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

chat.capture-cli · pfm chat capture · surface:chat · kind:success · entry:cli · op:chat.capture-cli · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

chat.keys-cli · pfm chat keys · surface:chat · kind:success · entry:cli · op:chat.keys-cli · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

chat.recover-cli · pfm chat recover · surface:chat · kind:success · entry:cli · op:chat.recover-cli · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

chat.schedule-set · pfm chat schedule set · surface:chat · kind:success · entry:cli · op:chat.schedule-set · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

chat.schedule-ls · pfm chat schedule ls · surface:chat · kind:success · entry:cli · op:chat.schedule-ls · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

chat.schedule-rm · pfm chat schedule rm · surface:chat · kind:success · entry:cli · op:chat.schedule-rm · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

chat.save-cli · pfm chat save · surface:chat · kind:success · entry:cli · op:chat.save-cli · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

chat.save-mcp · mcp save · surface:chat · kind:success · entry:mcp · op:chat.save-mcp · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

chat.find-cli · pfm chat find · surface:chat · kind:success · entry:cli · op:chat.find-cli · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

chat.branch-cli · pfm chat branch · surface:chat · kind:success · entry:cli · op:chat.branch-cli · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

chat.history-cli · pfm chat history · surface:chat · kind:success · entry:cli · op:chat.history-cli · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

chat.modal-cli · pfm chat modal · surface:chat · kind:success · entry:cli · op:chat.modal-cli · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

chat.resolve-cli · pfm chat resolve · surface:chat · kind:success · entry:cli · op:chat.resolve-cli · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

chat.resolve-mcp · mcp resolve · surface:chat · kind:success · entry:mcp · op:chat.resolve-mcp · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

chat.open-mcp · mcp open · surface:chat · kind:success · entry:mcp · op:chat.open-mcp · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

chat.kill-mcp · mcp kill · surface:chat · kind:success · entry:mcp · op:chat.kill-mcp · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

chat.unkill-mcp · mcp unkill · surface:chat · kind:success · entry:mcp · op:chat.unkill-mcp · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

chat.new-account-cli · pfm chat new --account · surface:chat · kind:success · entry:cli · op:chat.new-account-cli · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

chat.reload-new · pfm chat reload --new · surface:chat · kind:success · entry:cli · op:chat.reload-new · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

chat.reload-then · pfm chat reload --then · surface:chat · kind:success · entry:cli · op:chat.reload-then · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

chat.reload-model · pfm chat reload --model · surface:chat · kind:success · entry:cli · op:chat.reload-model · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

report.tokens · pfm tokens · surface:report · kind:success · entry:cli · op:report.tokens · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

report.model-cost · pfm model-cost · surface:report · kind:success · entry:cli · op:report.model-cost · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

report.log · pfm log · surface:report · kind:success · entry:cli · op:report.log · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

report.index · pfm index · surface:report · kind:success · entry:cli · op:report.index · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

report.usage-hook · pfm usage-hook · surface:report · kind:success · entry:cli · op:report.usage-hook · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

headless.exec · pfm headless exec · surface:headless · kind:success · entry:cli · op:headless.exec · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

theme.use · pfm theme · surface:theme · kind:success · entry:cli · op:theme.use · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

config.init · pfm config init · surface:config · kind:success · entry:cli · op:config.init · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

config.show · pfm config show · surface:config · kind:success · entry:cli · op:config.show · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

config.validate · pfm config validate · surface:config · kind:success · entry:cli · op:config.validate · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

config.claude-account · pfm config claude --account · surface:config · kind:success · entry:cli · op:config.claude-account · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

ops.reap · pfm reap · surface:ops · kind:success · entry:cli · op:ops.reap · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

ops.archive · pfm archive · surface:ops · kind:success · entry:cli · op:ops.archive · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

ops.heal · pfm heal · surface:ops · kind:success · entry:cli · op:ops.heal · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

ops.name-sync · pfm name-sync · surface:ops · kind:success · entry:cli · op:ops.name-sync · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

lifecycle.generate · pfm generate · surface:lifecycle · kind:success · entry:cli · op:lifecycle.generate · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

lifecycle.update-check · pfm update check · surface:lifecycle · kind:success · entry:cli · op:lifecycle.update-check · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

lifecycle.update-adopt · pfm update adopt · surface:lifecycle · kind:success · entry:cli · op:lifecycle.update-adopt · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

lifecycle.update-pin · pfm update pin · surface:lifecycle · kind:success · entry:cli · op:lifecycle.update-pin · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

lifecycle.update-ignore · pfm update ignore · surface:lifecycle · kind:success · entry:cli · op:lifecycle.update-ignore · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

lifecycle.update-drop · pfm update drop · surface:lifecycle · kind:success · entry:cli · op:lifecycle.update-drop · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

lifecycle.update-accept · pfm update accept · surface:lifecycle · kind:success · entry:cli · op:lifecycle.update-accept · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

lifecycle.init · pfm init · surface:lifecycle · kind:success · entry:cli · op:lifecycle.init · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

lifecycle.upgrade-previous · previous release upgrade · surface:lifecycle · kind:success · entry:daemon · op:lifecycle.upgrade-previous · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

mcp.serve · pfm mcp serve · surface:mcp · kind:success · entry:cli · op:mcp.serve · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

mcp.ls · pfm mcp ls · surface:mcp · kind:success · entry:cli · op:mcp.ls · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

mcp.enable-disable · pfm mcp enable-disable · surface:mcp · kind:success · entry:cli · op:mcp.enable-disable · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

ops.version · pfm version · surface:ops · kind:success · entry:cli · op:ops.version · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

ops.help · pfm help · surface:ops · kind:success · entry:cli · op:ops.help · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

picker.tabs · picker tab shift+tab · surface:picker · kind:success · entry:picker-key · op:picker.tabs · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

picker.stats-sort · picker stats c m · surface:picker · kind:success · entry:picker-key · op:picker.stats-sort · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

picker.limits-scroll · picker limits scroll · surface:picker · kind:success · entry:picker-key · op:picker.limits-scroll · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

picker.cosmos · picker cosmos keys · surface:picker · kind:success · entry:picker-key · op:picker.cosmos · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

picker.omnibox · picker omnibox @ and / · surface:picker · kind:success · entry:picker-key · op:picker.omnibox · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

picker.query-edit · picker query edit · surface:picker · kind:success · entry:picker-key · op:picker.query-edit · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

picker.carousel-deactive · picker carousel deactive · surface:picker · kind:success · entry:picker-key · op:picker.carousel-deactive · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

schedule.fire · schedule timer every 5 min · surface:schedule · kind:success · entry:daemon · op:schedule.fire · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

daemon.update-check · update check child of the picker · surface:daemon · kind:success · entry:daemon · op:daemon.update-check · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

hook.git-guard · PreToolUse git-guard · surface:hook · kind:success · entry:hook · op:hook.git-guard · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

hook.store-guard · PreToolUse store-guard · surface:hook · kind:success · entry:hook · op:hook.store-guard · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

hook.pcm-guard · PreToolUse pcm-guard · surface:hook · kind:success · entry:hook · op:hook.pcm-guard · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

hook.engine-guard · Codex/OpenCode engine-guard · surface:hook · kind:success · entry:hook · op:hook.engine-guard · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

hook.epic-inject · UserPromptSubmit epic-inject · surface:hook · kind:success · entry:hook · op:hook.epic-inject · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

hook.launcher-repair · SessionStart launcher-repair · surface:hook · kind:success · entry:hook · op:hook.launcher-repair · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

hook.resume-unkill · SessionStart resume-unkill · surface:hook · kind:success · entry:hook · op:hook.resume-unkill · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

hook.rr-dir · SubagentStart rr-dir · surface:hook · kind:success · entry:hook · op:hook.rr-dir · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

report.statusline-large-transcript · statusline render over a 120 MB transcript · surface:report · kind:success · entry:daemon · op:report.statusline-large-transcript · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

report.statusline-subagent · statusline sub-agent row without a count field · surface:report · kind:success · entry:daemon · op:report.statusline-subagent · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

hook.launcher-fast-exit · launcher fast exit · surface:hook · kind:success · entry:daemon · op:hook.launcher-fast-exit · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

mcp.ls-removed-cwd · mcp ls from a removed folder · surface:mcp · kind:success · entry:mcp · op:mcp.ls-removed-cwd · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

herdr.seat-old-state · native start hook on an older state file · surface:herdr · kind:success · entry:daemon · op:herdr.seat-old-state · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

ops.opencode-mirror-roots · OpenCode mirror over several roots · surface:ops · kind:success · entry:hook · op:ops.opencode-mirror-roots · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

report.usage-hook-killed · usage hook killed under load · surface:report · kind:success · entry:daemon · op:report.usage-hook-killed · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

chat.whoami-reused-pid · whoami beside an ended seat with a reused pid · surface:chat · kind:success · entry:daemon · op:chat.whoami-reused-pid · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

ops.process-read-gone · a process exiting mid-read · surface:ops · kind:success · entry:daemon · op:ops.process-read-gone · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

picker.scan-enrichment-warning · picker scan with one failed enrichment · surface:picker · kind:success · entry:picker-key · op:picker.scan-enrichment-warning · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

report.model-cost-broken-table · pfm model-cost check on a broken table · surface:report · kind:success · entry:cli · op:report.model-cost-broken-table · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

chat.new-cancelled · a cancelled spawn · surface:chat · kind:success · entry:daemon · op:chat.new-cancelled · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

ops.process-walk-pid-zero · process walk from a dead pane · surface:ops · kind:success · entry:daemon · op:ops.process-walk-pid-zero · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

herdr.stale-shim-refused · a stale Herdr shim call · surface:herdr · kind:success · entry:daemon · op:herdr.stale-shim-refused · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

report.price-catalog-columns · price catalog column rename · surface:report · kind:success · entry:daemon · op:report.price-catalog-columns · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

hook.launcher-leading-comment · tmux launch below a leading comment · surface:hook · kind:success · entry:daemon · op:hook.launcher-leading-comment · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

chat.reload-intercept-error · reload intercept error position · surface:chat · kind:success · entry:daemon · op:chat.reload-intercept-error · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

ops.doctor-no-codex · pfm doctor on a no-Codex install · surface:ops · kind:success · entry:cli · op:ops.doctor-no-codex · ruled:no · needs:none · today:unit · pfm/cmd/pfm/main.go:158

tmux.server-stopped-new · a stopped tmux server leaves a typed chat start working · surface:chat · kind:refusal · entry:typed · op:chat.open · ruled:no · needs:none · today:unit · pfm/internal/spawn/socket.go:1

herdr.rename-inside-engine · a rename inside the engine reaches the Herdr tab · surface:chat · kind:success · entry:typed · op:chat.name · ruled:no · needs:none · today:unit · pfm/internal/workbench/opencode_herdr_plugin.mjs:13

herdr.rename-replaced-session · a rename after the session was replaced follows the seat · surface:chat · kind:success · entry:typed · op:herdr.seat-follow · ruled:no · needs:none · today:unit · pfm/internal/hookentry/herdr_seat.go:135

chat.reload-background-work · typed /reload while background work runs · surface:chat · kind:success · entry:typed · op:chat.reload-idle-wait · ruled:no · needs:none · today:unit · pfm/cmd/pfm/chat_reload_command.go:140

herdr.reload-retired-seat · a reload beside a retired seat on the same pane · surface:chat · kind:success · entry:typed · op:herdr.pane-owner · ruled:no · needs:none · today:unit · pfm/internal/reload/birth.go:183

chat.reload-continued · reload of an engine-continued chat stays one chat · surface:chat · kind:success · entry:cli · op:chat.resume-segment · ruled:no · needs:none · today:unit · pfm/internal/chat/open.go:319

status.dropped-report-recovers · a state report dropped under load still reaches Herdr · surface:status · kind:success · entry:hook · op:status.resend · ruled:no · needs:none · today:unit · pfm/internal/hookentry/herdr_activity.go:281

status.turn-error · a failed turn shows error · surface:status · kind:refusal · entry:hook · op:status.error · ruled:no · needs:none · today:unit · pfm/internal/hookentry/herdr_activity.go:281

status.notification-turn · a task-notification turn shows working then idle · surface:status · kind:success · entry:hook · op:status.notification · ruled:no · needs:none · today:unit · pfm/internal/hookentry/herdr_activity.go:281

herdr.indicators-worktree-removed · the indicator daemon survives its hook folder's removal · surface:status · kind:success · entry:daemon · op:herdr.indicators · ruled:no · needs:none · today:unit · pfm/internal/themesync/herdr_plugin.go:56

chat.call-dead-refused · a call to a dead chat answers dead · surface:chat · kind:refusal · entry:mcp · op:chat.call · ruled:no · needs:none · today:unit · pfm/internal/call/engine.go:499

herdr.ls-lost-seat · the listing shows a lost seat as lost · surface:chat · kind:success · entry:cli · op:chat.ls · ruled:no · needs:none · today:unit · pfm/internal/hookentry/herdr_seat.go:135

herdr.picker-refresh-dead-seat · the picker refresh does not re-probe a dead seat · surface:chat · kind:success · entry:picker-key · op:picker.open · ruled:no · needs:none · today:unit · pfm/internal/ui/model.go:1097

picker.deleted-cwd · the picker keeps a chat whose folder was deleted under its project · surface:chat · kind:success · entry:picker-key · op:picker.open · ruled:no · needs:none · today:unit · pfm/internal/compose/project_test.go:29

herdr.whoami-moved · a chat moved to another workspace still finds its pane · surface:chat+identity · kind:success · entry:herdr-keybinding · op:herdr.seat-rebind · ruled:yes · needs:none · today:unit · pfm/internal/resolve/herdr.go:32

chat.kill-lost-seat · ctrl+x hides a lost seat · surface:chat · kind:success · entry:picker-key · op:herdr.seat-clear · ruled:no · needs:none · today:unit · pfm/internal/kill/manager.go:237

chat.open-ssh · resume a chat over ssh · surface:chat · kind:success · entry:ssh · op:chat.resume · ruled:no · needs:none · today:unit · pfm/cmd/pfm/chat_command.go:86

ops.doctor-after-ends · doctor stays clean after every chat ended · surface:operations · kind:success · entry:cli · op:ops.doctor-crumbs · ruled:no · needs:none · today:unit · pfm/internal/doctor/herdr_shell.go:1

lifecycle.install-stats-link · engine-owned `/stats` input replaces the stats-cache link with fixture bytes · surface:lifecycle · kind:success · entry:engine-input · op: · ruled:no · needs:none · today:unit · pfm/internal/installer/live_chats.go:148

chat.reload-new-hide · pfm chat reload --new --hide · surface:chat+data-lifecycle · kind:success · entry:cli-new-hide · op:chat.reload-new · ruled:yes · needs:none · today:unit · pfm/cmd/pfm/chat_reload_command.go:273
```
