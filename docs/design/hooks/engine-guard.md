# engine-guard

`engine-guard` is the guard every Codex and OpenCode chat runs, main loop and every role alike, `gitter` included: it denies every git write and every write to a framework file. Only Claude changes either, under [git-guard](git-guard.md) and [pcm-guard](pcm-guard.md). Body: `pfm/internal/hookentry/engine_guard.go` (`EngineGuard`), run as `pfm internal engine-guard --engine codex|opencode` (the `engine-guard` branch of `runInternal`, `pfm/cmd/pfm/main.go`).

Decisions live in this file. A change lands here first, then in the code, then in every surface under [Surfaces that stay in sync](#surfaces-that-stay-in-sync).

## Contents

- [Why](#why)
- [Where it runs](#where-it-runs)
- [What it judges](#what-it-judges)
- [What it denies](#what-it-denies)
- [How it fails](#how-it-fails)
- [Named gaps](#named-gaps)
- [Tests](#tests)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## Why

Neither engine carries a Claude hook. A prose rule ("only gitter writes git") and a path-scoped pcm rule were the only law there, and a Codex or OpenCode chat could write git or a framework file past both. One guard over each engine's own tool-call hook closes both doors without a second set of rules: git writes and framework files are Claude's alone, on every engine, and a denial tells the caller to return the exact change to the user.

## Where it runs

| Engine | Carrier | Installed by |
| --- | --- | --- |
| Codex | one `PreToolUse` handler, matcher `Bash\|apply_patch`, timeout 10 s (`codexEngineGuardTimeoutSeconds`), command `{home}/.local/bin/pfm internal engine-guard --engine codex`, in every Codex home's `hooks.json` (`codexEngineGuardHook`, `pfm/internal/installer/codex_hooks.go`) | `pfm install`: ledger-recorded, trust-recorded like the other pfm handlers, and its timeout rewritten when it differs; the Codex probe expects it ([hooks.md](hooks.md#codex-hooksjson)) |
| OpenCode | the plugin `pfm-engine-guard.js` in the `plugins/` directory beside the configured OpenCode config path as written, the directory OpenCode scans even when the config file is a symlink (`wireOpenCodeEngineGuard`, `pfm/internal/installer/opencode_engine_guard.go`; body `pfm/internal/workbench/opencode_engine_guard_plugin.js`, staged with the absolute `{home}/.local/bin/pfm`) | `pfm install` stages or refreshes it and `pfm uninstall` removes it (`wireOpenCodeEngineGuard` runs in both modes); `pfm doctor` reports it MISSING, STALE or UNREADABLE (`probeOpenCodeEngineGuard`); every OpenCode chat on the host loads it, launched by pfm or not |

The Codex handler and the OpenCode plugin judge the main chat and every role alike. The OpenCode plugin judges in `tool.execute.before`, ahead of OpenCode's permission prompt, for the tools `bash`, `edit`, `write`, `apply_patch`, and older builds' `multiedit` and `patch`; it pipes `{"tool_name","tool_input","cwd"}` to the entry with a 10 s timeout and throws the deny reason, which OpenCode hands the model as the tool's error. `--engine` takes `codex` or `opencode` alone; any other value or shape exits 2 with usage.

## What it judges

| Engine | Tool | Read as |
| --- | --- | --- |
| Codex | `Bash` | a shell command, `tool_input.command` |
| Codex | `apply_patch` | a patch, in `tool_input.command` |
| OpenCode | `bash` | a shell command; `tool_input.workdir` joins the cwd |
| OpenCode | `apply_patch`, `patch` | a patch, `tool_input.patchText` |
| OpenCode | `edit`, `write`, `multiedit` | one file, `tool_input.filePath` |

Any other tool name is allowed unread. No `agent_type` exempts a caller: every framework path lands in the pcm-only class and no theme exemption exists.

## What it denies

- **Every git write** (`gitGuardCheck` in engine mode, [git-guard.md](git-guard.md#what-it-blocks)): the same parts git-guard blocks, `git reflog` past `show` and `exists` and every subcommand neither judged by its arguments nor listed as a read in `engineGuardGitReads` (`bisect`, `sparse-checkout`, `init`, an alias) included, plus a whole `git stash` (only `list` and `show` pass) and every run of `.claude/scripts/worktree.sh`, which git-guard leaves to a Claude main chat alone. The reason names no `gitter`, since no git writer runs on these engines. Git reads stay open: `status`, `log`, `diff`, `show`, `blame`, `ls-files`.
- **Every write to a framework file**, by the pcm-guard judge in engine mode ([pcm-guard.md](pcm-guard.md#what-it-protects)): any `CLAUDE.md`, `AGENTS.md`, `AGENTS.override.md`, `opencode.json` or `opencode.jsonc`; anything under a `.claude/`, `.codex/`, `.opencode/` or `.git/` element; the Professor clone's `templates/`; the whole Claude config dir, the Codex home (`CODEX_HOME`, else `~/.codex`) and the OpenCode config dir (`OPENCODE_CONFIG_DIR`, `$XDG_CONFIG_HOME/opencode`, `~/.config/opencode`); and the pfm binary, `~/.local/bin/pfm`, as written and physical (`engineGuardRoots`, `engineClass`).
- **What Bash reads beyond pcm-guard** (`enginePart`): the files an editor (`vi`, `vim`, `nvim`, `nano`, `emacs`, `ed`, …), `chmod`, `chown`, `chgrp`, `mkdir`, `rmdir`, `shred` or `sponge` names; the paths `git checkout` and `git restore` write; the files `patch`, `git apply` or `apply_patch` write, from the patch file, heredoc, here-string or `<` file, under `patch -d`'s or `git apply --directory`'s directory (a `---`/`+++` or `***`/`---` line counts only as its diff's header pair, so a body line is no target); and the code a script file or a `#!` program runs (`shebang`). A file written by a heredoc earlier in the same command is read from that body (`engineHeredoc`, called from the pcm-guard bash walk before `enginePart`): a `cat` with no file operand, or a `tee`, fed a heredoc records the body as the text of each file it writes (`>`, `>|`, `&>` targets and `tee` operands); under `>>`, `&>>` or `tee -a` the body is appended to the file's current text, and an append to a file the guard cannot read records nothing; any other write to that path later in the command forgets the record. `readFile` reads a recorded text before the disk, so an interpreter run, a `#!` program run by path, or a patch file is judged from the heredoc body exactly as from a file on disk.
- **Blind parts.** A shell, interpreter or editor left to read a terminal or a pipe, an editor with no operand, a script the guard cannot read (neither on disk nor written by a heredoc earlier in the same command, or over 1 MiB, run by an interpreter or as a `#!` program), and a patch it cannot read are denied with `engineGuardBlindLine`: "it runs code or applies a patch the guard cannot read (not on disk yet, a pipe, a terminal): pass code inline (-c, -e) or as a heredoc to the interpreter, or write the script or patch file in one command and run it in the next." Codex can type into such a program through `write_stdin` with no hook.

One message names every denied write and the ban (`engineGuardGitBan`, `engineGuardFileBan`): "Codex and OpenCode never write git: only Claude does" and "never change framework files", each ending with "Return the exact change to the user". The deny is the `PreToolUse` `permissionDecision` `deny` object every pfm guard writes.

## How it fails

- **Payload unreadable or undecodable, or empty:** one `pfm internal engine-guard: … (fail-open)` stderr line, the call allowed.
- **The parser itself errors:** fail-open the same way.
- **A command it cannot read that names a framework file** (a part that does not parse, a parse bound, an unreadable `-c` string): denied with the unreadable line. One that names none is allowed.
- **The pfm binary missing, or the guard failing or timing out:** the call runs unjudged. Codex treats a failed hook as no hook; the OpenCode plugin logs `engine guard did not run (fail-open)` and returns.
- **The deny cannot be written:** stderr line and exit 1.

## Named gaps

- **Codex MCP tools.** The handler's matcher covers `Bash` and `apply_patch` only, so an MCP tool that writes a file or git state is not seen.
- **Interactive programs.** A program driven through `write_stdin` after it started (a shell, an editor, an interpreter) is judged only at its start; what is typed into it later is never a hook call.
- **Compiled binaries, build tools and modules.** A program that writes a framework file from inside its own code (`make`, `go run`, a compiled tool, `python -m`) names no protected path in its command and is read as a plain program.
- **`python -c` and other interpreter code.** The code is judged by the write-site heuristics of [pcm-guard.md](pcm-guard.md#named-gaps), so a path built at run time or written through an unlisted API passes.
- **Fail-open.** A guard that does not run (a missing or failing binary, a timeout) allows the call.

## Tests

`pfm/internal/hookentry/engine_guard_test.go` (`TestEngineGuardGit`, `TestEngineGuardFrameworkFiles`, `TestEngineGuardEngineFlag`); `pfm/internal/installer/opencode_engine_guard_test.go` (`TestOpenCodeEngineGuardPluginRoundTrip`); the Codex rows in `pfm/internal/installer/hook_probe_test.go` and `expected_hooks_test.go`.

## Surfaces that stay in sync

- [hooks.md](hooks.md) — the Codex and OpenCode rows, the count table and the Codex probe's expected handlers.
- `pfm/internal/installer/codex_hooks.go`, `codex_hooks_herdr.go`, `settings_ownership.go` — the handler, its ledger and trust.
- `pfm/internal/installer/opencode_engine_guard.go`, `pfm/internal/workbench/opencode_herdr.go` — the plugin's staging.
- `templates/global/engine-sync.json`, `.claude/engine-sync.json` — what Codex and OpenCode are told about the ban (`codex.rootAdapter`, both agent preambles, `opencode.permission`).
- `docs/dev/pfm-surface.md` — the `internal` row.
