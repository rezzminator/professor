# Inventory — engines, philosophy, release history

Tracer report, 2026-09-13, HEAD `00da35b5`. Raw map, no verdicts. Telemetry: 4 tracer threads dispatched → 4 received.

Since this pass: `engines/` is `workflows/`, and the `wave-walker` engine (§ (1) below) is retired — `/wave:walker` now dispatches the `tracer` and `reviewer` agents and folds their reports; only `workflows/deep-rr/` remains. § (1)'s wave-walker map describes the retired engine as it was at this HEAD.

## (1) Engines

### `engines/wave-walker/` — no README at this path; source of truth is `engines/wave-walker/engine/design.md`

Pipeline stages/agents (`engines/wave-walker/engine/design.md`):

- `:49` SCOUT (1 agent, +1 corrective retry) — threads + sensor jobs + gate files + auth rule
- `:53-57` BARRIER 1 (parallel): threadWalker, sliceSensor, gateSweep, securityAuditor, invariantHunter
- `:63-65` BARRIER 2 (parallel): anomalyJudge, territoryDigest, coverageCritic
- `:69` SECOND OPINION (opus, chunks of 4)
- `:71` FINAL JUDGE (1, opus)
- `:74` FOLD (1) — writes review, returns ledger
- `:81-101` full 19-seat roster: scout, threadWalker, sliceSensor, gateSweep, securityAuditor, invariantHunter, coverageCritic, anomalyJudge, territoryDigest, secondOpinion, finalJudge, fold, claimExtractor, claimVerifier, consistencyJudge, probe, brainer, claimAuditor, synthesiser
- Other modes: `:37` manifest-verify, `:38` verify, `:39` investigate (alongside `walk`)

Runtimes compiled to:

- `package.json:6` — "Wave Walker engine — one modular source compiled by cross-workflow for Claude Workflow and the Codex SDK"
- `cross-workflow.config.js:18` — "The authoritative four-mode Professor Wave Walker compiled from one source for Claude Workflow and the Codex SDK."
- `cross-workflow.config.js:58-62` — Claude models: opus (judgment), sonnet (execution), haiku (collector)
- `cross-workflow.config.js:63-88` — Codex models: gpt-5.6-sol (judgment), gpt-5.6-terra (execution), gpt-5.6-luna (collector)
- `build.js:41-45` — CROSS-RUNTIME COMPILE: native Claude compiler emits `dist/cross-workflow/claude/workflow.js`; the Codex compiler emits `dist/cross-workflow/codex/runner.mjs`

The `cross-workflow` mechanism:

- `cross-workflow.config.js:7` imports `defineHarnessProgram` from `'cross-workflow'`
- `cross-workflow.config.js:9-102` exports `createWaveWalkerDefinition(source)`: sourceSha256 (`:10`), `defineHarnessProgram()` with definitionKind, irVersion, programVersion, id, version, description, four inputSchema/outputSchema variants (FAILED/investigate/verify/walk), Claude+Codex models, modelAliases, effortAliases, requires, source, sourceSha256, sourceProvenance
- `build.js:60-63` imports `compileHarnessProgramClaudeNative`, `compileHarnessProgramCodexNative`
- `build.js:315-327` — definition → Claude bundle + manifest.json; → Codex runner + manifest.json
- Pinned compiler vendored at `engines/wave-walker/engine/vendor/cross-workflow-0.2.0.tgz`

Other top-level files:

- `activate.js:1-3` — atomically points `dist/active-workflow.js` at the pinned legacy bundle or a proven cross-workflow candidate
- `build.js:1` — dependency-derived concatenating bundler
- `code-read-fence.mjs:2-3` — PreToolUse(Bash) fence for the read-only Wave Walker caller
- `equivalence.js:1-5` — legacy vs cross-workflow Claude bundles on identical deterministic input; promotion gate
- `headless-equivalence.js:1-5` — live old-vs-new proof through separate headless Claude sessions
- `headless-equivalence-lib.js:1` — equivalence evidence validation
- `production-caller.js:1` — `CODE_READ_FENCE_SCRIPT`, `claudeConfigRoot()`, `CODE_READ_BUILTIN_TOOLS`
- `production-walk.js:1-4` — transcribes one real production walk into the promotion gate's artifact
- `production-walk-evidence.js:1-3` — `assertProductionWalkEvidence()`
- `validate-bundle.js:1-5` — enforces the Workflow sandbox contract on a bundle
- `verify.js:1-3` — rebuilds every cross-workflow target in a disposable dir, byte-diffs against committed candidates

