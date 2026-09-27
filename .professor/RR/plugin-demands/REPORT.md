# Demanded Claude Code plugins

**Date:** 2026-09-27

## Method

The corpus contains 57,950 anthropics/claude-code issues harvested 2026-09-26: 12,541 open and 45,409 closed as `not_planned`. Batch agents judged 57,946 rows (four lost to TSV quote parsing) and reported 15,652 kept in 1,062 area-batch clusters. This merge reads the headerless TSV with `csv.QUOTE_NONE`, groups clusters by one publishable plugin capability, and filters to requests solvable or workable through the plugin-builder layer table. Demand is 👍 + 0.25 × (reactions − 👍) + 0.5 × comments + issues, using distinct issue numbers and fresh TSV counts.

## Coverage and source reconciliation

| Measure | Count |
| --- | ---: |
| Rows judged, batch agents reported | 57,946 |
| Rows kept, batch agents reported | 15,652 |
| Batch clusters, batch agents reported | 1,062 |
| Cluster entries present in the 26 JSON arrays | 1,083 |
| Distinct issue IDs present in those arrays | 15,279 |
| Distinct issue IDs ranked after spot check | 15,272 |
| Canonical plugins | 615 |

The batch summary and current JSON arrays disagree by 21 cluster entries and 373 kept issue IDs. The ranking covers exactly the current JSON memberships, with 33 spot-checked issues moved to another capability and 7 excluded as misfits; it does not infer missing memberships.

## Top 25

### 1. Permission policy controller

**Demand:** 4,123.00 · **Issues/open:** 639/90 · **👍:** 1,925 · **Comments:** 3,075 · **Layer:** `settings-hook` · **Feasibility:** workaround

**Ask:** Make equivalent shell commands, paths, and tools receive consistent permission decisions.

**Plugin:** A PreToolUse and PermissionRequest settings hook parses commands and resolved paths, then applies named allow, ask, and deny rules. A companion command shows which rule made each decision.

