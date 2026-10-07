---
name: github-marketing
description: Writes and audits repo discoverability — description, topics, README, social preview, community files, registry metadata, launch; "get this repo found", "more stars", "a better README". `/github-marketing [audit|write|launch] [owner/repo|path] [surface…]`, no mode = audit. Returns a gap table, drafted files or a launch plan.
argument-hint: "[audit|write|launch] [owner/repo|path] [description|topics|readme|preview|community|registry]"
---

# GitHub marketing

Arguments `$ARGUMENTS`: the mode (`audit` when absent); the target — `owner/repo` or a local checkout path, absent = the current directory's repo (`gh repo view --json nameWithOwner -q .nameWithOwner`); for `write`, the surfaces to limit it to — `description`, `topics`, `readme`, `preview`, `community`, `registry`, absent = all.

Evidence, source URLs and open questions behind every rule and limit here: `.professor/RR/github-repo-discoverability-adoption-2026-09-27.md`. Tiers: [DOC] the platform's own docs · [EMP] a study with data · [PRAC] practitioner canon · [FOLK] asserted, unproven. Every recommendation handed to the user carries its tier; a [FOLK] lever is never sold as fact.

## 1. Read the repo — every mode starts here

One call, `{repo}` = `owner/repo`:

```bash
gh repo view {repo} --json name,description,homepageUrl,repositoryTopics,licenseInfo,usesCustomOpenGraphImage,hasDiscussionsEnabled,latestRelease,isInOrganization,fundingLinks,stargazerCount,pushedAt,primaryLanguage || echo "FAILED: repo view"
gh api repos/{repo}/community/profile --jq '{health: .["health_percentage"], files: .files}' || echo "FAILED: community profile"
gh issue list -R {repo} --label "good first issue" --state open --limit 5 --json number,title || echo "FAILED: issue list"
```

Then the product: its manifest (`package.json`, `pyproject.toml`, `Cargo.toml`, `go.mod`, a formula, `Dockerfile`), entry points and current README — every sentence you write is a claim about this code. Without a local checkout, read a file with `gh api -H 'Accept: application/vnd.github.raw' repos/{repo}/contents/{path}`.

Keyword research — run for 3–5 phrases naming the PROBLEM a stranger types before knowing the name, never the brand:

```bash
gh api -X GET search/repositories -f q='{phrase}' -f sort=stars -f per_page=10 --jq '.items[] | [.full_name, .stargazers_count, .description, (.topics|join(","))] | @tsv'
```

A read that failed is UNREAD, with its error, in every later table — never a GAP. "Could not look" and "nothing there" are different verdicts.

## 2. The law

### Hard limits [DOC]

| Surface | Limit |
| --- | --- |
| Topics | ≤ 20; each ≤ 50 chars; lowercase letters, numbers, hyphens |
| Description | no documented cap; users report the API rejecting past 350 chars |
| Page title (observed) | `GitHub - owner/repo: {description} · GitHub` |
| Social preview | PNG/JPG/GIF under 1 MB; ≥ 640×320, 1280×640 best; transparent PNG allowed |
| README | read from `.github/`, then root, then `docs/`; truncated past 500 KiB; GitHub builds a heading ToC itself |
| Images/GIFs in markdown | ≤ 10 MB each |
| HTML in markdown | `align` survives, `style` is stripped; `<picture>` + `prefers-color-scheme` swaps dark/light images |
| Alerts | `> [!NOTE]` `[!TIP]` `[!IMPORTANT]` `[!WARNING]` `[!CAUTION]` |
| FUNDING.yml | `.github/` on the default branch; one handle per platform, ≤ 4 custom URLs |
| Default health files | a public `{owner}/.github` repo supplies CODE_OF_CONDUCT, CONTRIBUTING, FUNDING, issue/PR templates, SECURITY, SUPPORT to every repo lacking its own |
| Pinned repos | 6 per profile |

### How strangers find a repo