Subdirs: `src/` — agents/, types/, utils/ + batching.ts, config.ts, constants.ts, engine.ts, index.ts, ledger.ts, meta.ts, rules.ts, runtime.ts. `test/` — fixtures/ + 13 `.test.ts`. `vendor/` — cross-workflow-0.2.0.tgz.

### `engines/deep-rr/` (README.md, design.md, SKILL.md, CLAUDE.md, workflow.js, midrun.js, persist.js)

Pipeline stages/agents:

- Scout swarm: README.md:17 PLANNER, :18 PROBE (haiku per angle), :20 MERGER — design.md:34-36 Planner (sonnet/high), Probes (haiku/medium parallel), Merger (sonnet/high, no tools)
- README.md:22-23 Prospector (Opus) — design.md:44
- design.md:50 Brainer (Opus/xhigh, delta-driven COORD contract)
- Research waves: README.md:25 Scheduler (sonnet), :26 reader (haiku), :28 claimAuditor (haiku), :29 lineageClerk (sonnet), :35 DERIVATION (stored Python), :36 rerunner (haiku), :38 validator (sonnet) — design.md:60-70 plus `:61` bin-packing (code), `:70` ⏺CKPT (code)
- Finalize: README.md:40 initiator, :41 refine, :42 JUDGE (Opus), :42 synthesiser — design.md:87-90 Initiator (opus/xhigh), Refine (sonnet per group), Judge (opus/xhigh, ≤3 passes), Synthesiser (opus/xhigh)
- README.md:50 Debug mode (opt-in)

Runtime: compiles to the Claude Code Workflow sandbox only (README.md:5, :85, :96, :108; SKILL.md:35-38). No Codex/cross-workflow mention anywhere in `engines/deep-rr/` — positive absence, confirmed by grep.

SKILL.md frontmatter (`:1-6`): `name: deep-rr`, `version: "3.2.4"`, "USER-ONLY — the user asks for it by name; never start a run inside another task." Triggers: `:69` `rr fast <query>`, `:89` `RR <question>`.

CLAUDE.md:1-7 — "RR ("Research and Report") — a standalone Claude Code skill: a deterministic background Workflow that runs a brainer-steered web crawl over a quote-pinned claim ledger and derives a cited answer."

`midrun.js:502` `function main()` plus ~19 utilities (fingerprint, clip, fmtAge, statMtime, hhmmss, listSection, parseArgs, resolveGivenPath, autoDiscoverJournal, getRepoRoot, loadJournal, classify, splitPending, mergedLandscape, computeFlags, inferPhases, buildStatusReport, buildFindingsReport, buildHeadline). `persist.js` — no top-level function/export matched; FRONTIER.

## (2) Philosophy — every named principle, document order

### docs/BLUEPRINT.md (377 lines)

