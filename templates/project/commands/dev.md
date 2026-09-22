---
name: dev
description: Runs the {PROJECT_NAME} local dev stack — `/dev`/`up`/`start`, `kill`/`stop`/`down`, `restart [{project}]`, `status`, `log`/`logs [{project}] [N]`, `drop` (wipe containers + DB volume), `fresh` (kill+drop+start), `clear-logs`/`cl`, `export` (DB→seed-data), `credentials`/`creds [demo]`, `iso init|pull|merge|destroy|list|{cmd} {profile}`. Route dev-environment, port and local/test-mode asks here.
argument-hint: [up|kill|restart|status|log|drop|fresh|clear-logs|export|creds|iso]
---

# Dev Environment

Manage the {PROJECT_NAME} development environment: $ARGUMENTS

Two jobs: keep `.claude/scripts/dev.sh` in sync with project state (§ Step 0), then run it and present the report. The script does the heavy lifting — infra, deps, DB creation, servers — in bash.

Route `$ARGUMENTS` to the matching `## Mode:` section; each mode heading carries its aliases. Every script run prints its markers between `---REPORT---` and `---END---` — parse that block, then report per § Canonical Report Template.

---

## Step 0 — Script Maintenance

Runs before UP and bare RESTART only; every other mode goes straight to its script call.

### 0a. Read current project state (parallel)

For every roster entry (`dev.sh` holds the `PROJECTS=(…)` array; iterate it):

- Each runnable project's manifest (`{project}/package.json` scripts section, `{project}/pyproject.toml` entry point, etc.)
- The infra-owning project's `Makefile`, if a roster entry owns the shared infra — target names
- Each project's `docs/runbook.md` (or `docs/runbook-local.md` for the infra-owning project) — ports, env vars, startup commands, health check endpoints
- `.claude/scripts/dev.sh` itself

### 0b. Compare and detect drift

Check each against its source of truth:

- Ports — one `*_PORT` variable per runnable roster entry (a non-HTTP queue-consumer project may serve an HTTP endpoint alongside its consumer, sharing the one port) against the runbooks + `.env.local` files
- Dependency install in `cmd_up()` — each roster entry's own `{PROJECT_PKG_MGR}`
- Database step in `cmd_up()` — a bare `db-create-local` is not the whole story if a project boots its own migrator: read whichever project's persistence layer owns migrations (its `migrate.ts`/`migrate.py`/equivalent) — if it is the single migration authority and seeds on boot, a second migration path here that applies the schema without writing that authority's own migration ledger causes it to re-run every migration from scratch on its own boot
- Server start commands in `cmd_up()` — each project's dev entry point, with the port forced from the script's port variable so isolated envs work
- Health-check URLs in `cmd_up()` — the runbook health endpoints on those port variables
- `check_prereqs()` — the tools the start commands invoke
- Infra calls — the infra-owning project's live `Makefile` target names; `grep -E '^[a-z0-9-]+:' {project}/Makefile` before trusting any name
- `clean_ports()` and `cmd_kill()` patterns — must match what UP launches
- Service count — add any project or service the script doesn't yet handle
- Env file bootstrap — `cmd_up()` seeds a roster entry's missing `.env.local` from that project's own `.env.local.example` and fills required secrets from the infra-owning project's secrets file; the example must carry every key the project's env/config module requires

### 0c. Update the script if drift detected

Edit only the drifted sections; keep the script's structure — functions, helpers, `set -euo pipefail`, the `---REPORT---` / `---END---` markers, output format. Then tell the user "Updated dev.sh: {what changed}". Stay silent when nothing drifted.

A new service needs all six: a start entry in `cmd_up()`, its `*_PORT` variable (skip for a non-HTTP service), a `cmd_kill()` pattern, a health check, its log file in the archival + log-reading functions, and its port in `clean_ports()`.

---

## Canonical Report Template

All modes assemble from these blocks; `{COMMAND FOOTER}` is always last.

