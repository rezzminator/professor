# RR — Who is Tomas Vykruta (@tvykruta), where do his 2026-09-30 "design principles as hard rules for Claude Code" thread's claims come from, and does external evidence support them?

Question: GOAL: produce a research map answering who Tomas Vykruta (@tvykruta on X) is, where his 2026-09-30 thread's claims come from, and whether external evidence supports them. (Q1 identity; Q2 primary sources, app, perch/Jev; Q3 principle lineage; Q4 external evidence on code health, context files, rule count, refactor-first; Q5 reception.)

## Answer

Tomas Vykruta co-founded EvolutionIQ, and CCC's own SEC-filed release names him "co-founder and president" in the deal CCC agreed to buy for $730 million on December 20, 2024 ([SEC 8-K Ex. 99.1](https://www.sec.gov/Archives/edgar/data/1818201/000119312524283677/d919182dex991.htm)). That deal closed in January 2025 ([closing 8-K Ex. 99.1](https://www.sec.gov/Archives/edgar/data/1818201/000119312525002624/d900396dex991.htm)). The thread's headline numbers ("$7 → $1 per PR. 9× fewer tokens") exist only in his own post ([FxTwitter](https://api.fxtwitter.com/tvykruta/status/2105302045573959697)). The closest published research does not show gains anywhere near that size:
- Context files gave no general improvement in success and raised inference cost by over 20% ([Gloaguen et al.](https://arxiv.org/html/2602.11988)).
- For Sonnet, refactoring broke healthy and unhealthy code at about the same rate ([CodeScene 2601.02200](https://arxiv.org/html/2601.02200)).
- Unhealthy code cost about 44–45% more input tokens, not 9× ([CodeScene 2608.18645](https://arxiv.org/html/2608.18645)).

His "10 design principles … from the '90s" are 13–15 items. Three of them date from 2004, 2015 and 2016 ([Shore](https://martinfowler.com/ieeeSoftware/failFast.pdf), [McKinley](https://mcfunley.com/choose-boring-technology), [tef](https://programmingisterrible.com/post/139222674273/write-code-that-is-easy-to-delete-not-easy-to)).

## Map

### Q1 — Identity

**EvolutionIQ role**
- CCC's announcement names him co-founder and president, and his two co-founders co-CEOs: "Tomas Vykruta, co-founder and president, EvolutionIQ." Also "Michael Saltzman, co-founder and co-CEO of EvolutionIQ" and "Jonathan Lewin, EvolutionIQ co-founder and co-CEO." ([SEC 8-K Ex. 99.1, 2024-12-20](https://www.sec.gov/Archives/edgar/data/1818201/000119312524283677/d919182dex991.htm))
- First Round Review calls him co-founder and CTO: "Tom gravitated towards tech, so he was also the CTO, overseeing the engineering, data science and machine learning parts." It also says that around 2022 "I started focusing more on just managing engineering and technology." ([First Round Review](https://review.firstround.com/evolutioniq-path-to-pmf/))
- theorg.com lists him as "Co-Founder & CTO" ([theorg.com](https://theorg.com/org/evolutioniq/org-chart/tom-vykruta)).
- His 2019 Medium post says "Introducing EvolutionIQ (formerly known as DeepFraud AI)" ([Medium](https://tom-vykes.medium.com/goodbye-google-hello-deepfraud-ai-7c8661835ee5)). SELF-REPORTED.

**The acquisition**
- Buyer, price and terms: "CCC is expected to acquire EvolutionIQ for $730 million to be paid for with a combination of approximately 40% CCCS common stock and 60% cash to be funded through cash on balance sheet and an additional $225 million term loan." The agreement was announced "December 20, 2024" ([SEC 8-K Ex. 99.1](https://www.sec.gov/Archives/edgar/data/1818201/000119312524283677/d919182dex991.htm)).
- Close: "Jan.6, 2025 … announced today that it has completed the previously announced acquisition of EvolutionIQ, Inc." ([closing 8-K Ex. 99.1](https://www.sec.gov/Archives/edgar/data/1818201/000119312525002624/d900396dex991.htm)).
- CCC's FY2024 10-K says: "In January 2025, we completed the acquisition, announced in December 2024, of EvolutionIQ, Inc." ([10-K](https://www.sec.gov/Archives/edgar/data/1818201/000095017025026848/cccs-20241231.htm))
- The bio's "$730M 1/2025" is therefore SUPPORTED: $730M is the announced price and January 2025 the close.
- The purchase consideration as booked in the 10-Q note was not quoted from any filing (see open rabbit holes).

**Google**
- His HN comment says "I left Google / Google X last year after an 8.5 year career as a staff engineer to start EvolutionIQ" ([HN Algolia](https://hn.algolia.com/api/v1/search?query=tvykruta)). SELF-REPORTED.
- His Medium post, dated Sep 2019, says "After 8.5 amazing years at @Google…" ([Medium](https://tom-vykes.medium.com/goodbye-google-hello-deepfraud-ai-7c8661835ee5)). SELF-REPORTED.
- Read together, the two posts put him at Google from about 2011 to 2019. That is an inference, and his start year is not stated on any page.
- boringbusinessnerd: "Tomas was previously a Staff Engineer at Google" ([boringbusinessnerd](https://www.boringbusinessnerd.com/investor/tomas-vykruta)).
- Insurance Business says he "served a stint at what became Waymo" ([Insurance Business](https://www.insurancebusinessmag.com/us/news/technology/product-showcase-whats-fueling-this-claims-guidance-platform-404427.aspx)).
- First Round says Jeff Dean picked him for "Machine Learning Ninjas" in 2010 ([First Round Review](https://review.firstround.com/evolutioniq-path-to-pmf/)). That fetch was a summary, so this fact is `unquoted`. Its 2010 date also sits oddly with the 8.5 years counted back from 2019.

**Microsoft**
- No independent page was found. NOT FOUND.
- His own account is relayed by a Korean digest of the thread: "Microsoft Xbox의 Advanced Technology Group에서 콘솔 게임 코드를 작성했고, PS2, PS3, Xbox용 상업 게임의 렌더링 엔진을 만들었다." That is: he wrote console game code at Xbox's Advanced Technology Group, and rendering engines for PS2, PS3 and Xbox games ([digest PR #229](https://patch-diff.githubusercontent.com/raw/eiaserinnys/seosoyoung-blog/pull/229.diff)). SELF-REPORTED.
- The dates "May 2008 – July 2010" come from a search summary only and are unverified.
- The C/C++ game-engine career itself is independently attested: MobyGames credits him on "The Suffering 2004 Senior Engine Programmers", "The Suffering: Ties That Bind 2005 Technology Lead Programmer", "Drakan: Order of the Flame 1999 Programming", and "6 games with Surreal Software, Inc." ([MobyGames](https://www.mobygames.com/person/9907/tomas-vykruta/)).

**Jeff Korn**
- The thread credits him, as relayed by the digest: "Google 시절 Jeff Korn 밑에서 C++ 코드를 작성하며 익힌 원칙을 Claude에게 주었다." That is: he gave Claude the principles he learned writing C++ under Jeff Korn at Google ([digest](https://patch-diff.githubusercontent.com/raw/eiaserinnys/seosoyoung-blog/pull/229.diff)). SELF-REPORTED.
- Google Research lists a Jeffrey L. Korn with the research areas "Anti abuse" and "Software engineering" ([research.google](https://research.google/people/jeffreykorn/)). That description is `unquoted`.
- The same page lists him as a co-author of "Zanzibar: Google's Consistent, Global Authorization System" ([research.google](https://research.google/people/jeffreykorn/)).
- No page ties this Korn to Vykruta or to C++: NOT FOUND.

**Since the exit**
- His X post: "I am writing a book on Vertical AI. It's my first book…" ([X, Feb 2025](https://x.com/tvykruta/status/1893279407579562182)). This comes from a search listing, so it is SELF-REPORTED and `unquoted` from a fetch.
- The app in the thread is unnamed. No new company and no investments were found: NOT FOUND.
- His bio says "austin", but his FxTwitter profile lists the location as "New York City" ([FxTwitter profile](https://api.fxtwitter.com/tvykruta)). DISPUTED, unresolved.

### Q2 — Primary sources

**The results thread**
- It is status 2105302045573959697, created "Wed Sep 30 14:21:36 +0000 2026" ([FxTwitter](https://api.fxtwitter.com/tvykruta/status/2105302045573959697)).
- The opening tweet contains "Yesterday I shared a breakthrough", "I took the 10 design principles I learned back in the '90s and turned them into hard rules for Claude Code", "My app is 100% AI-written with Opus", "one feature ate 5 days, 49 PRs and ~$640 in tokens" and "$7 → $1 per PR. 9× fewer tokens. Every PR since merged on the first try." (same page).
- A Thread Reader copy also exists ([threadreaderapp](https://threadreaderapp.com/thread/2105302045573959697.html)).

**Tweets 8–10**
- They were never posted. "원문 타래는 7번 다음이 11번이다. 11번 트윗은 7번 트윗에 직접 단 답글이라, 8번부터 10번까지는 게시된 적이 없다." That is: the original thread goes from 7 to 11, and tweet 11 is a direct reply to tweet 7 ([digest](https://patch-diff.githubusercontent.com/raw/eiaserinnys/seosoyoung-blog/pull/229.diff)).
- This is corroborated by the Thread Reader copy jumping from 7 to 11 ([threadreaderapp](https://threadreaderapp.com/thread/2105302045573959697.html)), which is `unquoted`.

**The "breakthrough" post**
- It is not from 09-29 but status 2105122130908074219, created "Wed Sep 30 02:26:41 +0000 2026" ([FxTwitter](https://api.fxtwitter.com/tvykruta/status/2105122130908074219)). The digest gives the same time: "9월 30일 2시 26분(UTC)에 다른 타래를 올렸다." ([digest](https://patch-diff.githubusercontent.com/raw/eiaserinnys/seosoyoung-blog/pull/229.diff)).
- It opens: "I spent years in C and C++ at Microsoft and Google and architected 100s of systems and reviewed 1000s. I smell bad code the way some people smell smoke."
- It also says: "I saw the same Spaghetti code lazy programmers have always written — Claude just writes it 200x faster."
- It ends: "4/ Fix has been here since 1994. Anyone recognize this? 5/ Steal — paste into `AGENTS.md`."
- Counts: views 191,859; likes 1,825; replies 78; quotes 19 ([FxTwitter](https://api.fxtwitter.com/tvykruta/status/2105122130908074219)).

**The app**
- NOT FOUND. The digest notes: "타래에 앱의 이름과 용도는 나오지 않는다." That is: the thread names neither the app nor its purpose ([digest](https://patch-diff.githubusercontent.com/raw/eiaserinnys/seosoyoung-blog/pull/229.diff)).
- That it is a breeding or collectibles market is an inference from the AGENTS.md domains (pricing, valuation, "pedigree reading"), and it is unverified.

**Repo and AGENTS.md**
- github.com/tvykruta holds six old public repos and no AGENTS.md ([GitHub](https://github.com/tvykruta)). That is a fetch summary, so it is `unquoted`.
- The AGENTS.md is public only as tweet text (tweet 13 and the breakthrough post).

**Measurement write-up**
- NOT FOUND, beyond the tweet's own "measured, not estimated" claim.

**perch and Jev**
- The perch README opens "Semantic code linting with Jev." ([perch README](https://raw.githubusercontent.com/lakeday-org/perch/main/README.md)).
- TypeSafe AI describes Jev: "Jev is TypeSafe's first public System One Model, optimized for automation." ([typesafe.ai](https://typesafe.ai)).
- No page links perch or TypeSafe to Vykruta. @joshuafbrown's reply and its link were NOT FOUND, so whether he points to this repo is unverified.

### Q3 — Lineage of the listed principles

| Principle | Originator, year | Source |
|---|---|---|
| Separation of concerns | Dijkstra, EWD447 (1974) — "'the separation of concerns'… the only available technique for effective ordering of one's thoughts" | [EWD447](https://www.cs.utexas.edu/~EWD/transcriptions/EWD04xx/EWD447.html) |
| Encapsulation / information hiding | Parnas, 1972 — "first described by David Parnas in 1972"; paper "Copyright @ 1972" | [Wikipedia](https://en.wikipedia.org/wiki/Information_hiding), [CACM paper](https://doi.org/10.1145/361598.361623) |
| Cohesion / coupling | Constantine, late 1960s; Stevens, Myers, Constantine, IBM Systems Journal, June 1974 | [Wikipedia](https://en.wikipedia.org/wiki/Cohesion_(computer_science)) |
| DRY | Hunt & Thomas, *The Pragmatic Programmer*, 1999 (`unquoted` year) | [Wikipedia](https://en.wikipedia.org/wiki/Don%27t_repeat_yourself) |
| KISS | "reportedly coined by Kelly Johnson" of Lockheed Skunk Works, about 1960 (`unquoted` year) | [Wikipedia](https://en.wikipedia.org/wiki/KISS_principle) |
| YAGNI | Beck and Hendrickson on the C3 project; earliest dated source Jeffries, "April 4, 1998" | [Fowler](https://martinfowler.com/bliki/Yagni.html), [Wikipedia](https://en.wikipedia.org/wiki/You_aren%27t_gonna_need_it) |
| Single responsibility | Martin: "In the late 1990s I tried to consolidate these notions into a principle". DISPUTED: Wikipedia cites his "2000 paper" | [Clean Coder](https://blog.cleancoder.com/uncle-bob/2014/05/08/SingleReponsibilityPrinciple.html), [Wikipedia SOLID](https://en.wikipedia.org/wiki/SOLID) |
| Depend on contracts | Dependency Inversion, Martin, C++ Report 1996 (the year is implied by the column dates, `unquoted`); Design by Contract, Meyer, "first described in various articles starting in 1986" | [DIP PDF](https://www.cs.utexas.edu/~downing/papers/DIP-1996.pdf), [Wikipedia DbC](https://en.wikipedia.org/wiki/Design_by_contract) |
| Composition over inheritance | Gamma et al., *Design Patterns*: "Favor object composition over class inheritance." "Published October 1994" | [Artima](https://www.artima.com/articles/design-principles-from-design-patterns), [Hillside](https://www.hillside.net/elements-of-reusable-object-oriented-software-book) |
| Open/closed | Meyer, *Object-Oriented Software Construction*, 1988; Martin's 1996 article | [Wikipedia](https://en.wikipedia.org/wiki/Open%E2%80%93closed_principle) |
| Law of Demeter | "proposed by Ian Holland at Northeastern University towards the end of 1987"; Lieberherr, Holland and Riel, OOPSLA '88 | [Wikipedia](https://en.wikipedia.org/wiki/Law_of_Demeter), [OOPSLA'88](http://www.ccs.neu.edu/research/demeter/papers/law-of-demeter/oopsla88-law-of-demeter.pdf) |
| Fail fast | Jim Shore, IEEE Software, Sep/Oct 2004 — "build your software to 'fail fast.'" | [Shore PDF](https://martinfowler.com/ieeeSoftware/failFast.pdf) |
| Optimize for deletion | tef, "Write code that is easy to delete, not easy to extend", 2016-02-13 | [programmingisterrible](https://programmingisterrible.com/post/139222674273/write-code-that-is-easy-to-delete-not-easy-to) |
| Boring tech | Dan McKinley, "Choose Boring Technology", 2015 (`unquoted` year) | [mcfunley.com](https://mcfunley.com/choose-boring-technology) |

**His "only where change has happened twice"**
- It resembles the Rule of Three, which was "popularised by Martin Fowler in Refactoring and attributed to Don Roberts" ([Wikipedia](https://en.wikipedia.org/wiki/Rule_of_three_(computer_programming))). No source links the two.

**Count**
- He lists 13 items. Split into separate principles they make 15: cohesion/coupling and KISS/YAGNI each count as two.
- On top of those come "refactor first, then change" and "Gates, not promises". The claimed number is 10.

**Postdating the '90s**
- Three are flagged: fail fast (2004), boring tech (2015) and optimize for deletion (2016).
- SRP is borderline (Martin says late 1990s; others cite 2000).
- Most of the rest predate the 1990s: KISS (about 1960), information hiding (1972), separation of concerns (1974), cohesion/coupling (1974), Demeter (1987) and open/closed (1988).
- Only YAGNI (1998), DRY (1999), composition over inheritance (1994) and DIP (1996) are from the '90s.
- His own breakthrough post says "Fix has been here since 1994" ([FxTwitter](https://api.fxtwitter.com/tvykruta/status/2105122130908074219)). That year matches *Design Patterns*.

### Q4 — External evidence

**(a) Code health and duplication**
- Code Red: "Low quality code contains 15 times more defects than high quality code." Also: "Resolving issues in low quality code takes on average 124% more time in development." This is human-developer data ([arXiv 2203.04374](https://arxiv.org/abs/2203.04374)).
- CodeScene on LLM refactoring: "All five medium-sized LLMs have significantly lower break rates on healthy code, with relative risk reductions of about 15% for the weakest model and 30% for the strongest." But "For the SotA LLM Sonnet, there is no significant difference between Healthy and Unhealthy code", and "Claude breaks only about 5% of the test suites regardless of whether the code is Healthy or Unhealthy." ([arXiv 2601.02200](https://arxiv.org/html/2601.02200)). This is vendor-authored.
- CodeScene on test generation: "In the lowest CH interval, the median input-token count is 45.1% higher for Java and 43.8% higher for C++ compared with the near-perfect interval." Only Qwen3-30B was tested ([arXiv 2608.18645](https://arxiv.org/html/2608.18645)). This is vendor-authored.
- GitClear 2025: changed lines associated with refactoring "sunk from 25% of changed lines in 2021, to less than 10% in 2024, while lines classified as 'copy/pasted' (cloned) rose from 8.3% to 12.3%". The dataset is "211 million changed lines" ([GitClear 2025](https://www.gitclear.com/ai_assistant_code_quality_2025_research)). This is a correlational trend and does not measure agent cost.
- GitClear 2024: "approximately 153 million changed lines of code" ([GitClear 2024](https://www.gitclear.com/coding_on_copilot_data_shows_ais_downward_pressure_on_code_quality)).
- A primary study tying repository size, duplication or modularity to agent success or cost: NOT FOUND.

**(b) Context files (AGENTS.md / CLAUDE.md)**
- Gloaguen et al.: "providing context files does not generally improve task success rates, while increasing inference cost by over 20% on average."
- With LLM-generated files, "the average resolution rate is reduced by 0.5% and 2% on average on SWE-bench and CTXbench, respectively".
- With developer-written files: "Developer-provided context files improve agent performance by 2.4% on average (p=21%)".
- The authors also report that "instructions in context files are well followed" ([arXiv 2602.11988](https://arxiv.org/html/2602.11988)).
- Lulla et al. (JAWs 2026, Codex, 124 PRs): "the presence of AGENTS.md is associated with a lower median runtime (Δ28.64%) and reduced output token consumption (Δ16.58%), while maintaining a comparable task completion behavior" ([PDF](https://assets.empirical-software.engineering/pdf/jaws26-agents.md-efficiency.pdf)).
- DISPUTED on cost: Gloaguen finds cost up by over 20%, Lulla finds runtime and output tokens down. They used different agents, models and task sizes.
- Descriptive study: 2,303 context files from 1,925 repositories; developers prioritise "test procedures (75.9%), implementation details (70.8%), and architecture (68.1%)" ([arXiv 2511.12884](https://arxiv.org/abs/2511.12884)).

**(c) Rule count**
- IFScale: "even the best frontier models only achieve 68% accuracy at the max density of 500 instructions".
- At 500 instructions, claude-sonnet-4 scores 42.9% and claude-opus-4 44.6% (Table 1).
- It finds three decay patterns. Threshold decay: gemini-2.5-pro and o3. Linear decay: "gpt-4.1 and claude-3.7-sonnet". Exponential decay: "claude-3.5-haiku and llama-4-scout".
- It also finds primacy effects, "the tendency of models to better satisfy instructions appearing earlier" ([arXiv 2507.11538](https://arxiv.org/html/2507.11538)).
- His list of about 15–20 rules sits far below the densities tested there. Per-model figures at 10–20 instructions were not extracted.

**(d) Refactor first, then change**
- Kent Beck: "for each desired change, make the change easy (warning: this may be hard), then make the easy change", with created_at "2012-09-25" ([tweet syndication](https://cdn.syndication.twimg.com/tweet-result?id=250733358307500032&token=a)).
- Fowler quotes it in "Preparatory Refactoring", dated "05 January 2015", and frames work in two modes: "refactoring and adding function" ([martinfowler.com](https://martinfowler.com/articles/preparatory-refactoring-example.html)).
- The two-hats wording in *Refactoring* (1999) was not quoted from the book.
- An empirical study of refactor-first workflows with agents: NOT FOUND.
- The nearest work studies agents refactoring, not whether refactoring first helps a later change. Its "agentic efforts are dominated by low-level, consistency-oriented edits" with "small but statistically significant improvements in structural metrics" ([arXiv 2511.04824](https://arxiv.org/abs/2511.04824)).

**(e) "Gates, not promises" (CI or hooks over prompts)**
- TRACE "mines user corrections, rewrites them as atomic rules, and compiles them into runtime checks that must pass before an agent completes future tasks" ([arXiv 2606.13174](https://arxiv.org/abs/2606.13174)). Its compliance figures were not on the abstract page (see Verification).

**Verdicts per claim**

| Claim | Verdict | Reason |
|---|---|---|
| Founder of EvolutionIQ, acquired for $730M 1/2025 | SUPPORTED | SEC 8-K: co-founder and president, $730M announced 2024-12-20, closed 2025-01-06 |
| Years of C/C++ at Microsoft and Google | PLAUSIBLE-UNVERIFIABLE | Google 8.5 years and Xbox ATG are self-reported only; C++ game-engine credits 1999–2005 independently on MobyGames; Microsoft years NOT FOUND |
| Learned C++ "under Jeff Korn" | PLAUSIBLE-UNVERIFIABLE | A Jeffrey L. Korn is a Google researcher; no tie to Vykruta found |
| "10 design principles … from the '90s" | CONTRADICTED | He lists 13–15; three postdate 1999 (2004, 2015, 2016); most predate 1990 |
| $7 → $1 per PR (7× cheaper), 5× faster | PLAUSIBLE-UNVERIFIABLE | Self-reported, n=1, no data or write-up published; no external study approaches it |
| 9× fewer tokens | PLAUSIBLE-UNVERIFIABLE | Self-reported. Nearest evidence: about 44–45% more input tokens on unhealthy code (2608.18645, one model), roughly 1.45× rather than 9×. Lulla: −16.58% output tokens |
| Every PR merged first try | PLAUSIBLE-UNVERIFIABLE | Self-reported; the digest notes "규칙을 도입한 뒤의 기록은 아직 짧다" (the post-rules record is still short) |
| Messy or duplicated code makes AI agents worse | SUPPORTED for mid-size models; CONTRADICTED for Sonnet/Claude refactoring break rates | 2601.02200: 15–30% risk reduction for medium LLMs, "no significant difference" for Sonnet |
| AGENTS.md rules make the agent much better or cheaper | CONTRADICTED as to magnitude | Gloaguen: no general success gain, cost up over 20%; developer files +2.4%, not significant; Lulla: modest runtime and output-token cuts |
| Rules are followed reliably | PLAUSIBLE-UNVERIFIABLE | IFScale shows decay as instruction density rises, but his rule count is low; low-density figures not extracted |
| "A prompted 'never' alone does not protect … enforce by CI or a hook" | PLAUSIBLE-UNVERIFIABLE | TRACE compiles rules into runtime checks; its compliance numbers are unverified (NOT ON PAGE) |
| Refactor first, then change | SUPPORTED as lineage; empirical agent evidence NOT FOUND | Beck 2012, Fowler 2015; no agent study tests it |

### Q5 — Reception

- Engagement on the breakthrough post: 191,859 views, 1,825 likes, 78 replies, 19 quotes ([FxTwitter](https://api.fxtwitter.com/tvykruta/status/2105122130908074219)).
- Engagement on the results thread: 69,193 views, 657 likes, 18 replies, 3 quotes ([FxTwitter](https://api.fxtwitter.com/tvykruta/status/2105302045573959697)).
- Reply text was not retrievable, since FxTwitter returns no conversation data. @joshuafbrown's reply: NOT FOUND.
- The only external write-up is a Korean blog digest dated 2026-10-01 ([digest PR #229](https://patch-diff.githubusercontent.com/raw/eiaserinnys/seosoyoung-blog/pull/229.diff)). It raises its own caveats:
  - "PR 49개와 약 640달러로 계산하면 PR당 약 13달러다. 타래는 PR당 7달러를 어떤 범위의 PR로 산정했는지 밝히지 않았다." That is: 49 PRs at about $640 is about $13 per PR, and the thread does not say which PRs the $7 covers.
  - "규칙을 도입한 뒤의 기록은 아직 짧다." That is: the record after the rules is still short.
- Hacker News: the 12 hits under "tvykruta" are all 2019–2023 items, with no story on this thread ([HN Algolia](https://hn.algolia.com/api/v1/search?query=tvykruta)).
- Reddit, critiques and replications were searched and nothing was found. LinkedIn was searched with no related post found; its pages are blocked (HTTP 999).

## Coverage

- Q1 Identity: partial. Microsoft role and years independently NOT FOUND; Google years self-reported; CCC's booked consideration unquoted; Korn tie NOT FOUND.
- Q2 Primary sources: partial. Thread, breakthrough post and the 8–10 gap settled; app NOT FOUND; Brown's reply NOT FOUND.
- Q3 Principle lineage: settled.
- Q4a Code health and duplication: settled.
- Q4b+c Context files and rule count: settled.
- Q4d Refactor-first: settled for lineage; agent evidence NOT FOUND.
- Q5 Reception: partial. Reply text and LinkedIn unread.
- Digging ended: end of round 3 (safety ceiling).

## Verification

- 34 statements were checked on 8 pages: the SEC 8-K, both FxTwitter statuses, Gloaguen, IFScale, arXiv 2606.13174, Medium and the digest diff.
- NOT ON PAGE: arXiv 2606.13174's compliance figures, "31.6% (No Rules) / 55.0% (All Rules) / 70.1% (Compiled Rules)". The abstract page lacks them, so they are removed from the map.
- Corrected: IFScale's decay-pattern examples. The page gives "gpt-4.1 and claude-3.7-sonnet" (linear) and "claude-3.5-haiku and llama-4-scout" (exponential), not claude-sonnet-4 or gpt-4o as a digger had reported. The map uses the page's wording.
- UNCHECKED — HTTP 403: Medium "8.5 amazing years at @Google" and "formerly known as DeepFraud AI". The 8.5 years is also carried by his HN comment.

## Open rabbit holes

- CCC's booked EvolutionIQ purchase consideration: a search summary gives $674.3M total against the $730M headline. Read the 10-Q note or the XBRL R-pages. Dug, unsettled.
- Microsoft ATG dates ("May 2008 – July 2010", from a search summary only), and the Google start year (2010 per First Round vs about 2011 per 8.5 years). Try patents.google.com inventor listings and the "GPT-4 and Plugins" talk bio. Dug, unsettled.
- Jeff Korn's tie to Vykruta or to Google C++ readability. Dug, unsettled.
- Reply text on both statuses, and @joshuafbrown's reply and link. Dug, unsettled.
- The app's identity: try his other posts and bio links. Dug, unsettled.
- The news.hada.io topic (likely a Korean aggregator discussion of the thread). Undug.
- arXiv 2606.13174 full text, for its compliance figures. Undug.
- Harada et al., "Curse of Instructions" primary figures (OpenReview blocked), and arXiv 2509.21051. Dug, unsettled.
- arXiv 2607.02389 and 2607.26819, possible further evidence on constraints and hook compliance. Undug.
- The two-hats wording in Fowler's *Refactoring* (1999), and Beck as its source. Dug, unsettled.
- The SWE-bench snippet claiming repositories under 100 files see success "above twenty percent". Dug, unsettled (unverified snippet).
- CodeScene's "AI-Ready Code" whitepaper (its "at least 60%" claim): the PDF fetch failed. Dug, unsettled.