- `:1` "BLUEPRINT — The Philosophy" — the foundational discipline/character text.
- `:18` "Personality is load-bearing" — character/voice is structural, not decorative.
- `:18` "transplantable nervous system" — a portable rule/role architecture that moves across projects.
- `:22` "The three-tier framework" — Tier A (universal), B (opt-in domain), C (invisible plumbing).
- `:32` "The cast (Tier A — universal)" — archetypal roles working in any domain.
- `:34` "The Professor" — polymath orchestrator persona, root voice.
- `:35` "/pfm" — meta-engineer that edits the pipeline at source (surgery, not journaling).
- `:40` "Bundled commands" — framework-supplied commands shipped with the blueprint.
- `:42` "the framework bus" — release flow that publishes the blueprint and manages project adoption.
- `:43` "/wave:refine" — refinement narrowing specs to zero-gap.
- `:44` "/wave:walker" — end-to-end functional/hygiene walk whose report gates the merge.
- `:44` "train protocol" — the coordinated wave testing/review sequence.
- `:45` "/wave:ccc" — Control & Command Center, standing command over a running train.
- `:46` "/rnd" — project-scope research lifecycle (open/continue/verify/land).
- `:47` "/tokens" — per-agent/per-workflow token spend attribution ranked by cost.
- `:48` "quality gates" — `/quality:*` commands enforcing reference shapes, prompts, descriptions, markdown.
- `:49` "Sweep Mode" — active dead-code removal behind QA in hygiene audits.
- `:53` "deep-rr" — in-tree research protocol.
- `:54` "architecture-design" — codebase layout designed for agent maintainers.
- `:62` "The optional cast (Tier B — opt-in at install)".
- `:64` "/officer" — compliance enforcer with selectable regulations.
- `:65` "/mentor" — business advisor for chosen market/jurisdiction.
- `:66` "/marketer" — visibility strategist for chosen channels/language.
- `:70` "The plumbing (Tier C — invisible)" — infrastructure agents/mechanics, no character.
- `:81` "The five load-bearing walls".
- `:85` "Only `gitter` touches git" — single git operator.
- `:95` "QA gates the merge".
- `:99` "Path variables, not hardcoded paths".
- `:117` "Worktree isolation per pipeline".
- `:128` "Self-improvement at the source" — `/pfm` edits agent definitions directly to fix bug classes.
- `:134` "The non-negotiable rules" — ten enforceable rules (no cowboy coding, QA-first, single git operator, no hardcodes, no force-push, no mocking internal deps, fail-loud, …).
- `:151` "Pipeline architecture" — planner → architect → developer → QA → merge → post-QA → audit → main-loop docs merge → gitter DOCS-COMMIT.
- `:220` "Meta path: `/pfm`".
- `:224` "File layout".
- `:274` "What you get out of the box".
- `:276` "self-disciplined engineering team with character".
- `:279` "pipeline that refuses cowboy coding".
- `:281` "Analysis Protocol" — cross-disciplinary reasoning embedded in the Professor prompt.
- `:283` "Optional dual-runtime".
- `:284` "Path conventions that scale".
- `:285` "Documentation discipline".
- `:286` "Memory backup" — SessionEnd hook syncs persistent memory to private git.
- `:316` "Optional: Codex dual-runtime" — Claude for judgment, Codex for heavy implementation.
- `:323` "pointer layer" — `.codex/` translates mechanics over local sources without restating them.
- `:326` "Professor contract" — the same behavioral contract mirrored across both runtimes.
- `:343` "Staying current — the update mechanism".
- `:355` "Review and adopt project-template changes" — `pfm update check`.
- `:371` "The smell test" — personality must survive domain change.

### docs/ARCHITECTURE.md (116 lines)

- `:1` "ARCHITECTURE — source, render, install, update".
- `:18` "The pipeline" — SOURCE → RENDER → INSTALL → UPDATE.
- `:21` "SOURCE" — plain files where universal; templates where harness/project differs.
- `:25` "RENDER" — in-repo build step; outputs committed, verified at release.
- `:28` "INSTALL" — only `pfm` writes fleet-installed paths.
- `:31` "UPDATE" — pull/build/apply; installed assets always match their binary.
- `:36` "Ownership law — one writer per installed path".
- `:38` "Backup, never destroy".
- `:47` "Stable addresses" — `~/.local/bin/pfm`; `~/.local/share/pfm/install/`.
- `:51` "The harness axis — one source, per-harness renders".
- `:53` "pointer-stub mirror" — named as what NOT to do.
- `:55` "{{#if claude}} / {{#if codex}}" — template conditionals.
- `:59` "Semantic divergence is authored, not translated".
- `:61` "Values, not forks".
- `:65` "Render manifest" — hash of every artifact.
- `:67` "Tiers" — host executable, host assets, repo files.
- `:77` "Update flow".
- `:87` "Origin flow — the maintainer's repo is an adopter".
- `:92` "refresh-map.json" — migration ledger.
- `:97` "Migration state".
- `:107` "Safety canon" — dry-run default, backup-over-destroy, one source line, leak gate, immutable tags.

