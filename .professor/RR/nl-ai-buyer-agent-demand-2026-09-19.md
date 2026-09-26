# RR — Is there real demand for an AI home-buying assistant for buyers in the Netherlands?

Question: Goal: validate or kill the DEMAND for one product, with hard numbers. Return the saved report path first, then a cited answer ending in a plain verdict: strong / moderate / weak demand, and the evidence that would change it. The product: an AI buying assistant for home BUYERS in the Netherlands. (1) a chatbot interviews the buyer and works out what they want; (2) it watches new listings; (3) it researches each home (value, WOZ, energy label, neighbourhood, risks); (4) it emails "this one fits — shall I book a viewing?"; (5) on yes it books a viewing slot from the buyer's calendar with the selling makelaar. Evidence to collect: market size, scarcity of viewings, willingness to pay, competitor traction, search demand, international proof, counter-evidence — each with a number, a year and a source.

Research telemetry: 6 sub-agents dispatched across 2 waves, 6 reports received, 0 missing. Lead ran 7 additional searches and 4 fetches to close named gaps.

---

## Answer

**The PAIN is real and the money is real — Dutch buyers spend roughly EUR 220-400M a year on human buyer's agents against ~200,000 transactions, and only about a third of interested parties ever get a viewing. But the specific product as scoped is weakly supported: no Dutch buyer-side software company has published consumer scale, the one that tried a software-only model (Walter Living) went dark after 2020 and repriced into a human-style closing fee, the market is cooling on official forecasts, and the exact mechanic you lead with — an AI that books on your behalf — collides with 56% of Dutch consumers saying every AI-initiated purchase needs human confirmation.** Verdict: **moderate**, and it is moderate for the service, not for the software.

---

## 1. Market size