- HEADER: the mode's one-liner, defined per mode below.
- INFRA BLOCK (DROP/FRESH, when a roster entry owns the shared infra): `Infrastructure: Docker containers: nuked + recreated | {DATABASE} ({DB_PORT}): {ready/failed} | {QUEUE} ({QUEUE_PORT}): {ready/failed} | Database: created (the migration authority migrates + seeds on boot)`
- SERVICE TABLE (UP/RESTART/DROP-with-restart/FRESH): `| Service | Status | URL |` — one row per runnable roster entry at its port/URL (`{PROJECT_ROLE}` at `:{PROJECT_PORT}`; a non-HTTP queue-consumer project shows "{QUEUE} consumer"; add a Health row for whichever project exposes `/health`). Status: GREEN=running, RED=down, YELLOW=bundling/compiling.
- STATUS TABLE (STATUS only): the same rows as `| Service | PID | Port | Status |` plus a row for any API endpoint a project exposes, then `Seed progress: {SEED_INSERTED}/{SEED_EXPECTED} ({SEED_STATUS}) — {SEED_DETAIL}`, omitted when `SEED_STATUS=unknown` or absent.
- CREDENTIALS: shown when the project seeds login credentials. Read the file named by `CREDENTIALS_FILE` unless it is `MISSING` — a flat `{ "email": "password" }` map. Derive the role from the email local-part suffix (the install's seeding convention defines the suffix→role map, e.g. `+god`→Admin, `+manager`→Manager, and per-role suffixes for {USER_NOUN}/{SUBJECT_NOUN}) and render `| Role | Email | Password |`. `MISSING` → warn "Credentials file missing — seeded on boot in LOCAL env. Check the seeding project's logs." Omit this block entirely for projects with no auth/seeding.
- COMMAND FOOTER: a `Commands:` block listing every mode named in this file's `description:` frontmatter with a one-line gloss each.

An `ERRORS` value other than `none` adds an "Errors detected" section carrying the details.

---

## Auto-Heal Escalation

Applies to UP, RESTART, DROP-with-restart and FRESH, after the report is shown. Skip it when `DEV_NO_AUTOHEAL=1` is set — that flag breaks the `/dev` → fix → `/dev` loop.

Escalate when a service is RED, `ERRORS` is not `none`, or `RESTART_RESULT=fail`:

1. Tell the user: "One or more services came up unhealthy — diagnosing now. ☕"
2. Read the last 30 lines of each failing service's log (`dev.sh` writes it to `/tmp/<repo-dir>/dev/`, named by the service's log column).
3. Report the RED service list, the `ERRORS` value and the log details; routing the fix is the user's call. A retry runs with `DEV_NO_AUTOHEAL=1` set so the loop cannot repeat.

Healthy, not failures: a bundling/compiling project YELLOW, `ALREADY_RUNNING=true`, all-GREEN with `CREDENTIALS_FILE=MISSING`.

After a fix lands on a service, `/dev restart {project}` bounces just that one.

---

## Mode: UP — `up`, `start`, (empty)

1. Run Step 0.
2. Run `./.claude/scripts/dev.sh up 2>&1` (timeout 120s).
3. Markers: one `{PROJECT}_STATUS` per roster entry (each GREEN|YELLOW|RED), `CREDENTIALS_FILE`, `ERRORS`. `ALREADY_RUNNING=true` → ask the user "Dev servers appear to be running. Kill them first with `/dev kill`?"
4. Report with header "Dev environment is up!"

---

## Mode: KILL — `kill`, `stop`, `down`

Run `./.claude/scripts/dev.sh kill 2>&1`, then report each service killed on its port variable — one line per roster entry (a non-HTTP queue-consumer project shows "({QUEUE} consumer)") — plus orphan processes cleaned and registry cleared, then `{COMMAND FOOTER}`.

---

## Mode: RESTART — `restart [{project}]`

Bare `/dev restart`: run Step 0, then `./.claude/scripts/dev.sh restart 2>&1`, and report as UP with header "Dev environment restarted (killed old servers, started fresh)."

Named service: a targeted bounce, skipping Step 0. `{project}` is any roster entry's key. Run `./.claude/scripts/dev.sh restart {project} 2>&1`, parse `RESTART_RESULT=success|fail` and `RESTARTED={project}`, and report which service bounced and its result.

---

## Mode: DROP / FRESH — `drop`, `fresh`

Both nuke the Docker containers _and their volumes_ — the local database is wiped — then rebuild the infrastructure. DROP restarts servers only if they were already running; FRESH always kills, drops and starts.

1. Run `./.claude/scripts/dev.sh drop 2>&1` or `./.claude/scripts/dev.sh fresh 2>&1` (timeout 180s).
2. Markers: `NUKE_RESULT` and `INFRA_RESULT` (success|fail); DROP adds `WERE_RUNNING=true|false` and `SERVERS_SKIPPED=true` when servers were not restarted; the UP server markers appear whenever servers were (re)started.
3. Report with the infrastructure block. DROP header: "Dev environment dropped and rebuilt from scratch."; FRESH header: "Dev environment rebuilt from scratch." A DROP without restart adds "Servers were not running before drop — infrastructure is ready, use `/dev` to start servers."
4. FRESH only, when the roster has an async queue consumer that processes seed data — after the report, launch `./.claude/scripts/drain-wait.sh` in the background: FRESH re-seeds from an empty DB, so the environment is ready only once that consumer has processed every seeded {SESSION_NOUN}. The harness wakes you on the script's exit with `DRAIN_RESULT=clean|error|timeout` — report that result when it lands. Skip for a roster with no such consumer.