## (3) Release history — headline features

CHANGELOG `## Releases` (lines 40-145): 94 bullets = 94 files in `releases/`, no coverage gap. Eight most recent read in full; the remaining 86 covered by CHANGELOG one-liner only (named skim-coverage).

Newest → oldest, the eight read in full:

- v0.76.0 — hiding a live chat ends it, `/exit`/`e` close the terminal tab, `chat new` inherits the caller's engine, WebGL-safe glyphs, first `develop → main` release. Added: `pfm chat reload --model M --effort E`; `/handoff --branch` detached successor chat; Limits page one `% used` scale across engines.
- v0.75.1 — `pfm install` stops restarting an unchanged launch agent (MCP daemon no longer taken down). Fixed-only.
- v0.75.0 — output styles retired at every Claude launch; `rr` persists every answer to `.professor/RR/<slug>-<date>.md`; `/rnd` project-scope; `deep-rr` gated USER-ONLY.
- v0.74.0 — [BREAKING] Harvester config in `~/.config/pfm/harvester.config.json`; two-port MCP daemon; self-hosted SearXNG fixed (#21); `chat_load` retired.
- v0.73.1 — headless cancellation verification fix. Fixed-only.
- v0.73.0 — shared Claude/Codex headless execution: system prompts, schemas, timeouts, normalized results, native streaming; macOS credential recovery; fleet diagnostics.
- v0.72.0 — chat MCP surface stops reporting work it did not do.
- v0.71.0 — [BREAKING] legacy `cc*` shell surface and `/p:360` retired; fresh Claude chats spawn natively (`action.ClaudeSpawn`); every gate names its own broken state; picker in color by default; memory helpers renamed.

Version range: v0.1.1 → v0.76.0 (94/94 accounted for).

INSTALL.md facts (verbatim):

- `:67` `pfm install --skip-harvest --skip-engine codex --skip-themes`
- `:73` `--skip-themes` suppresses source-fetched theme installation; entries from `templates/themes/sources.json`; current Tokyo Night target `~/.claude/themes/tokyo-night.json`.
- `:77` theme fetch failures are visible nonfatal skips; a locally modified theme is preserved and reported as drift.
- `:84` `pfm install --vscode` — opt-in preview: the Professor VS Code extension + the PFM default terminal.
- `:98` VS Code — links the extension into `extensions/professor` of every VS Code product present (`~/.vscode`, `~/.vscode-insiders`, `~/.vscode-oss`, `~/.vscode-server`, `~/.vscode-server-insiders`, portable), adds a `PFM` terminal profile and selects it as platform default.

## Coverage / absences / frontier

- ABSENT: `engines/rr` does not exist — `ls engines` returns only `deep-rr` and `wave-walker`. README.md's "Engines" section names `rr/` — dangling pointer.
- ABSENT: `engines/wave-walker/README.md` and `engines/wave-walker/design.md` — real path `engines/wave-walker/engine/design.md`.
- FRONTIER: `engines/deep-rr/persist.js` write behavior; `engines/wave-walker/engine/src/*.ts` stage-to-file mapping; releases v0.1.1–v0.70.0 opened only via CHANGELOG one-liner.
- Files read by lead: engine dir listings, BLUEPRINT/ARCHITECTURE heading greps, CHANGELOG 1-45, INSTALL.md greps, releases listing. By threads: wave-walker design.md (180), cross-workflow.config.js (104), package.json, build.js ranges, ten top-level headers, src/test/vendor listings; deep-rr README (134), design.md (135), SKILL.md (204), CLAUDE.md header, workflow.js/midrun.js/persist.js grepped; BLUEPRINT (377), ARCHITECTURE (116); CHANGELOG 40-145; eight release files in full; INSTALL.md grepped; docs/RELEASE.md + docs/README.md skimmed.