**Top issues by reactions:** [#28240 [BUG] Permission prompt incorrectly triggers on cd instead of the actual command in compound bash statements](https://github.com/anthropics/claude-code/issues/28240); [#30519 Permissions matching is fundamentally broken — 30+ open issues, no staff engagement, community building workarounds](https://github.com/anthropics/claude-code/issues/30519); [#27957 Add option to disable 'Command contains quoted characters in flag names' warning](https://github.com/anthropics/claude-code/issues/27957)

### 2. Keyboard and input controls

**Demand:** 1,854.75 · **Issues/open:** 363/128 · **👍:** 940 · **Comments:** 1,081 · **Layer:** `function-hooks` · **Feasibility:** workaround

**Ask:** Edit and submit multiline prompts with predictable keyboard behavior.

**Plugin:** Experimental function hooks intercept prompt editing and UI input events to implement configurable shortcuts, selection, and draft recovery. They keep bindings consistent across supported terminals.

**Top issues by reactions:** [#32726 VSCode extension: add option to prevent panel from stealing focus](https://github.com/anthropics/claude-code/issues/32726); [#14027 [FEATURE] Leave Claude with single Ctrl+D](https://github.com/anthropics/claude-code/issues/14027); [#729 Suggestion: Don't auto-submit on `Enter`, makes it difficult to write multiple lines](https://github.com/anthropics/claude-code/issues/729)

### 3. IDE connector bridge

**Demand:** 1,818.50 · **Issues/open:** 67/15 · **👍:** 1,410 · **Comments:** 535 · **Layer:** `mixed` · **Feasibility:** workaround

**Ask:** Connect Claude Code to more editors with the correct workspace and launch environment.

**Plugin:** An ACP or editor bridge exposes workspace, selection, and diagnostics through MCP tools. A bin launcher carries the editor-selected executable, shell, cwd, and environment into each session.

**Top issues by reactions:** [#15942 Add support for Visual Studio 2026 Integration](https://github.com/anthropics/claude-code/issues/15942); [#6686 Feature Request: Add support for Agent Client Protocol (ACP)](https://github.com/anthropics/claude-code/issues/6686); [#1234 Support for other IDEs (Neovim/Emacs)](https://github.com/anthropics/claude-code/issues/1234)

### 4. Usage and quota dashboard

**Demand:** 1,786.25 · **Issues/open:** 340/54 · **👍:** 838 · **Comments:** 1,191 · **Layer:** `mixed` · **Feasibility:** workaround

**Ask:** Show subscription quota, token use, cost, and reset times across sessions.

**Plugin:** A statusLine command reads local usage records and displays per-session totals. An MCP or authenticated adapter adds account quota when available and labels missing data rather than guessing.

**Top issues by reactions:** [#13585 Add Quota Information Access to Claude Code CLI](https://github.com/anthropics/claude-code/issues/13585); [#5621 StatusLine should expose API usage/quota information in JSON input](https://github.com/anthropics/claude-code/issues/5621); [#21943 Feature Request: Expose subscription usage data via local file or API](https://github.com/anthropics/claude-code/issues/21943)

### 5. Voice companion

**Demand:** 1,719.25 · **Issues/open:** 22/2 · **👍:** 1,259 · **Comments:** 373 · **Layer:** `function-hooks` · **Feasibility:** workaround

**Ask:** Restore an optional on-screen companion with spoken responses.

**Plugin:** An experimental function hook renders the companion and audio controls in the Claude Code UI. A settings hook sends completed replies to a configurable text-to-speech executable.

**Top issues by reactions:** [#45596 Bring Back Buddy — A Consolidated Plea from the Community](https://github.com/anthropics/claude-code/issues/45596); [#45732 Bring Back /buddy: 511 Reasons Why](https://github.com/anthropics/claude-code/issues/45732); [#45612 Feature Request: Make /buddy a permanent opt-in feature](https://github.com/anthropics/claude-code/issues/45612)

### 6. Project instruction guard

**Demand:** 1,677.25 · **Issues/open:** 305/44 · **👍:** 628 · **Comments:** 1,467 · **Layer:** `mixed` · **Feasibility:** workaround

**Ask:** Load project instructions reliably and check required steps before completion.

**Plugin:** A SessionStart hook loads applicable project rules, and PreToolUse and Stop settings hooks check explicit workflow requirements. A skill presents the active requirements in a short, inspectable checklist.

**Top issues by reactions:** [#2571 [BUG] CLAUDE.md files in subdirectories are not being automatically loaded](https://github.com/anthropics/claude-code/issues/2571); [#26489 [FEATURE] skills/, agents/, commands/ should traverse parent directories like CLAUDE.md does](https://github.com/anthropics/claude-code/issues/26489); [#2544 [BUG] CLAUDE.md Mandatory Rules Consistently Ignored Across Multiple Repositories](https://github.com/anthropics/claude-code/issues/2544)

### 7. Worktree lifecycle manager

**Demand:** 1,560.00 · **Issues/open:** 308/66 · **👍:** 696 · **Comments:** 1,100 · **Layer:** `mixed` · **Feasibility:** workaround

**Ask:** Create, assign, inspect, and clean up isolated worktrees for parallel sessions and agents.

**Plugin:** A command and bin executable create named worktrees from a selected base, record ownership, and hand changes back. Settings hooks guard writes and cleanup against the active worktree boundary.

**Top issues by reactions:** [#23622 [Feature Request] Support selecting base branch when creating git worktree](https://github.com/anthropics/claude-code/issues/23622); [#5768 [BUG] Resuming sessions only works from the directory in which they were started](https://github.com/anthropics/claude-code/issues/5768); [#34437 Worktrees should share the same project directory as the main repo](https://github.com/anthropics/claude-code/issues/34437)

### 8. Theme and layout controls

**Demand:** 1,434.25 · **Issues/open:** 319/45 · **👍:** 619 · **Comments:** 941 · **Layer:** `function-hooks` · **Feasibility:** workaround

**Ask:** Customize Claude Code colors, density, and native UI layout.

**Plugin:** A theme file handles supported color changes. Experimental function hooks adjust native chrome and view layout where themes cannot.

**Top issues by reactions:** [#34196 VSCode extension: add font size setting for chat panel](https://github.com/anthropics/claude-code/issues/34196); [#18570 [FEATURE] Bring back ultrathink rainbow glow as easter egg](https://github.com/anthropics/claude-code/issues/18570); [#6038 Add option to disable thinking animation shimmer effect + show token usage](https://github.com/anthropics/claude-code/issues/6038)

### 9. MCP authentication bridge

**Demand:** 1,391.75 · **Issues/open:** 142/37 · **👍:** 797 · **Comments:** 813 · **Layer:** `mcp` · **Feasibility:** workaround

**Ask:** Connect multiple accounts and recover failed authentication for MCP services.

**Plugin:** An MCP proxy or companion executable completes OAuth or token flows, refreshes credentials, and presents stable authenticated tools. A diagnostic command shows the connected account and renewal failure.

**Top issues by reactions:** [#27302 [FEATURE] Support multiple Connector accounts (same connector, different accounts) in Claude and Claude Code on the web (claude.ai/code)](https://github.com/anthropics/claude-code/issues/27302); [#3433 Claude Code cannot connect to GitHub's remote MCP server using OAuth authentication](https://github.com/anthropics/claude-code/issues/3433); [#65036 [BUG] MCP OAuth: Claude doesn't auto-refresh access tokens, daily "Connection expired" despite valid refresh token](https://github.com/anthropics/claude-code/issues/65036)

### 10. Output and language style

**Demand:** 1,256.75 · **Issues/open:** 134/32 · **👍:** 767 · **Comments:** 636 · **Layer:** `mixed` · **Feasibility:** workaround

**Ask:** Keep replies in the requested language, tone, and format.

**Plugin:** A namespaced output style gives the model the requested writing rules. A final-response function hook checks obvious drift and can request a corrected reply.

**Top issues by reactions:** [#77136 [BUG] Claude 4.7, 4.8, 5.0, and Fable increasingly default to repetitive rhetorical tics and often struggle to produce coherent prose despite explicit style instructions](https://github.com/anthropics/claude-code/issues/77136); [#13378 2-space indent and hard wrap at 80 breaks copy-paste - Need a way to configure it out of the way](https://github.com/anthropics/claude-code/issues/13378); [#1599 [BUG] Claude clobbers correct typographic marks and can't be told otherwise](https://github.com/anthropics/claude-code/issues/1599)

### 11. Session archive and search

**Demand:** 1,190.25 · **Issues/open:** 169/63 · **👍:** 615 · **Comments:** 806 · **Layer:** `mixed` · **Feasibility:** workaround

**Ask:** Find, organize, archive, and recover conversations across projects.

**Plugin:** A bin indexer reads local transcript records and builds a searchable catalog. A command exposes labels, pins, archive actions, and recovery snapshots.

**Top issues by reactions:** [#26904 [FEATURE] Add /delete command to delete current session](https://github.com/anthropics/claude-code/issues/26904); [#27242 [BUG] No working mechanism to review previous context after compaction, plan-mode clear, or branch navigation — data preserved but UI inaccessible](https://github.com/anthropics/claude-code/issues/27242); [#9258 [BUG] History Sessions lost in Vscode plugin](https://github.com/anthropics/claude-code/issues/9258)

### 12. Precise and safe editing

**Demand:** 1,110.75 · **Issues/open:** 193/47 · **👍:** 450 · **Comments:** 934 · **Layer:** `mcp` · **Feasibility:** workaround

**Ask:** Apply precise edits while preserving encoding, line endings, and recoverability.

**Plugin:** A file-edit MCP server checks the expected bytes, stages an atomic replacement, and verifies the written result. It keeps a recoverable copy for conflicting or failed edits.

**Top issues by reactions:** [#11447 [BUG] Claude can't edit files that use tabs for indentation](https://github.com/anthropics/claude-code/issues/11447); [#2805 [BUG] Claude Code consistently creates files with Windows line endings on Linux systems](https://github.com/anthropics/claude-code/issues/2805); [#3471 [BUG] Too many edit file errors](https://github.com/anthropics/claude-code/issues/3471)

### 13. Output layout and formatting

**Demand:** 1,062.50 · **Issues/open:** 185/90 · **👍:** 650 · **Comments:** 444 · **Layer:** `function-hooks` · **Feasibility:** workaround

**Ask:** Control the display density of tool calls, diffs, thinking, and transcript text.

**Plugin:** Experimental ui.render function hooks collapse or expand transcript blocks and preserve per-view preferences. A command lets users switch display profiles.

**Top issues by reactions:** [#37951 Option to hide inline diffs for Edit/Write tool output](https://github.com/anthropics/claude-code/issues/37951); [#43113 [FEATURE] Add a flag that tells Claude Code to emit long lines for prose/markdown content and let the terminal emulator handle word wrapping, rather than inserting hard newline characters at word boundaries](https://github.com/anthropics/claude-code/issues/43113); [#13600 [FEATURE][CLI] Markdown renderer support in Claude Code CLI](https://github.com/anthropics/claude-code/issues/13600)

### 14. Model and effort router

**Demand:** 1,057.00 · **Issues/open:** 208/39 · **👍:** 504 · **Comments:** 676 · **Layer:** `function-hooks` · **Feasibility:** workaround

**Ask:** Select the model and effort level by task, budget, and policy.

**Plugin:** Experimental function hooks inspect requests before model selection and apply a configurable routing policy. A command shows the chosen model, effort, and rule for the current turn.

**Top issues by reactions:** [#43326 Feature: Auto-select model and effort level based on task complexity](https://github.com/anthropics/claude-code/issues/43326); [#22218 [FEATURE] Add Support for opusplan Model in VSCode Extension](https://github.com/anthropics/claude-code/issues/22218); [#38698 Feature: Per-agent model provider routing (e.g. local Ollama for subagents, Anthropic for orchestrator)](https://github.com/anthropics/claude-code/issues/38698)

### 15. Session naming and titles

**Demand:** 997.25 · **Issues/open:** 199/83 · **👍:** 534 · **Comments:** 513 · **Layer:** `mixed` · **Feasibility:** workaround

**Ask:** Set stable session names and display them across supported surfaces.

**Plugin:** A command records explicit names and a SessionStart hook restores them. An experimental UI hook can display the stored title where the native surface ignores it.

**Top issues by reactions:** [#25045 [FEATURE] allow skills to programmatically rename sessions](https://github.com/anthropics/claude-code/issues/25045); [#15762 [FEATURE] Smart Session Rename](https://github.com/anthropics/claude-code/issues/15762); [#33165 Allow Claude to rename its own session (programmatic session rename API)](https://github.com/anthropics/claude-code/issues/33165)

### 16. Code review assistant

**Demand:** 966.00 · **Issues/open:** 85/24 · **👍:** 646 · **Comments:** 416 · **Layer:** `mixed` · **Feasibility:** workaround

**Ask:** Review a whole task diff and decide which changes may land.

**Plugin:** A review skill gathers the full diff and relevant tests, then records per-file findings. A command opens an external diff viewer and applies approved changes through a checked executable.

**Top issues by reactions:** [#33932 [FEATURE] VS Code Extension: Diff review UI similar to GitHub Copilot Edits Review](https://github.com/anthropics/claude-code/issues/33932); [#8660 [BUG] Edit preview/diff not showing in VSCode extension UI when confirming changes](https://github.com/anthropics/claude-code/issues/8660); [#29388 [FEATURE] VS Code Extension: Option to always show file diffs in VS Code editor panel instead of inline chat](https://github.com/anthropics/claude-code/issues/29388)

### 17. Reliable shell runner

**Demand:** 961.75 · **Issues/open:** 218/69 · **👍:** 388 · **Comments:** 690 · **Layer:** `mcp` · **Feasibility:** workaround

**Ask:** Run shell commands with explicit cwd, quoting, stdin, timeout, and exit status.

**Plugin:** An MCP shell server executes commands with declared environment and captures output and status reliably. A bin helper handles platform-specific shells where Claude Code can invoke it.

**Top issues by reactions:** [#7490 [FEATURE] Allow users to configure which shell the Bash tool uses or inherit initial shell](https://github.com/anthropics/claude-code/issues/7490); [#9881 [FEATURE] Add Interactive Shell Support to the Bash Tool via Pseudo-Terminal (PTY)](https://github.com/anthropics/claude-code/issues/9881); [#1127 [feature request] Stream output of script execution](https://github.com/anthropics/claude-code/issues/1127)

### 18. Compaction policy controller

**Demand:** 933.00 · **Issues/open:** 213/37 · **👍:** 295 · **Comments:** 841 · **Layer:** `function-hooks` · **Feasibility:** workaround

**Ask:** Choose what context compaction preserves, prunes, or delays.

**Plugin:** Experimental session.measure and session.compact function hooks select content or veto a native compaction attempt. The plugin cannot trigger compaction earlier than Claude Code does.

**Top issues by reactions:** [#6390 Feature Request: Add Context Pruning as Alternative to Compacting](https://github.com/anthropics/claude-code/issues/6390); [#24201 [FEATURE] Ask questions for compaction](https://github.com/anthropics/claude-code/issues/24201); [#3351 [Feature Request] Configurable autocompact context %](https://github.com/anthropics/claude-code/issues/3351)

### 19. Session handoff

**Demand:** 931.50 · **Issues/open:** 113/26 · **👍:** 540 · **Comments:** 538 · **Layer:** `mixed` · **Feasibility:** workaround

**Ask:** Carry decisions, task state, and critical rules into a fresh session.

**Plugin:** Session and compaction settings hooks write a compact, user-owned checkpoint. A handoff skill validates the target session and loads that checkpoint before work resumes.

**Top issues by reactions:** [#13354 [FEATURE] Continue when the session limit reached](https://github.com/anthropics/claude-code/issues/13354); [#28791 [FEATURE] Sync conversation history between CLI and Claude Code desktop app](https://github.com/anthropics/claude-code/issues/28791); [#15881 [Feature Request] Seamless session sharing between Claude Code and Claude Desktop](https://github.com/anthropics/claude-code/issues/15881)

### 20. Session checkpoint and recovery

**Demand:** 913.75 · **Issues/open:** 159/45 · **👍:** 358 · **Comments:** 768 · **Layer:** `mixed` · **Feasibility:** workaround

**Ask:** Preserve useful task state through compaction, interruption, and restart.

**Plugin:** PreCompact and SessionEnd settings hooks save structured checkpoints to local storage. A SessionStart hook and resume skill restore the selected checkpoint.

**Top issues by reactions:** [#17428 [Feature Request] Enhanced /compact with file-backed summaries and selective restoration](https://github.com/anthropics/claude-code/issues/17428); [#7502 [Bug] Auto-Compact Erases Entire Chat History Without Warning](https://github.com/anthropics/claude-code/issues/7502); [#11455 Feature Request: Session Handoff / Continuity Support](https://github.com/anthropics/claude-code/issues/11455)

### 21. Statusline and progress

**Demand:** 907.00 · **Issues/open:** 247/53 · **👍:** 333 · **Comments:** 642 · **Layer:** `mixed` · **Feasibility:** workaround

**Ask:** Keep session state, context, and progress visible in a configurable status line.

**Plugin:** A plugin statusLine command reads session metadata and renders chosen widgets. An experimental UI hook can change placement or width beyond native statusLine support.

**Top issues by reactions:** [#16078 Feature Request: Show active skill in status line](https://github.com/anthropics/claude-code/issues/16078); [#93667 Add setting to keep IDE selection indicator in footer instead of inline prompt](https://github.com/anthropics/claude-code/issues/93667); [#21894 Feature: Visual prompt state indicators (working/waiting/done/error)](https://github.com/anthropics/claude-code/issues/21894)

### 22. Clipboard and clean copy

**Demand:** 895.00 · **Issues/open:** 70/28 · **👍:** 614 · **Comments:** 406 · **Layer:** `mixed` · **Feasibility:** workaround

**Ask:** Copy clean transcript text across terminals and operating systems.

**Plugin:** A bin executable writes exact text to the platform clipboard or OSC 52. A UI hook selects complete transcript blocks and avoids duplicate copies.

**Top issues by reactions:** [#18170 Copy/paste from terminal includes unwanted indentation and trailing spaces](https://github.com/anthropics/claude-code/issues/18170); [#23134 Feature Request: Option to disable paste text collapse in input field](https://github.com/anthropics/claude-code/issues/23134); [#62699 [BUG] Text cannot be copied from Claude Code's output using `Ctrl+Shift+C` or right-click context menu.](https://github.com/anthropics/claude-code/issues/62699)

### 23. Notifications and alerts

**Demand:** 888.75 · **Issues/open:** 185/46 · **👍:** 352 · **Comments:** 694 · **Layer:** `mixed` · **Feasibility:** workaround

**Ask:** Alert users when work finishes or needs input.

**Plugin:** Stop and Notification settings hooks route events to desktop, terminal, or webhook targets. A monitor coalesces repeat events and keeps background work visible.

**Top issues by reactions:** [#57230 [VSCode Extension] Add native system/toast notifications when Claude needs attention](https://github.com/anthropics/claude-code/issues/57230); [#13922 [FEATURE] Configurable timeout for idle_prompt notification hook](https://github.com/anthropics/claude-code/issues/13922); [#29928 Feature request: built-in completion notifications for VS Code extension](https://github.com/anthropics/claude-code/issues/29928)

### 24. Destructive action guard

**Demand:** 877.75 · **Issues/open:** 276/59 · **👍:** 90 · **Comments:** 1,004 · **Layer:** `settings-hook` · **Feasibility:** workaround

**Ask:** Require explicit approval before destructive file, shell, database, or cloud operations.

**Plugin:** A PreToolUse settings hook parses commands and tool arguments, checks the target and backup state, then denies or requests permission. A skill explains the intended operation in the approval prompt.

**Top issues by reactions:** [#6608 [BUG] Claude code ran `rm -rf` command without permission](https://github.com/anthropics/claude-code/issues/6608); [#15863 KRITISCH - ZWEITER KATASTROPHALER DATENVERLUST in 4 Tagen - 1 Monat Arbeit PERMANENT ZERSTÖRT - Zahlender Kunde fordert SOFORTIGE Maßnahmen](https://github.com/anthropics/claude-code/issues/15863); [#6413 [BUG] Claude Calls `rm` command without having permission to do so](https://github.com/anthropics/claude-code/issues/6413)

### 25. Plan workflow

**Demand:** 850.00 · **Issues/open:** 110/17 · **👍:** 488 · **Comments:** 479 · **Layer:** `mixed` · **Feasibility:** workaround

**Ask:** Create named plans, review them, and gate execution on the accepted plan.

**Plugin:** A command writes a project-scoped plan and tracks review state. Settings hooks check that implementation follows the current approved plan.

**Top issues by reactions:** [#12619 [FEATURE] Allow setting plan naming scheme per-repo](https://github.com/anthropics/claude-code/issues/12619); [#14259 [FEATURE] PrePlanMode and PostPlanMode Hook Events](https://github.com/anthropics/claude-code/issues/14259); [#14866 Feature Request: Configurable Plan File Storage Path & Plan Templates](https://github.com/anthropics/claude-code/issues/14866)

## Full ranking

| Rank | Plugin | Demand | Issues | 👍 | Layer | Feasibility |
| ---: | --- | ---: | ---: | ---: | --- | --- |
| 1 | Permission policy controller | 4,123.00 | 639 | 1,925 | `settings-hook` | workaround |
| 2 | Keyboard and input controls | 1,854.75 | 363 | 940 | `function-hooks` | workaround |
| 3 | IDE connector bridge | 1,818.50 | 67 | 1,410 | `mixed` | workaround |
| 4 | Usage and quota dashboard | 1,786.25 | 340 | 838 | `mixed` | workaround |
| 5 | Voice companion | 1,719.25 | 22 | 1,259 | `function-hooks` | workaround |
| 6 | Project instruction guard | 1,677.25 | 305 | 628 | `mixed` | workaround |
| 7 | Worktree lifecycle manager | 1,560.00 | 308 | 696 | `mixed` | workaround |
| 8 | Theme and layout controls | 1,434.25 | 319 | 619 | `function-hooks` | workaround |
| 9 | MCP authentication bridge | 1,391.75 | 142 | 797 | `mcp` | workaround |
| 10 | Output and language style | 1,256.75 | 134 | 767 | `mixed` | workaround |
| 11 | Session archive and search | 1,190.25 | 169 | 615 | `mixed` | workaround |
| 12 | Precise and safe editing | 1,110.75 | 193 | 450 | `mcp` | workaround |
| 13 | Output layout and formatting | 1,062.50 | 185 | 650 | `function-hooks` | workaround |
| 14 | Model and effort router | 1,057.00 | 208 | 504 | `function-hooks` | workaround |
| 15 | Session naming and titles | 997.25 | 199 | 534 | `mixed` | workaround |
| 16 | Code review assistant | 966.00 | 85 | 646 | `mixed` | workaround |
| 17 | Reliable shell runner | 961.75 | 218 | 388 | `mcp` | workaround |
| 18 | Compaction policy controller | 933.00 | 213 | 295 | `function-hooks` | workaround |
| 19 | Session handoff | 931.50 | 113 | 540 | `mixed` | workaround |
| 20 | Session checkpoint and recovery | 913.75 | 159 | 358 | `mixed` | workaround |
| 21 | Statusline and progress | 907.00 | 247 | 333 | `mixed` | workaround |
| 22 | Clipboard and clean copy | 895.00 | 70 | 614 | `mixed` | workaround |
| 23 | Notifications and alerts | 888.75 | 185 | 352 | `mixed` | workaround |
| 24 | Destructive action guard | 877.75 | 276 | 90 | `settings-hook` | workaround |
| 25 | Plan workflow | 850.00 | 110 | 488 | `mixed` | workaround |
| 26 | Background process supervisor | 840.50 | 211 | 310 | `mixed` | workaround |
| 27 | Browser automation bridge | 820.50 | 158 | 346 | `mixed` | workaround |
| 28 | IDE context consent | 790.75 | 88 | 509 | `mixed` | workaround |
| 29 | Context budget meter | 754.25 | 130 | 303 | `mixed` | workaround |
| 30 | Attachment preflight | 739.50 | 153 | 308 | `mixed` | workaround |
| 31 | Git change guard | 733.75 | 122 | 314 | `mixed` | workaround |
| 32 | Plugin manager | 718.50 | 133 | 341 | `mixed` | workaround |
| 33 | Skill and command discovery | 695.25 | 104 | 361 | `mixed` | workaround |
| 34 | Persistent project memory | 690.50 | 130 | 208 | `mixed` | workaround |
| 35 | Evidence-gated completion | 687.50 | 215 | 105 | `mixed` | workaround |
| 36 | Model presets and selector | 659.75 | 75 | 409 | `mixed` | workaround |
| 37 | Headless task runner | 655.75 | 144 | 273 | `mixed` | workaround |
| 38 | LSP code intelligence | 644.25 | 86 | 369 | `mixed` | workaround |
| 39 | Agent model and effort router | 641.75 | 136 | 257 | `function-hooks` | workaround |
| 40 | Agent team coordinator | 627.00 | 136 | 245 | `mixed` | workaround |
| 41 | Repository search and index | 620.75 | 126 | 294 | `mixed` | workaround |
| 42 | Cross-platform shell bridge | 613.25 | 138 | 224 | `mixed` | workaround |
| 43 | MCP access profiles | 598.00 | 68 | 367 | `function-hooks` | workaround |
| 44 | Project context loader | 597.25 | 69 | 343 | `mixed` | workaround |
| 45 | Transcript export and sharing | 586.25 | 84 | 331 | `mixed` | workaround |
| 46 | MCP connection doctor | 585.50 | 132 | 209 | `mixed` | workaround |
| 47 | Settings policy helper | 557.50 | 60 | 309 | `mixed` | workaround |
| 48 | Session discovery and repair | 514.25 | 116 | 136 | `bin` | workaround |
| 49 | Workspace and worktree router | 509.25 | 86 | 297 | `mixed` | workaround |
| 50 | Voice input | 501.50 | 106 | 239 | `mixed` | workaround |
| 51 | Durable agent messaging | 499.50 | 121 | 153 | `mcp` | workaround |
| 52 | Claude Projects knowledge bridge | 495.50 | 1 | 411 | `mcp` | workaround |
| 53 | MCP server manager | 483.25 | 81 | 233 | `mixed` | workaround |
| 54 | Hook workflow builder | 479.00 | 125 | 149 | `mixed` | workaround |
| 55 | Scope and consent guard | 478.50 | 134 | 83 | `mixed` | workaround |
| 56 | Config and project layout | 472.75 | 22 | 338 | `mixed` | workaround |
| 57 | Agent terminal panes | 469.00 | 22 | 382 | `mixed` | workaround |
| 58 | Tool visibility | 458.25 | 40 | 282 | `mixed` | workaround |
| 59 | MCP tool discovery | 447.75 | 100 | 168 | `mixed` | workaround |
| 60 | Session library and groups | 446.25 | 122 | 195 | `mixed` | workaround |
| 61 | Remote session bridge | 418.75 | 62 | 242 | `mixed` | workaround |
| 62 | Scoped file access guard | 416.75 | 97 | 103 | `settings-hook` | workaround |
| 63 | Agent context manager | 413.00 | 98 | 137 | `mixed` | workaround |
| 64 | Permission prompt helper | 401.75 | 105 | 118 | `mixed` | workaround |
| 65 | Plugin and skill doctor | 400.25 | 115 | 96 | `mixed` | workaround |
| 66 | Usage and workflow telemetry | 394.25 | 81 | 151 | `mixed` | workaround |
| 67 | Secret and egress shield | 382.25 | 97 | 99 | `mixed` | workaround |
| 68 | Agent hang watchdog | 363.00 | 112 | 79 | `mixed` | workaround |
| 69 | Conversation branching | 351.75 | 42 | 208 | `mixed` | workaround |
| 70 | Companion customization | 348.75 | 87 | 125 | `function-hooks` | workaround |
| 71 | Gmail connector | 341.25 | 39 | 202 | `mcp` | workaround |
| 72 | MCP schema and result adapter | 337.50 | 93 | 90 | `mcp` | workaround |
| 73 | Account profile launcher | 315.00 | 16 | 185 | `bin` | workaround |
| 74 | Conversation navigation | 308.00 | 102 | 112 | `mixed` | workaround |
| 75 | Agent status dashboard | 305.75 | 92 | 104 | `mixed` | workaround |
| 76 | Output formatting checks | 300.00 | 10 | 255 | `mixed` | workaround |
| 77 | Chat reading controls | 292.50 | 39 | 175 | `function-hooks` | workaround |
| 78 | Tool efficiency coach | 287.75 | 9 | 223 | `mixed` | workaround |
| 79 | Claude history and projects bridge | 285.75 | 6 | 235 | `mcp` | workaround |
| 80 | Code navigation | 276.00 | 27 | 167 | `mixed` | full |
| 81 | Evidence first research | 274.75 | 74 | 57 | `mixed` | workaround |
| 82 | Read-only planning guard | 264.50 | 43 | 128 | `mixed` | workaround |
| 83 | Spend and quota guard | 261.25 | 79 | 55 | `mixed` | workaround |
| 84 | Web retrieval with source fidelity | 258.75 | 62 | 85 | `mixed` | workaround |
| 85 | Git attribution guard | 245.00 | 36 | 143 | `mixed` | workaround |
| 86 | Quiet interface | 241.00 | 13 | 173 | `function-hooks` | workaround |
| 87 | api-retry-diagnostics | 223.00 | 11 | 154 | `function-hooks` | workaround |
| 88 | Commands across clients | 217.75 | 14 | 153 | `function-hooks` | workaround |
| 89 | Terminal title controller | 213.00 | 33 | 109 | `mixed` | workaround |
| 90 | GitLab connector | 213.00 | 2 | 181 | `mcp` | full |
| 91 | Accessible output | 211.50 | 33 | 118 | `mixed` | workaround |
| 92 | Secrets and environment injection | 209.50 | 4 | 185 | `settings-hook` | workaround |
| 93 | MCP process supervisor | 206.25 | 44 | 53 | `mixed` | workaround |
| 94 | Conversation timestamps | 204.50 | 7 | 157 | `mixed` | workaround |
| 95 | Slack connector | 201.50 | 17 | 144 | `mcp` | workaround |
| 96 | Subagent permission broker | 200.50 | 39 | 57 | `mixed` | workaround |
| 97 | Scratch and cache cleanup | 196.75 | 33 | 90 | `monitor` | workaround |
| 98 | Live session dashboard | 196.50 | 38 | 84 | `mixed` | workaround |
| 99 | Session and agent lifecycle automation | 193.50 | 32 | 87 | `function-hooks` | workaround |
| 100 | Prompt injection boundary | 184.50 | 38 | 57 | `mixed` | workaround |
| 101 | Plugin and skill sync | 183.75 | 3 | 156 | `mixed` | workaround |
| 102 | Rich math and diagrams | 182.50 | 5 | 149 | `mixed` | workaround |
| 103 | Usage and model status | 179.00 | 15 | 132 | `mixed` | workaround |
| 104 | Portable skill and profile sync | 178.50 | 13 | 111 | `mixed` | workaround |
| 105 | External integration | 177.50 | 4 | 146 | `mcp` | full |
| 106 | cache-policy | 172.50 | 48 | 57 | `function-hooks` | workaround |
| 107 | Terminal UI preferences | 172.50 | 16 | 109 | `function-hooks` | workaround |
| 108 | Date and environment context | 171.75 | 10 | 108 | `settings-hook` | full |
| 109 | MCP sampling bridge | 171.50 | 1 | 124 | `function-hooks` | workaround |
| 110 | Safe prompt clicks | 168.00 | 22 | 117 | `function-hooks` | workaround |
| 111 | Prompt and theme UI | 167.50 | 16 | 109 | `function-hooks` | workaround |
| 112 | Configuration profiles | 166.00 | 40 | 51 | `mixed` | workaround |
| 113 | Hook data and mutation | 156.00 | 28 | 65 | `function-hooks` | workaround |
| 114 | Model identity and switch alerts | 155.00 | 53 | 23 | `function-hooks` | workaround |
| 115 | Provider gateway | 154.75 | 35 | 70 | `mixed` | workaround |
| 116 | Sandbox policy and runner | 154.50 | 17 | 94 | `mixed` | workaround |
| 117 | Skill scope and profiles | 153.25 | 23 | 87 | `function-hooks` | workaround |
| 118 | Terminal setup | 151.75 | 17 | 90 | `bin` | workaround |
| 119 | MCP tool policy | 151.25 | 34 | 49 | `mixed` | workaround |
| 120 | Configuration integrity guard | 151.25 | 26 | 68 | `monitor` | workaround |
| 121 | Unicode tool-argument guard | 148.25 | 3 | 136 | `function-hooks` | workaround |
| 122 | Message timestamps | 148.00 | 11 | 98 | `function-hooks` | full |
| 123 | Skill authoring coach | 147.00 | 58 | 15 | `mixed` | workaround |
| 124 | Command authoring | 144.25 | 23 | 59 | `command` | workaround |
| 125 | MCP UI customization | 143.50 | 38 | 45 | `function-hooks` | workaround |
| 126 | AskUserQuestion enhancements | 142.50 | 41 | 53 | `function-hooks` | workaround |
| 127 | Agent delegation control | 142.50 | 5 | 99 | `mixed` | workaround |
| 128 | Git and PR workflow | 141.25 | 15 | 87 | `skill` | full |
| 129 | Session retention and cleanup | 140.75 | 14 | 65 | `command` | workaround |
| 130 | account-profiles | 136.75 | 16 | 96 | `mixed` | workaround |
| 131 | Transcript vault | 136.50 | 53 | 39 | `mixed` | workaround |
| 132 | Deterministic tool policy | 135.50 | 43 | 5 | `mixed` | workaround |
| 133 | Environment setup and diagnostics | 131.00 | 13 | 84 | `command` | workaround |
| 134 | requirements-ledger | 130.75 | 81 | 5 | `mixed` | workaround |
| 135 | File link rendering | 125.00 | 26 | 64 | `mixed` | workaround |
| 136 | Git provider connectors | 123.50 | 10 | 95 | `mixed` | full |
| 137 | Plain text output and copy | 121.00 | 1 | 106 | `mixed` | workaround |
| 138 | Spinner and tip controls | 118.00 | 29 | 46 | `mixed` | workaround |
| 139 | Spinner wording | 117.50 | 7 | 84 | `function-hooks` | workaround |
| 140 | Desktop computer control | 111.75 | 38 | 21 | `mcp` | workaround |
| 141 | Execution environment and profiles | 103.00 | 43 | 18 | `mixed` | workaround |
| 142 | Queued prompts | 100.75 | 8 | 68 | `mixed` | workaround |
| 143 | Local credential broker | 99.50 | 10 | 57 | `mixed` | workaround |
| 144 | Editor window behavior | 99.25 | 11 | 63 | `function-hooks` | workaround |
| 145 | Spoken responses | 99.00 | 11 | 57 | `mixed` | full |
| 146 | Media preprocessing | 94.25 | 3 | 68 | `mixed` | workaround |
| 147 | Usage and context dashboard | 92.75 | 25 | 19 | `mixed` | workaround |
| 148 | Test and review workflow | 92.50 | 24 | 19 | `mixed` | workaround |
| 149 | MCP resource bridge | 92.50 | 15 | 42 | `mixed` | workaround |
| 150 | Localize Claude Code UI | 91.75 | 30 | 25 | `function-hooks` | workaround |
| 151 | MCP elicitation bridge | 91.00 | 18 | 39 | `function-hooks` | workaround |
| 152 | Compaction handoff | 88.50 | 27 | 9 | `mixed` | workaround |
| 153 | Interactive terminal help | 87.00 | 31 | 0 | `mixed` | workaround |
| 154 | Hook integrity guard | 86.00 | 15 | 23 | `settings-hook` | workaround |
| 155 | Telemetry export | 85.25 | 7 | 50 | `mixed` | workaround |
| 156 | source-grounding | 84.00 | 43 | 6 | `mixed` | workaround |
| 157 | Agent file persistence check | 84.00 | 3 | 54 | `mixed` | workaround |
| 158 | Terminal links | 83.50 | 30 | 29 | `function-hooks` | workaround |
| 159 | Project folder shortcuts | 82.50 | 14 | 42 | `command` | workaround |
| 160 | File picker search | 81.50 | 10 | 42 | `mixed` | workaround |
| 161 | Document and media processing | 81.50 | 7 | 45 | `mcp` | workaround |
| 162 | Queue next prompt | 81.50 | 3 | 63 | `function-hooks` | full |
| 163 | Independent browser MCP | 80.75 | 18 | 33 | `mcp` | workaround |
| 164 | Agent configuration | 79.25 | 18 | 25 | `agent` | workaround |
| 165 | Time and runtime metadata | 78.50 | 12 | 35 | `mixed` | full |
| 166 | Prompt editing and suggestions | 78.50 | 11 | 45 | `function-hooks` | full |
| 167 | Queued prompt manager | 78.00 | 15 | 39 | `function-hooks` | workaround |
| 168 | Messaging channels | 76.50 | 24 | 24 | `mixed` | workaround |
| 169 | Channel workflow bridge | 76.50 | 14 | 34 | `mixed` | workaround |
| 170 | Command and menu extensions | 76.00 | 36 | 16 | `mixed` | workaround |
| 171 | Git and worktree status | 76.00 | 17 | 33 | `mixed` | workaround |
| 172 | Repository host connector | 76.00 | 5 | 61 | `mixed` | workaround |
| 173 | Right-to-left text support | 75.50 | 17 | 26 | `function-hooks` | workaround |
| 174 | Plan and document annotation | 72.75 | 15 | 35 | `mixed` | workaround |
| 175 | Session control and exit | 72.50 | 37 | 19 | `mixed` | workaround |
| 176 | Telemetry and privacy inspector | 72.00 | 13 | 40 | `mixed` | workaround |
| 177 | Browser access workaround | 72.00 | 9 | 46 | `mcp` | workaround |
| 178 | Model picker controls | 71.00 | 2 | 60 | `function-hooks` | workaround |
| 179 | Model and effort indicator | 70.00 | 19 | 32 | `function-hooks` | workaround |
| 180 | Hook trace and debugger | 69.75 | 18 | 20 | `mixed` | workaround |
| 181 | Background attach | 69.50 | 5 | 51 | `bin` | workaround |
| 182 | Context and compaction manager | 69.00 | 17 | 16 | `function-hooks` | workaround |
| 183 | Shared scheduled task registry | 68.50 | 3 | 61 | `mcp` | workaround |
| 184 | Channel process supervisor | 68.25 | 13 | 28 | `monitor` | workaround |
| 185 | Private sessions | 68.25 | 9 | 33 | `mixed` | workaround |
| 186 | UI localization and RTL | 68.00 | 38 | 13 | `function-hooks` | workaround |
| 187 | Skill permission profiles | 68.00 | 21 | 12 | `function-hooks` | workaround |
| 188 | Prompt history search | 68.00 | 9 | 37 | `function-hooks` | full |
| 189 | File reference and completion | 66.00 | 22 | 10 | `mixed` | workaround |
| 190 | Slash command picker | 65.50 | 20 | 25 | `function-hooks` | workaround |
| 191 | Prompt queue and steering | 64.50 | 9 | 25 | `function-hooks` | workaround |
| 192 | Settings reference and validator | 63.50 | 26 | 3 | `mixed` | workaround |
| 193 | Permission profiles | 63.00 | 17 | 15 | `mixed` | workaround |
| 194 | Skill trigger bridge | 63.00 | 15 | 29 | `settings-hook` | workaround |
| 195 | Shared project memory | 62.50 | 7 | 38 | `mixed` | workaround |
| 196 | Composable skill workflows | 62.00 | 21 | 8 | `mixed` | workaround |
| 197 | Prompt autocomplete and suggestions | 62.00 | 8 | 41 | `function-hooks` | workaround |
| 198 | MCP prompts and templates | 61.00 | 14 | 23 | `command` | workaround |
| 199 | Composer submission and queue | 60.25 | 6 | 44 | `function-hooks` | workaround |
| 200 | Workflow command guard | 60.00 | 12 | 20 | `mixed` | workaround |
| 201 | RTL and script display | 60.00 | 5 | 41 | `function-hooks` | workaround |
| 202 | Configuration sync | 60.00 | 1 | 46 | `bin` | workaround |
| 203 | Input keybindings | 59.25 | 14 | 23 | `function-hooks` | workaround |
| 204 | Notebook bridge | 59.25 | 4 | 36 | `mixed` | workaround |
| 205 | Google Drive and Docs connector | 58.00 | 13 | 29 | `mcp` | workaround |
| 206 | Jira connector | 58.00 | 1 | 39 | `mcp` | workaround |
| 207 | Session search and index | 57.25 | 15 | 20 | `bin` | full |
| 208 | Permission and sandbox auditor | 57.00 | 22 | 1 | `mixed` | workaround |
| 209 | Command shortcuts | 56.50 | 17 | 12 | `mixed` | workaround |
| 210 | Hyperlink helper | 56.50 | 14 | 20 | `mixed` | workaround |
| 211 | Agent configuration validator | 56.25 | 21 | 9 | `mixed` | workaround |
| 212 | Prompt preflight | 56.00 | 13 | 12 | `mixed` | workaround |
| 213 | Local feedback capture | 55.25 | 17 | 11 | `command` | workaround |
| 214 | Cross-surface handoff | 55.25 | 7 | 34 | `mixed` | workaround |
| 215 | action-guard | 55.00 | 39 | 1 | `settings-hook` | workaround |
| 216 | Notebook editor | 55.00 | 10 | 20 | `mcp` | full |
| 217 | Scheduled connector bridge | 55.00 | 7 | 28 | `mcp` | workaround |
| 218 | Spinner and motion controls | 54.50 | 31 | 8 | `mixed` | full |
| 219 | Session backup and recovery | 54.50 | 11 | 11 | `mixed` | workaround |
| 220 | Edit conflict guard | 54.50 | 9 | 24 | `settings-hook` | workaround |
| 221 | Session search and grouping | 54.00 | 22 | 23 | `mixed` | workaround |
| 222 | Tool output filtering | 53.75 | 15 | 9 | `function-hooks` | workaround |
| 223 | Background task supervisor | 53.50 | 25 | 2 | `mixed` | workaround |
| 224 | Notion connector | 53.50 | 8 | 29 | `mcp` | workaround |
| 225 | Media conversion and generation | 52.00 | 11 | 19 | `mcp` | full |
| 226 | Installation and provider doctor | 51.50 | 20 | 1 | `mixed` | workaround |
| 227 | Session title manager | 51.50 | 19 | 14 | `mixed` | workaround |
| 228 | Agent message queue | 51.50 | 9 | 21 | `mixed` | workaround |
| 229 | Local file and format tools | 50.50 | 8 | 25 | `mixed` | workaround |
| 230 | MCP session identity | 50.50 | 3 | 37 | `function-hooks` | workaround |
| 231 | Project task and roadmap manager | 49.50 | 17 | 11 | `mcp` | full |
| 232 | Terminal title controller | 49.50 | 16 | 11 | `settings-hook` | workaround |
| 233 | Git provider review bridge | 49.25 | 3 | 35 | `mixed` | full |
| 234 | Feedback prompt controls | 48.75 | 20 | 3 | `function-hooks` | workaround |
| 235 | Queued and mid-turn messages | 48.50 | 19 | 8 | `function-hooks` | workaround |
| 236 | Privacy and safety controls | 48.00 | 4 | 31 | `mixed` | workaround |
| 237 | Browser site grants | 47.50 | 10 | 23 | `function-hooks` | workaround |
| 238 | Prompt and response rewriting | 47.50 | 7 | 24 | `function-hooks` | full |
| 239 | Permission outcome ledger | 47.00 | 20 | 10 | `mixed` | workaround |
| 240 | Screen reader access | 45.50 | 27 | 6 | `function-hooks` | workaround |
| 241 | Live session watchdog | 45.50 | 11 | 8 | `monitor` | workaround |
| 242 | Browser site policy | 45.50 | 6 | 22 | `mcp` | workaround |
| 243 | Localization | 45.00 | 7 | 22 | `mixed` | workaround |
| 244 | PDF and document reader | 44.50 | 13 | 15 | `mcp` | full |
| 245 | Agent resource meter | 44.00 | 9 | 22 | `mixed` | workaround |
| 246 | Diff review controls | 43.50 | 25 | 11 | `mixed` | workaround |
| 247 | File and artifact browser | 42.50 | 19 | 19 | `mixed` | workaround |
| 248 | Command execution policy | 42.50 | 10 | 14 | `mixed` | workaround |
| 249 | AskUserQuestion helper | 42.00 | 16 | 6 | `function-hooks` | workaround |
| 250 | Clarification before action | 42.00 | 14 | 8 | `mixed` | workaround |
| 251 | Question context and previews | 41.50 | 16 | 15 | `function-hooks` | workaround |
| 252 | Accessibility output | 41.50 | 8 | 15 | `mixed` | workaround |
| 253 | Workflow composition | 41.00 | 10 | 8 | `mixed` | workaround |
| 254 | completion-gate | 40.50 | 28 | 0 | `mixed` | workaround |
| 255 | Current API and version lookup | 40.50 | 15 | 3 | `mixed` | workaround |
| 256 | MCP across clients | 40.25 | 5 | 21 | `mixed` | workaround |
| 257 | Scheduled task permission preflight | 40.00 | 18 | 5 | `mixed` | workaround |
| 258 | Terminal display controls | 40.00 | 10 | 16 | `function-hooks` | workaround |
| 259 | Agent team persistence | 40.00 | 9 | 11 | `mcp` | workaround |
| 260 | MCP output control | 40.00 | 9 | 10 | `mixed` | workaround |
| 261 | Question and dialog key safety | 39.50 | 19 | 13 | `function-hooks` | workaround |
| 262 | Terminal title and progress | 39.25 | 8 | 14 | `mixed` | workaround |
| 263 | Prompt suggestion controls | 39.00 | 21 | 0 | `function-hooks` | full |
| 264 | Session lifecycle commands | 39.00 | 10 | 17 | `command` | workaround |
| 265 | Skill cost guard | 39.00 | 5 | 16 | `mixed` | workaround |
| 266 | Tool capability reference | 38.00 | 12 | 5 | `mixed` | workaround |
| 267 | Live steering and interruption | 38.00 | 9 | 13 | `function-hooks` | workaround |
| 268 | Agent cost and usage display | 37.75 | 6 | 25 | `function-hooks` | workaround |
| 269 | efficiency-coach | 36.75 | 19 | 1 | `mixed` | workaround |
| 270 | SSH session launcher | 36.50 | 6 | 14 | `bin` | workaround |
| 271 | Prompt and context observability | 36.50 | 5 | 1 | `function-hooks` | workaround |
| 272 | Model and context routing | 36.25 | 4 | 17 | `function-hooks` | workaround |
| 273 | Plugin author analytics | 35.75 | 8 | 13 | `mixed` | workaround |
| 274 | Task and agent workflows | 35.50 | 8 | 13 | `mixed` | workaround |
| 275 | Permission rule linter | 35.00 | 15 | 8 | `command` | full |
| 276 | Time and timezone context | 35.00 | 12 | 4 | `mixed` | workaround |
| 277 | Project launch profiles | 35.00 | 4 | 17 | `bin` | workaround |
| 278 | Agent model and effort display | 34.50 | 11 | 17 | `function-hooks` | workaround |
| 279 | Command palette | 34.50 | 8 | 12 | `mixed` | workaround |
| 280 | Project rules bridge | 34.25 | 3 | 20 | `function-hooks` | workaround |
| 281 | Scheduled task notification bridge | 34.00 | 15 | 8 | `mcp` | workaround |
| 282 | Session switching and launch | 34.00 | 14 | 13 | `function-hooks` | full |
| 283 | Local file explorer | 34.00 | 8 | 14 | `mixed` | workaround |
| 284 | Plugin hook parity | 34.00 | 6 | 14 | `function-hooks` | workaround |
| 285 | Channels bridge | 33.50 | 18 | 3 | `mcp` | workaround |
| 286 | model-state | 33.50 | 14 | 8 | `function-hooks` | workaround |
| 287 | Context and compaction advisor | 33.50 | 7 | 8 | `function-hooks` | workaround |
| 288 | Update notices | 33.00 | 4 | 16 | `settings-hook` | full |
| 289 | Feedback prompt control | 32.75 | 23 | 4 | `function-hooks` | full |
| 290 | Session and background task control | 32.50 | 9 | 9 | `mixed` | workaround |
| 291 | Microsoft 365 connector | 32.50 | 5 | 17 | `mcp` | workaround |
| 292 | Skill prerequisite gate | 32.00 | 14 | 5 | `settings-hook` | workaround |
| 293 | Change impact tracing | 32.00 | 11 | 4 | `mixed` | workaround |
| 294 | Prompt cache hygiene | 32.00 | 5 | 4 | `function-hooks` | workaround |
| 295 | Question dialog customization | 31.75 | 5 | 18 | `function-hooks` | workaround |
| 296 | Input spellcheck | 31.50 | 3 | 23 | `function-hooks` | workaround |
| 297 | Scheduled task connector profile | 31.25 | 11 | 6 | `command` | workaround |
| 298 | Secret broker | 31.25 | 8 | 16 | `mixed` | workaround |
| 299 | Shell-only interactive mode | 31.00 | 13 | 4 | `mixed` | workaround |
| 300 | Shell environment refresh | 30.75 | 1 | 23 | `bin` | workaround |
| 301 | Prompt and tool hygiene | 29.00 | 15 | 2 | `function-hooks` | workaround |
| 302 | Scheduled task orchestration | 29.00 | 11 | 3 | `mixed` | workaround |
| 303 | Prompt transformer | 29.00 | 7 | 19 | `function-hooks` | workaround |
| 304 | External diff | 29.00 | 1 | 26 | `command` | workaround |
| 305 | Claude Code in-session advisor | 28.50 | 14 | 0 | `mixed` | workaround |
| 306 | Agent dispatch controls | 28.50 | 9 | 10 | `mixed` | workaround |
| 307 | Draft preservation | 28.50 | 8 | 7 | `function-hooks` | workaround |
| 308 | MCP tool bridge | 28.50 | 6 | 12 | `mcp` | workaround |
| 309 | External action approval | 28.00 | 20 | 0 | `settings-hook` | workaround |
| 310 | Security and production checks | 28.00 | 12 | 0 | `mixed` | workaround |
| 311 | Permission audit trail | 28.00 | 7 | 5 | `mixed` | workaround |
| 312 | Private feedback | 27.50 | 15 | 3 | `command` | workaround |
| 313 | Hook diagnostics | 27.00 | 12 | 0 | `mixed` | workaround |
| 314 | Safe session controls | 27.00 | 8 | 6 | `function-hooks` | workaround |
| 315 | Task board | 27.00 | 8 | 1 | `mixed` | workaround |
| 316 | credential-audit | 26.75 | 9 | 8 | `mixed` | workaround |
| 317 | Context and tool catalog control | 26.50 | 8 | 4 | `function-hooks` | workaround |
| 318 | Model and tool interception | 26.50 | 7 | 7 | `function-hooks` | full |
| 319 | Headless automation guide | 26.00 | 10 | 0 | `mixed` | workaround |
| 320 | Cloud instruction sync | 25.50 | 7 | 6 | `mixed` | workaround |
| 321 | Design system and Figma connector | 25.50 | 6 | 13 | `mcp` | workaround |
| 322 | Diff and preview UI | 25.50 | 4 | 14 | `bin` | workaround |
| 323 | Menu customization | 25.50 | 3 | 14 | `function-hooks` | workaround |
| 324 | Prompt injection shield | 25.25 | 14 | 0 | `settings-hook` | workaround |
| 325 | Outbound prose gate | 24.50 | 11 | 3 | `function-hooks` | workaround |
| 326 | Prompt history and recovery | 24.50 | 8 | 6 | `mixed` | workaround |
| 327 | Research workflow | 24.00 | 11 | 1 | `skill` | full |
| 328 | Time and environment context | 24.00 | 11 | 5 | `settings-hook` | full |
| 329 | Context and compaction policy | 23.75 | 3 | 16 | `function-hooks` | workaround |
| 330 | Insights report | 23.50 | 9 | 3 | `command` | workaround |
| 331 | Skill overlays | 23.50 | 9 | 8 | `mixed` | workaround |
| 332 | Tool output limiter | 23.50 | 5 | 6 | `function-hooks` | full |
| 333 | Safer approval UI | 23.00 | 12 | 5 | `function-hooks` | workaround |
| 334 | Cross-platform hook runner | 23.00 | 9 | 1 | `bin` | workaround |
| 335 | Approval timeout control | 23.00 | 5 | 11 | `mixed` | workaround |
| 336 | Design system sync | 22.50 | 12 | 7 | `skill` | workaround |
| 337 | Agent prompt quality | 22.50 | 9 | 0 | `agent` | workaround |
| 338 | Commit and PR policy | 22.50 | 6 | 6 | `mixed` | workaround |
| 339 | Prompt stash | 22.50 | 4 | 10 | `function-hooks` | full |
| 340 | Marketplace browser and search | 22.00 | 9 | 0 | `mixed` | workaround |
| 341 | Search current chat | 22.00 | 5 | 11 | `function-hooks` | workaround |
| 342 | MCP long-job adapter | 22.00 | 4 | 10 | `mcp` | workaround |
| 343 | Accessibility mode | 21.50 | 8 | 3 | `mixed` | workaround |
| 344 | Autonomous task governance | 21.50 | 7 | 4 | `mixed` | workaround |
| 345 | Cloud environment setup | 21.50 | 4 | 12 | `command` | workaround |
| 346 | Security review workflow | 21.00 | 9 | 0 | `mixed` | workaround |
| 347 | Inline images and graphics | 20.75 | 5 | 12 | `mixed` | workaround |
| 348 | Browser session targeting | 20.50 | 7 | 6 | `mcp` | workaround |
| 349 | Tool-use guardrails | 19.50 | 11 | 0 | `settings-hook` | workaround |
| 350 | Agent liveness and progress | 19.00 | 12 | 0 | `monitor` | workaround |
| 351 | Direct action bindings | 19.00 | 10 | 4 | `function-hooks` | full |
| 352 | Transcript privacy | 19.00 | 7 | 2 | `function-hooks` | workaround |
| 353 | workflow-scheduler | 19.00 | 7 | 1 | `mixed` | workaround |
| 354 | Permission decision explainer | 18.50 | 10 | 5 | `function-hooks` | full |
| 355 | CI and PR monitor | 18.50 | 6 | 6 | `monitor` | workaround |
| 356 | Plugin recommendations | 18.50 | 6 | 1 | `skill` | full |
| 357 | Browser activity log | 18.50 | 3 | 11 | `settings-hook` | workaround |
| 358 | Worktree access | 18.50 | 2 | 12 | `function-hooks` | workaround |
| 359 | Autonomous workflow budgets | 18.00 | 8 | 1 | `mixed` | workaround |
| 360 | Read boundary | 17.50 | 7 | 2 | `settings-hook` | workaround |
| 361 | Review cost controller | 17.00 | 11 | 0 | `mixed` | workaround |
| 362 | Response and diff presentation | 17.00 | 5 | 6 | `mixed` | workaround |
| 363 | Project folder continuity | 17.00 | 4 | 11 | `mixed` | workaround |
| 364 | review-gate | 16.50 | 16 | 0 | `mixed` | workaround |
| 365 | Session status dashboard | 16.50 | 10 | 3 | `statusline` | workaround |
| 366 | Agent permissions and scope | 16.50 | 6 | 0 | `mixed` | workaround |
| 367 | Telemetry and tracing | 16.50 | 3 | 10 | `mixed` | workaround |
| 368 | Output presentation | 16.00 | 4 | 6 | `mixed` | workaround |
| 369 | Worktree naming and lifecycle | 16.00 | 2 | 11 | `settings-hook` | workaround |
| 370 | Read-only specialist agents | 15.50 | 10 | 1 | `mixed` | workaround |
| 371 | Conversation recap and chapters | 15.50 | 7 | 0 | `mixed` | workaround |
| 372 | Session navigation | 15.50 | 4 | 4 | `command` | workaround |
| 373 | Atlassian connector | 15.00 | 7 | 0 | `mcp` | workaround |
| 374 | Prompt suggestions | 15.00 | 6 | 3 | `function-hooks` | workaround |
| 375 | Scheduled skill packaging | 15.00 | 6 | 0 | `mixed` | workaround |
| 376 | Inline annotations and replies | 15.00 | 4 | 5 | `function-hooks` | workaround |
| 377 | Agent initialization | 15.00 | 2 | 7 | `settings-hook` | workaround |
| 378 | developer-tracing | 14.50 | 7 | 3 | `mixed` | workaround |
| 379 | Dependency guard | 14.50 | 4 | 8 | `settings-hook` | full |
| 380 | Git action guardrails | 14.50 | 4 | 7 | `mixed` | full |
| 381 | Remote MCP compatibility | 14.50 | 4 | 4 | `mcp` | workaround |
| 382 | Session collision guard | 14.50 | 2 | 0 | `settings-hook` | full |
| 383 | Browser content extraction | 14.00 | 3 | 10 | `mcp` | workaround |
| 384 | Workflow input normalizer | 14.00 | 2 | 9 | `function-hooks` | workaround |
| 385 | Tool output transformer | 13.75 | 6 | 0 | `mixed` | workaround |
| 386 | Agent message provenance | 13.50 | 8 | 1 | `function-hooks` | workaround |
| 387 | Skill and plugin inventory | 13.50 | 7 | 3 | `command` | workaround |
| 388 | Editor and preview launcher | 13.00 | 6 | 2 | `command` | workaround |
| 389 | Completion and approval alerts | 12.50 | 4 | 4 | `mixed` | workaround |
| 390 | Project-local skill profiles | 12.50 | 4 | 4 | `mixed` | workaround |
| 391 | Scoped grants | 12.00 | 7 | 3 | `function-hooks` | workaround |
| 392 | Browser device guard | 12.00 | 5 | 0 | `settings-hook` | workaround |
| 393 | Side-channel helper | 12.00 | 5 | 3 | `command` | workaround |
| 394 | MCP server health | 12.00 | 2 | 3 | `monitor` | workaround |
| 395 | Security audit workflow | 11.75 | 8 | 0 | `mixed` | full |
| 396 | Agent cost and loop limit | 11.75 | 6 | 0 | `function-hooks` | full |
| 397 | Project diagnostics | 11.50 | 4 | 1 | `mixed` | full |
| 398 | Workflow pipeline | 11.50 | 4 | 3 | `mixed` | full |
| 399 | Agent transcript viewer | 11.50 | 3 | 4 | `mixed` | workaround |
| 400 | Cross-session task board | 11.50 | 3 | 3 | `mixed` | full |
| 401 | Prompt and command queue | 11.50 | 3 | 2 | `function-hooks` | workaround |
| 402 | Session controls | 11.50 | 3 | 4 | `function-hooks` | workaround |
| 403 | Precise edit tool | 11.50 | 1 | 7 | `mcp` | full |
| 404 | Usage efficient commands | 11.25 | 4 | 1 | `mixed` | full |
| 405 | Unattended task approvals | 11.00 | 6 | 1 | `function-hooks` | workaround |
| 406 | Transcript viewer | 11.00 | 5 | 1 | `command` | workaround |
| 407 | API and tool error recovery | 11.00 | 4 | 1 | `mixed` | workaround |
| 408 | Browser identity context | 11.00 | 1 | 6 | `function-hooks` | workaround |
| 409 | Repository security audit | 10.50 | 8 | 0 | `mixed` | full |
| 410 | Adversarial source review | 10.50 | 7 | 0 | `agent` | full |
| 411 | Question answer editing | 10.50 | 7 | 0 | `function-hooks` | workaround |
| 412 | Change recovery | 10.50 | 3 | 0 | `mixed` | workaround |
| 413 | Conversation annotation | 10.50 | 3 | 6 | `function-hooks` | workaround |
| 414 | review-before-edit | 10.00 | 7 | 0 | `mixed` | workaround |
| 415 | Custom tool registration | 10.00 | 6 | 1 | `function-hooks` | workaround |
| 416 | File and link handling | 10.00 | 6 | 2 | `mixed` | workaround |
| 417 | Task and milestone tracker | 10.00 | 4 | 0 | `mixed` | workaround |
| 418 | Visual deliverable verification | 10.00 | 4 | 0 | `mixed` | workaround |
| 419 | Spellcheck and autocorrect | 10.00 | 3 | 4 | `settings-hook` | workaround |
| 420 | IDE selection badge | 10.00 | 1 | 9 | `function-hooks` | full |
| 421 | Edit preview sandbox | 9.75 | 1 | 7 | `command` | workaround |
| 422 | conversation-memory | 9.50 | 7 | 0 | `mixed` | workaround |
| 423 | Edit preview | 9.50 | 4 | 3 | `function-hooks` | full |
| 424 | Session launcher | 9.50 | 3 | 2 | `bin` | workaround |
| 425 | Terminal display adapter | 9.50 | 2 | 5 | `function-hooks` | workaround |
| 426 | Outbound data guard | 9.00 | 5 | 0 | `settings-hook` | workaround |
| 427 | Concurrent session coordinator | 9.00 | 4 | 0 | `mixed` | workaround |
| 428 | Safety and approval gate | 9.00 | 3 | 1 | `settings-hook` | workaround |
| 429 | CI status panel | 9.00 | 2 | 4 | `mixed` | workaround |
| 430 | MCP widget presentation | 8.50 | 4 | 3 | `function-hooks` | workaround |
| 431 | Trusted peer sessions | 8.50 | 2 | 5 | `function-hooks` | full |
| 432 | Stop reason indicator | 8.25 | 2 | 2 | `function-hooks` | workaround |
| 433 | Cross-session messaging | 8.00 | 6 | 0 | `mixed` | workaround |
| 434 | Write boundary | 8.00 | 6 | 0 | `settings-hook` | workaround |
| 435 | Interactive question controls | 8.00 | 3 | 3 | `function-hooks` | workaround |
| 436 | Browser credential handoff | 8.00 | 2 | 3 | `mcp` | workaround |
| 437 | Session privacy scrubber | 8.00 | 2 | 1 | `mixed` | workaround |
| 438 | Local task API | 8.00 | 1 | 4 | `bin` | workaround |
| 439 | Desktop session tabs | 7.50 | 4 | 2 | `function-hooks` | workaround |
| 440 | ui-test-auth | 7.50 | 3 | 4 | `mcp` | workaround |
| 441 | File mutation audit | 7.50 | 2 | 4 | `settings-hook` | workaround |
| 442 | Local shell selection | 7.50 | 2 | 5 | `function-hooks` | workaround |
| 443 | github-workflows | 7.50 | 2 | 1 | `mixed` | workaround |
| 444 | Tool result validation | 7.00 | 3 | 0 | `function-hooks` | workaround |
| 445 | Transcript backup and recovery | 7.00 | 3 | 0 | `mixed` | workaround |
| 446 | External event triggers | 7.00 | 2 | 1 | `mixed` | workaround |
| 447 | Session search | 7.00 | 2 | 0 | `mcp` | full |
| 448 | design-bridge | 7.00 | 2 | 3 | `mcp` | workaround |
| 449 | Monorepo context | 7.00 | 1 | 2 | `skill` | full |
| 450 | task-reminder | 6.50 | 6 | 0 | `mixed` | workaround |
| 451 | Transcript reading and recap | 6.50 | 4 | 0 | `command` | workaround |
| 452 | PR and CI automation | 6.50 | 3 | 2 | `mixed` | workaround |
| 453 | Slack actions | 6.50 | 3 | 0 | `mcp` | full |
| 454 | MCP workflow orchestration | 6.50 | 2 | 1 | `mixed` | full |
| 455 | Attachment picker | 6.50 | 1 | 3 | `mixed` | workaround |
| 456 | Editor format command | 6.50 | 1 | 3 | `mcp` | workaround |
| 457 | Web application security checks | 6.00 | 5 | 0 | `mixed` | full |
| 458 | Output privacy warnings | 6.00 | 4 | 0 | `settings-hook` | workaround |
| 459 | Session instruction visibility | 6.00 | 4 | 0 | `function-hooks` | workaround |
| 460 | Preview workflow | 6.00 | 3 | 0 | `mixed` | workaround |
| 461 | Session identity labels | 6.00 | 3 | 0 | `mixed` | workaround |
| 462 | Cross-device setup sync | 6.00 | 2 | 1 | `bin` | workaround |
| 463 | Image perception tools | 6.00 | 1 | 2 | `mcp` | workaround |
| 464 | MCP trace context | 6.00 | 1 | 4 | `mixed` | workaround |
| 465 | Task manager integration | 6.00 | 1 | 2 | `mcp` | full |
| 466 | Interactive plugin UI | 5.50 | 3 | 1 | `function-hooks` | workaround |
| 467 | Transcript display controls | 5.50 | 3 | 1 | `function-hooks` | workaround |
| 468 | Personal memory | 5.50 | 2 | 1 | `mixed` | workaround |
| 469 | Task service connectors | 5.50 | 2 | 1 | `mcp` | full |
| 470 | Scheduled task control | 5.50 | 1 | 3 | `mcp` | workaround |
| 471 | Custom prompt and autocomplete | 5.00 | 4 | 0 | `function-hooks` | workaround |
| 472 | Channel message safety | 5.00 | 3 | 0 | `mcp` | full |
| 473 | Google Calendar connector | 5.00 | 3 | 0 | `mcp` | workaround |
| 474 | iCloud and CalDAV calendars | 5.00 | 3 | 0 | `mcp` | full |
| 475 | code-comment-hygiene | 5.00 | 2 | 2 | `mixed` | workaround |
| 476 | Output and reasoning viewer | 4.50 | 3 | 0 | `mixed` | workaround |
| 477 | Office mail connector | 4.50 | 2 | 0 | `mcp` | full |
| 478 | Translation and locale | 4.50 | 2 | 0 | `mixed` | workaround |
| 479 | SSH certificate bridge | 4.50 | 1 | 3 | `mcp` | workaround |
| 480 | MCP tool adapter | 4.00 | 3 | 0 | `mcp` | workaround |
| 481 | Plugin configuration bridge | 4.00 | 3 | 0 | `mixed` | workaround |
| 482 | Agent configuration hot reload | 4.00 | 2 | 0 | `mixed` | workaround |
| 483 | Messenger bridge | 4.00 | 2 | 0 | `mcp` | full |
| 484 | Screenshot saver | 4.00 | 2 | 0 | `mcp` | workaround |
| 485 | Slack tool extension | 4.00 | 2 | 0 | `mcp` | full |
| 486 | Terminal scrollback controls | 4.00 | 2 | 0 | `mixed` | workaround |
| 487 | Service status monitor | 4.00 | 1 | 1 | `monitor` | full |
| 488 | Masked secret entry | 3.50 | 3 | 0 | `function-hooks` | full |
| 489 | early-checkin | 3.50 | 3 | 0 | `mixed` | workaround |
| 490 | Global and scoped configuration | 3.50 | 2 | 0 | `function-hooks` | workaround |
| 491 | issue-reporter | 3.50 | 2 | 1 | `mixed` | full |
| 492 | thinking-language | 3.50 | 2 | 1 | `function-hooks` | workaround |
| 493 | GitHub repository access | 3.50 | 1 | 0 | `mcp` | workaround |
| 494 | Prompt library | 3.50 | 1 | 0 | `skill` | full |
| 495 | Remote compute connector | 3.50 | 1 | 0 | `mcp` | full |
| 496 | Sentry connector | 3.50 | 1 | 0 | `mcp` | full |
| 497 | audio-input | 3.50 | 1 | 1 | `mcp` | full |
| 498 | File picker and mentions | 3.00 | 3 | 0 | `function-hooks` | workaround |
| 499 | Agent activity audit | 3.00 | 2 | 0 | `mixed` | full |
| 500 | Compliance workflow gate | 3.00 | 2 | 0 | `settings-hook` | full |
| 501 | Generic email connector | 3.00 | 2 | 0 | `mcp` | full |
| 502 | Network device inspector | 3.00 | 2 | 0 | `mcp` | full |
| 503 | Nextcloud connector | 3.00 | 2 | 0 | `mcp` | full |
| 504 | QuickBooks connector | 3.00 | 2 | 0 | `mcp` | full |
| 505 | workflow-loop | 3.00 | 2 | 0 | `mixed` | workaround |
| 506 | Mobile UI test agent | 3.00 | 1 | 0 | `mixed` | full |
| 507 | Prompt coaching | 3.00 | 1 | 1 | `skill` | full |
| 508 | SEO skill | 3.00 | 1 | 0 | `skill` | full |
| 509 | Security audit receipts | 3.00 | 1 | 0 | `mixed` | workaround |
| 510 | Spotify playlist operations | 3.00 | 1 | 0 | `mcp` | workaround |
| 511 | JAR patching toolkit | 2.75 | 1 | 0 | `bin` | workaround |
| 512 | Agent roster lifecycle | 2.50 | 2 | 0 | `mixed` | workaround |
| 513 | Asana connector | 2.50 | 1 | 0 | `mcp` | full |
| 514 | Credential handoff | 2.50 | 1 | 1 | `mcp` | workaround |
| 515 | Dependency release watcher | 2.50 | 1 | 0 | `mixed` | full |
| 516 | Google Drive document editing | 2.50 | 1 | 0 | `mcp` | workaround |
| 517 | Migration assistant | 2.50 | 1 | 0 | `skill` | full |
| 518 | Outlook task export | 2.50 | 1 | 0 | `skill` | full |
| 519 | Structured data formats | 2.50 | 1 | 0 | `mcp` | full |
| 520 | Terminal launch preferences | 2.50 | 1 | 1 | `function-hooks` | workaround |
| 521 | Visual asset generation | 2.50 | 1 | 0 | `mcp` | full |
| 522 | Agent oversight | 2.00 | 1 | 0 | `mixed` | workaround |
| 523 | Ancient DNA workflow | 2.00 | 1 | 0 | `skill` | workaround |
| 524 | Apple Reminders connector | 2.00 | 1 | 0 | `mcp` | full |
| 525 | Atlassian integration | 2.00 | 1 | 0 | `mcp` | workaround |
| 526 | CAD file adapter | 2.00 | 1 | 0 | `mcp` | workaround |
| 527 | Calendar connector | 2.00 | 1 | 0 | `mcp` | full |
| 528 | Code comment restraint | 2.00 | 1 | 0 | `mixed` | workaround |
| 529 | Codebase ignore generator | 2.00 | 1 | 0 | `mixed` | full |
| 530 | Cross-device session sync | 2.00 | 1 | 0 | `bin` | workaround |
| 531 | Database documentation | 2.00 | 1 | 0 | `skill` | workaround |
| 532 | Date and weekday check | 2.00 | 1 | 0 | `settings-hook` | full |
| 533 | Debug adapter integration | 2.00 | 1 | 0 | `mcp` | full |
| 534 | Directus integration | 2.00 | 1 | 0 | `mcp` | full |
| 535 | Directus schema generator | 2.00 | 1 | 0 | `mixed` | full |
| 536 | Example-based project generator | 2.00 | 1 | 0 | `command` | full |
| 537 | Facebook Ads operations | 2.00 | 1 | 0 | `mcp` | workaround |
| 538 | Feishu media tool | 2.00 | 1 | 0 | `mcp` | full |
| 539 | Google Chat connector | 2.00 | 1 | 0 | `mcp` | full |
| 540 | Google Sheets connector | 2.00 | 1 | 0 | `mcp` | full |
| 541 | HealthEx connector | 2.00 | 1 | 0 | `mcp` | full |
| 542 | Image asset generation | 2.00 | 1 | 0 | `mcp` | full |
| 543 | Issue filing assistant | 2.00 | 1 | 0 | `skill` | full |
| 544 | Jira intake pipeline | 2.00 | 1 | 0 | `mixed` | full |
| 545 | Keynote editing | 2.00 | 1 | 0 | `mixed` | workaround |
| 546 | Lead scraping agent | 2.00 | 1 | 0 | `agent` | workaround |
| 547 | Literal path import control | 2.00 | 1 | 0 | `function-hooks` | workaround |
| 548 | Looker connector | 2.00 | 1 | 0 | `mcp` | full |
| 549 | MCP skill catalog | 2.00 | 1 | 0 | `mcp` | workaround |
| 550 | Plugin discovery and setup | 2.00 | 1 | 0 | `skill` | workaround |
| 551 | Read state sync | 2.00 | 1 | 1 | `mixed` | workaround |
| 552 | Related file change agent | 2.00 | 1 | 0 | `mixed` | workaround |
| 553 | Remote skill sync | 2.00 | 1 | 0 | `mixed` | workaround |
| 554 | Remote terminal skill | 2.00 | 1 | 0 | `skill` | full |
| 555 | Rendered plan preview | 2.00 | 1 | 0 | `bin` | full |
| 556 | Scripted session bridge | 2.00 | 1 | 0 | `function-hooks` | workaround |
| 557 | Session daemon | 2.00 | 1 | 0 | `mixed` | workaround |
| 558 | Task flow skill | 2.00 | 1 | 0 | `mixed` | full |
| 559 | Thinking transcript viewer | 2.00 | 1 | 0 | `function-hooks` | workaround |
| 560 | Unity editor connector | 2.00 | 1 | 0 | `mcp` | full |
| 561 | Workflow automation integration | 2.00 | 1 | 0 | `mcp` | full |
| 562 | Zoom meeting agent | 2.00 | 1 | 0 | `mixed` | workaround |
| 563 | figma-coverage | 2.00 | 1 | 0 | `skill` | workaround |
| 564 | iCloud Calendar connector | 2.00 | 1 | 0 | `mcp` | full |
| 565 | Autofix scope | 1.50 | 1 | 0 | `settings-hook` | full |
| 566 | Code simplifier | 1.50 | 1 | 0 | `skill` | full |
| 567 | Command favorites | 1.50 | 1 | 0 | `function-hooks` | workaround |
| 568 | Dashlane credential access | 1.50 | 1 | 0 | `mcp` | workaround |
| 569 | Dependency security fixer | 1.50 | 1 | 0 | `skill` | full |
| 570 | Device test viewer | 1.50 | 1 | 0 | `mcp` | workaround |
| 571 | Feedback privacy control | 1.50 | 1 | 0 | `function-hooks` | workaround |
| 572 | Gmail operations | 1.50 | 1 | 0 | `mcp` | workaround |
| 573 | Google Workspace events | 1.50 | 1 | 0 | `mcp` | full |
| 574 | Incident trace linker | 1.50 | 1 | 0 | `mcp` | full |
| 575 | Localization companion | 1.50 | 1 | 0 | `mixed` | workaround |
| 576 | Machine learning experiment workflow | 1.50 | 1 | 0 | `mixed` | full |
| 577 | Notion to GitHub migration | 1.50 | 1 | 0 | `mcp` | workaround |
| 578 | Odoo Runbot connector | 1.50 | 1 | 0 | `mcp` | full |
| 579 | Project templates | 1.50 | 1 | 0 | `mixed` | full |
| 580 | Ramp connector | 1.50 | 1 | 0 | `mcp` | full |
| 581 | Regression suite design | 1.50 | 1 | 0 | `skill` | workaround |
| 582 | Remote exposure check | 1.50 | 1 | 0 | `bin` | full |
| 583 | Safety and permission guard | 1.50 | 1 | 0 | `mixed` | workaround |
| 584 | Session time injection | 1.50 | 1 | 0 | `function-hooks` | full |
| 585 | System process diagnostics | 1.50 | 1 | 0 | `mixed` | workaround |
| 586 | Transient error recovery | 1.50 | 1 | 0 | `mixed` | workaround |
| 587 | design-polish | 1.50 | 1 | 0 | `mixed` | workaround |
| 588 | ios-modernization | 1.50 | 1 | 0 | `skill` | workaround |
| 589 | learning-plan | 1.50 | 1 | 0 | `skill` | full |
| 590 | ocr-verify | 1.50 | 1 | 0 | `mixed` | workaround |
| 591 | Side chat command | 1.25 | 1 | 0 | `function-hooks` | workaround |
| 592 | Answer-first assistant | 1.00 | 1 | 0 | `output-style` | workaround |
| 593 | Approved scripts | 1.00 | 1 | 0 | `mixed` | full |
| 594 | Authentication audit | 1.00 | 1 | 0 | `mixed` | full |
| 595 | Cloud environment bootstrap | 1.00 | 1 | 0 | `mixed` | workaround |
| 596 | Collaborative list review | 1.00 | 1 | 0 | `mixed` | full |
| 597 | Desktop fast-mode control | 1.00 | 1 | 0 | `function-hooks` | workaround |
| 598 | Diff presentation | 1.00 | 1 | 0 | `function-hooks` | workaround |
| 599 | GitHub Projects integration | 1.00 | 1 | 0 | `mcp` | full |
| 600 | Local UI localization | 1.00 | 1 | 0 | `function-hooks` | workaround |
| 601 | Mac fleet diagnostics | 1.00 | 1 | 0 | `mixed` | full |
| 602 | Printer event listener | 1.00 | 1 | 0 | `mixed` | full |
| 603 | Scanner device bridge | 1.00 | 1 | 0 | `mcp` | full |
| 604 | Secret entry | 1.00 | 1 | 0 | `mcp` | full |
| 605 | Session environment metadata | 1.00 | 1 | 0 | `function-hooks` | workaround |
| 606 | Settings backup | 1.00 | 1 | 0 | `bin` | workaround |
| 607 | Skill-driven discussion mode | 1.00 | 1 | 0 | `mixed` | full |
| 608 | Time tracking integration | 1.00 | 1 | 0 | `mcp` | full |
| 609 | Workflow parallelism | 1.00 | 1 | 0 | `command` | workaround |
| 610 | archive-decrypt | 1.00 | 1 | 0 | `mcp` | full |
| 611 | davinci-control | 1.00 | 1 | 0 | `mixed` | full |
| 612 | prompt-trust | 1.00 | 1 | 0 | `mixed` | workaround |
| 613 | telegram-admin | 1.00 | 1 | 0 | `mcp` | full |
| 614 | vault-secret | 1.00 | 1 | 0 | `mixed` | workaround |
| 615 | warning-surface | 1.00 | 1 | 0 | `settings-hook` | workaround |

## Function hooks and demand by layer

133 canonical plugins require experimental function hooks as their primary layer and cannot enter the official directory. Highest-demand examples: Keyboard and input controls (1,854.75), Voice companion (1,719.25), Theme and layout controls (1,434.25), Output layout and formatting (1,062.50), Model and effort router (1,057.00), Compaction policy controller (933.00), Agent model and effort router (641.75), MCP access profiles (598.00).

| Primary layer | Plugins | Demand | Issues |
| --- | ---: | ---: | ---: |
| `mixed` | 272 | 50,528.00 | 9,377 |
| `function-hooks` | 133 | 16,023.25 | 2,868 |
| `mcp` | 101 | 7,391.75 | 1,170 |
| `settings-hook` | 33 | 6,433.50 | 1,247 |
| `bin` | 19 | 1,351.25 | 205 |
| `command` | 22 | 905.75 | 194 |
| `monitor` | 8 | 515.25 | 104 |
| `skill` | 21 | 247.25 | 61 |
| `agent` | 4 | 114.25 | 35 |
| `statusline` | 1 | 16.50 | 10 |
| `output-style` | 1 | 1.00 | 1 |
