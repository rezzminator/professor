# Professor — everything unique it does, ranked for the pitch

Built from five tracer inventories (read from disk, 5 tracers dispatched → 5 received) plus `research-blocked-web-percent.md`; all five inventories are since removed and every row's evidence cites the source files directly. Rank = (nobody else ships it) × (visible on stage in 60 s) × (the AI Builders audience's own pain). Evidence pointers name the code anchor.

## The ranking

| # | Capability | What only Professor does | Stage-visible? | Evidence |
|---|---|---|---|---|
| 1 | **The fleet — chats as infrastructure** | Every Claude Code / Codex / OpenCode chat on the machine, across accounts, live or resumable, in one picker; a Limits tab that says "no usage source" instead of drawing 0%; the cosmos sky where each project is a star and every chat orbits it, with lineage and cross-chat edges drawn from a durable ledger; 24h chronoscope replay. | Yes — the sky alone is the screenshot. | `pfm/cmd/pfm/main.go` `ls`, `--no-sky`, `sky`; README "The fleet, at a glance" |
| 2 | **Chats that talk to each other** | `chat_inject` types a signed turn into another chat's pane under a per-target lock; `chat ask` waits for the answer with named exit codes (0/2/3/4/5/6); `chat_goal` fires a compiled `/goal`; 18 fleet-control MCP tools (17 `chat_*` verbs plus `servicedesk`) so an agent can spawn, message, read and kill other chats across harnesses, and file a bug against its own host tool. | Yes — one chat asks another and gets the answer back. | `pfm/internal/mcpserv/server.go` `chatToolNames` |
| 3 | **The discipline layer** | 26 mandatory rules that every install carries: only `gitter` writes git; guarded files behind a PreToolUse hook; "an error never renders as absence"; the coincidence detector; the judge is never the thing being judged; fix loops capped at three; 11 hook bindings; read-only mappers (tracer) separated from judges (reviewer). | Yes — a subagent tries to edit `.claude/**` and the guard refuses, with the unlock steps in the deny. | `templates/project/CLAUDE.md` § MANDATORY Rules; `templates/project/settings.json` hooks; BLUEPRINT "five load-bearing walls" |
| 4 | **Harvester — reads what the web won't show a bot** | 7-rung fetch ladder (direct → Chrome-fingerprint TLS → jina → defuddle → headless-then-headed browser → Wayback → OCR), 12 concurrent open-access resolvers, DOI/ISBN/PMID/PMCID routing, app-shell rejection (never stores a blank SPA), landing-page detection, SSRF-safe mirrors, redaction of which rung succeeded; 4 MCP tools (`harvester_read`, `harvester_search_literature`, `harvester_download_file`, conditional `harvester_search_web`); pinned Python sidecar. | Yes — fetch a page WebFetch 403s on (the AI Builders site itself did tonight) and read it. | `pfm/internal/harvest/harvest.go` § fetch ladder; `pfm/internal/harvestmcp/service.go` |
| 5 | **One contract, three runtimes** | `CLAUDE.md` compiles to `AGENTS.md`; `.claude/**` compiles to `.codex/**` (`pfm codex build`) and `.opencode/**` (`pfm opencode build`); global agents `.md` → `.toml` twins; the Stop hook recompiles and blocks the turn if a mirror drifts. | Partly — show the marker line at `AGENTS.md:1` and a `pfm codex check` drift. | `pfm/internal/codexgen/`; `AGENTS.md:1` |
| 6 | **Reload / handoff without losing the conversation** | `pfm chat reload` reboots a live chat in place onto another account, model or effort — same pane, same history; `--then` hands the baton unattended; `/handoff --branch` carries full context into a detached successor. | Yes — swap accounts mid-conversation, history intact. | `pfm/cmd/pfm/chat_reload_command.go`; releases v0.76.0 |
| 7 | **The Professor persona as the harness prompt** | `professor.md` replaces the vendor system prompt (`--system-prompt-file`); the vendor baselines are pinned by sha256 as drift sentinels — `pfm doctor` reports MATCHES / DRIFT / CHECK FAILED, never silence; "personality is load-bearing" (BLUEPRINT). | Yes — the voice is the demo. | `pfm/harness-prompts/`; `pfm/internal/doctor` |
| 8 | **The flights pipeline** | `/flights:spec` narrows a spec to zero-gap and writes one task file per unit of work; `/flights:orchestrate-nested` / `-live` / `-cross-harness` dispatch one executor per task; one `flights-lander` per project gates the whole diff (checks, one review, adversarial tests, its own fixes) before `gitter` commits — a missing executor return is named, never silent, and a suite the lander did not watch run is never a pass. | Partly — a flight takes minutes; show a finished landing. | `templates/global/commands/flights/`; `templates/global/agents/flights-*.md` |
| 9 | **Research engines with a claim ledger** | `rr` (inline, Haiku diggers, saved report) and `deep-rr` (brainer-steered crawl, quote-pinned claims mechanically audited, lineage clustering so corroboration counts independent sources, attack lanes, Python derivations with variance-steered reading, Chao1 coverage stop). | Partly — show tonight's RR report as the artifact. | `workflows/deep-rr/README.md`; `research-blocked-web-percent.md` |
| 10 | **The blueprint as a versioned product** | `pfm init` scaffolds and pins a baseline; `pfm update check` reports UPDATED / NEW / GONE-UPSTREAM / LOCAL-DELETED with the exact diff; `pin` / `ignore` / `drop` / `adopt`; templates are the live files verbatim, 178 placeholder tokens under one substitution law; `/pfm:release` sweeps ledgers into `releases/` and lands develop → main. | No — narrate. | `pfm/internal/update`, `pfm/internal/updatecheck`; BLUEPRINT "Staying current" |
| 11 | **Ops that name their own broken state** | `pfm doctor` runs ~30 checks; `reap` / `archive` / `heal` / `install` default to a dry run that IS the apply's preview; `heal` backs up before deleting; `tokens` attributes spend per agent, `context-meter` per surface. | Partly — one `pfm doctor` line that says UNREADABLE instead of "fine". | `pfm/internal/doctor`; `pfm/cmd/pfm/reap_command.go`, `archive_command.go`, `heal_command.go`; global commands tokens, context-meter |
| 12 | **Headless exec** | One scriptable interface for Claude and Codex: prompt/system/schema in, normalized result out, sealed tool surface, native streaming. | No — narrate. | `pfm/cmd/pfm/headless_exec_command.go` |
| 13 | **Optional cast + legal shelf** | `/officer`, `/mentor`, `/marketer`; 11 legal playbooks (DPA, DPIA, breach, notices, vendor DD, red-team). | No. | `templates/project/commands/{officer,mentor,marketer}.md`; `templates/project/skills/legal/` |
| 14 | **VS Code extension + PFM terminal, themes** | Every new integrated terminal opens at the fleet picker. | Yes, briefly. | INSTALL.md :84, :98 |

