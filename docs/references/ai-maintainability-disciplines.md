# AI-Maintainability Coding Disciplines

Reference document backing the "Surgical changes only" and "Follow project placement conventions" rules in the Professor pipeline. Grounded in industry research (2025-2026), community patterns (Karpathy CLAUDE.md, 100K+ GitHub stars), and production case studies.

---

## Rule 1: Surgical Changes Only

**The rule:** Every changed line must trace to the current task. Do not refactor, rename, restructure, or cosmetically improve adjacent code that already works. Always fix broken code you encounter regardless of who wrote it — leaving a bug because it is out of scope is negligence, not discipline.

### Why it matters

LLM agents exhibit four failure patterns that expand change scope beyond what was requested (Karpathy analysis, Yajin Zhou case study):

1. **Drive-by improvements** — reformatting, renaming, adding comments to adjacent code
2. **Scope drift** — solving related but unrequested problems
3. **Hypertrophy** — over-abstracting beyond task scope
4. **Collateral changes** — modifying working code while fixing something nearby

Each inflates PR diffs, makes review harder, and risks introducing regressions in code that was never broken.

### The critical distinction: cosmetic drift vs. broken code

The Karpathy-canonical formulation ("don't touch what you weren't asked to touch") conflates two different things:

| Category | Example | Policy |
| ---------------------- | ------------------------------------------------------------------------------------ | ------------- |
| **Cosmetic drift** | Renaming a variable, reformatting a function, adding comments, restructuring imports | **Forbidden** |
| **Fixing broken code** | Swallowed exception, N+1 query, security vulnerability, failing test | **Mandatory** |

Cosmetic drift is forbidden because it expands blast radius without fixing anything. Fixing broken code is mandatory because leaving a known bug unfixed because it is "out of scope" is negligence — the product serves real users.

### Effectiveness and enforcement

Instructional rules alone are ~80% effective (two independent sources). Compliance degrades with CLAUDE.md length and under rapid-fire requests. Three documented failure modes:

1. Rush mode under rapid-fire requests
2. Context compaction memory decay
3. Active rule-breaking via agent risk assessment

Structural enforcement closes the gap:

| Mechanism | Determinism | Where it helps |
| ------------------------------ | ---------------- | --------------------------- |
| Git worktrees | Filesystem-level | `/build` pipeline isolation |
| Hooks (PreToolUse exit code 2) | Unconditional | Per-file/tool blocking |
| `--allowedTools` flag | Per-session | Tool restriction |
| Permission allowlists | Glob-pattern | File access control |

### Sources

- Karpathy CLAUDE.md analysis (forrestchang/andrej-karpathy-skills, 100K+ GitHub stars)
- Anthropic best practices (code.claude.com/docs/en/best-practices)
- Yajin Zhou: "Why AI Agents Break Rules" (yajin.org/blog/2026-03-22)
- Martin Fowler: Structured Prompt-Driven Development (martinfowler.com/articles/structured-prompt-driven)
- MIT Technology Review: "Rules Fail at the Prompt, Succeed at the Boundary" (2026-01-28)

---

## Rule 2: Follow Project Placement Conventions

**The rule:** Each child project's `CLAUDE.md` documents where new code goes. Do not create new directories, new architectural patterns, or new organizational structures unless the task explicitly requires it. When adding code, follow the existing naming and structure patterns in that project.

### Why it matters

Placement rules exist at four granularity levels in production AI-maintained codebases:

| Level | Example | Prevalence |
| ------------------------ | ---------------------------------------------------- | ----------- |
| Package/service boundary | "Frontend code lives only in `client/`" | Common |
| Layer within package | "src/services/ — business logic (no DB calls here)" | Most common |
| File naming convention | "kebab-case (user-profile.tsx, NOT UserProfile.tsx)" | Very common |
| Within-file structure | "exported component, subcomponents, helpers, types" | Less common |

The sweet spot is **Levels 2+3 together** — layer boundaries + naming conventions.

### Behavioral invariants over directory maps

Augmentcode's study of well-performing AGENTS.md files found that documenting repository structure in the file itself becomes a liability as it goes stale. High-performing files describe _behavior and invariants_ ("no DB calls in services layer") not directory maps.

Two authoring patterns:

1. **Reference-based:** "Follow the pattern in `packages/api/src/routers/users.ts`" — stays fresh
2. **Prescriptive:** "All UI components go in `src/components/ui`" — works for stable layer boundaries

Anti-patterns are more common than prescriptions in well-maintained files:

- "Never touch vendor/"
- "Never create circular deps"
- "Do not import directly from another package's src/ — always use the package's public API"

### Sources

- Augmentcode: "How to Build agents.md" (augmentcode.com/guides)
- GitHub Blog: "How to Write a Great agents.md" (2500 repos study)
- builder.io/blog/agents-md
- factory.ai/docs/cli/configuration/agents-md

---

## Supplementary: VSA Transition Trigger

Not a rule — a documented signal for when to reassess architecture.

No quantitative threshold exists in the literature. The hybrid pattern (layered infrastructure + VSA product features) is the 2025 consensus. Transition trigger (qualitative):

- Controllers/services with 10+ methods and diverging responsibilities
- A feature modification requires touching 4-5 separate folders/layers
- Business logic scattered across service classes

Document the trigger, not the transition. You'll know from the pain signals.

---

## Supplementary: Context Tier Architecture

Not a rule — a reference for when to add meta-documentation about how your context tiers work.

The canonical framework (arXiv 2602.20478, 108K-line system, 283 sessions):

| Tier | Label | Mechanism | Threshold for docs |
| ---- | ------------------------- | ------------------------- | ------------------ |
| 1 | Hot Memory (Constitution) | Auto-loaded every session | Always documented |
| 2 | Domain-Expert Subagents | Invoked per task | 19+ agents |
| 3 | Cold Memory (Knowledge) | Retrieved on demand | 34+ specs |

If your system works without meta-docs about the tiers, defer. Adding meta-documentation saying "here's how the context tiers work" describes infrastructure to the infrastructure. Revisit when context tier failures appear.