---

## Mode: STATUS — `status`

1. Run `./.claude/scripts/dev.sh status 2>&1`.
2. `NO_SERVERS=true` → report "No dev servers running." Otherwise parse the `SVC=...|PID=...|PORT=...|ALIVE=...|RESPONDING=...` lines, the `*_HEALTH` markers, and the pipe-delimited `SEED_*` fields the script emits.
3. Report the STATUS variant with header "Dev server status:".

---

## Mode: LOG — `log`, `logs`

`/dev log [{project}] [N]` — `{project}` is any roster entry's key; defaults: all services, 50 lines. Run `./.claude/scripts/dev.sh log [service] [N] 2>&1` and display its output directly; the script formats the headers and error summary.

---

## Mode: CLEAR-LOGS — `clear-logs`, `cl`

Run `./.claude/scripts/dev.sh clear-logs 2>&1`, then report "Logs cleared. Current logs: removed. Archive: removed." + `{COMMAND FOOTER}`. Nothing cleared → "No logs to clear."

---

## Mode: EXPORT — `export`

Run `./.claude/scripts/dev.sh export 2>&1`, parse `EXPORT_FILES=N`, `EXPORT_ROWS=N`, `EXPORT_DIR=<path>`, and report a table of files/rows/dir plus the tables dumped and the empty ones. `EXPORT_FILES=0` → "Nothing to export — all tables empty. Run a session first."

---

## Mode: CREDENTIALS — `credentials`, `creds` `[demo]`

No script run — read the seeding project's `{project}/seeding/{profile}/passwords.json` directly, profile `demo` for `/dev creds demo` and `local` otherwise, then report the CREDENTIALS block (header "Seeded credentials ({profile}):") + `{COMMAND FOOTER}`.

---

## Mode: ISO — `iso init|pull|merge|destroy|list|{cmd} {profile}`

Fully isolated environments: each has its own worktree, Docker containers ({DATABASE} + {QUEUE}, plus any other infra the roster runs) and allocated ports, sharing nothing with the main dev environment or another isolated env. The profile (`demo` or `local`) IS the environment name, and one environment exists per profile — if `.worktrees/{profile}/` already exists, refuse: "Already exists. Destroy first with `/dev iso destroy {profile}`."

Routing for the arguments after `iso`:

- `init {demo|local}` — create the environment
- `{start|kill|restart|status|log|...} {profile}` — forward any /dev command into it
- `pull {profile}` — pull main's committed code into the worktree
- `merge {profile}` — merge the worktree branch into main
- `destroy {profile}` — tear it down completely
- `list` — scan `.worktrees/*/` for directories holding a `.dev-ports`; report each profile, its ports, and infra/server status

### ISO INIT — `/dev iso init {demo|local}`

