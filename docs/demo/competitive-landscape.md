# Professor vs. its open-source peers — competitive landscape

Snapshot taken 2026-09-14. Every number below was read with read-only `gh api` calls and depth-1 clones into a scratch directory, not taken from memory. Capability cells come from each project's own README. None of the peer tools was installed or run. Where a README says nothing about a capability, the cell says **unverified**. It never says "no" unless the README rules the capability out itself.

## Method and its limits

- **Metadata**: `gh api repos/{owner}/{repo}` gave stars, creation date, last push, language and license. `releases?per_page=100` gave release counts; a count of 100 means **≥100** because of the page cap. `contributors?anon=1` gave contributor counts.
- **Test signal**: grep over depth-1 clones for Rust `#[test]`/`#[tokio::test]`, Go `func Test`, JS/TS `it(`/`test(` in `*.test.*`/`*.spec.*`, and Python `def test_`, excluding `node_modules`/`vendor`. This is a rough size signal. It does not measure quality, and it misses table-driven subtests and unusual test layouts.
- **CI**: the number of files under `.github/workflows/`.
- **Professor's own numbers** come from the local `develop` checkout. Its capability claims are its README's claims. Only the package names in `pfm/internal/` (`reload`, `inject`, `sky`, `usagehook`, `harvest`, `headless`, `codexgen`) were checked to confirm the named subsystems exist. Their behaviour was not exercised in this pass.

## The field (12 in scope)

