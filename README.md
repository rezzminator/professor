<h1 align="center">Professor</h1>

<p align="center">
  <strong>An LLM-harness fleet boost framework.</strong><br>
  Turns the AI coding chats on your machine into a disciplined engineering team —<br>
  one you can see, message, and hold to the rules.
</p>

<p align="center">
  <a href="https://github.com/rezzminator/professor/releases"><img alt="release" src="https://img.shields.io/github/v/release/rezzminator/professor"></a>
  <a href="LICENSE"><img alt="license" src="https://img.shields.io/github/license/rezzminator/professor"></a>
  <img alt="go" src="https://img.shields.io/badge/go-1.24-00ADD8">
  <img alt="platform" src="https://img.shields.io/badge/platform-linux%20%7C%20macOS-lightgrey">
  <img alt="works with" src="https://img.shields.io/badge/works%20with-Claude%20Code%20%C2%B7%20Codex%20%C2%B7%20OpenCode-8A2BE2">
</p>

<p align="center">
  <a href="#six-things-you-can-watch-it-do">Watch it work</a> ·
  <a href="#install">Install</a> ·
  <a href="#the-discipline-layer-templates">Discipline layer</a> ·
  <a href="#the-fleet-cli-pfm">Fleet CLI</a> ·
  <a href="#workflows-workflows">Workflows</a> ·
  <a href="docs/BLUEPRINT.md">Blueprint</a>
</p>

<p align="center">
  <img src="docs/img/pfm-fleet.gif" alt="pfm ls: a fleet of Claude Code chats across four projects and two accounts, fuzzy-found, then the Limits dashboard for two Claude and two Codex accounts, then the cosmos sky with a live comms ledger of chats messaging each other" width="900">
</p>

A fleet controller and a discipline layer for **Claude Code, Codex, and OpenCode** — chats that talk to each other, agents that follow the rules, and a harvester that reads what the web won't show a bot.

## Why Professor?

You already run more than one AI chat. They cannot see each other, they forget the rules the moment context compacts, and a growing share of the web answers them with a 403. Professor is the layer that fixes all three — without touching what your harness is, only what it does.

| | Without Professor | With Professor |
| --- | --- | --- |
| Many chats, many accounts | Scattered terminal tabs; a closed tab is a lost chat | One picker — every chat on every harness, live or resumable |
| One chat needs another | You copy-paste between windows | `chat_inject` — a signed turn, delivered and acknowledged |
| The rules, after compaction | Whatever the prompt still remembers | Hooks that refuse, with the unlock steps in the refusal |
| A bot-blocked page | A 403, or an empty page reported as content | A seven-rung fetch ladder; a block is reported as a block |
| Claude and Codex rules | Two files that drift apart | One `CLAUDE.md`, compiled mirrors, drift fails the check |
| An account hits its limit | Start over in a new chat | `/reload --account 2` — same pane, same history |

---

## Six things you can watch it do

Every transcript below is real output from this repository, redacted only of names.

### 1. See the fleet

```text
 pfm  🥇 account 1 · ⚡1h · 12 rows · 38 killed · 64 empty
 tabs   Chats   Stats   Limits    tab/shift+tab
 Chats · fuzzy search and all existing chat controls
find › type project or name                                                        12/12 visible
╭─ fleet 12 ───────────────────────────────────────────────────────────────────────────────────────╮
│╭─ api                                                                                            │
│› ✦ [ Claude ] Codex OpenCode     🥇                                             0p     0B      0s│
││ ● PAYMENTS_REFACTOR             ⬢ 🥇 ⇄                                       118p    14M      2m│
││ ⚙ SCHEMA_MIGRATION              ⚙ agent 🥈                                    20p   6.1M      7h│
│╭─ webapp                                                                                         │
││ ● ORCHESTRATOR                  🥇 ⇄ ←here                                    37p   2.7M      1m│
││ ⚙ DESIGN_PASS                   ⚙ agent 🥇                                    18p   1.9M     58m│
││ ↻ DEPLOY_PROD                   🥇                                            59p   8.6M      2d│
│╭─ ops                                                                                            │
││ ● FLEET_BUILDER                 ⬢ ⇄                                           77p    94M      0s│
││ ↻ CCC                           🥈                                           452p    32M     54m│
╰──────────────────────────────────────────────────────────────────────────────────────────────────╯
 ↑↓ move  enter open  esc cancel  type to fuzzy-find
 ⌃X hide  ⌃E 1h  ⌃S account  ⌃O reboot
```

