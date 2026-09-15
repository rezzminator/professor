<!-- ROSTER PATTERN — the per-project child CLAUDE.md, expressed ONCE with {project} tokens.
     SETUP expands this block once per roster entry (a roster of one has NO child CLAUDE.md — its
     conventions live in the root CLAUDE.md). Substitute the entry's name, role, stack, package
     manager, test runner, and ports. Delete sections a given project does not need (a project with
     no database drops § Data Conventions; a project that only owns infra drops the two-tier test block).
     Keep ONLY the project-specific delta — NEVER re-declare a workspace rule already in root
     CLAUDE.md (anti-pattern #11). Delete this comment at install. -->

# {PROJECT_NAME} {PROJECT_ROLE}

{PROJECT_STACK}. {PROJECT_TYPING_RULES}, {PROJECT_PKG_MGR}.

## Quick Start

```bash
{PROJECT_INSTALL_CMD}
# create .env.local with the vars in § Environment
{PROJECT_RUN_CMD}
```

## Stack

- Runtime: {RUNTIME}
- Framework: {FRAMEWORK}
- Data: {DATA_LAYER} <!-- drop if the project has no datastore -->
- Testing: {PROJECT_TEST_RUNNER}
- Package manager: {PROJECT_PKG_MGR}

## Scripts

| Script | Command | Description |
| ----------- | --------------- | ----------- |
| `dev` | {PROJECT_RUN_CMD} | Dev server |
| `build` | {BUILD_CMD} | Build |
| `test` | {PROJECT_TEST_RUNNER} | Run tests |
| `lint` | {PROJECT_LINT} | Lint |
| `typecheck` | {PROJECT_TYPECHECK} | Type check |

## Code Standards

### Logging Convention

- Structured logger only (`{LOGGER_PATH}`) — never raw {RAW_LOG_CALLS}
- Scoped/bound loggers per module with context
- DEBUG at every significant path; `{LOG_LEVEL_ENV}` controls verbosity
- Derived content is still content — anything derived from {DOMAIN_NOUN} data is {SENSITIVE_DATA}. Log `X_length` or `has_X`, never the string.

### File Structure

<!-- A fenced tree or a directory table — whichever reads cleaner for this stack. Show only the
     dirs an agent must know to place code correctly; skip the obvious. -->

```
{PROJECT_TREE}
```

<!-- KEEP the next line ONLY if this project is the roster's wire-contract/schema hub; delete it
     otherwise. It is a navigation delta, never a restatement: name the hub's own consumer index
     here and leave the command to the root — e.g. "`docs/wire-index.md` summarizes the boundaries
     the consumer index covers (root CLAUDE.md § Architecture names the command)." -->

### {FRAMEWORK} Conventions

<!-- The handful of framework-specific rules that differ from defaults or encode a real gotcha.
     One canonical term per concept. No platitudes. -->

- {CONVENTION_1}
- {CONVENTION_2}

### Data Conventions <!-- drop entirely if the project owns no datastore -->

- {DATA_ACCESS_RULE}
- Always parameterized queries — never string-interpolated raw SQL.
- **Migrations are LOUD** — plain `CREATE` / `ALTER ADD COLUMN`, no `IF [NOT] EXISTS` or guard blocks; a ledger table owns run-once (checksum-verified at boot, in a transaction, per file), and a changed already-applied migration is fatal.
- **Schema ownership** — schema design/changes go through the schema-owning agent in the pipeline.
- Never read a migration by name — introspect the live DB.

### Testing Rules

<!-- Two tiers, strict separation. Root CLAUDE.md owns the cross-project mock policy, zero-tolerance
     gates, and parallel-N invariant — restate here ONLY the project-specific mechanics. -->

<!-- Only for a project with a real database + hand-written migrations; drop entirely otherwise. -->

**Database & test-data discipline (root rule applies — project delta below):**

- Missing table/column → add the migration, never a defensive `CREATE ... IF [NOT] EXISTS` in the test.
- Seed/fixture data is dev/demo data, never a unit/integration fixture.
- Immutable-reference-data exception: any system-actor row required as an FK target, seeded in the baseline migration.
- Assert schema via the database's own introspection tables — never a migration filename.
- **Restructuring migrations is schema-only-blind** — a schema-only dump drops every data-mutating statement; when squashing, carry forward every data op (or prove it moot) and re-point dependent tests.

#### Unit ({UNIT_TEST_DIR})

- {UNIT_RUNNER}; mock ALL external deps — fast, isolated
- Target ≥ 70% coverage; descriptive names

#### Integration ({INTEGRATION_TEST_DIR})

- {INTEGRATION_RUNNER}; mock only external deps, everything within 1 hop runs real
- Setup: `make -C {PROJECT} up-test && make -C {PROJECT} db-setup-test` (`{PROJECT}` = the roster entry whose Makefile owns the infra targets); each test seeds its own rows inline
- Runs `{PARALLEL_FLAG}` always — a test that fails at parallel-N is made parallel-safe, never pinned serial
- QA reports `BUG-WRONG-ENV` if any integration test loads `.env.local` as the primary env

### Environment Files

<!-- Add a row per extra tier THIS project needs beyond root's two (e.g. a demo/staging env with its
     own flag defaults) — most projects need none. -->

| File | Purpose | Infrastructure |
| ------------ | ----------------- | ----------------------------------------------------- |
| `.env.local` | Local development | {PROJECT} local — {DB_PORT} |
| `.env.test` | Integration tests | {PROJECT} test — {DB_PORT_TEST}, fully isolated |

### Access Control

<!-- KEEP only if this project serves more than one role or fences records per owner; drop it
     entirely for a project with no per-user data boundary. -->

- The auth token carries identity + role only, never {SENSITIVE_DATA}; every handler receives the resolved user from context.
- **Ownership fence (SACRED — reads AND writes):** a {SUBJECT_NOUN}'s {DOMAIN_ADJ} content belongs to the OWNING {USER_NOUN} alone (`record.ownerId === user.id`). Every path that loads or mutates a record by a client-supplied id applies the fence; {ORG_UNIT}-equality is NEVER a sufficient fence — it narrows a {ROLE_SUPER}'s roster and never widens anyone's reach.
- A default-deny floor (unauthenticated requests blocked outside an explicit allowlist) is a floor, not a replacement: every handler still opens with its own auth + ownership guard.
- A read that redacts fields per role redacts at the RESOLVER for every role it serves — a role absent from the redaction branch is a leak, not a default.

## Ethics

<!-- The project's slice of sacred ground — the {DOMAIN_ADJ}-safety red lines that apply to THIS
     surface. Concrete, not "be careful". -->

- {ETHICS_RULE_1}
- {ETHICS_RULE_2}
