# Inventory — the harness prompt and the cross-harness mirrors

Tracer report, 2026-09-13, HEAD `00da35b5`, clean tree. Raw map, no verdicts. Telemetry: 5 threads planned, 1 failed to dispatch (concurrency cap; lead self-walked that bucket), 4 dispatched → 4 received. One line-number correction by lead: the `codex-appendix` dispatch is `pfm/cmd/pfm/main.go:396`.

## Part A — the customized harness prompt

### A.1 `templates/prompts/` contents (from `templates/prompts/README.md`)

| File | Purpose (README line) |
|---|---|
| `professor.md` | the Professor system prompt. `"systemPrompt": "professor"` makes every managed Claude launch inject it via `--system-prompt-file`. Byte-identical to the embedded installer asset `prompts/professor-prompt.md`; a Go test enforces the pairing. (README:5-8) |
| `codex-appendix.md` | a model-independent developer message from Codex's native SessionStart `additionalContext` hook. `pfm install` registers and individually trusts the owned handler in each configured account. (README:9-11) |
| `harness-original-v2.1.278.md`, `harness-opus-v2.1.278.md` | reviewed Sonnet and Opus built-in prompt baselines, captured in print mode with dynamic sections excluded. Each has a `.sha256` pin and `.model` provenance file. (README:29-32) |
| `harness-{original,opus}.model` / `.sha256` | provenance + hash pins (A.5) |
| `README.md` | the manifest for the directory |

### A.2 `professor.md` — section headings verbatim (72 lines)

```
:3   # Voice
:15  # Stance
:21  # Harness
:30  # Work rhythm
:42  # Boundaries
:51  # Model Selection
:64  # The Verdict
```

### A.3 How professor.md relates to the baselines

`professor.md` is a full replacement prompt (7 sections), structurally unrelated to either vendor baseline. The baselines (`harness-opus-v2.1.261.md`, 233 lines; `harness-original-v2.1.257.md`, 219 lines; headings `# System`, `# Doing tasks`, `# Executing actions with care`, `# Using your tools`, `# Tone and style`, `# auto memory`, `# Text output…`, `# Session-specific guidance`, `# Environment`, `# Context management`, plus `# Delivering work` / `# Corrections` on opus) are drift sentinels for the vendor's built-in prompt under the `production`/`lean` modes — never professor.md's edit source. `docs/PLACEHOLDERS.md:157`, `docs/BLUEPRINT.md:34,281`: professor.md ships verbatim.

### A.4 Install / verify mechanism

Install:

- `pfm/internal/installer/assets/prompts/professor-prompt.md` — embedded byte-identical copy; `pfm/internal/installer/prompts_asset_test.go:13-17` `TestProfessorPromptAssetMatchesShippedTemplate` pairs `professor-prompt.md`↔`professor.md` and `codex-appendix.md`↔`codex-appendix.md` (`bytes.Equal`, :29-31).
- Runtime resolution: `pfm/internal/action/synth.go:379` → `~/.local/share/pfm/install/prompts/professor-prompt.md`.
- Injection: `pfm/internal/headless/run/run.go:559-560` `--system-prompt-file`; Codex: `:588-589` `-c model_instructions_file=…`.
- Config gate: `pfm/internal/config/config.go:38-40` `SystemPromptProduction|Lean|Professor`; validated `:1021-1022`.

Verify (`pfm doctor`):

- `pfm/internal/doctor/harness_prompt_baselines.go` — `{sonnet: harness-original}, {opus: harness-opus}`.
- `pfm/internal/doctor/harness_prompt.go` defines `configuredHarnessCapture()`, `normalizeHarnessPrompt()`, and `captureHarnessPrompt()` — headless Claude against a localhost sink returning HTTP 400, so no model inference occurs (README:47).
- States, each named: BASELINE UNREADABLE → "run pfm install"; BASELINE MALFORMED; BASELINE UNAVAILABLE; CHECK FAILED ("drift unknown"); MATCHES; DRIFT ("harness instructions changed; review before re-pinning") in `pfm/internal/doctor/harness_prompt_baselines.go`.
- CLI entry `pfm/internal/doctor/doctor.go` calls `printHarnessPromptDoctor` from `Run()`.
- `pfm/internal/professor/store.go` defines `ResolveStore()`, `InspectStore()`, and `HashTemplate()`; `baseline.go` defines `Load()` and `Save()` → `~/.professor/baseline.json` (`FilePin{TemplateHash, PinnedSHA, PinnedAt}`), written by `scaffold.go`. Callers: `internal/doctor/doctor.go`, `internal/doctor/harness_prompt_baselines.go`, `internal/professor/scaffold.go`, `internal/professor/project.go`, `ask/ask.go`.

