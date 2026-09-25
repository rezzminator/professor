# Documentation Hub

Entry point for {PROJECT_NAME}'s documentation. This **Reference tier** records the current state of the system; the **Narrative tier** (`docs/epics/`) records why decisions were made; **Raw build artifacts** (`docs/dev/builds/`) preserve per-pipeline notes. Reference docs are clustered — read a cluster's `_index.md` (cheap), then open the one topic file you need.

Cross-project docs live under `docs/agents/`; single-project internals live under each `{project}/docs/`.

<!-- INSTALL: the cluster rows below are the standard set. SETUP Phase 2.7 creates each
     cluster directory + its _index.md from the codebase. If a cluster is deferred (Phase 2.7,
     project too new), mark its row "(deferred — bootstrap when the code exists)" instead of linking
     a file that does not exist. A single-project install keeps this same hub; "cross-project"
     simply collapses to the one project. -->

## Cross-Project Reference (`docs/agents/`)

| Area | Index | Covers |
| ------------ | ------------------------------------------------- | ----------------------------------------------------------------------- |
| Architecture | [architecture/\_index.md](architecture/_index.md) | Topology, service boundaries, integration contracts, data flow |
| API | [api/\_index.md](api/_index.md) | Operation surface, endpoints, inter-service contracts, shared types |
| System Map | [map/\_index.md](map/_index.md) | Components, workflows, DB schema, access control, ports/env/tests |
| Features | [features/\_index.md](features/_index.md) | Feature inventory by category |
| Standards | [standards.md](standards.md) | Architecture source-of-truth — invariants every architect reads (owned by `/pcm`) |

## Child Projects (`{project}/docs/`)

<!-- INSTALL: one row-group per roster project. Standard per-project docs (create as the
     project earns them): architecture/_index.md, developer-reference/_index.md,
     api-reference.md, qa-reference.md, runbook.md. Only list files that exist. -->

| Project | Index / Doc | Covers |
| --------- | -------------------------------------------------- | ---------------------------------------- |
| {project} | [architecture/\_index.md]({project}/docs/architecture/_index.md) | Project-internal architecture |

## Other Doc Tiers

- **Narrative — Epics** ([`docs/epics/`](../epics/)): initiative-level context; each has a `manifest.md` (Vision & Scope, Key Decisions, Progress Log, Discoveries).
- **Command / tooling references** (`docs/commands/{cmd}/references/`): per-command protocol + resource docs.
- **Raw build artifacts** (`docs/dev/builds/`): per-pipeline working notes from a build run.
