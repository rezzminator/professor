# GPT twins

## Runtime prerequisite

Invoke a registered twin without the Agent `model` override. A compatible gateway accepts the Claude harness's Anthropic Messages requests and routes the GPT model IDs to an OpenAI-compatible backend. The `[1m]` suffix is a harness context-window hint. [gpt-gateway](https://github.com/rezzminator/gpt-gateway), pinned as `plugins/gpt-gateway`, supplies that route; Professor supplies no credentials, endpoint or account configuration. Installation and mirror compilation are local operations, not provider calls.

## Canonical role inventory

Every row preserves its canonical manual. New twins use `gpt-6.1-sol[1m]` at `high`; existing research tiers retain `low` for `gpt-rr` and `medium` for `gpt-rr-pro`/`gpt-rr-pro-max`. Existing lightweight transcript and trace readers retain `gpt-6-luna[1m]` at `medium`.

| Canonical role | GPT registration | Source scope |
| --- | --- | --- |
| `agent-investigator` | `gpt-agent-investigator` | global declaration |
| `clone-distiller` | `gpt-clone-distiller` | global declaration |
| `collector` | `gpt-collector` | global declaration |
| `conflict-resolver` | `gpt-conflict-resolver` | global declaration |
| `documenter` | `gpt-documenter` | global declaration |
| `flights-foreman` | `gpt-foreman` | global declaration |
| `flights-foreman-sonnet` | `gpt-foreman` | shared tier coverage |
| `flights-hunter` | `gpt-flights-hunter` | global declaration |
| `flights-lander` | `gpt-flights-lander` | global declaration |
| `flights-spec-walker` | `gpt-flights-spec-walker` | global declaration |
| `flights-sub-lander` | `gpt-flights-sub-lander` | global declaration |
| `gitter` | `gpt-gitter` | global declaration + portable project wrapper |
| `imaginator` | `gpt-imaginator` | global declaration |
| `reviewer` | `gpt-reviewer` | global declaration |
| `reviewer-nt` | `gpt-reviewer-nt` | global declaration |
| `reviewer-pro` | `gpt-reviewer-pro` | global declaration |
| `rr` | `gpt-rr` | global declaration |
| `rr-pro` | `gpt-rr-pro` | global declaration |
| `rr-pro-max` | `gpt-rr-pro-max` | global declaration |
| `sub-rr` | `gpt-sub-rr` | global declaration |
| `sub-tracer` | `gpt-sub-tracer` | global declaration |
| `tracer` | `gpt-tracer` | global declaration |
| `transcript-reader` | `gpt-transcript-reader` | global declaration |
| `pcm` | `gpt-pcm` | portable project wrapper |
| `changelogger` | `gpt-changelogger` | repository-only project wrapper |
| `releaser` | `gpt-releaser` | repository-only project wrapper |
| `themer` | `gpt-themer` | repository-only project wrapper |
| `general-purpose`, `Explore` | `sol` | general worker / read-only exploration brief |

The other general workers, `luna` and `astra`, retain `gpt-6-luna[1m]` at `medium` and `gpt-6-astra[1m]` at `high`; `sol` uses sol at `high`.

## Declarations and project wrappers

Machine declarations in `templates/global/agents/{name}.md` use the existing `from:` renderer. Shared profiles under `shared/` are not agents. `shared/gpt.md` supplies `requires: [gpt-agents]`, Claude-only engine opt-outs, compaction thresholds (`230k`, `190k`, `20k`) and fail-closed child routing. `shared/gpt-flights.md` adds the final-return law and replaces the canonical Provider section. Derived artifacts live in `templates/generated/agents/`; engine-sync stamps their canonical `role`, including `role: reviewer` for the `reviewer-nt` derivative. Source declarations and wrappers never set `role`.

Project loading rejects `from:`. Each `.claude/agents/gpt-{role}.md` is a registered wrapper whose body reads `.claude/agents/{role}.md` in full, then executes that protocol with GPT-only dispatch. Its frontmatter copies the canonical tools, skills and hooks and pins the GPT model/effort. All but gitter opt out of Codex and OpenCode. Reading a manual alone does not activate its metadata. Portable `gpt-pcm` and `gpt-gitter` ship under `templates/project/agents/`; release and theme wrappers stay repository-local. The global gitter retains its delegation to the repository's canonical `.claude/agents/gitter.md` under the inherited GPT constraint.

A twin removes `effortControl` and its three effort buttons when its canonical role carries them, preserving the other tools. Canonical role bodies remain unchanged; declarations rename child registrations, not file paths or skill names such as `tracer:use`.

## Child-dispatch closure

All GPT dispatches inspect the live registry, omit explicit model overrides, propagate GPT-only routing, and return `BLOCKED: GPT role unavailable: {role}` for an unavailable or inaccessible required child. There is no Anthropic fallback and no skipped protocol step.