1. Create the worktree and allocate ports via gitter SETUP, using `{profile}` as the pipeline name.
2. Write `.dev-ports` at the worktree root — dev.sh sources it and switches to ISO mode. It carries a `# Profile: {profile}` comment line (dev.sh greps that comment for the profile, not a variable) plus every variable dev.sh reads from it: read the port-discovery block at the top of `dev.sh` for the exact names, since `set -u` aborts the script on any variable it references and the file omits. Add the container names and any analytics/object-store port the steps below use.
3. Create `docker-compose.{profile}.yml` mirroring the infra-owning project's local compose (same services + images), with per-profile ports.
4. Start Docker and wait for the health checks.
5. Apply the DB schema (extensions + migrations) and create any auxiliary databases the infra needs.
6. Symlink `schema/` at the worktree root → the schema-owning project's `schema/` dir, if the project uses one.
7. Patch the env files — see [Env-Patch Procedure](#env-patch-procedure).
8. Install deps for every runnable roster entry ({PROJECT_PKG_MGR} per entry).
9. Warn if `git status` shows uncommitted changes — a worktree checks out HEAD only.
10. Report every URL and port.

### Env-Patch Procedure

Used by ISO INIT (step 7), ISO PULL (step 3) and ISO MERGE (sanitization).

**Profile-aware env loading:** each runnable roster entry loads its env per its framework's convention — one PATTERN line per entry (SETUP fills the actual mechanism, e.g. a `NODE_ENV`-selected dotenv file, an `ENV_FILE` var, a copied `.env`, or a framework-default `.env.local`). For a `demo` profile the per-project loader resolves to the `.env.demo` variant; for `local`, to `.env.local`.

So patch `.env.{profile}` in each project, perform each project's framework-specific copy/var step, and dev.sh passes the per-project selectors (e.g. `NODE_ENV={profile}`, `ENV_FILE=.env.{profile}`). The `local` profile needs no selector.

What to patch: start from each project's live `.env.local` and rewrite every value that must differ per environment — one PATTERN block per roster entry: its own port; DB connection (host/port/name/user, or a single connection URL) with `{DB_PORT}`/`{QUEUE_PORT}`/any analytics port folded into every host URL (database, all queue URLs, the {QUEUE} endpoint, cross-project peer URLs, public-facing base URLs), `DB_NAME={db_prefix}_{profile}`, and the auth secret. External-service API keys ({LLM_API_KEY}, {TRANSCRIPTION_SERVICE} key, {EMAIL_SERVICE} key, etc.) copy across unchanged.

**CRITICAL — respect each project's strict-env mode:** if a project's settings layer forbids unknown keys (e.g. a strict-env validator declaring most fields without defaults), every key present in its main `.env.local` survives into `.env.{profile}` and no key is invented — read that project's settings module for the field list, since two projects can use different names for the same concept (e.g. one project's `AWS_ENDPOINT_URL`/`AWS_DEFAULT_REGION` vs. another's `SQS_ENDPOINT_URL`/`AWS_REGION`), and the wrong spelling crashes the receiving project at startup.

Seed data for the `demo` profile rides in the worktree's HEAD checkout (the seeding project's `seeding/demo/`) — copy from main only when it is uncommitted there.

### ISO FORWARD — `/dev iso {command} {profile}`

1. Verify `.worktrees/{profile}/.dev-ports` exists.
2. Run that worktree's script — the profile's selector env var plus `.claude/scripts/dev.sh {command}` for `demo`, bare `.claude/scripts/dev.sh {command}` for `local`. dev.sh reads `.dev-ports` for the port and infra config itself (ISO_MODE).
3. Parse and report with the isolated ports.

### ISO PULL — `/dev iso pull {profile}`

1. Verify `.worktrees/{profile}/` exists.
2. Merge: `cd .worktrees/{profile} && git merge main --no-edit`
3. Re-apply the [Env-Patch Procedure](#env-patch-procedure) — the merge may have overwritten patched files; read `.dev-ports` for the values.
4. Reinstall deps for any project whose manifest (`package.json`/`pyproject.toml`/etc.) changed, using each project's own `{PROJECT_PKG_MGR}` (with that project's install flags, if its manifest needs them).
5. Report the count of commits merged, that the env files were re-patched with the iso ports and deps reinstalled, then point at `/dev iso restart {profile}`.

### ISO MERGE — `/dev iso merge {profile}`

Merges the worktree branch into main. ISO-specific files never reach main. Steps 2–6 run via gitter — one freeform dispatch carrying them verbatim (git writes are gitter-only; /dev runs no git write directly).

1. Verify `.worktrees/{profile}/` exists.
2. Sanitize, in the worktree directory:
   - `git checkout main -- {project}/.env.{profile} 2>/dev/null || true` per roster entry (plus any infra deploy env)
   - `rm -f .dev-ports docker-compose.{profile}.yml schema`
   - `git checkout main -- .env.ports {project}/.env.ports 2>/dev/null || true` per entry that carries one
3. Verify `git diff --name-only | grep -E '\.env\.(demo|local)$|\.dev-ports|docker-compose\.(demo|local)\.yml|\.env\.ports$'` comes back empty; restore anything it lists.
4. Commit: `git add -A && git diff --cached --quiet || git commit -m "chore: iso {profile} changes"`
5. Merge: `cd {repo_root} && git merge pipeline/{profile} --no-edit`
6. Post-merge check: `grep -l "{db_prefix}_{profile}\|{pg_port}\|{queue_port}" {project}/.env.{profile} 2>/dev/null` across all entries — on any match, revert those files from `HEAD~1` and commit the fix.
7. Report the branch merged into main, the commit count, that env sanitization excluded the iso-specific files and the post-merge check found no iso artifacts. The environment keeps running — destroy it separately with `/dev iso destroy {profile}`.

### ISO DESTROY — `/dev iso destroy {profile}`

Kill the servers via dev.sh, run `docker compose down -v`, then have gitter remove the worktree, delete the branch, free the ports and remove the docs.
