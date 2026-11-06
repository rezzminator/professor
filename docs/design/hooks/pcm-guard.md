# pcm-guard

`pcm-guard` is the machine-global `PreToolUse` hook on the edit tools and `Bash` that holds one rule at the call instead of in the prompt: only `pcm` writes a prompt file, and only `themer` writes a theme. It is [git-guard](git-guard.md)'s twin for prompt files. Body: `pfm/internal/hookentry/pcm_guard.go`; the programs a part runs in `pcm_guard_code.go`, the reading of code text in `pcm_guard_scan.go`. The fleet's other hooks, the ownership rule and the `pfm doctor` check that proves each hook is installed live in [hooks.md](hooks.md); there `pcm-guard` is one row of the inventory.

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

An earlier guard (`pcm-guard.sh`, renamed `pfm-guard.sh`, shipped from v0.40.0 to v0.66.0, with its `guard-stamp.sh`) opened its gate with session marker files. It paid for that state in marker-theft fixes, sandboxed stamps that failed silently, and deny/re-stamp loops (`releases/v0.43.0.md`, `v0.44.0.md`, `v0.48.0.md`). This guard keeps no state: it exempts by the hook payload's exact `agent_type`, as git-guard exempts `gitter`.

## Where it runs

`claudelaunch.HookTemplates` names it — event `PreToolUse`, matcher `Edit|Write|MultiEdit|NotebookEdit|Bash` (`claudelaunch.HookPcmGuardMatcher`), command `pfm internal pcm-guard` (`pfm/internal/claudelaunch/hooks.go`) — and `claudelaunch.Render` carries it in the launch-line `--settings` of every Claude chat a pfm door starts ([hooks.md](hooks.md)); `pfm doctor` spawn-audit expects it in each live chat's payload like every pfm hook. It is an internal verb (`internalSubcommands` and the `pcm-guard` branch of `runInternal`, `pfm/cmd/pfm/main.go`). Because it rides every launch and not one project's settings, it runs in every repository, whether pfm manages it or not.

