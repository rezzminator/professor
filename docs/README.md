# Professor Pipeline Blueprint

An opinionated multi-agent development pipeline for Claude Code — **with a full cast of characters**. It drops into **any Claude Code project, at any repo size**, regardless of language, framework, or runtime. Structure is captured at install as a **roster** of 1..N projects: a single-project repo is a roster of one (first-class — the worktree is the repo root, routing is trivial, cross-project steps drop out), and a multi-project repo lights up per-project agents and cross-project routing. The mechanics survive every stack; the characters' voices survive every domain.

This is the **transplantable nervous system** — not a config dump. Built by the Professor (the grandfatherly polymath who runs the show). The pipeline gives you the **discipline + personalities**, you parameterize the domain content (your stack, your sacred-ground concerns, your regulation, your user persona).

---

## What this gives you

A complete `.claude/` infrastructure that turns Claude Code from "an AI that writes code when you ask" into **a self-disciplined engineering team with character**:

- **The full cast** — The Professor (orchestrator), PFM, Audit, plus optional Tier B archetypes (Officer, Mentor, Marketer). The harness prompt owns the Professor voice; working prompts carry their task contracts.
- **Worktree isolation** — every feature gets its own git worktree branch + a unique port allocation. Multiple parallel pipelines on the same repo without collisions.
- **A pipeline that refuses cowboy coding** — `planner → architect → developer → QA → merge`. QA gates block bad code from reaching `main`. Only one agent (`gitter`) touches git.
- **Self-improvement at the source** — the change manager (`/pcm`) edits the pipeline rules where they live instead of accumulating "lessons learned" files nobody reads.
- **Path conventions that scale** — `$DOCS`, `$WORKTREE`, `$CDOCS` so agents never hardcode paths. Rename a directory once, every agent follows.
- **Documentation discipline** — pipeline docs are temporary and archived; only one agent writes to permanent project docs.
- **Memory backup (opt-in)** — a `SessionEnd` hook auto-syncs Claude's persistent project memory to a private repo on session end, so a machine wipe or new machine doesn't lose what Claude learned. Plain git, zero tokens.

---

## When to use it

✅ **Good fit:**

- Single-project apps — a roster of one, first-class (worktree is the repo root, routing trivial)
- Multi-project repos where features cross boundaries — per-project agents and routing light up
- Team or solo dev who keeps losing work to half-finished branches and forgotten state
- Project where "what was decided and why" matters as much as the code
- Projects where you want your agents to have a voice, not just behaviors

⚠️ **Overkill for:**

- A 200-line script
- Throwaway prototypes
- Anything where you genuinely don't care if `main` breaks

---

## What's in the box

```
docs/                     ← you are here
├── README.md, BLUEPRINT.md, ARCHITECTURE.md, SETUP.md, PLACEHOLDERS.md, RELEASE.md
└── references/

templates/
├── refresh-map.json      ← template-to-source tracking
├── project/              ← scaffolded rules, agents, commands, hooks, and engine adapters
├── global/               ← machine-global commands, agents, and skill source registry
├── prompts/              ← Claude replacement and Codex appendix
└── themes/               ← source-fetched theme registry
workflows/deep-rr/         ← bundled research skill
```

Host fleet tooling has one source outside the template tree: the Go engine under `../pfm/`, with every staged host asset under `../pfm/internal/installer/assets/`. A fresh box gets the `pfm` binary, then runs `pfm install --yes`; no separate host template bundle is copied.

---

## Quick start

1. **Read `BLUEPRINT.md`** — understand the three-tier framework and the five load-bearing walls.
2. **Run install via Claude:**

```bash
# Clone to any directory you like (use the latest release tag)
git clone --branch vX.Y.Z https://github.com/rezzminator/professor.git /path/to/professor

cd /path/to/your-project
pfm init .
claude
> Read /path/to/professor/docs/SETUP.md and follow its Install interview.
> Conduct the interview before touching any files.
```

