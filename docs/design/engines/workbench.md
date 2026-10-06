# Workbench

## Contents

- [Manifest](#manifest)
- [Discovery and ownership](#discovery-and-ownership)
- [Persona precedence](#persona-precedence)
- [Launch doors](#launch-doors)
- [Mirrors](#mirrors)
- [Picker rows](#picker-rows)
- [Cache](#cache)
- [Doctor](#doctor)
- [Operator manual and installation](#operator-manual-and-installation)

## Manifest

A workbench is a nested sub-project marked by `{dir}/.professor/workbench.json`, below the nearest managed root containing `.professor/baseline.json`. It holds its own persona prompt, `CLAUDE.md`, Claude settings, and optional `.claude/agents/` and `.claude/skills/`. The manifest and prompt are read at launch; the nearest eligible bench owns the cwd. Sources: `pfm/internal/workbench/manifest.go`, `pfm/internal/workbench/discover.go`, `pfm/internal/professor/project.go`, `pfm/internal/paths/paths.go` (`WorkbenchManifest`).

| field | default | validation |
| --- | --- | --- |
| `prompt` | required | Relative regular non-empty file inside `.professor/`, including after physical-path resolution. |
| `engines` | `["claude"]` | Non-empty ordered array of distinct `claude`, `codex`, `opencode` words. |
| `title` | `filepath.Base(dir)` | Non-empty string when present. |
| `name` | `naming.WorkbenchPrefix(title)` | Non-empty string without whitespace or `:`. |
| `effort` | none | String; every launch door validates it through its engine's effort roster (`action.PersonaEffort` over `ClaudeEffort`, `CodexEffort`) and carries it lower-cased; an unknown value refuses the launch naming the bench. |
| `model` | none | String passed verbatim. |

`LoadBench` uses `DisallowUnknownFields`; field type errors and unknown fields invalidate the bench. Errors retain its identity and use `{manifest path}: {fault}`. `WorkbenchPrefix` uppercases the title, retains Unicode letters/digits and falls back to `WORKBENCH`; `NextNumbered` chooses the first free positive `{prefix}:{n}`. `chat.WorkbenchName` reads live and killed roster names and refuses on a roster read error. Sources: `pfm/internal/naming/workbench.go`, `pfm/internal/chat/workbench_name.go`.

The shared example is `/work/acme/docs/scribe` beneath `/work/acme`, with `{"prompt": "scribe.md", "title": "Scribe", "name": "_SCRIBE", "effort": "xhigh"}` and `.professor/scribe.md` holding `You are scribe.` Its key is `acme › Scribe`.

## Discovery and ownership

`Discover` walks depths 1–6 below each managed root, descends into `.professor/`, `.workbenches/`, and nested git repositories, skips other dot-directories, `node_modules`, `vendor`, `venv`, and follows no symlink. It sorts benches by directory and returns `WalkError` separately; a failed directory does not stop other branches. Its text is `workbench discovery under {Root} could not read {Path}: {Err}`. `ManagedRoots` deduplicates the nearest managed ancestors. Source: `pfm/internal/workbench/discover.go`.

`Owner` and `Nearest` pick the nearest eligible bench above cwd. In a linked git worktree, `Owner` maps cwd to the main checkout at the same relative path (`gitroot.MainCheckout`), so the picker groups the chat under the main checkout's bench; `Nearest` finds the managed root through that main-checkout path, then walks and loads the worktree's own bench at the same relative path, whose prompt and mirror the launched engine reads (a bench only on the worktree's branch launches on its persona; a bench missing from the worktree gives none); discovery and lookup share `eligibleBench`. `Project` is the label the picker gives chat rows in the managed root, `gitroot.Project(Root)`'s name: a managed root nested in a git repository groups under that repository beside its chats, and a bench inside a nested repository still groups under its managed root's project. Duplicate titles within a project use `{Project} › {dir relative to the project's repository root}` for both keys, and a key two clones of one repository still share uses `{Project} › {absolute dir}`; other keys are `{Project} › {Title}`. Sources: `pfm/internal/workbench/discover.go`, `pfm/internal/gitroot/`.

## Persona precedence

`ForLaunch` reads the owning manifest and prompt. Invalid manifests refuse both `New` and `Resume`. An explicitly requested disabled engine in `New` refuses with `workbench {dir} does not enable {word}: add "{word}" to "engines" in {manifest path}`; a disabled engine in `Resume` gets a zero persona and the fleet prompt. Source: `pfm/internal/workbench/persona.go`.

| value | explicit | workbench | fleet |
| --- | --- | --- | --- |
| prompt | Claude `--harness-prompt`/per-socket record or bare `--system-prompt-file`; bare Codex `-c developer_instructions=…` | Manifest prompt through the engine's system channel. | Account/config/machine prompt policy. |
| effort | `--effort` or `-c model_reasoning_effort=…` | Manifest `effort`. | No bench default. |
| model | `--model` or `-m` | Manifest `model`. | No bench default. |

Claude carries `--system-prompt-file {abs}`. Codex carries the body through `action.CodexDeveloperInstructionsArg`. OpenCode carries only the prompt: `OPENCODE_CONFIG_CONTENT` adds the manifest prompt to `instructions`, which OpenCode unions across config layers, and names the staged plugin; `PFM_OPENCODE_SYSTEM_FILE` names the manifest prompt and `PFM_OPENCODE_FLEET_FILE` the composed fleet prompt `pfm install` lists in OpenCode's machine `instructions`. `WorkbenchSeat` removes that fleet block from `output.system` in `experimental.chat.system.transform`; the provider prompt, environment, `AGENTS.md` instructions and the operator's other entries stay. It throws when the workbench prompt is unreadable or blank, and when a request carries the workbench prompt while OpenCode lists a fleet prompt whose text it cannot find. Sources: `pfm/internal/action/persona.go`, `pfm/internal/action/headless.go`, `pfm/internal/hookentry/codex_launch.go`, `pfm/internal/workbench/opencode_seat.go`, `pfm/internal/installer/opencode_instructions.go`.

`--agent-role` composes onto the explicit harness, else workbench prompt, else fleet prompt. `BasePrompt`, `ResolveSeatPrompt` and `RefreshSeatPrompt` share this base across new seats and reloads. An inherited Claude branch model is kept; absent one, the bench model applies. Sources: `pfm/internal/agentrole/base_prompt.go`, `pfm/internal/agentrole/agentrole.go`, `pfm/internal/action/headless.go`.

An omitted engine in `chat new` / MCP `chat_new` keeps the current engine when enabled with an account, else `PickEngine` chooses the first enabled engine with an account. An explicit disabled engine is refused. Sources: `pfm/internal/workbench/engine_pick.go`, `pfm/cmd/pfm/chat_new_command.go`, `pfm/internal/mcpserv/actions.go`.

## Launch doors

| door | mode | source and route |
| --- | --- | --- |
| Picker ✦ new Claude | `New` | `pfm/internal/action/persona.go`, `pfm/internal/action/synth.go`: `Executor.Open` / `OpenDetached` → `Synthesize(NewClaude)`. |
| Picker resume/agent, `chat open`, MCP `chat_open`, reminder | `Resume` | `pfm/internal/chat/open.go`, `pfm/internal/action/executor.go`, `pfm/internal/action/open_detached.go`, `pfm/internal/action/synth.go`: `Synthesize(ResumeClaude, Agent, ResumeCodex, ResumeOpenCode)`. |
| Picker ✦ new OpenCode | `New` | `pfm/internal/action/persona.go`, `pfm/internal/action/synth.go`: mirror/plugin preparation → `Synthesize(NewOpenCode)`. |
| Picker ✦ new Codex, bare `cx` | `New` or `Resume` for resume/continue/fork | `pfm/internal/action/persona.go` checks the picker request; `pfm/internal/hookentry/codex_launch.go`: `CodexLaunch` builds the mirror and inserts persona flags. |
| Bare `claude`, `internal launch`, `internal claude-launch` | `New` or `Resume` for resume/continue/fork | `pfm/internal/hookentry/launch.go`, `pfm/internal/hookentry/claude_launch.go`, `pfm/internal/action/synth.go`, `pfm/internal/claudelaunch/render.go`: `Launch` → `LauncherRunAs`. |
| agent-open | `Resume` | `pfm/internal/agentopen/real.go`: `ExecCommands.Resume`. |
| `pfm chat new`, MCP `chat_new` | `New` | `pfm/internal/mcpserv/actions.go`, `pfm/cmd/pfm/chat_new_command.go`: `PlanClaude` / `PlanCodex`. |
| `pfm chat reload` prompt | `Resume` | `pfm/internal/agentrole/agentrole.go`: `RefreshSeatPrompt`. |
| `pfm chat reload` effort, model, mirror | `Resume` | `pfm/internal/reload/reload.go`, `pfm/internal/reload/workbench.go`: `Run` → `applyReloadWorkbench`, before pane locking. |
| `pfm chat branch` | `Resume` | `pfm/cmd/pfm/chat_satellite_command.go`, `pfm/internal/action/headless.go`: `HeadlessFork`. |

Attaching to an already live seat does not relaunch it. Bare non-session administrative commands pass through. `pfm headless exec`, `PurposeQuery` probes (`reap/busy.go`, agent-open queries, `chat_inject_resume.go`) and `internal/ask` one-shot runs are outside this rule. OpenCode `chat new`, reload and branch remain refused.

## Mirrors

A workbench owns its own CLAUDE.md: pfm codex build {dir} compiles it into {dir}/AGENTS.md, and without it every Codex launch in the workbench is refused.

`EnsureMirror` builds Codex/OpenCode before a persona launch; a compiler failure refuses it as `build the {engine} mirror of workbench {dir}: {error}`. `CheckMirror` separates a first stale artifact problem from a compiler error. Claude needs no mirror. Source: `pfm/internal/workbench/mirror.go`.

`codexgen` and `opencodegen` skip a child marked by a workbench manifest whether discovered or explicitly listed: each bench builds as its own root. `pfm codex build` without a root inside a bench resolves that bench rather than its git/managed parent. Builds at a bench write no home-level Codex skills/prompts or OpenCode commands. Sources: `pfm/internal/codexgen/project_discovery.go`, `pfm/internal/codexgen/compiler.go`, `pfm/internal/opencodegen/compiler.go`, `pfm/internal/opencodegen/reconcile.go`, `pfm/cmd/pfm/codex_command.go`.

Codex `.codex/agents/` is generated only from the bench's own `.claude/agents/*.md`. A bench without roles gets no empty agents directory: the role lookup can continue to the managed root. Codex and OpenCode also load ancestor `AGENTS.md` files, so the parent repo's rules reach their workbench seats. Sources: `pfm/internal/codexgen/compiler.go`, `pfm/internal/agentrole/agentrole.go` (`roleLadderRoot`).

## Picker rows

Owned rows carry `Row.Workbench = Bench.Dir` and `Row.Project = Bench.Key`. Managed-root families retain activity ordering; bench groups follow their parent in directory order. A chatless bench still gets its group. Opening the picker inside one prioritizes its family while keeping the top ✦ row at the managed root. Source: `pfm/internal/compose/workbench.go`.

The first bench row is ✦ `New {Short} chat`, with cwd at the bench and engines filtered to enabled engines with accounts in manifest order. Invalid manifests, walk failures and no available account produce `WorkbenchInvalid` notices. Killed view adds neither bench launch nor error rows. Source: `pfm/internal/compose/workbench.go`.

The bench carousel uses its row's `Engines`; a global engine absent there resolves to the first one for rendering, Enter and account cycling. The top carousel remains global. Notices render `⚠ WORKBENCH ⚠  {name}`, have inert Enter and refuse kill as `⌃X refused — the workbench error row is a notice, not a chat`. Name-fold identity is bench directory plus NUL plus prefix, keeping equal prefixes in different benches and ordinary projects apart; ordinary cross-project folds keep their existing behaviour. Sources: `pfm/internal/ui/workbench_rows.go`, `pfm/internal/ui/model.go`, `pfm/internal/ui/listpanel.go`, `pfm/internal/ui/deckrow.go`, `pfm/internal/ui/dossier.go`.

Claude ✦ launches choose a numbered name from the live/killed roster. Codex/OpenCode picker rows do not pre-name through that path. A deleted bench directory refuses open as `open: workbench directory {dir} is missing`, including detached open; plain rows retain their cwd fallback. Source: `pfm/internal/chat/open.go`.

## Cache

`paths.WorkbenchCache` names `workbenches.json` beside `CacheDB`. The version-1 cache stores bench directories/roots and discovery failures, not prompt content. `ReadCache` reloads manifests, skips deleted entries, recalculates duplicate-title keys, distinguishes missing from corrupt files, and returns saved walk failures. `WriteCache` publishes atomically. Source: `pfm/internal/workbench/cache.go`.

Picker first paint uses this cache without walking; refresh discovers benches and publishes both rows and cache. A corrupt cache produces a first frame without benches and refresh repairs it. Fleet scans opt in through `Request.Workbenches`; plain scans keep their existing rows. Sources: `pfm/internal/picker/pipeline.go` (`refreshWorkbenches`), `pfm/internal/fleet/scan.go`.

## Doctor

`printWorkbenchDoctor` resolves the managed root and walks it afresh. No managed root means no bench lines; lookup failure prints `doctor: workbench UNREADABLE {err}` and counts one failure. Source: `pfm/internal/doctor/workbench_checks.go`, wired by `pfm/internal/doctor/doctor.go`.

| output pattern | count | repair |
| --- | --- | --- |
| `doctor: workbench {dir} ok · engines {engines} · prompt {prompt}` | none | Read following mirror lines. |
| `doctor: workbench {dir} FAILED: {fault}` | failure | Repair the manifest/prompt named by the fault; no mirror check runs for it. |
| `doctor: workbench {dir} {engine} mirror STALE: {first problem} — the next launch there rebuilds it` | warning | Repair the named problem and rebuild that engine's mirror. |
| `doctor: workbench {dir} {engine} mirror BROKEN: {err}` | failure | Repair the named compiler input and rebuild. |
| `doctor: workbench discovery FAILED: {WalkError text} — whether more workbenches exist there is UNKNOWN` | failure | Restore access to the path and rerun discovery. |

## Operator manual and installation

`templates/global/commands/pfm/workbench.md` defines `/pfm:workbench new|adopt|modify|check`; `templates/global/commands/pfm.md` routes workbench requests there. Creation writes the manifest, persona, bench `CLAUDE.md`, and `.claude/settings.json` with `claudeMdExcludes: ["!**/docs/scribe/CLAUDE.md"]` and `autoMemoryEnabled: false`. Adoption keeps the existing prompt and translates effort/harness/name into the manifest before retiring the old launcher.

`pfm install` links each top-level entry in `templates/global/commands/`, so the `pfm/` directory becomes `~/.claude/commands/pfm` beside `~/.claude/commands/pfm.md`. Project-local `/pfm:release` remains in the project's own command directory. Source: `pfm/internal/installer/installer.go` (`wireGlobalCommands`). The refresh map marks the manual curated.
