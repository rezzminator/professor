# pcm-guard

`pcm-guard` is the machine-global `PreToolUse` hook on the edit tools and `Bash` that holds one rule at the call instead of in the prompt: only `pcm` writes a prompt file, and only `themer` writes a theme. It is [git-guard](git-guard.md)'s twin for prompt files. Body: `pfm/internal/hookentry/pcm_guard.go`. The fleet's other hooks, the ownership rule and the `pfm doctor` check that proves each hook is installed live in [hooks.md](hooks.md); there `pcm-guard` is one row of the inventory.

Decisions live in this file. A change lands here first, then in the code, then in every surface under [Surfaces that stay in sync](#surfaces-that-stay-in-sync).

## Contents

- [Why](#why)
- [Where it runs](#where-it-runs)
- [Who is exempt](#who-is-exempt)
- [What it protects](#what-it-protects)
- [How it reads a command](#how-it-reads-a-command)
- [What it blocks](#what-it-blocks)
- [The deny](#the-deny)
- [How it fails](#how-it-fails)
- [Named gaps](#named-gaps)
- [Tests](#tests)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## Why

The user's law: "Rules must be pcm protected everywhere, like .claude/* and CLAUDE.md. Only pcm can touch them." Their decision: "Make it a block hook".

A live test proved prose is not enough. A fresh chat in a plugin checkout edited its `CLAUDE.md` with `sed -i` through Bash and ignored both the `CLAUDE.md` routing line and a path-scoped rule; a path-scoped rule never loads on a Bash write.

An earlier guard (`pcm-guard.sh`, renamed `pfm-guard.sh`, shipped from v0.40.0 to v0.66.0; `pfm-guard.sh` and its `guard-stamp.sh` are retired names in `pfm/internal/update/retired_names.go`) opened its gate with session marker files. It paid for that state in marker-theft fixes, sandboxed stamps that failed silently, and deny/re-stamp loops (`releases/v0.43.0.md`, `v0.44.0.md`, `v0.48.0.md`). This guard keeps no state: it exempts by the hook payload's exact `agent_type`, as git-guard exempts `gitter`.

## Where it runs

`claudelaunch.HookTemplates` names it — event `PreToolUse`, matcher `Edit|Write|MultiEdit|NotebookEdit|Bash` (`claudelaunch.HookPcmGuardMatcher`), command `pfm internal pcm-guard` (`pfm/internal/claudelaunch/hooks.go`) — and `claudelaunch.Render` carries it in the launch-line `--settings` of every Claude chat a pfm door starts ([hooks.md](hooks.md)); `pfm doctor` spawn-audit expects it in each live chat's payload like every pfm hook. It is an internal verb (`internalSubcommands` and the `pcm-guard` branch of `runInternal`, `pfm/cmd/pfm/main.go`). Because it rides every launch and not one project's settings, it runs in every repository, whether pfm manages it or not.

Codex and OpenCode carry no equivalent (see [Named gaps](#named-gaps)).

## Who is exempt

The payload's `agent_type`, compared exactly, decides; the guard keeps no file, marker or session state.

| Caller | Writes |
| --- | --- |
| `pcm` | every protected path except `templates/themes/**` |
| `themer` | `templates/themes/**` only |
| anyone else: the main chat (no `agent_type`), `general-purpose` told to act as pcm, `PCM`, `pcm2`, every other agent | none |

Any agent registered as `pcm` qualifies: a project's own `.claude/agents/pcm.md`, scaffolded from `templates/project/agents/pcm.md`. A project with no pcm cannot write its prompt files from a chat; the deny sends the user to the project that owns them.

## What it protects

Each target is made absolute (a relative path joins the payload's `cwd`, `~` is `HOME`) and judged twice: as written, and with its symlinks resolved through the deepest existing ancestor (`paths.PhysicalPath`). It is protected when either form is (`pcmGuardScope.class`):

1. **Any file named `CLAUDE.md`**, letter case folded.
2. **The Professor source clone's `templates/**`**, and the same tree in each flight worktree under the clone's `.worktrees/{flight}/`. The clone is the one `pfm install` links global templates from, found the way pfm finds it: `paths.GlobalSourceRepo` (the recorded source-repo marker, else `$HOME/.professor`). `templates/themes/**` is themer's.
3. **Inside a Claude config dir** — `~/.claude` and the chat's `CLAUDE_CONFIG_DIR`, each as written and physical — only `agents/`, `commands/`, `rules/` and `skills/`; `settings.json`, `user-info.md`, `projects/*/memory/**`, `plans/` and the rest stay writable. Another seat's config dir links its prompt directories into `~/.claude`, so its writes resolve into rule 3 or 2.
4. **Anywhere else, any path with a `.claude` element**: a project's `.claude/` and everything under it.

A symlink such as `~/.claude/rules/x.md` resolves into the clone's `templates/global/rules/`, so it is protected by rule 3 as written and by rule 2 resolved.

## How it reads a command

- **Edit tools.** `Edit`, `Write`, `MultiEdit` and `NotebookEdit` are judged by `tool_input.file_path` (`notebook_path` for `NotebookEdit`). Every other tool, `Read` included, returns at once.
- **Bash.** The command goes through the shell parser (`pfm/internal/cmdparse`) exactly as git-guard's does: wrappers unwrapped (`sudo`, `timeout`, `env`), `&&`, `;`, pipes, subshells and loops followed, literal variables resolved with `HOME` seeded, an inner `bash -c` / `sh -c` string parsed into parts of its own. Each part carries its `Redirs` (`cmdparse.Redirect`): its own redirections, then those of each compound command around it, so `{ echo a; } > f` and `for …; done >> f` reach every part inside; a redirection with no command (`> f`) is a part with no program.
- **The directory.** `cd`, `pushd` and `popd` move the directory a later relative word joins. The parts carry no subshell or inner-shell bounds, so a relative word is judged in every directory the call may stand in: the cwd and each directory a `cd`, `pushd` or `popd` before it may have moved to (`(cd /tmp); echo x > templates/a.md` run in the clone is judged in both). `cd` alone is `HOME`; `cd -`, a `popd` or a word the parser cannot know adds an unknown directory; a bare `pushd` adds none. Past 32 directories the command is unreadable.
- **Unknown words.** A word holding an expansion the parser cannot know keeps its literal tail after the last expansion on an unknown root, so `"$DIR/CLAUDE.md"` is still a `CLAUDE.md`; a word with no such tail (`"$F"`) is unreadable (see [How it fails](#how-it-fails)).
- **Globs.** A glob is expanded against the filesystem, or kept as written when it matches nothing, as the shell does; Go's matching lets `*` match a leading dot, so `rm -rf *` beside a `.claude/` counts it.

## What it blocks

| Form | Write targets |
| --- | --- |
| redirection | the target of `>`, `>>`, `>\|`, `<>`, `&>`, `&>>`, `n>` and `>&` to a file name (`>&2` is a descriptor) |
| `tee` | every operand |
| `sed -i`, `perl -i` | the file operands; `-i` alone, with a suffix, in a short cluster (`-ni`, `-pi`) or `--in-place[=suffix]`; sed's script operand is skipped when no `-e`/`-f` names it |
| `cp`, `mv`, `install`, `rsync` | the destination, its glob expanded, or each source's name inside it when it is a directory (`-t` alone or joined, `--target-directory`, a trailing `/`, an existing directory); `mv` also each source it removes |
| `ln` | the link name: the last operand, or the target's name in the directory |
| `rm`, `unlink`, `touch`, `truncate` | every operand |
| `dd` | `of=` |
| an interpreter's inline code | python (`-c`), node (`-e`, `-p`, `--eval`, `--print`), perl (`-e`, `-E`), ruby (`-e`), `dash` and `ksh` (`-c`, whose string the parser does not read), the code the next word or joined to its flag (`-c'…'`), and a heredoc or here-string fed to any of them or to `sh`, `bash`, `zsh`, `dash`, `ksh`: every path-shaped token of the code (holding a `/`, named `CLAUDE.md`, or naming an existing file) is judged as a write |
| the edit tools | `file_path`, `notebook_path` |

Never denied: a read (`Read`, `cat`, `grep`, `sed -n`, `head`, `diff`, `cp CLAUDE.md /tmp/x`), heredoc text fed to a non-interpreter (`cat > notes.md <<EOF` naming `CLAUDE.md`), a script file run with a protected path as its argument (`python3 check.py CLAUDE.md`), and every `git` argument: git commands are git-guard's. A `git` part's redirections are shell writes and are judged (`git show HEAD:CLAUDE.md > CLAUDE.md`).

Known costs, each a deliberate deny:

- Interpreter inline code is judged by the paths it names, not by what it does, so a read-only snippet naming a prompt file (`python3 -c "print(open('CLAUDE.md').read())"`) is denied too: the cost of reading code without its language's parser.
- A project's whole `.claude/` is protected, `settings.json` and `settings.local.json` included, so a permission or hook change there goes through pcm as well.
- A checkout placed under a `.claude/` directory (a tool that keeps worktrees in `{repo}/.claude/worktrees/`) sits under a `.claude` element, so every write inside it is denied.

## The deny

One message, one line per writer (`pcmGuardJudge.reason`):

`pcm-guard: Only pcm writes {paths}: spawn Agent(subagent_type: "pcm") with the exact change; a sub-agent returns the change as a proposal; no pcm registered here → ask the user to dispatch pcm from the project that owns the file.`

A theme path gives the same line naming `themer`. An unreadable command adds `pcm-guard could not read this command, and it names a prompt file (CLAUDE.md, .claude, templates): split it into simple commands with literal paths; a prompt-file write goes to pcm: spawn Agent(subagent_type: "pcm") with the exact change.` Each path is named as the call wrote it, made absolute; a glob is named by each file it matched.

## How it fails

git-guard's fail mode ([git-guard.md](git-guard.md#how-it-fails)), with prompt-file names in place of `git`:

- **Payload unreadable or undecodable:** one `pfm internal pcm-guard: … (fail-open)` stderr line, the call allowed, like every pfm hook.
- **The parser itself errors:** fail-open the same way; a parser failure must never freeze every Bash call on the machine.
- **A part does not parse, a parse bound cut the command short (over 64 KiB, or a `-c` string nested past 8 levels), or a write target is an expansion with no literal tail:** denied with the unreadable line when the command names a prompt file — it holds `CLAUDE.md`, `.claude` or `templates`, letter case folded (`pcmGuardMentions`) — and allowed otherwise. For `pcm` the test is `themes` alone, since only a theme is closed to it.
- **`HOME` is empty:** `~/.claude` and the clone cannot be placed; one stderr line names it, and `CLAUDE.md` and `.claude` paths are still judged by name.
- **The clone cannot be resolved** (an unreadable source-repo marker): one stderr line, and `templates/**` is unguarded while every other rule holds.
- **The deny cannot be written:** stderr line and exit 1.

## Named gaps

- **Codex and OpenCode.** No pcm-guard hook rides either engine; there the guard is prose alone, an always-on one-line rule (`professor.md`, which pcm owns and has not yet written).
- **git writes.** git arguments are git-guard's, and git-guard allows working-tree restores and `git apply` to the working tree, so `git checkout -- CLAUDE.md` and `git apply x.patch` touching a prompt file pass both guards.
- **Directory copies.** `cp -r dir/ dest` or `rsync -a dir/ dest/` is judged by the destination and the source's own name; a `CLAUDE.md` or `.claude/` inside the copied tree is not seen. Archive extraction (`tar x`, `unzip`) is not read at all.
- **Recursive removal of an ancestor.** `rm -rf {project}` deletes the project's `CLAUDE.md` but names no protected path; only the paths written are judged.
- **Programs not in the table.** `find -delete`, `find -exec`, `xargs` with operands from stdin, `chmod`, `patch`, an editor (`vim`, `ed`) and any other program writing a file it names are not read as writes.
- **A script file.** `python3 tool.py` writing a prompt file from inside the script is not seen; only inline code is read.
- **Code piped in.** Code reaching a shell or interpreter through a pipe (`echo 'rm CLAUDE.md' | sh`, `cat <<'EOF' | python3`) is not read; only its own heredoc, here-string or code flag is.
- **Paths built at run time.** Code that assembles a path from pieces (`os.path.join(root, ".claude", "agents")` with no existing `.claude` beside it) and a variable the parser cannot resolve whose literal tail names nothing protected pass.
- **A protected path behind an unknown directory.** After `cd "$X"`, a relative word stands on an unknown root, so only a `CLAUDE.md` or `.claude` name in it is judged, never `templates/`.
- **A symlink created then written in one call.** Symlinks are resolved as they stand when the hook runs.
- **Every parser gap of git-guard's** ([git-guard.md](git-guard.md#named-gaps)): unknown wrappers, ANSI-C quoting, loop variables, unsplit values, command substitution as the program.

## Tests

- Unit: `pfm/internal/hookentry/pcm_guard_test.go` — every edit tool denied to the main chat, `general-purpose`, `flights-foreman`, `PCM`, `pcm2` and `themer` on each protected path (project `CLAUDE.md` and `.claude/`, the clone's and a flight worktree's `templates/`, the store's and the seat's prompt directories); every Bash write form above denied to the main chat and `general-purpose`; `pcm` allowed every one of them; a theme denied to `pcm` and everyone but `themer`, directly and through a link, and `themer` denied every other prompt file; `~/.claude/rules/x.md`, the seat's `rules/x.md` and a project link resolving into the clone denied, and a recorded clone elsewhere moving the guard with it; reads, git commands, unrelated paths, `~/.claude/settings.json`, `user-info.md`, auto-memory and the clone's `docs/` allowed in silence; an unreadable command, an unresolvable target and a parse bound denied when they name a prompt file and allowed when not; a malformed payload failing open loudly; an empty `HOME` still judging by name and saying so.
- Parser: `pfm/internal/cmdparse/cmdparse_test.go` `TestRedirectsRideTheirParts` — a statement's, a compound command's and a bare redirection's `Redirs`, and a heredoc body.
- Registration: `pfm/internal/claudelaunch/hooks_test.go` `TestPcmGuardRidesEveryWritingTool` and `pfm/internal/installer/expected_hooks_test.go` `TestClaudeHookTemplatesIncludesPcmGuard` pin exactly one `PreToolUse` template running `pfm internal pcm-guard` on `Edit|Write|MultiEdit|NotebookEdit|Bash`; `pfm/testdata/golden/cmdlines.txt` carries it in every launch line.
- Fence: lane `O2`, beat `O2.05-internal-plumbing` (`infra/fence/lanes/O2.sh`) — a main-chat `CLAUDE.md` write denied naming pcm, the same write passed for pcm, a `cat CLAUDE.md` passed; `O1.sh` checks the verb is in the launch templates.

## Surfaces that stay in sync

- [hooks.md](hooks.md) — the inventory row, the count and the fail-open decision.
- `docs/dev/pfm-surface.md` — the `internal` row.
- `README.md` — the rules-that-bite paragraph.
- `.claude/agents/pcm.md` and its twin `templates/project/agents/pcm.md`, `.claude/agents/themer.md` — the writers the deny sends every caller to.
- `pfm/internal/update/retired_names.go` — the retired stamp-gated guard the comment there contrasts with this one.