1. GitHub search [DOC]: default repo search matches only the name, the description and the topics; the README counts only under `in:readme`. These three fields carry GitHub discovery.
2. Trending [DOC]: stars, forks, commits, follows and pageviews weighted by recency, recomputed 8×/day into daily/weekly/monthly top 25 — launch activity concentrated into days beats the same activity spread over months.
3. Releases [EMP]: stars arrive right after creation and after each release.
4. Hacker News [EMP]: on average +121 stars in 24 h, +289 in a week; posting time matters (no study names the hour); the "Show HN" tag itself shows no advantage.
5. Curated topics [DOC]: an existing, widely used topic joins its `github.com/topics/{topic}` page; an invented one joins nothing.
6. `good first issue` [DOC]: GitHub surfaces labelled issues to newcomers.
7. Google [DOC+EMP]: the repo root is crawlable (secondary views are robots-blocked); README and About links are `nofollow`, so a docs site on GitHub Pages is the followable, linkable asset — point the Website field at it.
8. AI answers [DOC+EMP]: Google states AI Overviews need no special optimisation; LLMs recommend already-popular libraries; llms.txt shows no measured uptake. GEO is a by-product of popularity and plain, quotable text — promise nothing more.

Best-match ranking past field matching is undocumented; the one reverse-engineered hypothesis [FOLK] rewards the search term's SHARE of the About text, so padding dilutes it.

### What makes a visitor adopt

- The README answers what it does, why it is useful, how to start, where to get help [PRAC]. "What" is in 97% of READMEs, "Why" in 26% [EMP] — a sharp Why is the differentiator.
- Popular READMEs use lists, images and external links, and carry contribution guidelines [EMP].
- Build, coverage and dependency-freshness badges are reliable quality signals [EMP]; a vanity badge signals nothing.
- 73% of developers check the star count before adopting; organisation-owned repos outperform personal ones [EMP] — social proof counts, and only real proof survives (§ Never).

### Description formula

`{what it is} for {who / which problem} — {the differentiator}`, the primary search keyword in the first words (they follow `owner/repo:` in the page title), under 120 chars. This sentence is the canonical one-liner: reused verbatim as the README tagline, the social-preview subtitle and every registry summary, cut down only where a registry caps it (§ Registry metadata). [derived from DOC limits; no source tests the formula]

### Topics method

