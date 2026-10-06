---
name: pfm:workbench
description: "Create and maintain sub-projects — load for /pfm:workbench new|adopt|modify|check [dir], or a nested directory needing its own persona. Framework edits → /pcm; fleet verbs → /pfm."
argument-hint: "[new|adopt|modify|check] [dir]"
---

# Workbench

A workbench is a directory holding `.professor/workbench.json` below a managed root: the nearest ancestor holding `.professor/baseline.json`. Its persona is its prompt, effort and model; its manifest declares the engines it enables. The nearest workbench owns launches beneath it.

A workbench owns its own CLAUDE.md: pfm codex build {dir} compiles it into {dir}/AGENTS.md, and without it every Codex launch in the workbench is refused.

`$ARGUMENTS` selects the verb and directory; resolve a relative directory under the managed root. Pass `.claude/**` and `CLAUDE.md` writes through the project's guard. Use invented examples when writing shared instructions; the example here is `/work/acme/docs/scribe` under managed root `/work/acme`.

| field | type | default | rule |
| --- | --- | --- | --- |
| `prompt` | string | required | a relative path resolving to a regular non-empty file inside `{dir}/.professor/` |
| `engines` | string array | `["claude"]` | each of `claude`, `codex`, `opencode`, no repeat, not empty; order is the ✦ carousel order |
| `title` | string | `filepath.Base(dir)` | not empty when present |
| `name` | string | `naming.WorkbenchPrefix(title)` | no whitespace, no `:` |
| `effort` | string | none | each launch validates it against its engine's efforts and carries it lower-cased; an unknown value refuses the launch |
| `model` | string | none | carried verbatim |

Unknown or mistyped fields invalidate the manifest. Engine words are `claude`, `codex`, `opencode`; `cc` and `cx` are CLI aliases, not manifest values. OpenCode carries the prompt only.

## new

For `/pfm:workbench new docs/scribe` in `/work/acme`:

1. Confirm `/work/acme/.professor/baseline.json` marks the managed root. The bench sits 1–6 directories below it; use a path discovery can walk (limits below). Inspect the target before writing and keep existing content.
2. Write `/work/acme/docs/scribe/.professor/scribe.md`, a non-empty persona file; the example body is `You are scribe.` Write `/work/acme/docs/scribe/CLAUDE.md` with the bench's own orientation and rules. `.claude/agents/` and `.claude/skills/` are optional: create them only for roles or skills the bench needs.
3. Write `docs/scribe/.claude/settings.json`, preserving other settings, with:

   ```json
   {"claudeMdExcludes": ["!**/docs/scribe/CLAUDE.md"], "autoMemoryEnabled": false}
   ```

4. Write `docs/scribe/.professor/workbench.json`:

   ```json
   {"prompt": "scribe.md", "title": "Scribe", "name": "_SCRIBE", "effort": "xhigh"}
   ```

   Omitted `engines` enables Claude. To enable Codex too, set `"engines": ["claude", "codex"]`, then run `pfm codex build /work/acme/docs/scribe`; step 2's `CLAUDE.md` is its source. For OpenCode, include `"opencode"`, then run `pfm opencode build /work/acme/docs/scribe`.
5. Run `pfm doctor` from `/work/acme`, read its workbench lines under `check`, then open `pfm ls`. The group is `acme › Scribe`; its ✦ row launches in `docs/scribe`. `pfm chat new --cwd /work/acme/docs/scribe` or `chat_new` with that cwd may omit the name: the first unused `_SCRIBE:{n}` is chosen across live and killed roster names.

## adopt

1. Inspect the existing directory, prompt and launcher script. Keep the prompt's content; put its file inside the bench's `.professor/` and set `prompt` to its relative filename. Write or preserve `{dir}/CLAUDE.md` before enabling Codex or building its mirror. Apply `new` step 3's settings for this directory.
2. Translate the launcher's persona into manifest fields: `--effort xhigh` → `"effort": "xhigh"`; `--harness-prompt /work/acme/docs/scribe/.professor/scribe.md` → `"prompt": "scribe.md"`; `--name "_X:$n"` → `"name": "_X"`. The `_SCRIBE` example uses that prefix instead. Map a model flag to `model`, and the supported launch engines to `engines`.
3. Write the manifest; build each enabled Codex/OpenCode mirror with `pfm codex build {dir}` / `pfm opencode build {dir}`. Run `check`, then retire the script and its callers in the same change, replacing them with the ✦ row or `pfm chat new --cwd {dir}`. Keep launch behaviour outside the persona fields in the project's own workflow.

## modify