| Figure | Year | Source |
|---|---|---|
| 203,555 existing-home transactions, +~12% vs 2023 | 2024 | CBS [snippet, not page-verified] |
| ~63,000 existing homes sold in the quarter, +15.6% YoY, avg EUR 487,000 | Q3 2025 | [CBS](https://www.cbs.nl/nl-nl/nieuws/2026/03/meer-bestaande-koopwoningen-verkocht-minder-nieuwbouw) |
| 47,600 existing homes sold via NVM, +11% YoY — second-highest Q4 since 1995 | Q4 2025 | [NVM](https://www.nvm.nl/nieuws/2026/nvm-gemiddelde-transactieprijs-boven-half-miljoen-bij-meer-aanbod-en-vlotte-verkopen/) |
| ~34,600 NVM transactions, -27% vs Q4 2025 (seasonal) | Q1 2026 | [NVM](https://www.nvm.nl/nieuws/2026/nvm-woningmarkt-meer-in-balans-door-toename-aanbod-en-afvlakkende-prijzen/) |
| -6.4% YoY (Q3 2022), -6.9% YoY (Q3 2023), +18.6% YoY (Q4 2024) | 2022-2024 | [CBS](https://www.cbs.nl/nl-nl/nieuws/2026/03/meer-bestaande-koopwoningen-verkocht-minder-nieuwbouw) |
| Starters ~47% of NVM sales | Q4 2025 | [NVM](https://www.nvm.nl/nieuws/2026/nvm-gemiddelde-transactieprijs-boven-half-miljoen-bij-meer-aanbod-en-vlotte-verkopen/) |
| Avg transaction price EUR 502,000, +3.9% YoY | Q4 2025 | [NVM](https://www.nvm.nl/nieuws/2026/nvm-gemiddelde-transactieprijs-boven-half-miljoen-bij-meer-aanbod-en-vlotte-verkopen/) |
| 226,000 home sales forecast | 2027 | Rabobank [snippet — rabobank.nl returned HTTP 403, lookup failed] |

**Proxy for the searcher pool** (no direct active-searcher count was retrieved): Funda had 4.6M unique visitors/month and 837M visits in 2025, averaging 158 return visits per visitor per year, across 263,959 new listings — all [snippet]; pers.funda.nl and the jaarverslag page both returned CAPTCHA walls, so **lookup failed** on page-verification.

ESTIMATE (mine, arithmetic shown): 4.6M monthly uniques ÷ ~200,000 annual buyers ≈ **23 lookers per eventual buyer**. This overstates serious searchers badly — Funda is also browsed by neighbours, renters and price-watchers — so treat it only as an upper bound on the addressable audience.

**Not retrieved:** international/expat buyer share of purchases; a credible active-searcher headcount; average search duration; bids lost before winning. The first digger spent its search budget on transaction volume and never reached Vereniging Eigen Huis or the bank quarterlies. These are lookup gaps, not evidence of absence.

## 2. Scarcity of viewings — the strongest evidence in this report

- **10.3 physical viewings per home before it sells** — NVM Q1 2025 market analysis, via [kopenmetkennis.nl](https://kopenmetkennis.nl/hoeveel-bezichtigingen-krijgt-een-woning-gemiddeld-2025/)
- **Funda online reactions per listing, 2024: Flevoland 32, Utrecht 28, Noord-Holland 25, Zeeland 12** — Funda, [same page](https://kopenmetkennis.nl/hoeveel-bezichtigingen-krijgt-een-woning-gemiddeld-2025/)

ESTIMATE (mine — the rationing rate, the single best quantification of the pain). Arithmetic: Utrecht 10.3 viewings ÷ 28 online reactions = **37% of interested parties get through the door, ~63% do not**. Flevoland: 10.3 ÷ 32 = 32% get in, **~68% do not**. Caveat that matters: this divides a 2025 national NVM viewings figure by a 2024 provincial Funda reactions figure, and an "online reaction" is a weaker signal than a viewing request. Directional only — but even halving it leaves most interested buyers shut out.

Total viewing volume, ESTIMATE: 200,000 transactions × 10.3 = **~2.06M viewings a year** to be scheduled nationally.

**Is it still a bottleneck in 2026? Loosening, not loose:**

| Indicator | Q4 2025 | Q1 2026 | Q2 2026 |
|---|---|---|---|
| NVM krapte-indicator (national) | 1.9 | 2.6 | not verified |
| Days on market (national) | 28 | 32 | — |
| Sold above asking (national) | 72% | 66.7% | — |
| Avg overbid | +4.7% | +3.7% | — |

Sources: [NVM Q4 2025](https://www.nvm.nl/nieuws/2026/nvm-gemiddelde-transactieprijs-boven-half-miljoen-bij-meer-aanbod-en-vlotte-verkopen/), [NVM Q1 2026](https://www.nvm.nl/nieuws/2026/nvm-woningmarkt-meer-in-balans-door-toename-aanbod-en-afvlakkende-prijzen/). NVM's krapte-indicator counts how many suitable homes a searcher can choose from, so a **rising** number means a **looser** market; NVM treats ~5 as balanced. At 2.6 the market is still firmly seller-favourable.

Regional Q2 2026 (Haaglanden, [i4housing](https://www.i4housing.nl/woningmarkt-cijfers-2e-kwartaal-2026/)): Den Haag krapte 3.2 vs 2.5 a year earlier; Wassenaar 5.9 vs 4.9; 69.8% of Den Haag homes above asking; 31 days on market vs 28; new listings +37% QoQ; standing inventory +27% YoY. **Correction worth recording:** an early search snippet presented the 3.2 and 69.8% as national Q2 2026 figures — they are Den Haag. That page also labels rising krapte as "tightening", which inverts NVM's own definition.

- Only 32% of 2025 bidders received the biedlogboek after the sale — Vereniging Eigen Huis [snippet] ([vastgoedwereld.nl](https://vastgoedwereld.nl/nvm/nog-maar-32-krijgt-achteraf-biedlogboek-te-zien-veh-leg-biedproces-eerlijk-vast/)). Process opacity is a real, measured buyer grievance.

## 3. Willingness to pay

- Aankoopmakelaar usage climbing: **25%** (>5 yrs before the survey) → **35%** (3-5 yrs) → **45%** (H1 2021) — Radar/AVROTROS, 2021, n=12,000+ with purchase experience plus 1,757 active searchers ([avrotros.nl](https://radar.avrotros.nl/artikel/aankoopmakelaars-maken-zichzelf-onmisbaar-woningzoekende-betaalt-de-rekening-51177)). **This is consumer journalism, not official statistics, and it is five years stale.** No NVM, VEH or Kadaster adoption rate was retrieved — lookup gap.
- Share of users paying >= EUR 2,000: **20%** (2016-18) → **44%** (H1 2021) — same survey.
- ~1,000 VEH complaints about agents and the buying process in H1 2026 ([consumentenbond.nl](https://www.consumentenbond.nl/hypotheek/starter/aankoopmakelaar-nodig)) — denominator not established.
- Established by earlier passes (not re-researched): buyer's-agent fee averages **EUR 4,400** (Krib.nl, Feb 2026, **+13% YoY**).

ESTIMATE — the fee pool, arithmetic shown. At the stale 45% adoption: 200,000 × 0.45 × EUR 4,400 = **EUR 396M/year**. At a conservative 25%: 200,000 × 0.25 × EUR 4,400 = **EUR 220M/year**. Either way the buyer-side services market is in the **hundreds of millions of euros annually and its unit price is rising 13% a year** — this is the single strongest commercial fact in the report.

ESTIMATE — why subscription is the wrong shape. A EUR 29/month product captured by 1% of annual buyers over a 6-month average search: 2,000 × EUR 29 × 6 = **EUR 348,000/year**. The same 2,000 customers on a EUR 2,950 closing fee = **EUR 5.9M**. Seventeen times more. The money is in the transaction, not the subscription.

- Rentbird: "8,200+ users found a home" [snippet, company marketing]; rentbird.nl/over-ons returned 404 — **lookup failed**, no subscriber or revenue figure obtained. Stekkies is a **rental** aggregator, not a purchase-side proxy.

## 4. Competitor traction

**Walter Living** — the most informative case, because it is the closest analogue:
- ~**EUR 1M** raised by Nov 2019 from 15 anonymous informal investors; a Series A was aspirational and no closing was ever confirmed ([mtsprout.nl](https://mtsprout.nl/artikel/startups/dit-techbedrijf-berekent-huizenprijzen-minder-makelaars-dat-juichen-wij-toe), [Quotenet](https://www.quotenet.nl/zakelijk/a30728254/walter-living-steven-van-wel-makelaars-vereniging-amsterdam-jerry-wijnen/))
- **10 employees (8 FTE)**, Nov 2019; "thousands" of users, no exact count — same sources
- 2019-2020 pricing: free tier, **EUR 19.95** analysis, **EUR 24.95** Walter Plus, planned **EUR 299** Walter Offer — same sources
- **Nothing published after ~Feb 2020 could be retrieved.** app.dealroom.co returned HTTP 403 — **lookup failed**.
- Read against the current EUR 29/mo or EUR 2,950-at-closing pricing (established earlier), the model moved from **EUR 20-300 self-serve reports to a EUR 2,950 human-style closing fee**. That is an apparent pivot — inferred from comparing sources, not confirmed by a source that states it. **The self-serve software price point did not hold.** That is a direct warning for a software-first buyer assistant.

**Roofmatch** — the best evidence that Dutch buyers pay:
- Live pricing confirmed on its own site: **EUR 2,599** succesfee + EUR 299 startfee (Aankoop Pro), **EUR 3,599** + EUR 299 (Pro+) ([roofmatch.com](https://www.roofmatch.com/))
- **Trustpilot 4.7/5 from 1,760 reviews** — same page. ESTIMATE: at a 10-25% review rate that implies ~7,000-17,600 customers lifetime; at EUR 2,599 that is roughly **EUR 18-46M of lifetime revenue**. Very soft — the true review rate is unknown and reviews may include non-closing intakes.
- Founding year, funding, revenue and transaction count are **disclosed nowhere reachable** — searched "Roofmatch aankoopmakelaar oprichter financiering omzet", results were city landing pages only.

**Funda** (the incumbent that owns the demand): EUR **58.5M revenue in 2024, +22.2% YoY**; 4.6M monthly uniques; 837M visits in 2025 — **all [snippet]; both funda.nl source pages returned CAPTCHA walls, lookup failed.**

**Huispedia**: 2.5M monthly visitors, 7 staff, **bootstrapped with no external investors**, revenue from advertising, referral commissions and data sales — explicitly *not* transaction fees ([mtsprout.nl](https://mtsprout.nl/groei/dit-huizenplatform-daagt-funda-uit-door-minder-op-transacties-te-leunen)). This is **June 2020** data; current status not researched.

**HousApp**: EUR 4.3M seed, investors Arches Capital and Antler [snippet]. Note it is **makelaar-facing B2B**, not a buyer-side consumer product — adjacent, not analogous.

**Roof Radar, Homeup**: no published figures surfaced in any search run.

**Closures**: the only 2023-2026 Dutch housing-startup bankruptcy found was **Homes Factory** (Breda, prefab construction) [snippet, [vastgoedjournaal.nl](https://vastgoedjournaal.nl/news/72392/faillissement-treft-bredase-huizenfabriek-homes-factory)) — a manufacturer, not buyer-side software. No confirmed bankruptcy or distress exit for any NL buyer-side platform was found. That is a **search result, not a clean bill of health**: the sector's private companies simply do not publish.

## 5. Search demand

- National term "makelaar": **-38% search volume from 2020 to 2024** (Ahrefs, measured May 2020 vs May 2024); "makelaar + plaats" terms around Utrecht **-20%**; "verkoopmakelaar" **-36%** — [Odiv makelaarsonderzoek 2024](https://odiv.nl/makelaarsonderzoek-2024/)
- Krib.nl organic traffic grew 520 → **11,733** monthly visitors over four years; Trustoo.nl 93 → 383 — same source. Lead-gen comparison sites are taking the traffic that makelaar sites lost.
- Buyer-research intent is large and specific: **"woz waarde" 33,100 searches/month**, "woz waarde opvragen" 12,100, "woz waarde checken" 5,400, "woningwaarde berekenen" 3,600 — Semrush, **undated** ([seo-specialist.nl](https://seo-specialist.nl/zoekwoorden-makelaar/))
- Top five makelaar terms >51,000 searches/month; "makelaar Amsterdam" 4,400/month — both [snippet]
- **Not retrieved:** absolute volumes for "aankoopmakelaar", "funda alert", "bezichtiging aanvragen", "AI makelaar", and any Google Trends direction line for them. Named gap.

Read: demand for *finding an agent* is shrinking, demand for *home research data* (the WOZ/value cluster, ~57k/month on five terms alone) is substantial. That favours feature (3) of the product over feature (5).

## 6. International proof

- **Page-verified and the best signal available:** Redfin users on Sierra-built conversational search are **47% more likely to request a tour** and view **~2x as many listings** as filtered-search users ([sierra.ai/customers/redfin](https://sierra.ai/customers/redfin)). No date or period is given on the page, and it stops at "tour requested" — no booked-tour, conversion or revenue figure.
- **Zillow AI mode** launched 25 March 2026 ([zillow.com](https://www.zillow.com/news/zillow-debuts-ai-mode/), [inman.com](https://www.inman.com/2026/03/25/zillow-goes-ai-mode-with-new-home-search-assistant/)). The widely repeated "~5% of users adopted by May 2026" figure is **[snippet] and unverified** — the Inman article that carries it returned HTTP 403, **lookup failed**.
- **Homa** (US): launched April 2025, **USD 1,995 flat fee**, completed its **first** AI-assisted self-represented home sale around November 2025 in Florida; the article computes ~USD 10,005 saved on a USD 400,000 home ([realestatenews.com](https://www.realestatenews.com/2025/12/27/an-ai-driven-homebuying-model-is-picking-up-steam)). Funding and investors undisclosed. A competing [snippet] claims USD 24,000 saved and a 5 May 2025 launch — the sources disagree and neither reconciles it. **One confirmed transaction is not a market.**
- Reali's 2022 shutdown one year after raising USD 100M, and "proptech job postings down ~59%" (Inman, Sept 2026), were both **unverified — lookup failed** on the fetch attempts.
- reAlpha, Flyhomes, Propy and any UK/DE analogue were **not reached** within budget. Named gap.

## 7. Counter-evidence

**This is where the specific product is weakest.**

- **56% of Dutch consumers say every AI-agent purchase requires human confirmation; 67.9% want to be able to review or halt an AI's action before it executes; 52% associate AI shopping with loss of control; just over half would cap an autonomous AI assistant at EUR 50/month of spending authority** — Appinio, n=1,000 NL consumers, Dec 2025, via [Emerce](https://www.emerce.nl/wire/meer-helft-nederlanders-wantrouwt-ai-koper). This is general agentic commerce, not house-buying, so it is a proxy — but it lands squarely on step (5), the booking-on-your-behalf mechanic. Caveat: the Emerce piece is Riverty-sponsored, a payments company with an interest in this framing.
- **84% say humans must have the final say in important decisions; 73% trust an experienced human financial advisor more than an AI chatbot; national AI trust score 5.8/10** — KPMG Nationale AI Vertrouwensmonitor 2025 [snippet; the KPMG press-release URL returned 404, **lookup failed** on page-verification].
- **Honest counter-counter:** ~25% of Dutch think AI will largely replace financial advisors within ten years and **more than 1 in 3 say they would use AI for financial advice** — [accountant.nl](https://www.accountant.nl/nieuws/2025/11/kwart-van-nederlands-denkt-dat-ai-binnen-tien-jaar-financieel-adviseurs-grotendeels-vervangt/), [schade-magazine.nl](https://www.schade-magazine.nl/nieuws/archief/2025/11/ruim-1-op-de-3-nederlanders-zou-ai-gebruiken-voor-financieel-advies/12129), both Nov 2025. An early-adopter third exists.
- **Market cooling, official and dated:** ABN AMRO cut its 2026 transaction forecast to **-3%** (from -1%) and forecasts **-4% for 2027**; price growth +3% in 2026 and +4% in 2027; Q1 2026 new-build completions **-7.6% YoY** ([vastgoedactueel.nl](https://vastgoedactueel.nl/abn-amro-voorspelt-afkoelende-markt-met-minder-transacties/), 2 April 2026). Rabobank: prices +4.2% in 2026, +3.2% in 2027 [snippet, rabobank.nl 403]. ING ~+3.5% for 2026 [snippet]. Overbidding "is no longer automatic in a growing number of municipalities" [snippet].
- **The retention problem, quantified:** only **7% of owner-occupied homes change hands in a year** (versus 12-17% for rentals) — CBS, 2024 [snippet, [cbs.nl](https://www.cbs.nl/nl-nl/nieuws/2024/48/woningen-met-45-plussers-komen-minst-vaak-vrij)]. ESTIMATE: 1 ÷ 0.07 = **an average holding period of ~14 years**. Average woonduur is 5.2 years in Amsterdam, 7 in Haarlem, 14 in Volendam-Edam [snippet]. A buyer needs this product roughly **once every 5-14 years**. Churn is effectively 100% per transaction; there is no recurring-revenue business here, only a lead-generation-and-close business.
- **Makelaar resistance to third-party/automated viewing booking: COMPLETELY UNRESOLVED.** The digger's searches were blocked by a DuckDuckGo CAPTCHA and returned zero content, and no dedicated search was successfully run. Funda's terms on automated access, NVM rules on who may request a viewing, and any makelaar pushback are **unknown** — this is a lookup failure, not a finding that no resistance exists. Given that step (5) depends entirely on the selling agent accepting a bot-initiated booking, **this is the most important unanswered question in the report.**

---

## Verdict: MODERATE

Moderate, with a sharp split inside it:

- **Strong** for buyer-side purchase *help* as a paid service. EUR 220-400M/year fee pool (estimate, arithmetic above), average fee EUR 4,400 rising 13% YoY, ~200,000 transactions a year, roughly two-thirds of interested parties never getting a viewing, and Roofmatch sitting on 1,760 reviews at EUR 2,599-3,599 a deal.
- **Weak** for the product exactly as scoped. The subscription shape is off by ~17x versus a closing fee (arithmetic above) and is contradicted by a 7%/year move rate; the one Dutch company that tried self-serve buyer software at EUR 20-300 (Walter Living) repriced into a EUR 2,950 closing fee and published nothing after 2020; national search demand for "makelaar" fell 38%; and the autonomous-booking step runs into 56% of Dutch consumers demanding human confirmation of any AI purchase. International proof is one confirmed Homa transaction plus an engagement lift (Redfin's 47%) that stops short of a booking.
- The defensible wedge the evidence actually supports is features (2) and (3) — listing monitoring and automated home research, where the WOZ/value search cluster alone runs ~57,000 searches a month — monetised at the close, with the human confirming and, most likely, a human doing the final booking.

### The evidence that would change this verdict

**To strong:**
1. An official NVM / VEH / Kadaster aankoopmakelaar adoption rate for 2024-2026 confirming the stale 2021 45% figure still holds or has grown — that would firm up the fee pool from estimate to fact.
2. Roofmatch or a peer publishing transaction counts or revenue showing a fixed-fee buyer's-agent service scaling past a few thousand deals a year.
3. Evidence that selling makelaars accept third-party or automated viewing requests at a workable rate — the single most load-bearing unknown, and completely unretrieved here.
4. A Dutch survey specifically on AI-assisted home buying (not general agentic commerce) showing acceptance materially above the 56%/67.9% general-AI resistance.
5. Zillow's AI-mode adoption figure confirmed at or above 5% with a tours-booked number attached.

**To weak:**
1. The NVM krapte-indicator continuing past ~4 toward NVM's balanced level of 5, with above-asking share falling below ~50% — the rationing that creates the pain would be gone.
2. ABN AMRO's -3%/-4% transaction forecasts proving conservative, i.e. a sharper volume drop.
3. Confirmation that Walter Living shut down or was absorbed, or that Roofmatch's review base does not correspond to meaningful deal volume.
4. Any documented Funda or NVM policy blocking automated viewing requests — that would kill step (5) outright.

---

## Open questions (named lookup failures, not absences)

- **Makelaar acceptance of third-party/automated viewing bookings** — zero data retrieved; searches blocked by CAPTCHA. Most load-bearing gap in the report.
- Official aankoopmakelaar adoption share 2024-2026 — only the 2021 Radar survey was found; NVM/VEH/Kadaster not reached.
- Walter Living post-2020: funding, users, revenue, survival — Dealroom returned 403, nothing else published.
- Roofmatch founding year, funding, transaction volume — undisclosed on every reachable route.
- Funda's 4.6M uniques and EUR 58.5M 2024 revenue — snippet-level only; both funda.nl pages CAPTCHA-blocked.
- International/expat buyer share of NL purchases; active-searcher headcount; average search duration; bids lost before winning — never reached.
- Absolute Dutch search volumes for "aankoopmakelaar", "funda alert", "bezichtiging aanvragen", "AI makelaar"; no Google Trends direction line obtained.
- Zillow AI mode ~5% adoption (Inman 403); Reali's 2022 shutdown; "proptech job postings -59%" (Inman Sept 2026) — all unverified.
- Rentbird and Stekkies subscriber/revenue figures — Rentbird's about page 404s; Stekkies is rental-only and not a valid purchase-side proxy.
- Huispedia's current status — last data is June 2020.
- reAlpha, Flyhomes, Propy and UK/DE AI-buying analogues — not reached within budget.
