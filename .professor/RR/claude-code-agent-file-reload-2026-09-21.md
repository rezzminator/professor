# RR — When does Claude Code read `.claude/agents/*.md`, and what happens on mid-session edits?

Question: Claude Code custom sub-agents (`.claude/agents/*.md`) — when is an agent definition file read: at session start only, or again on each spawn? What happens to a session already running when the file is edited, created or deleted mid-session (body, `tools:`, `model:`), and what do Anthropic's docs, changelog and GitHub issues say about reloading the agent roster (`/agents`, restart requirements, caching of a broken frontmatter parse)?

Neither: agent files are read at session start **and** re-read by a filesystem watcher, so an edit or an added file in an already-watched directory reaches the *next* delegation within seconds with no restart — subject to three documented exceptions (a newly created `agents` directory, `--add-dir` scopes, `--disable-slash-commands`), plus a live open bug in which an edited `tools:` list is never picked up even after a full process restart.

## 1. When the file is read

Anthropic documents a watcher, not a start-only load: "Claude Code watches `~/.claude/agents/` and `.claude/agents/`. When you add or edit a subagent file on disk, or ask Claude to write one for you, Claude Code detects the change within a few seconds and the next delegation uses the updated definition, with no restart needed." ([Claude Code docs — Create custom subagents](https://code.claude.com/docs/en/sub-agents))

The unit of refresh is the *next delegation*, not the spawn-in-flight. The docs say nothing about whether a subagent already mid-task is affected by an edit to its own definition — `open`.

## 2. The three documented restart cases

Verbatim, all from the same page ([docs](https://code.claude.com/docs/en/sub-agents)), under "Three cases still need a restart":

- "The watcher covers only directories that existed when the session started, so after creating a scope's first agent file in a new `agents` directory, restart to load it."
- "Claude Code doesn't watch `.claude/agents/` inside directories added with `--add-dir` or `/add-dir`, so after adding or editing a subagent there, restart to load the change."
- "Sessions started with `--disable-slash-commands` don't watch these directories at all."

A separate rule governs plugin-provided agents: "changes to `hooks/`, `.mcp.json`, `agents/`, and `output-styles/` need `/reload-plugins` to take effect" ([slash-commands docs](https://code.claude.com/docs/en/slash-commands), relayed by a digger — `unquoted` by my own verification pass).

## 3. Edit, create, delete — by mutation

- **Body / prompt edit**: hot-reloaded per the watcher sentence above.
- **Create**: hot-reloaded if the `agents` directory already existed at session start; otherwise restart.
- **Delete**: the docs are **silent** — verified: the page "is entirely silent on deleting an agent file or how the watcher handles file removals. It discusses additions and edits only." Whether a live session drops the roster entry, errors on spawn, or keeps a cached definition is `open`. The only adjacent evidence is [issue #41415](https://github.com/anthropics/claude-code/issues/41415) (closed as not planned, versions 2.1.87/2.1.88), where Claude Code's own node process bulk-deleted user agent files: "Claude Code's node process is silently deleting all user-managed files from `~/.claude/agents/`" — after which spawns failed.
- **`tools:` edit — DISPUTED against the docs**: [issue #95357](https://github.com/anthropics/claude-code/issues/95357) (**open**, Claude Code 2.1.276, Windows 11, opened Sep 18 2026) reports "Adding a tool to an existing subagent's `tools:` frontmatter (`~/.claude/agents/<name>.md`) is not applied to subagents dispatched via the `Agent`/`Task` tool — not in the same session, not in a brand-new session, and not even after a full kill-and-relaunch of the Claude Code process." The top-level "Available agent types" listing showed the new tool; the dispatched subagent's list "is exactly the _pre-edit_ six". No maintainer reply. This is the sharpest live contradiction of the hot-reload claim; a single report, uncorroborated.
- **`model:` edit**: the docs give a four-step precedence — "1. The per-invocation `model` parameter 2. The subagent definition's `model` frontmatter, where `inherit` selects the main conversation's model 3. The `CLAUDE_CODE_SUBAGENT_MODEL` environment variable... 4. The main conversation's model" ([docs](https://code.claude.com/docs/en/sub-agents)). DISPUTED by [issue #44385](https://github.com/anthropics/claude-code/issues/44385) (closed as duplicate, Apr 6 2026): "the `model:` field in agent `.md` frontmatter is completely ignored. Subagents always inherit the parent model unless `model` is explicitly passed in the Agent tool call." No duplicate target visible; whether the docs postdate a fix is unresolved.

## 4. `/agents` and the roster surface

"As of v2.1.198, the `/agents` command no longer opens the interactive creation wizard; running it prints a reminder to ask Claude or edit `.claude/agents/` directly." Before that: "On Claude Code v2.1.197 and earlier, `/agents` opens an interactive wizard with a **Running** tab that lists live subagents and a **Library** tab for creating, editing, and deleting them." ([docs](https://code.claude.com/docs/en/sub-agents)) The release notes carry the same change: "Removed the `/agents` wizard; ask Claude to create or manage subagents, or edit `.claude/agents/` directly" ([v2.1.198 release](https://github.com/anthropics/claude-code/releases/tag/v2.1.198), relayed by a digger).

There is **no** `/reload` or `/agents reload` command for the agent roster; reload is passive. The hot-reload feature requests are all closed as duplicates and all predate the documented watcher: [#22050](https://github.com/anthropics/claude-code/issues/22050) (Jan 30 2026, "changes only take effect after restarting Claude Code"), [#23608](https://github.com/anthropics/claude-code/issues/23608) (Feb 6 2026, v2.1.34), [#29202](https://github.com/anthropics/claude-code/issues/29202) (Feb 27 2026, "We already have hot-reloading skills, but why not for agents as well?"), and the older [#5738](https://github.com/anthropics/claude-code/issues/5738) (Aug 14 2025, closed): "New agents created in `.claude/agents/` directory are not automatically loaded by Claude Code, requiring users to restart the entire session."

## 5. Broken frontmatter: silent skip, debug log, and a cached parse error

Required fields: "Only `name` and `description` are required." Failure is **silent in-session**: Claude Code skips such a file "without reporting it in the session", and specifically — "**YAML that doesn't parse**: Claude Code reads no fields from the file, skips it, and writes the parse error to the debug log", "**A `name` but no `description`**: Claude Code skips the file and writes the reason to the debug log." The offered diagnostic: "To find files in an `agents` directory whose frontmatter doesn't parse, run `claude plugin validate` against the directory... Requires Claude Code v2.1.233 or later." ([docs](https://code.claude.com/docs/en/sub-agents))

Silent skips are confirmed in the wild: [#50522](https://github.com/anthropics/claude-code/issues/50522) ("When the description attribute is missing from an agent's definition frontmatter, it's silently ignored", closed not planned) and [#86748](https://github.com/anthropics/claude-code/issues/86748) (an unquoted `description` containing `:` → "this agent loads with empty metadata (all frontmatter fields silently dropped)... with no error surfaced unless they run `claude plugin validate` manually").

**A broken parse is cached until restart** — [issue #17127](https://github.com/anthropics/claude-code/issues/17127) (opened Jan 9 2026, closed as duplicate): "When editing files in `.claude/` (agents, settings, hooks), changes are not reflected until the session is exited and restarted", with the repro "1. Edit `.claude/agents/my-agent.md` to fix a frontmatter error 2. Run `/doctor` - still shows the old error (cached)" and "The fix was applied to disk but `/doctor` continued reporting the cached (stale) error until session restart." Whether this survives the current watcher is unresolved — the issue predates v2.1.198 and was closed as a duplicate with no target named.

## 6. Older discovery failures (historical context)

Four reports of custom agents never loading at all, none with an Anthropic reply: [#11205](https://github.com/anthropics/claude-code/issues/11205) (v2.0.35, Termux, open — "Custom agents in `~/.claude/agents/` are **completely ignored**"), [#20931](https://github.com/anthropics/claude-code/issues/20931) (v2.1.19, closed as duplicate), [#59881](https://github.com/anthropics/claude-code/issues/59881) (v2.1.143, macOS, closed as not planned), [#8256](https://github.com/anthropics/claude-code/issues/8256) (v1.0.127, Windows, closed). These are discovery bugs, not reload bugs.

## Coverage

| Sub-area | Status |
| --- | --- |
| Read timing (start vs per-spawn vs watcher) | settled |
| Mid-session edit / create | settled |
| Mid-session delete | open — docs silent, no authoritative source |
| `tools:` / `model:` frontmatter mutation | partial — both DISPUTED against docs by single, unanswered issues |
| `/agents` + reload history | settled |
| Broken frontmatter parse + its caching | partial — skip behavior settled; whether #17127's cached-parse-until-restart still holds post-watcher is open |
| Changelog origin of the watcher (added sub-area) | open — no version pins when file-watching first shipped |

## Verification

13 facts checked across 2 source pages. All confirmed with quotes except: the four-step `model:` precedence, where the verifier answered NO but quoted the identical four-step list — treated as **confirmed by its own quote**, flagged here rather than silently. The `/reload-plugins` sentence and the v2.1.198 release-note wording are `unquoted` by my pass (digger-sourced only). Nothing was NOT ON PAGE; nothing was UNCHECKED.

## Rabbit holes left open

- The CHANGELOG version where the `~/.claude/agents/` watcher first shipped — the digger could not quote it; the raw `CHANGELOG.md` on `main` is ~746KB and truncated before the relevant range.
- The duplicate targets of #22050, #23608, #29202, #17127 and #44385 — none rendered a linked target, so no fix commit or version can be named.
- Whether a compiled agent registry or on-disk index exists that holds the stale `tools:` list of #95357 — the reporter's own open question.
- Deletion and rename semantics of the watcher, including inode-vs-path tracking (an unlink+recreate, as a dotfile symlink manager does).
- Whether the watcher covers nested subdirectories under `agents/` and plugin-provided agents.
- CRLF, BOM and wrong-extension frontmatter failure modes — searched, nothing found (a genuine empty result, not a failed lookup).
