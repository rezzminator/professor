# e2b-dev/awesome-ai-agents — submission research

Repo: <https://github.com/e2b-dev/awesome-ai-agents> — 29,990 stars, default branch `main`, last push 2026-08-21T18:52:45Z. Maintained by the E2B team; one E2B staff member is the CODEOWNERS reviewer.

## 1. Mechanics — which file, format, ordering, CI

- **Single file to edit: `README.md`** (5,591 lines at time of research). No separate data file (no `agents.json`/`data/` dir); the README is a flat list rendered directly on GitHub.
- **No `CONTRIBUTING.md`.** Contribution rule lives inline in the README itself, under `## Have anything to add?` (line 55-56):
  > "Create a pull request or fill in this [form](https://forms.gle/UXQFCogLYrPFvfoUA). Please keep the alphabetical order and in the correct category."
- **No CI / lint workflow on PRs.** `.github/workflows` returns 404 (repo has no workflow files) — no automated check, no `.github/PULL_REQUEST_TEMPLATE.md` either (`.github` dir itself 404s at root listing). The only automation is a **CLA bot** and an **email/identity bot**, both comment-only (see §3).
- **Entry format** (copied verbatim from the two neighbouring entries, `Pezzo` and `Private GPT`, README.md lines 2299-2344):

  ```
  ## [Name](https://project-url)
  One-line tagline

  <details>

  ![image](optional-logo-url)

  ### Category
  Comma, separated, tags

  ### Description
  Prose description, optionally with a sub-bullet list

  ### Links
  - [GitHub](https://github.com/...)
  - [Documentation](https://...)

  </details>
  ```

  The `![image]` logo line is common but not universal (some entries omit it). `### Category` and `### Description` are present in every entry sampled; `### Links` closes the block before `</details>`.
- **Ordering:** strictly alphabetical by entry name, within one of exactly two top-level sections (`# Open-source projects` at line 71, `# Closed-source projects and companies` at line 2964). There are no sub-category sections in the README structure itself — "Category" is just a tag field inside each entry, not a heading.
- **Two other required steps found only in PR comments, not in the README:**
  1. **CLA required.** A bot (`cla-bot[bot]`) blocks every PR from a contributor "not on file" with: *"We require contributors to sign our Contributor License Agreement... sign at <https://e2b.dev/docs/cla> ... comment '@cla-bot check'."* This fires on essentially every external PR.
  2. **Commit identity must resolve to a GitHub account** — a second bot blocks PRs whose commit author email isn't linked to a GitHub identity, with instructions to `git config --global user.email ...` matching a verified GitHub email.

## 2. Section and exact insertion point

Professor is open-source (MIT) → **`# Open-source projects`** section (README.md, heading at line 71).

Alphabetically, `Professor` sorts between `Private GPT` and `PromethAI`:

- **Immediately BEFORE** (README.md line 2325): `## [Private GPT](https://www.privategpt.io/)`
- **Immediately AFTER** (README.md line 2346): `## [PromethAI](https://github.com/topoteretes/PromethAI-Backend)`

Insert the new `## [Professor](...)` block between the closing `</details>` of the Private GPT entry (line 2344) and the blank line before `## [PromethAI]` (line 2346).

## 3. What others did — 8 recent add-a-project PRs read