### A.5 What `.model` / `.sha256` pin (hash re-verified live: both MATCH)

```
harness-opus.model      claude-opus-5
harness-opus.sha256     00ab0f4eedd5b8a29f273929310b6711613a8a37d90b3e7f97029137de3f63db  harness-opus-v2.1.278.md
harness-original.model  claude-sonnet-5
harness-original.sha256 b0cce46877ebcfb974d87cdfdda9686d31a97b738368de0a84b272f95cb6a746  harness-original-v2.1.278.md
```

`.model` = resolved model ID of the capture (informational — README:35: model changes alone never report drift). `.sha256` = normalized hash of the named baseline. Embedded twins under `pfm/internal/installer/assets/prompts/` enforced by `prompts_asset_test.go:35-40` `TestHarnessBaselineAssetPairIsCoherent`.

## Part B — cross-harness compilation

### B.1 `pfm codex build|check|agents`

| Source | Generator | Output | Verifier |
|---|---|---|---|
| root `CLAUDE.md`, per-project `CLAUDE.md`, `.claude/agents/*.md`, `.claude/commands/**`, `.claude/skills/*` | `pfm codex build` → `codexgen.Run(ModeBuild)`, `pfm/internal/codexgen/compiler.go:83` | `AGENTS.md` (marker at `AGENTS.md:1`: `Generated by pfm codex build from CLAUDE.md; do not edit…`), `.codex/agents/{name}.toml`, `.codex/skills/`, `.codex/prompts/` symlinks | `pfm codex check` → `codexgen.Run(ModeCheck)`; drift branch `reconcile.go:127-138` (`MISSING`/`STALE`/`CONFLICT`, writes nothing) vs build branch `:145-153` (`atomicfile.Write`) |
| `templates/global/agents/*.md` | `pfm codex agents` → `codexgen.RunGlobalAgents()`, `pfm/cmd/pfm/codex_command.go:24-25→155` | sibling `.toml` — `globalagents.go:279` `filepath.Join(filepath.Dir(mdPath), name+".toml")`; render `toml.go:36`, escape `:13-17` | same in `ModeCheck` from installer `wireCodexAgents()` `installer.go:349-386` |
| `.claude/commands/**` (global) | `codexgen.RunGlobalCommands()` | `.codex` global command TOMLs | `installer.go:679` (build) / `:724` (`planCodexCommands`, check) |
| generation markers | — | `reconcile.go:13` `generatedMarker = "Generated by pfm codex build"`; `:14` legacy `build-codex.mjs` marker; `mcp.go` `mcpBegin`/`legacyMCPBegin`/`mcpEnd` fence for `.mcp.json`→`config.toml` | — |

Legacy adopter-only path: `templates/project/scripts/build-codex.mjs:200-210` maps `CLAUDE.md → AGENTS.md`; this repo's `.claude/scripts/codex-sync.sh` calls `pfm codex build`, never that script.

### B.2 `codex-appendix.md` → native Codex SessionStart hook

| Source | Generator | Output | Verifier |
|---|---|---|---|
| `templates/prompts/codex-appendix.md` | embedded 1:1 (`prompts_asset_test.go:14`) | staged `~/.local/share/pfm/install/prompts/codex-appendix.md` — `pfm/internal/codexappendix/hook.go:16-17` | `pfm/cmd/pfm/codex_appendix_command.go:9-14` `runCodexAppendix()`; dispatch `main.go:396` |
| hook body | `hook.go:59` `marker + "\n\n" + prompt`, marker `# Professor Codex appendix` | native JSON `hookSpecificOutput.additionalContext` (`hook.go:63`) | — |
| account registration | `pfm/internal/installer/codex_hooks.go:70-71` appends SessionStart hook per account; `expected_hooks.go:334` `ExpectedHook{Event:"SessionStart", Name:"codex-appendix"}` | native Codex hooks config | trust `installer.go:2153` `codexappendix.Register(...)`; removal `:2148`; `register.go:66` checks `sessionStart`, `:80` writes `hooks.state.<key>` |

### B.3 `pfm opencode build` — `.claude/**` → `.opencode/**`

`internal/opencodegen`: `.claude/agents/*.md → .opencode/agent/{name}.md`; `.claude/commands/**/*.md → .opencode/command/{flat-name}.md`; `.claude/skills/*/ → .opencode/skills/{name}` (symlink); `.mcp.json →` managed mcp object in `.opencode/opencode.jsonc`. Modes: `build`/`check`/`doctor`. Marker: `Generated by pfm opencode build from {src}`. Denies in `.opencode/opencode.jsonc`: edit deny `**/.claude/**`, `AGENTS.md`, `**/AGENTS.md`, `CLAUDE.md`, `**/CLAUDE.md`, `.opencode/**`; bash deny `git commit*`, `git push*`, `git tag*`, `gh release*`.

