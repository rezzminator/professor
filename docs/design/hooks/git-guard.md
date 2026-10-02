# git-guard

`git-guard` is the machine-global `PreToolUse` hook on `Bash` that holds one rule at the call instead of in the prompt: only `gitter` writes shared git state. Body: `pfm/internal/hookentry/git_guard.go`. The fleet's other hooks, the ownership rule and the `pfm doctor` check that proves each hook is installed live in [hooks.md](hooks.md); there `git-guard` is one row of the inventory.

Decisions live in this file. A change lands here first, then in the code, then in every surface under [Surfaces that stay in sync](#surfaces-that-stay-in-sync).

## Contents

- [Why](#why)
- [Where it runs](#where-it-runs)
- [Who is exempt](#who-is-exempt)
- [How it reads a command](#how-it-reads-a-command)
- [What it blocks](#what-it-blocks)
- [The deny](#the-deny)
- [How it fails](#how-it-fails)
- [Named gaps](#named-gaps)
- [Tests](#tests)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## Why

An agent that made its own worktree left it behind: nothing tore it down, and stale worktrees and branches piled up in every repository. A prompt rule ("only gitter writes git") was read and then broken; a hook is not.

## Where it runs

`claudelaunch.HookTemplates` names it — event `PreToolUse`, matcher `Bash`, command `pfm internal git-guard` (`pfm/internal/claudelaunch/hooks.go:30`) — and `claudelaunch.Render` carries it in the launch-line `--settings` of every Claude chat a pfm door starts ([hooks.md](hooks.md)); `pfm doctor` spawn-audit expects it in each live chat's payload like every pfm hook. It is an internal verb (`pfm/cmd/pfm/main.go:69`, dispatched at `:427`). Because it rides every launch and not one project's settings, it runs in every repository, whether pfm manages it or not.

## Who is exempt

A payload whose `agent_type` is `gitter` is always allowed (`git_guard.go:26`, checked at `:121`). The hook reads only the name, so either gitter qualifies: a project's own (`.claude/agents/gitter.md`, scaffolded from `templates/project/agents/gitter.md`) or the machine-global one (`templates/global/agents/gitter.md`, which `pfm install` links into every account and a project's own gitter overrides by name). Every other caller is checked, a main chat (no `agent_type`) included.

## How it reads a command

A non-Bash tool, or a command containing neither `git` nor `worktree.sh`, returns at once (`git_guard.go:121-124`). Otherwise the command goes through the shell parser (`pfm/internal/cmdparse`), which unwraps wrappers (`timeout`, `env`), follows `&&`, `;`, pipes and subshells, resolves literal variables (`G=git; $G push` is a `git` call), and parses an inner `bash -c` string; a heredoc body and an `echo git …` argument are data, never a git call. The parser expands brace lists but never a glob: an unquoted `*` reaches the guard as the literal `*`, never as the file names a shell would expand it to. A pathspec is wide (`gitGuardAnyWide`, `:430`) when it is `.`, `:/`, `*`, `:(top)` or a path resolving to the repository's top level, or an all-star glob (`gitGuardAllStarGlob`, `:462`): with one leading magic prefix (`:(…)`, `:/` or a lone `:`) and a leading `./` stripped and a trailing `/` trimmed, its `/`-separated elements after the literal prefix (the elements before the first holding `*` or `?`) are at least one, each only `*` and `?` with at least one `*`, and that prefix is empty, `.`, or resolves to the top level. So `?*`, `**`, `./*`, `:(glob)**`, `**/*` and `{top}/*` are wide; `src/*`, `*.go` and `.*` are not. One `checkout` operand that is not an existing path is a branch or revision whatever glob characters it carries, since a commit expression (`HEAD^{/fi.*}`, `:/fi.*`) may hold `*`, `?` or `[` (`gitGuardCheckout`, `:294`). Two bounds keep any command from holding the hook: a command over 64 KiB (`maxCommandBytes`) is one unparsed part never handed to the shell parser, and a literal `-c` string nested past 8 levels (`maxShellDepth`) is one unparsed part in place of its parse; the parser marks each `Bounded`. For each part whose program is `git`, git's global options (`-C`, `-c`, `--git-dir`, `--work-tree`, `--no-pager` and the rest) are skipped, `-C` naming the repository (`gitGuardSplit`, `git_guard.go:219`); the subcommand and its arguments decide (`gitGuardBlocks`, `:247`).

## What it blocks

| Area | Blocked | Allowed |
| --- | --- | --- |
| worktrees | `worktree add`, `remove`, `prune`, `move`, `lock`, `unlock`, `repair`; `.claude/scripts/worktree.sh create`, `remove`, `prune` | `worktree list`; `worktree.sh list` |
| history | `commit`, `merge`, `rebase`, `cherry-pick`, `revert`, `am`, `pull`; every `reset` (a mode, a commit, paths, or bare) | `log`, `show`, `diff` and every other read |
| branches and tags | `switch`; `checkout -b`, `-B`, `--orphan`, `--track`, `--detach`, or one operand that is not an existing path, glob characters or not (a branch or revision switch); `branch` with a name or `-d`, `-D`, `-m`, `-M`, `-c`, `-f`, `-u`; `tag` with a name or `-d`, `-a`, `-s`, `-m`, `-f`; `update-ref`; `symbolic-ref` with two operands or `-d` | `branch` bare, `--list`, `-a`, `-r`, `-v`, `--show-current`, `--contains`, `--merged`, `--no-merged`; `tag` bare, `-l`, `--list`, `--contains`, `--points-at`; `symbolic-ref HEAD` |
| remotes | `push`, `fetch`; `remote add`, `remove`, `rename`, `set-url`, `set-head`, `set-branches`, `prune`, `update` | `remote`, `remote -v`, `ls-remote` |
| staging | `add`, `rm`, `mv`, `restore --staged` / `-S`, `apply --index` / `--cached` / `--3way`, `update-index`, `hash-object -w` | `add -n`, `rm -n` (`--dry-run`); `apply` to the working tree, `apply --check`; `hash-object` |
| whole-tree destruction | `stash` with no pathspec (bare, `push` or flags without paths, `save`), `stash drop`, `clear`, `store`, `branch`; `clean`; `checkout`, `restore` or `stash push` of `.`, `:/`, `*`, an all-star glob (`?*`, `**`, `./*`, `:(glob)**`, `**/*`) or the repository root | `stash push -- <paths>`, `stash pop`, `apply`, `list`, `show`; `clean -n`; `checkout -- <file>`, `checkout -- *.txt`, `restore <file>` |
| repository settings | a `config` write (anything but `--get`, `--get-all`, `--get-regexp`, `--list`, `-l`, `--show-origin`, `get`, `list` or a single-key read); `gc`, `prune`, `filter-branch`, `filter-repo`, `replace`; `notes` except `show` and `list`; `submodule add`, `update`, `deinit`, `sync` | `config --get user.name`, `config user.name`; `notes show`; `submodule status` |

Anything not in the blocked column is allowed, `archive` included. A single-path stash stays open on purpose: an agent parks its own file and restores it, which touches nobody else's work.

## The deny

One message names every blocked part of the call as its words read, then `Only gitter writes this: spawn Agent(subagent_type: "gitter") with the repo path and the exact change.` (`git_guard.go:28`, composed by `gitGuardReason`, `:517`). A worktree block adds the right way for its repository: the `-C` directory, else the payload's `cwd`, walked up to the directory holding a `.git` entry without running git (`gitGuardTopLevel`, `:496`). When that repository has `.claude/scripts/worktree.sh`, the line reads `gitter creates and removes worktrees with {repo}/.claude/scripts/worktree.sh create|remove|prune — the only right way here.` (`:534`); otherwise `gitter creates and removes worktrees (Phase SETUP).` (`:32`). A blocked `git stash` adds, once, ``To park only your own files, `git stash push -- <path>...` is allowed (restore with `git stash pop`); a whole-tree stash moves every other agent's uncommitted work.`` (`gitGuardStashHint`, `:33-35`), so an agent parking its own files to watch a test fail against the unchanged code stashes those paths instead of giving up.

## How it fails

- **Payload unreadable or undecodable:** one `pfm internal git-guard: … (fail-open)` stderr line, the call allowed, like every pfm hook (`git_guard.go:107-119`).
- **The parser itself errors:** fail-open the same way (`:131-134`); a parser failure must never freeze every Bash call on the machine.
- **One part of the command does not parse, and the command mentions git:** denied with `could not read this command; split it so each git call is its own simple command.` (`:29`, decided at `:146-149`), because allowing it would let any git write through by breaking the quoting. A command mentions git when it holds the token `git` with the string's edge or a non-word character (anything outside `[A-Za-z0-9_]`) on each side — `git push`, `"git"`, `git;`, `git<`, `git` before a tab or newline — or `.claude/scripts/worktree.sh` (`gitGuardMentionsGit`, `:171`). `digits`, `legit` and `mygit` are not git, so an unparsable command whose only `git` sits inside a word is allowed.
- **A parse bound cut the command short:** denied whenever the command passed the `git` / `worktree.sh` filter, with ``could not read this command in full: it passes a parse bound (over 64 KiB, or a `-c` string nested past 8 levels). Shorten the command; write long content to a file with the Write tool.`` (`gitGuardBounded`, `:30-31`, decided at `:140-145`). Past a bound nothing is read, so no spelling of git (`"git" push`, `'git' push`, `G=git; $G push`) can be told from the text around it. The reason never tells the agent to split git calls the command may not hold, and alone it adds no gitter line: a shortened command is read and judged like any other.
- **The deny cannot be written:** stderr line and exit 1 (`:158-161`).

## Named gaps

- **Python snippets.** The parser no longer reads Python, so `python -c` and heredoc Python are not inspected and the hook never spawns `python3`; a git call made from inside Python (`subprocess.run(["git", "push"])`) is not seen by the guard.
- **Unresolved words.** A `$VAR` or other word the parser cannot resolve is read as written, so `git $CMD` passes unless its literal words are a blocked form.
- **Obfuscated git in an unparsable command.** A command that does not parse and spells git only through quoting or escapes (`g\it`, `"gi"t`) does not mention git, so it is allowed.
- **A glob program word.** Globs are never expanded, so `/usr/bin/gi? push` runs Program `/usr/bin/gi?`, not `git`, and is allowed.
- **An absolute glob naming the root.** `git checkout -- /abs/rep?` names the repository root only once expanded; read literally it resolves to no top level, so it is not wide.
- **Loop variables.** A `for` variable is bound to every item at once, so `for g in true git; do $g push; done` runs Program `true`.
- **Unsplit values.** An unquoted `$X` is never split into words, so `G="git push"; $G` is one program word `git push`, not a git call.
- **Command substitution as the program.** `$(which git) push` leaves its program word unresolved, read as written with status OK, and is allowed.
- **An empty brace alternative.** `{git,} push` expands to Program `git` with an empty first argument, read as the subcommand, so `push` is never judged.
- **Unknown wrappers.** `eval`, `env -S`, `stdbuf`, `caffeinate`, `flock` and `find -exec` are not unwrapped, so a git call behind one is not seen.
- **ANSI-C quoting.** `$'\x67it'` is taken as its raw text, never decoded to `git`.
- **Uncapped expansion.** Arrays and loops expand without a cost cap, so a command built to expand hugely can hold the hook.
- **Last assignment wins.** A variable keeps its last literal assignment across subshells and both arms of a conditional, so `$G` may resolve to a value the shell would not hold there.
- **Wide globs outside the all-star rule.** A bracket or mixed glob that still covers the whole tree (`[a-z]*`) is not read as wide.
- **Exclude-only pathspecs.** git reads a pathspec list of only exclusions (`':!a.txt'`) as the whole tree minus them; the guard does not read it as wide.

## Tests

- Unit: `pfm/internal/hookentry/git_guard_test.go` — gitter allowed everything; a main-chat `worktree add` names the right way, with and without the repository's script; every blocked row denied for an executor; reads and own-file writes allowed; every blocked part of one call named; an unquoted `*` in `checkout --` and `stash push --` denied; the all-star globs `?*`, `**`, `./*`, `:(glob)**`, `**/*` denied after `--` and `?*` as one operand, a narrow glob (`*.txt`, `src/*.go`) allowed, and `gitGuardAnyWide` judged over the wide-pathspec table; a single revision operand carrying glob characters (`HEAD^{/fi.*}`, `:/fi.*`) denied; an unparseable git command denied, `git` before a tab and quoted `"git"` included, one whose only `git` sits inside a word allowed; a command past the size or the nesting bound denied with the parse-bound reason, whatever the spelling of git (`"git"`, `'git'`, `$G`) and even with `git` only inside a word; a malformed payload fails open loudly.
- Registration: `pfm/internal/installer/expected_hooks_test.go` pins exactly one `PreToolUse`/`Bash` template running `pfm internal git-guard` among the templates read from `claudelaunch.HookTemplates`.
- Fence: lane `O2`, beat `O2.05-internal-plumbing` (`infra/fence/lanes/O2.sh`) — a main-chat `worktree add` denied naming gitter, the same call passed for gitter, `git status` passed for anyone; `O1.sh` checks the verb is dispatched.

## Surfaces that stay in sync

- [hooks.md](hooks.md) — the inventory row and the fail-open decision.
- `docs/dev/pfm-surface.md` — the `internal` row.
- `templates/global/agents/gitter.md` and `templates/project/agents/gitter.md` — the writer the deny sends every caller to.
