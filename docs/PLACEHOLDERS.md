# Placeholder Convention — canonical map

> **The regeneration principle (MANDATORY):** a template IS the live source file, **verbatim** — same structure, same mechanics, same character, same working logic — with ONLY the project-specific _values_ swapped for the placeholders below. Do NOT abstract, skeletonize, summarize, or "genericize" the prose. Keep the code. Swap the customized variables. If a line works and carries no project-specific value, it ships unchanged.

Every regeneration agent reads this file and applies it uniformly. One canonical token per concept — never invent a synonym.

## Identity

| Source value | Placeholder |
| ---------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------- |
| the project name — current AND any former/renamed-from brand (a rename orphans the old name in the source, so scrub both) | `{PROJECT_NAME}` |
| the project tagline | `{PROJECT_TAGLINE}` |
| the project domain (its `.com` etc.) | `{PROJECT_DOMAIN}` |
| `Professor` (persona) | **keep `Professor`** — ships as default name with a "rename if you want" note; never placeholder it |
| the canonical blueprint repo `owner/repo` | `{BLUEPRINT_REPO}` |
| the blueprint repo owner (GH/GL handle) | `{GH_USER}` |
| the local blueprint clone path | `{BLUEPRINT_CLONE_PATH}` |
| the memory-vault directory name (memory-backup scripts; default base `$HOME/work/{MEMORY_VAULT_DIR}`, env override `CLAUDE_MEMORY_REPO`) | `{MEMORY_VAULT_DIR}` |

> Blueprint self-references resolve at install: a user with push access to the canonical repo targets it directly; everyone else targets their own fork.

## Project roster — structure-agnostic (N projects, single-project first-class)

The blueprint does NOT assume a fixed set of sub-projects. Structure is a **roster**: an ordered list of 1..N projects captured from the SETUP interview. A single-project repo is a roster of **one** — first-class, not a stripped path. The source this was mined from happens to have several projects; that is an _instance_ of the roster, never baked into the templates.

Each roster entry has: directory, role label, tech stack, package manager, test runner, dev port(s). Templates reference the roster with generic per-entry tokens — never the source's concrete role names:

| Concept | Placeholder | Notes |
| ---------------------------------------------------- | ----------------------- | ------------------------------------------------------------------------------------------------- |
| the project roster (the list) | `{PROJECT_ROSTER}` | 1..N entries; SETUP fills from the interview |
| one entry's directory | `{project}` | generic — used inside per-project PATTERN blocks |
| one entry's directory, uppercase/shell-var-safe form | `{PROJECT}` | e.g. `{PROJECT}_STATUS`; sibling of `{project}`. In a rule that names ONE specific entry — `make -C {PROJECT}` infra ops, the `{AI_SERVICE_NAME}` source tree, the public-site paths — it is that entry, and the install-time KEEP/INSTALL-NOTE comment beside the rule says which |
| one entry's role label | `{PROJECT_ROLE}` | the adopter's own one-line description of what the entry is; the blueprint carries no role names of its own |
| one entry's stack | `{PROJECT_STACK}` | lang / framework / ORM / UI etc. for that entry |
| one entry's package manager | `{PROJECT_PKG_MGR}` | |
| one entry's test runner | `{PROJECT_TEST_RUNNER}` | |
| one entry's dev port(s) | `{PROJECT_PORT}` | |

A roster carries no fixed roles. The only per-entry attributes beyond the fields above are ownership facts SETUP asks for — which entry owns the shared infra/orchestration (`Makefile`, containers, DB, queue), which owns the migrations dir, which holds the `{AI_SERVICE_NAME}` LLM-calling code, which serves the public site — and a rule conditioned on one of them names the entry through `{PROJECT}` plus its KEEP comment. Role-shaped tokens — a `ROLE_PROJECT` marker (infra / ai / backend / frontend / web) or a per-role `ROLE_STACK` / `ROLE_PKG_MGR` / `ROLE_TEST_RUNNER` / `ROLE_PORT` — are retired and deliberately unregistered, so the token gate fails any template that re-introduces one.

### Materialization (how SETUP expands the roster)

Templates carry per-project **PATTERN blocks** written once with the generic `{project}` tokens. At install SETUP **expands each pattern block once per roster entry**, substituting that entry's fields — so a 1-project and a 7-project adopter get correctly-sized files from the same template. Pattern sites: `agents/per-project/{developer,qa}.md` (instantiated per entry) and `worktree.sh`/`dev.sh` (which hold a `PROJECTS=(…)` array SETUP fills and iterate it).