| PR | Title | State | Created → closed/merged | Outcome / stated reason (quoted, <15 words) |
| --- | --- | --- | --- | --- |
| [#123](https://github.com/e2b-dev/awesome-ai-agents/pull/123) | Adding PraisonAI Low Code and Code AI Agents Framework | **MERGED** | 2025-01-09 → 2026-07-09 (≈6 months) | No comment; silently merged after long backlog wait — last content-adding PR merged. |
| [#1511](https://github.com/e2b-dev/awesome-ai-agents/pull/1511) | Add ENZO — self-hosted BYOK AI workspace | CLOSED, unmerged | 2026-09-06 → same day range | "We require contributors to sign our Contributor License Agreement... on file." (CLA bot) |
| [#1457](https://github.com/e2b-dev/awesome-ai-agents/pull/1457) | docs: add SandBase CLI agent | CLOSED, unmerged | 2026-08-17 → 2026-08-30 | Contributor disclosed conflict of interest: "Disclosure: I contribute to the project." — no maintainer merge followed. |
| [#1439](https://github.com/e2b-dev/awesome-ai-agents/pull/1439) | Add The Rookery — 26 free client-side tools | CLOSED, unmerged | 2026-08-27 → same day | CLA-bot block; contributor ran `@cla-bot check`, still not merged. |
| [#1431](https://github.com/e2b-dev/awesome-ai-agents/pull/1431) | Add OpenOutreach (Sales) | CLOSED, unmerged | 2026-09-02 | CLA-bot block only, no maintainer comment. |
| [#1430](https://github.com/e2b-dev/awesome-ai-agents/pull/1430) | Add foldkeep — context folding engine | CLOSED, unmerged | 2026-08-29 | CLA-bot block only, no follow-up. |
| [#1429](https://github.com/e2b-dev/awesome-ai-agents/pull/1429) | Add Persona | CLOSED, unmerged | 2026-08-27 | CLA-bot block only. |
| [#1428](https://github.com/e2b-dev/awesome-ai-agents/pull/1428) | Add Awareness to Open-source projects | CLOSED, unmerged | 2026-08-27 | Contributor self-closed: **"my error"** (broken/private link) and **"Awareness is a memory layer... not really a fit for this list."** — the one PR with an actual maintainer-style scope judgment, made by the submitter themselves. |
| [#1423](https://github.com/e2b-dev/awesome-ai-agents/pull/1423) | Add Xenon — Terminal AI coding agent | CLOSED, unmerged | 2026-09-03 | CLA-bot block; contributor signed CLA and re-checked, still closed unmerged. |
| [#1529](https://github.com/e2b-dev/awesome-ai-agents/pull/1529) | Add Symbio to the list | CLOSED, unmerged | 2026-09-07 | Blocked by the **identity/email bot**, not CLA: "could not parse the GitHub identity of... huy tran." |

**Patterns that correlate with merge:** essentially none observed in the live data — the sample of 10 has exactly **one merge** (#123), and it merged with no comment after a roughly six-month wait from PR creation to merge, well after the CLA/identity bots (if triggered) would have been satisfied. Every other read PR — CLA-signed or not — sits closed-unmerged or (in the 30 most-recent list separately pulled) simply open and unreviewed for weeks. There is no visible reviewer approval, label, or maintainer merge comment pattern to learn from; the only consistent gate is passing the CLA bot and the commit-identity bot, which is necessary but evidently not sufficient for a maintainer to actually merge.

## 4. Final entry text, verbatim, plus PR mechanics

**Entry to insert** (README.md, between line 2344 and 2346, formatted per §1):

```markdown
## [Professor](https://github.com/rezzminator/professor)
An LLM-harness fleet boost framework for Claude Code, Codex & OpenCode

<details>

### Category
Multi-agent, Developer tools

### Description
Professor turns your AI coding agents into a disciplined engineering team you can see, message, and hold to the rules.
- pfm: a Go CLI/TUI fleet picker across every AI coding chat on the machine, with a Limits dashboard and cosmos view
- Chats message each other via `pfm chat call` and an MCP server, and can reload onto another account while keeping history
- A discipline layer of agents/commands/hooks/rules for Claude Code, compiled to Codex and OpenCode mirrors
- Harvester: a document fetcher with a multi-rung fallback ladder, exposed over MCP

### Links
- [GitHub](https://github.com/rezzminator/professor)

</details>
```

**Branch name:** `docs/add-professor-to-awesome-ai-agents`

**Commit message:**

```
docs: add Professor to Open-source projects
```

**PR title:**

```
Add Professor to Open-source projects
```

**PR body:**

```
Adds Professor, an LLM-harness fleet framework for Claude Code, Codex & OpenCode: pfm (Go fleet CLI/TUI with cross-chat messaging and account-preserving reload), a discipline layer of agents/commands/hooks compiled across all three runtimes, and Harvester (a document fetcher over MCP). Inserted alphabetically between Private GPT and PromethAI. MIT licensed.
```

## 5. Risks specific to Professor

- **Star count (~4).** No explicit minimum star rule was found anywhere in the README or bot messages — unlike `hesreallyhim/awesome-claude-code`'s stated 100-star-or-14-days bar, this list states no threshold. Not a hard blocker on paper, but low stars give a time-starved maintainer no signal to prioritize the review.
- **Maintainer activity is the dominant risk.** The last successfully merged *content-adding* PR (#123) took ~6 months from open to merge and was itself queued from January 2025. The repo currently has 25+ open "Add X" PRs from the last 10 days alone (per the `gh pr list --state all` pull), essentially none reviewed. This looks like an unmaintained submission queue behind an actively-pushed README (last push 2026-08-21 was a docs/link-tracking change by an E2B staffer, not a content merge).
- **CLA + commit-identity gates are process risk, not judgment risk** — straightforward to clear (sign at <https://e2b.dev/docs/cla>, ensure commit author email matches a verified GitHub email) but must be done correctly before any human ever looks at the PR.
- **Scope fit is plausible but untested.** The README frames the list as "AI assistants and agents," redirecting SDKs/frameworks to a sister list (`awesome-sdks-for-ai-agents`). Professor is a fleet/discipline framework rather than a single agent; PR #1428 shows the one case in this sample where a submitter self-identified their tool as "not really a fit" for being infrastructure rather than an agent. Professor's `pfm` orchestration and chat-fleet layer arguably qualifies as agent infrastructure, but a maintainer applying the same lens as #1428 could push back.
- **No CI to pre-validate formatting** — a malformed entry (bad alphabetical placement, wrong heading depth) has no automated check to catch it before human review, and given the maintainer-review backlog, a rejected/needs-fix PR may simply never get a follow-up look.

## Gaps

None — all API/PR/README reads used above returned data; no unreadable page or failed lookup was hit during this research.