| Parent | Required GPT children |
| --- | --- |
| `gpt-foreman` | `gpt-foreman`, `gpt-documenter`, `gpt-pcm` |
| `gpt-flights-lander` | `gpt-flights-hunter`, `gpt-flights-spec-walker`, `gpt-flights-sub-lander` |
| `gpt-reviewer`, `gpt-reviewer-pro` | `gpt-tracer` |
| `gpt-rr` | `gpt-sub-rr` |
| `gpt-rr-pro`, `gpt-rr-pro-max` | `gpt-sub-rr`, `gpt-clone-distiller` |
| `gpt-clone-distiller` | `gpt-tracer` |
| `gpt-documenter` | recursive `gpt-documenter`; cheap mechanical checks use `sol` |
| `gpt-gitter` | `gpt-conflict-resolver` |
| `gpt-agent-investigator` | `gpt-transcript-reader` |
| `gpt-tracer`, `gpt-sub-tracer` | `gpt-sub-tracer` |
| `gpt-pcm` | `sol` for both Explore walkers and independent `model: haiku` checks; both foreman tiers use `gpt-foreman` |
| `gpt-releaser` | `gpt-reviewer`, `gpt-changelogger`, `gpt-gitter` |
| `gpt-changelogger` | `gpt-tracer` |

Helper `visibility` admits the mapped callers, and gitter's child allowlist contains `gpt-conflict-resolver`. The RR leads retain their original save law, budgets and tools. Their `RR-DIR:` comes from the existing `rr-dir` SubagentStart matcher naming all three GPT research leads (`HookRRDirMatcher`, `pfm/internal/claudelaunch/hooks.go`).

## GitGuard boundary

Neither a wrapper's registration nor compiler-stamped `role: gitter` authorizes Git writes. GitGuard exempts exactly `gitter` and `gpt-gitter` (`pfm/internal/hookentry/git_guard.go:121`). Before the first Git write, both GPT gitter surfaces prove it in one Bash call: `git -C {repo} worktree prune -n`, a dry run the hook denies to every other native `agent_type`, then a test payload for `gpt-gitter` and one for `control` piped to the installed guard (`$HOME/.local/bin/pfm internal git-guard`, the launch-line hook command, absent from every settings file). Proof is the call not denied, a bare `[gpt-gitter exit=0]` row and a deny on the `control` row; anything else returns `BLOCKED: gpt-gitter native GitGuard identity unproven`. They never spoof identity, bypass the guard, resolve a refused conflict themselves, or weaken explicit publication authorization.

## Opt-in and engine mirrors

`"gptAgents": true` under `claude` in `pfm.config.json` makes pfm write `gpt-agents` to `targets.capabilities` in the global engine-sync policy. Machine declarations are linked only with that capability; without it, doctor prints `agent SKIPPED all {twin} {path} (requires gpt-agents unmet)` and an install retires only its previously owned links and generated artifacts. Project wrappers are scaffolded project files, not machine-managed links; their `requires` metadata is not a claim that the Claude project loader hides them. Dispatch still checks the live registry.

The GPT entries are Claude-only except `gpt-gitter`: its global declaration and project wrappers preserve Codex `sandbox: inherit` with model `gpt-6-luna`, and OpenCode `"git *": allow` with model `openai/gpt-6.1-sol-fast`. These exact identities carry runtime permissions, not native GitGuard authority; the fail-closed guard check still applies. Other canonical roles keep their supported engine overrides. The mirrors stay generated. A new session or user-authorized reload discovers added registrations; a file read does not refresh the session registry. No account or default model setting changes to select a twin.

## Hooks and ownership

Project wrappers preserve canonical hooks, notably releaser's `PreToolUse` wait hook, and pcm's preloaded `quality:prompt` skill. Machine twins inherit their originals' hook metadata. The retired GPT Stop-hook scripts remain retired; installation removes only its managed links and receipts (`retireGptHooks`, `pfm/internal/installer/retired_hooks.go`).

A pre-existing regular machine agent file is not pfm-owned: install reports a conflict and doctor reports `SHADOWED`. `pfm install --adopt-agents` moves it into the engine-sync adoption state before linking its owned render. Edit a declaration or canonical manual rather than replacing a link with a hand copy.

## Verification and landing timing

`scripts/check-agent-roster.test.mjs` independently pins the rendered roster, canonical manual preservation, runtime metadata, child routes, helper visibility, capability-on/off behavior and engine opt-outs. It reads rendered files, not compiler verdict claims. A missing role, changed manual or blocked helper is a named assertion failure. The generated-agent freshness check holds tracked renders equal to a scratch render. Installed engine-sync must actually pass Codex build/check and OpenCode build/doctor after project source changes.

Run affected tests through `.claude/scripts/test.sh`; the full gate belongs to landing, not each wrapper. `.professor/manifest.json` enumerates tracked project sources only: the authorized Git writer stages new wrappers before `infra/check-self-hosted-manifest.sh --write`, then checks the ledger and commits. An unstaged wrapper is not prematurely recorded as tracked. `templates/refresh-map.json` classifies portable wrappers and global declarations as curated, and repository-only release/theme roles as ignored live sources.
