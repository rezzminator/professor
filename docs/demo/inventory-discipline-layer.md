# Inventory — the discipline layer (`templates/global/**` + `templates/project/**`)

Tracer report, 2026-09-13, HEAD `00da35b5`, working tree clean. Raw map, no verdicts. Telemetry: 5 tracer threads dispatched → 5 received; 122 files read.

Kind note: the brief's enum was `agent|command|skill|hook|script|workflow|settings`. Reference/scope docs and included text fragments are marked `doc`, `reference doc`, `text fragment`, or `data` rather than force-fitted.

## templates/global/** (27 files)

| tier | kind | name | purpose (verbatim) | path |
|---|---|---|---|---|
| global | agent | architect | Fills gaps in a spec or design doc — fixes false anchors, wrong mechanisms, coupled edits, and missed failure modes in the file, then spawns fresh architects until a pass declares NO MORE GAPS. Delegate for /wave:refine R4, an $architecture-design hand-off, or "make this plan buildable". Returns pass count, deltas, and open decisions. | templates/global/agents/architect.md |
| global | agent | architect | Codex twin of architect | templates/global/agents/architect.toml |
| global | agent | reviewer | Reviews a diff range or code lane, every hunk ledgered, tests run — returns ONE line, the report path. Delegate for "review this branch/range/merge", "is this correct", or after a tracer map; the default where /code-review would be used. Modes pre-merge, post-merge; a wave dir in → REVIEW.md ledger gitter reads before merge. Read-only. | templates/global/agents/reviewer.md |
| global | agent | reviewer | Codex twin of reviewer | templates/global/agents/reviewer.toml |
| global | agent | rr | Answers one research query inline, sources cited — delegate for "rr", "quick research", "fast answer with sources" when one web search will not do and deep-rr is overkill; low effort by default, "super rr" → the caller passes effort: medium at spawn. Returns the saved .professor/RR/{slug}-{date}.md path first, then the cited answer and open questions. | templates/global/agents/rr.md |
| global | agent | rr | Codex twin of rr | templates/global/agents/rr.toml |
| global | agent | scheduler | ORCHESTRATOR-ONLY — turns queued /wave:refine specs plus a builder count N into the train at docs/dev/trains/{train}/, merging overlapping specs into one wave and flagging stale ones RE-REFINE; the orchestrator's user gate follows. Returns the train path, wave table, merge log, RE-REFINE flags, open questions. | templates/global/agents/scheduler.md |
| global | agent | scheduler | Codex twin of scheduler | templates/global/agents/scheduler.toml |
| global | agent | tracer | Maps a target's consumer tree — every writer, consumer and hop to its terminals. Delegate for "where does X go", "who feeds X", "map it now", or "walker fast" / "fast walk", on a table, wire field, prompt slot, queue message, jsonb key or API entry. Returns a raw map only — verdicts are the reviewer's. | templates/global/agents/tracer.md |
| global | agent | tracer | Codex twin of tracer | templates/global/agents/tracer.toml |
| global | settings | config | Professor global Codex defaults. Existing settings win on pfm install. | templates/global/codex/config.toml |
| global | command | context-meter | Audits context cost per surface — CLAUDE.md chain, agents, commands, skills, MCP, machine-global roster included; ranks over-limit files and savings by tokens reclaimed, `--verbose` per file. Triggers "context budget", "what's eating my context". Report-only, trims → /pfm; runtime spend → /tokens. | templates/global/commands/context-meter.md |
| global | command | h:gh | GitHub CLI, installed and authenticated on this host — use for any GitHub operation (PRs, issues, releases, repo API). | templates/global/commands/h/gh.md |
| global | command | pfm | Operates the fleet CLI — `pfm` verb map, the adopter update flow, `pfm codex build|check`, MCP-vs-shell routing. | templates/global/commands/pfm.md |
| global | command | quality:description | MANDATORY — load before writing or certifying any `description:` (command, skill, agent, MCP tool or server); the four components and their order, the caps, the cut order, naming, family-chain and invocation-class law, the MCP self-containment rules, and the Approval gate. General prompt law → /quality:prompt. | templates/global/commands/quality/description.md |
| global | command | quality:doc | MANDATORY — load before writing or restructuring any reference doc under docs/ (root or child project), and to certify one via the Approval gate (APPROVED/REJECTED); owns doc SHAPE — cluster + _index.md, ≤500-line topic files, table-vs-sections, grep-true headings, current-state only. Prose → /quality:prompt; a `description:` → /quality:description; markdown mechanics → /quality:md-forlint. | templates/global/commands/quality/doc.md |
| global | command | quality:md-forlint | Lint/format markdown — `check [path]` reports, `fmt [path]` rewrites, `prompt-safe <file>` keeps machine-read markers intact, `audit` prices the policy, `profile <path>` names a path's category. Route every markdown lint/format/style ask here; load before changing `.rumdl.toml`. Prose quality lives in /quality:prompt and /quality:doc. | templates/global/commands/quality/md-forlint.md |
| global | command | quality:prompt | MANDATORY — load before editing any LLM-consumed prompt (CLAUDE.md, agents, commands, skills); leanness plus correctness law for any prompt. `cut <file>` rewrites the target leaner in place. Harness file rules → /pfm; a `description:` → /quality:description; doc shape → /quality:doc; markdown mechanics → /quality:md-forlint. | templates/global/commands/quality/prompt.md |
| global | doc | tokens/README | Token attribution for both local agent harnesses — per-agent / per-operation for Claude Code sessions, per-session-thread for the Codex CLI (`--codex`) — parsed straight from the JSONL each harness writes locally. Zero dependencies (node: builtins only), READ-ONLY over transcripts, no network. Node 20+. | templates/global/commands/tokens/README.md |
| global | skill | tokens | Attributes runtime token spend, heaviest first — Claude Code sub-agents and Workflow runs, or Codex CLI threads with `--codex`. Flags `--all`, `--by-workflow`, `--filter <substr>`, `--detail <id>`, `--by-day`, `--since <date>`, `--top N`, `--session <id>`; `--help` lists all. Triggers "token ledger", "which agent burned the most", "what did the wave cost". Static context size → /context-meter. | templates/global/commands/tokens/SKILL.md |
| global | script | token-ledger | per-agent / per-operation token attribution for Claude Code sessions, and per-session attribution for Codex CLI sessions (--codex). Zero dependencies. READ-ONLY over transcripts. No network. | templates/global/commands/tokens/token-ledger.mjs |
| global | command | wave:builder | ORCHESTRATOR-ONLY — implements one wave from the /goal /wave:orchestrator sends (train, spec, worktree, ports), task-by-task per the spec, never re-deciding it; reports BUILD-GREEN then DONE to the orchestrator, which gates the merge on the reviewer, /wave:walker supplementing. | templates/global/commands/wave/builder.md |
| global | command | wave:ccc | USER-ONLY — /wave:ccc {train?}, default the newest under docs/dev/trains/. The standing Control & Command seat over a running /wave:orchestrator train — verifies every DONE or green claim against the tree, rules in-train escalations, dispatches only through the orchestrator. | templates/global/commands/wave/ccc.md |
| global | command | wave:refine | Writes ONE zero-gap wave spec — to docs/dev/trains/queue/{date}-{slug}.md, asking only what the code cannot answer. Chain head — refine → /wave:orchestrator → /wave:builder → /wave:walker. `poc <goal>` refines AND builds under .professor/RND/POC/{name}/; merge mode (scheduler-invoked, non-interactive) unifies two+ specs into one. Triggers "refine", "refine this/tasks/poc". | templates/global/commands/wave/refine.md |
| global | skill | architecture-design | Lays out a codebase for agent maintainers — one directory per unit of change, grep-true names, no parallel registries. Use for `$architecture-design <feature|LLM call|project|path>`, "where should X live", before /wave:refine on a feature touching 3+ directories, and every new LLM call; greenfield designs a tree, brownfield measures then migrates. Returns a design document; edits no code. | templates/global/skills/architecture-design/SKILL.md |
| global | data | sources (machine-scope skill registry) | Machine-scope (host-global) skill registry — shared once across every project on the machine, installed at the host level, never per-repo. source_fetched entries: SETUP fetches each from its canonical public repo at install. in_tree entries are registry pointers (e.g. deep-rr ships with the blueprint clone itself). | templates/global/skills/sources.json |

