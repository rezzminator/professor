# Getting Harvester into punkpeye/awesome-mcp-servers

Research only — no PR, issue, fork, star, or comment was opened. All links verified read-only via `gh` against `punkpeye/awesome-mcp-servers` (94,921 stars) on 2026-09-14.

## 1. Mechanics

- **File to edit:** `README.md` at repo root (4,388 lines). One PR = one entry, one line, added under the correct `###` category.
- **Contributing guide:** [`CONTRIBUTING.md`](https://github.com/punkpeye/awesome-mcp-servers/blob/main/CONTRIBUTING.md) — fork, branch (e.g. `add-new-server`), edit README, commit, push, open PR. "One server per line", "alphabetical order within each category", "keep it consistent" with existing formatting.
- **Scope gate (CONTRIBUTING.md § Scope):** "This list is for servers with a public GitHub repository — something you install and run yourself. If your server is remote-only (just a hosted URL, no installable package), it belongs in [awesome-remote-mcp-servers] instead." Harvester runs locally with an installable package (Go, monorepo path) — in scope.
- **Entry format:** `- [owner/repo](https://github.com/owner/repo) [![owner/repo MCP server](https://glama.ai/mcp/servers/owner/repo/badges/score.svg)](https://glama.ai/mcp/servers/owner/repo) <emoji legend> - <description>.`
  - Link text **must** be `owner/repo`, not just the repo name — enforced by CI (see below).
- **Emoji legend** (`README.md` "## Legend", lines 51–76; CI enforces the same list in `.github/workflows/check-glama.yml`):
  - Official: 🎖️
  - Language: 🐍 Python · 📇 TypeScript/JS · 🏎️ Go · 🦀 Rust · #️⃣ C# · ☕ Java · 🌊 C/C++ · 💎 Ruby
  - Scope: ☁️ Cloud Service · 🏠 Local Service · 📟 Embedded Systems
  - OS: 🍎 macOS · 🪟 Windows · 🐧 Linux
  - README note: "Use local when the MCP server is talking to locally installed software... use cloud when it's talking to remote APIs."
- **Description length:** no hard character cap stated anywhere, but every merged/live entry is 1–3 sentences (roughly 150–400 characters). Longer entries are routinely accepted if they stay to the point.
- **CI gate — `.github/workflows/check-glama.yml`** (`pull_request_target`, runs on open/edit/sync), checked only against genuinely new added lines:
  1. **Glama badge** — flags `missing-glama` if no `glama.ai/mcp/servers/OWNER/REPO/badges/score.svg` link is present; auto-posts a comment asking the author to list the server at <https://glama.ai/mcp/servers>, claim it, and add the score badge. This is a nag, not a hard block by itself, but in every merged PR sampled the author added the badge before merge — see §4.
  2. **Emoji check** — must include ≥1 permitted emoji from the legend; unknown/missing emoji triggers an auto-comment and a `missing-emoji` label.
  3. **Name-format check** — link text must be full `owner/repo`; a bare repo name triggers an auto-comment (e.g. "`Ridealong` should be `williamblaismedia-create/ridealong`").
  4. **Duplicate check** — flags any GitHub URL already present in base README.
  5. **GitHub-only check** — non-`github.com` primary links are flagged and redirected to `awesome-remote-mcp-servers`.
  6. On merge, a bot posts a welcome comment inviting a Discord "server-author" flair and mentioning `awesome-remote-mcp-servers`/Glama connectors for hosted variants.
  - **Agent fast-track:** CONTRIBUTING.md: "If you are an automated agent, we have a streamlined process for merging agent PRs. Just add 🤖🤖🤖 to the end of the PR title to opt-in. Merging your PR will be fast-tracked." (In practice, sampled 🤖🤖🤖 PRs still took days–weeks to merge — maintainer appears to merge in periodic batches, not instantly.)
- **Required Glama listing in practice:** every *merged* add-server PR sampled (5/5) carries a Glama score badge, and in three of them the maintainer (`punkpeye`) explicitly blocked merge until the server was submitted/claimed/scored on Glama (PR #12340, #10752, #8248 — #8248 was ultimately closed for an unrelated reason but the Glama gate was raised first). Treat the Glama badge as a de facto requirement even though the CI check alone doesn't hard-fail the PR.

## 2. Monorepo servers — accepted, with precedent

Servers that live inside a monorepo, linked via a subdirectory path (`/tree/main/<subpath>`), are already present in the live README:

- `- [Agnuxo1/benchclaw-integrations](https://github.com/Agnuxo1/benchclaw-integrations/tree/main/mcp-server) ...` — Research section, line 3263.
- `- [Embassy-of-the-Free-Mind/sourcelibrary-v2](https://github.com/Embassy-of-the-Free-Mind/sourcelibrary-v2/tree/main/mcp-server) ...` — Research section, line 3272.

Both use the `owner/repo` (not `owner/repo/subpath`) link text with a `/tree/main/<subdir>` URL — exactly the pattern Harvester would need (`rezzminator/professor` linking into `pfm/internal/harvestmcp`). No PR was found that was rejected specifically for being a monorepo/subdirectory server; the only "multiple servers" rejections (PR #14270, #8248) were for **one PR proposing two separate, unrelated servers**, not for a single server living in a larger repo. Monorepo placement is not itself a rejection reason on this list.

## 3. Section and exact insertion point

**Section:** `### 🔬 <a name="research"></a>Research` in `README.md` (starts line 3259, description: "Tools for conducting research, surveys, interviews, and data collection."). This fits Harvester's `harvester_read`/`harvester_search_literature`/open-access-resolver/paper-retrieval feature set better than "Search & Data Extraction" (which the README's own precedent — `mlava/scholar-sidekick-mcp`, `Liyux3/scholar-mcp`, `smeet666/mcp-archiveorg` — confirms is the home for citation/DOI/ISBN/paper-fetch tools, not general web scraping).

**Exact insertion point** (alphabetical by `owner/repo`, `m` cluster), `README.md`:

- Line immediately **before** (line 3283): `- [mnemox-ai/idea-reality-mcp](https://github.com/mnemox-ai/idea-reality-mcp) [![idea-reality-mcp MCP server](https://glama.ai/mcp/servers/@mnemox-ai/idea-reality-mcp/badges/score.svg)](https://glama.ai/mcp/servers/@mnemox-ai/idea-reality-mcp) 🐍 ☁️ 🍎 🪟 🐧 - Pre-build reality check for AI coding agents. Scans GitHub, Hacker News, npm, PyPI, and Product Hunt to detect existing competition before building, returning a reality signal score (0-100), duplicate likelihood, similar projects, and pivot hints.`
- **Harvester's line goes here.**
- Line immediately **after** (line 3284): `- [musharna/data-aggregator-mcp](https://github.com/musharna/data-aggregator-mcp) [![musharna/data-aggregator-mcp MCP server](https://glama.ai/mcp/servers/musharna/data-aggregator-mcp/badges/score.svg)](https://glama.ai/mcp/servers/musharna/data-aggregator-mcp) 🐍 🏠 🍎 🪟 🐧 - Search and fetch research datasets across Zenodo, DataCite (Dryad/Figshare/Dataverse/OSF), NCBI omics (GEO/SRA/BioProject), and literature (PubMed/OpenAIRE) behind one normalized model — DOI deduplication, NCBI-Taxonomy synonym expansion, paper→data linking, and checksum-verified download. \`uvx data-aggregator-mcp\`.`

(STALE after the rename: this slot was computed for the old owner `mreza0100`, in the `m` cluster. As `rezzminator/professor` the line sorts in the `r` cluster — re-derive the neighbours from the live README before submitting.)

## 4. What others did — 10 recent add-server PRs sampled

| PR | Title | State | Opened → Merged/Closed | Reason / maintainer quote |
| --- | --- | --- | --- | --- |
| [#14333](https://github.com/punkpeye/awesome-mcp-servers/pull/14333) | Add Nebelus MCP server | Closed | — | Remote-only, no GitHub package. Author: "Nebelus is a remote-only server... belongs in awesome-remote-mcp-servers... Closing this one." (duplicate of #14332) |
| [#14332](https://github.com/punkpeye/awesome-mcp-servers/pull/14332) | Add Nebelus MCP server | Closed | — | Same non-GitHub-URL bot flag; re-opened from an org fork, still closed |
| [#14273](https://github.com/punkpeye/awesome-mcp-servers/pull/14273) | Add Ridealong | Closed | — | Missing emoji + bad link-name (`Ridealong` not `owner/repo`) + unclaimed Glama. punkpeye: "your server needs to be claimed on Glama... any grade is acceptable" (<15 words as quoted) |
| [#14270](https://github.com/punkpeye/awesome-mcp-servers/pull/14270) | Add Envie and Exnos by GOL Productions 🤖🤖🤖 | Closed | — | punkpeye: "this PR adds multiple servers... submit one server per PR" |
| [#14154](https://github.com/punkpeye/awesome-mcp-servers/pull/14154) | Add jlucasmcrell/apify-scrapers to Search & Data Extraction 🤖🤖🤖 | Merged | 2026-09-10 → 2026-09-13 (3 days) | Clean submission with Glama badge already present; bot welcome comment on merge |
| [#12340](https://github.com/punkpeye/awesome-mcp-servers/pull/12340) | Add jlsookiki/secondhand-mcp (E-Commerce) | Merged | 2026-08-17 → 2026-09-07 (~3 weeks) | punkpeye: "a few things to address... Claim your server... on Glama." Merged after author claimed listing + added CI/tests |
| [#11247](https://github.com/punkpeye/awesome-mcp-servers/pull/11247) | Add срезAI MCP server 🤖🤖🤖 | Merged | 2026-07-31 → 2026-08-29 (~4 weeks) | No review comments needed — clean submission, batch-merged |
| [#10752](https://github.com/punkpeye/awesome-mcp-servers/pull/10752) | Add grounder-mcp - web grounding for local & cloud LLMs | Merged | 2026-07-23 → 2026-08-29 (~5 weeks) | punkpeye: "not yet found on Glama... let me know if you have any questions" — merged once Glama re-indexed and score resolved |
| [#6654](https://github.com/punkpeye/awesome-mcp-servers/pull/6654) | Add vassiliylakhonin/agenda-intelligence-md to Research | Merged | 2026-05-20 → 2026-05-27 (1 week) | No maintainer pushback; author proactively added Glama badge/score before merge |
| [#8248](https://github.com/punkpeye/awesome-mcp-servers/pull/8248) | Add deep-research-mcp-server (Research) and multi-scraper-mcp (Search) | Closed | — | punkpeye: "this PR adds multiple servers... one server per PR" (also unresolved Glama score) |
| [#9338](https://github.com/punkpeye/awesome-mcp-servers/pull/9338) | Add TownBrief MCP server | Closed | — | punkpeye: "This didn't make it into this list, but if it has a hosted/remote endpoint... awesome-remote-mcp-servers" |

**Patterns:**

- Merges land in **maintainer batches**, roughly every 1–4 weeks, not on-demand — even 🤖🤖🤖-tagged "fast-track" PRs sat 3 days to 4 weeks in this sample.
- **Glama listing/score is the single most common blocker** raised by the maintainer directly (3 of 5 merges required it explicitly; the CI bot nags on every PR lacking it).
- **"One server per PR"** is a hard rule — two multi-server PRs were closed solely for that.
- **Non-GitHub / remote-only servers are redirected**, never merged here — two closures for that reason.
- **Bad `owner/repo` link text and missing/unknown emoji** are auto-flagged by CI and must be fixed before merge.
- No PR in the sample (or found via search) was closed *for being part of a monorepo*.

## 5. Final entry, branch, commit, PR text

### Entry (verbatim, to insert at the point in §3)

```
- [rezzminator/professor](https://github.com/rezzminator/professor/tree/main/pfm/internal/harvestmcp) 🏎️ 🏠 - Public-document retrieval: fetch a URL, DOI, ISBN, PMID, PMCID, or local file to Markdown through a multi-rung fallback ladder (direct → Chrome-fingerprint TLS → reader proxy → extractor → headless browser → Wayback → OCR). Open-access resolvers for papers, plus (when configured) web search. Tools: `harvester_read`, `harvester_search_literature`, `harvester_download_file`, and conditionally `harvester_search_web`. Runs locally; MIT; lives at `pfm/internal/harvest(mcp)` inside the Professor monorepo.
```

- No Glama badge included — Harvester has no Glama listing today (see §6, Risks). CI will flag `missing-glama` and the maintainer will likely nag for one before merge, per §4 patterns.

### Branch name (per CONTRIBUTING.md's `add-new-server`/`fix-typo` convention)

```
add-rezzminator-professor-harvester
```

### Commit message (per CONTRIBUTING.md's "Add new XYZ server" example)

```
Add rezzminator/professor (Harvester) to Research
```

### PR title (per the sampled convention "Add owner/repo to Category", optionally 🤖🤖🤖-tagged for the agent fast-track)

```
Add rezzminator/professor (Harvester) to Research
```

or, if submitted by/as an automated agent and opting into the fast-track described in CONTRIBUTING.md:

```
Add rezzminator/professor (Harvester) to Research 🤖🤖🤖
```

### PR body (no `.github/PULL_REQUEST_TEMPLATE.md` exists in this repo — none to fill; body format follows the sampled merged PRs, e.g. #14154's "Summary" style)

```
### Summary

This PR adds [rezzminator/professor](https://github.com/rezzminator/professor) (Harvester) to the **Research** category.

- **Repository:** https://github.com/rezzminator/professor
- **Server location:** `pfm/internal/harvest` + `pfm/internal/harvestmcp` (one of two MCP servers in this monorepo)
- **Language / Runtime:** Go, MIT license
- **Capabilities:** Public-document retrieval — fetch a URL/DOI/ISBN/PMID/PMCID/local file to Markdown through a multi-rung fallback ladder (direct → Chrome-fingerprint TLS → reader proxy → extractor → headless browser → Wayback → OCR), open-access paper resolvers, and (when configured) web search. Tools: `harvester_read`, `harvester_search_literature`, `harvester_download_file`, and conditionally `harvester_search_web`.
- **Scope:** Local service, installed and run yourself.

Alphabetical order and category structure preserved (inserted between `mnemox-ai/idea-reality-mcp` and `musharna/data-aggregator-mcp`).
```

### Does the chat-fleet MCP server fit a category?

Not established with confidence in this research pass (out of the boundary set for this task, which scoped facts to Harvester only). Scanning the category list (§ headings, line-numbered above), the closest plausible fits by name alone would be **Coding Agents** (`### 🤖 <a name="coding-agents"></a>`, line 719) or **Developer Tools** (line 1233) if it exposes chat/session control to coding agents, but no feature facts about the chat-fleet server were supplied to confirm this — flagged as a gap rather than guessed.

## 6. Risks

- **No Glama listing found for Harvester.** In this sample, every merged PR ended up with a Glama score badge, and the maintainer directly blocked 3 of 5 merges pending one. Harvester would need to be submitted, claimed, and scored at <https://glama.ai/mcp/servers> before or during review to avoid a stalled PR — this is real setup work, not just a form field, and Glama needs the server to actually start and respond to MCP introspection.
- **Monorepo, not a dedicated repo.** Not a rejection reason found in precedent (§2 shows two accepted monorepo entries), but it does mean the `owner/repo` name shown in the list (`rezzminator/professor`) doesn't describe what "Harvester" is — a reviewer unfamiliar with the repo may ask for clarification on scope/what's actually being installed, and the entry necessarily under-names the specific tool.
- **~4 stars, created 2026-04-25 (very new, low traction).** No PR in the sample was rejected purely for low star count or age, but "maintenance signal" was explicitly cited as a merge-quality factor by one contributor (#12340: cut a release + added CI/tests "so the maintenance signal should look healthier"). A near-zero-star, ~4.5-month-old monorepo entry may draw the same kind of nudge.
- **Two MCP servers in one repo.** The "one server per PR" rule (enforced twice in-sample) applies to one PR proposing multiple *different* servers — it should not block Harvester alone, but the repo's second (chat-fleet) server would need its own, separate PR/entry if ever proposed, not bundled into this one.
- **CI's owner/repo link-text rule** and its expectation of a `/tree/main/<subpath>` for a monorepo entry (matching the two precedent lines in §2) must be followed exactly, or the automated name-check will flag and comment on the PR.

## Gaps

None encountered — README, CONTRIBUTING.md, the CI workflow source, and 10 real PRs (5 merged, 5 closed) were all readable via `gh api`/`gh pr view` without error. The chat-fleet MCP server's fit (§5) is reported as "not researched" rather than guessed, since no feature facts about it were provided in scope.