> `pfm ls`: every AI chat on the machine — Claude Code, Codex (⬢), and OpenCode — across accounts (🥇🥈), grouped by repo, live (●), resumable (↻), or agent-run (⚙). `⇄` marks chats that talk to other chats; `←here` is the one you are sitting in; `✦` opens a new one on any harness. Pick one, attach, or fire it a goal without ever attaching. A chat that scrolled off a closed terminal tab is not gone — it is a resumable transcript, and now somebody can find it.

`tab` once more and the same fleet is drawn as a sky:

![The pfm cosmos tab: the agent fleet drawn as a star map, each project a star and each chat a body orbiting it](docs/img/pfm-cosmos.png)

> Every project is a star its chats orbit; a spawned chat rises as a moon at its parent's angle, so lineage is visible in the sky itself. When chats talk to each other the sky draws an edge between them, read from a durable comms ledger — an edge is a fact, not a guess. The chronoscope replays the last 24h, and a chat that is dead now still renders as the ghost it was back then.

### 2. Chats that talk to each other

Two panes, two harnesses. You type one line into the Claude chat on the left; the Codex chat on the right receives it as a signed turn and gets to work.

```text
┌────────────────────────────────────────────────────────────┐        ┌────────────────────────────────────────────────────────────┐
│  ▐▛███▛█   Claude Code v2.1.270                            │        │ ╭──────────────────────────────────────────╮               │
│ ▝▜██████▀  Fable 5.1 with low effort · Claude Max          │        │ │ >_ OpenAI Codex (v0.154.0)               │               │
│   ▝▝ ▝▝    ~/.professor                                    │        │ │ model:       gpt-6-astra xhigh           │               │
│                                                            │        │ │ directory:   ~/.professor                │               │
│ ❯ Tell the Codex chat named DEMO_CODEX to run the          │        │ │ permissions: YOLO mode                   │               │
│   migration test suite and report back when green.         │        │ ╰──────────────────────────────────────────╯               │
│                                                            │        │ › Run the migration test suite and report back when        │
│   Called chat_inject                                       │ -----> │   it is green.  — sid 0a98b7fe · to reply:                 │
│ ⏺ Sent — DEMO_CODEX has the instruction (delivered         │        │   chat_inject DEMO_CLAUDE <message>                        │
│   live, it's already working) and will report back         │        │                                                            │
│   here when the migration suite is green.                  │        │ • Explored                                                 │
│   Verdict: message delivered to DEMO_CODEX — awaiting      │        │   └ Read dev.sh · Search migration (suite|test)            │
│   its green report in this chat. ☕                        │        │ • The runner has no separate migration target, so          │
│ ✻ Baked for 9s · done 1:11 AM                              │        │   I'll run the full PFM suite, which includes the          │
│ ─────────────────────────────────────── DEMO_CLAUDE ─      │        │   migration tests, inside the container fence.             │
│ ❯                                                          │        │ • Waiting for background terminal (2m 17s)                 │
│ ────────────────────────────────────────────────────       │        │   └ .claude/scripts/dev.sh iso test pfm                    │
│  🥈 ✦ Fable 5.1 │ 🔖 DEMO_CLAUDE │ 🔹 low │ 🌿 develop     │        │ › Ask Codex to do anything                                 │
│  ▰▱▱▱▱ 5h-used:36% │ ▰▱▱▱▱ 7d-used:35% ↻5d3h               │        │   gpt-6-astra xhigh · Working · Context 82% left           │
└────────────────────────────────────────────────────────────┘        └────────────────────────────────────────────────────────────┘
```

> The footer on the Codex side is the signature: who spoke (`sid 0a98b7fe`) and the exact command to answer them. A message no sender could be derived for is refused, never delivered anonymously.