1. Read the manifest and edit the named field: `engines` sets availability and carousel order; `effort` and `model` set defaults; `prompt` names the file under `.professor/`; `title` labels the picker group; `name` sets the numbered-name prefix. Preserve every other field and the prompt unless requested.
2. When adding `codex` to `engines`, write `{dir}/CLAUDE.md` first if absent, then enable it and run `pfm codex build {dir}`. When enabling `opencode`, run `pfm opencode build {dir}`. Rebuild the enabled mirrors after changing their Claude sources; launches also rebuild their own mirror before spawning.
3. Run `check`. Explicit launch flags win over the persona: Claude `--system-prompt-file` / `--harness-prompt`, Codex `-c developer_instructions=…`, and each engine's explicit effort/model flags. `--agent-role` composes the role on the explicit harness, else the workbench prompt, else the fleet prompt.

## check

Run `pfm doctor` from the managed root or bench. Read each line, including discovery failures; `none — not inside a Professor project` means the cwd has no managed root; `none under {root}` means discovery found no workbench there. The output patterns below come from `pfm/internal/doctor/workbench_checks.go` in `{BLUEPRINT_CLONE_PATH}`; `{dir}`, `{root}`, `{engine}`, `{engines}`, `{prompt}`, `{fault}`, `{problem}`, `{problems}` and `{err}` stand for printed values; `{problems}` joins rebuildable problems with `; `.

| doctor line | count | action |
| --- | --- | --- |
| `doctor: workbench none — not inside a Professor project` | none | Run from a managed root or bench. |
| `doctor: workbench none under {root}` | none | No workbench was found under this root. |
| `doctor: workbench {dir} ok · engines {engines} · prompt {prompt}` | none | Manifest valid; printed after mirror checks only when none failed. |
| `doctor: workbench {dir} FAILED: {fault}` | failure | Repair the named manifest field or prompt: valid JSON, known typed fields, an in-bench regular non-empty prompt, supported non-repeated engines, non-empty title and a one-word name without `:`. Rerun doctor. |
| `doctor: workbench {dir} {engine} mirror STALE: {problems} — the next launch there rebuilds it` | warning | A build clears stale, missing, wrong-mode and orphaned outputs. Launch there to rebuild, or run `pfm {engine} build {dir}`. |
| `doctor: workbench {dir} {engine} mirror FAILED: {problem} — the next launch there fails; fix it, then run pfm {engine} build {dir}` | failure per problem | Repair each named problem, build that mirror, then rerun doctor. Write a missing `CLAUDE.md` before a Codex build. |
| `doctor: workbench {dir} {engine} mirror STALE: {problems} — rebuilt by pfm {engine} build {dir} once the failures are fixed` | warning | Fix the mirror failures, then build to clear these rebuildable outputs. |
| `doctor: workbench {dir} {engine} mirror BROKEN: {err}` | failure | Repair the named source/config, build that mirror, then rerun doctor. |
| `doctor: workbench discovery FAILED: {WalkError text} — whether more workbenches exist there is UNKNOWN` | failure | Restore access to the named path; rerun doctor. |
| `doctor: workbench UNREADABLE {err}` | failure | Repair the managed-root lookup path, then rerun doctor. |

An invalid manifest refuses new launches and resumes. A new disabled engine is refused as `workbench {dir} does not enable {word}: add "{word}" to "engines" in {manifest path}`; add the intended engine and build its mirror. A disabled engine resuming an existing chat uses the fleet prompt.

A missing bench orientation refuses Codex with:

```text
build the codex mirror of workbench /work/acme/docs/scribe: read /work/acme/docs/scribe/CLAUDE.md: open /work/acme/docs/scribe/CLAUDE.md: no such file or directory
```

Write `docs/scribe/CLAUDE.md`, then rerun `pfm codex build /work/acme/docs/scribe`.

## Limits

- Discovery walks 1–6 directories below each managed root, including nested git repositories, `.professor/` and `.workbenches/`; it skips other dot-directories (including `.worktrees`), `node_modules`, `vendor`, `venv`, and symlinks. A chat in a linked worktree groups under the main checkout's bench at the same relative path, and launches on the worktree's own copy of that bench: its prompt, manifest and mirror come from the worktree's branch.
- Codex and OpenCode also load ancestor `AGENTS.md` files: a parent repo's rules reach a workbench seat on those engines. The persona takes the fleet prompt's place and nothing else: `CLAUDE.md`, `AGENTS.md`, Codex's base instructions, OpenCode's provider prompt, environment and your other `instructions` entries stay; it does not isolate the bench from ancestor rules.
- The ✦ carousel lists enabled engines with accounts. With none, it shows a workbench error notice. An omitted engine in `chat new` / `chat_new` uses the current choice when enabled and usable, else the first enabled engine with an account.
- OpenCode `chat new`, reload and branch remain refused; use its picker ✦ row and resume door. `pfm headless exec`, `PurposeQuery` probes and `internal/ask` one-shot runs are outside the workbench persona rule.