Fill all 20, in this order: category (`cli`, `library`, `framework`) → language → framework/runtime → problem domain (2–5 synonyms from § 1's keyword research) → audience/platform → integrations. Prefer the topics the leaders in § 1's research already carry — a widely used topic has a populated page. Every topic is true of the repo.

### README

Top to bottom; the first screen (hero through demo) decides the visit:

1. Hero, centred (`<div align="center">`): the logo through `<picture>` with dark and light sources (no logo → a wordmark of the name); `# Name` matching the repo and package name; the one-liner; one row of badges; one row of links (Docs · Quickstart · Discussions · Changelog).
2. Demo above the fold: a GIF ≤ 10 MB, or dark/light screenshots sized with `width=`. For a CLI, commit the `vhs` `.tape` script beside the GIF so it regenerates with the product.
3. Why: 3–6 bullets, each a benefit a stranger wants, never an internal feature name.
4. Install + Quickstart: one copy-paste block per ecosystem, ending at the first visible result; alternatives (other OSes, package managers) fold into `<details>`.
5. Usage and examples, then the link to the full docs.
6. Comparison with alternatives, only when every cell is verifiable.
7. Architecture as a `mermaid` block, when the design is a selling point.
8. Help and community: Discussions, SUPPORT, chat.
9. Contributing: link CONTRIBUTING and the `good first issue` filter.
10. License, last.

Headings carry the search phrases naturally — Google reads the rendered README [PRAC]. Alerts mark what a user must not miss. A star-history chart goes in only once the curve rises; a flat curve is anti-proof. Translations live in `README.{lang}.md`, linked from a language row under the hero. Decoration follows the project's tone and never pushes content off the first screen.

### Community and trust files

LICENSE as an unedited stock SPDX text (GitHub and pkg.go.dev detect only those), CONTRIBUTING, CODE_OF_CONDUCT, SECURITY, SUPPORT, issue templates (`.github/ISSUE_TEMPLATE/*.md` with `name:` and `about:`) and a PR template complete the Community Standards checklist (`health` in § 1). CITATION.cff for research code (a "Cite this repository" sidebar link); FUNDING.yml when the project takes money; Discussions on when questions will come; a few issues labelled `good first issue`.

### Registry metadata

The one-liner and the top topics, mirrored:

| Registry | Fields and caps |
| --- | --- |
| npm | `description`, `keywords` — both feed `npm search` |
| PyPI | `description` (one line), `readme` as `text/markdown`, valid classifiers (invalid ones are rejected), `[project.urls]` with the well-known labels: homepage, source, documentation, changelog, issues, funding |
| crates.io | `description` required; ≤ 5 `keywords` (≤ 20 ASCII chars: letters, digits, `_`, `-`, `+`); ≤ 5 `categories` from the official slugs |
| pkg.go.dev | fetched via the module proxy (`go get`, or Request on the site); an undetected license limits what it shows |
| Homebrew core | `desc` ≤ 80 chars — no leading article, not starting with the name, no full stop, no emoji; eligible at ≥ 30 forks, 30 watchers or 75 stars (self-submitted 90/90/225) and ≥ 30 days old |
| Docker Hub | description ≤ 100 chars; ≤ 3 categories |

### Launch sequence

1. Surfaces complete: name, description, 20 topics, Website, social preview, license, community files, README with demo.
2. Registries published with the mirrored metadata.
3. A tagged release with notes.
4. Several issues labelled `good first issue`.
5. Show HN: `Show HN: {Name} – {one-liner}`, a no-signup way to try it, the author answering in the comments; Show HN takes "things people can run", never a landing page or a blog post.
6. The communities where the audience lives, each under its own posted rules, read before drafting (no sitewide Reddit policy was confirmed); X / Bluesky / Mastodon — the most used promotion channel among top-100 repos [EMP].
7. Newsletters: console.dev (developer tools a person can try alone; pre-1.0 or beta), Changelog News (own work welcome; commercial products sponsor).
8. Awesome lists: one PR per relevant list, by that list's own CONTRIBUTING, human-reviewed (the main awesome index rejects fully AI-generated PRs).
9. Keep releasing with a CHANGELOG — recency feeds Trending.

## 3. Modes

### audit — read-only

One row per surface — `surface | now | verdict | fix`, verdict PASS, GAP or UNREAD — over: description, topics, Website, social preview, README (hero, demo, why, quickstart, help, contributing, license), license, community files, releases, `good first issue`, each registry the repo publishes to. Close with the top 3 fixes, "find" levers before "adopt" levers, each with its tier.

### write

Draft every requested surface by § 2, from § 1's reads:

- Local checkout: edit in place (README, community files, `.github/`, manifests, the `.tape`), left uncommitted.
- No checkout: drafts under `/tmp/{project}/github-marketing/{owner}-{repo}/`, `{project}` per root `CLAUDE.md` § Local.
- GitHub settings: print one `gh repo edit {repo} --description … --homepage … --add-topic …` (plus `--enable-discussions` when earned).
- Social preview: write `social-preview.svg` at 1280×640 (logo, name, one-liner, high contrast), render it to PNG with the first available of `rsvg-convert`, `magick`, or headless Chrome (`"{chrome}" --headless --hide-scrollbars --window-size=1280,640 --screenshot={abs}/social-preview.png file://{abs}/social-preview.svg`), confirm it is under 1 MB, and tell the user to upload it at Settings → General → Social preview (no `gh repo edit` flag sets it). No renderer → hand over the SVG and name the gap.

Report the files written, the `gh repo edit` line awaiting the user's yes, the preview's path and size, and every claim the code could not confirm, as questions.

### launch

The § 2 launch sequence fitted to the repo: the steps the audit shows done, then a draft for each open one — the Show HN title and first comment, one post per chosen community, newsletter pitches, and the awesome lists from `gh api -X GET search/repositories -f q='awesome {topic}' -f sort=stars` with each list's contribution rule and the drafted entry line. Every draft goes to the user to send.

## 4. Hand-off

Committing is gitter's (Phase COMMIT, exact paths). A push, a `gh repo edit`, a post or a PR to another repo runs only on the user's explicit ask in this turn. Target = this repo → `/dev verify templates` (the leak gate) before handing over.

## Never

GitHub's Acceptable Use Policy bans rank abuse; most fake-star repos get deleted and their long-term star gain falls [EMP]. Each of these costs the repo itself:

- Buying, trading or automating stars or follows.
- Soliciting upvotes, comments or submissions on HN or any vote-ranked site.
- A topic or keyword the repo does not earn; a description padded with keywords.
- Mass or fully AI-generated PRs to awesome lists; any Hacktoberfest-style spam PR.
- A claim — feature, benchmark, user count, logo wall, testimonial, comparison cell — the code or a public source does not back. What cannot be confirmed goes to the user as a question, never into the README.
