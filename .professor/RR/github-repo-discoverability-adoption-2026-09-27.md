# RR — What makes a public GitHub repository found and adopted by strangers

Question: map everything that makes a public GitHub repository FOUND and ADOPTED by strangers, as evidence a slash command (`/github-marketing`) will encode into rules for writing a repo's description, topics, README and every other discoverability/adoption surface. Scope: GitHub's own surfaces and exact limits; GitHub search ranking and Explore/Trending; Google/Bing SEO for a repo; GEO / AI discoverability; README craft (empirical studies + practitioner canon); package-registry metadata; launch and growth channels; anti-patterns and risks (fake stars, stuffing, star-begging, ToS). Output: hard-limits table, levers ranked by evidence strength, README skeleton, description formula and topic method, launch checklist, anti-patterns, open questions.

## Answer

Strangers find a repo mainly through three things. First, GitHub's own search, which by default matches only the **name, description and topics** (documented). Second, GitHub Trending, which weighs stars, forks, commits, follows and pageviews by how recent they are (documented). Third, outside pushes: Hacker News, awesome lists and registries, where one study measured an average of **121 stars in 24 hours** after HN exposure. Strangers adopt a repo when the README answers what, how, why and where to get help, uses lists, images and links, and carries reliable trust signals: CI, coverage and dependency badges, a license, contribution guidelines. Growth hacks backfire: fake stars cut organic star gain in the long run, most fake-star repos get deleted, and GitHub's Acceptable Use Policy bans "rank abuse".

## Map

Evidence labels: **[DOC]** first-party documentation or policy · **[EMP]** empirical study with data · **[PRAC]** practitioner consensus / canon · **[FOLK]** folklore (asserted, no primary evidence).

### 1. GitHub's own surfaces — hard limits table (date checked: 2026-09-27)