Trigger chain: `.claude/settings.json` Stop hook → `guard-stamp.sh stop` + `codex-sync.sh sync` (timeout 60). `codex-sync.sh`: `mark` (PostToolUse Edit|Write sets `/tmp/{project}/guard/codex_dirty`), `sync` (Stop: both `pfm codex build|check` and `pfm opencode build|check`; failure warns at turn end, exit 1, names the failed stage; the flag stays set so the next turn retries). `.claude/scripts/dev.sh` runs `pfm opencode build|check|doctor` and names the native repair command on failure.

### B.4 `AGENTS.md` compile

`CLAUDE.md:27-29`: "`AGENTS.md` is compiled from this file — never hand-edited, never a symlink; edit `CLAUDE.md` and the `Stop` hook recompiles both mirrors." Writer: `pfm codex build` (B.1). Verified: marker at `AGENTS.md:1`; `## Repo structure` present in both.

Absence: `"Three-runtime team"` has zero hits in `templates/project/CLAUDE.md`; the adopter template carries `## Two-runtime team — Claude + Codex (OPTIONAL)` at `:22` — Codex only, no OpenCode mention. The three-runtime team is this repo's own arrangement, not the adopter default.

### B.5 `.toml` twins in `templates/global/agents/`

Present: `scheduler.toml`, `reviewer.toml`, `tracer.toml`, `rr.toml`, `architect.toml`. Only `tracer.md ↔ tracer.toml` diffed (name/description match verbatim; tools/model folded into `developer_instructions` per `toml.go`). Other four: FRONTIER, not diffed.

### B.6 `templates/project/codex/`

`README.md:1-3`: ".codex/ is a pointer layer over .claude/ — holds routing and sandbox config, never protocol. Everything works on Claude Code alone; .codex/ optional." `config.toml`, `rules/repo-law.rules`: FRONTIER (not read). `skills/{chat,wave-builder}/SKILL.md`: hand-written worked-example cards, not compiler outputs.

## Dead-end ledger

- `templates/prompts/README.md` → human-facing, 0 code callers.
- `professor.md` → embedded asset → `synth.go:379` → `run.go:559-560, 588-589` → live system prompt (Claude) / `model_instructions_file` (Codex).
- `codex-appendix.md` → embedded asset → `hook.go` → Codex SessionStart `additionalContext`.
- `harness-*` baselines + pins → `harness_prompt_baselines.go:20` → `harness_prompt_doctor.go` → `doctor.go:119` terminal output; never injected.
- `professor/baseline.go` → `~/.professor/baseline.json` → `update_project.go:127` reports.
- `CLAUDE.md` → `pfm codex build` → `AGENTS.md` → OpenCode loader + Codex sessions.
- `.claude/agents/*.md` → `codexgen.Run` → `.codex/agents/*.toml`.
- `templates/global/agents/*.md` → `RunGlobalAgents` (`globalagents.go:254,279`) → `~/.codex/agents/*.toml` + `~/.claude/agents/*.md` links (see the account-fanout bug: `:168` hardcodes `.claude`).
- `.claude/{agents,commands,skills}` + `.mcp.json` → `pfm opencode build` → `.opencode/**` + `opencode.jsonc` mcp; `.mcp.json` → `codexgen/mcp.go` → `config.toml` mcp block.
- Both mirrors → `pfm codex check` / `pfm opencode check|doctor` → terminal; on failure `codex-sync.sh sync` warns at Stop (exit 1, flag kept).

## Frontier / gaps (named, not folded into "complete")

- `pfm/internal/codexgen/{config.go, globallink.go, transform.go}`, `pfm/internal/codexmeta/{header.go, session_index.go}` — header-read only.
- `pfm/cmd/pfm/codex_launch_compat.go`, `doctor_codex_pane_test.go`, `reconcile_codex_panes_jail_test.go` — boundary (fleet-pane vs compile) unresolved.
- Four `.toml` twins not diffed; `templates/project/codex/config.toml` + `rules/repo-law.rules` not read.
- `.opencode/agent/{qa,scheduler}.md` and nine `.opencode/command/*.md` pattern-inferred from four spot-checks.
- `.professor/manifest.json` (tracks the installed OpenCode projection inputs) — not independently verified.

Excluded by scope: `releases/v0.60.0–v0.61.1.md`; `.worktrees/**` duplicate checkouts.