## The one-sentence pitch — three candidates, ranked

1. **"Professor turns the AI coding chats on your machine into a disciplined engineering team — one you can see, message, and hold to the rules."**
   Covers #1–#3 in one breath; the harvester and the three runtimes are the second sentence.
2. **"A discipline layer and a fleet controller for Claude Code, Codex and OpenCode: agents that follow rules, chats that talk to each other, and a harvester that reads what the web won't show a bot."**
   The literal inventory; longer, but nothing in it needs explaining.
3. **"The operating system for a fleet of AI coding agents — discipline, communication, and reach."**
   Shortest; "reach" has to be unpacked (harvester + three runtimes).

## The harvester opener — what the research supports

The planned line "30–40% of the internet is blocked for bots" has **no measured source**. What the report (`research-blocked-web-percent.md`) can carry on stage:

- Most defensible: *"About one in ten of the world's top 10,000 websites now tells AI crawlers to stay out — and among news publishers it is more than half."* (HasData, Jul 2026: 10.3% top-10k; 56.4% of news publishers.)
- Punchier, still honest: *"Announce yourself as an AI bot and nearly half of news sites shut the door: same IP, a browser got in 84% of the time, GPTBot 54%."* (HasData paired requests.)
- Also usable: *"A fifth of the web sits behind Cloudflare, and since July 2025 Cloudflare blocks AI crawlers by default."* (Cloudflare, 1 Jul 2025.)

Do not use: "half the web is bots" (Imperva 53% / Cloudflare 52.7% — measures who sends traffic, not who is blocked); any robots.txt figure as a hard block; any top-N figure as whole-web.

## README defects found on the way (for the rewrite)

- Opens with a clone script at line 5 before saying what Professor is; the first sentence pitches two halves and neither.
- `## Engines` names `engines/rr/` — does not exist; the workflow is `workflows/deep-rr/`.
- `pfm install` links global agents/commands/skills into the primary account only (`codexgen/globalagents.go:168`, `installer.go:459`, `:617`) while the retire paths fan out over every account — fix in flight.