| Surface | Limit / behaviour | Source |
|---|---|---|
| Topics | "Add no more than 20 topics." · "Use 50 characters or less." · "Use lowercase letters, numbers, and hyphens." | [GitHub Docs — topics](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/classifying-your-repository-with-topics) |
| Description (About) | **UNCONFIRMED in GitHub docs.** The REST docs describe it only as "A short description of the repository." and the GraphQL docs as "A short description of the new repository.", with no number given. The error text reported by users: "Description cannot be more than 350 characters". The 128-char figure appeared only in a search snippet and is unverified. | [REST docs](https://docs.github.com/en/rest/repos/repos?apiVersion=2022-11-28), [GraphQL docs](https://docs.github.com/en/graphql/reference/repos#input-object-updaterepositoryinput), [MeshCentral #4724](https://github.com/Ylianst/MeshCentral/issues/4724) |
| Page `<title>` | `"GitHub - torvalds/linux: Linux kernel source tree · GitHub"`, i.e. `GitHub - owner/repo: description · GitHub` (unquoted, read from a live page by a digger) | [github.com/torvalds/linux](https://github.com/torvalds/linux) |
| Meta/embed fallback | When no description is shown, embeds read "Contribute to username/repo development by creating an account on GitHub." | [community #54372](https://github.com/orgs/community/discussions/54372) |
| Social preview | "PNG, JPG, or GIF file under 1 MB in size", "at least 640 by 320 pixels (1280 by 640 pixels for best display)". Absent: "repository links expand to show basic information about the repository and the owner's avatar". Transparent PNG is supported, which suits dark mode. | [GitHub Docs — social preview](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/customizing-your-repositorys-social-media-preview) |
| Auto OG image | "GitHub now generates OpenGraph images for public repositories, issues, commits, and pull requests" (2021). What the card shows: UNCONFIRMED | [changelog 2021-04-21](https://github.blog/changelog/2021-04-21-opengraph-images-for-github-repositories-commits-issues-and-pull-requests/) |
| README | Shown from `.github`, then root, then `docs`. "any content beyond 500 KiB will be truncated". Auto table of contents from headings; relative links are rewritten per branch. | [GitHub Docs — About READMEs](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/about-readmes) |
| Images/GIFs in markdown attachments | "10MB for images and gifs"; videos "10MB" (free plan) / "100MB" (paid); "25MB for all other files" | [GitHub Docs — attaching files](https://docs.github.com/en/get-started/writing-on-github/working-with-advanced-formatting/attaching-files) |
| Repo files | warning > 50 MiB; browser upload ≤ 25 MiB; "GitHub blocks files larger than 100 MiB" | [GitHub Docs — large files](https://docs.github.com/en/repositories/working-with-files/managing-large-files/about-large-files-on-github) |
| Pinned repos | Profile: "Select up to six repositories and gists, combined." Org: "up to six repositories for public users and six repositories for members" | [profile pins](https://docs.github.com/en/account-and-profile/setting-up-and-managing-your-github-profile/customizing-your-profile/pinning-items-to-your-profile), [org profile](https://docs.github.com/en/organizations/collaborating-with-groups-in-organizations/customizing-your-organizations-profile) |
| Org profile README | "In your organization's `.github` repository, create a `README.md` file in the `profile` folder." | [org profile](https://docs.github.com/en/organizations/collaborating-with-groups-in-organizations/customizing-your-organizations-profile) |
| FUNDING.yml | "one username, package name, or project name per external funding platform and up to four custom URLs". Lives in "`.github` folder, on the default branch". Platforms: GitHub Sponsors, Open Collective, Patreon, Ko-fi, Liberapay, Tidelift, Polar, Buy Me a Coffee, thanks.dev, IssueHunt, LFX Mentorship. | [GitHub Docs — sponsor button](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/displaying-a-sponsor-button-in-your-repository) |
| CITATION.cff | "a link is automatically added to the repository landing page in the right sidebar, with the label 'Cite this repository.'" (APA and BibTeX) | [GitHub Docs — CITATION](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/about-citation-files) |
| Community profile | "checks to see if a project includes recommended community health files, such as README, CODE_OF_CONDUCT, LICENSE, or CONTRIBUTING". Issue templates live in `.github/ISSUE_TEMPLATE` with `name:` and `about:` keys. | [GitHub Docs — community profiles](https://docs.github.com/en/communities/setting-up-your-project-for-healthy-contributions/about-community-profiles-for-public-repositories) |
| Default health files | Defaults possible for "CODE_OF_CONDUCT.md, CONTRIBUTING.md, Discussion category forms, FUNDING.yml, Issue and pull request templates and config.yml, SECURITY.md, SUPPORT.md" via a repo "called `.github`", which "must be public". GOVERNANCE is not on the list. | [GitHub Docs — default health file](https://docs.github.com/en/communities/setting-up-your-project-for-healthy-contributions/creating-a-default-community-health-file) |
| License | "The open source Ruby gem Licensee...compares the repository's *LICENSE* file to a short list of known licenses." "A shortened license name, linking to the repository's license file, is displayed on the repository page." Searchable via the `license:` qualifier. | [GitHub Docs — licensing](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/licensing-a-repository), [blog 2016](https://github.blog/2016-09-21-license-now-displayed-on-repository-overview/) |
| Releases | "Releases are based on Git tags"; release notes can be auto-generated | [GitHub Docs — releases](https://docs.github.com/en/repositories/releasing-projects-on-github/about-releases) |
| Discussions | "allows your project's community to engage in conversations about the project's direction and future in an open-ended format" | [GitHub Docs — Discussions](https://docs.github.com/en/discussions/collaborating-with-your-community-using-discussions/about-discussions) |
| Pages | `<owner>.github.io` (user/org site) and `<owner>.github.io/<repositoryname>` (project site) | [GitHub Docs — Pages](https://docs.github.com/en/pages/getting-started-with-github-pages/what-is-github-pages) |
| GFM alerts | `> [!NOTE]`, `[!TIP]`, `[!IMPORTANT]`, `[!WARNING]`, `[!CAUTION]`. "The `<picture>` HTML element is supported." Emoji via `:EMOJICODE:`. | [GitHub Docs — formatting](https://docs.github.com/en/get-started/writing-on-github/getting-started-with-writing-and-formatting-on-github/basic-writing-and-formatting-syntax) |
| Dark/light images | "specify whether to display images for light or dark themes in Markdown, using the HTML `<picture>` element in combination with the `prefers-color-scheme` media feature" (2022-05-19) | [changelog](https://github.blog/changelog/2022-05-19-specify-theme-context-for-images-in-markdown-beta/) |
| HTML sanitisation | `align` is on the allowlist; `style` is not (read from the sanitiser source, unquoted) | [html-pipeline sanitization_filter.rb](https://github.com/gjtorikian/html-pipeline/blob/main/lib/html_pipeline/sanitization_filter.rb) |
| i18n READMEs | No official convention; GitHub picks only `README.md` / `README.txt`. Names seen in the wild: `README_EN.md`, `README_CN.md`, `README.jp.md`. | [community #50719](https://github.com/orgs/community/discussions/50719) |

**Rules:**
- Set a description (it is searched and fills the page title).
- Fill all 20 topic slots with relevant topics.
- Upload a 1280×640 PNG under 1 MB as the social preview.
- Ship LICENSE, CONTRIBUTING, CODE_OF_CONDUCT, SECURITY and SUPPORT so the community checklist is complete.
- Add CITATION.cff for research code.
- Add FUNDING.yml when you accept money.
- Keep GIFs at or under 10 MB.
- Use `<picture>` for logos that must work in dark and light themes.
- Use `align="center"`, not `style=`.

### 2. GitHub search ranking, Explore and Trending

- **[DOC] Search fields.** "When you omit this qualifier, only the repository name, description, and topics are searched." `in:readme` must be asked for explicitly. Qualifiers exist for `stars:`, `topic:`, `topics:`, `good-first-issues:` and `license:`. [GitHub Docs — searching repositories](https://docs.github.com/en/search-github/searching-on-github/searching-for-repositories). The README body is therefore invisible to default repo search; name, description and topics carry the load.
- **[DOC] Sort options.** "Use the Sort dropdown menu to sort results by relevance, number of stars, number of forks, and how recently the items were updated." [sorting docs](https://docs.github.com/en/search-github/getting-started-with-searching-on-github/sorting-search-results). "We'll still sort by 'best match' by default" ([github.blog](https://github.blog/news-insights/sorting-through-search-results/)). No first-party source gives the best-match formula.
- **[FOLK/PRAC] Description word share.** One reverse-engineering hypothesis says best match rewards the share of search terms among the words of the About text: "cgm-remote-monitor" outranked "prometheus" despite "20x more stars". Single author, inference only. [markepear.dev](https://www.markepear.dev/blog/github-search-engine-optimization)
- **[DOC] Trending** (2013, updated 2019): "We look at a variety of data points including stars, forks, commits, follows, and pageviews, weighting them appropriately." "It's not just about total numbers, but also how recently the events happened." "Eight times a day we calculate trending data into three time buckets: daily, weekly, and monthly." "We want to surface just the top 25." [github.blog](https://github.blog/news-insights/company-news/explore-what-is-trending-on-github/). The claim that Trending is judged relative to the language and time window is community folklore ([#163970](https://github.com/orgs/community/discussions/163970)).
- **[DOC] Curated topic pages.** github/explore: topic `index.md` carries `aliases`, `display_name`, `short_description`, `topic`, `wikipedia_url`. "Valuable topics... include those that are already widely used by repositories". Contributors must avoid "conflicts of interest" and "self promotion". [CONTRIBUTING](https://github.com/github/explore/blob/main/CONTRIBUTING.md). Picking an existing curated topic joins its page; inventing a new topic does not.
- **[DOC] Good first issue.** "GitHub uses an algorithm to determine the most approachable issues in each repository and surface them in various places on GitHub", and "Adding the good first issue label can increase the likelihood that your issues are surfaced." [GitHub Docs — labels](https://docs.github.com/en/communities/setting-up-your-project-for-healthy-contributions/encouraging-helpful-contributions-to-your-project-with-labels)

**Rules:**
- Put the primary keyword in the repo name and the description.
- Use topics for the rest of the keywords.
- Don't rely on README text for GitHub search.
- Concentrate launch activity (stars, forks, commits) in time, because Trending weights recency.
- Label approachable issues `good first issue`.

### 3. External SEO and GEO

- **[DOC] Crawling.** GitHub's robots.txt disallows secondary views, e.g. `Disallow: /*/*/stargazers`, `Disallow: /*/tree/`, `Disallow: /*/raw/`, `Disallow: /search$`. The repo root (with its README) is not disallowed; that it is crawlable is an inference. [robots.txt](https://github.com/robots.txt)
- **[EMP, single test] Links.** Links in README, About and issues carry `rel="nofollow"`: "Every link had `rel=\"nofollow\"`" on n8n-io/n8n, and GitHub Pages sites were exempt. [dev.to test](https://dev.to/fhiltscher/your-readme-link-is-nofollow-i-checked-heres-the-script-319p). Google recommends marking UGC links with `ugc`/`nofollow` ([Google Search Central](https://developers.google.com/search/docs/crawling-indexing/qualify-outbound-links)). So README backlinks pass no link equity; a Pages or docs site is the linkable, followable asset.
- **[PRAC/FOLK] Names and backlinks.** "Your repository name is the weightiest ranking factor", the About text acts as the meta description, and awesome lists and registries are "authoritative backlinks". No data backs any of this ([digispot.ai](https://digispot.ai/blog/github-backlinks)).
- **[DOC] Google AI features.** "There are no additional requirements to appear in AI Overviews or AI Mode, nor other special optimizations necessary." "You don't need to create new machine readable files, AI text files, or markup to appear in these features." [Google Search Central](https://developers.google.com/search/docs/appearance/ai-features)
- **[DOC] llms.txt** is a proposal: "We propose adding a `/llms.txt` markdown file to websites to provide LLM-friendly content". Published 2024-09-03 and modified 2026-08-10. [llmstxt.org](https://llmstxt.org/). An Ahrefs sample of "137,210 domains" found "97% received zero requests". That figure comes via a secondary summary and is unquoted from Ahrefs itself ([dev.to](https://dev.to/swapbiswas/do-you-need-an-llmstxt-file-google-says-no-and-the-data-agrees-2026-45eb)).
- **[EMP] GitHub's share of AI citations.** github.com had "14,569" ChatGPT citations ("1.62%"). In Google AI Mode it ranked 9th with "8,728" ("1.48%"). Data: January–February 2026, US. [Similarweb](https://aisearch.similarweb.com/blog/most-cited-domains-llms/)
- **[EMP] Popularity bias in LLMs.** LLMs favour already-popular libraries: "NumPy is used in responses to 192 of the 305 tasks where it is not part of the ground-truth solution". [arXiv 2503.17181](https://arxiv.org/html/2503.17181). LLM recommendation follows existing popularity and training-corpus presence, which a README cannot directly move.
- **Thin evidence.** No source was found showing which README structure makes a repo more quotable by LLMs.

**Rules:**
- Treat GEO as a by-product of ordinary popularity and clear plain text.
- Don't promise llms.txt gains.
- Put a docs site on GitHub Pages when SEO matters, since its links are not nofollowed.
- Get listed in registries and awesome lists for referral traffic, not link equity.

### 4. README craft — evidence and canon

**Empirical studies:**
- **[EMP] Prana et al. (EMSE 2018)** identified 8 categories (What, Why, How, When, Who, References, Contribution, Other).
  - "97% of the files contain at least one section describing the 'What' of the repository and 88.5% offer some 'How' content".
  - Contribution appears in 27.8% of files and Why in 25.7% (Table 3).
  - Classifier F1 "0.746".
  - "The majority of participants (60%) indicated that the files with our labels made it easier to discover relevant information."
  - [ar5iv 1802.06997](https://ar5iv.labs.arxiv.org/html/1802.06997)
- **[EMP] Borges & Valente (JSS 2018)**, 791 responses:
  - "73% of the participants consider the number of stars before using or contributing to GitHub projects".
  - "More than half of the participants (52.5%) answered they starred the repositories because they liked the project".
  - Stars "might favor projects with successful marketing and advertising strategies".
  - [ar5iv 1811.07643](https://ar5iv.labs.arxiv.org/html/1811.07643)
- **[EMP] Borges, Hora & Valente (ICSME 2016)**:
  - "Repositories owned by organizations are more popular than the ones owned by individuals".
  - "Repositories receive more stars right after creation and after releases".
  - [slides](https://speakerdeck.com/aserg_ufmg/understanding-the-factors-that-impact-the-popularity-of-github-repositories-icsme-2016)
- **[EMP] Ikeda et al. (arXiv 2206.10772)**, "1950 readme files" across ten languages:
  - Popular projects' READMEs "are well organised using lists and images, and comprise links to external sources".
  - READMEs "containing contribution guidelines and references were observed to be associated with higher popularity".
  - [arXiv](https://arxiv.org/abs/2206.10772)
- **[EMP] Trockman et al. (ICSE 2018)**, 294,941 npm repos: "non-trivial badges, which display the build status, test coverage, and up-to-dateness of dependencies, are mostly reliable signals, correlating with more tests, better pull requests, and fresher dependencies". [researchr](https://conf.researchr.org/details/icse-2018/icse-2018-Technical-Papers/77/Adding-Sparkle-to-Social-Coding-An-Empirical-Study-of-Repository-Badges-in-the-npm-E) (unquoted: the page was not re-checked in verification)

**Practitioner canon:**
- **[PRAC] opensource.guide.** A README answers "What does this project do?", "Why is this project useful?", "How do I get started?", "Where can I get more help, if I need it?" [opensource.guide](https://opensource.guide/starting-a-project/)
- **[PRAC] standard-readme** section order: Title, Banner, Badges, Short Description, Long Description, Table of Contents, Security, Background, Install, Usage, Extra Sections, API, Maintainers, Thanks, Contributing, License.
  - Short description: "Must be less than 120 characters".
  - Table of contents: "Required; optional for READMEs shorter than 100 lines".
  - License: "Must be last section".
  - Title "must match repository, folder and package manager names".
  - [spec](https://github.com/RichardLitt/standard-readme/blob/main/spec.md)
- **[PRAC] makeareadme.com.** "Choose a self-explaining name", badges, and "include screenshots or even a video (you'll frequently see GIFs rather than actual videos)". [makeareadme](https://www.makeareadme.com/)
- **[PRAC] awesome-readme** teaches by example only: "images, screenshots, GIFs, text formatting". [awesome-readme](https://github.com/matiassingers/awesome-readme/blob/master/readme.md)

**Tooling:**
- shields.io static badges follow `https://img.shields.io/badge/{label}-{message}-{color}`, with `?style=` and `&logo=` ([shields.io](https://shields.io/docs/static-badges)).
- vhs renders `.tape` scripts to `out.gif`, `out.mp4` or `out.webm` ([vhs](https://github.com/charmbracelet/vhs/blob/main/README.md)).
- star-history calls itself "The de facto GitHub star history graph" ([star-history.com](https://star-history.com/)).

**[FOLK]** Claims that a demo video or GIF yields "meaningfully higher engagement" come from unsourced marketing ([repoclip.io](https://repoclip.io/blog/how-to-get-more-stars-on-github)). No study found says badge overload hurts.

**Recommended README skeleton (with evidence per section):**
1. **Title plus logo** via `<picture>` dark/light. Name matches the repo and package [PRAC standard-readme; DOC `<picture>`].
2. **One-line value proposition** of at most 120 chars, mirroring the About description [PRAC standard-readme]. The "What" is present in 97% of READMEs [EMP Prana].
3. **Badges, a few and reliable**: build, coverage, dependency freshness, version, license [EMP Trockman: these signal quality]. Overload is untested.
4. **Demo GIF or screenshot** of 10 MB or less [EMP Ikeda: images correlate with popularity; DOC 10 MB].
5. **Why / features** as a bulleted list [EMP Ikeda: lists; Prana: Why appears in only 25.7%, so it is a differentiator].
6. **Install + Quickstart**, copy-pasteable [PRAC opensource.guide "How do I get started?"; EMP Prana: How in 88.5%]. A cap of "≤ N lines" has no evidence; unverified.
7. **Usage / examples, then docs link** [EMP Ikeda: external links].
8. **Table of contents** only for READMEs over 100 lines (GitHub also auto-generates one) [PRAC; DOC].
9. **Getting help / community** (Discussions, SUPPORT) [PRAC opensource.guide].
10. **Contributing**, linking CONTRIBUTING and good-first-issues [EMP Ikeda: contribution guidelines correlate with popularity; DOC labels].
11. **License, last** [PRAC standard-readme; DOC licensee].

### 5. Package-registry metadata

| Registry | Field / limit | Source |
|---|---|---|
| npm | `keywords` "helps people discover your package as it's listed in `npm search`"; so does `description` | [npm docs](https://docs.npmjs.com/cli/v11/configuring-npm/package-json/) |
| npm search | Dec 2024: "By removing vague options – i.e. popularity, quality, maintenance – each sorting option is now straightforward and transparent" (now relevance, downloads, dependents, last published); the 2017 quality/popularity/maintenance scoring is gone | [github.blog changelog, 2024-12-03](https://github.blog/changelog/) (unquoted URL path; digger-cited) |
| PyPI | Summary: "A one-line summary of what the distribution does". Description-Content-Type: `text/plain`, `text/x-rst`, `text/markdown` (variant GFM/CommonMark). | [core metadata spec](https://packaging.python.org/en/latest/specifications/core-metadata/) |
| PyPI classifiers | "PyPI will always reject packages with classifiers beginning with `Private ::`". Invalid classifiers are rejected with "is an invalid value for Classifier". | [pypi.org/classifiers](https://pypi.org/classifiers/), [warehouse #3430](https://github.com/pypi/warehouse/issues/3430) |
| PyPI URLs | Well-known `[project.urls]` labels: homepage, source/repository, download, changelog, releasenotes, documentation/docs, issues, funding, security. Verification happens "when release files are uploaded". | [well-known URLs](https://packaging.python.org/en/latest/specifications/well-known-project-urls/), [PyPI docs](https://docs.pypi.org/project_metadata/) |
| crates.io | "a maximum of 5 keywords. Each keyword must be ASCII text, have at most 20 characters, start with an alphanumeric character, and only contain letters, numbers, `_`, `-` or `+`." "a maximum of 5 categories" matched exactly against category_slugs. "crates.io requires the `description` to be set." | [Cargo manifest](https://doc.rust-lang.org/cargo/reference/manifest.html) |
| pkg.go.dev | "Data for the site is downloaded from proxy.golang.org"; add a module via "Request" or `go get`. "Information for a given package or module may be limited if we are not able to detect a suitable license." | [pkg.go.dev/about](https://pkg.go.dev/about) |
| Homebrew core | "at least 30 forks, 30 watchers or 75 stars"; self-submission "at least 90 forks, 90 watchers or 225 stars". "A code repository less than 30 days old is normally not eligible." | [Package Acceptance Policy](https://docs.brew.sh/Package-Acceptance-Policy) |
| Homebrew `desc` | `MAX_DESC_LENGTH = 80`. The description "shouldn't start with an article", "should start with a capital letter", "shouldn't start with the #{type} name", "shouldn't end with a full stop", and "shouldn't contain Unicode emojis or symbols". | [DescHelper](https://docs.brew.sh/rubydoc/RuboCop/Cop/DescHelper.html) |
| Docker Hub | "The description can be a maximum of 100 characters long"; "up to three categories". Full README "limited to 25,000 bytes" according to third-party docs only; not stated by Docker. | [Docker docs](https://docs.docker.com/docker-hub/repos/manage/information/), [dockerhub-description](https://github.com/peter-evans/dockerhub-description/blob/main/README.md) |

**Rules:**
- Reuse one canonical one-liner everywhere.
- For Homebrew's 80-char limit: no leading article, no project name, no full stop.
- Mirror topics into registry keywords (npm keywords; up to 5 on crates.io).
- Fill PyPI `project_urls` with the well-known labels.
- Choose a license that licensee and pkg.go.dev detect.

### 6. Launch and growth channels

**Hacker News:**
- **[DOC] What qualifies.** Show HN is for "things people can run on their computers or hold in their hands". Off-topic: "blog posts, sign-up pages, newsletters, lists, and other reading material".
- **[DOC] Barriers.** "Please make it easy for users to try your thing out, ideally without barriers such as signups or emails."
- **[DOC] Voting.** "Please don't ask friends to upvote or comment. That's not ok on HN." [showhn](https://news.ycombinator.com/showhn.html). "Don't solicit upvotes, comments, or submissions." [guidelines](https://news.ycombinator.com/newsguidelines.html)
- **[DOC] Ranking.** "The basic algorithm divides points by a power of the time since a story was submitted". [newsfaq](https://news.ycombinator.com/newsfaq.html)
- **[EMP] Launch effect.** Kraishan (2025), 138 launches from 2024–2025: repos "gain an average of 121 stars within 24 hours, 189 stars within 48 hours, and 289 stars within a week". "the 'Show HN' tag shows no statistical advantage after controlling for other factors". "Posting timing appears as key factor" (the best hours are not given). [arXiv 2511.04453](https://arxiv.org/abs/2511.04453)
- **[EMP] Promotion channels.** Borges & Valente: Twitter was the most used channel (56 of the top-100 projects). On HN, "the projects covered by successful posts gained 74 stars in the first three days before... in the first three days after the publication, the projects gained 138 stars". [ar5iv 1908.04219](https://ar5iv.labs.arxiv.org/html/1908.04219) (unquoted: not re-checked)

**Awesome lists** ([template](https://github.com/sindresorhus/awesome/blob/main/pull_request_template.md), [create-list](https://github.com/sindresorhus/awesome/blob/main/create-list.md)):
- **[DOC]** "You have to review at least 4 other open pull requests."
- **[DOC]** "Fully AI-generated pull requests are not accepted."
- **[DOC]** Comment `unicorn` on the PR.
- **[DOC]** The list "Has been around for at least 30 days".
- **[DOC]** "Includes the Awesome badge".
- **[DOC]** "Run `awesome-lint`".

**Newsletters:**
- **[DOC] console.dev.** "an individual developer should be able to try it themselves without speaking to anyone". Release must be "pre 1.0 and/or" labelled beta. "Email hello@console.dev". [console.dev](https://console.dev/selection-criteria)
- **[DOC] Changelog News.** "Submitting your own work is also encouraged"; commercial products must sponsor. [changelog.com](https://changelog.com/news/submit)

**Other channels:**
- **[PRAC] Product Hunt.** One case: "#14 of the day with 193 votes... and 30 installs", "ten new GitHub stars". Anecdote. [infrasity](https://www.infrasity.com/blog/product-hunt-launch-for-developer-tools)
- **[DOC/self-reported] keepachangelog.** "Changelogs are for humans"; dates in `YYYY-MM-DD`. [keepachangelog 2.0.0](https://keepachangelog.com/en/2.0.0/)
- **Reddit.** No policy fetched. The "90/10 rule" and "Redditor with a website" line are snippet-only (see Open rabbit holes).

**Launch checklist (in order):**
1. Fill the surfaces: name, description, 20 topics, website, 1280×640 preview, license, community files, and a README with a demo.
2. Publish to the relevant registries with matching metadata.
3. Tag a first release. Stars spike "right after creation and after releases" [EMP ICSME 2016].
4. Label several `good first issue` items.
5. Show HN with a no-signup demo; never ask for upvotes.
6. Post to relevant communities within their rules, and to X / Bluesky / Mastodon (Twitter was the most used channel in the top-100 study).
7. Submit to a newsletter (console.dev if pre-1.0; Changelog News).
8. After 30 days of maturity, submit to the relevant awesome lists, one each and following each list's template.
9. Keep a release cadence and a CHANGELOG. This is recency for Trending [DOC].

### 7. Anti-patterns and risks

- **[EMP] StarScout** (arXiv 2412.13459 v2, "Six Million (Suspected) Fake Stars").
  - "18,617 repositories with fake star campaigns and 301k participating accounts (corresponding to 3.81 million fake stars)".
  - Fake stars are "≤1%" of all stars but "surged since 2024", with "16.66% of popular repositories (3,499)" affected in July 2024.
  - Effect: "in the long term, buying fake stars has a negative effect on star gain". The short-term effect is "about 5x smaller compared to real stars".
  - Enforcement: "90.42% of repositories and 57.07% of accounts in fake star campaigns... have been deleted".
  - [arXiv html](https://arxiv.org/html/2412.13459v2)
- **[DOC] GitHub Acceptable Use Policies** ban:
  - "inauthentic interactions, such as fake accounts and automated inauthentic activity";
  - "rank abuse, such as automated starring or following";
  - "creation of or participation in secondary markets for the purpose of the proliferation of inauthentic activity".
  - [AUP](https://docs.github.com/en/site-policy/acceptable-use-policies/github-acceptable-use-policies)
- **[EMP, journalism] Star market.** "1,000 fake GitHub stars for as little as $64"; "Only three-quarters of the fake Baddhi Shop stars remained" after a month. [Dagster](https://dagster.io/blog/fake-stars)
- **[DOC] Self-promotion in curation.** github/explore forbids "self promotion" and "conflicts of interest" in topic curation ([CONTRIBUTING](https://github.com/github/explore/blob/main/CONTRIBUTING.md)). HN forbids upvote solicitation (§6).
- **[EMP] Spam PRs.** Hacktoberfest 2020: "1,136" PRs explicitly tagged spam at filing time. DigitalOcean responded by letting maintainers opt out and banning persistent offenders. [The Register](https://www.theregister.com/2020/10/01/digitalocean_hacktoberfest_pull_request_spam/). The awesome template now rejects "Fully AI-generated pull requests".
- **Keyword stuffing / star-begging.** Searched for a GitHub policy against either, and nothing was found. Stuffing is folklore-risky: the only ranking hypothesis rewards term *share*, so padding dilutes it ([markepear.dev](https://www.markepear.dev/blog/github-search-engine-optimization)).

**Anti-patterns list:**
1. Buying or trading stars.
2. Automated follow/star.
3. Asking for HN upvotes.
4. Irrelevant or trending-but-unrelated topics.
5. Descriptions padded with keywords.
6. Mass or AI-generated awesome-list PRs, or PRs to lists younger than 30 days.
7. Hacktoberfest-style spam PRs.
8. Show HN behind a signup wall.
9. Badge walls of vanity badges (untested; only non-trivial badges are shown to be reliable signals).
10. A README past 500 KiB or a GIF over 10 MB.
11. `style=` HTML that gets stripped.

### Description formula and topic method (derived rules, from the evidence above)

- **Description:** `{what it is} for {who / problem} — {key differentiator}`.
  - Put the primary search keyword first, because it is searched and it fills `<title>` after "owner/repo:".
  - Reuse it verbatim as the README one-liner (under 120 characters, standard-readme), the registry summary (100 characters on Docker Hub) and the Homebrew `desc`.
  - A version trimmed to 80 characters for Homebrew: no leading article, doesn't start with the name, no full stop, no emoji.
  - Stay far below 350 characters (the limit is unconfirmed).
  - Derivation only; no source tests this formula.
- **Topics (20 slots, lowercase-hyphen, 50 characters or fewer):**
  - Prefer existing curated topics from github/explore that are "already widely used".
  - Fill in this order: category (e.g. `cli`), language (`go`), framework/runtime, problem domain (2–5 synonyms searchers use), audience/platform, ecosystem integrations.
  - Skip generic or irrelevant trending tags.
  - Mirror the top 5 into crates.io keywords and npm keywords.
  - Derivation; the exact weighting of topics in best match is undocumented.

## Coverage

- GitHub surfaces and limits: partial. The description limit is unconfirmed in GitHub docs (350 is the reported error text); the OG card contents are unconfirmed.
- GitHub search ranking / Explore / Trending: settled. The best-match formula is undocumented, and that is a stated finding.
- External SEO and GEO: partial. The literal meta description for a repo with a description is unconfirmed, and so is whether Pages outranks the repo; GEO evidence is thin.
- README craft: settled. The Wang et al. JSS 2023 fetch failed.
- Package-registry metadata: settled.
- Launch channels: partial. Reddit's policy text is unfetchable.
- Anti-patterns: settled.

Digging ended: round 4 settled no sub-area and added none; the remaining gaps are fetch-blocked or undocumented by GitHub.

## Verification

13 facts were checked on 12 pages (8 of them facts the answer rests on).
- Confirmed with quotes: the Trending mechanics (4 facts), the Prana figures (5), the Borges figures (4), Ikeda (3), Similarweb (3), the search fields and qualifiers (3), standard-readme (5), Kraishan HN (4), the awesome PR template (5), FUNDING.yml (4), the MeshCentral 350 error text, and the "Contribute to…" fallback.
- NOT ON PAGE: "Google search results show the description or its fallback as the snippet" (community #54372). This was removed from the map.
- NOT ON PAGE: "Borges & Valente give discovery-channel figures" (ar5iv 1811.07643). This was never a map fact.
- UNCHECKED: none.

## Open rabbit holes

- Official repository description max length in GitHub docs — dug, unsettled (REST, GraphQL and limits pages are silent).
- Literal `<meta name="description">` / `og:description` of a repo that has a description, and what the auto OG card shows — dug, unsettled.
- Whether a GitHub Pages / docs site outranks the repo, and canonical handling between README and docs — dug, unsettled.
- What README structure makes a repo cited by LLMs — dug, unsettled (no source).
- Primary Ahrefs llms.txt study — undug (seen only through a secondary summary).
- Reddit's official self-promotion policy (the 90/10 rule, "Redditor with a website") — dug, unsettled (every Reddit route blocked).
- Wang, Wang & Chen, JSS 2023, README vs popularity — dug, unsettled (403 on ScienceDirect and SSRN).
- Evidence that badge overload hurts — dug, unsettled (none found).
- Evidence for a "quickstart ≤ N lines" cap — undug.
- Best HN posting hours (Kraishan says timing matters but gives no hours) — dug, unsettled.
- Topic weight in best match — dug, unsettled (undocumented).
- Star-for-star exchange rings (as opposed to paid services) — undug.
- JavaScript Weekly / Golang Weekly / TLDR submission routes — dug, unsettled (pages not fetched).
- The 2024 npm search change — dug. The quoted sentence is from the December 2024 GitHub changelog, but its exact URL is not carried here; re-fetch to pin it.