`pfm chat inject` is what the Claude chat called — it types a real, signed turn into another chat's pane — under a per-target lock, safe against a busy target (`--force-now`) and shell-hostile payloads (`--file`). `pfm chat ask` waits for the answer, with named exit codes: `0 done · 2 usage · 3 chat dead · 4 no such chat · 5 answer timed out · 6 message not delivered · 7 answered, but another message reached the chat mid-wait`. The same verbs are an MCP server, so an agent can spawn, message, read, and retire other chats across all three harnesses — and `issue_servicedesk` lets it file a bug against its own host tool.

### 3. Rules that bite

A subagent tries to `Edit` a file under `.claude/`. The PreToolUse guard answers:

```text
DENIED — infra edits route through /pcm: open this session's gate from the repo root …
Do NOT route around this by disabling the hook or editing infra outside /pcm.
```

The refusal carries its own unlock steps. That is one of 26 mandatory rules every install ships with: only `gitter` writes git; fix loops cap at three attempts, then `BLOCKED-DEFERRED`; read-only mappers (`tracer`) are separated from judges (`reviewer`); and **every check names what its own broken state reports** — a gate that says "fine" when healthy and when broken is a coincidence detector.

### 4. Read what the web hides from bots

About one in ten of the world's top 10,000 websites now tells AI crawlers to stay out — among news publishers, more than half (HasData AI Crawler Block Index, July 2026). Harvester fetches the way a reader's browser does and climbs a seven-rung ladder — direct → Chrome-fingerprint TLS → reader proxy → extractor → headless-then-headed browser → Wayback → OCR — until it holds real content:

```text
$ pfm harvest https://www.nytimes.com/          # robots.txt: every AI crawler Disallow: /
→ 2,930 chars, the live front page

$ pfm harvest https://www.reuters.com/
→ ERROR: The source is protected by an access challenge. Choose another copy.
```

The second line is the design: an app shell is never stored as the page, and a block is reported as a block — never as an empty success. DOIs, ISBNs, PMIDs and PMCIDs route through twelve open-access resolvers in parallel; `pfm harvest ask -p "…" <sources>` feeds the full cached artifacts to a Claude or Codex ask engine, failed sources kept visible as receipts. The whole surface is also an MCP server. The browser rung never solves anything interactive.

### 5. One contract, three runtimes

```text
$ head -1 AGENTS.md
<!-- Generated by pfm codex build from CLAUDE.md; do not edit — edit the source, then re-run: pfm codex build -->
$ pfm codex check .
CODEX CHECK PASS
```

`CLAUDE.md` compiles to `AGENTS.md`; `.claude/**` compiles to `.codex/**` and `.opencode/**`; global agents get `.toml` twins. The Stop hook recompiles the mirrors and a drifted mirror fails the check, so the three harnesses can never disagree about the law. Even the verification engine is one TypeScript source compiled for both the Claude Workflow runtime and the Codex SDK.

### 6. Reload without losing the conversation

Account 🥇 is at 94% of its weekly limit. You type `/reload --account 2`; the same pane comes back on 🥈 with the whole conversation, and remembers the codeword it was given on the other account.