Global counts: agent=10, settings=1, command=10, doc=1, skill=2, script=1, data=1 → 26.

## templates/project/agents/** + commands/** (15 files)

| tier | kind | name | purpose (verbatim) | path |
|---|---|---|---|---|
| project | agent | gitter | The ONLY agent that writes git. Phases SETUP, COMMIT, MERGE, DOCS-COMMIT, PUSH, PULL, WORKTREE-CHECKPOINT, SYNC; no phase named = freeform git ask. Returns the phase confirmation. Pushes only on the user's explicit ask. | templates/project/agents/gitter.md |
| project | agent | developer | Implements code for the {project} project ({PROJECT_ROLE}) from the brief-carried task spec — worktree with allocated ports, self-QA before finishing. Spawn AFTER architect; OPEN bugs in the brief-named 6-bugs.md make it a fix loop. Returns 5-dev-report-{project}.md. | templates/project/agents/per-project/developer.md |
| project | agent | qa | Breaks the {project} project ({PROJECT_ROLE}) via unhappy paths — writes adversarial integration + compliance tests, then fixes what they expose; a fresh qa-{project} verifies, never the fixer. Scopes TARGETED, FULL (GATE-1 pre-merge), POST-MERGE (GATE-2 on main). | templates/project/agents/per-project/qa.md |
| project | command | audit:code-hygiene | Scans AI-authored code for duplication, ghost fields, dead code, deps, architecture, types, naming, quality, magic numbers — scopes `all`, `dup`, `ghosts`, `dead`, `deps`, `arch`, `types`, `naming`, `quality`, `magic`, `{project}`, `diff`, `sweep` (the one scope that removes dead code, gated by approval). | templates/project/commands/audit/code-hygiene.md |
| project | command | audit:security | Scans every attack surface by section — info-leak, injection, auth, {API_PROTOCOL}, LLM/prompt, {SENSITIVE_DATA}, health, crypto, secrets, transport, supply-chain, CI/CD, concurrency/SoD. Returns SECURITY findings by severity. | templates/project/commands/audit/security.md |
| project | command | dev | Runs the {PROJECT_NAME} local dev stack — up/start, kill/stop/down, restart, status, logs, drop, fresh, clear-logs, export, credentials, `iso init|pull|merge|destroy|list|{cmd} {profile}`. | templates/project/commands/dev.md |
| project | command | marketer | The CMO for {MARKET_SEGMENT} — scopes seo, copy, content, landing, compete, social, pitch/sales, email, conference, channel, persona, brand, funnel; `audit`; `wave` hands tasks to /wave:refine. | templates/project/commands/marketer.md |
| project | command | mentor | Blunt, numbers-driven startup consulting for {MARKET_SEGMENT} — formation, tax, funding, gtm, competition, hiring, regulation, insurance, exit, mvp, plan/roadmap, expansion, ip, finance, pitch; vision/stress-test. | templates/project/commands/mentor.md |
| project | command | officer | Privacy and compliance counsel — {REGULATION}, {AI_REGULATION}; `audit [data-flow|codebase|architecture|infrastructure|documentation|all]`, advisory, drafting (DPIA/DPA/ROPA/ToS/privacy policy), incident, certification. Writes law, never code. | templates/project/commands/officer.md |
| project | command | pcm | MANDATORY — route every framework or process-file change here; owns CLAUDE.md, .claude/** and the .codex/ mirror. `audit [scope]` runs the read-only pipeline audit; `retro` folds the steering-conscience inbox. | templates/project/commands/pfm.md |
| project | command | rnd | Runs research on {AI_SERVICE_NAME} LLM calls under .professor/RND/<call>/<N>-<slug>/, executing its own run; new, continue, verify, land (user-ratified only). | templates/project/commands/rnd.md |
| project | command | wave:live | Batches a task list on `main` — no worktree; parallel builds, qa-{project} tests per touched project, one docs pass + gitter commit, then /wave:walker with inline remediation. | templates/project/commands/wave/live.md |
| project | command | wave:orchestrator | Runs a wave train end to end — after /wave:refine, the scheduler agent writes docs/dev/trains/{train}/ and the user approves the table; each wave goes to /wave:builder and lands through a reviewer-gated gitter MERGE. `resume {train}`. | templates/project/commands/wave/orchestrator.md |
| project | command | wave:walker | Verifies a landed wave end-to-end — every changed flow, seam and field walked to its terminal; supplements the reviewer gate, never replaces it. Auto after /wave:live W6; `/wave:walker {report-path}` by hand, `branch` for a pre-merge worktree diff; `walker fast <mission>` / "fast walk" → the tracer agent. Returns the verdict written into the wave report. | templates/project/commands/wave/walker.md |

Counts: agent=3, command=12 → 15.

## templates/project/skills/** + codex/** (18 files)

| tier | kind | name | purpose | path |
|---|---|---|---|---|
| project | skill | legal | Index of curated legal/compliance playbooks — DPA (Art. 28), DPIA (Art. 35), breach response (Art. 33/34), privacy notices (Art. 13/14), vendor due diligence, red-team QC, NDA/risk triage, statute reading, pre-delivery self-check, skill-optimizer. Formal compliance assessment → /officer. | templates/project/skills/legal/SKILL.md |
| project | skill reference doc | GDPR Breach Response (Art. 33 & 34) | | templates/project/skills/legal/references/breach-response.md |
| project | skill reference doc | DPA Drafting Guide (Art. 28) | | templates/project/skills/legal/references/dpa-drafting-guide.md |
| project | skill reference doc | DPIA Building & Risk Register (Art. 35) | | templates/project/skills/legal/references/dpia.md |
| project | skill reference doc | NDA Triage & Legal Risk Assessment | | templates/project/skills/legal/references/nda-risk-triage.md |
| project | skill reference doc | Pre-Delivery Self-Check — Adversarial QC Before Any Legal Document Ships | | templates/project/skills/legal/references/pre-delivery-self-check.md |
| project | skill reference doc | Privacy Compliance & DPA Review ({REGULATION} Art. 28) | | templates/project/skills/legal/references/privacy-compliance-dpa.md |
| project | skill reference doc | Privacy Notice & Policy (Art. 13/14) | | templates/project/skills/legal/references/privacy-notice-policy.md |
| project | skill reference doc | Red Team Verifier — Adversarial QC on Legal Drafts | | templates/project/skills/legal/references/red-team-verifier.md |
| project | skill reference doc | Skill-Optimizer — Mine a Session's Mistakes, Patch the Skill | | templates/project/skills/legal/references/skill-optimizer.md |
| project | skill reference doc | Statutory Interpretation — Reading a New Statute Correctly | | templates/project/skills/legal/references/statute-analysis.md |
| project | skill reference doc | Vendor / Sub-Processor Due Diligence | | templates/project/skills/legal/references/vendor-due-diligence.md |
| project | data | sources | Project-scope source-fetched skills; SETUP fetches each from its canonical public repo at install. | templates/project/skills/sources.json |
| project | doc | Codex Integration Layer | | templates/project/codex/README.md |
| project | settings | config | Repo-level Codex config — loads only when this project is trusted in ~/.codex/config.toml. | templates/project/codex/config.toml |
| project | settings/rule file | repo-law | {PROJECT_NAME} repo law at the execpolicy layer — loads for EVERY trusted Codex session in this repo. | templates/project/codex/rules/repo-law.rules |
| project | skill (Codex twin) | chat | Messages the tmux agent chats through `pfm chat` — inject, whoami, self-compact. | templates/project/codex/skills/chat/SKILL.md |
| project | skill (Codex twin) | wave-builder | ORCHESTRATOR-ONLY — the {PROJECT_NAME} builder lane for Codex; maps Codex mechanics onto the binding /wave:builder protocol. | templates/project/codex/skills/wave-builder/SKILL.md |

## templates/project/docs-agents/** + docs-commands/** + epics/** + per-project/** + rumdl-policy.toml (15 files)

| kind | name | heading | path |
|---|---|---|---|
| doc | _index | Documentation Hub | templates/project/docs-agents/_index.md |
| doc | standards | Architectural Standards | templates/project/docs-agents/standards.md |
| doc | build-reference | /wave:builder Reference | templates/project/docs-commands/build/references/build-reference.md |
| doc | qa-commons | QA Commons — shared rules for the pipeline QA gates | templates/project/docs-commands/build/references/qa-commons.md |
| doc | gitter-history | Gitter — History & Large-File Registry | templates/project/docs-commands/git/references/gitter-history.md |
| doc | gitter phase cards | DOCS-COMMIT, MERGE, PUSH, SETUP, WORKTREE-CHECKPOINT + SYNC | templates/project/docs-commands/git/references/gitter-phase-*.md |
| doc | debug-discipline | Debug Discipline — hangs, deadlocks, mystery failures | templates/project/docs-commands/wave/references/debug-discipline.md |
| doc | fix-core | Fix Core — the fix loop (Steps 2–8) | templates/project/docs-commands/wave/references/fix-core.md |
| doc | TEMPLATE | Epic templates | templates/project/epics/TEMPLATE.md |
| doc | CLAUDE (per-project addendum) | {PROJECT_NAME} {PROJECT_ROLE} | templates/project/per-project/CLAUDE.md |
| settings | rumdl-policy | Markdown policy for this repo — read by `rumdl`. | templates/project/rumdl-policy.toml |

## templates/project/scripts/** (17 files, all scripts)

| name | purpose (first line) | path |
|---|---|---|
| alloc-ports | Allocate unique ports for a worktree pipeline | templates/project/scripts/alloc-ports.sh |
| baseline-sync | release Step-0 gate, mechanical half. Compares .professor/VERSION | templates/project/scripts/baseline-sync.sh |
| build-codex | compile every Codex artifact from the Claude sources of truth (adopter blueprint only) | templates/project/scripts/build-codex.mjs |
| checkpoint | per-worktree audit trail at <worktree>/.checkpoint.json | templates/project/scripts/checkpoint.sh |
| codex-sync | Codex-mirror auto-compile — "always compile after framework edits" | templates/project/scripts/codex-sync.sh |
| dev | Fast dev environment manager for {PROJECT_NAME} | templates/project/scripts/dev.sh |
| drain-wait | blocking barrier: waits until a background worker/queue finishes ALL | templates/project/scripts/drain-wait.sh |
| filter-test-output | Failure-biased filter for test-runner output | templates/project/scripts/filter-test-output.sh |
| format-md | PostToolUse hook — formats the one Professor-owned .md file just written | templates/project/scripts/format-md.sh |
| git-lock | advisory lock guarding the trunk against two gitter operations | templates/project/scripts/git-lock.sh |
| guard-stamp | Session-keyed guard-marker maintenance (pfm gate) | templates/project/scripts/guard-stamp.sh |
| memory-consolidate | one-time consolidation of ~/work/<project> memory | templates/project/scripts/memory-consolidate.sh |
| memory-sync | SessionEnd hook. Syncs the WHOLE memory vault | templates/project/scripts/memory-sync.sh |
| memory-wire | SessionStart hook | templates/project/scripts/memory-wire.sh |
| notify | NO-DESCRIPTION — no header comment block | templates/project/scripts/notify.sh |
| pfm-guard | PreToolUse(Edit|Write) — guards /pcm territory | templates/project/scripts/pfm-guard.sh |
| worktree | Create and manage git worktrees for parallel pipeline work | templates/project/scripts/worktree.sh |

## Settings + hooks

| kind | name | purpose | path |
|---|---|---|---|
| settings | settings | env, MCP allow-list, tool permissions, PreToolUse/PostToolUse/Stop hook wiring | templates/project/settings.json |
| settings | settings-global | `{"cleanupPeriodDays": 36500}` only | templates/project/settings-global.json |

Hooks wired in `templates/project/settings.json` — 9 bindings across 6 groups:

| event | matcher | script |
|---|---|---|
| PreToolUse | (all) | `notify.sh start` |
| PreToolUse | `Edit|Write` | `pfm-guard.sh` |
| PostToolUse | `Read` | `guard-stamp.sh` |
| PostToolUse | `Edit|Write` | `format-md.sh` |
| PostToolUse | `Edit|Write` | `codex-sync.sh mark` |
| PostToolUse | `Bash` | `filter-test-output.sh` |
| Stop | (all) | `notify.sh stop` |
| Stop | (all) | `guard-stamp.sh stop` |
| Stop | (all) | `codex-sync.sh sync` (timeout 60s) |

## CLAUDE.md, reference cards, placeholders

- `templates/project/CLAUDE.md` — `# {PROJECT_NAME} — {PROJECT_TAGLINE}`; carries § MANDATORY Rules below.
- `docs/commands/**` reference cards: 1 — `docs/commands/pcm/references/refresh.md`.
- `docs/PLACEHOLDERS.md`: 178 unique `{TOKEN}` tokens (251 lines); single-brace syntax throughout, 0 double-brace hits.

## MANDATORY rules in `templates/project/CLAUDE.md` (verbatim first clause)

Code:
1. No secrets in any code — keys in `.env.*`
2. Never swallow exceptions: every `catch`/`except` logs the full stack trace
3. An error never renders as ABSENCE
4. AI-generated content is marked at the RENDERED SURFACE
5. Never assert by only the existence or count of data, read it
6. Validate at the entry of data, never `as`-cast it
7. Generated artifacts → `ROOT/tmp/`
8. SQL lives in ONE place: `{MIGRATIONS_DIR}` migrations (conditional: DB project in roster)
9. Surgical changes — every changed line traces to the task
10. When removing code, delete end to end like it never existed
11. Follow placement conventions
12. NO duplicatation (sic): grep for the existing function/component/hook/type/util and import it
13. Right-size and finish: simplest thing that works; no speculative abstractions; complete, no stubs

Process:
14. NEVER edit code on `main`: worktree branches only, gitter-merged after QA
15. Only gitter WRITES git
16. NEVER commit broken code or merge before QA passes
17. Only the main-loop session writes permanent docs (the owning command writes its own `docs/business/` surface)
18. Never install unvalidated libraries
19. All infra ops via `make -C {INFRA_PROJECT}` (conditional)
20. Guarded files: PreToolUse hooks gate `.claude/**` + every `CLAUDE.md` (route: `/pcm`)
21. Worktrees are costly: batch a session's related changes into one, and ask before creating one

Testing & Environment:
22. MANDATORY: load `/test` before running ANY test
23. CI verifies, never debugs

Meta:
24. Three lenses at once — Computer Science, {DOMAIN_NOUN}, Regulatory Compliance
25. AskUserQuestion is the user's whole screen
26. When in doubt, do the right thing

## Per-(tier, kind) totals

| tier | kind | count |
|---|---|---|
| global | agent | 10 |
| global | settings | 1 |
| global | command | 11 |
| global | doc | 1 |
| global | skill | 2 |
| global | script | 1 |
| global | data | 1 |
| project | agent | 3 |
| project | command | 12 |
| project | skill | 1 |
| project | skill reference doc | 11 |
| project | data | 1 |
| project | doc | 15 |
| project | settings | 4 |
| project | settings/rule file | 1 |
| project | skill (Codex twin) | 2 |
| project | script | 17 |
| project | hook (bindings) | 9 |

Files read: 122 at the time of this tracer pass; 23 have since been removed as dead: `wave/walker-invariants.md` (the wave-walker Workflow engine is retired; `/wave:walker` dispatches `tracer` + `reviewer`), `mono-architect.md`, `mono-documenter.md`, `mono-planner.md`, `qa-wrapper.md`, `rndier.md`, `role-wrapper.md`, `audit/ai-output.md`, `documenter/archive.md`, `km.md`, `pm.md`, `km-guard.sh`, `workflows/audit-ai-output-sessions.js` (orphaned once its only caller shipped), and the `/documenter` family — `commands/documenter.md` plus the nine `docs-commands/documenter/references/**` cards (doc consolidation is the main-loop session's step, the fix-core card § Step 6). Every file in the `find` sweep of `templates/global` and `templates/project` has a row. Out of scope, untouched: `templates/prompts/`, `pfm/`, `workflows/` (then `engines/`), this repo's `.claude/`, tests. Note: `notify.sh` has no header comment — `NO-DESCRIPTION` stands; `NO duplicatation` typo is verbatim in CLAUDE.md.