> Replace `/path/to/professor` with a permanent clone path, conventionally `~/.professor`. Keep it: installed machine-global callers execute the engine in this clone, and `pfm update` updates the same authority in place.

`pfm init` scaffolds the project; Claude interviews you and adapts those local files. The install verification checks the selected project tooling and runtime mirrors.

For the manual path, see `SETUP.md`.

---

## The three tiers

Every command, agent, and rule in this blueprint sorts into one of three tiers:

- **Tier A — Universal archetypes** ship with FULL CHARACTER. Domain references inside (the opted-in Tier B cast, the Professor's example stack traces) parameterize per install.
- **Tier B — Domain archetypes** ship as ARCHETYPE SKELETONS with placeholders. You fill in regulation, user persona, market, knowledge domain — the voice and structure are universal.
- **Tier C — Pure mechanics** ship as INFRASTRUCTURE. No character; just role-defined plumbing.

See `SETUP.md` for the install interview and adaptation guidance.

---

## The cast at a glance

**Tier A — universal archetypes (ship with character):**

- **The Professor** — Grandfatherly polymath with 15+ PhDs, one in whatever area the work touches. Warm, precise, gently devastating. The orchestrator and root identity — lives in CLAUDE.md, not a separate command.
- **/pcm** — Professor Change Manager. Edits pipeline rules at the source; `/pfm` (machine-global) is the CLI guide.
- **/flights:{spec,orchestrate-nested,orchestrate-live,orchestrate-cross-harness,audit}, /dev** — pipeline mechanics with light Professor voice.

**Bundled commands (ship with the blueprint):** `/flights:spec`, `/flights:audit`, `/rnd`, `/quality:doc`, `/quality:prompt`, `/quality:description`, `/quality:md-forlint`, `/quality:llm-codebase`, `/quality:integration-suite`, `/audit:code-hygiene`, `/audit:security`. `/rnd` is project-scope and executes its own research run.

**Skill sources:** machine-global fetches are declared in `templates/global/skills/sources.json`; project fetches in `templates/project/skills/sources.json`. `deep-rr` lives in `workflows/deep-rr/`; `legal` is bundled under `templates/project/skills/`.

**Host tooling (opt-in):** statusline, the `pfm install --vscode` Professor VS Code extension (Professor's assistant in VS Code) with the PFM terminal made default, a Linux/macOS multi-account `/reload` (per-chat billing switch across subscriptions), and the launcher-agnostic chat fleet (`pfm` picker, `/clear` auto-kill, `pfm reap` orphan sweeper).

**Tier B — opt-in domain archetypes:**

- **/officer** — compliance enforcer. Pick your regulation(s).
- **/mentor** — business advisor. Pick your market + jurisdiction.
- **/marketer** — visibility strategist. Pick your channels + language.

---

## A note on technology

The blueprint pins your test command, build command, package manager, etc. at install time via the interview. After install, the templates are filled in for your stack — no leftover placeholders. The templates do NOT prescribe a stack; the install interview asks for one.

If you find a tech-specific assumption leaking through after install (e.g., a hardcoded `pnpm` somewhere it should be your package manager), that's a bug — open an issue or invoke `/pcm` to fix it locally.

---

## A note on character

**Personality is load-bearing, not decoration.** Strip the Professor's voice and you have a Confluence wiki. Strip Professor's cross-disciplinary depth and the analysis becomes generic.

The blueprint deliberately does NOT offer a "no character" mode. If you want sterile agents, this isn't the blueprint for you. If you want agents with voice, identity, and signature traits — refitted to your domain — read on.

---

## Origin & maintenance

This blueprint is **regenerated and published** from the live upstream source whenever its pipeline evolves. Each commit corresponds to a snapshot of a working production pipeline — not a theoretical design.

Maintained by [@rezzminator](https://github.com/rezzminator). Issues and PRs welcome — but please open an issue first to discuss large changes, since the canonical source lives in the upstream project and edits flow downstream from there.

---

## License

MIT. Use it, fork it, ship it. Attribution appreciated but not required.