| Project | Stars | Created | Last push | Lang | License | Contrib. | Releases (latest) | CI wf | Test cases (grep) | Why in scope |
|---|---|---|---|---|---|---|---|---|---|---|
| **Professor** ([rezzminator/professor](https://github.com/rezzminator/professor)) | 4 | 2026-04-25 | 2026-09-13 | Go | MIT | 4 | 30 (v0.76.0, 09-11) | 3 | ~4,500 Go + ~1,560 JS/TS + 22 py | subject |
| [aannoo/hcom](https://github.com/aannoo/hcom) | 490 | 2025-07-21 | 2026-09-13 | Rust | MIT | 20 | 40 (v0.7.25, 08-09) | 6 | ~2,285 Rust | closest peer for chat-to-chat messaging across harnesses |
| [raysonmeng/agent-bridge](https://github.com/raysonmeng/agent-bridge) | 341 | 2026-03-20 | 2026-09-13 | TS | MIT | 3 | 31 (v0.1.31, 09-12) | 7 | ~1,890 JS/TS | Claude↔Codex messaging plus quota handoff |
| [uwuclxdy/clauth](https://github.com/uwuclxdy/clauth) | 153 | 2026-04-28 | 2026-09-13 | Rust | MIT | 7 | 45 (v0.15.2, 09-13) | 3 | ~3,470 Rust | multi-account, limits, cross-account delegation |
| [smtg-ai/claude-squad](https://github.com/smtg-ai/claude-squad) | 8,474 | 2025-03-09 | 2026-08-20 | Go | AGPL-3.0 | 20 | 20 (v1.0.20, 08-20) | 5 | 39 Go | best-known multi-session manager |
| [agentty-xyz/agentty](https://github.com/agentty-xyz/agentty) | 36 | 2026-02-04 | 2026-09-13 | Rust | Apache-2.0 | 4 | ≥100 (v0.15.15, 09-10) | 11 | ~5,230 Rust | terminal ADE: multi-session plus orchestrator with verified research |
| [RBraga01/a-team](https://github.com/RBraga01/a-team) | 16 | 2026-05-30 | 2026-08-09 | JS | MIT | 8 | 5 (v1.4.0, 07-19) | 8 | 93 JS + 29 py | closest peer for the discipline layer (agents, hooks, auditor, 4 runtimes) |
| [conorbronsdon/agent-context-os](https://github.com/conorbronsdon/agent-context-os) | 24 | 2026-03-06 | 2026-09-11 | Python | MIT | 7 | 2 (v0.13.1, 09-03) | 3 | ~720 py | cross-runtime context and handoff layer |
| [SuperClaude-Org/SuperClaude_Framework](https://github.com/SuperClaude-Org/SuperClaude_Framework) | 23,889 | 2025-06-22 | 2026-08-21 | Python | MIT | 45 | 13 (v4.3.0, 03-22) | 6 | 142 py | best-known Claude Code discipline/config framework |
| [ruvnet/ruflo](https://github.com/ruvnet/ruflo) (formerly claude-flow) | 72,340 | 2025-06-02 | 2026-09-14 | TS | MIT | 40 | ≥100 (v3.41.2, 09-10) | 29 | ~12,170 JS/TS + 131 Rust | largest "meta-harness" for Claude Code and Codex |
| [mag123c/toktrack](https://github.com/mag123c/toktrack) | 189 | 2026-01-26 | 2026-09-13 | Rust | MIT | 9 | 94 (v2.17.1, 09-04) | 7 | ~740 Rust | cross-CLI usage tracker |
| [ccusage/ccusage](https://github.com/ccusage/ccusage) | 18,538 | 2025-05-29 | 2026-09-13 | Rust | NOASSERTION | 77 | ≥100 (v20.0.20, 08-15) | 10 | ~830 Rust | the standard usage/cost reporter |
| [musistudio/claude-code-router](https://github.com/musistudio/claude-code-router) | 37,225 | 2025-02-25 | 2026-09-14 | TS | MIT | 55 | 24 (v3.1.0, 09-10) | 3 | 29 test files (cases not counted) | local control plane: accounts, providers, fallback |

Checked but left out of the matrix:

- [BloopAI/vibe-kanban](https://github.com/BloopAI/vibe-kanban) (28k stars). Its README headline reads "Vibe Kanban is sunsetting."
- [stravu/crystal](https://github.com/stravu/crystal) (3.1k stars). Its README says "Crystal is deprecated and replaced by Nimbalyst."
- [dagger/container-use](https://github.com/dagger/container-use) (4k stars, last release v0.4.2 on 2025-08-19). It gives agents isolated container sandboxes, which is next to Professor's scope rather than overlapping it.
- [1ay1/agentty](https://github.com/1ay1/agentty) (600 stars, C++). A single-agent pair programmer with the same name, and not a fleet tool.

## Capability matrix

Legend: **Y** means the README states it. **partial** means the README states a narrower form (the cell says how). **no** means the README itself excludes it. **unverified** means the README is silent. Professor's column repeats its README's claims.

| Capability | Professor | hcom | AgentBridge | clauth | Claude Squad | agentty | A Team | Context OS | SuperClaude | Ruflo | toktrack | ccusage | CCR |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| Fleet listing | Y: every chat on the machine, live or resumable, incl. ones it did not launch | partial: TUI of hcom agents; `hcom r` adopts outside sessions | partial: pairs per project | partial: browse and resume past sessions per account | partial: its own instances | partial: its own sessions | unverified | unverified | unverified | unverified | no (usage only) | no (usage only) | unverified |
| Multi-account | Y (🥇🥈 accounts, per-account config) | unverified | unverified | **Y (core)** | unverified | unverified | unverified | unverified | unverified | unverified | unverified | unverified | partial: credential pools, key rotation |
| Cross-harness | Claude Code, Codex, OpenCode | **10+**: Claude, Codex, OpenCode, Kilo, Pi, Cursor, Kimi, Copilot, Gemini… | Claude + Codex only | Claude Code only | Claude, Codex, OpenCode, Amp | Codex, Claude, Antigravity, Gemini | Claude, Codex, Cursor, OpenCode | Claude, Codex, OpenClaw, OpenCode (+3 experimental) | Claude Code (a Codex plugin badge is shown) | Claude Code + Codex | 9 CLIs (usage) | 12+ sources (usage) | 10+ clients |
| Chat-to-chat messaging | Y: `chat_inject`, `chat ask`, 20 MCP fleet tools | **Y**: mid-turn delivery, subscriptions, collision alerts, cross-device relay | Y (Claude↔Codex, turn busy-guard) | partial: delegate a prompt to another account over MCP | unverified | unverified | unverified | unverified | unverified | partial: "federation" | no | no | no |
| Reload / handoff | Y: in-place reload onto another account/model, same history; `/handoff` | partial: fork/resume, context bundles | Y: quota relay to the other agent | Y: auto-switch account on limit | partial: pause/resume | unverified | partial: stateless, file-derived resume | **Y**: reviewed cross-agent handoffs | unverified | unverified | no | no | partial: fallback models |
| Usage / limits view | Y: Limits tab, shows "no usage source" instead of 0% | unverified | Y: `abg budget` 5h/weekly | **Y**: live 5h/7d bars, spend ceilings | unverified | unverified | unverified | unverified | unverified | unverified | **Y**: history kept after the CLI deletes its logs | **Y**: daily/monthly/5h blocks, statusline | Y: tokens, cost, account status |
| Discipline / rules layer | Y: 26 rules, 11 hook bindings, guard hooks, gitter-only git | unverified | partial: AGENTS.md collaboration contract | no | unverified | partial: read-only research, mutation denied | **Y**: 26 agents, 20 gated skills, hooks, pipeline auditor | partial: digest-bound lifecycle proposals | **Y**: 30 commands, 20 agents, 7 modes | Y: 98 agents, hooks, swarms | no | no | no |
| Multi-runtime compilation (one source → mirrors, drift check) | Y: CLAUDE.md→AGENTS.md, .claude→.codex/.opencode, drift fails check | unverified | unverified | no | unverified | unverified (CLAUDE.md is a symlink to AGENTS.md in its repo) | unverified (supports 4 runtimes; the mechanism is not stated) | partial: "registered runtimes share the same kernel" | unverified | unverified | no | no | no |
| Document fetching | Y: Harvester, 7-rung ladder, 12 OA resolvers, MCP | unverified | unverified | no | unverified | unverified | unverified | no (imports are selective and manual) | partial: MCP integrations | unverified | no | no | partial: "web search" fusion |
| Verification engine | Y: wave walker (tracer + reviewer fold) | unverified | unverified | no | unverified | partial: research reports "verified by the controller" | partial: pipeline auditor "verifies agents actually ran required checks" | partial: hash-checked receipts | unverified | partial: `verification/` dir present, not read | no | no | no |
| Install footprint | Go binary in `$HOME` + clone + interview; needs tmux | single Rust binary; brew/pip/curl | Bun + npm + daemon + CC plugin | cargo/curl binary, signed self-update | brew/curl binary; tmux + gh | npm/npx/curl/cargo | one-liner curl copies files into the repo | clone + `setup.sh` | pipx | `npx ruflo init` or plugin | npx/brew/binary | `npx ccusage` | unverified |
| Platforms | Linux, macOS | macOS, Linux, Termux, WSL, **native Windows** | Windows not officially supported | Linux, macOS, Windows (Git Bash) | unverified | unverified | Mac/Linux/WSL + PowerShell | unverified | unverified | unverified | macOS, Linux, Windows | unverified | unverified |

## 1. Where peers are clearly better than Professor

- **hcom beats Professor at inter-agent messaging, on every axis except discipline.** It covers 10+ harnesses to Professor's 3. Messages "arrive mid-turn (injected between tool calls)". Agents can subscribe to events, and two agents editing the same file within 30 s both get told. It has an end-to-end-encrypted cross-device relay with a written threat model, and runs on native Windows. It installs with `brew install`. It has 490 stars and 20 contributors against Professor's 4 and 4, and ~2,285 Rust tests. If you only want chats to talk to each other, hcom is the more mature choice. ([README](https://github.com/aannoo/hcom))
- **clauth beats Professor at multi-account depth.** It walks a fallback chain the moment an account hits its limit, with weekly-window and spend-ceiling gates. It has a headless daemon that publishes `status.json`, a live Claude status-incident feed, signed self-updates, and Windows support. It has ~3,470 tests and 45 releases in 4.5 months. Professor offers account swap and reload but no automatic fallback chain or spend ceiling. ([README](https://github.com/uwuclxdy/clauth))
- **ccusage and toktrack beat Professor at usage analytics.** They read 12+ and 9 sources respectively. ccusage has daily/weekly/monthly/5h-block reports and a statusline, and toktrack keeps history after Claude Code "deletes your session data after 30 days". Both install with one `npx`. They have ~18.5k and ~190 stars and ≥100 and 94 releases. Professor's Limits tab and `/tokens` are narrower: live windows plus per-agent attribution, not a historical cost ledger. ([ccusage](https://github.com/ccusage/ccusage), [toktrack](https://github.com/mag123c/toktrack))
- **Claude Squad beats Professor at getting started.** It is one binary with brew, one TUI, and a README that promises "Each task gets its own isolated git workspace". It has 8.5k stars. Professor's onboarding is a clone, a pinned tag, and a 580-line `docs/SETUP.md` interview. ([README](https://github.com/smtg-ai/claude-squad))
- **claude-code-router does something Professor does not do at all:** provider and model routing, failover, key rotation, and credential pools across 10+ clients. It has 37k stars and 55 contributors. ([README](https://github.com/musistudio/claude-code-router))
- **agentty shows more visible engineering rigor.** It has ~5,230 Rust tests, 11 CI workflows, codecov and Sonar config, and ≥100 releases, against Professor's 3 workflows. Its README also states the subscription-auth ToS question outright ("use API key authentication through Claude Console"). Professor's README does not address it, even though Professor drives subscription accounts. ([README](https://github.com/agentty-xyz/agentty))
- **AgentBridge beats Professor at automated handoff between providers.** At a quota limit, one side "stops cleanly at a turn boundary" and passes the task to the other side. Only agent conclusions cross the bridge, filtered by tag, so context stays small. Professor's `/reload --account` is a manual swap within one harness. ([README](https://github.com/raysonmeng/agent-bridge))
- **Ruflo and SuperClaude beat Professor on adoption and ecosystem.** They have 72k and 24k stars, 40 and 45 contributors, and plugin-marketplace install paths that leave "Zero" files in the workspace (Ruflo). Ruflo's test volume (~12k cases, 29 workflows) is the largest in the field.

## 2. Where Professor is clearly ahead

- **All of it in one tool.** Professor is the only project here whose README claims all of these together: machine-wide fleet listing (including chats it did not launch), multi-account, three harnesses, chat-to-chat messaging, in-place account reload with history kept, and a limits view. Across the matrix, no peer's README claims more than about three of those six. The code packages exist (`pfm/internal/{fleet,inject,reload,sky,usagehook,spawn}`). This is Professor's real differentiator. It is a claim of combining features, not of any single feature being best in class.
- **Document fetching.** No peer README describes a fetcher that holds up against bot blocks. Harvester (7-rung ladder, DOI/ISBN/PMID routing, and reporting "a block is reported as a block") has no counterpart in this set.
- **One source compiled into three runtimes, with a drift gate.** A Team and Context OS both support several runtimes. Neither README states a single-source compiler whose drifted mirror fails a check (`pfm codex check` → `CODEX CHECK PASS`). Treat their mechanism as unverified, not as absent.
- **Verification depth.** The wave walker has named failure states (an empty changed set or a missing agent report fails the walk, never a verdict). The nearest peers are A Team's pipeline auditor and agentty's controller-verified research. Both are real but narrower, going by their READMEs.
- **Test volume for a 4-star repo.** ~4,500 Go test functions across 485 tracked `_test.go` files, plus ~1,560 JS/TS cases. By raw count that is in the same range as hcom, clauth and agentty, and well above Claude Squad (39) and SuperClaude (142). Grep counts are not a quality measure.
- **Failures are reported as failures.** The Limits tab shows "no usage source registered" rather than a 0% bar, and dry-run is the default for destructive verbs. No peer README makes this a stated rule. AgentBridge's `abg doctor` "read-only diagnostics" and hcom's written threat model are the nearest match.

## 3. Where they are comparable

- **Discipline layer vs. A Team, SuperClaude and Ruflo.** All four ship agent casts, hooks and gated workflows. A Team's "26 specialist agents… hard enforcement hooks… pipeline auditor" is close in shape to Professor's rules plus reviewer and tracer. Professor's stated extras are the guard hook that refuses with unlock steps and git writes limited to gitter. SuperClaude and Ruflo are larger, and whether they are stricter is unverified.
- **Session handoff vs. Context OS.** Both carry context from one agent to another. Context OS does it through reviewed, hash-checked files. Professor does it by rebooting the pane (`/handoff`, `pfm chat reload`). The approaches differ, and neither wins outright.
- **Release cadence.** Professor has 30 GitHub releases in ~4.5 months, level with AgentBridge (31) and hcom (40). ccusage, agentty and Ruflo publish much more often (≥100).

## 4. Professor's real weaknesses, as a user would feel them

1. **No evidence anyone else uses it.** It has 4 stars and 4 contributors after 4.5 months, against 150–72k stars for every live peer. Nobody outside the author has confirmed any of the capability claims.
2. **Onboarding is heavy.** The two halves install separately. The discipline layer is a clone, a tag checkout, and a 580-line setup interview. Every peer in the usage and session categories installs with one `npx`/`brew`/`cargo` line.
3. **The scope is very wide.** Fleet TUI, discipline templates, a document harvester with mirror providers, two research engines, a verification engine, a legal playbook shelf, memory "dream" organs, a VS Code extension and themes. Each rival does one of these and explains it in a paragraph. The pitch doc itself has to rank 15 capabilities.
4. **Platforms are narrow.** Linux and macOS only, and tmux is required (README requirements). hcom, clauth, toktrack and A Team all document Windows paths.
5. **Risky defaults.** Claude runs in bypass mode and Codex in approval bypass by default. The README says so, but some adopters will stop there.
6. **Legal and ToS exposure.** Harvester fetches with a "Chrome-fingerprint TLS" rung and has opt-in mirror providers (`md5-catalog`, `doi-mirror`). Professor also drives multiple subscription accounts, and agentty's README flags that as an open ToS question. Professor's README addresses neither issue, and a company adopter will ask.
7. **Rough edges in its own materials.** README badges and the install script point at `mreza0100/professor`, which currently resolves to `rezzminator/professor`, presumably by redirect. `capabilities-ranked.md` lists open README defects and a global-agent fan-out bug that is still being fixed.
8. **Messaging covers fewer harnesses than hcom.** hcom covers 10+ harnesses and devices. Professor covers 3 harnesses on one machine.

## 5. Honest positioning

Professor is not the best tool in any single category. hcom is better at messaging, clauth at accounts, ccusage and toktrack at usage, Claude Squad at simple multi-session work, and claude-code-router at provider routing, and each of them has far more users. What Professor offers that none of them do is the combination. One Go binary sees every Claude Code, Codex and OpenCode chat on the machine across accounts, including chats it did not start, and lets them message each other and reboot onto another account without losing history. It also ships a rules layer that refuses rather than reminds, compiled from one source into all three runtimes, plus a bot-resistant document fetcher and a verification engine. The honest pitch is "the integrated operating layer for a heavy, multi-account, multi-harness user on Linux or macOS who wants hard rules." It is not "a better X than X." It asks a lot of that user in onboarding and trust, and so far has no outside evidence that the claims hold up.

## Gaps

- **`agentty`**: `opencode-ai/agentty` returned `HTTP 404 Not Found`. The name matches at least two different projects. This report uses `agentty-xyz/agentty` (a multi-session ADE) as the peer and leaves `1ay1/agentty` (a single-agent C++ pair programmer) out of scope. If a different agentty was meant, that row is wrong.
- **Harvester MCP**: the `claude.ai Harvester` server failed to connect (`Error 502: Bad gateway`), so every read went through `gh api` and git clones.
- **Behaviour**: no peer was installed or run, and no Professor capability was exercised in this pass. Every capability cell restates a README.
- **Test counts**: grep counts miss subtests, macros and unusual test layouts. claude-code-router's cases were not counted; only its 29 test files were.
- **"Conductor-style" managers**: none were confirmed as open source in this pass, so none are covered.