```text
┌────────────────────────────────────────────────────────────┐        ┌────────────────────────────────────────────────────────────┐
│  ▐▛███▛█   Claude Code v2.1.270                            │        │  ▐▛███▛█   Claude Code v2.1.270                            │
│ ▝▜██████▀  Opus 5 (1M context) · Claude Max                │        │ ▝▜██████▀  Opus 5 · Claude Max                             │
│   ▝▝ ▝▝    ~/.professor                                    │        │   ▝▝ ▝▝    ~/.professor                                    │
│                                                            │        │                                                            │
│ ❯ Remember this codeword for later: BLUE-HERON.            │        │ ❯ Remember this codeword for later: BLUE-HERON.            │
│ ⏺ BLUE-HERON — held, my friend. ☕                         │        │ ⏺ BLUE-HERON — held, my friend. ☕                         │
│   Verdict: codeword stored — note the 7-day cap            │        │   Verdict: codeword stored — note the 7-day cap            │
│   is at 95%, so /reload may be needed.                     │ reload │   is at 95%, so /reload may be needed.                     │
│ ✻ Baked for 4s · done 1:34 AM                              │ -----> │ ✻ Baked for 4s · done 1:34 AM                              │
│                                                            │        │ ⏺ UserPromptSubmit operation blocked by hook:              │
│                                                            │        │   Original prompt: /reload --account 2                     │
│                                                            │        │ ❯ What was the codeword I gave you? One line.              │
│                                                            │        │ ⏺ The codeword is BLUE-HERON.                              │
│                                                            │        │ ✻ Brewed for 12s · done 1:35 AM                            │
│ ─────────────────────────────────────── RELOAD_DEMO ─      │        │ ─────────────────────────────────────── RELOAD_DEMO ─      │
│ ❯ /reload --account 2                                      │        │ ❯                                                          │
│ ────────────────────────────────────────────────────       │        │ ────────────────────────────────────────────────────       │
│  🥇 ◆ Opus 5 (1M context) │ 🔖 RELOAD_DEMO                 │        │  🥈 ◆ Opus 5 │ 🔖 RELOAD_DEMO                              │
│  ▱▱▱▱▱ 5h-used:2% │ ▰▰▰▰▱ 7d-used:94% ↻4d6h                │        │  ▰▱▱▱▱ 5h-used:39% │ ▰▱▱▱▱ 7d-used:36% ↻5d3h               │
└────────────────────────────────────────────────────────────┘        └────────────────────────────────────────────────────────────┘
```

> "Blocked by hook" is the design: the `/reload` hook takes the prompt before the model sees it, so the reboot never spends a turn.

The running chat reboots in place — same pane, same history, new account, model, or effort. `--then "prompt"` hands the baton unattended; `/reload` typed by a human runs through a hook without spending a model turn; `/handoff --branch` carries the full context into a detached successor.

---

## Install

Two independent halves; adopt either without the other.

- **`pfm`**, the fleet CLI — one Go binary, touches only your `$HOME`. Binary or source: [INSTALL.md](INSTALL.md).
- **The discipline layer** — cloned, then scaffolded into your repo through an interview:

```bash
REPO=rezzminator/professor
TAG=$(git ls-remote --tags --sort=-v:refname "https://github.com/${REPO}.git" 'v*' \
  | grep -v '\^{}' | head -1 | sed 's#.*/##')
git clone "https://github.com/${REPO}.git" "$HOME/.professor"
git -C "$HOME/.professor" checkout "$TAG"
cat "$HOME/.professor/docs/SETUP.md"      # the install interview — start here
```