Codex and OpenCode carry no pcm-guard: their [engine-guard](engine-guard.md) denies every framework-file write there, to every caller (see [Named gaps](#named-gaps)).

## Who is exempt

The payload's `agent_type`, compared exactly, decides; the guard keeps no file, marker or session state.

| Caller | Writes |
| --- | --- |
| `pcm` | every protected path except `templates/themes/**` |
| `themer` | `templates/themes/**` only |
| an agent a project grant names | the directories under that project's `.claude/` the grant lists |
| anyone else: the main chat (no `agent_type`), `general-purpose` told to act as pcm, `PCM`, `pcm2`, every other agent | none |

A project grant is `{project}/.claude/pcm-guard.json`, `{"writers": {"{agent_type}": [".claude/{dir}", …]}}`, each directory relative to the project and strictly under its `.claude/` (`pcmGuardJudge.granted`, `pfm/internal/hookentry/pcm_guard_grant.go`). The file lies under `.claude/`, so only pcm writes it. A write is granted when each form of its target (as written, physical) is either unprotected or under a granted directory of the project at its last `.claude` element, matched letter case exact. A grant never covers a `CLAUDE.md`, the clone's `templates/`, a Claude config dir (whose own files anyone may write), the grant file itself, the main chat, or an engine caller.

Any agent registered as `pcm` qualifies. pcm is machine-global: `templates/global/agents/pcm.md`, linked into `~/.claude/agents/` by `pfm install`; a project's own `.claude/agents/pcm.md` overrides it (Claude Code: a project agent wins over a user agent). The guard never reads either file, only the payload's `agent_type`.

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
- **Programs.** A part running an interpreter (`python*`, `node`, `perl`, `ruby`) or a shell (`sh`, `bash`, `zsh`, `dash`, `ksh`) has its program read (`pcmGuardJudge.program`): its inline code (python `-c`, node `-e`, `-p`, `--eval`, `--print`, perl `-e`, `-E`, ruby `-e`, alone, in a short cluster or joined: `-c'…'`), and a heredoc or here-string fed to it when that is its program. Stdin is the program when there is no program operand, the operand is `-`, a shell runs `-s`, an inline code flag is present (its stdin stays code, so `python3 -c "exec(sys.stdin.read())" <<EOF` is read), or an option the per-interpreter table (`pcmGuardOptionTable`) does not know comes first. It is data when the first operand after the options names an existing regular file, resolved like any word in each directory, or python runs `-m` (`pcmGuardJudge.stdinIsCode`): `python3 tool.py write <<'EOF'` naming prompt files passes. A `bash`, `sh`, `zsh`, `dash` or `ksh -c` string is the parser's own.
- **Shell code.** A heredoc or here-string that is a shell's program is judged as a Bash command run in that directory, through the same parser and rules, one level deeper (`pcmGuardJudge.shell`); past 8 levels the command is unreadable. So `bash <<'EOF'` running `cat CLAUDE.md` passes and one running `rm CLAUDE.md` is denied. A here-string holding `$` or `` ` `` may be source text the parser could not expand, so it is read by the paths it names instead, as before.
- **Interpreter code** is judged by its writes (`pcmGuardJudge.interpret`, `pcmGuardWrites`): with no write site and no escape site, nothing is judged (a read passes); when every write site names a target it can read, exactly those targets are judged; when any write site's target cannot be read, or any escape site is present, every path-shaped token of every piece of the part's code is judged (holding a `/`, named `CLAUDE.md`, or naming an existing file), the reading before this one, so no deny that reading gave is lost. A literal target with `$` or `` ` `` is read as the shell's expansion would be, since an unquoted heredoc expands first.
  - Write sites: python `open`, `io.open`, `codecs.open`, `gzip`/`bz2`/`lzma`/`tarfile.open`, `zipfile.ZipFile`, `io.FileIO` with a mode holding `w`, `a`, `x` or `+` (positional or `mode=`; a mode that is not a literal cannot be read); `Path.open` likewise, unless its first argument is a literal no mode spells (`Image.open('x.png')` reads like the builtin `open`); the Path methods `write_text write_bytes touch unlink rmdir mkdir symlink_to hardlink_to chmod lchmod` on their receiver, and `rename replace` only when the code names `Path` or `pathlib` (`str.replace` is the everyday read); `os.remove unlink rmdir removedirs mkdir makedirs mkfifo mknod rename renames replace link symlink truncate chmod lchmod chown lchown chflags lchflags utime`; `shutil.copy copy2 copyfile copytree copymode copystat move rmtree chown`. node's fs `writeFile appendFile rm unlink rmdir mkdir mkdtemp rename copyFile cp symlink link truncate chmod chown lchown lchmod utimes lutimes` and their `Sync` forms, `createWriteStream` and `WriteStream`, and `open`/`openSync` with a flag holding `w`, `a` or `+`. perl `open` with a `>`, `>>`, `+<` or `+>` mode (2- or 3-arg), `unlink rename mkdir rmdir symlink link truncate`, File::Copy `copy move cp mv`, File::Path `rmtree make_path remove_tree mkpath`. ruby `File.write binwrite truncate delete unlink rename symlink link`, `File.open`/`File.new`/`Kernel#open` with a `w`, `a` or `+` mode, `IO.write binwrite copy_stream`, `FileUtils.*`, `Dir.mkdir rmdir delete unlink`, each called with `.` or `::`. A write name not called (`o = open`, `{writeFileSync}`) is a site whose target cannot be read; a write naming several paths judges every one it writes: both of a rename or move, only the destination of a copy or link (`os.link`, `os.symlink`, `shutil.copy*`), and for `shutil.copy`/`copy2` into a directory (one ending in `/` or existing) the source's name inside it.
  - Escape sites (`pcmGuardEscapes`): python `subprocess`, `os.system popen exec* spawn* posix_spawn* open fdopen fork* chdir fchdir chroot`, `pty`, `exec(`, `eval(`, `compile(`, `__import__`, `importlib`, `getattr`/`setattr`/`globals`/`locals`/`vars`, `builtins`, `__dict__`, `sys.modules`, `fileinput`, `ctypes`, `runpy`, `sqlite3`, `dbm`, `shelve`, `tempfile`, `logging`'s `FileHandler`/`basicConfig`, `operator` (`methodcaller`, `attrgetter`), archive extraction (`unpack_archive`, `make_archive`, `extractall`, `.extract(`), a call through an index (`x[k](…)`), a `from os|shutil|io|… import` or `import os|shutil|… as`, and such a module used as a value (`x = os`); node `child_process`, `exec*`, `spawn*`, `fork*`, `process.chdir`, `eval(`, `Function(`, `vm`, `import(`, a `require` of a non-literal, `process.binding`, `Reflect`, `globalThis`, `constructor`, a call through an index; perl `system`, `exec`, backticks, `qx`, `eval`, `sysopen`, `readpipe`, `pipe`, `chdir`, `chmod`, `chown`, `utime`, in-place edit (`$^I`, `INPLACE_EDIT`), `Archive::`, a pipe `open`, `->(`, `&$f`, `*{…}`, `do FILE`, an `/e` substitution, `Path::Tiny`, `File::Slurp`, `IO::File`, `File::Temp`, `Tie::File`; ruby `system`, `exec`, `spawn`, backticks, `%x`, `IO.popen`, `Open3`, `chdir`, `reopen`, `sysopen`, `chmod`, `chown`, `lchmod`, `lchown`, `utime`, `lutime`, `mkfifo`, `eval` and its kin, `send`, `method`, `const_get`, `.call`, `load`, `Pathname`, `include FileUtils`, and `File`/`IO`/`Dir`/`FileUtils` used as a value.
  - A target it can read: one plain string literal, with no interpolation (an f-string, JS `${`, perl's `"$…"`/`"@…"`, ruby's `"#…"`) and no escape; in python also `Path(…)`/`pathlib.Path(…)` and `os.path.join(…)` over such values, and a name every binding of which is a plain `name = value` to one, judged as each of those values in turn; a copy or link call keeps each argument's values apart, so its destination stays the second argument's. Every way python binds a name counts toward that check (assignment, augmented, tuple, `for`, `as`, `def`, `lambda`, `import`, `global`, `:=`, `case`), counted on the statements, on the statements with their bracketed newlines flattened (a `def f(\n p):` parameter) and on the raw text, so a binding is over-counted, never missed. node, perl and ruby read literals only.

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
| a shell's program | a heredoc or here-string that is its program: every write above, read as a Bash command (see [How it reads a command](#how-it-reads-a-command)) |
| an interpreter's program | python, node, perl and ruby inline code, and a heredoc or here-string that is the program: the target of each write site; every path-shaped token when a target cannot be read or an escape site is present (see [How it reads a command](#how-it-reads-a-command)) |
| the edit tools | `file_path`, `notebook_path` |

Never denied: a read (`Read`, `cat`, `grep`, `sed -n`, `head`, `diff`, `cp CLAUDE.md /tmp/x`), heredoc text fed to a non-interpreter (`cat > notes.md <<EOF` naming `CLAUDE.md`), a heredoc that is a script's data (`python3 tool.py write <<EOF` naming `CLAUDE.md`), interpreter or shell code that only reads (`python3 -c "print(open('CLAUDE.md').read())"`, `bash <<EOF` running `cat CLAUDE.md`), interpreter code writing a literal path outside the prompt files whatever its text names (`open('/tmp/x/review.md','w').write(s)` after `s.replace(…, 'CLAUDE.md')`), a script file run with a protected path as its argument (`python3 check.py CLAUDE.md`), and every `git` argument: git commands are git-guard's. A `git` part's redirections are shell writes and are judged (`git show HEAD:CLAUDE.md > CLAUDE.md`).

Known costs, each a deliberate deny:

- Interpreter code with an escape site, or a write whose target is not a literal it can read, is judged by every path it names, so `subprocess.run(['cat', 'CLAUDE.md'])` or `open(sys.argv[1], 'w')` beside a prompt-file name is denied. In python, write and escape sites are found with comments and the text of every string but an f-string blanked (`pcmGuardPyMask`), so prose in a string (`'see [a](b)'`, a backticked path) is no call or escape; their arguments are still read from the raw code. node, perl and ruby are read raw.
- A project's whole `.claude/` is protected, `settings.json` and `settings.local.json` included, so a permission or hook change there goes through pcm as well.
- A checkout placed under a `.claude/` directory (a tool that keeps worktrees in `{repo}/.claude/worktrees/`) sits under a `.claude` element, so every write inside it is denied.

## The deny

One message, one line per writer (`pcmGuardJudge.reason`):

`pcm-guard: Only pcm writes {paths}: spawn Agent(subagent_type: "pcm") with the exact change; a sub-agent returns the change as a proposal; pcm is machine-global: no pcm registered means this host's install is stale → run pfm install --yes.`

A theme path gives the same line naming `themer`, with its own tail (themer stays Professor's project agent): `no themer registered here → ask the user to dispatch themer from the project that owns the file.` An unreadable command adds `pcm-guard could not read this command, and it names a prompt file (CLAUDE.md, .claude, templates): split it into simple commands with literal paths; a prompt-file write goes to pcm: spawn Agent(subagent_type: "pcm") with the exact change.` Each path is named as the call wrote it, made absolute; a glob is named by each file it matched.

## How it fails

git-guard's fail mode ([git-guard.md](git-guard.md#how-it-fails)), with prompt-file names in place of `git`:

- **Payload unreadable or undecodable:** one `pfm internal pcm-guard: … (fail-open)` stderr line, the call allowed, like every pfm hook.
- **The parser itself errors:** fail-open the same way; a parser failure must never freeze every Bash call on the machine.
- **A part does not parse, a parse bound cut the command short (over 64 KiB, a `-c` string nested past 8 levels, or a shell program nested past 8 levels), a `bash`, `sh` or `zsh -c` string holds an expansion the parser cannot know (`bash -c "rm CLAUDE.md $X"`, its part marked `cmdparse.Part.Unread`), or a write target is an expansion with no literal tail:** denied with the unreadable line when the command names a prompt file — anywhere in the whole command, `CLAUDE.md`, `.claude` or `templates` as a whole path element or word, letter case folded, a trailing dot dropped (`pcmGuardMentions`; `.claude-plugin`, `.claude.json` and `my_templates_x` name none) — and allowed otherwise. For `pcm` the test is the element `themes` alone, since only a theme is closed to it.
- **`HOME` is empty:** `~/.claude` and the clone cannot be placed; one stderr line names it, and `CLAUDE.md` and `.claude` paths are still judged by name.
- **The clone cannot be resolved** (an unreadable source-repo marker): one stderr line, and `templates/**` is unguarded while every other rule holds.
- **A grant file that is a symlink, unreadable or malformed:** it grants nothing (the deny stands), with one stderr line naming the file; **an entry not strictly under `.claude/`:** that entry grants nothing, with one stderr line naming the file and the entry, while the file's other entries stand; a missing grant file grants nothing in silence.
- **The deny cannot be written:** stderr line and exit 1.

## Named gaps

- **Codex and OpenCode.** No pcm-guard hook rides either engine, and no `pcm` or `themer` role writes framework files there: [engine-guard.md](engine-guard.md) denies every write to a prompt file or other framework file to every Codex and OpenCode caller, by the judge this guard shares.
- **git writes.** git arguments are git-guard's, and git-guard allows working-tree restores and `git apply` to the working tree, so `git checkout -- CLAUDE.md` and `git apply x.patch` touching a prompt file pass both guards.
- **Directory copies.** `cp -r dir/ dest` or `rsync -a dir/ dest/` is judged by the destination and the source's own name; a `CLAUDE.md` or `.claude/` inside the copied tree is not seen. Archive extraction (`tar x`, `unzip`) is not read at all.
- **Recursive removal of an ancestor.** `rm -rf {project}` deletes the project's `CLAUDE.md` but names no protected path; only the paths written are judged.
- **Programs not in the table.** `find -delete`, `find -exec`, `xargs` with operands from stdin, `chmod`, `patch`, an editor (`vim`, `ed`) and any other program writing a file it names are not read as writes, in a Bash command and in a shell program read from a heredoc alike.
- **A script file.** `python3 tool.py` writing a prompt file from inside the script is not seen; only inline code and a heredoc or here-string that is the program are read, and a heredoc fed to an existing script is its data.
- **Writes outside the tables.** Interpreter code writing through an API outside the write-site tables and naming no escape site (`df.to_csv('CLAUDE.md')`, a third-party library's writer) is judged as a read.
- **Code piped in.** Code reaching a shell or interpreter through a pipe (`echo 'rm CLAUDE.md' | sh`, `cat <<'EOF' | python3`) is not read; only its own heredoc, here-string or code flag is.
- **Paths built at run time.** Code whose write target cannot be read falls back to the paths it names, so one that assembles a path from pieces (`os.path.join(root, ".claude", "agents")` with no existing `.claude` beside it) and a variable the parser cannot resolve whose literal tail names nothing protected pass.
- **A protected path behind an unknown directory.** After `cd "$X"`, a relative word stands on an unknown root, so only a `CLAUDE.md` or `.claude` name in it is judged, never `templates/`.
- **A symlink created then written in one call.** Symlinks are resolved as they stand when the hook runs.
- **Every parser gap of git-guard's** ([git-guard.md](git-guard.md#named-gaps)): unknown wrappers, ANSI-C quoting, loop variables, unsplit values, command substitution as the program.

## Tests

- Unit: `pfm/internal/hookentry/pcm_guard_test.go` — every edit tool denied to the main chat, `general-purpose`, `flights-foreman`, `PCM`, `pcm2` and `themer` on each protected path (project `CLAUDE.md` and `.claude/`, the clone's and a flight worktree's `templates/`, the store's and the seat's prompt directories); every Bash write form above denied to the main chat and `general-purpose`; `pcm` allowed every one of them; a theme denied to `pcm` and everyone but `themer`, directly and through a link, and `themer` denied every other prompt file; `~/.claude/rules/x.md`, the seat's `rules/x.md` and a project link resolving into the clone denied, and a recorded clone elsewhere moving the guard with it; reads, git commands, unrelated paths, `~/.claude/settings.json`, `user-info.md`, auto-memory and the clone's `docs/` allowed in silence; an unreadable command, an unresolvable target, a parse bound and an inner `bash`, `sh` or `zsh -c` string the parser cannot expand denied when they name a prompt file and allowed when not; a malformed payload failing open loudly; an empty `HOME` still judging by name and saying so; a project grant admitting its agent to the granted directory by edit tool and Bash, and refusing it the skill's `SKILL.md`, a sibling directory, a `CLAUDE.md` inside the grant, the grant file and every other prompt file, while every other caller stays refused the granted directory.
- Unit: `pfm/internal/hookentry/pcm_guard_grant_test.go` — a missing grant file refusing in silence; a malformed or linked grant file and an entry that is `.claude` itself, escapes it, lies outside it or is absolute granting nothing and naming it; a link inside a granted directory resolving into the clone, a grant file in a Claude config dir or the clone's `templates/` and a Codex caller with the granted `agent_type` all refused.
- Unit: `pfm/internal/hookentry/pcm_guard_code_test.go` — admitted: a heredoc that is an existing script's data, interpreter and shell code that only reads, a `/tmp` write whose text names prompt files, doc text with a markdown link and a backticked path written to `/tmp` targets a name takes in turn, `python3 -m`, a copy whose source is a prompt file, `Image.open` of one; a mention as a path element only (`.claude-plugin`, `.claude.json`, `my_templates_x` none); denied: attributable python, node, perl and ruby writes, writes behind a name bound by `for` or to a protected value among several literals, an f-string's braces read as code, an escape (`subprocess`, `os.system`, `getattr`, `from os import`, an index call, `child_process`, backticks), a mode that is not a literal, stdin code behind `-W`, `-X`, `-i`, `-s`, `-` or an inline code flag, shell programs in heredocs and here-strings, a parameter bound across lines, a moved cwd, archive extraction, a call by name (`operator.methodcaller`), metadata writes (`chmod`), a copy into a directory under a prompt-file name, node `WriteStream`, perl `$^I`, ruby `File::write` and `reopen`; nested shell programs read to the bound and unreadable past it.
- Unit: `pfm/internal/hookentry/pcm_guard_scan_test.go` — plain literals per language, python names followed through their one binding and refused on any other (the writes table follows every plain literal binding), top-level argument splitting, the extended tables, and every truncation of a sample program read without a panic.
- Parser: `pfm/internal/cmdparse/cmdparse_test.go` `TestRedirectsRideTheirParts` — a statement's, a compound command's and a bare redirection's `Redirs`, and a heredoc body; `pfm/internal/cmdparse/innershell_test.go` `TestInnerShellUnknownStringIsUnread` — a `bash`, `sh` or `zsh -c` string the parser cannot know marks its part `Unread`, a literal one does not.
- Registration: `pfm/internal/claudelaunch/hooks_test.go` `TestPcmGuardRidesEveryWritingTool` and `pfm/internal/installer/expected_hooks_test.go` `TestClaudeHookTemplatesIncludesPcmGuard` pin exactly one `PreToolUse` template running `pfm internal pcm-guard` on `Edit|Write|MultiEdit|NotebookEdit|Bash`; `pfm/testdata/golden/cmdlines.txt` carries it in every launch line.
- Fence: lane `O2`, beat `O2.05-internal-plumbing` (`infra/fence/lanes/O2.sh`) — a main-chat `CLAUDE.md` write denied naming pcm and `pfm install`, the same write passed for pcm, a `cat CLAUDE.md` passed; `O1.sh` checks the verb is in the launch templates.

## Surfaces that stay in sync

- [hooks.md](hooks.md) — the inventory row, the count and the fail-open decision.
- `docs/dev/pfm-surface.md` — the `internal` row.
- `README.md` — the rules-that-bite paragraph.
- `templates/global/agents/pcm.md`, this repo's override `.claude/agents/pcm.md`, `.claude/agents/themer.md` — the writers the deny sends every caller to.
