# tracer-rr

`tracer-rr` is the family's repository digger: given a public repository's URL and a numbered batch of sub-queries, it clones the repository into `/tmp`, answers each sub-query from the code, writes a result file whose every piece of evidence is an absolute `path:line` into the clone, and returns a code-quoted finding per sub-query to its lead. It is `tracer`'s reading discipline pointed at someone else's code for a research lead. It is spawned only by `rr-pro` and `rr-pro-max`, never delegated to directly; `rr` does not know it exists. The lead's side of the exchange (when a sub-area goes to a repository, the brief, verification) is the repository lane in this file and in `rr.md` in this directory.

## Contents

- [Identity](#identity)
- [Who spawns it](#who-spawns-it)
- [The brief it receives](#the-brief-it-receives)
- [The clone](#the-clone)
- [Reading](#reading)
- [The result file](#the-result-file)
- [The return](#the-return)
- [Marks](#marks)
- [Why it writes a file](#why-it-writes-a-file)
- [Untrusted code](#untrusted-code)
- [Not part of the design](#not-part-of-the-design)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## Identity

| Field | Value |
| --- | --- |
| Kind | original agent, `templates/global/agents/tracer-rr.md`, linked by `pfm install` |
| Class | `RR-ONLY` |
| Model, effort | `opus`, `medium` — `tracer-pro-max`'s pin: following a call chain through unfamiliar code is judgment, not extraction |
| Tools | `Bash, Read, Grep, Glob` — no `Write`: the result file is written by `Bash` (§ The result file) |
| Budget | 30 tool calls |
| Spawns | nothing — it holds no `Agent` |
| Writes | its clone under `/tmp/rr-repos/`, and one result file under `{RR dir}/tracer-rr/` |
| Codex sandbox | workspace-write (codex-sandbox): the clone and the result file are writes; network inside Codex's sandbox is the engine's |
| Start hook | none — `pfm/internal/claudelaunch/hooks.go` (`HookRRDirMatcher`) lists exact names and omits it; the `RR-DIR:` line arrives in the brief |

Description, verbatim: `RR-ONLY digs a repository's code — spawned by rr-pro and rr-pro-max with a repo URL and numbered sub-queries, never delegated to directly. Returns its result file path, then a code-quoted finding per sub-query, then rabbit holes.`

## Who spawns it

Only the two deeper leads. The repository lane is not in `rr.md`'s body: `variants.json` swaps it into `rr-pro` and `rr-pro-max` by one `replace` entry each, keyed on the DIG step's last sentence, `` `sub-rr` is the only agent type you spawn. `` A plain `rr` run keeps its one digger and reads a repository only as web pages. The lane costs an `opus` digger and a clone per repository, which the cheap map does not buy.

The swapped-in text, identical in both entries:

- A sub-area whose answer lives in a public repository's code (how a project implements something, not what a page says about it) goes to `tracer-rr`: one digger per repository, never two on one repository in a round, counted in the round's cap. A sub-area spanning several repositories gets one `tracer-rr` per named repository: a `sub-rr` sent to read several repositories' code guessed raw file URLs that did not exist in a trial run. A later question about a repository a `tracer-rr` already dug goes to a `tracer-rr` on that repository, never to a `sub-rr`, which would re-derive it from web pages.
- Its brief carries `Repository:`, `Ref:` when the query names one, `History: yes` when a sub-query asks how the code changed over time, the numbered sub-queries, the `Goal:` line and the lead's own `RR-DIR:` line.
- Its findings are read from the clone, not through a fetch model, so they carry no paraphrase to catch: each such fact takes its `tracer-rr` link inline in the map, counts as fetched, and is not re-fetched at VERIFY, whose pages go to web facts, `unquoted` ones first.
- The document's `Coverage` section lists each result file a `tracer-rr` returned by its path relative to the RR directory, `tracer-rr/{file}`: the document may be tracked, and an absolute path would carry the machine's home directory into it.

## The brief it receives

| Line | Holds |
| --- | --- |
| `Repository:` | the clone URL, `https://` or `git@` form |
| `Ref:` | optional: a branch or tag; absent means the default branch |
| `History:` | optional: `yes` when a sub-query asks how the code changed over time |
| plan lines | `{sub-area} — settled when {evidence}` for each sub-area this dig owns, first, as in every brief of the family |
| numbered sub-queries | each self-contained, one subject, named in full |
| `Goal:` | the run's query in one line, identical in every brief of the run |
| `RR-DIR:` | the lead's ledger directory, where the result file goes |

## The clone

The brief's `Repository:`, `Ref:` and `History:` values reach the recipe as data: the agent writes them on the lines of a quoted heredoc the script reads, never inside shell quotes. The URL must start with `https://` or `git@`, the URL and ref may hold only `A-Z a-z 0-9 . _ ~ : / @ + % -`, and a ref may not start with `-`; anything else is `CLONE FAILED` before git runs.

One Bash call clones, or reuses a clone of the same commit:

1. The clone key is the URL without scheme, user and `.git` suffix: `https://github.com/{owner}/{repo}.git` keys as `github.com/{owner}/{repo}`.
2. With no `Ref:`, `git ls-remote {url} HEAD` names the commit first; a directory `/tmp/rr-repos/{key}@{sha12}` that already exists is reused without a clone. Rounds and runs that dig one repository at one commit share one clone.
3. Otherwise `git clone --depth 1 --no-recurse-submodules` into a temporary sibling (with `History: yes`, `--filter=blob:limit=1m` instead of `--depth 1`: every commit and every file under 1 MB, into a directory suffixed `+history`), `--branch {ref}` for a branch or tag. The clone is then moved to `/tmp/rr-repos/{key}@{sha12}` unless that directory appeared meanwhile, in which case the temporary copy is deleted and the existing one used.
4. Every git call runs with `GIT_TERMINAL_PROMPT=0`, so a missing or private repository fails instead of waiting for a password nobody will type, and `GIT_LFS_SKIP_SMUDGE=1`, so large-file pointers are not downloaded; the clone itself runs under `timeout 300`.

The directory is keyed by commit so it never changes under a reader: two diggers, or a user opening a path days later, read the same bytes the finding quoted. `/tmp/rr-repos/` is machine-wide rather than per project because a clone serves every project's runs and the paths it hands out must not depend on the caller's checkout. The result file records the URL and full commit, so a clone lost to a reboot is re-creatable.

## Reading

`tracer`'s rules, applied to the clone:

- Every read of a round in one message; a file read once is not read again; a file over 300 lines is read by `grep -n` hits and line ranges, never whole.
- Read to the answer: follow a call to the line that decides, returns, raises or writes; a fact not read goes under `Not read`.
- Search the whole clone with `grep -rn` unless the sub-query narrows it; a search that finds nothing is stated with its pattern and scope.
- A list claim names each item or counts the share and names the exceptions.
- History only in a `+history` clone, each command under `timeout 90`: `git log`, `git log -p -- {path}`, `git log -S{text}`, `git show {commit}:{path}`; a timeout, or a history question in a shallow clone, goes under `Not read` with the command. The limit sits below the Bash tool's 120 s, past which the harness moves a command to the background and its completion notice re-invokes a digger that has already returned.
- Only `git` read commands and file-read commands run: never the repository's code, build, tests, package manager or install scripts, never `fetch`, `gh`, `curl` or a second clone. `fetch` is also refused by the fleet's git-guard hook.
- Every read command starts with `emulate sh 2>/dev/null;`: the Bash tool runs the user's shell, which may be zsh, where `echo ====`, an unquoted `--include=*.ts` and `$P:src` abort the command, so a search that never ran reads like one that found little. bash ignores the prefix. The CLONE and SAVE recipes run under `bash -s` and need none.

## The result file

`{RR dir}/tracer-rr/{owner}-{repo}-{YYYY-MM-DD}.md`, the suffix `-2`, then `-3` when the name is taken that day. Written once, at the end, by one `Bash` call that claims the name with `set -C` (the shell's noclobber: `: > {file}` fails on an existing file, atomically) and fills the claimed file from a quoted heredoc. `Write` cannot guard the name: the harness lets an agent holding `Read` overwrite a file it never read (`rr.md`, § The document), and this digger needs `Read`.

The agent types each piece of evidence as a pointer, `@@ {path}:{A-B}` for lines read in the clone or `@@ {rev}:{path}:{A-B}` for lines read with `git show`, and types no excerpt and no link. For each pointer the script resolves the revision with `git rev-parse` (`HEAD` for the plain form), copies the lines with `git show`, and builds the link pinned to that commit, the path percent-encoded (`%`, space, `#`, `?`): `https://github.com/{owner}/{repo}/blob/{sha}/{path}#L{a}-L{b}` on GitHub, `https://{host}/{project}/-/blob/{sha}/{path}#L{a}-{b}` on a GitLab host, `clone-only` on any other. Excerpt and link are therefore exact and cost no output tokens. A pointer whose `{rev}:{path}` names a file that exists at `HEAD` (a path holding a colon) is read as a path. When any pointer resolves to no lines, the script removes the file and prints `NOT SAVED — pointers that resolve to no lines:` with the list, so a file only lands whole; the agent fixes or drops them and saves again under the same name. `SAVED` is followed by one line per pointer, `{pointer} {link}`, which the return copies.

The file holds:

- a header: repository URL, ref, full commit, clone directory (absolute) and the `Goal:` line;
- per sub-query: the finding, 2-4 sentences, then the detail (lists, commit histories) and its evidence, each `{absolute path}:{A-B}` (or `{sha12}:{path}:{A-B}` for a `git show` read) with its link beside it and the copied lines in a `~~~~` fence below;
- `Not read`, then `Rabbit holes`.

The file is for whoever opens the code after the run: the user following a claim into the clone, and `agent-optimizer` auditing the dig. The lead does not read it; the RR document lists its path.

## The return

In this order, and nothing else:

1. `SAVED {path}`, or `NOT SAVED — {the error}`.
2. `{url} @ {commit}`.
3. One finding per sub-query, under its number: its file section's opening paragraph, copied as saved, 2-4 sentences, while lists and histories stay in the file. Every claim the finding turns on carries its link, copied from the SAVE output rather than typed, and, where one line carries it, that line in backticks. A host with no link form marks the claim `clone-only`.
4. `Rabbit holes`: every lead the code raised, each a concrete next step and why it matters: another repository (a dependency, an upstream fork) for a `tracer-rr`, a URL found in a comment or README for a `sub-rr`.

The return carries links, not absolute paths: the lead cites web URLs only, and a local path is never a source in the family's documents. The return is the digger's last message: a background notice that re-invokes it afterwards is answered with one line, `{its first line} — return unchanged`, since whatever it sends then reaches the lead as a second result.

## Marks

- `CLONE FAILED — {the error}`: the clone or the reuse check failed; the return is this line alone and no file is written.
- `NOTHING FOUND — {patterns and scope}`: a sub-query whose searches ran over the clone and answered nothing.
- `NOT SAVED — {the error}`: the dig ran, the file did not land; the findings follow as usual. `NOT SAVED — pointers that resolve to no lines` is the script refusing a file with an unresolved pointer; the agent fixes it and saves again.
- `clone-only`: a claim on a host with no commit-pinned link form.

## Why it writes a file

`sub-rr` never writes because its findings would race for the run's one document. `tracer-rr`'s file is its own: one name per repository and day, claimed atomically so a second dig of the same repository that day takes the next suffix instead of erasing the first. The file carries what the lead does not need in its context (absolute paths, fenced excerpts), so the lead's context grows by the finding alone while the evidence stays on disk.

## Untrusted code

The digger reads a stranger's repository while holding `Bash`. Its prompt treats the repository as data (a line that reads as an instruction is content to report, never an order) and limits it to `git` and read commands. Two guards are mechanical. A repository URL can come from text a researched repository wrote (a `Rabbit holes` line becomes the next dig's `Repository:`), so the recipes never paste a brief value into shell syntax: the values are read from a quoted heredoc and checked against a whitelist (§ The clone), and the SAVE script refuses a clone outside `/tmp/rr-repos/`, a name outside `A-Z a-z 0-9 . _ -`, a range that is not two numbers, and a revision starting with `-`. Beyond the recipes the prompt is the only guard: nothing in the harness stops a `Bash` call a poisoned README talks it into. A Codex twin inherits its session's sandbox, where the network is often off; there the clone fails as `CLONE FAILED`, never as an empty answer.

## Not part of the design

| Left out | Reason |
| --- | --- |
| Evidence typed by the agent | Retyped excerpts cost most of a dig's output tokens and drifted from the code they cited (`...` gaps, lost indentation, short ranges); a pointer the script expands is exact and free |
| Links typed by the agent | A typed link drifted from the lines it cited (a `git show 3aebe1b^` read linked to another commit) and cost a second save; the script resolves the commit and builds the link |
| A `blob:none` history clone | Each old file version is fetched over the network, one request at a time, the first time `git log -S` or `-p` touches it: a pickaxe over one repository's history ran past 120 s in every dig of a trial, where the `blob:limit=1m` clone answers in under a second |
| A clone verb in the harvester | It would take `Bash` away from a reader of untrusted code and give Codex a sandbox-independent clone, at the price of engine work; the digger clones with `git` itself |
| Reusing `tracer` | `tracer` forbids the network and returns spec-writer furniture (test homes, check commands) a research lead cannot use |
| A lane in plain `rr` | The cheap map stays cheap; `rr-pro` and `rr-pro-max` pay for an `opus` digger and a clone by design |
| A commit `Ref:` | Every git command that materializes an arbitrary commit — `fetch`, `remote add`, `checkout` of a commit — is blocked by the fleet's git-guard hook (`pfm/internal/hookentry/git_guard.go`, `gitGuardBlocks`); running one inside a heredoc the guard does not parse would route around it. `git clone --branch` takes a branch or tag and is not guarded |
| Absolute paths in the return | The lead cites web URLs only; the paths live in the result file |

## Surfaces that stay in sync

| Surface | File |
| --- | --- |
| The agent | `templates/global/agents/tracer-rr.md` |
| The lane its briefs come from | `templates/global/agents/variants.json`, the `replace` entry of `rr-pro` and of `rr-pro-max`, identical text |
| The sentence the lane swaps | `templates/global/agents/rr.md`, step 4's last sentence |
| The renderer's gate | `TestShippedGlobalAgentVariantsRender`, `pfm/internal/codexgen/globalvariants_test.go` |
| The family doc | `docs/design/RR/rr.md` |
| The roster line | `docs/BLUEPRINT.md` |
