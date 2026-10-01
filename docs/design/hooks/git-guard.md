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

`pfm install` registers it in every account's `settings.json`, event `PreToolUse`, matcher `Bash`, command `pfm internal git-guard` (`pfm/internal/installer/expected_hooks.go:91-98`), and `pfm doctor` expects it there like every machine-global hook. It is an internal verb (`pfm/cmd/pfm/main.go:70`, dispatched at `:449`). Because the registration is per account and not per project, it runs in every repository, whether pfm manages it or not.

## Who is exempt

A payload whose `agent_type` is `gitter` is always allowed (`git_guard.go:28`, checked at `:121`). The hook reads only the name, so either gitter qualifies: a project's own (`.claude/agents/gitter.md`, scaffolded from `templates/project/agents/gitter.md`) or the machine-global one (`templates/global/agents/gitter.md`, which `pfm install` links into every account and a project's own gitter overrides by name). Every other caller is checked, a main chat (no `agent_type`) included.

## How it reads a command

A non-Bash tool, or a command containing neither `git` nor `worktree.sh`, returns at once (`git_guard.go:121-124`). Otherwise the command goes through the shell parser (`pfm/internal/cmdparse`), which unwraps wrappers (`timeout`, `env`), follows `&&`, `;`, pipes and subshells, resolves literal variables (`G=git; $G push` is a `git` call), and parses an inner `bash -c` string; a heredoc body and an `echo git …` argument are data, never a git call. The parser expands brace lists but never a glob: an unquoted `*` reaches the guard as the literal `*`, the whole-tree pathspec `gitGuardAnyWide` names (`:417`), never as the file names a shell would expand it to, and one `checkout` operand carrying `*`, `?` or `[` is a pathspec, never a branch, since `git check-ref-format` forbids those characters in a ref name (`gitGuardCheckout`, `:290-294`). Two bounds keep any command from holding the hook: a command over 64 KiB (`maxCommandBytes`) is one unparsed part never handed to the shell parser, and a literal `-c` string nested past 8 levels (`maxShellDepth`) is one unparsed part in place of its parse; the parser marks each `Bounded`. For each part whose program is `git`, git's global options (`-C`, `-c`, `--git-dir`, `--work-tree`, `--no-pager` and the rest) are skipped, `-C` naming the repository (`gitGuardSplit`, `git_guard.go:203`); the subcommand and its arguments decide (`gitGuardBlocks`, `:231`).

## What it blocks

| Area | Blocked | Allowed |
| --- | --- | --- |
| worktrees | `worktree add`, `remove`, `prune`, `move`, `lock`, `unlock`, `repair`; `.claude/scripts/worktree.sh create`, `remove`, `prune` | `worktree list`; `worktree.sh list` |
| history | `commit`, `merge`, `rebase`, `cherry-pick`, `revert`, `am`, `pull`; every `reset` (a mode, a commit, paths, or bare) | `log`, `show`, `diff` and every other read |
| branches and tags | `switch`; `checkout -b`, `-B`, `--orphan`, `--track`, `--detach`, or one operand that is not an existing path and carries no `*`, `?` or `[` (a branch switch); `branch` with a name or `-d`, `-D`, `-m`, `-M`, `-c`, `-f`, `-u`; `tag` with a name or `-d`, `-a`, `-s`, `-m`, `-f`; `update-ref`; `symbolic-ref` with two operands or `-d` | `branch` bare, `--list`, `-a`, `-r`, `-v`, `--show-current`, `--contains`, `--merged`, `--no-merged`; `tag` bare, `-l`, `--list`, `--contains`, `--points-at`; `symbolic-ref HEAD` |
| remotes | `push`, `fetch`; `remote add`, `remove`, `rename`, `set-url`, `set-head`, `set-branches`, `prune`, `update` | `remote`, `remote -v`, `ls-remote` |
| staging | `add`, `rm`, `mv`, `restore --staged` / `-S`, `apply --index` / `--cached` / `--3way`, `update-index`, `hash-object -w` | `add -n`, `rm -n` (`--dry-run`); `apply` to the working tree, `apply --check`; `hash-object` |
| whole-tree destruction | `stash` with no pathspec (bare, `push` or flags without paths, `save`), `stash drop`, `clear`, `store`, `branch`; `clean`; `checkout` or `restore` of `.`, `:/`, `*` or the repository root | `stash push -- <paths>`, `stash pop`, `apply`, `list`, `show`; `clean -n`; `checkout -- <file>`, `restore <file>` |
| repository settings | a `config` write (anything but `--get`, `--get-all`, `--get-regexp`, `--list`, `-l`, `--show-origin`, `get`, `list` or a single-key read); `gc`, `prune`, `filter-branch`, `filter-repo`, `replace`; `notes` except `show` and `list`; `submodule add`, `update`, `deinit`, `sync` | `config --get user.name`, `config user.name`; `notes show`; `submodule status` |

