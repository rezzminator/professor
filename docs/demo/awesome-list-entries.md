# Awesome-list submission drafts — Professor

Research-only draft. Nothing here has been posted, opened, or forked. Source facts used throughout: Professor is an LLM-harness fleet boost framework for Claude Code, Codex & OpenCode — turns your AI coding agents into a disciplined engineering team you can see, message, and hold to the rules. Repo: <https://github.com/rezzminator/professor> · MIT · Go + Markdown/shell · created 2026-04-25 · 4 stars at time of writing.

---

## 1. hesreallyhim/awesome-claude-code

- Repo URL: <https://github.com/hesreallyhim/awesome-claude-code>
- Activity seen: 2026-09-14 (pushed same day), 53,986 stars, not archived
- Section to add to: **Multi-Purpose** (spans orchestration, observability, and a rules/discipline layer — no single narrower category covers all three; "Agent Orchestration" is the fallback if a maintainer prefers a tighter fit)
- Rules affecting us:
  - **Submissions go through the repo's web UI issue form template ONLY — pull requests and CLI submissions are explicitly disallowed** ("risk being restricted" if not followed). This entry is drafted as a reference for filling that form, not as a PR diff.
  - Inclusion requires either 100+ GitHub stars **or** 14+ days old with active development. Professor is ~4.5 months old with ongoing commits, so it clears the age/activity bar even at 4 stars.
  - Descriptions must be factual/single-line, no emojis, no second-person address.

```
- [Professor](https://github.com/rezzminator/professor) by [rezzminator](https://github.com/rezzminator) - An LLM-harness fleet boost framework for Claude Code, Codex & OpenCode; a Go CLI/TUI (pfm) lists and controls every AI coding chat on the machine, lets chats message each other, and reloads a chat onto another account keeping history, paired with a discipline layer of agents/commands/hooks/rules compiled for Codex and OpenCode.
```

PR title: N/A (issue-form submission, not a PR)