### Single-project collapse

When the roster has one entry: the worktree is the repo root (no per-project subdir), cross-project/integration steps are skipped, routing is trivially that one project, and the multi-project framing drops to "the project." A template must read correctly at roster size 1 — never assume more than one.

### Materialization-expansion tokens (SETUP renders these per roster entry)

These are **NOT hand-filled.** SETUP renders them by expanding the per-project PATTERN blocks once per roster entry (roster size 1 = a single expansion). Each token below is an expansion product, not an interview answer.

| Token | Concept |
| ------------------------------------ | -------------------------------------------------------------------- |
| `{PROJECT_AGENT_ROSTER}` | rendered list of every per-project agent across the roster |
| `{PROJECT_PLANNER_ROSTER}` | per-roster list of planner agents |
| `{PROJECT_ARCHITECT_ROSTER}` | per-roster list of architect agents |
| `{PROJECT_DEVELOPER_ROSTER}` | per-roster list of developer agents |
| `{PROJECT_QA_ROSTER}` | per-roster list of QA agents |
| `{PROJECT_ANALYSIS_REPORT_LIST}` | per-roster analysis-report paths |
| `{PROJECT_ARCHITECTURE_REPORT_LIST}` | per-roster architecture-report paths |
| `{PROJECT_DEV_REPORT_LIST}` | per-roster dev-report paths |
| `{PROJECT_BUG_REPORT_LIST}` | per-roster bug-report paths |
| `{ROSTER_DOC_PATHS}` | space-joined roster doc directories |
| `{PROJECT_TYPING_RULES}` | per-stack typing block (one per roster entry's language) |
| `{PROJECT_TYPECHECK}` | per-project typecheck command |
| `{PROJECT_FORMAT}` | per-project format command |
| `{PROJECT_LINT}` | per-project lint command |
| `{PROJECT_INSTALL_CMD}` | per-project install command |
| `{PROJECT_RUN_CMD}` | per-project run command |
| `{PROJECT_ENV_FILES}` | per-project env-file set |
| `{HEALTH_PROBE}` | per-roster health-probe script block SETUP expands |
| `{ENV_FILE_PROVISION}` | per-roster env-file provisioning block |
| `{ENV_BOOTSTRAP}` | per-roster env-bootstrap block |
| `{POST_INSTALL_HOOKS}` | per-roster post-install hook block |
| `{STATUS_EXTRA_PROBES}` | per-roster extra status probes |
| `{DEV_PROCESS_PATTERN}` | per-roster dev-process pattern block |
| `{DEV_PREREQS}` | per-roster dev prerequisites |
| `{PORT_DEFAULTS}` | per-roster port-default assignments |
| `{SEED_PROJECT}` | ownership marker — the entry that owns seeding (`"-"` sentinel when absent); scripts only |
| `{MIGRATIONS_DIR}` | ownership marker — the migrations dir (`"-"` sentinel when absent) |

### Per-project `CLAUDE.md` decomposition (templates/project/per-project/CLAUDE.md)

This file decomposes the aggregate roster tokens above into individual bullets/headings. Real slots, register as siblings of their aggregate:

| Token | Concept | Sibling of |
| ------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------- |
| `{PROJECT_BUILD_CMD}` | per-project build command (was `{BUILD_CMD}` — see RENAME below) | `{PROJECT_INSTALL_CMD}` / `{PROJECT_RUN_CMD}` family |
| `{RUNTIME}` | "Runtime: …" bullet, decomposed from `{PROJECT_STACK}` | `{PROJECT_STACK}` |
| `{FRAMEWORK}` | stack framework name, also reused as a heading ("### {FRAMEWORK} Conventions") | `{PROJECT_STACK}` |
| `{DATA_LAYER}` | data-layer bullet, decomposed from `{PROJECT_STACK}`; close kinship to `{DATABASE}`/`{ORM}` — maintainer may fold in instead | `{PROJECT_STACK}` |
| `{UNIT_RUNNER}` / `{UNIT_TEST_DIR}` | per-tier runner+dir pair | `{PROJECT_TEST_RUNNER}` |
| `{INTEGRATION_RUNNER}` / `{INTEGRATION_TEST_DIR}` | sibling pair | `{PROJECT_TEST_RUNNER}` |
| `{PARALLEL_FLAG}` | parallel-test-execution flag | test-tier family |
| `{LOGGER_PATH}` | structured-logger module path | — |
| `{LOG_LEVEL_ENV}` | env var controlling log verbosity | `{LOGGER_PATH}` |
| `{RAW_LOG_CALLS}` | banned raw-log call name | `{LOGGER_PATH}` |
| `{CONVENTION_1}` / `{CONVENTION_2}` | framework-convention bullets | — |
| `{DATA_ACCESS_RULE}` | data-access rule bullet | — |
| `{ETHICS_RULE_1}` / `{ETHICS_RULE_2}` | domain ethics-rule bullets | `{SACRED_GROUND}` |
| `{PROJECT_TREE}` | file-structure diagram slot | — |

## Tech stack (keep the mechanics, swap the names)

A roster entry's whole stack, package manager and test runner are the per-entry `{PROJECT_STACK}` / `{PROJECT_PKG_MGR}` / `{PROJECT_TEST_RUNNER}` above. The inline tokens below name one component wherever a sentence needs just that component:

| Source tech | Placeholder |
| ------------------------------------------------------------ | ------------------------------------------------------ |
| the ORM (e.g. Drizzle, SQLAlchemy) | `{ORM}` |
| the API framework (e.g. Express, GraphQL Yoga) | `{API_FRAMEWORK}` |
| the UI framework (e.g. React Native, Next.js) | `{UI_FRAMEWORK}` |
| the LLM-orchestration framework (e.g. LangChain, LangGraph) | `{AI_FRAMEWORK}` |
| `Postgres` / `postgres.mmd` | `{DATABASE}` / keep `postgres.mmd` filename (generic) |
| cross-cutting `GraphQL`, `WebSocket`, `SQS` | `{API_PROTOCOL}`, `{REALTIME_PROTOCOL}`, `{QUEUE}` |

## External services (vendors)

| Source value | Placeholder |
| ------------------------------------------------------- | ------------------------------------------------------- |
| Gemini / Google / Vertex AI / `GEMINI_API_KEY` / `AIza` | `{LLM_PROVIDER}` / `{LLM_API_KEY}` / `{LLM_KEY_PREFIX}` |
| AssemblyAI | `{TRANSCRIPTION_SERVICE}` |
| Resend | `{EMAIL_SERVICE}` |
| `europe-west4` / EU residency | `{DATA_REGION}` |

## Ports

| Source value | Placeholder |
| --------------------------------------- | ------------------------------------------------------------------ |
| a roster entry's dev server port | `{PROJECT_PORT}` (per entry); the whole set is `{PORT_DEFAULTS}` |
| 5432 / 5433 (db local/test) | `{DB_PORT}` / `{DB_PORT_TEST}` |
| 4566 / 4567 (queue emulator local/test) | `{QUEUE_PORT}` / `{QUEUE_PORT_TEST}` |

## Domain nouns (the `{DOMAIN_NOUN}` family)

| Source value | Placeholder |
| ------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| therapist | `{USER_NOUN}` |
| patient / client | `{SUBJECT_NOUN}` |
| therapy session | `{SESSION_NOUN}` |
| clinical / therapeutic (adj) | `{DOMAIN_ADJ}` |
| therapy / clinical practice (the field) | `{DOMAIN_NOUN}` |
| the domain's "sacred ground" / do-no-harm frame | `{SACRED_GROUND}` — every domain has a hard "never do this" line; keep the frame, swap the specifics. Canonical spelling — `{DOMAIN_SAFETY}` was a duplicate spelling naming the same concept, collapsed into this one; never re-add `{DOMAIN_SAFETY}` |
| DSM-5, diagnoses, treatment recommendations, diagnostic labels | `{FORBIDDEN_DOMAIN_OUTPUTS}` — the `{SACRED_GROUND}` examples for this domain; keep the guard, swap the examples |
| CBT/DBT/psychodynamic, Jung/Rogers/Freudian, Gottman, CCRT, Rose of Leary | `{DOMAIN_FRAMEWORKS}` — keep one illustrative slot |
| clinic / SUPERVISOR / THERAPIST roles | `{ORG_UNIT}` / `{ROLE_SUPER}` / `{ROLE_USER}` / `{ROLE_ADMIN}` (top-of-hierarchy admin, above SUPER) |

## The PhDs (persona qualification)

The Professor's qualification is fixed prose — "15+ PhDs, one in whatever area the work touches" — shipped verbatim in the fleet prompt (`templates/harness-prompts/share/head.md`); no per-install discipline slots exist and SETUP collects none. `{PHD_DISCIPLINE_1..10}` (prose form `{PHD_DISCIPLINE_N}`) and `{PHD_DOMAIN_DISCIPLINE_1..5}` are dead tokens — retired, never re-add them; a refresh pass finding a discipline roster in a live persona genericizes it to the fixed line.

## Regulation / compliance

| Source value | Placeholder |
| --------------------------------- | ---------------------------------------------- |
| GDPR | `{REGULATION}` |
| EU AI Act | `{AI_REGULATION}` |
| GDPR articles (Art. 17 etc.) | `{REGULATION}` Art. `{N}` — keep the structure |
| PHI / PII | `{SENSITIVE_DATA}` |
| MDR / SaMD / NEN 7510 / ISO 27001 | `{DOMAIN_STANDARDS}` |
| Dutch / NL / BV / KVK | `{JURISDICTION}` / `{LEGAL_ENTITY_TYPE}` |
| `europe-west4` / EU | `{DATA_REGION}` |

### Tier B archetype slots

The opt-in Tier B commands ship as archetype skeletons whose domain content is one named slot each. SETUP fills these from the interview; a template must never carry the source instance's concrete answer.

| Command | Slots |
| ----------- | ------------------------------------------------------------------------------------------------------------------------------------- |
| `/officer` | `{REGULATION}`, `{REGULATION_FRAMEWORK_DOCS}`, `{ENFORCEMENT_AUTHORITY}`, `{DATA_SUBJECT_RIGHTS}`, `{INCIDENT_NOTIFICATION_TIMELINE}` |
| `/mentor` | `{MARKET_SEGMENT}`, `{JURISDICTION}`, `{LEGAL_ENTITY_TYPE}`, `{FUNDING_LANDSCAPE}`, `{REGULATORY_BODIES}` |
| `/marketer` | `{CHANNEL_LANDSCAPE}`, `{TARGET_LANGUAGE}`, `{COMPETITIVE_LANDSCAPE}`, `{INDUSTRY_CONFERENCES}`, `{AUDIENCE_VOCABULARY}`, `{USER_PERSONA}` |
| `/rnd` | `{SECONDARY_LANG}` |

A named regulator, competitor, conference, or association surviving in one of these templates is a leak, not a default — the archetype keeps the SHAPE of the answer, never the answer.

## Paths (mostly generic pipeline paths — KEEP unchanged)

Keep verbatim: `docs/agents/`, `docs/commands/` (`$CDOCS`), `docs/epics/`, `docs/dev/{builds,backlog.md}`, `tmp/flights/`, `.worktrees/`, `tmp/`, `.claude/`, path-vars `$DOCS`/`$CDOCS`/`$REFS`/`$WORKTREE`. Swap only the project-named leaves: a path rooted in one roster entry's directory → `{PROJECT}/...` (the `{AI_SERVICE_NAME}` package `src/<pkg>/` → `{PROJECT}/src/{ai_module}/...`), machine-absolute `/Users/<user>/.../<repo>/...` → `{REPO_ROOT}/...`.

## Model pins

`model: sonnet` / `claude-opus-4-6` / `model: "opus"` → keep as literal defaults but add a one-line `{MODEL_TIER}` note where a pin appears, per the existing blueprint convention. Do not churn working pins into placeholders inside prompt bodies.

The actual frontier-model name/alias (distinct from the `{MODEL_TIER}` label) → `{FRONTIER_MODEL}`.

## Codex layer is KEPT — do NOT propagate Codex-removals

The blueprint is a **dual-runtime** product (Claude + Codex). If the live source has retired its Codex layer, do **NOT** adopt that removal: for any file whose live change was _"remove a Codex section/line/reference,"_ **preserve the Codex content from the CURRENT blueprint template** while adopting every OTHER live improvement.

This makes the codex-touched files a 3-way merge — read all three:

1. **Live source** (the spine: adopt all non-Codex improvements).
2. **Current blueprint template** (re-inject the Codex sections/lines/refs that live deleted).
3. **This map** (apply placeholders).

Codex-touched shipped templates: root `CLAUDE.md` (keep the "Two-runtime team" section + `.codex/` refs), `commands/pcm.md` (keep ALL Codex-management: invariants stay at 10, Special-Ops Codex steps, codex audit scope — also fix the 34-vs-31 agent-count inconsistency to ONE consistent generic count), `scripts/format-md.sh` (keep `AGENTS.md` in the allow-list; its body is curated upstream on `rumdl` + the repo-root `.rumdl.toml` — a refresh never reverts it to a `prettier` call). Keep `AGENTS.md` references generally — it is the Codex-side mirror of `CLAUDE.md`.

## Ignored artifacts (do NOT ship, drop references)

A live-source command, script or rule the blueprint does not ship (`ignore_sources` in `templates/refresh-map.json`) is **out of scope**. When a shipped template (root `CLAUDE.md`, `dev`, `pcm`, `settings.json`) references one, drop that row/rule/line so the blueprint has no dangling pointers.

## Tokens (additional canonical entries)

These slot into the concept families above — registered here to close prior gaps. One canonical token per concept; never invent a synonym.

| Source value | Placeholder | Family |
| -------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------- | ------------ |
| the AI service's own name / codename (the source's internal AI-service brand) | `{AI_SERVICE_NAME}` | Identity |
| the test database name (e.g. `<project>_test`) | `{TEST_DB_NAME}` | Tech stack |
| transcript / case note / session record (the artifact holding `{SENSITIVE_DATA}`) | `{RECORD_NOUN}` | Domain nouns |
| illustrative persona examples — a tech artifact, a domain artifact, a domain risk (Professor opening + Model Selection examples) | `{TECH_EXAMPLE_A}` / `{DOMAIN_EXAMPLE_A}` / `{DOMAIN_RISK_EXAMPLE}` | Persona |
| the Codex model this repo defaults to (`templates/project/codex/config.toml` `model =`) | `{CODEX_MODEL}` | Model pins |
| the Codex model id named per tier in the token-ledger `PRICING` notes (frontier / spec-execution / collector) | `{CODEX_MODEL_FRONTIER}` / `{CODEX_MODEL_SPEC}` / `{CODEX_MODEL_COLLECTOR}` | Model pins |
| the Codex reasoning effort this repo defaults to (`templates/project/codex/config.toml` `model_reasoning_effort =`) | `{CODEX_REASONING_EFFORT}` | Model pins |
| the database CLI forbidden at the execpolicy layer (e.g. `psql`) | `{DB_CLI}` | Tech stack |
| the container runtime forbidden at the execpolicy layer (e.g. `docker`) | `{CONTAINER_RUNTIME}` | Tech stack |
| the cloud CLI forbidden at the execpolicy layer (e.g. `aws`) | `{CLOUD_CLI}` | Tech stack |
| the CMO character's own name (parallel to the Professor identity slot) | `{MARKETER_NAME}` | Identity |
| the artifact an ATLAS/compliance finding gets routed to (e.g. the adopter's DPIA) | `{PRIVACY_ASSESSMENT}` | Tier B |
| the persona's founding metaphor (e.g. "the couch meets the terminal") | `{DOMAIN_METAPHOR_A}` | Persona |
| the N+1-query-joke punchline concept, domain-side | `{DOMAIN_UNCONSCIOUS}` | Persona |
| the N+1-query-joke setup concept, domain-side | `{DOMAIN_DEFENSE_MECHANISM}` | Persona |
| generic tech-stack mention inside Tier-A prose (refresh.md's own documented catch-all) | `{TECH_STACK_PLACEHOLDER}` | Persona |

## Runtime metavariables — registered here so they are NOT substituted

These ALL-CAPS brace tokens appear in shipped templates but are **not install placeholders**. They are metavariables inside a format string, filled by the running command at runtime — siblings of the lowercase `{name}`, `{summary}`, `{sha}`, `{path}`, `{project}`, `{YYYY-MM-DD}` metavariables they sit beside. SETUP must never fill them; a regeneration agent must never swap them for a placeholder or invent a value for one.

They are listed because the template token gate (`dev.sh verify templates`) FAILS on any ALL-CAPS brace token absent from this file. Both classes must therefore be registered, and the two lists carry different instructions: everything above gets substituted, everything here is left exactly as written. A new token that belongs to neither list is an unruled token, and the gate is right to stop the release for it.

| Token | Owner template | Filled at runtime with |
| ------------------------------------------------------------------------- | ----------------------------------------------------------------------------------- | ---------------------------------------------------------- |
| `{SHA}` | `epics/TEMPLATE.md` | a commit sha |
| `{MB}` | `docs-commands/git/references/gitter-history.md` | a file size in megabytes |
| `{SCOPE}` | `commands/pcm.md` | the audit scope being run |
| `{CALLER}` | `commands/quality/description.md` and every description carrying a `{CALLER}-ONLY` token | the name of the only entity allowed to invoke the entry |
| `{STATUS_LITERAL}` | `commands/audit/code-hygiene.md` | an example status string literal in the code being audited |
| `{SEED_INSERTED}` / `{SEED_EXPECTED}` / `{SEED_STATUS}` / `{SEED_DETAIL}` | `commands/dev.md` | the seed progress row's counts, state, and detail |