Anything not in the blocked column is allowed, `archive` included. A single-path stash stays open on purpose: an agent parks its own file and restores it, which touches nobody else's work.

## The deny

One message names every blocked part of the call as its words read, then `Only gitter writes this: spawn Agent(subagent_type: "gitter") with the repo path and the exact change.` (`git_guard.go:30`, composed by `gitGuardReason`, `:467`). A worktree block adds the right way for its repository: the `-C` directory, else the payload's `cwd`, walked up to the directory holding a `.git` entry without running git (`gitGuardTopLevel`, `:447`). When that repository has `.claude/scripts/worktree.sh`, the line reads `gitter creates and removes worktrees with {repo}/.claude/scripts/worktree.sh create|remove|prune — the only right way here.` (`:484`); otherwise `gitter creates and removes worktrees (Phase SETUP).` (`:32`). A blocked `git stash` adds, once, ``To park only your own files, `git stash push -- <path>...` is allowed (restore with `git stash pop`); a whole-tree stash moves every other agent's uncommitted work.`` (`gitGuardStashHint`, `:33-35`), so an agent parking its own files to watch a test fail against the unchanged code stashes those paths instead of giving up.

## How it fails

- **Payload unreadable or undecodable:** one `pfm internal git-guard: … (fail-open)` stderr line, the call allowed, like every pfm hook (`git_guard.go:107-119`).
- **The parser itself errors:** fail-open the same way (`:131-134`); a parser failure must never freeze every Bash call on the machine.
- **One part of the command does not parse, or a parse bound cut it short, and the command mentions git:** denied with `could not read this command; split it so each git call is its own simple command.` (`:31`, decided at `:140-142`), because allowing it would let any git write through by breaking the quoting or padding past a bound. A command mentions git when it holds `git` followed by any whitespace (space, tab, newline) or `.claude/scripts/worktree.sh` (`gitGuardMentionsGit`, `:162`); an unreadable command that mentions neither, a 70 KiB heredoc say, is allowed.
- **The deny cannot be written:** stderr line and exit 1 (`:151-154`).

## Named gaps

- **Python snippets.** The parser no longer reads Python, so `python -c` and heredoc Python are not inspected and the hook never spawns `python3`; a git call made from inside Python (`subprocess.run(["git", "push"])`) is not seen by the guard.
- **Unresolved words.** A `$VAR` or other word the parser cannot resolve is read as written, so `git $CMD` passes unless its literal words are a blocked form.
- **Obfuscated git in an unreadable command.** A command that does not parse, or passes a bound, and spells git only through quoting or escapes (`g\it`, `"gi"t`) does not mention git, so it is allowed.

## Tests

- Unit: `pfm/internal/hookentry/git_guard_test.go` — gitter allowed everything; a main-chat `worktree add` names the right way, with and without the repository's script; every blocked row denied for an executor; reads and own-file writes allowed; every blocked part of one call named; an unquoted `*` in `checkout --` and `stash push --` denied, one glob `checkout` operand allowed; an unparseable git command denied, `git` before a tab included; a git command past the size or the nesting bound denied, an oversize command that never mentions git allowed; a malformed payload fails open loudly.
- Registration: `pfm/internal/installer/expected_hooks_test.go` pins exactly one `PreToolUse`/`Bash` template running `pfm internal git-guard`; `settings_wiring_test.go` carries it in the wired settings.
- Fence: lane `O2`, beat `O2.05-internal-plumbing` (`infra/fence/lanes/O2.sh`) — a main-chat `worktree add` denied naming gitter, the same call passed for gitter, `git status` passed for anyone; `O1.sh` checks the verb is dispatched.

## Surfaces that stay in sync

- [hooks.md](hooks.md) — the inventory row and the fail-open decision.
- `docs/dev/pfm-surface.md` — the `internal` row.
- `templates/global/agents/gitter.md` and `templates/project/agents/gitter.md` — the writer the deny sends every caller to.