Issue-form body (paste into the web form's description field):

```
Professor pairs a Go fleet CLI/TUI (pfm) — fleet picker, Limits dashboard, cosmos view, cross-chat messaging via chat_inject, and account-preserving chat reload — with a discipline layer of Claude Code agents/commands/hooks/rules that also compiles to Codex and OpenCode mirrors. Best fit: Multi-Purpose (touches orchestration, observability, and configuration/rules at once). MIT licensed, Go + Markdown/shell.
```

---

## 2. e2b-dev/awesome-ai-agents

- Repo URL: <https://github.com/e2b-dev/awesome-ai-agents>
- Activity seen: 2026-08-21, 29,990 stars, not archived
- Section to add to: **Multi-agent** (pfm's fleet control + cross-chat messaging is multi-agent coordination infrastructure, not a single assistant)
- Rules affecting us: PR-based; "keep the alphabetical order and in the correct category"; this list explicitly scopes itself to AI assistants/agents themselves (SDKs/frameworks are redirected to a sister "Awesome SDKs for AI Agents" list) — Professor qualifies because pfm's fleet/orchestration layer functions as agent infrastructure, not merely an SDK.

```
## [Professor](https://github.com/rezzminator/professor)
An LLM-harness fleet boost framework for Claude Code, Codex & OpenCode.

<details>

### Category
Multi-agent, Developer tools

### Description
- Go CLI/TUI (pfm) lists and controls every AI coding chat on the machine across harnesses and accounts (fleet picker, Limits dashboard, cosmos view)
- Lets chats message each other via chat_inject and an MCP server
- Reloads a chat onto another account while keeping history
- Discipline layer of agents/commands/hooks/rules for Claude Code, compiled to Codex and OpenCode mirrors
- Includes Harvester, a document fetcher with a multi-rung fallback ladder, exposed over MCP

### Links
- [Repository](https://github.com/rezzminator/professor)
- Author: [rezzminator](https://github.com/rezzminator)

</details>
```

PR title: `Add Professor to Multi-agent`

PR body:

```
Adds Professor, a fleet control/orchestration layer for Claude Code, Codex and OpenCode (Go CLI/TUI + cross-chat messaging + a discipline layer of agents/commands/hooks). Placed under Multi-agent per the list's scope note distinguishing agents/assistants from SDKs. MIT licensed.
```

---

## 3. punkpeye/awesome-mcp-servers

- Repo URL: <https://github.com/punkpeye/awesome-mcp-servers>
- Activity seen: 2026-09-13, 94,918 stars, not archived
- Section to add to: **🔎 Search & Data Extraction** — for Harvester specifically (the document fetcher with a multi-rung fallback ladder, exposed over MCP). The chat_inject/fleet-messaging MCP server is not a data-extraction server and does not cleanly fit any listed category (not Databases, File Systems, or Code Execution), so it is left out of this submission — only Harvester is proposed here.
- Rules affecting us: entries need language/scope/OS emoji tags (📇/🐍/etc., ☁️/🏠, 🍎/🪟/🐧) and a description under ~255 characters; no explicit star minimum visible in the README excerpt fetched. Harvester ships inside the Professor monorepo (`pfm/internal/harvest` + `internal/harvestmcp`) rather than as a standalone repo — flag this for the maintainer since some MCP lists prefer a dedicated repo/package per server; link points at the monorepo with a note.

```
- [rezzminator/professor](https://github.com/rezzminator/professor) 📇 🏠 - Harvester: document fetcher with a multi-rung fallback ladder, exposed over MCP (part of the Professor fleet framework for Claude Code, Codex & OpenCode).
```

PR title: `Add Harvester (Professor) to Search & Data Extraction`

PR body:

```
Adds Harvester, the document-fetching MCP server bundled in Professor (an LLM-harness fleet framework for Claude Code/Codex/OpenCode). Harvester exposes a multi-rung fallback fetch ladder over MCP. Note: it ships inside the Professor monorepo rather than a standalone package — happy to split it out if the list prefers a dedicated repo.
```

---

## 4. agarrharr/awesome-cli-apps

- Repo URL: <https://github.com/agarrharr/awesome-cli-apps>
- Activity seen: 2026-09-13, 20,386 stars, not archived
- Section to add to: **AI** (the README's AI section explicitly relaxes inclusion criteria for this fast-moving category); pfm is the CLI/TUI entry point
- Rules affecting us: CC0-licensed list, alphabetical ordering within subsections, single-line factual descriptions, no star minimum for the AI section per its stated relaxed criteria.

```
- [pfm](https://github.com/rezzminator/professor) - Go CLI/TUI that lists and controls every AI coding chat on the machine across harnesses and accounts, with cross-chat messaging and account-preserving reload; part of the Professor fleet framework for Claude Code, Codex & OpenCode.
```

PR title: `Add pfm (Professor) to AI`

PR body:

```
Adds pfm, the Go CLI/TUI component of Professor, an LLM-harness fleet framework. pfm gives a fleet picker, Limits dashboard, and cosmos view across every AI coding chat on the machine, with cross-chat messaging and account-preserving chat reload. Placed in AI per the list's relaxed criteria for this category.
```

---

## 5. rothgar/awesome-tuis

- Repo URL: <https://github.com/rothgar/awesome-tuis>
- Activity seen: 2026-09-13, 20,583 stars, not archived
- Section to add to: **Development** (debuggers/dashboards/dev tooling TUIs) — pfm's fleet picker, Limits dashboard, and cosmos view are dashboard-style TUIs for a developer workflow
- Rules affecting us: the list explicitly excludes tools that merely wrap other interactive commands (e.g. fzf) — pfm's TUI is a standalone dashboard, not a wrapper, so this should not block inclusion; submissions via PR; maintenance is a stated inclusion criterion (Professor is actively developed, most recent commits within the last day per repo status).

```
- [pfm](https://github.com/rezzminator/professor) Go TUI that lists and controls every AI coding chat on the machine across harnesses and accounts — fleet picker, Limits dashboard, and cosmos view, plus cross-chat messaging and account-preserving reload.
```

PR title: `Add pfm to Development`

PR body:

```
Adds pfm, the terminal dashboard component of Professor (an LLM-harness fleet framework for Claude Code, Codex & OpenCode). It's a standalone TUI — fleet picker, Limits dashboard, cosmos view — not a wrapper around another interactive tool, so it should fit the list's exclusion rule for fzf-style wrappers.
```

---

## 6. avelino/awesome-go

- Repo URL: <https://github.com/avelino/awesome-go>
- Activity seen: 2026-09-14, 184,052 stars, not archived
- Section to add to: **Standard CLI** (pfm is a Go CLI application, not a CLI-building library, which is what "Advanced Console UIs" covers)
- Rules affecting us: entries must be "maintained" and "a good fit"; alphabetical order within the category; this list is enforced with CI checks on PRs (link validation, format, duplicate detection) — expect an automated check to run on the submission PR.

```
- [pfm](https://github.com/rezzminator/professor) - CLI/TUI that lists and controls every AI coding chat on the machine across harnesses and accounts, part of the Professor fleet framework for Claude Code, Codex & OpenCode.
```

PR title: `Add pfm to Standard CLI`

PR body:

```
Adds pfm, a Go CLI/TUI that manages AI coding chat sessions across harnesses and accounts (fleet picker, dashboards, cross-chat messaging, account-preserving reload). It's the Go engine component of Professor, an MIT-licensed fleet framework for Claude Code, Codex & OpenCode.
```

---

## 7. awesome-opencode/awesome-opencode

- Repo URL: <https://github.com/awesome-opencode/awesome-opencode>
- Activity seen: 2026-07-03 (pushed), 10,223 stars, not archived — **more than two months stale at time of writing; still not archived, so kept in but flagged as slower-moving than the others on this list**
- Section to add to: **🛠 PROJECTS** (Professor is a broader framework that includes but is not limited to OpenCode support, so it does not fit AGENTS/PLUGINS/THEMES, which are OpenCode-specific extension points)
- Rules affecting us: PR-based ("Add a Project via PR"), entries use a collapsible `<details>` block with a star badge and "View Repository" link, appears alphabetically ordered; no explicit star minimum found in the fetched excerpt.

```
<details>
  <summary><b>Professor</b> <img src="https://badgen.net/github/stars/rezzminator/professor" /> - <i>A disciplined engineering-team layer for your AI coding agents</i></summary>
  <blockquote>
    An LLM-harness fleet boost framework for Claude Code, Codex & OpenCode. Includes pfm, a Go CLI/TUI that lists and controls every AI coding chat on the machine across harnesses and accounts, lets chats message each other, and reloads a chat onto another account keeping history; plus a discipline layer of agents/commands/hooks/rules compiled for OpenCode.
    <a href="https://github.com/rezzminator/professor">🔗 <b>View Repository</b></a>
  </blockquote>
</details>
```

PR title: `Add Professor to Projects`

PR body:

```
Adds Professor under Projects: a fleet framework spanning Claude Code, Codex and OpenCode, including a pfm-compiled OpenCode discipline layer for agents, commands, and skills, plus a cross-harness fleet CLI/TUI. MIT licensed.
```

---

## 8. hashgraph-online/awesome-ai-plugins

- Repo URL: <https://github.com/hashgraph-online/awesome-ai-plugins>
- Activity seen: 2026-09-13, 254 stars, not archived
- Section to add to: **Development & Workflow** (multi-agent coordination/orchestration tools spanning Claude Code, Codex, and other hosts)
- Rules affecting us: strict alphabetical order within the subsection; one line per tool, PR-based; the README states the generator "auto-fetches bundles" from referenced repos rather than storing file copies, and recommends (not requires) Scanner CI for a higher trust score — Professor does not currently run that specific Scanner CI, so the entry would land without the elevated trust badge until/unless that's set up.

```
- [Professor](https://github.com/rezzminator/professor) - Fleet framework turning Claude Code, Codex & OpenCode agents into a disciplined engineering team: a Go CLI/TUI for cross-harness chat control and messaging, plus a discipline layer of agents/commands/hooks/rules compiled across all three runtimes.
```

PR title: `Add Professor to Development & Workflow`

PR body:

```
Adds Professor, a fleet coordination and discipline-layer framework spanning Claude Code, Codex & OpenCode. One-line alphabetical entry per the list's format; no Scanner CI configured on our side yet, so it would land without the elevated trust badge for now.
```

---

## Skipped

- **arvkonstantin/awesome-codex-cli** (and its several near-identical forks: RoggeOhta, fengzizz, vdlabdepot-web) — all are forks in a chain traced back to an unclear original, the fork checked (arvkonstantin) shows 0 stars on the fork itself and no verifiable independent maintenance signal beyond "forked from RoggeOhta/awesome-codex-cli." Could not identify a canonical, actively-maintained upstream to submit to with confidence; skipping rather than guessing which fork (if any) is the real destination.
- **Ischca/awesome-agents-md** (and its forks danielrosehill, cyberpatrolunit) — scoped specifically to AGENTS.md files/templates/guides, not to tools or frameworks; Professor doesn't ship a standalone AGENTS.md artifact meant for reuse elsewhere, it's a compiled mirror internal to the project, so this list is not a genuine fit.
- No list was rejected for being dead/archived — all 8 submitted-to lists above are active (most recent push within the last day to ~2 months).