The checkout is pinned to the latest semantic version tag. A maintainer checkout also runs `git config core.hooksPath .githooks` so `pfm doctor` reports `pre-push gate=armed`. Upgrading? Follow the [update workflow](INSTALL.md#updating): `pfm update check` reports `UPDATED / NEW / GONE-UPSTREAM / LOCAL-DELETED` with the exact diff, and `pin` / `ignore` / `drop` record your decision — pfm never rewrites a project file after init.

> [!WARNING]
> **Read before opting in:** `pfm` defaults Claude to bypass mode and Codex to approval bypass; machine and per-account configuration can select the prompted posture. Both MCP servers ship disabled. The trade-off is deliberate and documented, not hidden.

---

## The discipline layer (`templates/`)

Clone it into a repo and you get the complete agent, command, hook, script, and `CLAUDE.md` template set. `docs/SETUP.md` walks an interview that substitutes your project's names into every placeholder; `docs/PLACEHOLDERS.md` is the substitution law. Every template is the live source file verbatim — never a skeleton.

The single idea underneath it is the **honest-looking absence** — an instrument that answers "nothing found" both when nothing is there and when the instrument itself is broken. The wave walker says it out loud:

> An empty enumeration is never a verdict.

- **One agent writes git.** `gitter` runs six named phases (SETUP, COMMIT, MERGE, PUSH, PULL, TAG). No other agent commits.
- **Guarded files.** `.claude/**` and every `CLAUDE.md` sit behind `/pcm` plus a session that has read the quality-prompt contract.
- **The judge is never the thing being judged.** Verdicts are read from disk, never from a brief that asserts green.
- **The wave pipeline.** refine → scheduler → orchestrator → builder → walker: the walker dispatches the `tracer` and `reviewer` agents over the landed diff and folds their two reports into one verdict, every unmapped target and unreached hunk named.
- **The persona is load-bearing.** The Professor prompt replaces the vendor system prompt; the vendor baselines are pinned by sha256 so `pfm doctor` reports `MATCHES / DRIFT / CHECK FAILED / CANNOT CAPTURE` — never silence.

Optional roles ship for teams that want them — `/officer`, `/mentor`, `/marketer` — along with a legal skill shelf. **The philosophy lives in [docs/BLUEPRINT.md](docs/BLUEPRINT.md).**

---

## The fleet CLI (`pfm/`)

One Go binary with embedded installer assets. Beyond the six moments above:

- **Limits, honestly.** The `Limits` tab shows every provider window on the box; a provider it cannot reach never renders as a 0% bar — the panel says `no usage source registered` instead.
- **Crash-safety by construction.** `reap`, `archive`, `heal`, and `install` default to a dry run, and the dry run **is** the apply's preview. `heal` backs up the store before it deletes a row.
- **Headless exec.** `pfm headless exec` is one scriptable interface for Claude and Codex: prompt, system prompt, schema, timeout in; normalized result and native streaming out. See [headless README](pfm/internal/headless/README.md).
- **Housekeeping.** `doctor` runs the dependency registry and fleet DB checks; `tokens` attributes spend per agent; `context-meter` prices every prompt surface; `statusline` renders identity, session and spend; `codex build|check` is the single writer of the Codex mirror.
- **Editor.** `pfm install --vscode` installs the Professor VS Code extension — visible as **Professor** in the Extensions view and a **Professor** entry in the terminal `+` dropdown — and makes the `PFM` settings profile (its own icon and colour) the default, so each new integrated terminal opens at the fleet picker. To open a Professor terminal, press Ctrl+Shift+Alt+T (macOS: Cmd+Shift+Alt+T), run **Professor: New Chat Terminal**, or pick **Professor** from the terminal `+` dropdown — all three give the next icon and colour; the default `+` terminal is `PFM`.

<details>
<summary><strong>Requirements</strong> — Linux or macOS, <code>tmux</code>, Go 1.24.13+ for source builds</summary>

From `pfm doctor`'s own registry: Linux or macOS, `amd64` or `arm64`, plus `tmux` ≥ 1.8, `git`, `sh`, `bash`, `zsh`, and `sleep`; `setsid` on Linux, `ps`/`lsof`/`launchctl` on macOS. Go **1.24.13 or newer** for source builds and `pfm update`. The `claude` and `codex` CLIs are optional diagnostics. The harvester provisions its own pinned `uv` and CPython (about 3.1 GB to download and 5.8 GB on disk for the current Linux `amd64` lock), skippable with `--skip-harvest`; themes with `--skip-themes`; the Codex probe with `--skip-engine codex`. Run the [dry preview](INSTALL.md#preview-optional-components-and-harvest-footprint) before applying. Harvester configuration: [harvest README](pfm/internal/harvest/README.md).

</details>

---

## Workflows (`workflows/`)

- **deep-rr** (`workflows/deep-rr/`) — background research that returns a cited report: a scout swarm, a brainer steering the crawl, quote-pinned claims audited mechanically, lineage clustering so corroboration counts independent sources. Compiled for the Claude Workflow runtime. Start at [workflows/deep-rr/README.md](workflows/deep-rr/README.md).

---

## Origin

Extracted from a live production monorepo, not designed in the abstract. Every rule here exists because something went wrong without it — the gate that reads disk instead of chat exists because an agent once claimed green; the scoped-commit rule exists because two concurrent commits once swallowed each other's files; the prevention step exists because the same bug class shipped twice. The characters exist because a generic agent wasn't good enough to argue with.

Built by [@rezzminator](https://github.com/rezzminator). Issues and PRs welcome.

**License:** MIT
